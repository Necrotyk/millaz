package conspiribot

import (
	"fmt"
	"strings"
)

// SummarizeAndStore fetches recent memory entries for a bot, summarizes them with Gemini (if available),
// and stores the compact summary in the memory_summaries table.
func SummarizeAndStore(state *SwarmState, botNick string) error {
	// fetch up to 100 recent memory entries
	rows, err := state.DB.Query(`SELECT content FROM memory WHERE bot_nick = ? ORDER BY id DESC LIMIT 100`, botNick)
	if err != nil {
		return err
	}
	defer rows.Close()

	parts := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return err
		}
		parts = append(parts, c)
	}

	if len(parts) == 0 {
		return nil
	}

	// reverse to chronological order
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}

	// Build a compact prompt for summarization
	combined := strings.Join(parts, "\n")
	if len(combined) > 20000 {
		// truncate to last 20k chars
		combined = combined[len(combined)-20000:]
	}

	prompt := fmt.Sprintf("Summarize the following conversation into 6 concise bullet points highlighting persistent facts, opinions, and recurring themes. Keep it under 800 characters.\n\nConversation:\n%s", combined)

	key := state.APIKey
	summary := ""
	if key != "" {
		// prefer model indicated by h4ck3rm4n or default
		model := "gemini-2.5-flash-lite"
		// call Gemini
		out, err := CallGeminiText(key, model, BotPersona{Nick: botNick, System: "Summarizer"}, "system", prompt)
		if err == nil && out != "" {
			summary = out
		}
	}

	if summary == "" {
		// fallback: take the most frequent short lines or first few entries
		take := 6
		if len(parts) < take {
			take = len(parts)
		}
		// take the last `take` entries as a crude summary
		summary = strings.Join(parts[len(parts)-take:], " \n")
		if len(summary) > 800 {
			summary = summary[len(summary)-800:]
		}
	}

	// store summary
	if err := SaveSummary(state, botNick, summary); err != nil {
		return err
	}

	// optional: delete very old memory rows to keep DB small; keep last 500
	go func() {
		_ = pruneOldMemory(state, botNick, 500)
	}()

	return nil
}

// pruneOldMemory keeps only the most recent `keep` rows for botNick
func pruneOldMemory(state *SwarmState, botNick string, keep int) error {
	// find the id threshold: select id from memory where bot_nick = ? order by id desc limit 1 offset keep-1
	var thresholdID int
	row := state.DB.QueryRow(`SELECT id FROM memory WHERE bot_nick = ? ORDER BY id DESC LIMIT 1 OFFSET ?`, botNick, keep-1)
	if err := row.Scan(&thresholdID); err != nil {
		// nothing to prune
		return nil
	}
	_, err := state.DB.Exec(`DELETE FROM memory WHERE bot_nick = ? AND id < ?`, botNick, thresholdID)
	return err
}
