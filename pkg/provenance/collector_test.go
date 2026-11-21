package provenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// fixedNow makes timestamps deterministic across test runs.
func fixedCollector(t *testing.T, statePath string, failLimit int, domains, providers []string) *Collector {
	t.Helper()
	c, err := NewCollector(statePath, failLimit, domains, providers)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return base }
	return c
}

func TestSingleSource(t *testing.T) {
	c := fixedCollector(t, "none", 3, []string{"example.com"}, []string{"wayback"})
	c.Observe("example.com", "wayback", "http://example.com/a")
	c.Observe("example.com", "wayback", "http://example.com/b")
	c.Complete("example.com", "wayback", false, nil)

	rep := c.Finalize()
	if len(rep.Records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(rep.Records))
	}
	for _, rec := range rep.Records {
		if len(rec.Sources) != 1 {
			t.Errorf("%s: expected 1 source, got %d", rec.URL, len(rec.Sources))
		}
		if _, ok := rec.Sources["wayback"]; !ok {
			t.Errorf("%s: missing wayback source", rec.URL)
		}
		if rec.FirstSeen != "2026-01-01T00:00:00Z" {
			t.Errorf("%s: unexpected first_seen %q", rec.URL, rec.FirstSeen)
		}
	}
	st := rep.Statuses[0]
	if st.Status != "ok" || st.Observations != 2 {
		t.Errorf("unexpected status: %+v", st)
	}
}

func TestMultiSourcePriorityBySourceCount(t *testing.T) {
	c := fixedCollector(t, "none", 3, []string{"d.example"}, []string{"wayback", "otx", "urlscan"})

	// one URL from all three providers, one from two, one from one.
	c.Observe("d.example", "wayback", "http://d.example/three")
	c.Observe("d.example", "otx", "http://d.example/three")
	c.Observe("d.example", "urlscan", "http://d.example/three")

	c.Observe("d.example", "wayback", "http://d.example/two")
	c.Observe("d.example", "otx", "http://d.example/two")

	c.Observe("d.example", "wayback", "http://d.example/one")

	for _, p := range []string{"wayback", "otx", "urlscan"} {
		c.Complete("d.example", p, false, nil)
	}

	rep := c.Finalize()
	if len(rep.Records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(rep.Records))
	}
	counts := make([]int, len(rep.Records))
	for i, rec := range rep.Records {
		counts[i] = len(rec.Sources)
	}
	// Acceptance assertion: priority follows source count, not lexicographic
	// order (lexicographic would put /one first).
	want := []int{3, 2, 1}
	for i := range want {
		if counts[i] != want[i] {
			t.Fatalf("priority order source counts = %v, want %v (records: %+v)", counts, want, urlsOf(rep))
		}
	}
	if rep.Records[0].URL != "http://d.example/three" {
		t.Errorf("expected three-source URL first, got %q", rep.Records[0].URL)
	}
}

func TestRawFormsRetainedPerSource(t *testing.T) {
	c := fixedCollector(t, "none", 3, []string{"e.com"}, []string{"wayback", "otx"})
	// same normalized URL, different original spellings per source.
	c.Observe("e.com", "wayback", "HTTP://E.com/Path#x")
	c.Observe("e.com", "wayback", "http://e.com:80/Path#y")
	c.Observe("e.com", "otx", "http://e.com/Path")
	c.Complete("e.com", "wayback", false, nil)
	c.Complete("e.com", "otx", false, nil)

	rep := c.Finalize()
	if len(rep.Records) != 1 {
		t.Fatalf("expected merged single record, got %d", len(rep.Records))
	}
	rec := rep.Records[0]
	wb := rec.Sources["wayback"]
	if len(wb.RawForms) != 2 {
		t.Errorf("wayback raw forms = %v, want both original spellings", wb.RawForms)
	}
	if !sort.StringsAreSorted(wb.RawForms) {
		t.Errorf("raw forms not sorted: %v", wb.RawForms)
	}
	if got := rec.Sources["otx"].RawForms; len(got) != 1 || got[0] != "http://e.com/Path" {
		t.Errorf("otx raw forms = %v", got)
	}
}

