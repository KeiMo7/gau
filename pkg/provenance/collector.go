package provenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// StateVersion is the on-disk provenance-state schema version. A state file
// whose version differs is rejected with an explicit compatibility error.
const StateVersion = 1

var (
	ErrEmptyURL    = errors.New("empty url")
	ErrNoHost      = errors.New("url has no host")
	ErrInvalidHost = errors.New("invalid host")
)

// SourceInfo retains everything one provider ever reported for one URL.
// It is a structured value from the collector to the output layer; the raw
// forms are never flattened into a string in intermediate layers.
type SourceInfo struct {
	// FirstSeen is the first run (RFC3339 timestamp) in which this provider
	// was observed providing this normalized URL.
	FirstSeen string `json:"first_seen"`
	// LastSeen is the most recent run in which the provider provided it.
	LastSeen string `json:"last_seen"`
	// RawForms holds every distinct original spelling the provider returned
	// (case, fragment, encoding, scheme spelling). Sorted at finalize time.
	RawForms []string `json:"raw_forms"`
}

// Record is one normalized URL together with every source that provides it.
type Record struct {
	URL     string                `json:"url"`
	Sources map[string]SourceInfo `json:"sources"`
	// FirstSeen is the earliest FirstSeen across all sources — the answer
	// to "when did this URL first appear anywhere".
	FirstSeen string `json:"first_seen"`
	// AddedSources / RemovedSources are computed against the previous run's
	// state: which providers started / stopped providing this URL.
	AddedSources   []string `json:"added_sources,omitempty"`
	RemovedSources []string `json:"removed_sources,omitempty"`
}

// ProviderStatus is the per-domain, per-provider outcome of one run.
type ProviderStatus struct {
	Provider         string `json:"provider"`
	Domain           string `json:"domain"`
	Status           string `json:"status"` // "ok", "empty", "failed", "skipped"
	Error            string `json:"error,omitempty"`
	ConsecutiveFails int    `json:"consecutive_fails"`
	Observations     int    `json:"observations"`
	Malformed        int    `json:"malformed"`
}

// Report is the complete, deterministic result of a provenance run.
type Report struct {
	GeneratedAt string           `json:"generated_at"`
	Domains     []string         `json:"domains"`
	Providers   []string         `json:"providers"`
	Records     []Record         `json:"records"`
	Statuses    []ProviderStatus `json:"provider_status"`
}

// persistedRecord / persistedState is the on-disk state schema.
type persistedRecord struct {
	URL       string                `json:"url"`
	FirstSeen string                `json:"first_seen"`
	Sources   map[string]SourceInfo `json:"sources"`
}

type persistedState struct {
	Version int                          `json:"version"`
	Domains map[string][]persistedRecord `json:"domains"`
	Health  map[string]int               `json:"health"` // provider -> consecutive failures
}

// runObservation is the current-run data for one normalized URL: the distinct
// raw forms each provider reported. Timestamps are only assigned at Finalize.
type runObservation struct {
	rawByProvider map[string][]string
}

// Collector receives (provider, rawURL) observations from a single run,
// merges them with persisted cross-run state and produces a Report.
//
// All shared state is guarded by one mutex; Fetch goroutines only ever block
// on the mutex for a map insert, never on network I/O.
type Collector struct {
	mu        sync.Mutex
	now       func() time.Time
	failLimit int
	statePath string
	persist   bool
	domains   []string
	providers []string

	// current: domain -> normalized URL -> current-run observation
	current  map[string]map[string]*runObservation
	statuses map[string]*ProviderStatus // "domain\x00provider" -> status
	health   map[string]int             // provider -> consecutive failures
	prev     *persistedState

	// merged is produced once by Finalize and reused by Save, so the merge
	// logic exists in exactly one place.
	merged    map[string][]persistedRecord
	finalized bool
}

// NewCollector loads prior state. A missing state file starts fresh; a
// corrupt or schema-mismatched file is a hard error rather than silently
// discarding history.
func NewCollector(statePath string, failLimit int, domains, providers []string) (*Collector, error) {
	c := &Collector{
		now:       time.Now,
		failLimit: failLimit,
		statePath: statePath,
		persist:   statePath != "" && statePath != "none",
		domains:   append([]string(nil), domains...),
		providers: append([]string(nil), providers...),
		current:   map[string]map[string]*runObservation{},
		statuses:  map[string]*ProviderStatus{},
		health:    map[string]int{},
		merged:    map[string][]persistedRecord{},
	}
	sort.Strings(c.domains)
	sort.Strings(c.providers)

	empty := func() *persistedState {
		return &persistedState{
			Version: StateVersion,
			Domains: map[string][]persistedRecord{},
			Health:  map[string]int{},
		}
	}
	if !c.persist {
		c.prev = empty()
		return c, nil
	}
	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		c.prev = empty()
		return c, nil
	} else if err != nil {
		return nil, fmt.Errorf("read provenance state: %w", err)
	}
	var ps persistedState
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("provenance state %s is corrupt: %w (fix or remove the file to start fresh)", statePath, err)
	}
	if ps.Version != StateVersion {
		return nil, fmt.Errorf("provenance state %s has schema version %d, expected %d; remove it or migrate before rerunning", statePath, ps.Version, StateVersion)
	}
	if ps.Domains == nil {
		ps.Domains = map[string][]persistedRecord{}
	}
	if ps.Health == nil {
		ps.Health = map[string]int{}
	}
	c.prev = &ps
	for k, v := range ps.Health {
		c.health[k] = v
	}
	return c, nil
}

