package conspiribot

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	genai "google.golang.org/genai"
)

// GetUserFacts retrieves persistent facts about the specific user
func GetUserFacts(state *SwarmState, userNick string) string {
	rows, err := state.DB.Query(context.Background(), "SELECT fact FROM conspiri_user_facts WHERE user_nick = $1", userNick)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var facts []string
	for rows.Next() {
		var f string
		rows.Scan(&f)
		facts = append(facts, f)
	}
	if len(facts) == 0 {
		return ""
	}
	// Deduplicate facts in memory before joining
	unique := make(map[string]bool)
	var clean []string
	for _, f := range facts {
		if !unique[f] {
			unique[f] = true
			clean = append(clean, f)
		}
	}
	return strings.Join(clean, "; ")
}

// ExtractFacts now respects privacy by sanitizing the input before sending to LLM
func ExtractFacts(state *SwarmState, user, message string) {
	// Refined trigger logic: stricter verbs
	lower := strings.ToLower(message)
	triggers := []string{"i am ", "i have ", "i live ", "my name ", "i own ", "i work ", "i love ", "i hate ", "we are "}
	triggered := false
	for _, t := range triggers {
		if strings.Contains(lower, t) {
			triggered = true
			break
		}
	}

	if !triggered {
		return
	}

	key := state.APIKey
	if key == "" {
		return
	}

	// Privacy: Mask the user's name in the prompt sent to Google
	privacy := NewPrivacySession("System_Extractor")
	maskedUser := privacy.Mask(user)
	maskedMessage := privacy.SanitizeText(message)

	sysPrompt := `You are a fact extractor. Analyze the message from the user.
    If they explicitly state a permanent fact about themselves (e.g., location, profession, name),
    extract it as a concise third-person statement using their identifier (e.g., "` + maskedUser + ` owns a car").
    If no fact is present, return "NO".`

	userPrompt := fmt.Sprintf("User: %s\nMessage: %s", maskedUser, maskedMessage)

	extractorPersona := BotPersona{Nick: "System_Extractor", System: sysPrompt}

	fact, err := CallGeminiText(key, "gemini-2.5-flash-lite", extractorPersona, maskedUser, userPrompt)
	if err != nil || fact == "" {
		return
	}

	// Privacy: Unmask the fact before storing it in the DB
	// The DB is trusted storage; we only obfuscate for the external LLM processor.
	cleanFact := strings.TrimSpace(privacy.DesanitizeText(fact))

	if strings.EqualFold(cleanFact, "NO") || len(cleanFact) < 5 {
		return
	}

	log.Printf("[Memory] Learned fact about %s: %s", user, cleanFact)

	// Generate embedding for the fact
	embedding, err := GetEmbedding(key, cleanFact)
	var embedBlob string
	if err == nil {
		var strs []string
		for _, f := range embedding {
			strs = append(strs, fmt.Sprintf("%f", f))
		}
		embedBlob = "[" + strings.Join(strs, ",") + "]"
	}

	SaveUserFact(state, user, cleanFact, embedBlob)
}

