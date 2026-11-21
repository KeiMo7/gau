package history

import (
	"testing"
)

func TestNormalize_EquivalentURLsShareIdentity(t *testing.T) {
	cases := []string{
		"https://example.com/a%7eb?b=2&a=1#frag",
		"https://example.com/a~b?a=1&b=2",
		"HTTPS://EXAMPLE.com/a~b?b=2&a=1",
		"https://example.com:443/a~b?a=1&b=2#other",
	}
	first := Normalize(cases[0])
	for _, raw := range cases[1:] {
		got := Normalize(raw)
		if got.ID != first.ID {
			t.Errorf("identity mismatch:\n  %s -> %s\n  %s -> %s", cases[0], first.ID, raw, got.ID)
		}
	}
	if len(first.QueryKeys) != 2 || first.QueryKeys[0] != "a" || first.QueryKeys[1] != "b" {
		t.Fatalf("expected sorted keys [a b], got %v", first.QueryKeys)
	}
}

func TestNormalize_DistinctPathsAndPorts(t *testing.T) {
	a := Normalize("http://example.com/x")
	b := Normalize("https://example.com/x")
	c := Normalize("https://example.com:8443/x")
	d := Normalize("https://example.com/y")
	ids := map[string]bool{a.ID: true, b.ID: true, c.ID: true, d.ID: true}
	if len(ids) != 4 {
		t.Fatalf("expected 4 distinct identities, got %d: %+v", len(ids), ids)
	}
}

func TestNormalize_UnparseableFallback(t *testing.T) {
	// Malformed upstream data must not panic and must compare deterministically.
	got := Normalize("://not a url")
	if got.ID == "" {
		t.Fatal("fallback identity should not be empty")
	}
	if Normalize("://not a url").ID != got.ID {
		t.Fatal("fallback normalization is not deterministic")
	}
}

func TestNormalize_RegistrableDomain(t *testing.T) {
	got := Normalize("https://api.www.example.co.uk/path")
	if got.Domain != "example.co.uk" {
		t.Fatalf("expected example.co.uk, got %q", got.Domain)
	}
}

func entry(id string, keys []string, domain, sample string, providers ...string) Entry {
	return Entry{ID: id, QueryKeys: keys, Domain: domain, Sample: sample, Providers: providers}
}

func round(id int, entries []Entry, failures ...Failure) *Round {
	return &Round{ID: id, Entries: entries, Failures: failures}
}

func TestDiff_BaselineIsFullSnapshot(t *testing.T) {
	cur := round(1, []Entry{
		entry("https://example.com/a", nil, "example.com", "https://example.com/a", "wayback"),
		entry("https://example.com/b", nil, "example.com", "https://example.com/b", "otx"),
	})
	rep := Diff(nil, cur)
	if !rep.Baseline {
		t.Fatal("expected baseline report")
	}
	if len(rep.Added) != 2 || len(rep.Removed) != 0 || len(rep.Persistent) != 0 {
		t.Fatalf("baseline bucket sizes wrong: %+v", rep)
	}
	if rep.AddedByDomain["example.com"] != 2 {
		t.Fatalf("domain aggregation wrong: %v", rep.AddedByDomain)
	}
}

func TestDiff_NormalIncremental(t *testing.T) {
	prev := round(1, []Entry{
		entry("https://example.com/keep", nil, "example.com", "k", "wayback"),
		entry("https://example.com/gone", nil, "example.com", "g", "otx"),
		entry("https://example.com/change", []string{"a"}, "example.com", "c1", "wayback"),
	})
	cur := round(2, []Entry{
		entry("https://example.com/keep", nil, "example.com", "k", "wayback"),
		entry("https://example.com/new", nil, "example.com", "n", "wayback"),
		entry("https://example.com/change", []string{"a", "b"}, "example.com", "c2", "wayback"),
	})
	rep := Diff(prev, cur)

	if len(rep.Added) != 1 || rep.Added[0].ID != "https://example.com/new" {
		t.Fatalf("added wrong: %+v", rep.Added)
	}
	if len(rep.Removed) != 1 || rep.Removed[0].ID != "https://example.com/gone" {
		t.Fatalf("removed wrong: %+v", rep.Removed)
	}
	if len(rep.Persistent) != 1 || rep.Persistent[0].ID != "https://example.com/keep" {
		t.Fatalf("persistent wrong: %+v", rep.Persistent)
	}
	if len(rep.Changed) != 1 {
		t.Fatalf("changed wrong: %+v", rep.Changed)
	}
	ch := rep.Changed[0]
	if ch.ID != "https://example.com/change" || len(ch.AddedKeys) != 1 || ch.AddedKeys[0] != "b" || len(ch.RemovedKeys) != 0 {
		t.Fatalf("param diff wrong: %+v", ch)
	}
	if rep.AddedByDomain["example.com"] != 1 {
		t.Fatalf("domain summary wrong: %v", rep.AddedByDomain)
	}
}

