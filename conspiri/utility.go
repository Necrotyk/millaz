package conspiribot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// UtilityHandler processes commands for the helper bot
func UtilityHandler(bot *Bot, sender, channel, message string, provider IRCProvider) {
	msg := strings.TrimSpace(message)

	// 1. URL Title Fetcher (Passive or Active)
	// Regex to find URLs
	urlRegex := regexp.MustCompile(`https?://[^\s]+`)
	if urls := urlRegex.FindAllString(msg, -1); len(urls) > 0 {
		for _, u := range urls {
			title := fetchTitle(u)
			if title != "" {
				bot.sendTo(channel, fmt.Sprintf("[URL] %s", title), provider)
			}
		}
	}

	// 2. Explicit Commands
	if !strings.HasPrefix(msg, "!") {
		return
	}

	parts := strings.Fields(msg)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "!ping":
		bot.sendTo(channel, fmt.Sprintf("%s: Pong.", sender), provider)

	case "!seen":
		if len(args) > 0 {
			handleSeen(bot.State, bot, channel, args[0], provider)
		}

	case "!ask":
		// Direct LLM query to the utility bot
		if len(args) == 0 {
			bot.sendTo(channel, "Usage: !ask <question>", provider)
			return
		}
		query := strings.Join(args, " ")
		handleAsk(bot, sender, channel, query, provider)

	case "!recap":
		// Summarize recent chat history
		handleRecap(bot, channel, provider)

	case "!help":
		bot.sendTo(channel, "Utility: !ping, !seen <user>, !ask <query>, !recap (summarize chat), (auto-url titles)", provider)
	}
}

func handleAsk(bot *Bot, sender, channel, query string, provider IRCProvider) {
	key := bot.State.APIKey
	if key == "" {
		bot.sendTo(channel, "Error: AI backend not configured.", provider)
		return
	}

	// Privacy Session
	privacy := NewPrivacySession(bot.Persona.Nick)
	maskedSender := privacy.Mask(sender)
	maskedQuery := privacy.SanitizeText(query)

	persona := BotPersona{
		Nick:   bot.Persona.Nick,
		System: "You are a helpful utility bot. Respond to " + maskedSender + ".",
	}

	bot.sendTo(channel, fmt.Sprintf("%s: Thinking...", sender), provider)

	// Call API with masked data
	reply, err := CallGeminiText(bot.State.Logger, key, "gemini-2.5-flash-lite", "", persona, maskedSender, maskedQuery)
	if err != nil {
		bot.sendTo(channel, "Error retrieving answer.", provider)
		return
	}

	// Restore real names in the answer
	finalReply := privacy.DesanitizeText(reply)

	bot.sendTo(channel, fmt.Sprintf("%s: %s", sender, finalReply), provider)
}

func handleRecap(bot *Bot, channel string, provider IRCProvider) {
	key := bot.State.APIKey
	if key == "" {
		bot.sendTo(channel, "AI backend not configured.", provider)
		return
	}

	recent, err := GetRecentHistory(bot.State, 50, channel)
	if err != nil || len(recent) == 0 {
		bot.sendTo(channel, "No recent history to recap.", provider)
		return
	}

	// Privacy Session
	privacy := NewPrivacySession(bot.Persona.Nick)

	var sb strings.Builder
	for i := len(recent) - 1; i >= 0; i-- {
		// Mask the sender of every message
		s := privacy.Mask(recent[i][0])
		m := privacy.SanitizeText(recent[i][1])
		sb.WriteString(fmt.Sprintf("%s: %s\n", s, m))
	}

	prompt := fmt.Sprintf("Summarize these logs into 3 brief bullet points:\n\n%s", sb.String())
	persona := BotPersona{Nick: bot.Persona.Nick, System: "Concise summarizer."}

	summary, err := CallGeminiText(bot.State.Logger, key, "gemini-2.5-flash-lite", "", persona, "system", prompt)
	if err != nil {
		bot.sendTo(channel, "Failed to generate recap.", provider)
		return
	}

	// Restore names so the summary makes sense to users
	// e.g. "Entity_5 was arguing about ..." -> "FlatDave was arguing about ..."
	finalSummary := privacy.DesanitizeText(summary)

	bot.sendTo(channel, fmt.Sprintf("📝 Recap:\n%s", finalSummary), provider)
}

// fetchTitle grabs the <title> tag from a URL
func fetchTitle(url string) string {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		return ""
	}

	// Limit read to 50KB to prevent attacks
	z := html.NewTokenizer(io.LimitReader(resp.Body, 51200))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return ""
		}
		if tt == html.StartTagToken {
			t := z.Token()
			if t.Data == "title" {
				if z.Next() == html.TextToken {
					title := strings.TrimSpace(z.Token().Data)
					// Decode HTML entities (e.g., &amp; -> &)
					title = html.UnescapeString(title)
					if len(title) > 100 {
						title = title[:97] + "..."
					}
					return title
				}
			}
		}
	}
}

func handleSeen(state *SwarmState, bot *Bot, channel, target string, provider IRCProvider) {
	var lastSeen string
	var lastMsg string
	err := state.DB.QueryRow(context.Background(), "SELECT timestamp, message FROM conspiri_history WHERE sender = $1 ORDER BY id DESC LIMIT 1", target).Scan(&lastSeen, &lastMsg)
	if err != nil {
		bot.sendTo(channel, fmt.Sprintf("I haven't seen %s.", target), provider)
		return
	}

	// Parse time for friendly display
	t, _ := time.Parse(time.RFC3339, lastSeen)
	duration := time.Since(t).Round(time.Minute)

	bot.sendTo(channel, fmt.Sprintf("%s was last seen %v ago saying: '%s'", target, duration, lastMsg), provider)
}