func GenerateReply(state *SwarmState, persona BotPersona, sender, prompt string, recent [][2]string, memory string, deep bool) string {
	// 1. Initialize Privacy Session
	// We exclude the current bot's nick from masking so it knows who IT is.
	privacy := NewPrivacySession(persona.Nick)

	// 2. Pre-calculate masks for all participants in recent history
	// This ensures consistency (UserA is always Entity_1) throughout the prompt
	privacy.Mask(sender) // Ensure sender has an ID
	for _, row := range recent {
		privacy.Mask(row[0])
	}

	// 3. Retrieve Context (Facts & Search)
	userFacts := GetUserFacts(state, sender) // Retrieved using real nick

	// Topic Recall: Search for relevant past memories using Semantic Search
	topicContext, _ := SearchRelevantMemory(state, persona.Nick, prompt, 3)

	// 4. Build Prompt with Sanitized Data
	var ctxBuilder strings.Builder
	ctxBuilder.WriteString("System: ")
	ctxBuilder.WriteString(persona.System)

	// Inject Facts (Sanitized)
	if userFacts != "" {
		ctxBuilder.WriteString("\nGlobal Knowledge: ")
		// We format it as "Entity_1 -> Fact"
		ctxBuilder.WriteString(privacy.Mask(sender))
		ctxBuilder.WriteString(" -> ")
		ctxBuilder.WriteString(privacy.SanitizeText(userFacts))
	}

	// Inject Topic Search (Sanitized)
	if topicContext != "" {
		ctxBuilder.WriteString("\nRelated Past Memories: ")
		ctxBuilder.WriteString(privacy.SanitizeText(topicContext))
	}

	// Inject Recent History (Sanitized & Pruned)
	prunedRecent := pruneContext(recent, 4000) // approximate char limit for history
	if len(prunedRecent) > 0 {
		ctxBuilder.WriteString("\nRecent Chat:\n")
		// pruneContext already returns newest-first, so we iterate normally?
		// pruneContext likely keeps order but slices.
		// Actually recent is [][2]string (oldest to newest usually, or newest to oldest?)
		// GetRecentHistory in database.go returns ORDER BY id DESC (newest first).
		// So recent[0] is newest.
		// For the prompt, we usually want oldest first (conversation flow).
		// So we iterate backwards.

		for i := len(prunedRecent) - 1; i >= 0; i-- {
			s := privacy.Mask(prunedRecent[i][0])
			m := privacy.SanitizeText(prunedRecent[i][1])
			ctxBuilder.WriteString(fmt.Sprintf("%s: %s\n", s, m))
		}
	}

	// Inject Current Message (Sanitized)
	ctxBuilder.WriteString("\nUser (")
	ctxBuilder.WriteString(privacy.Mask(sender))
	ctxBuilder.WriteString("): ")
	ctxBuilder.WriteString(privacy.SanitizeText(prompt))

	if deep {
		ctxBuilder.WriteString("\nMode: deep_reply")
	} else {
		ctxBuilder.WriteString("\nMode: short_reply")
	}

	// 5. Generate
	maskedPrompt := ctxBuilder.String()

	// Check context length again just in case (optional, but good practice)
	// We rely on pruneContext mostly.

	// Persona-specific reply templates for fallback
	personaReplies := map[string][]string{
		"LizardWatcher": {
			"The lizard people are behind it all.",
			"I saw the scales myself.",
			"Reptilian agenda confirmed.",
			"Keep your eyes peeled for cold-blooded moves.",
		},
		"FlatDave_88": {
			"The earth is flat, obviously.",
			"NASA can't fool me.",
			"Edge of the world is closer than you think.",
			"Gravity is a hoax.",
		},
		"ChemtrailSusan": {
			"Those clouds look suspicious.",
			"Aluminum in the air again!",
			"Chemtrails everywhere, stay inside.",
			"Government spraying us, as usual.",
		},
		"h4ck3rm4n": {
			"omg ping spike lol",
			"bruh, weird handshake, lowkey sus",
			"rekt? nah, just kiddie noise",
			"report: lol nothing to own here",
		},
		"LeRedditMod": {
			"Sources? This reads like a hot take.",
			"Cite your source, or it's probably OP bait.",
			"I'm skeptical — where's the evidence?",
			"Check a reputable outlet before we flame the thread.",
		},
	}

	replies := personaReplies[persona.Nick]
	if len(replies) == 0 {
		replies = []string{"Interesting theory.", "Are you sure about that?", "Let's investigate further."}
	}

	// Pick a reply for fallback
	pick := replies[rand.Intn(len(replies))]
	fallbackReply := pick

	// Fallback/Local logic integration
	if len(recent) > 0 && rand.Intn(2) == 0 {
		idx := rand.Intn(len(recent))
		recentUser := recent[idx][0]
		fallbackReply = integrateUsername(fallbackReply, recentUser, persona.Nick)
	}

	key := state.APIKey
	model := "gemini-2.5-flash-lite"
	if persona.Model != nil && *persona.Model != "" {
		model = *persona.Model
	}

	if key != "" {
		// We send the masked prompt
		out, err := CallGeminiText(key, model, persona, privacy.Mask(sender), maskedPrompt)
		if err != nil {
			LogGeminiError(err)
		} else if out != "" {
			// 6. Desanitize Output
			// The LLM might say "Entity_1 is right". We convert back to "UserA is right".
			finalReply := privacy.DesanitizeText(out)
			log.Printf("Gemini call succeeded for %s (Privacy active)", persona.Nick)
			return finalReply
		}
	}

	// ... fallback to random lines if API fails ...
	return fallbackReply
}

