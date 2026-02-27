package conspiribot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CacheEntry struct {
	Name      string
	ExpiresAt time.Time
	Mu        sync.Mutex
	FailCount int
}

type SwarmState struct {
	DB                  *pgxpool.Pool
	DBQueue             chan func()
	Cancel              context.CancelFunc
	APIKey              string
	Config              *Config
	Limiter             *RateLimiter
	Wg                  *sync.WaitGroup
	Logger              *slog.Logger
	CacheRegistry       sync.Map // map[string]*CacheEntry
	ConsecutiveFailures int
	FailMu              sync.Mutex
}

func NewSwarmState(db *pgxpool.Pool, ctx context.Context, apiKey string, config *Config, logger *slog.Logger) *SwarmState {
	ctx, cancel := context.WithCancel(ctx)
	s := &SwarmState{
		DB:      db,
		DBQueue: make(chan func(), 100),
		Cancel:  cancel,
		APIKey:  apiKey,
		Config:  config,
		Limiter: NewRateLimiter(),
		Wg:      &sync.WaitGroup{},
		Logger:  logger,
	}
	s.Wg.Add(1)
	go s.worker(ctx)
	return s
}

func (s *SwarmState) worker(ctx context.Context) {
	defer s.Wg.Done()
	for {
		select {
		case <-ctx.Done():
			// Shutdown worker and drain queue linearly
			for {
				select {
				case job := <-s.DBQueue:
					job()
				default:
					return
				}
			}
		case job := <-s.DBQueue:
			job()
		}
	}
}

