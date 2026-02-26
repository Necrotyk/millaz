package conspiribot

import (
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// IRCProvider defines the interface for interacting with the main IRC server
type IRCProvider interface {
	SendPrivmsg(channel, message string) error
	SendAction(channel, message string) error
} // ServerConfig is removed

type Bot struct {
	State         *SwarmState
	Persona       BotPersona
	scheduler     *SpeakScheduler
	sendMessageFn func(string) error
	enabledMu     sync.RWMutex
	enabled       bool
	// lastSpoke tracks when this bot last sent a message (for per-bot cooldowns)
	lastSpokeMu sync.Mutex
	lastSpoke   time.Time
}

// NewBot creates a new bot instance
func NewBot(state *SwarmState, persona BotPersona, scheduler *SpeakScheduler) *Bot {
	b := &Bot{
		State:     state,
		Persona:   persona,
		scheduler: scheduler,
	}

	// default enabled true unless explicitly set to false
	if persona.Enabled != nil && !*persona.Enabled {
		b.enabled = false
	} else {
		b.enabled = true
	}

	// register in global registry for admin control
	RegisterBot(b)
	return b
}

// sendTo sends a message to a given channel using the bot's IRC client
func (b *Bot) sendTo(channel, msg string, provider IRCProvider) error {
	if provider == nil {
		return fmt.Errorf("no connection")
	}
	if strings.HasPrefix(msg, "\x01ACTION ") && strings.HasSuffix(msg, "\x01") {
		content := strings.TrimSuffix(strings.TrimPrefix(msg, "\x01ACTION "), "\x01")
		return provider.SendAction(channel, content)
	}
	return provider.SendPrivmsg(channel, msg)
}

func (b *Bot) setEnabled(v bool) {
	b.enabledMu.Lock()
	defer b.enabledMu.Unlock()
	b.enabled = v
}

func (b *Bot) IsEnabled() bool {
	b.enabledMu.RLock()
	defer b.enabledMu.RUnlock()
	return b.enabled
}

// canSpeak checks per-bot cooldown and returns true if the bot may send now
func (b *Bot) canSpeak() bool {
	cooldown := time.Duration(b.Persona.ReplyCooldownSeconds) * time.Second
	if cooldown == 0 {
		cooldown = 10 * time.Second // default per-bot cooldown
	}
	b.lastSpokeMu.Lock()
	defer b.lastSpokeMu.Unlock()
	if time.Since(b.lastSpoke) < cooldown {
		return false
	}
	return true
}

// recordSpeak updates the lastSpoke timestamp after sending
func (b *Bot) recordSpeak() {
	b.lastSpokeMu.Lock()
	b.lastSpoke = time.Now()
	b.lastSpokeMu.Unlock()
}

// ProcessMessage handles an incoming IRC message from Millaz
func ProcessMessage(sender, channel, message string, provider IRCProvider) {
	// Deduplicated receive log (only first of near-simultaneous receives will print)
	LogReceived(sender, message)

	// Admin commands check (can be checked first universally, not bot-specific)
	if AppConfig != nil && sender == AppConfig.AdminNick && strings.HasPrefix(message, "!bot ") {
		cmd := strings.TrimSpace(strings.TrimPrefix(message, "!bot "))
		switch cmd {
		case "mute all":
			for _, other := range AllBots() {
				other.setEnabled(false)
			}
			provider.SendPrivmsg(channel, "All bots muted (speaking disabled).")
		case "unmute all":
			for _, other := range AllBots() {
				other.setEnabled(true)
			}
			provider.SendPrivmsg(channel, "All bots unmuted (speaking enabled).")
		case "status":
			var parts []string
			for _, other := range AllBots() {
				parts = append(parts, fmt.Sprintf("%s: %v", other.Persona.Nick, other.IsEnabled()))
			}
			provider.SendPrivmsg(channel, "Bot status: "+strings.Join(parts, "; "))
		default:
			provider.SendPrivmsg(channel, "Unknown admin command. Supported: mute all, unmute all, status")
		}
		return
	}

	for _, b := range AllBots() {
		if b.Persona.Type == "utility" {
			// Utility bot logic
			UtilityHandler(b, sender, channel, message, provider)
			continue
		}

		matched := false
		for _, t := range b.Persona.Triggers {
			if strings.Contains(strings.ToLower(message), strings.ToLower(t)) {
				if sender != b.Persona.Nick {
					matched = true
					log.Printf("[%s] Trigger present: '%s' in '%s'", b.Persona.Nick, t, message)
					break
				}
			}
		}

		if !b.IsEnabled() {
			continue
		}

		prob := b.Persona.ReplyProbability
		if prob <= 0 {
			prob = 0.35
		}

		if IsBot(sender) {
			if AreOpponents(b.Persona.Nick, sender) {
				prob *= 1.5
				if prob > 0.8 {
					prob = 0.8
				}
			} else {
				prob *= 0.5
			}
		}

		if !matched && rand.Float64() > prob {
			continue
		}

		if !b.canSpeak() {
			continue
		}

		go b.generateAndSpeak(sender, message, channel, provider)
	}
}

// (Old irc-client handler removed; go-ircevent callbacks are used instead.)

// generateAndSpeak generates a reply for `prompt` and schedules it via the shared scheduler.
func (b *Bot) generateAndSpeak(sender, prompt, channel string, provider IRCProvider) {
	if !b.IsEnabled() {
		return
	}

	// Gather recent history for context
	recent, _ := GetRecentHistory(b.State, 12, channel)

	// Check if prompt contains any trigger for deep reply
	deep := false
	for _, t := range b.Persona.Triggers {
		if strings.Contains(strings.ToLower(prompt), strings.ToLower(t)) {
			// only the first bot shall do the deep tangent for this message
			if IsFirstBotToTrigger(sender, prompt) {
				deep = true
			}
			break
		}
	}

	// Retrieve compact memory for the bot to include in prompt (limit tokens by chars)
	memory, _ := GetMemorySummary(b.State, b.Persona.Nick, channel, 20, 1000)

	// Generate reply using the lightweight LLM (may call Gemini)
	reply := GenerateReply(b.State, b.Persona, sender, prompt, recent, memory, deep)

	// PARSE ACTIONS:
	// If the reply starts with * and ends with *, treat it as an action.
	// e.g., "*adjusts tin foil hat*" -> CTCP ACTION
	if strings.HasPrefix(reply, "*") && strings.HasSuffix(reply, "*") {
		content := strings.Trim(reply, "* ")
		// \x01 is the CTCP delimiter
		reply = fmt.Sprintf("\x01ACTION %s\x01", content)
	}

	// Avoid repetition: do a simple check against recent messages
	for _, r := range recent {
		if r[1] == reply {
			// modify slightly
			reply = reply + " (hm)"
			break
		}
	}

	// Simulate typing delay based on message length (not too long)
	ms := 200 + len(reply)*30
	if ms > 8000 {
		ms = 8000
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)

	// Add a small randomized delay to stagger multiple bot replies naturally
	jitter := time.Duration(200+rand.Intn(1800)) * time.Millisecond
	time.Sleep(jitter)

	// Enqueue via scheduler to avoid floods
	if b.scheduler != nil {
		if err := b.scheduler.Enqueue(b, channel, reply, provider); err != nil {
			log.Printf("[%s] failed to enqueue message: %v", b.Persona.Nick, err)
			// fallback to direct send
			if err := b.sendTo(channel, reply, provider); err != nil {
				log.Printf("[%s] fallback send error: %v", b.Persona.Nick, err)
			}
		}
	} else {
		if err := b.sendTo(channel, reply, provider); err != nil {
			log.Printf("[%s] direct send error: %v", b.Persona.Nick, err)
		}
	}

	// Log the bot's message (channel-specific)
	if err := LogMessage(b.State, time.Now().Format(time.RFC3339), b.Persona.Nick, reply, channel); err != nil {
		log.Printf("[%s] failed to log message to DB: %v", b.Persona.Nick, err)
	}

	// Save the bot's reply into its memory as well (helps continuity)
	if err := SaveMemory(b.State, b.Persona.Nick, reply, channel); err != nil {
		log.Printf("[%s] failed to save reply to memory: %v", b.Persona.Nick, err)
	}
}
