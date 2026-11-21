package runner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lc/gau/v2/pkg/provenance"
	"github.com/lc/gau/v2/pkg/providers"
)

type fakeProvider struct {
	name string
	urls []string
	err  error
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Fetch(_ context.Context, _ string, results chan string) error {
	if f.err != nil {
		// a failing upstream may still have streamed partial data first;
		// those observations must be retained and attributed correctly.
		for _, u := range f.urls {
			results <- u
		}
		return f.err
	}
	for _, u := range f.urls {
		results <- u
	}
	return nil
}

type boom struct{}

func (boom) Error() string { return "connection reset by peer" }

func runProvenanceOnce(t *testing.T, provs []providers.Provider, domains []string, threads uint) *provenance.Report {
	t.Helper()
	names := make([]string, len(provs))
	for i, p := range provs {
		names[i] = p.Name()
	}
	collector, err := provenance.NewCollector("none", 3, domains, names)
	if err != nil {
		t.Fatal(err)
	}
	collector.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })

	r := &Runner{threads: threads}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workChan := make(chan Work)
	r.StartWithSources(ctx, workChan, collector)
	for _, p := range provs {
		for _, d := range domains {
			workChan <- NewWork(d, p)
		}
	}
	close(workChan)
	r.Wait()
	return collector.Finalize()
}

func TestRunnerSingleSource(t *testing.T) {
	rep := runProvenanceOnce(t,
		[]providers.Provider{&fakeProvider{name: "wayback", urls: []string{"http://d.com/a", "http://d.com/b"}}},
		[]string{"d.com"}, 2)
	if len(rep.Records) != 2 {
		t.Fatalf("want 2 records, got %d", len(rep.Records))
	}
	for _, rec := range rep.Records {
		if len(rec.Sources) != 1 {
			t.Errorf("%s: want exactly 1 source, got %d", rec.URL, len(rec.Sources))
		}
	}
}

func TestRunnerMultiSourceAttributionUnderConcurrency(t *testing.T) {
	// Three providers, overlapping URLs, many workers: attribution must be
	// exact regardless of goroutine scheduling.
	p1 := &fakeProvider{name: "p1"}
	p2 := &fakeProvider{name: "p2"}
	p3 := &fakeProvider{name: "p3"}
	for i := 0; i < 100; i++ {
		p1.urls = append(p1.urls, "http://d.com/shared")
		p2.urls = append(p2.urls, "http://d.com/shared")
		if i%2 == 0 {
			p3.urls = append(p3.urls, "http://d.com/shared")
		}
		p1.urls = append(p1.urls, "http://d.com/only-p1")
	}
	rep := runProvenanceOnce(t, []providers.Provider{p1, p2, p3}, []string{"d.com"}, 8)

	byURL := map[string]provenance.Record{}
	for _, rec := range rep.Records {
		byURL[rec.URL] = rec
	}
	shared := byURL["http://d.com/shared"]
	if len(shared.Sources) != 3 {
		t.Fatalf("shared URL sources = %v, want all three", sourceNames(shared))
	}
	only := byURL["http://d.com/only-p1"]
	if len(only.Sources) != 1 {
		t.Fatalf("exclusive URL leaked to other sources: %v", sourceNames(only))
	}
	// priority: the 3-source shared URL must sort before the 1-source one.
	if rep.Records[0].URL != "http://d.com/shared" {
		t.Errorf("multi-source URL must rank first, got %q", rep.Records[0].URL)
	}
}