// Init initializes the Swarm, the PostgreSQL database and exports a SwarmState
func Init(ctx context.Context, pool *pgxpool.Pool, apiKey string, config *Config, logger *slog.Logger) (*SwarmState, error) {
	if _, err := pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector;`); err != nil {
		return nil, fmt.Errorf("failed to create vector extension: %w", err)
	}

	historySQL := `
	CREATE TABLE IF NOT EXISTS conspiri_history (
		id SERIAL PRIMARY KEY,
		timestamp TEXT,
		sender TEXT,
		message TEXT,
		channel TEXT DEFAULT ''
	);`
	if _, err := pool.Exec(ctx, historySQL); err != nil {
		return nil, err
	}

	reputationSQL := `
	CREATE TABLE IF NOT EXISTS conspiri_reputation (
		nick TEXT PRIMARY KEY, 
		score INTEGER DEFAULT 0, 
		notes TEXT
	);`
	if _, err := pool.Exec(ctx, reputationSQL); err != nil {
		return nil, err
	}

	memorySQL := `
	CREATE TABLE IF NOT EXISTS conspiri_memory (
		id SERIAL PRIMARY KEY,
		bot_nick TEXT,
		timestamp TIMESTAMPTZ DEFAULT NOW(),
		content TEXT,
		channel TEXT DEFAULT '',
		embedding vector(768)
	);`
	if _, err := pool.Exec(ctx, memorySQL); err != nil {
		return nil, err
	}

	summarySQL := `
	CREATE TABLE IF NOT EXISTS conspiri_memory_summaries (
		bot_nick TEXT PRIMARY KEY,
		updated_at TEXT,
		summary TEXT
	);`
	if _, err := pool.Exec(ctx, summarySQL); err != nil {
		return nil, err
	}

	userFactsSQL := `
	CREATE TABLE IF NOT EXISTS conspiri_user_facts (
		user_nick TEXT,
		fact TEXT,
		created_at TEXT,
		embedding vector(768),
		PRIMARY KEY (user_nick, fact)
	);`
	if _, err := pool.Exec(ctx, userFactsSQL); err != nil {
		return nil, err
	}

	urlCacheSQL := `
	CREATE TABLE IF NOT EXISTS conspiri_url_cache (
		url_hash TEXT PRIMARY KEY,
		title TEXT,
		fetched_at TEXT
	);`
	if _, err := pool.Exec(ctx, urlCacheSQL); err != nil {
		return nil, err
	}

	cacheStateSQL := `
	CREATE TABLE IF NOT EXISTS conspiri_cache_state (
		bot_nick TEXT PRIMARY KEY,
		cache_name TEXT,
		expires_at TIMESTAMPTZ
	);`
	if _, err := pool.Exec(ctx, cacheStateSQL); err != nil {
		return nil, err
	}

	logger.Info("Database initialized for conspiri.")

	state := NewSwarmState(pool, ctx, apiKey, config, logger)

	rows, err := pool.Query(ctx, `SELECT bot_nick, cache_name, expires_at FROM conspiri_cache_state`)
	if err == nil {
		for rows.Next() {
			var nick, cname string
			var expires time.Time
			if err := rows.Scan(&nick, &cname, &expires); err == nil {
				state.CacheRegistry.Store(nick, &CacheEntry{
					Name:      cname,
					ExpiresAt: expires,
				})
			}
		}
		rows.Close()
	}

	// Setup AppConfig global for legacy compatibility
	AppConfig = config

	if config != nil {
		scheduler := NewSpeakScheduler(ctx, time.Duration(config.Scheduler.IntervalSeconds)*time.Second, config.Scheduler.QueueSize)
		for _, p := range config.Bots {
			NewBot(state, p, scheduler)
		}
	}

	go state.memoryCleaner(ctx)

	return state, nil
}

func (s *SwarmState) memoryCleaner(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Initial run
	s.runCleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runCleanup(ctx)
		}
	}
}

func (s *SwarmState) runCleanup(ctx context.Context) {
	// Purge records older than 30 days
	res, err := s.DB.Exec(ctx, `DELETE FROM conspiri_memory WHERE timestamp < NOW() - INTERVAL '30 days'`)
	if err != nil {
		s.Logger.Error("Failed to purge old memory", "error", err)
	} else if res.RowsAffected() > 0 {
		s.Logger.Info("Purged old memory", "rows", res.RowsAffected())
	}

	// Check row count and create HNSW index if > 100,000
	var count int
	err = s.DB.QueryRow(ctx, `SELECT reltuples::bigint AS estimate FROM pg_class WHERE relname = 'conspiri_memory'`).Scan(&count)
	if err == nil && count > 100000 {
		_, err = s.DB.Exec(ctx, `CREATE INDEX IF NOT EXISTS conspiri_memory_embedding_idx ON conspiri_memory USING hnsw (embedding vector_cosine_ops)`)
		if err != nil {
			s.Logger.Error("Failed to create HNSW index", "error", err)
		} else {
			s.Logger.Info("Ensured HNSW index exists on conspiri_memory")
		}
	}
}

// float32ArrayToString converts array of float32 to PostgreSQL vector compatible string
func float32ArrayToString(arr []float32) string {
	var strs []string
	for _, f := range arr {
		strs = append(strs, fmt.Sprintf("%f", f))
	}
	return "[" + strings.Join(strs, ",") + "]"
}

// SaveMemory appends a memory entry for a bot
func SaveMemory(state *SwarmState, botNick, content, channel string) error {
	go func() {
		var embedBlob string
		key := state.APIKey
		if key != "" {
			embedding, err := GetEmbedding(key, content)
			if err == nil {
				embedBlob = float32ArrayToString(embedding)
			}
		}

		state.DBQueue <- func() {
			var last string
			err := state.DB.QueryRow(context.Background(), `SELECT content FROM conspiri_memory WHERE bot_nick = $1 AND channel = $2 ORDER BY id DESC LIMIT 1`, botNick, channel).Scan(&last)
			if err == nil {
				if strings.TrimSpace(last) == strings.TrimSpace(content) {
					return
				}
			}

			if embedBlob != "" {
				_, err = state.DB.Exec(context.Background(), `INSERT INTO conspiri_memory(bot_nick, timestamp, content, channel, embedding) VALUES($1, NOW(), $2, $3, $4::vector)`, botNick, content, channel, embedBlob)
			} else {
				_, err = state.DB.Exec(context.Background(), `INSERT INTO conspiri_memory(bot_nick, timestamp, content, channel) VALUES($1, NOW(), $2, $3)`, botNick, content, channel)
			}

			if err != nil {
				state.Logger.Error("SaveMemory error", "error", err)
				return
			}

			go func() {
				if cnt, _ := GetMemoryCount(state, botNick); cnt >= 20 {
					_ = SummarizeAndStore(state, botNick)
				}
			}()
		}
	}()
	return nil
}

// GetMemorySummary returns the stored summary plus recent memory entries for bot
func GetMemorySummary(state *SwarmState, botNick string, channel string, limit int, maxChars int) (string, error) {
	summary, _ := GetSummary(state, botNick)

	var rows pgx.Rows
	var err error
	if channel == "" {
		rows, err = state.DB.Query(context.Background(), `SELECT content FROM conspiri_memory WHERE bot_nick = $1 ORDER BY id DESC LIMIT $2`, botNick, limit)
	} else {
		rows, err = state.DB.Query(context.Background(), `SELECT content FROM conspiri_memory WHERE bot_nick = $1 AND channel = $2 ORDER BY id DESC LIMIT $3`, botNick, channel, limit)
	}
	if err != nil {
		return "", err
	}
	defer rows.Close()

	parts := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return "", err
		}
		parts = append(parts, c)
	}

	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}

	combined := strings.Join(parts, " \n")
	if summary != "" {
		combined = summary + "\n" + combined
	}

	if len(combined) > maxChars {
		combined = combined[len(combined)-maxChars:]
	}
	return combined, nil
}

func GetMemoryCount(state *SwarmState, botNick string) (int, error) {
	var cnt int
	err := state.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM conspiri_memory WHERE bot_nick = $1`, botNick).Scan(&cnt)
	if err != nil {
		return 0, err
	}
	return cnt, nil
}

