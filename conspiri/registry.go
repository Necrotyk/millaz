package conspiribot

import (
	"sync"
	"time"
)

var (
	registryMu    sync.RWMutex
	registry      = map[string]*Bot{}
	lastTriggerMu sync.Mutex
	// lastTriggers stores the timestamp of recent triggers keyed by sender:message
	lastTriggers = map[string]time.Time{}
	triggerTTL   = 2 * time.Second
)

// RegisterBot registers a bot in the global registry
func RegisterBot(b *Bot) {
	if b == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[b.Persona.Nick] = b
}

// AllBots returns a snapshot slice of all registered bots
func AllBots() []*Bot {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*Bot, 0, len(registry))
	for _, b := range registry {
		out = append(out, b)
	}
	return out
}

// GetBot returns a bot by nick, or nil
func GetBot(nick string) *Bot {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[nick]
}

// IsBot returns true if the nick belongs to a registered bot
func IsBot(nick string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[nick]
	return ok
}

// IsFirstBotToTrigger ensures only one bot replies to a channel message per event
func IsFirstBotToTrigger(sender, message string) bool {
	key := sender + ":" + message
	now := time.Now()

	lastTriggerMu.Lock()
	defer lastTriggerMu.Unlock()

	// cleanup expired entries
	for k, t := range lastTriggers {
		if now.Sub(t) > triggerTTL {
			delete(lastTriggers, k)
		}
	}

	if t, ok := lastTriggers[key]; ok && now.Sub(t) <= triggerTTL {
		return false
	}
	lastTriggers[key] = now
	return true
}
