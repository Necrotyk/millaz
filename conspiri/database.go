package conspiribot

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // Import the sqlite3 driver
)

const (
	dbPath = "swarm.db"
)

type SwarmState struct {
	DB      *sql.DB
	DBQueue chan func()
	Cancel  context.CancelFunc
	APIKey  string
	Config  *Config
}

func NewSwarmState(db *sql.DB, ctx context.Context, apiKey string, config *Config) *SwarmState {
	ctx, cancel := context.WithCancel(ctx)
	s := &SwarmState{
		DB:      db,
		DBQueue: make(chan func(), 100),
		Cancel:  cancel,
		APIKey:  apiKey,
		Config:  config,
	}
	go s.worker(ctx)
	return s
}

func (s *SwarmState) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.DBQueue:
			job()
		}
	}
}

var stmtSearchMemory *sql.Stmt

// Init initializes the Swarm, the SQLite database and exports a SwarmState
func Init(ctx context.Context, dbPath, apiKey string, config *Config) (*SwarmState, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database at %s: %w", dbPath, err)
	}

	if _, err := db.Exec(`
		PRAGMA busy_timeout = 5000;
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA cache_size = -2000;
		PRAGMA temp_store = MEMORY;
	`); err != nil {
		return nil, fmt.Errorf("failed to set PRAGMAs: %w", err)
	}

	// Create history table (includes channel)
	historySQL := `
	CREATE TABLE IF NOT EXISTS history (
		id INTEGER PRIMARY KEY,
		timestamp TEXT,
		sender TEXT,
		message TEXT,
		channel TEXT DEFAULT ''
	);`
	if _, err := db.Exec(historySQL); err != nil {
		return nil, fmt.Errorf("failed to create history table: %w", err)
	}

	// Create reputation table
	reputationSQL := `
	CREATE TABLE IF NOT EXISTS reputation (
		nick TEXT PRIMARY KEY, 
		score INTEGER DEFAULT 0, 
		notes TEXT
	);`
	if _, err := db.Exec(reputationSQL); err != nil {
		return nil, fmt.Errorf("failed to create reputation table: %w", err)
	}

	// Create memory table for per-bot memory (includes channel)
	memorySQL := `
	CREATE TABLE IF NOT EXISTS memory (
		id INTEGER PRIMARY KEY,
		bot_nick TEXT,
		timestamp TEXT,
		content TEXT,
		channel TEXT DEFAULT '',
		embedding BLOB
	);`
	if _, err := db.Exec(memorySQL); err != nil {
		return nil, fmt.Errorf("failed to create memory table: %w", err)
	}

	// Create memory summaries table for compacted long-term memory
	summarySQL := `
	CREATE TABLE IF NOT EXISTS memory_summaries (
		bot_nick TEXT PRIMARY KEY,
		updated_at TEXT,
		summary TEXT
	);`
	if _, err := db.Exec(summarySQL); err != nil {
		return nil, fmt.Errorf("failed to create memory_summaries table: %w", err)
	}

	// Store persistent facts about users (Long-term memory)
	// We add an 'embedding' column for semantic search (BLOB to store serialized []float32)
	userFactsSQL := `
	CREATE TABLE IF NOT EXISTS user_facts (
		user_nick TEXT,
		fact TEXT,
		created_at TEXT,
		embedding BLOB,
		PRIMARY KEY (user_nick, fact)
	);`
	if _, err := db.Exec(userFactsSQL); err != nil {
		return nil, fmt.Errorf("failed to create user_facts table: %w", err)
	}

	// Store valid cached URL titles for the utility bot to avoid re-fetching
	urlCacheSQL := `
	CREATE TABLE IF NOT EXISTS url_cache (
		url_hash TEXT PRIMARY KEY,
		title TEXT,
		fetched_at TEXT
	);`
	if _, err := db.Exec(urlCacheSQL); err != nil {
		return nil, fmt.Errorf("failed to create url_cache table: %w", err)
	}

	// Ensure schema upgrades for channel-aware columns
	if err := ensureSchema(db); err != nil {
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	fmt.Printf("Database initialized at %s.\n", dbPath)

	state := NewSwarmState(db, ctx, apiKey, config)
	stmtSearchMemory, err = db.Prepare(`SELECT content, embedding FROM memory WHERE bot_nick = ? AND embedding IS NOT NULL ORDER BY id DESC`)

	// Setup AppConfig global for legacy compatibility
	AppConfig = config

	if config != nil {
		scheduler := NewSpeakScheduler(ctx, time.Duration(config.Scheduler.IntervalSeconds)*time.Second, config.Scheduler.QueueSize)
		for _, p := range config.Bots {
			NewBot(state, p, scheduler)
		}
	}

	return state, nil
}

