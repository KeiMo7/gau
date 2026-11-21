package history

import "time"

// Entry is one normalized URL as stored in a round record.
type Entry struct {
	// ID is the query-independent normalized identity.
	ID string `json:"id"`
	// QueryKeys is the sorted set of query parameter names. nil/empty means
	// the endpoint carries no query parameters.
	QueryKeys []string `json:"query_keys,omitempty"`
	// Domain is the registrable domain used for per-domain aggregation.
	Domain string `json:"domain"`
	// Sample is one representative raw URL observed for this identity.
	Sample string `json:"sample"`
	// Providers is the sorted set of providers that returned this identity.
	Providers []string `json:"providers"`
}

// Failure records a (provider, domain) fetch that returned an error during the
// round. A URL missing while every provider that previously supplied it is in
// this set is treated as "unknown" rather than "disappeared".
type Failure struct {
	Provider string `json:"provider"`
	Domain   string `json:"domain"`
	Error    string `json:"error"`
}

// Round is the complete persistent record of one collection run.
type Round struct {
	// ID is the 1-based monotonically increasing round number.
	ID int `json:"id"`
	// StartedAt / FinishedAt bracket the collection window.
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// Providers is the sorted list of providers configured for this round.
	Providers []string `json:"providers"`
	// Targets is the sorted list of domains requested in this round.
	Targets []string `json:"targets"`
	// Failures are the provider/domain fetches that errored.
	Failures []Failure `json:"failures,omitempty"`
	// Entries is the full normalized URL set observed this round.
	Entries []Entry `json:"entries"`
}

// FailedKeys returns the set of "provider\x00domain" keys that failed this
// round, for O(1) lookup during diffing.
func (r *Round) FailedKeys() map[string]struct{} {
	out := make(map[string]struct{}, len(r.Failures))
	for _, f := range r.Failures {
		out[f.Provider+"\x00"+f.Domain] = struct{}{}
	}
	return out
}
