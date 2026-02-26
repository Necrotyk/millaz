package conspiribot

// AreOpponents returns true if the two bots are considered opponents and should debate more often.
// Currently hardcoded based on known personas.
func AreOpponents(bot1, bot2 string) bool {
	// Simple map of pairs. Directionless (we check both ways).
	// "LeRedditMod" vs "FlatDave_88"
	// "LizardWatcher" vs "ChemtrailSusan" (Maybe not opponents, but let's say they fuel each other)

	pairs := []struct{ a, b string }{
		{"LeRedditMod", "FlatDave_88"},
		{"LeRedditMod", "LizardWatcher"},
		{"LeRedditMod", "ChemtrailSusan"}, // Skeptic vs Conspiracy
	}

	for _, p := range pairs {
		if (bot1 == p.a && bot2 == p.b) || (bot1 == p.b && bot2 == p.a) {
			return true
		}
	}
	return false
}
