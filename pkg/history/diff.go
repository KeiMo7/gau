package history

import "sort"

// ParamChange describes a parameter-structure change on an endpoint that
// existed in both rounds. Only parameter names are compared; the same path
// with a different set of query keys is reported here instead of producing a
// spurious add + remove pair.
type ParamChange struct {
	ID          string   `json:"id"`
	Domain      string   `json:"domain"`
	Sample      string   `json:"sample"`
	AddedKeys   []string `json:"added_keys,omitempty"`
	RemovedKeys []string `json:"removed_keys,omitempty"`
}

// SuppressedEntry is a URL that would have been reported as disappeared, but
// every provider that previously supplied it failed in the current round, so
// its absence cannot be trusted. It is kept out of the Removed bucket.
type SuppressedEntry struct {
	Entry           Entry    `json:"entry"`
	FailedProviders []string `json:"failed_providers"`
}

// Report is the deterministic round-to-round diff.
type Report struct {
	PreviousRound int `json:"previous_round"`
	CurrentRound  int `json:"current_round"`

	// Added: identities present in the current round but not the previous one.
	Added []Entry `json:"added"`
	// Removed: identities gone from the current round, where at least one
	// provider that previously supplied them completed successfully.
	Removed []Entry `json:"removed"`
	// Persistent: identities present in both rounds with the same query keys.
	Persistent []Entry `json:"persistent"`
	// Changed: identities present in both rounds with a different query-key set.
	Changed []ParamChange `json:"changed"`
	// Suppressed: would-be removals whose providers all failed this round.
	Suppressed []SuppressedEntry `json:"suppressed"`

	// AddedByDomain aggregates the Added bucket by registrable domain.
	AddedByDomain map[string]int `json:"added_by_domain"`

	// CurrentRoundFailures echoes the failures recorded in the current round.
	CurrentRoundFailures []Failure `json:"current_round_failures"`

	// Baseline is true when no previous round was available: the whole current
	// round is then reported as Added and no removals are possible.
	Baseline bool `json:"baseline"`
}

// Diff computes the diff between two rounds. It is a pure function: it never
// mutates its inputs, performs no I/O, and running it twice over the same two
// rounds yields byte-identical results (all output slices are sorted by ID
// and maps are produced from sorted iteration).
func Diff(prev, cur *Round) *Report {
	rep := &Report{
		CurrentRound:         cur.ID,
		Added:                []Entry{},
		Removed:              []Entry{},
		Persistent:           []Entry{},
		Changed:              []ParamChange{},
		Suppressed:           []SuppressedEntry{},
		AddedByDomain:        map[string]int{},
		CurrentRoundFailures: []Failure{},
	}

	rep.CurrentRoundFailures = append(rep.CurrentRoundFailures, cur.Failures...)
	sortFailures(rep.CurrentRoundFailures)

	curIndex := indexEntries(cur.Entries)

	if prev == nil {
		rep.Baseline = true
		rep.Added = append([]Entry(nil), cur.Entries...)
		sortEntries(rep.Added)
		for _, e := range rep.Added {
			rep.AddedByDomain[e.Domain]++
		}
		sortFailures(rep.CurrentRoundFailures)
		return rep
	}

	rep.PreviousRound = prev.ID
	prevIndex := indexEntries(prev.Entries)
	failedKeys := cur.FailedKeys()

	// Bucket every current identity.
	for _, curEntry := range cur.Entries {
		prevEntry, existed := prevIndex[curEntry.ID]
		switch {
		case !existed:
			rep.Added = append(rep.Added, curEntry)
			rep.AddedByDomain[curEntry.Domain]++
		case sameKeys(prevEntry.QueryKeys, curEntry.QueryKeys):
			rep.Persistent = append(rep.Persistent, curEntry)
		default:
			rep.Changed = append(rep.Changed, ParamChange{
				ID:          curEntry.ID,
				Domain:      curEntry.Domain,
				Sample:      curEntry.Sample,
				AddedKeys:   setDifference(curEntry.QueryKeys, prevEntry.QueryKeys),
				RemovedKeys: setDifference(prevEntry.QueryKeys, curEntry.QueryKeys),
			})
		}
	}

	// Bucket every previous identity that did not survive into the current round.
	for _, prevEntry := range prev.Entries {
		if _, stillThere := curIndex[prevEntry.ID]; stillThere {
			continue
		}
		if failed := failingProviders(prevEntry, failedKeys); len(failed) > 0 &&
			len(failed) == len(uniqueProviders(prevEntry.Providers)) {
			rep.Suppressed = append(rep.Suppressed, SuppressedEntry{
				Entry:           prevEntry,
				FailedProviders: failed,
			})
		} else {
			rep.Removed = append(rep.Removed, prevEntry)
		}
	}

	sortEntries(rep.Added)
	sortEntries(rep.Removed)
	sortEntries(rep.Persistent)
	sort.Slice(rep.Changed, func(i, j int) bool { return rep.Changed[i].ID < rep.Changed[j].ID })
	sort.Slice(rep.Suppressed, func(i, j int) bool { return rep.Suppressed[i].Entry.ID < rep.Suppressed[j].Entry.ID })
	sortFailures(rep.CurrentRoundFailures)
	return rep
}

func indexEntries(entries []Entry) map[string]Entry {
	m := make(map[string]Entry, len(entries))
	for _, e := range entries {
		m[e.ID] = e
	}
	return m
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
}

func sortFailures(failures []Failure) {
	sort.Slice(failures, func(i, j int) bool {
		if failures[i].Provider != failures[j].Provider {
			return failures[i].Provider < failures[j].Provider
		}
		return failures[i].Domain < failures[j].Domain
	})
}

// sameKeys compares two sorted, de-duplicated key sets.
func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// setDifference returns the sorted set of keys present in a but not b.
func setDifference(a, b []string) []string {
	bset := make(map[string]struct{}, len(b))
	for _, k := range b {
		bset[k] = struct{}{}
	}
	var out []string
	seen := map[string]struct{}{}
	for _, k := range a {
		if _, inB := bset[k]; inB {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// uniqueProviders returns the sorted, de-duplicated provider list of an entry.
func uniqueProviders(providers []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range providers {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// failingProviders returns the subset of an entry's providers that failed for
// the entry's domain in the current round. The empty slice means the entry's
// absence is a genuine disappearance.
func failingProviders(e Entry, failedKeys map[string]struct{}) []string {
	var out []string
	for _, p := range uniqueProviders(e.Providers) {
		if _, failed := failedKeys[p+"\x00"+e.Domain]; failed {
			out = append(out, p)
		}
	}
	return out
}