func SaveSummary(state *SwarmState, botNick, summary string) error {
	state.DBQueue <- func() {
		_, err := state.DB.Exec(context.Background(), `INSERT INTO conspiri_memory_summaries(bot_nick, updated_at, summary) VALUES($1, NOW(), $2) ON CONFLICT(bot_nick) DO UPDATE SET updated_at = NOW(), summary = EXCLUDED.summary`, botNick, summary)
		if err != nil {
			state.Logger.Error("SaveSummary error", "error", err)
		}
	}
	return nil
}

func GetSummary(state *SwarmState, botNick string) (string, error) {
	var s string
	err := state.DB.QueryRow(context.Background(), `SELECT summary FROM conspiri_memory_summaries WHERE bot_nick = $1`, botNick).Scan(&s)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return s, nil
}

func SearchRelevantMemory(state *SwarmState, botNick, query string, limit int) (string, error) {
	key := state.APIKey
	var queryEmbedding []float32
	var err error

	if key != "" {
		queryEmbedding, err = GetEmbedding(key, query)
	}

	if len(queryEmbedding) == 0 || err != nil {
		words := strings.Fields(query)
		var keywords []string
		ignore := map[string]bool{"the": true, "and": true, "for": true, "that": true, "this": true, "with": true, "what": true}

		for _, w := range words {
			clean := strings.ToLower(strings.Trim(w, ".,?!-:;"))
			if len(clean) > 3 && !ignore[clean] {
				keywords = append(keywords, clean)
			}
		}

		if len(keywords) == 0 {
			return "", nil
		}

		var args []interface{}
		args = append(args, botNick)
		var conditions []string
		argID := 2
		for _, kw := range keywords {
			conditions = append(conditions, fmt.Sprintf("content ILIKE $%d", argID))
			args = append(args, "%"+kw+"%")
			argID++
		}
		args = append(args, limit)
		sqlStr := fmt.Sprintf("SELECT content FROM conspiri_memory WHERE bot_nick = $1 AND (%s) ORDER BY id DESC LIMIT $%d", strings.Join(conditions, " OR "), argID)

		rows, err := state.DB.Query(context.Background(), sqlStr, args...)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		var hits []string
		for rows.Next() {
			var c string
			rows.Scan(&c)
			hits = append(hits, c)
		}
		return strings.Join(hits, "\n"), nil
	}

	embedStr := float32ArrayToString(queryEmbedding)
	rows, err := state.DB.Query(context.Background(), `SELECT content FROM conspiri_memory WHERE bot_nick = $1 AND embedding IS NOT NULL ORDER BY embedding <=> $2::vector LIMIT $3`, botNick, embedStr, limit)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var hits []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err == nil {
			hits = append(hits, c)
		}
	}
	return strings.Join(hits, "\n"), nil
}

func LogMessage(state *SwarmState, timestamp, sender, message, channel string) error {
	state.DBQueue <- func() {
		_, err := state.DB.Exec(context.Background(), `INSERT INTO conspiri_history(timestamp, sender, message, channel) VALUES($1, $2, $3, $4)`, timestamp, sender, message, channel)
		if err != nil {
			state.Logger.Error("LogMessage error", "error", err)
		}
	}
	return nil
}

func GetRecentHistory(state *SwarmState, limit int, channel string) ([][2]string, error) {
	var rows pgx.Rows
	var err error
	if channel == "" {
		rows, err = state.DB.Query(context.Background(), `SELECT sender, message FROM conspiri_history ORDER BY id DESC LIMIT $1`, limit)
	} else {
		rows, err = state.DB.Query(context.Background(), `SELECT sender, message FROM conspiri_history WHERE channel = $1 ORDER BY id DESC LIMIT $2`, channel, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res [][2]string
	for rows.Next() {
		var s, m string
		if err := rows.Scan(&s, &m); err != nil {
			return nil, err
		}
		res = append(res, [2]string{s, m})
	}
	return res, nil
}

func UpdateReputation(state *SwarmState, nick string, delta int) error {
	state.DBQueue <- func() {
		_, err := state.DB.Exec(context.Background(), `INSERT INTO conspiri_reputation(nick, score) VALUES($1, $2) ON CONFLICT(nick) DO UPDATE SET score = conspiri_reputation.score + EXCLUDED.score`, nick, delta)
		if err != nil {
			state.Logger.Error("UpdateReputation error", "error", err)
		}
	}
	return nil
}