// SetClock overrides the timestamp source. Production code keeps time.Now;
// tests use it to make first_seen/last_seen deterministic.
func (c *Collector) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// ShouldSkip implements the consecutive-failure circuit breaker. When a
// provider has failed failLimit consecutive runs, the work is skipped and an
// explicit "skipped" status is recorded; the warning is returned for logging.
// A failLimit of zero disables the breaker.
func (c *Collector) ShouldSkip(domain, provider string) (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failLimit > 0 && c.health[provider] >= c.failLimit {
		c.ensureStatus(domain, provider).Status = "skipped"
		return true, fmt.Sprintf("provider %s skipped: %d consecutive failures (threshold %d); reset by editing or removing %s",
			provider, c.health[provider], c.failLimit, c.statePathDescription())
	}
	return false, ""
}

// Observe records one raw URL reported by one provider for one domain.
// Malformed input is skipped and counted, never aborts the provider.
func (c *Collector) Observe(domain, provider, raw string) {
	normalized, err := Normalize(raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.ensureStatus(domain, provider)
	if err != nil {
		st.Malformed++
		return
	}
	domainMap, ok := c.current[domain]
	if !ok {
		domainMap = map[string]*runObservation{}
		c.current[domain] = domainMap
	}
	obs, ok := domainMap[normalized]
	if !ok {
		obs = &runObservation{rawByProvider: map[string][]string{}}
		domainMap[normalized] = obs
	}
	if !contains(obs.rawByProvider[provider], raw) {
		obs.rawByProvider[provider] = append(obs.rawByProvider[provider], raw)
	}
	st.Observations++
}

// Complete records the final outcome of one (domain, provider) work item.
// fail=false with zero observations means an explicit empty result, which is
// distinct from an error.
func (c *Collector) Complete(domain, provider string, fail bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.ensureStatus(domain, provider)
	if st.Status == "skipped" {
		return // circuit breaker decision stands
	}
	if fail {
		st.Status = "failed"
		if err != nil {
			st.Error = err.Error()
		}
		return
	}
	if st.Observations == 0 && st.Malformed == 0 {
		st.Status = "empty"
	} else {
		st.Status = "ok"
	}
}

// AllProvidersFailed reports every domain for which every configured provider
// ended in failed/skipped state (a domain with an explicit empty result is
// considered served, not failed).
func (c *Collector) AllProvidersFailed() []ProviderStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []ProviderStatus
	for _, d := range c.domains {
		bad := true
		for _, p := range c.providers {
			st := c.statuses[statusKey(d, p)]
			if st != nil && (st.Status == "ok" || st.Status == "empty") {
				bad = false
				break
			}
		}
		if bad {
			for _, p := range c.providers {
				if st := c.statuses[statusKey(d, p)]; st != nil {
					out = append(out, *st)
				} else {
					out = append(out, ProviderStatus{Provider: p, Domain: d, Status: "failed"})
				}
			}
		}
	}
	return out
}

