package output

import (
	"io"
	"strconv"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/lc/gau/v2/pkg/provenance"
)

func BenchmarkLegacyWriteURLs(b *testing.B) {
	const n = 50000
	urls := make([]string, n)
	for i := range urls {
		urls[i] = "https://example.com/r/" + strconv.Itoa(i)
	}
	bl := mapset.NewThreadUnsafeSet[string]()
	b.ReportAllocs()
	b.ResetTimer()
	for k := 0; k < b.N; k++ {
		ch := make(chan string, 1024)
		done := make(chan struct{})
		go func() {
			_ = WriteURLs(io.Discard, ch, bl, false)
			close(done)
		}()
		for _, u := range urls {
			ch <- u
		}
		close(ch)
		<-done
	}
}

func BenchmarkProvenanceWriteText(b *testing.B) {
	const n = 50000
	c, err := provenance.NewCollector("none", 3, []string{"d.com"}, []string{"p1", "p2", "p3", "p4"})
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		c.Observe("d.com", "p"+strconv.Itoa(1+i%4), "https://example.com/r/"+strconv.Itoa(i))
	}
	rep := c.Finalize()
	bl := mapset.NewThreadUnsafeSet[string]()
	b.ReportAllocs()
	b.ResetTimer()
	for k := 0; k < b.N; k++ {
		if err := WriteProvenance(io.Discard, rep, bl, false, false); err != nil {
			b.Fatal(err)
		}
	}
}