// CallGeminiText calls a Gemini-like REST endpoint. It expects an API key in Bearer form.
// It returns the generated text or an error. If the environment provides GEMINI_API_URL,
// that will be used; otherwise a sensible default is attempted (may vary by deployment).
func CallGeminiText(apiKey, model string, persona BotPersona, sender, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := &genai.ClientConfig{APIKey: apiKey}
	// Allow overriding the base URL if needed (useful for region/project-specific endpoints)
	if u := os.Getenv("GEMINI_API_URL"); u != "" {
		cfg.HTTPOptions = genai.HTTPOptions{BaseURL: u}
	}
	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to create Gemini client: %w", err)
	}
	// Debug: masked info (do not print key itself)
	log.Printf("[Gemini] calling model=%s keyLen=%d", model, len(apiKey))

	result, err := client.Models.GenerateContent(
		ctx,
		model,
		genai.Text(prompt),
		nil,
	)
	if err != nil {
		// Log raw error for debug (will be rate-limited by LogGeminiError elsewhere)
		log.Printf("[Gemini][debug] SDK error: %+v", err)
		return "", fmt.Errorf("gemini sdk error: %w", err)
	}
	return result.Text(), nil
}

// GetEmbedding returns the vector embedding for the given text using Gemini
func GetEmbedding(apiKey, text string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := &genai.ClientConfig{APIKey: apiKey}
	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Using embedding-001 model (or text-embedding-004 if available, sticking to 001 for safety)
	model := "text-embedding-004"

	res, err := client.Models.EmbedContent(ctx, model, genai.Text(text), nil)
	if err != nil {
		return nil, err
	}

	if res.Embeddings == nil || len(res.Embeddings) == 0 || len(res.Embeddings[0].Values) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return res.Embeddings[0].Values, nil
}

// pruneContext limits the history based on estimated character count (token proxy)
// history is [][2]string{sender, message} (newest first)
func pruneContext(history [][2]string, maxChars int) [][2]string {
	var pruned [][2]string
	currentChars := 0

	for _, entry := range history {
		// Estimate chars: sender + message + overhead
		l := len(entry[0]) + len(entry[1]) + 5
		if currentChars+l > maxChars {
			break
		}
		pruned = append(pruned, entry)
		currentChars += l
	}
	return pruned
}

// integrateUsername weaves the username into the reply in a natural way.
// It avoids parenthetical forms and avoids mentioning the bot's own nick.
func integrateUsername(reply, user, botNick string) string {
	if user == "" || user == botNick {
		return reply
	}
	// pick a natural pattern
	pats := []string{
		"%s, %s",
		"Hey %s — %s",
		"%s — noted, %s.",
		"%s — %s",
	}
	p := pats[rand.Intn(len(pats))]
	// some replies may already start with the user; avoid duplication
	if strings.HasPrefix(reply, user) || strings.Contains(reply, user) {
		return reply
	}
	return fmt.Sprintf(p, user, reply)
}
