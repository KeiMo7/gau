package providers

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

// DefaultMaxPages bounds pagination when the upstream provides no
// reliable termination signal of its own.
const DefaultMaxPages uint = 1000

// NormalizeURLKey returns a canonical equivalence key for a raw URL.
// Two URLs that denote the same address but differ in spelling —
// trailing slash, default port, fragment, scheme/host casing — map to
// the same key. URLs that cannot be parsed fall back to their raw
// string so nothing is silently merged.
func NormalizeURLKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return raw
	}

	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" {
		// drop default ports only
		if !((scheme == "http" && port == "80") || (scheme == "https" && port == "443")) {
			host += ":" + port
		}
	}

	path := u.EscapedPath()
	if trimmed := strings.TrimRight(path, "/"); trimmed != "" {
		path = trimmed
	} else {
		path = "/"
	}

	var b strings.Builder
	b.WriteString(scheme)
	b.WriteString("://")
	if u.User != nil {
		b.WriteString(u.User.String())
		b.WriteString("@")
	}
	b.WriteString(host)
	b.WriteString(path)
	if u.RawQuery != "" {
		b.WriteString("?")
		b.WriteString(u.RawQuery)
	}
	// fragments are dropped: they are client-side only
	return b.String()
}

// PageCollector accumulates rows across the pages of a single
// provider+domain fetch. It deduplicates rows by their normalized
// equivalence key, tracks which pages failed, and produces a
// deterministic (sorted) output order that does not depend on the
// upstream's pagination order.
type PageCollector struct {
	provider string
	domain   string

	seen         map[string]struct{}
	rows         []string
	pagesFetched uint
	failedPages  []uint
	duplicates   uint
	hitPageCap   bool
}

func NewPageCollector(provider, domain string) *PageCollector {
	return &PageCollector{
		provider: provider,
		domain:   domain,
		seen:     make(map[string]struct{}),
	}
}

// AddPage records the rows of one successfully fetched page. Rows
// already seen under an equivalent key are dropped as duplicates; the
// first-seen spelling is kept. It returns the number of new rows.
func (c *PageCollector) AddPage(page uint, rows []string) int {
	c.pagesFetched++
	added := 0
	for _, row := range rows {
		key := NormalizeURLKey(row)
		if _, ok := c.seen[key]; ok {
			c.duplicates++
			continue
		}
		c.seen[key] = struct{}{}
		c.rows = append(c.rows, row)
		added++
	}
	return added
}

// FailPage marks a page as missing after its retries were exhausted.
// The fetch continues with the remaining pages; the source is
// reported as incomplete at the end.
func (c *PageCollector) FailPage(page uint, err error) {
	c.failedPages = append(c.failedPages, page)
	logrus.WithFields(logrus.Fields{
		"provider": c.provider,
		"domain":   c.domain,
		"page":     page,
	}).Warnf("page failed, will continue with remaining pages: %v", err)
}

// MarkPageCap records that pagination was stopped by the hard page
// cap rather than by an upstream termination signal.
func (c *PageCollector) MarkPageCap() {
	c.hitPageCap = true
}

// Complete reports whether every page was fetched and pagination
// ended on an upstream signal (not the page cap).
func (c *PageCollector) Complete() bool {
	return len(c.failedPages) == 0 && !c.hitPageCap
}

// FailedPages returns the pages that could not be fetched.
func (c *PageCollector) FailedPages() []uint {
	return c.failedPages
}

// IncompleteReason describes why the fetch is incomplete.
func (c *PageCollector) IncompleteReason() string {
	switch {
	case len(c.failedPages) > 0:
		return fmt.Sprintf("missing page(s) %v", c.failedPages)
	case c.hitPageCap:
		return "stopped at page cap"
	default:
		return ""
	}
}

// Sorted returns the deduplicated rows in a deterministic order that
// is independent of the order the upstream returned its pages in.
func (c *PageCollector) Sorted() []string {
	out := make([]string, len(c.rows))
	copy(out, c.rows)
	sort.Slice(out, func(i, j int) bool {
		ki, kj := NormalizeURLKey(out[i]), NormalizeURLKey(out[j])
		if ki != kj {
			return ki < kj
		}
		return out[i] < out[j]
	})
	return out
}

// Report logs how many pages and rows this source produced and
// whether the round was complete. Incomplete rounds are logged at
// error level so they are visible even without --verbose.
func (c *PageCollector) Report() {
	fields := logrus.Fields{
		"provider":   c.provider,
		"domain":     c.domain,
		"pages":      c.pagesFetched,
		"urls":       len(c.rows),
		"duplicates": c.duplicates,
		"complete":   c.Complete(),
	}
	if !c.Complete() {
		logrus.WithFields(fields).Errorf("incomplete fetch: %s", c.IncompleteReason())
		return
	}
	logrus.WithFields(fields).Info("fetch complete")
}
