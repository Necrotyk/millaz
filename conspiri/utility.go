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

	// 1. Passive URL Title Extraction
	urlRegex := regexp.MustCompile(`https?://[^\s]+`)
	if urls := urlRegex.FindAllString(msg, -1); len(urls) > 0 && !strings.HasPrefix(msg, "!") {
		for _, u := range urls {
			// Background thread to prevent blocking IRC ingress
			go func(target string) {
				title, _, err := fetchWebpage(target)
				if err == nil && title != "" {
					bot.sendTo(channel, fmt.Sprintf("[URL] %s", title), provider)
				}
			}(u)
		}
	}

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

	case "!summarize":
		if len(args) == 0 {
			bot.sendTo(channel, "Usage: !summarize <url>", provider)
			return
		}

		// Auth Gate: Ensure AppConfig exists and sender matches admin criteria
		if AppConfig == nil || sender != AppConfig.AdminNick {
			bot.sendTo(channel, "Unauthorized.", provider)
			return
		}

		targetURL := args[0]
		bot.sendTo(channel, fmt.Sprintf("%s: Fetching and analyzing DOM...", sender), provider)

		go func(url string) {
			title, content, err := fetchWebpage(url)
			if err != nil || content == "" {
				bot.sendTo(channel, fmt.Sprintf("%s: Failed to extract content from URL.", sender), provider)
				return
			}

			// Truncate content to roughly 20k characters to stay within safe token limits
			if len(content) > 20000 {
				content = content[:20000]
			}

			key := bot.State.APIKey
			if key == "" {
				bot.sendTo(channel, "Error: AI backend offline.", provider)
				return
			}

			prompt := fmt.Sprintf("Summarize the following web page content into 3 concise, informative bullet points. Page Title: %s\n\nContent:\n%s", title, content)
			persona := BotPersona{Nick: bot.Persona.Nick, System: "You are a precise technical summarizer."}

			summary, err := CallGeminiText(bot.State.Logger, key, "gemini-2.5-flash-lite", "", persona, "system", prompt)
			if err != nil {
				bot.sendTo(channel, fmt.Sprintf("%s: LLM summarization failed.", sender), provider)
				return
			}

			// Clean multi-line responses for IRC output
			lines := strings.Split(summary, "\n")
			for _, line := range lines {
				cleaned := strings.TrimSpace(line)
				if cleaned != "" {
					bot.sendTo(channel, cleaned, provider)
					time.Sleep(500 * time.Millisecond) // Flood control pacing
				}
			}
		}(targetURL)

	case "!help":
		bot.sendTo(channel, "Utility: !ping, !seen <user>, !ask <query>, !recap (summarize chat), !summarize <url>, (auto-url titles)", provider)
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

// fetchWebpage extracts the title and raw text content from a URL safely.
func fetchWebpage(targetURL string) (title string, textContent string, err error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", "", err
	}
	// Spoof UA to prevent 403s from basic CDNs
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		return "", "", fmt.Errorf("invalid response or non-HTML content")
	}

	// Hard limit to 1MB to prevent memory exhaustion
	limitReader := io.LimitReader(resp.Body, 1024*1024)
	tokenizer := html.NewTokenizer(limitReader)

	var textBuilder strings.Builder
	var isTitle bool
	ignoreTags := map[string]bool{"script": true, "style": true, "noscript": true}
	ignoreCurrent := false

	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}

		token := tokenizer.Token()
		switch tt {
		case html.StartTagToken:
			if token.Data == "title" {
				isTitle = true
			} else if ignoreTags[token.Data] {
				ignoreCurrent = true
			}
		case html.EndTagToken:
			if token.Data == "title" {
				isTitle = false
			} else if ignoreTags[token.Data] {
				ignoreCurrent = false
			}
		case html.TextToken:
			if isTitle {
				title = strings.TrimSpace(token.Data)
			} else if !ignoreCurrent {
				cleaned := strings.TrimSpace(token.Data)
				if cleaned != "" {
					textBuilder.WriteString(cleaned + " ")
				}
			}
		}
	}

	return html.UnescapeString(title), textBuilder.String(), nil
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
