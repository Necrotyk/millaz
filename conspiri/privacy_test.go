package conspiribot

import (
	"testing"
)

func TestSanitizeText(t *testing.T) {
	p := NewPrivacySession("Bot")
	p.Mask("Alice")
	p.Mask("Bob")
	p.Mask("[Admin]")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple replacement",
			input:    "Hello Alice",
			expected: "Hello Entity_1",
		},
		{
			name:     "Multiple nicks",
			input:    "Alice and Bob",
			expected: "Entity_1 and Entity_2",
		},
		{
			name:     "Adjacent nicks",
			input:    " Alice Alice ",
			expected: " Entity_1 Entity_1 ",
		},
		{
			name:     "Nick with special characters",
			input:    "Hello [Admin]",
			expected: "Hello Entity_3",
		},
		{
			name:     "Sub-word avoidance",
			input:    "AliceAlpaca",
			expected: "AliceAlpaca",
		},
		{
			name: "Case insensitive nick match (if supported by logic)",
			// Note: The current logic uses nicks from p.realToFake keys.
			// Let's see if it's case insensitive.
			// The regex uses (%s), and it doesn't have (?i).
			// So it's probably case sensitive for now.
			input:    "hello alice",
			expected: "hello alice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.SanitizeText(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeText() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMask(t *testing.T) {
	botNick := "MyBot"
	p := NewPrivacySession(botNick)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Empty string", "", ""},
		{"Bot nick exact", "MyBot", "MyBot"},
		{"Bot nick case insensitive", "mybot", "mybot"},
		{"Bot nick upper", "MYBOT", "MYBOT"},
		{"New nick", "Alice", "Entity_1"},
		{"Same nick repeated", "Alice", "Entity_1"},
		{"Different nick", "Bob", "Entity_2"},
		{"Case sensitive mapping (lower)", "alice", "Entity_3"},
		{"Case sensitive mapping (upper)", "ALICE", "Entity_4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Mask(tt.input)
			if got != tt.expected {
				t.Errorf("Mask(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func BenchmarkSanitizeText(b *testing.B) {
	p := NewPrivacySession("Bot")
	nicks := []string{"Alice", "Bob", "Charlie", "Dave", "Eve", "Frank", "Grace", "Heidi", "Ivan", "Judy"}
	for _, n := range nicks {
		p.Mask(n)
	}

	text := "Alice and Bob went to see Charlie. Dave was already there with Eve and Frank. Grace, Heidi, Ivan, and Judy stayed home."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.SanitizeText(text)
	}
}

func BenchmarkSanitizeTextMultipleLines(b *testing.B) {
	p := NewPrivacySession("Bot")
	nicks := []string{"Alice", "Bob", "Charlie", "Dave", "Eve", "Frank", "Grace", "Heidi", "Ivan", "Judy"}
	for _, n := range nicks {
		p.Mask(n)
	}

	lines := []string{
		"Alice: Hello Bob",
		"Bob: Hi Alice, have you seen Charlie?",
		"Alice: No, but Dave said he's with Eve.",
		"Charlie: I'm here!",
		"Frank: Where is Grace?",
		"Heidi: She's with Ivan and Judy.",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			p.SanitizeText(line)
		}
	}
}
