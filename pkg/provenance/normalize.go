// Package provenance implements optional, opt-in URL provenance tracking:
// every URL emitted by a provider is kept together with the provider that
// produced it, its original (unmodified) form and cross-run history.
//
// All URL normalization in the codebase happens in this single file:
// protocol completion, default port stripping, case folding, percent-encoding
// case handling and fragment removal are defined nowhere else.
package provenance

import (
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// Normalize is the single definition of URL equality used by provenance
// tracking. Two raw strings describe the same URL if and only if their
// Normalize results are identical.
//
// Rules (in order):
//  1. inputs without a scheme are treated as http:// URLs (protocol completion)
//  2. unparseable inputs yield an error and are counted as malformed by callers
//  3. host is lower-cased; Unicode hosts are converted to punycode (IDNA)
//  4. scheme is lower-cased; default ports (http:80, https:443) are stripped
//  5. percent-escapes are upper-cased (e.g. %2f -> %2F)
//  6. fragments are removed (the fragment identifier is client-side state,
//     not part of the resource identifier)
//  7. a trailing "." on a host name is trimmed
//
// Everything not covered by an explicit rule is preserved byte-for-byte:
// query parameter order, repeated parameters, non-default ports, path case
// and trailing slashes all remain significant.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrEmptyURL
	}
	if !hasScheme(raw) {
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", ErrNoHost
	}

	scheme := strings.ToLower(u.Scheme)

	host := u.Hostname()
	host = strings.TrimSuffix(host, ".")
	host = strings.ToLower(host)
	if ascii, err := idna.Lookup.ToASCII(host); err == nil {
		host = ascii
	}
	// Reject obviously broken hosts (e.g. spaces) instead of silently
	// treating them as valid URLs.
	if host == "" || strings.ContainsAny(host, " \t") {
		return "", ErrInvalidHost
	}

	port := u.Port()
	switch {
	case port == "":
	case scheme == "http" && port == "80":
		port = ""
	case scheme == "https" && port == "443":
		port = ""
	}
	if net.ParseIP(host) != nil && strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	if port != "" {
		u.Host = u.Host + ":" + port
	}

	u.Scheme = scheme
	u.Path = normalizeEscapes(u.Path)
	u.RawPath = ""
	u.RawQuery = normalizeEscapes(u.RawQuery)
	u.Fragment = ""
	u.RawFragment = ""

	return u.String(), nil
}

// hasScheme reports whether raw begins with a RFC 3986 scheme followed by ":".
func hasScheme(raw string) bool {
	if len(raw) < 2 {
		return false
	}
	c := raw[0]
	if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
		return false
	}
	for i := 1; i < len(raw); i++ {
		switch raw[i] {
		case ':':
			return i > 0
		case '/', '?', '#':
			return false
		}
	}
	return false
}

// normalizeEscapes upper-cases the hex digits of every percent-escape so that
// "%2f" and "%2F" normalize to the same string. The escape must be well
// formed; malformed escapes are left untouched (url.Parse would have already
// rejected an invalid percent-escape in a query, and an unescaped literal is
// legal in a path).
func normalizeEscapes(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte('%')
			b.WriteByte(toUpperHex(s[i+1]))
			b.WriteByte(toUpperHex(s[i+2]))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func toUpperHex(b byte) byte {
	if b >= 'a' && b <= 'f' {
		return b - ('a' - 'A')
	}
	return b
}