func TestRunnerDeterministicAcrossConcurrentRuns(t *testing.T) {
	build := func() []byte {
		rep := runProvenanceOnce(t,
			[]providers.Provider{
				&fakeProvider{name: "p1", urls: []string{"http://d.com/z", "http://d.com/a", "http://d.com/m"}},
				&fakeProvider{name: "p2", urls: []string{"http://d.com/a", "http://d.com/m"}},
				&fakeProvider{name: "p3", urls: []string{"http://d.com/m", "http://d.com/x", ""}},
			},
			[]string{"d.com"}, 8)
		data, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first := build()
	second := build()
	if string(first) != string(second) {
		t.Fatalf("report differs across identical runs:\n%s\nvs\n%s", first, second)
	}
}

func TestRunnerConnectionResetKeepsPartialDataAndFailsOnlyThatSource(t *testing.T) {
	rep := runProvenanceOnce(t,
		[]providers.Provider{
			&fakeProvider{name: "good", urls: []string{"http://d.com/kept"}},
			&fakeProvider{name: "flaky", urls: []string{"http://d.com/partial"}, err: boom{}},
		},
		[]string{"d.com"}, 4)

	status := map[string]provenance.ProviderStatus{}
	for _, st := range rep.Statuses {
		status[st.Provider] = st
	}
	if status["flaky"].Status != "failed" {
		t.Errorf("flaky provider status = %q, want failed", status["flaky"].Status)
	}
	if status["good"].Status != "ok" {
		t.Errorf("good provider status = %q, want ok", status["good"].Status)
	}
	urls := map[string]bool{}
	for _, rec := range rep.Records {
		urls[rec.URL] = true
	}
	if !urls["http://d.com/kept"] {
		t.Error("healthy provider data missing")
	}
	if !urls["http://d.com/partial"] {
		t.Error("partial data emitted before the reset must be retained")
	}
}

func TestRunnerMalformedDataCountedNotFatal(t *testing.T) {
	rep := runProvenanceOnce(t,
		[]providers.Provider{&fakeProvider{name: "p", urls: []string{"http://d.com/ok", "", "http://bad host/x"}}},
		[]string{"d.com"}, 2)
	st := rep.Statuses[0]
	if st.Malformed != 2 {
		t.Errorf("malformed = %d, want 2", st.Malformed)
	}
	if st.Status != "ok" {
		t.Errorf("status = %q, want ok (malformed rows must not fail the source)", st.Status)
	}
	if len(rep.Records) != 1 {
		t.Errorf("want 1 valid record, got %d", len(rep.Records))
	}
}

func TestRunnerAllProvidersFailed(t *testing.T) {
	collector, err := provenance.NewCollector("none", 3, []string{"d.com"}, []string{"p1", "p2"})
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{threads: 2}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workChan := make(chan Work)
	r.StartWithSources(ctx, workChan, collector)
	workChan <- NewWork("d.com", &fakeProvider{name: "p1", err: boom{}})
	workChan <- NewWork("d.com", &fakeProvider{name: "p2", err: boom{}})
	close(workChan)
	r.Wait()

	failed := collector.AllProvidersFailed()
	if len(failed) != 2 {
		t.Fatalf("want 2 explicit failures, got %+v", failed)
	}
	rep := collector.Finalize()
	if len(rep.Records) != 0 {
		t.Errorf("all-failed run must not present records, got %d", len(rep.Records))
	}
}

func TestLegacyPathUnchanged(t *testing.T) {
	// The opt-off pipeline must behave exactly as before the feature.
	r := &Runner{threads: 2}
	results := make(chan string)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workChan := make(chan Work)
	r.Start(ctx, workChan, results)
	go func() {
		workChan <- NewWork("d.com", &fakeProvider{name: "p", urls: []string{"http://d.com/a", "http://d.com/b", "http://d.com/a"}})
		workChan <- NewWork("d.com", &fakeProvider{name: "broken", err: boom{}})
		close(workChan)
		r.Wait()
		close(results) // mirrors cmd/gau: results close after workers finish
	}()
	got := map[string]int{}
	for u := range results {
		got[u]++
	}
	if got["http://d.com/a"] != 2 || got["http://d.com/b"] != 1 {
		t.Fatalf("legacy streaming semantics changed: %v", got)
	}
}

func sourceNames(rec provenance.Record) []string {
	out := make([]string, 0, len(rec.Sources))
	for name := range rec.Sources {
		out = append(out, name)
	}
	return out
}
