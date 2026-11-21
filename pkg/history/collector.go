package history

import (
	"sort"
	"sync"
)

// Collector accumulates the normalized URLs seen during a round. All methods
// are safe for concurrent use by the gau worker goroutines.
type Collector struct {
	mu      sync.Mutex
	entries map[string]*Entry
	order   []string
}

func NewCollector() *Collector {
	return &Collector{entries: make(map[string]*Entry)}
}

// Add normalizes a raw URL and merges it into the round, recording which
// provider supplied it. Repeated observations of the same identity are
// de-duplicated; the provider set is unioned.
func (c *Collector) Add(rawURL, provider string) {
	id := Normalize(rawURL)
	if id.ID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[id.ID]
	if !ok {
		e = &Entry{
			ID:        id.ID,
			QueryKeys: id.QueryKeys,
			Domain:    id.Domain,
			Sample:    id.Sample,
			Providers: providersOrNil(provider),
		}
		c.entries[id.ID] = e
		c.order = append(c.order, id.ID)
		return
	}
	if provider != "" {
		e.Providers = appendUnique(e.Providers, provider)
	}
}

func providersOrNil(provider string) []string {
	if provider == "" {
		return nil
	}
	return []string{provider}
}

// Snapshot returns the entries sorted by identity, giving deterministic
// output independent of goroutine scheduling. The returned slice and its
// members are independent of the collector and may be stored directly.
func (c *Collector) Snapshot() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]Entry, 0, len(c.order))
	for _, id := range c.order {
		e := c.entries[id]
		providers := append([]string(nil), e.Providers...)
		sort.Strings(providers)
		keys := append([]string(nil), e.QueryKeys...)
		sort.Strings(keys)
		out = append(out, Entry{
			ID:        e.ID,
			QueryKeys: keys,
			Domain:    e.Domain,
			Sample:    e.Sample,
			Providers: providers,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Len returns the number of distinct identities collected so far.
func (c *Collector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.order)
}

func appendUnique(set []string, v string) []string {
	for _, existing := range set {
		if existing == v {
			return set
		}
	}
	return append(set, v)
}
