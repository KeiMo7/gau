package provenance

import (
	"encoding/json"
	"testing"
)

// BenchmarkObserve measures the per-observation cost added in the fetch
// path: normalize + mutex-protected map insert.
func BenchmarkObserve(b *testing.B) {
	c, err := NewCollector("none", 3, []string{"d.com"}, []string{"p1", "p2", "p3", "p4"})
	if err != nil {
		b.Fatal(err)
	}
	urls := make([]string, 1000)
	for i := range urls {
		urls[i] = "https://example.com/path/segment/resource?id=" +
			string(rune('a'+i%26)) + string(rune('A'+(i/26)%26)) + "&v=" +
			itoa(i) + "#section"
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		c.Observe("d.com", "p"+itoa(1+n%4), urls[n%len(urls)])
	}
}

// BenchmarkNormalize isolates the normalization cost (the one new CPU step
// applied to every URL in provenance mode).
func BenchmarkNormalize(b *testing.B) {
	raw := "HTTP://EXAMPLE.com:80/path/segment/resource?id=abc&v=42#section"
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, err := Normalize(raw); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFinalize measures end-of-run merge + sort over a realistic
// 50k-URL / 4-provider dataset.
func BenchmarkFinalize(b *testing.B) {
	const nURLs = 50000
	c, err := NewCollector("none", 3, []string{"d.com"}, []string{"p1", "p2", "p3", "p4"})
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < nURLs; i++ {
		u := "https://example.com/r/" + itoa(i)
		c.Observe("d.com", "p"+itoa(1+i%4), u)
	}
	for _, p := range []string{"p1", "p2", "p3", "p4"} {
		c.Complete("d.com", p, false, nil)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		rep := c.Finalize()
		if len(rep.Records) != nURLs {
			b.Fatalf("got %d records", len(rep.Records))
		}
	}
}

// BenchmarkJSONRender is the output-layer cost for the same 50k dataset.
func BenchmarkJSONRender(b *testing.B) {
	const nURLs = 50000
	c, _ := NewCollector("none", 3, []string{"d.com"}, []string{"p1", "p2", "p3", "p4"})
	for i := 0; i < nURLs; i++ {
		u := "https://example.com/r/" + itoa(i)
		c.Observe("d.com", "p"+itoa(1+i%4), u)
	}
	for _, p := range []string{"p1", "p2", "p3", "p4"} {
		c.Complete("d.com", p, false, nil)
	}
	rep := c.Finalize()
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, err := json.Marshal(rep); err != nil {
			b.Fatal(err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