// ensureSchema upgrades older DBs by adding missing columns if necessary
func ensureSchema(db *sql.DB) error {
	// helper to check column existence
	hasCol := func(table, col string) (bool, error) {
		rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name string
			var ctype string
			var notnull int
			var dflt interface{}
			var pk int
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				return false, err
			}
			if name == col {
				return true, nil
			}
		}
		return false, nil
	}

	if ok, _ := hasCol("history", "channel"); !ok {
		if _, err := db.Exec(`ALTER TABLE history ADD COLUMN channel TEXT DEFAULT ''`); err != nil {
			return err
		}
	}
	if ok, _ := hasCol("memory", "channel"); !ok {
		if _, err := db.Exec(`ALTER TABLE memory ADD COLUMN channel TEXT DEFAULT ''`); err != nil {
			return err
		}
	}
	if ok, _ := hasCol("user_facts", "embedding"); !ok {
		if _, err := db.Exec(`ALTER TABLE user_facts ADD COLUMN embedding BLOB`); err != nil {
			return err
		}
	}
	if ok, _ := hasCol("memory", "embedding"); !ok {
		if _, err := db.Exec(`ALTER TABLE memory ADD COLUMN embedding BLOB`); err != nil {
			return err
		}
	}
	return nil
}

// SaveMemory appends a memory entry for a bot
func SaveMemory(state *SwarmState, botNick, content, channel string) error {
	// Offload to worker to avoid locks, but we need embeddings which are slow.
	// We calculate embedding *outside* the critical DB lock, then enqueue the write.

	go func() {
		// Calculate embedding (best effort, ignore error)
		var embedBlob []byte
		key := state.APIKey
		if key != "" {
			embedding, err := GetEmbedding(key, content)
			if err == nil {
				embedBlob = float32ToByte(embedding)
			}
		}

		state.DBQueue <- func() {
			// Avoid inserting exact duplicates that are already the most recent memory for this bot/channel.
			var last string
			err := state.DB.QueryRow(`SELECT content FROM memory WHERE bot_nick = ? AND channel = ? ORDER BY id DESC LIMIT 1`, botNick, channel).Scan(&last)
			if err == nil {
				if strings.TrimSpace(last) == strings.TrimSpace(content) {
					// Skip inserting duplicate
					return
				}
			}

			_, err = state.DB.Exec(`INSERT INTO memory(bot_nick, timestamp, content, channel, embedding) VALUES(?, ?, ?, ?, ?)`,
				botNick, time.Now().Format(time.RFC3339), content, channel, embedBlob)
			if err != nil {
				fmt.Printf("[DB] SaveMemory error: %v\n", err)
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

// GetMemorySummary returns up to `limit` recent memory entries for bot concatenated and truncated to maxChars
// GetMemorySummary returns the stored summary plus recent memory entries for bot
// If channel is non-empty, it will include only memory rows for that channel.
func GetMemorySummary(state *SwarmState, botNick string, channel string, limit int, maxChars int) (string, error) {
	// First, get any stored summary
	summary, _ := GetSummary(state, botNick)

	var rows *sql.Rows
	var err error
	if channel == "" {
		rows, err = state.DB.Query(`SELECT content FROM memory WHERE bot_nick = ? ORDER BY id DESC LIMIT ?`, botNick, limit)
	} else {
		rows, err = state.DB.Query(`SELECT content FROM memory WHERE bot_nick = ? AND channel = ? ORDER BY id DESC LIMIT ?`, botNick, channel, limit)
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

	// reverse to chronological order (oldest first)
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}

	combined := strings.Join(parts, " \n")
	// Prepend stored summary if present
	if summary != "" {
		combined = summary + "\n" + combined
	}

	if len(combined) > maxChars {
		combined = combined[len(combined)-maxChars:]
	}
	return combined, nil
}

// GetMemoryCount returns number of memory rows for a bot
func GetMemoryCount(state *SwarmState, botNick string) (int, error) {
	var cnt int
	err := state.DB.QueryRow(`SELECT COUNT(*) FROM memory WHERE bot_nick = ?`, botNick).Scan(&cnt)
	if err != nil {
		return 0, err
	}
	return cnt, nil
}

// SaveSummary upserts a compact summary for a bot
func SaveSummary(state *SwarmState, botNick, summary string) error {
	state.DBQueue <- func() {
		_, err := state.DB.Exec(`INSERT INTO memory_summaries(bot_nick, updated_at, summary) VALUES(?, ?, ?) ON CONFLICT(bot_nick) DO UPDATE SET updated_at = excluded.updated_at, summary = excluded.summary`, botNick, time.Now().Format(time.RFC3339), summary)
		if err != nil {
			fmt.Printf("[DB] SaveSummary error: %v\n", err)
		}
	}
	return nil
}

// GetSummary returns the stored summary for a bot (or empty string)
func GetSummary(state *SwarmState, botNick string) (string, error) {
	var s string
	err := state.DB.QueryRow(`SELECT summary FROM memory_summaries WHERE bot_nick = ?`, botNick).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return s, nil
}

// SearchRelevantMemory performs a semantic search on the memory table using embeddings.
// Fallbacks to keyword search if embedding fails or isn't available.
func SearchRelevantMemory(state *SwarmState, botNick, query string, limit int) (string, error) {
	key := state.APIKey
	var queryEmbedding []float32
	var err error

	if key != "" {
		queryEmbedding, err = GetEmbedding(key, query)
	}

	// If we can't get an embedding, fallback to keyword search
	if len(queryEmbedding) == 0 || err != nil {
		// 1. Extract significant keywords (ignoring small words)
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
		for _, kw := range keywords {
			conditions = append(conditions, "content LIKE ?")
			args = append(args, "%"+kw+"%")
		}
		args = append(args, limit)
		sqlStr := fmt.Sprintf("SELECT content FROM memory WHERE bot_nick = ? AND (%s) ORDER BY id DESC LIMIT ?", strings.Join(conditions, " OR "))

		rows, err := state.DB.Query(sqlStr, args...)
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

	// Semantic Search Logic
	// Fetch all memories with embeddings. Note: This linear scan will be slow with large datasets.
	// In a real production system, use a vector database or sqlite-vec.
	rows, err := state.DB.Query(`SELECT content, embedding FROM memory WHERE bot_nick = ? AND embedding IS NOT NULL ORDER BY id DESC`, botNick)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	type result struct {
		content string
		score   float32
	}
	var results []result

	// Pre-calculate query magnitude once
	var queryMag float32
	for _, v := range queryEmbedding {
		queryMag += v * v
	}
	queryMag = float32(math.Sqrt(float64(queryMag)))

	for rows.Next() {
		var c string
		var b []byte
		if err := rows.Scan(&c, &b); err != nil {
			continue
		}
		// Optimized calculation: avoids allocating []float32 per row
		score := cosineSimilarityFromBytes(queryEmbedding, queryMag, b)
		if score > 0.4 { // Similarity threshold
			results = append(results, result{c, score})
		}
	}

	// Sort by score DESC
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Take top K
	var hits []string
	count := 0
	for _, r := range results {
		hits = append(hits, r.content)
		count++
		if count >= limit {
			break
		}
	}

	return strings.Join(hits, "\n"), nil
}

// LogMessage inserts a chat message into the history table
func LogMessage(state *SwarmState, timestamp, sender, message, channel string) error {
	state.DBQueue <- func() {
		_, err := state.DB.Exec(`INSERT INTO history(timestamp, sender, message, channel) VALUES(?, ?, ?, ?)`, timestamp, sender, message, channel)
		if err != nil {
			fmt.Printf("[DB] LogMessage error: %v\n", err)
		}
	}
	return nil
}

// GetRecentHistory returns the most recent N messages (sender,message)
func GetRecentHistory(state *SwarmState, limit int, channel string) ([][2]string, error) {
	var rows *sql.Rows
	var err error
	if channel == "" {
		rows, err = state.DB.Query(`SELECT sender, message FROM history ORDER BY id DESC LIMIT ?`, limit)
	} else {
		rows, err = state.DB.Query(`SELECT sender, message FROM history WHERE channel = ? ORDER BY id DESC LIMIT ?`, channel, limit)
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

// UpdateReputation increments (or decrements) the reputation score for a nick
func UpdateReputation(state *SwarmState, nick string, delta int) error {
	state.DBQueue <- func() {
		_, err := state.DB.Exec(`INSERT INTO reputation(nick, score) VALUES(?, ?) ON CONFLICT(nick) DO UPDATE SET score = reputation.score + ?`, nick, delta, delta)
		if err != nil {
			fmt.Printf("[DB] UpdateReputation error: %v\n", err)
		}
	}
	return nil
}