func TestSingleSourceFailureDoesNotBlockOthers(t *testing.T) {
	c := fixedCollector(t, "none", 3, []string{"d.com"}, []string{"wayback", "otx"})
	c.Observe("d.com", "wayback", "http://d.com/ok")
	c.Complete("d.com", "wayback", false, nil)
	c.Complete("d.com", "otx", true, errBoom{})

	if failed := c.AllProvidersFailed(); len(failed) != 0 {
		t.Fatalf("with one healthy provider AllProvidersFailed should be empty, got %+v", failed)
	}
	rep := c.Finalize()
	byStatus := map[string]ProviderStatus{}
	for _, st := range rep.Statuses {
		byStatus[st.Provider] = st
	}
	if byStatus["otx"].Status != "failed" {
		t.Errorf("otx status = %q, want failed", byStatus["otx"].Status)
	}
	if byStatus["wayback"].Status != "ok" {
		t.Errorf("wayback status = %q, want ok", byStatus["wayback"].Status)
	}
	if len(rep.Records) != 1 {
		t.Fatalf("healthy provider's data must survive: got %d records", len(rep.Records))
	}
	if rep.Records[0].Sources["otx"].FirstSeen != "" {
		t.Error("failed provider must not appear as a source")
	}
}

func TestEmptyResultIsDistinctFromFailure(t *testing.T) {
	c := fixedCollector(t, "none", 3, []string{"d.com"}, []string{"wayback", "otx"})
	c.Complete("d.com", "wayback", false, nil) // zero observations
	c.Observe("d.com", "otx", "http://d.com/a")
	c.Complete("d.com", "otx", false, nil)

	rep := c.Finalize()
	for _, st := range rep.Statuses {
		if st.Provider == "wayback" && st.Status != "empty" {
			t.Errorf("wayback status = %q, want empty", st.Status)
		}
	}
	if failed := c.AllProvidersFailed(); len(failed) != 0 {
		t.Fatalf("an explicit empty result means the domain was served: %+v", failed)
	}
}

func TestAllProvidersFailedIsHardError(t *testing.T) {
	c := fixedCollector(t, "none", 3, []string{"d.com"}, []string{"wayback", "otx", "urlscan"})
	c.Complete("d.com", "wayback", true, errBoom{})
	c.Complete("d.com", "otx", true, errBoom{})
	c.Complete("d.com", "urlscan", true, errBoom{})

	failed := c.AllProvidersFailed()
	// Acceptance assertion: every provider is reported with an explicit
	// failure rather than an empty URL list.
	if len(failed) != 3 {
		t.Fatalf("expected 3 failed statuses, got %d: %+v", len(failed), failed)
	}
	for _, st := range failed {
		if st.Status != "failed" {
			t.Errorf("provider %s status = %q, want failed", st.Provider, st.Status)
		}
	}
	rep := c.Finalize()
	if len(rep.Records) != 0 {
		t.Errorf("no records should exist when all sources failed, got %d", len(rep.Records))
	}
}

func TestMalformedObservationSkippedAndCounted(t *testing.T) {
	c := fixedCollector(t, "none", 3, []string{"d.com"}, []string{"wayback"})
	c.Observe("d.com", "wayback", "http://d.com/good")
	c.Observe("d.com", "wayback", "")
	c.Observe("d.com", "wayback", "http://exa mple.com/bad")
	c.Complete("d.com", "wayback", false, nil)

	rep := c.Finalize()
	if len(rep.Records) != 1 {
		t.Fatalf("malformed records must be skipped, got %d records", len(rep.Records))
	}
	st := rep.Statuses[0]
	if st.Malformed != 2 {
		t.Errorf("malformed count = %d, want 2", st.Malformed)
	}
	if st.Status != "ok" {
		t.Errorf("provider with valid data plus bad rows should be ok, got %q", st.Status)
	}
}

func TestCrossRunAddedAndRemovedSources(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")

	// run 1: wayback + otx both provide the URL.
	c1 := fixedCollector(t, state, 3, []string{"d.com"}, []string{"wayback", "otx"})
	c1.Observe("d.com", "wayback", "http://d.com/u")
	c1.Observe("d.com", "otx", "http://d.com/u")
	c1.Complete("d.com", "wayback", false, nil)
	c1.Complete("d.com", "otx", false, nil)
	r1 := c1.Finalize()
	if err := c1.Save(r1); err != nil {
		t.Fatal(err)
	}

	// run 2 (later): otx drops out, urlscan starts.
	c2, err := NewCollector(state, 3, []string{"d.com"}, []string{"wayback", "otx", "urlscan"})
	if err != nil {
		t.Fatal(err)
	}
	c2.now = func() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) }
	c2.Observe("d.com", "wayback", "http://d.com/u")
	c2.Observe("d.com", "urlscan", "http://d.com/u")
	c2.Complete("d.com", "wayback", false, nil)
	c2.Complete("d.com", "otx", false, nil) // empty now
	c2.Complete("d.com", "urlscan", false, nil)
	r2 := c2.Finalize()
	if err := c2.Save(r2); err != nil {
		t.Fatal(err)
	}

	rec := r2.Records[0]
	if !equalStrings(rec.AddedSources, []string{"urlscan"}) {
		t.Errorf("added = %v, want [urlscan]", rec.AddedSources)
	}
	if !equalStrings(rec.RemovedSources, []string{"otx"}) {
		t.Errorf("removed = %v, want [otx]", rec.RemovedSources)
	}
	// first appearance is the run-1 timestamp, not run 2's.
	if rec.FirstSeen != "2026-01-01T00:00:00Z" {
		t.Errorf("first_seen = %q, want run 1 timestamp", rec.FirstSeen)
	}
	// otx history (first/last seen) is retained even though it dropped out.
	otx := rec.Sources["otx"]
	if otx.FirstSeen == "" || otx.LastSeen != "2026-01-01T00:00:00Z" {
		t.Errorf("otx history not retained: %+v", otx)
	}
	ws := rec.Sources["wayback"]
	if ws.LastSeen != "2026-02-01T00:00:00Z" {
		t.Errorf("wayback last_seen = %q, want run 2", ws.LastSeen)
	}
}

