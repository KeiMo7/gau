package history

import (
	"fmt"
	"testing"

	"github.com/lc/gau/v2/pkg/providers"
)

var sink int

// BenchmarkBaselineChannel mirrors the pre-change hot path: a tagged result
// is relayed with no history work attached.
func BenchmarkBaselineChannel(b *testing.B) {
	results := make(chan providers.Result, 1024)
	done := make(chan struct{})
	go func() {
		n := 0
		for r := range results {
			n += len(r.URL)
		}
		sink = n
		close(done)
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results <- providers.Result{URL: fmt.Sprintf("https://example.com/path/%d?a=1&b=2", i%10000), Provider: "wayback", Domain: "example.com"}
	}
	close(results)
	<-done
}

// BenchmarkHistoryChannel adds exactly the per-URL work the feature performs
// on the hot path: Normalize + locked collector merge, in addition to the
// relay.
func BenchmarkHistoryChannel(b *testing.B) {
	results := make(chan providers.Result, 1024)
	c := NewCollector()
	done := make(chan struct{})
	go func() {
		n := 0
		for r := range results {
			n += len(r.URL)
			c.Add(r.URL, r.Provider)
		}
		sink = n
		close(done)
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results <- providers.Result{URL: fmt.Sprintf("https://example.com/path/%d?a=1&b=2", i%10000), Provider: "wayback", Domain: "example.com"}
	}
	close(results)
	<-done
}