// Acceptance: a URL absent this round must NOT count as disappeared when the
// only provider that supplied it failed this round.
func TestDiff_SourceFailureDoesNotCountAsRemoval(t *testing.T) {
	prev := round(1, []Entry{
		entry("https://example.com/flaky", nil, "example.com", "f", "wayback"),
		entry("https://example.com/confirmed", nil, "example.com", "c", "wayback", "otx"),
		entry("https://example.com/gone", nil, "example.com", "g", "otx"),
	})
	cur := round(2, []Entry{
		entry("https://example.com/gone2", nil, "example.com", "g2", "otx"),
	}, Failure{Provider: "wayback", Domain: "example.com", Error: "connection reset by peer"})

	rep := Diff(prev, cur)

	// flaky: sole provider wayback failed -> suppressed, not removed.
	if len(rep.Suppressed) != 1 || rep.Suppressed[0].Entry.ID != "https://example.com/flaky" {
		t.Fatalf("suppression wrong: %+v", rep.Suppressed)
	}
	for _, e := range rep.Removed {
		if e.ID == "https://example.com/flaky" {
			t.Fatal("URL from a failed provider was counted as removed")
		}
	}
	// confirmed: otx (one of its two suppliers) completed successfully and
	// still omitted it -> genuine removal.
	// gone: otx succeeded this round -> genuine removal.
	removedIDs := map[string]bool{}
	for _, e := range rep.Removed {
		removedIDs[e.ID] = true
	}
	if !removedIDs["https://example.com/gone"] || !removedIDs["https://example.com/confirmed"] {
		t.Fatalf("expected confirmed and gone as removed, got %v", removedIDs)
	}
	if len(rep.CurrentRoundFailures) != 1 {
		t.Fatalf("failures not echoed: %+v", rep.CurrentRoundFailures)
	}
}

// Acceptance: diffing the same pair twice yields identical conclusions.
func TestDiff_Idempotent(t *testing.T) {
	prev := round(1, []Entry{
		entry("https://a.example/x", []string{"q"}, "example.com", "x", "wayback"),
		entry("https://a.example/y", nil, "example.com", "y", "otx"),
	})
	cur := round(2, []Entry{
		entry("https://a.example/x", nil, "example.com", "x2", "wayback"),
		entry("https://a.example/z", nil, "example.com", "z", "otx"),
	}, Failure{Provider: "otx", Domain: "example.com", Error: "timeout"})

	r1 := Diff(prev, cur)
	r2 := Diff(prev, cur)
	if !reportsEqual(r1, r2) {
		t.Fatal("same input pair produced different diff results")
	}
}

// Encoding differences must never produce an add/remove pair.
func TestDiff_EncodingNoiseIsStable(t *testing.T) {
	prev := round(1, []Entry{
		NormalizeAsEntry("https://example.com/a%7eb?x=1", "wayback"),
	})
	cur := round(2, []Entry{
		NormalizeAsEntry("https://example.com/a~b?x=2", "wayback"),
	})
	rep := Diff(prev, cur)
	if len(rep.Added) != 0 || len(rep.Removed) != 0 || len(rep.Changed) != 0 {
		t.Fatalf("encoding-only difference was not normalized away: %+v", rep)
	}
	if len(rep.Persistent) != 1 {
		t.Fatalf("expected persistent, got %+v", rep.Persistent)
	}
}

func NormalizeAsEntry(raw, provider string) Entry {
	id := Normalize(raw)
	return Entry{ID: id.ID, QueryKeys: id.QueryKeys, Domain: id.Domain, Sample: raw, Providers: []string{provider}}
}

func reportsEqual(a, b *Report) bool {
	if a.Baseline != b.Baseline || a.PreviousRound != b.PreviousRound || a.CurrentRound != b.CurrentRound {
		return false
	}
	if len(a.Added) != len(b.Added) || len(a.Removed) != len(b.Removed) ||
		len(a.Persistent) != len(b.Persistent) || len(a.Changed) != len(b.Changed) ||
		len(a.Suppressed) != len(b.Suppressed) {
		return false
	}
	for i := range a.Added {
		if a.Added[i].ID != b.Added[i].ID {
			return false
		}
	}
	for i := range a.Removed {
		if a.Removed[i].ID != b.Removed[i].ID {
			return false
		}
	}
	for i := range a.Changed {
		if a.Changed[i].ID != b.Changed[i].ID {
			return false
		}
	}
	for i := range a.Suppressed {
		if a.Suppressed[i].Entry.ID != b.Suppressed[i].Entry.ID {
			return false
		}
	}
	return true
}
