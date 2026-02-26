package conspiribot

import (
	"testing"
)

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		s1       string
		s2       string
		expected int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"kitten", "sitting", 3},
		{"book", "back", 2},
		{"hello", "world", 4},
		// Unicode
		{"你好", "你好", 0},
		{"你好", "好", 1},
		{"Café", "Cafe", 1}, // é vs e
	}

	for _, tt := range tests {
		t.Run(tt.s1+"_vs_"+tt.s2, func(t *testing.T) {
			result := levenshtein(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("levenshtein(%q, %q) = %d; want %d", tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func BenchmarkLevenshtein(b *testing.B) {
	s1 := "kitten"
	s2 := "sitting"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		levenshtein(s1, s2)
	}
}

func BenchmarkLevenshteinLong(b *testing.B) {
	s1 := "The quick brown fox jumps over the lazy dog"
	s2 := "The quick brown fox jumps over the lazy cat"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		levenshtein(s1, s2)
	}
}
