package conspiribot

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var globalRegexCache sync.Map

// PrivacySession manages the obfuscation of nicks for a single LLM interaction context.
type PrivacySession struct {
	realToFake map[string]string
	fakeToReal map[string]string
	counter    int
	// Nicks to strictly exclude from obfuscation (e.g. the bot itself)
	excluded map[string]bool
}

// NewPrivacySession creates a session. Pass the bot's own nick to ensure it isn't masked.
func NewPrivacySession(botNick string) *PrivacySession {
	p := &PrivacySession{
		realToFake: make(map[string]string),
		fakeToReal: make(map[string]string),
		excluded:   make(map[string]bool),
		counter:    1,
	}
	p.excluded[strings.ToLower(botNick)] = true
	return p
}

// Mask returns the obfuscated alias for a given nick (e.g., "Entity_1").
// It remains consistent for the same nick within this session.
func (p *PrivacySession) Mask(nick string) string {
	if nick == "" {
		return ""
	}
	// If it's the bot, return as is
	if p.excluded[strings.ToLower(nick)] {
		return nick
	}

	// Check existing mapping
	if fake, ok := p.realToFake[nick]; ok {
		return fake
	}

	// Create new mapping
	fake := fmt.Sprintf("Entity_%d", p.counter)
	p.counter++
	p.realToFake[nick] = fake
	p.fakeToReal[fake] = nick
	return fake
}

// SanitizeText replaces all known occurrences of real nicks in the text with their aliases.
// Note: This does a simple word-boundary replacement for nicks currently in the mapping.
func (p *PrivacySession) SanitizeText(text string) string {
	// We sort nicks by length (descending) to avoid partial replacement issues
	var nicks []string
	for n := range p.realToFake {
		nicks = append(nicks, n)
	}
	sort.Slice(nicks, func(i, j int) bool {
		return len(nicks[i]) > len(nicks[j])
	})

	out := text
	for _, n := range nicks {
		fake := p.realToFake[n]
		// Use regex word boundaries `\b` to avoid replacing substrings (e.g. "Cat" in "Catapult")
		// However, \b only matches between \w and \W. If the nick contains symbols (e.g., "[Admin]"),
		// \b might not match where we expect.
		// We construct a custom pattern:
		// (?i)(?:^|\W)NICK(?:$|\W) -- looking for NICK surrounded by non-word chars or start/end of string.
		// But wait, if NICK starts with non-word char, `\W` before it might not match if it's preceded by another non-word char?
		// Actually, we want to ensure we are not inside a word.
		// If nick is "Dave", we don't want to match "SuperDave".
		// If nick is "[Admin]", we don't want to match "Super[Admin]".

		// Let's stick to a robust approach:
		// Construct regex: (?i)(^|[^a-zA-Z0-9_])(ESCAPED_NICK)($|[^a-zA-Z0-9_])
		// We use groups to preserve the surrounding delimiters.

		// Note: The previous implementation `\b%s\b` fails for "[Admin]" because `[` is not a word char,
		// so `\b` expects the previous char to be a word char (or vice versa).

		var re *regexp.Regexp
		if val, ok := globalRegexCache.Load(n); ok {
			if val != nil {
				re = val.(*regexp.Regexp)
			}
		} else {
			escaped := regexp.QuoteMeta(n)
			pattern := fmt.Sprintf(`(^|[^a-zA-Z0-9_])(%s)($|[^a-zA-Z0-9_])`, escaped)
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				globalRegexCache.Store(n, nil)
			} else {
				re = compiled
				globalRegexCache.Store(n, re)
			}
		}

		if re != nil {
			// We must loop because the regex consumes the delimiters (e.g. space),
			// preventing adjacent matches from being found in a single pass.
			// e.g. " nick nick " -> first match consumes first space and "nick" and second space.
			// The remaining string starts after the second space, so the second "nick" has no leading space to match.
			for {
				newOut := re.ReplaceAllString(out, "${1}"+fake+"${3}")
				if newOut == out {
					break
				}
				out = newOut
			}
		} else {
			// Fallback
			out = strings.ReplaceAll(out, n, fake)
		}
	}
	return out
}

// DesanitizeText reverses the process, replacing aliases with real nicks.
func (p *PrivacySession) DesanitizeText(text string) string {
	// Sort fakes (Entity_10 before Entity_1)
	var fakes []string
	for f := range p.fakeToReal {
		fakes = append(fakes, f)
	}
	sort.Slice(fakes, func(i, j int) bool {
		return len(fakes[i]) > len(fakes[j])
	})

	out := text
	for _, f := range fakes {
		real := p.fakeToReal[f]
		// Entity_X matches word boundaries naturally, but safer to use simple replace
		// since we generated them.
		out = strings.ReplaceAll(out, f, real)
	}
	return out
}
