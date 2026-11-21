// Package history implements run-to-run (round-to-round) diffing of collected
// URLs. Everything in this package is opt-in: when the history flags are not
// provided the rest of gau behaves exactly as before and no files are created.
package history

import (
	"net/url"
	"sort"
	"strings"

	"github.com/bobesa/go-domain-util/domainutil"
)

// Identity is the normalized identity of a collected URL.
//
// ID is the query-independent identity (scheme + host + canonical path).
// Two URLs that differ only by encoding, default port, parameter order or
// fragment normalize to the same ID. QueryKeys holds the sorted set of query
// parameter names and is tracked separately so that a parameter-structure
// change on the same endpoint is reported as a change rather than as an
// add/remove pair.
type Identity struct {
	ID        string
	QueryKeys []string
	Domain    string
	Sample    string
}

// Normalize computes the normalized identity of a raw URL string.
//
// Determinism rules:
//   - scheme and host are lower-cased;
//   - default ports (80 for http, 443 for https) are stripped;
//   - percent-encoding is canonicalized (e.g. "%7E" and "~" are equivalent,
//     "%2F" decodes to a path separator);
//   - query parameter names are sorted and de-duplicated, values are ignored;
//   - the fragment is ignored;
//   - URLs that fail to parse (or have no host) fall back to the trimmed raw
//     string as identity, so malformed upstream data never panics and still
//     compares deterministically across rounds.
func Normalize(raw string) Identity {
	if strings.TrimSpace(raw) == "" {
		return Identity{}
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Identity{
			ID:     fallbackID(raw),
			Domain: fallbackDomain(raw),
			Sample: raw,
		}
	}

	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = host + ":" + port
	}

	// Clearing RawPath forces EscapedPath to re-encode the decoded Path with
	// canonical minimal escaping, which collapses encoding-only differences.
	u.RawPath = ""
	canonicalPath := u.EscapedPath()
	if canonicalPath == "" {
		canonicalPath = "/"
	}

	var id strings.Builder
	id.WriteString(scheme)
	id.WriteString("://")
	if u.User != nil {
		id.WriteString(u.User.String())
		id.WriteString("@")
	}
	id.WriteString(host)
	id.WriteString(canonicalPath)

	return Identity{
		ID:        id.String(),
		QueryKeys: queryKeys(u),
		Domain:    registrableDomain(host),
		Sample:    raw,
	}
}

func queryKeys(u *url.URL) []string {
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil
	}
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func registrableDomain(host string) string {
	if d := domainutil.Domain(host); d != "" {
		return d
	}
	return fallbackDomain(host)
}

func fallbackID(raw string) string {
	return strings.TrimSpace(raw)
}

// fallbackDomain makes a best-effort registrable-domain guess for unparseable
// input by taking the last two dot-separated labels.
func fallbackDomain(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "*.")
	if i := strings.Index(trimmed, "/"); i >= 0 {
		trimmed = trimmed[:i]
	}
	labels := strings.Split(trimmed, ".")
	if len(labels) >= 2 {
		return strings.Join(labels[len(labels)-2:], ".")
	}
	return trimmed
}