func TestCircuitBreakerSkipsAfterThreshold(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	limit := 2

	// two failing runs cross the threshold.
	for i := 0; i < limit; i++ {
		c := fixedCollector(t, state, limit, []string{"d.com"}, []string{"flaky"})
		c.Complete("d.com", "flaky", true, errBoom{})
		r := c.Finalize()
		if err := c.Save(r); err != nil {
			t.Fatal(err)
		}
	}

	c, err := NewCollector(state, limit, []string{"d.com"}, []string{"flaky"})
	if err != nil {
		t.Fatal(err)
	}
	skip, reason := c.ShouldSkip("d.com", "flaky")
	if !skip {
		t.Fatal("expected provider to be skipped after reaching failure limit")
	}
	if reason == "" {
		t.Error("skip should return a warning reason")
	}
	c.Complete("d.com", "flaky", false, nil) // must not overwrite skipped
	rep := c.Finalize()
	st := rep.Statuses[0]
	if st.Status != "skipped" || st.ConsecutiveFails < limit {
		t.Errorf("unexpected status after breaker: %+v", st)
	}
}

func TestCircuitBreakerResetsOnSuccess(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	c1 := fixedCollector(t, state, 2, []string{"d.com"}, []string{"p"})
	c1.Complete("d.com", "p", true, errBoom{})
	r1 := c1.Finalize()
	if err := c1.Save(r1); err != nil {
		t.Fatal(err)
	}

	c2, err := NewCollector(state, 2, []string{"d.com"}, []string{"p"})
	if err != nil {
		t.Fatal(err)
	}
	c2.now = func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) }
	c2.Observe("d.com", "p", "http://d.com/x")
	c2.Complete("d.com", "p", false, nil)
	r2 := c2.Finalize()
	if err := c2.Save(r2); err != nil {
		t.Fatal(err)
	}

	c3, err := NewCollector(state, 2, []string{"d.com"}, []string{"p"})
	if err != nil {
		t.Fatal(err)
	}
	skip, _ := c3.ShouldSkip("d.com", "p")
	if skip {
		t.Fatal("successful run should reset the failure counter")
	}
}

func TestDeterministicOutputAcrossRuns(t *testing.T) {
	build := func() *Report {
		c := fixedCollector(t, "none", 3, []string{"d.com"}, []string{"p1", "p2", "p3"})
		// feed in a deliberately jumbled order
		c.Observe("d.com", "p3", "http://d.com/z")
		c.Observe("d.com", "p1", "http://d.com/a")
		c.Observe("d.com", "p2", "http://d.com/a")
		c.Observe("d.com", "p1", "http://d.com/m")
		c.Observe("d.com", "p2", "http://d.com/m")
		c.Observe("d.com", "p3", "http://d.com/m")
		for _, p := range []string{"p1", "p2", "p3"} {
			c.Complete("d.com", p, false, nil)
		}
		return c.Finalize()
	}
	first, err := json.Marshal(build())
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(build())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("non-deterministic report:\n%s\nvs\n%s", first, second)
	}
}

func TestCorruptStateRejectedNotSilentlyReset(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	if err := os.WriteFile(state, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCollector(state, 3, []string{"d.com"}, []string{"p"}); err == nil {
		t.Fatal("corrupt state must produce an explicit error")
	}
}

func TestSchemaVersionMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	old := persistedState{Version: 999, Domains: map[string][]persistedRecord{}, Health: map[string]int{}}
	data, _ := json.Marshal(old)
	if err := os.WriteFile(state, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCollector(state, 3, []string{"d.com"}, []string{"p"}); err == nil {
		t.Fatal("schema mismatch must produce an explicit error")
	}
}

func urlsOf(r *Report) []string {
	out := make([]string, len(r.Records))
	for i, rec := range r.Records {
		out[i] = rec.URL
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

type errBoom struct{}

func (errBoom) Error() string { return "simulated upstream failure" }
