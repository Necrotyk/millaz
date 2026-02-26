import sys

with open("database.go", "r") as f:
    content = f.read()

content = content.replace("package conspiribot\n\nimport (\n\t\"database/sql\"\n\t\"fmt\"\n", "package conspiribot\n\nimport (\n\t\"context\"\n\t\"database/sql\"\n\t\"fmt\"\n")

state_code = """type SwarmState struct {
	DB      *sql.DB
	DBQueue chan func()
	Cancel  context.CancelFunc
}

func NewSwarmState(db *sql.DB, ctx context.Context) *SwarmState {
	ctx, cancel := context.WithCancel(ctx)
	s := &SwarmState{
		DB:      db,
		DBQueue: make(chan func(), 100),
		Cancel:  cancel,
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

// InitDB initializes the SQLite database and creates tables if they don't exist
func InitDB(ctx context.Context) (*SwarmState, error) {"""

content = content.replace("// InitDB initializes the SQLite database and creates tables if they don't exist\nfunc InitDB() (*sql.DB, error) {", state_code)

pragmas_old = """	// Set a busy timeout to reduce "database is locked" errors under concurrency
	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		return nil, fmt.Errorf("failed to set busy_timeout: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, fmt.Errorf("failed to set journal_mode=WAL: %w", err)
	}"""
pragmas_new = """	if _, err := db.Exec(`
		PRAGMA busy_timeout = 5000;
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA cache_size = -2000;
		PRAGMA temp_store = MEMORY;
	`); err != nil {
		return nil, fmt.Errorf("failed to set PRAGMAs: %w", err)
	}"""
content = content.replace(pragmas_old, pragmas_new)

old_return = """	// Start DB Worker
	StartDBWorker(db)

	return db, nil
}"""
new_return = """	state := NewSwarmState(db, ctx)
	stmtSearchMemory, err = db.Prepare(`SELECT content, embedding FROM memory WHERE bot_nick = ? AND embedding IS NOT NULL ORDER BY id DESC`)

	return state, nil
}"""
content = content.replace(old_return, new_return)

content = content.replace("func SaveMemory(db *sql.DB, ", "func SaveMemory(state *SwarmState, ")
content = content.replace("dbQueue <- func() {", "state.DBQueue <- func() {")
content = content.replace("err := db.QueryRow", "err := state.DB.QueryRow")
content = content.replace("_, err = db.Exec", "_, err = state.DB.Exec")
content = content.replace("(db, botNick)", "(state, botNick)")

content = content.replace("func GetMemorySummary(db *sql.DB, ", "func GetMemorySummary(state *SwarmState, ")
content = content.replace("db.Query(", "state.DB.Query(")

content = content.replace("func GetMemoryCount(db *sql.DB, ", "func GetMemoryCount(state *SwarmState, ")
content = content.replace("func SaveSummary(db *sql.DB, ", "func SaveSummary(state *SwarmState, ")

content = content.replace("func GetSummary(db *sql.DB, ", "func GetSummary(state *SwarmState, ")
content = content.replace("func SearchRelevantMemory(db *sql.DB, ", "func SearchRelevantMemory(state *SwarmState, ")
content = content.replace("rows, err := db.Query(`SELECT content, embedding FROM memory WHERE bot_nick = ? AND embedding IS NOT NULL ORDER BY id DESC`, botNick)", "rows, err := stmtSearchMemory.Query(botNick)")

content = content.replace("func LogMessage(db *sql.DB, ", "func LogMessage(state *SwarmState, ")
content = content.replace("_, err := db.Exec(`INSERT INTO", "_, err := state.DB.Exec(`INSERT INTO")

content = content.replace("func GetRecentHistory(db *sql.DB, ", "func GetRecentHistory(state *SwarmState, ")
content = content.replace("func UpdateReputation(db *sql.DB, ", "func UpdateReputation(state *SwarmState, ")

worker_code = """// --- DB Worker ---

var dbQueue = make(chan func(), 100)

func StartDBWorker(db *sql.DB) {
	go func() {
		for job := range dbQueue {
			job()
		}
	}()
}"""
content = content.replace(worker_code, "")

with open("database.go", "w") as f:
    f.write(content)

with open("database_helpers.go", "r") as f:
    dh = f.read()

dh = dh.replace("func SaveUserFact(db *sql.DB, ", "func SaveUserFact(state *SwarmState, ")
dh = dh.replace("dbQueue <- func() {", "state.DBQueue <- func() {")
dh = dh.replace("_, err := db.Exec", "_, err := state.DB.Exec")
with open("database_helpers.go", "w") as f:
    f.write(dh)

with open("bot.go", "r") as f:
    bot = f.read()

bot = bot.replace("\tDB            *sql.DB", "\tState         *SwarmState")
bot = bot.replace("func NewBot(db *sql.DB,", "func NewBot(state *SwarmState,")
bot = bot.replace("\t\tDB:        db,", "\t\tState:     state,")
bot = bot.replace("b.DB", "b.State")
bot = bot.replace("AppDB", "AppState")
with open("bot.go", "w") as f:
    f.write(bot)

with open("utility.go", "r") as f:
    ut = f.read()

ut = ut.replace("func handleSeen(db *sql.DB,", "func handleSeen(state *SwarmState,")
ut = ut.replace("db.QueryRow", "state.DB.QueryRow")
ut = ut.replace("bot.DB", "bot.State")
with open("utility.go", "w") as f:
    f.write(ut)

with open("llm.go", "r") as f:
    llm = f.read()

llm = llm.replace("func GetUserFacts(db *sql.DB", "func GetUserFacts(state *SwarmState")
llm = llm.replace("db.Query", "state.DB.Query")
llm = llm.replace("func ExtractFacts(db *sql.DB", "func ExtractFacts(state *SwarmState")
llm = llm.replace("SaveUserFact(db, ", "SaveUserFact(state, ")
llm = llm.replace("func GenerateReply(db *sql.DB", "func GenerateReply(state *SwarmState")
llm = llm.replace("GetUserFacts(db, ", "GetUserFacts(state, ")
llm = llm.replace("SearchRelevantMemory(db, ", "SearchRelevantMemory(state, ")
with open("llm.go", "w") as f:
    f.write(llm)

