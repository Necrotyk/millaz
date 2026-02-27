package conspiribot

import (
	"context"
	"sync"
	"time"
)

// SpeakRequest represents a bot's request to speak
type SpeakRequest struct {
	Bot      *Bot
	Message  string
	Channel  string
	Provider IRCProvider
	Done     chan error
}

// SpeakScheduler serializes outgoing messages and enforces cooldowns
type SpeakScheduler struct {
	queue    chan SpeakRequest
	cooldown time.Duration
	maxQueue int
	// recentMessages holds recent messages per channel to avoid sending exact duplicates
	mu             sync.Mutex
	recentMessages map[string][]recentMsg
}

type recentMsg struct {
	msg string
	at  time.Time
}

// NewSpeakScheduler creates a scheduler with a cooldown between messages and queue size
func NewSpeakScheduler(ctx context.Context, cooldown time.Duration, maxQueue int) *SpeakScheduler {
	s := &SpeakScheduler{
		queue:          make(chan SpeakRequest, maxQueue),
		cooldown:       cooldown,
		maxQueue:       maxQueue,
		recentMessages: make(map[string][]recentMsg),
	}

	go s.loop(ctx)
	return s
}

func (s *SpeakScheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(s.cooldown)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case req := <-s.queue:
			if req.Bot == nil {
				req.Done <- nil
				continue
			}

			// Deliver message via bot's sendTo method
			err := req.Bot.sendTo(req.Channel, req.Message, req.Provider)
			if err != nil {
				req.Bot.State.Logger.Error("scheduler send error", "bot", req.Bot.Persona.Nick, "error", err)
				req.Done <- err
			} else {
				// record that the bot spoke (per-bot cooldown)
				req.Bot.recordSpeak()
				// record recent message for deduplication
				s.mu.Lock()
				lst := s.recentMessages[req.Channel]
				lst = append([]recentMsg{{msg: req.Message, at: time.Now()}}, lst...)
				if len(lst) > 20 {
					lst = lst[:20]
				}
				s.recentMessages[req.Channel] = lst
				s.mu.Unlock()
				req.Done <- nil
			}

			// Wait cooldown before allowing next message
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}
}

// Enqueue asks the scheduler to send a message; blocks if the queue is full
func (s *SpeakScheduler) Enqueue(bot *Bot, channel, message string, provider IRCProvider) error {
	// Protect against duplicate messages recently sent to the same channel.
	// If the exact message was sent within the last 60s, try to apply a small variation.
	s.mu.Lock()
	now := time.Now()
	if s.recentMessages == nil {
		s.recentMessages = make(map[string][]recentMsg)
	}
	var seen bool
	if lst, ok := s.recentMessages[channel]; ok {
		for _, r := range lst {
			// Check exact match or Levenshtein distance for fuzzy match
			if now.Sub(r.at) < 60*time.Second {
				if r.msg == message || isSimilar(r.msg, message) {
					seen = true
					break
				}
			}
		}
	}
	s.mu.Unlock()

	if seen {
		// apply a tiny variation to avoid exact duplicates (non-destructive)
		variants := []string{"", "", " (again)", " — hmm", " ?", " :/"}
		// pick a random small suffix (we can't import math/rand here; use time seed)
		idx := int(time.Now().UnixNano() % int64(len(variants)))
		message = message + variants[idx]
	}

	done := make(chan error, 1)
	req := SpeakRequest{Bot: bot, Channel: channel, Message: message, Provider: provider, Done: done}
	s.queue <- req
	return <-done
}
