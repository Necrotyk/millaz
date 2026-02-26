package conspiribot

import (
	"math/rand"
	"sort"
	"testing"
)

// benchResult mimics the internal struct used in database.go
type benchResult struct {
	content string
	score   float32
}

func generateResults(n int) []benchResult {
	results := make([]benchResult, n)
	for i := 0; i < n; i++ {
		results[i] = benchResult{
			content: "test",
			score:   rand.Float32(),
		}
	}
	return results
}

func BenchmarkBubbleSort(b *testing.B) {
	// Setup consistent data
	rand.Seed(42)
	baseData := generateResults(1000)

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		// Copy data so each iteration sorts a fresh slice
		results := make([]benchResult, len(baseData))
		copy(results, baseData)

		// Bubble Sort Implementation from database.go
		for i := 0; i < len(results); i++ {
			for j := i + 1; j < len(results); j++ {
				if results[j].score > results[i].score {
					results[i], results[j] = results[j], results[i]
				}
			}
		}
	}
}

func BenchmarkSortSlice(b *testing.B) {
	// Setup consistent data
	rand.Seed(42)
	baseData := generateResults(1000)

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		// Copy data so each iteration sorts a fresh slice
		results := make([]benchResult, len(baseData))
		copy(results, baseData)

		// Optimized Sort Implementation
		sort.Slice(results, func(i, j int) bool {
			return results[i].score > results[j].score
		})
	}
}

func BenchmarkBubbleSortLarge(b *testing.B) {
	// Setup consistent data
	rand.Seed(42)
	baseData := generateResults(10000)

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		// Copy data so each iteration sorts a fresh slice
		results := make([]benchResult, len(baseData))
		copy(results, baseData)

		// Bubble Sort Implementation from database.go
		for i := 0; i < len(results); i++ {
			for j := i + 1; j < len(results); j++ {
				if results[j].score > results[i].score {
					results[i], results[j] = results[j], results[i]
				}
			}
		}
	}
}

func BenchmarkSortSliceLarge(b *testing.B) {
	// Setup consistent data
	rand.Seed(42)
	baseData := generateResults(10000)

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		// Copy data so each iteration sorts a fresh slice
		results := make([]benchResult, len(baseData))
		copy(results, baseData)

		// Optimized Sort Implementation
		sort.Slice(results, func(i, j int) bool {
			return results[i].score > results[j].score
		})
	}
}
