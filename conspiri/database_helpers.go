package conspiribot

import (
	"context"
)

// SaveUserFact inserts a new fact into the user_facts table via the serialized queue
func SaveUserFact(state *SwarmState, user, fact string, embedding string) {
	state.DBQueue <- func() {
		if embedding != "" {
			_, err := state.DB.Exec(context.Background(), "INSERT INTO conspiri_user_facts (user_nick, fact, created_at, embedding) VALUES ($1, $2, NOW(), $3::vector) ON CONFLICT (user_nick, fact) DO NOTHING",
				user, fact, embedding)
			if err != nil {
				state.Logger.Error("Memory DB Error", "error", err)
			}
		} else {
			_, err := state.DB.Exec(context.Background(), "INSERT INTO conspiri_user_facts (user_nick, fact, created_at) VALUES ($1, $2, NOW()) ON CONFLICT (user_nick, fact) DO NOTHING",
				user, fact)
			if err != nil {
				state.Logger.Error("Memory DB Error", "error", err)
			}
		}
	}
}
