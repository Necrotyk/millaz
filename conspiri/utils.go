package conspiribot

import (
	"strings"
)

// isSimilar returns true if s1 and s2 are very similar (based on Levenshtein distance).
// It considers them similar if the edit distance is small relative to the string length.
func isSimilar(s1, s2 string) bool {
	// Simple pre-checks
	if s1 == s2 {
		return true
	}

	// Normalize: lower case and trim
	s1 = strings.ToLower(strings.TrimSpace(s1))
	s2 = strings.ToLower(strings.TrimSpace(s2))

	if s1 == s2 {
		return true
	}

	dist := levenshtein(s1, s2)
	maxLen := len(s1)
	if len(s2) > maxLen {
		maxLen = len(s2)
	}

	if maxLen == 0 {
		return true
	}

	// Calculate similarity ratio
	// If distance is less than 20% of length, consider it a duplicate
	// Also if distance is <= 2 and length > 4, consider it a duplicate (handles punctuation)
	if dist <= 2 && maxLen > 4 {
		return true
	}

	ratio := float64(dist) / float64(maxLen)
	return ratio < 0.20
}

// levenshtein calculates the Levenshtein distance between two strings
func levenshtein(s1, s2 string) int {
	r1, r2 := []rune(s1), []rune(s2)

	// Ensure r1 is the shorter string to minimize memory usage (O(min(N, M)))
	if len(r1) > len(r2) {
		r1, r2 = r2, r1
	}

	len1 := len(r1)
	len2 := len(r2)

	v0 := make([]int, len1+1)
	v1 := make([]int, len1+1)

	for i := 0; i <= len1; i++ {
		v0[i] = i
	}

	for i := 0; i < len2; i++ {
		v1[0] = i + 1
		for j := 0; j < len1; j++ {
			cost := 0
			if r1[j] != r2[i] {
				cost = 1
			}
			v1[j+1] = min(v1[j]+1, min(v0[j+1]+1, v0[j]+cost))
		}
		v0, v1 = v1, v0
	}

	return v0[len1]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
