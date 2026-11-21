package history

import "sort"

// Trend verdicts.
const (
	TrendGrowing   = "growing"
	TrendShrinking = "shrinking"
	TrendStable    = "stable"
)

// DomainTrend stacks the per-round diffs of one registrable domain over a
// series of rounds. Counts are aligned by round ID (index 0 is the oldest
// round in the window) so growth and convergence are visible directly.
type DomainTrend struct {
	Domain     string `json:"domain"`
	RoundIDs   []int  `json:"round_ids"`
	Added      []int  `json:"added"`
	Removed    []int  `json:"removed"`
	Persistent []int  `json:"persistent"`
	Total      []int  `json:"total"`
	Verdict    string `json:"verdict"`
}

// Trends builds per-domain trends from an ordered (oldest-first) series of
// rounds. Each returned trend covers exactly rounds[1:] — the first round is
// the baseline and has no diff. With fewer than two rounds the result is
// empty. The output is sorted by descending total growth and then domain name,
// making the result independent of map iteration order.
func Trends(rounds []*Round) []DomainTrend {
	if len(rounds) < 2 {
		return nil
	}

	type acc struct {
		roundIDs                   []int
		added, removed, persistent []int
		total                      []int
	}
	byDomain := map[string]*acc{}
	order := []string{}

	for i := 1; i < len(rounds); i++ {
		rep := Diff(rounds[i-1], rounds[i])
		touch := func(domain string) *acc {
			a, ok := byDomain[domain]
			if !ok {
				a = &acc{}
				byDomain[domain] = a
				order = append(order, domain)
			}
			return a
		}
		bump := func(a *acc) {
			a.roundIDs = append(a.roundIDs, rounds[i].ID)
			a.added = append(a.added, 0)
			a.removed = append(a.removed, 0)
			a.persistent = append(a.persistent, 0)
			a.total = append(a.total, 0)
		}

		// Every domain that appears in either side of the diff gets a slot so
		// that domains which only shrink still appear in the output.
		domains := map[string]struct{}{}
		for _, e := range rep.Added {
			domains[e.Domain] = struct{}{}
		}
		for _, e := range rep.Removed {
			domains[e.Domain] = struct{}{}
		}
		for _, e := range rep.Persistent {
			domains[e.Domain] = struct{}{}
		}
		for _, c := range rep.Changed {
			domains[c.Domain] = struct{}{}
		}
		for d := range domains {
			a := touch(d)
			bump(a)
		}

		for _, e := range rep.Added {
			byDomain[e.Domain].added[len(byDomain[e.Domain].added)-1]++
		}
		for _, e := range rep.Removed {
			byDomain[e.Domain].removed[len(byDomain[e.Domain].removed)-1]++
		}
		for _, e := range rep.Persistent {
			byDomain[e.Domain].persistent[len(byDomain[e.Domain].persistent)-1]++
		}
		for _, c := range rep.Changed {
			// A parameter change means the endpoint itself persisted.
			byDomain[c.Domain].persistent[len(byDomain[c.Domain].persistent)-1]++
		}
		for _, r := range rounds[i].Entries {
			if _, ok := domains[r.Domain]; ok {
				byDomain[r.Domain].total[len(byDomain[r.Domain].total)-1]++
			}
		}
	}

	out := make([]DomainTrend, 0, len(order))
	for _, d := range order {
		a := byDomain[d]
		out = append(out, DomainTrend{
			Domain:     d,
			RoundIDs:   a.roundIDs,
			Added:      a.added,
			Removed:    a.removed,
			Persistent: a.persistent,
			Total:      a.total,
			Verdict:    verdict(a.added, a.removed),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		gi := out[i].Total[len(out[i].Total)-1] - out[i].Total[0]
		gj := out[j].Total[len(out[j].Total)-1] - out[j].Total[0]
		if gi != gj {
			return gi > gj
		}
		return out[i].Domain < out[j].Domain
	})
	return out
}

// verdict classifies a domain from its per-round added/removed counts:
//   - growing:  additions strictly outpace removals across the window;
//   - shrinking: removals strictly outpace additions;
//   - stable:    everything else (including equal churn).
func verdict(added, removed []int) string {
	var sumAdded, sumRemoved int
	for i := range added {
		sumAdded += added[i]
		sumRemoved += removed[i]
	}
	switch {
	case sumAdded > sumRemoved:
		return TrendGrowing
	case sumRemoved > sumAdded:
		return TrendShrinking
	default:
		return TrendStable
	}
}
