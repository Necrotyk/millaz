package conspiribot

import (
	"testing"
)

func TestAreOpponents(t *testing.T) {
	tests := []struct {
		bot1     string
		bot2     string
		expected bool
	}{
		// Positive cases (hardcoded pairs)
		{"LeRedditMod", "FlatDave_88", true},
		{"FlatDave_88", "LeRedditMod", true}, // Symmetric
		{"LeRedditMod", "LizardWatcher", true},
		{"LizardWatcher", "LeRedditMod", true},
		{"LeRedditMod", "ChemtrailSusan", true},
		{"ChemtrailSusan", "LeRedditMod", true},

		// Negative cases
		{"FlatDave_88", "LizardWatcher", false}, // Both in list but not a pair
		{"LizardWatcher", "ChemtrailSusan", false},
		{"Nobody", "LeRedditMod", false},
		{"LeRedditMod", "Nobody", false},
		{"SomeBot", "OtherBot", false},
		{"", "", false},
		{"LeRedditMod", "LeRedditMod", false},
	}

	for _, tt := range tests {
		t.Run(tt.bot1+"_vs_"+tt.bot2, func(t *testing.T) {
			result := AreOpponents(tt.bot1, tt.bot2)
			if result != tt.expected {
				t.Errorf("AreOpponents(%q, %q) = %v; want %v", tt.bot1, tt.bot2, result, tt.expected)
			}
		})
	}
}
