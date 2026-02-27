package conspiribot

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

var (
	recvMu sync.Mutex
	// store recent seen keys and their timestamp
	recvSeen = map[string]time.Time{}
	recvTTL  = 2 * time.Second
)

var (
	geminiErrMu   sync.Mutex
	geminiErrSeen = map[string]time.Time{}
	geminiErrTTL  = 30 * time.Second
)

// LogReceived deduplicates nearly-simultaneous receipt logs so we don't spam one line per bot.
// It returns true if this call performed the log (first occurrence), false otherwise.
func LogReceived(logger *slog.Logger, sender, message string) bool {
	key := sender + ":" + message
	now := time.Now()

	recvMu.Lock()
	defer recvMu.Unlock()

	// cleanup expired entries
	for k, ts := range recvSeen {
		if now.Sub(ts) > recvTTL {
			delete(recvSeen, k)
		}
	}

	if t, ok := recvSeen[key]; ok && now.Sub(t) <= recvTTL {
		return false
	}
	recvSeen[key] = now
	logger.Info("<Recv>", "sender", sender, "message", message)
	return true
}

// LogGeminiError logs Gemini-related errors but rate-limits identical messages
// to avoid spamming the console when the API consistently returns the same error.
func LogGeminiError(logger *slog.Logger, err error) {
	if err == nil {
		return
	}
	s := strings.TrimSpace(err.Error())

	geminiErrMu.Lock()
	defer geminiErrMu.Unlock()

	now := time.Now()
	// cleanup
	for k, ts := range geminiErrSeen {
		if now.Sub(ts) > geminiErrTTL {
			delete(geminiErrSeen, k)
		}
	}

	if t, ok := geminiErrSeen[s]; ok && now.Sub(t) <= geminiErrTTL {
		return
	}
	geminiErrSeen[s] = now
	// Provide helpful, concise guidance for common failures
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "status 401") || strings.Contains(low, "unauthenticated"):
		logger.Error("authentication failed (401). Check your GEMINI_API_KEY or .geminikey contents; falling back to local reply", "component", "Gemini")
	case strings.Contains(low, "status 404") || strings.Contains(low, "not_found") || strings.Contains(low, "requested entity was not found"):
		logger.Error("model or endpoint not found (404). Verify GEMINI_API_URL and model name; falling back to local reply", "component", "Gemini")
	default:
		// Only log a short summary to avoid dumping large JSON bodies
		firstLine := s
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			firstLine = s[:idx]
		}
		logger.Error("falling back to local reply", "component", "Gemini", "error", firstLine)
	}
}
