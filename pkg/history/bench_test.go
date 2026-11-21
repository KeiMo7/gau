package history

import (
	"fmt"
	"sync"
	"testing"
)

// StressCollector hammers the collector from many goroutines to surface any
// data race or map corruption (run with -race when cgo is available).
func TestStressCollector(t *testing.T) {
	c := NewCollector()
	const goroutines = 200
	const perG = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				c.Add(fmt.Sprintf("https://example.com/p%d?b=1&a=2", i%50), "wayback")
			}
		}(g)
	}
	wg.Wait()
	snap := c.Snapshot()
	if len(snap) != 50 {
		t.Fatalf("expected 50 deduped identities under concurrency, got %d", len(snap))
	}
}

func BenchmarkNormalize(b *testing.B) {
	raw := "https://user@WWW.Example.com:80/a%7eb/path?z=9&a=1&m=hello%20world#frag"
	for i := 0; i < b.N; i++ {
		_ = Normalize(raw)
	}
}

func BenchmarkDiff(b *testing.B) {
	prev := makeRoundForBench(1, 10000, 0)
	cur := makeRoundForBench(2, 10500, 200) // 700 added, 200 removed
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Diff(prev, cur)
	}
}

func makeRoundForBench(id, n, offset int) *Round {
	entries := make([]Entry, 0, n)
	for i := offset; i < offset+n; i++ {
		entries = append(entries, entry(
			fmt.Sprintf("https://example.com/path/%d", i),
			[]string{"q"},
			"example.com",
			"sample",
			"wayback",
		))
	}
	return round(id, entries)
}
