package conspiribot

import (
	"fmt"
	"time"
)

// SaveUserFact inserts a new fact into the user_facts table via the serialized queue
func SaveUserFact(state *SwarmState, user, fact string, embedding []byte) {
	state.DBQueue <- func() {
		_, err := state.DB.Exec("INSERT OR IGNORE INTO user_facts (user_nick, fact, created_at, embedding) VALUES (?, ?, ?, ?)",
			user, fact, time.Now().Format(time.RFC3339), embedding)
		if err != nil {
			fmt.Printf("[Memory] DB Error: %v\n", err)
		}
	}
}