// Finalize merges current observations with prior state, updates the health
// counters, computes source changes and deterministic ordering, and returns
// the run report. The merged per-domain state is retained for Save.
func (c *Collector) Finalize() *Report {
	c.mu.Lock()
	defer c.mu.Unlock()

	runTS := c.now().UTC().Format(time.RFC3339)
	report := &Report{
		GeneratedAt: runTS,
		Domains:     append([]string(nil), c.domains...),
		Providers:   append([]string(nil), c.providers...),
	}

	c.settleHealthLocked()

	for _, domain := range c.domains {
		prev := map[string]persistedRecord{}
		for _, rec := range c.prev.Domains[domain] {
			prev[rec.URL] = rec
		}
		cur := c.current[domain]

		// every URL seen this run or a previous one remains in history.
		all := map[string]struct{}{}
		for u := range cur {
			all[u] = struct{}{}
		}
		for u := range prev {
			all[u] = struct{}{}
		}

		merged := make([]persistedRecord, 0, len(all))
		for normalized := range all {
			prevRec, hadPrev := prev[normalized]
			var curObs *runObservation
			if cur != nil {
				curObs = cur[normalized]
			}

			providerSet := map[string]struct{}{}
			if curObs != nil {
				for p := range curObs.rawByProvider {
					providerSet[p] = struct{}{}
				}
			}
			if hadPrev {
				for p := range prevRec.Sources {
					providerSet[p] = struct{}{}
				}
			}

			persisted := persistedRecord{URL: normalized, Sources: map[string]SourceInfo{}}
			rec := Record{URL: normalized, Sources: map[string]SourceInfo{}}
			var added, removed []string

			for p := range providerSet {
				var info SourceInfo
				if curObs != nil {
					if rawForms, active := curObs.rawByProvider[p]; active {
						// source provides the URL this run
						if old, ok := prevRec.Sources[p]; ok {
							info = old
							info.LastSeen = runTS
							info.RawForms = mergeSorted(old.RawForms, rawForms)
						} else {
							info = SourceInfo{FirstSeen: runTS, LastSeen: runTS, RawForms: append([]string(nil), rawForms...)}
							added = append(added, p)
						}
					} else {
						// source provided it previously but not this run
						info = prevRec.Sources[p]
						removed = append(removed, p)
					}
				} else {
					info = prevRec.Sources[p]
					removed = append(removed, p)
				}
				sort.Strings(info.RawForms)
				persisted.Sources[p] = info
				rec.Sources[p] = info
			}
			sort.Strings(added)
			sort.Strings(removed)
			rec.AddedSources = added
			rec.RemovedSources = removed
			persisted.FirstSeen = earliestFirstSeen(persisted.Sources)
			if persisted.FirstSeen == "" && hadPrev {
				persisted.FirstSeen = prevRec.FirstSeen
			}
			rec.FirstSeen = persisted.FirstSeen
			merged = append(merged, persisted)
			report.Records = append(report.Records, rec)
		}
		sort.Slice(merged, func(i, j int) bool { return merged[i].URL < merged[j].URL })
		c.merged[domain] = merged
	}

	// deterministic ordering: source count descending (multi-source priority),
	// then first-seen, then URL — identical input yields identical lines.
	sort.Slice(report.Records, func(i, j int) bool {
		ni, nj := len(report.Records[i].Sources), len(report.Records[j].Sources)
		if ni != nj {
			return ni > nj
		}
		if report.Records[i].FirstSeen != report.Records[j].FirstSeen {
			return report.Records[i].FirstSeen < report.Records[j].FirstSeen
		}
		return report.Records[i].URL < report.Records[j].URL
	})

	for _, key := range sortedStatusKeys(c.statuses) {
		st := c.statuses[key]
		st.ConsecutiveFails = c.health[st.Provider]
		report.Statuses = append(report.Statuses, *st)
	}
	c.finalized = true
	return report
}

// settleHealthLocked updates consecutive-failure counters from this run's
// outcomes. A provider is judged per run, not per domain: any ok/empty result
// proves reachability and resets the counter; only a run with no success and
// at least one failure increments it. Explicit rule (not map iteration order)
// so the outcome is identical regardless of scheduling.
func (c *Collector) settleHealthLocked() {
	success := map[string]bool{}
	failure := map[string]bool{}
	for _, st := range c.statuses {
		switch st.Status {
		case "ok", "empty":
			success[st.Provider] = true
		case "failed":
			failure[st.Provider] = true
		}
	}
	for p := range failure {
		if !success[p] {
			c.health[p]++
		}
	}
	for p := range success {
		c.health[p] = 0
	}
}

// Save atomically persists the state produced by Finalize.
func (c *Collector) Save(_ *Report) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.persist {
		return nil
	}
	if !c.finalized {
		return errors.New("Save called before Finalize")
	}
	ps := persistedState{
		Version: StateVersion,
		Domains: c.merged,
		Health:  c.health,
	}
	data, err := json.MarshalIndent(&ps, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(c.statePath, append(data, '\n'))
}

func (c *Collector) ensureStatus(domain, provider string) *ProviderStatus {
	key := statusKey(domain, provider)
	st, ok := c.statuses[key]
	if !ok {
		st = &ProviderStatus{Provider: provider, Domain: domain}
		c.statuses[key] = st
	}
	return st
}

func (c *Collector) statePathDescription() string {
	if c.persist {
		return c.statePath
	}
	return "state storage (disabled with --prov-state none)"
}

func statusKey(domain, provider string) string { return domain + "\x00" + provider }

func sortedStatusKeys(m map[string]*ProviderStatus) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// mergeSorted unions two string slices and returns a sorted, deduplicated
// result without relying on arrival order.
func mergeSorted(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func earliestFirstSeen(sources map[string]SourceInfo) string {
	var earliest string
	for _, s := range sources {
		if s.FirstSeen == "" {
			continue
		}
		if earliest == "" || s.FirstSeen < earliest {
			earliest = s.FirstSeen
		}
	}
	return earliest
}
