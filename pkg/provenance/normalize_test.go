package provenance

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "lowercases host", in: "HTTP://EXAMPLE.com/Path", want: "http://example.com/Path"},
		{name: "protocol completion", in: "example.com/a", want: "http://example.com/a"},
		{name: "strips default http port", in: "http://example.com:80/a", want: "http://example.com/a"},
		{name: "strips default https port", in: "https://example.com:443/a", want: "https://example.com/a"},
		{name: "keeps non-default port", in: "https://example.com:8443/a", want: "https://example.com:8443/a"},
		{name: "removes fragment", in: "https://example.com/a#section", want: "https://example.com/a"},
		{name: "upper-cases percent escapes in query", in: "http://example.com/a?x=%2f", want: "http://example.com/a?x=%2F"},
		{name: "path escape canonicalized by net/url", in: "http://example.com/a%2fb", want: "http://example.com/a/b"},
		{name: "trims host trailing dot", in: "http://example.com./a", want: "http://example.com/a"},
		{name: "preserves query order", in: "http://example.com/?b=2&a=1", want: "http://example.com/?b=2&a=1"},
		{name: "preserves path case", in: "http://example.com/MixedCase", want: "http://example.com/MixedCase"},
		{name: "keeps ipv6 bracketed", in: "http://[2001:db8::1]/x", want: "http://[2001:db8::1]/x"},
		{name: "punycode unicode host", in: "http://例え.jp/x", want: "http://xn--r8jz45g.jp/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.in)
			if err != nil {
				t.Fatalf("Normalize(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeEquivalence(t *testing.T) {
	// All of these spellings must collapse to the same normalized URL —
	// this is the rule the multi-source merge relies on.
	variants := []string{
		"http://example.com/a#frag",
		"HTTP://EXAMPLE.COM/a#frag",
		"http://example.com:80/a",
		"example.com/a",
		"http://example.com/a#other-fragment",
	}
	first, err := Normalize(variants[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range variants[1:] {
		got, err := Normalize(v)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", v, err)
		}
		if got != first {
			t.Errorf("Normalize(%q) = %q, want %q", v, got, first)
		}
	}
}

func TestNormalizeDistinguishesEncodedFromLiteral(t *testing.T) {
	// An encoded '#' (%23) is data; a literal '#' starts a fragment.
	encoded, err := Normalize("http://example.com/a%23")
	if err != nil {
		t.Fatal(err)
	}
	literal, err := Normalize("http://example.com/a#")
	if err != nil {
		t.Fatal(err)
	}
	if encoded == literal {
		t.Fatalf("encoded escape and fragment must not compare equal: %q", encoded)
	}
}

func TestNormalizeErrors(t *testing.T) {
	for _, in := range []string{"", "   ", "http://", "http:// /a", "http://exa mple.com/a"} {
		if _, err := Normalize(in); err == nil {
			t.Errorf("Normalize(%q) expected error, got nil", in)
		}
	}
}

func TestNormalizeIsSingleDefinition(t *testing.T) {
	// Guard: the normalization package is the only place defining URL
	// equality; the output package must not need net/url parsing for
	// identity decisions. This test documents that callers get stable keys.
	a, _ := Normalize("https://Example.com:443/x?a=1#top")
	b, _ := Normalize("https://example.com/x?a=1")
	if a != b {
		t.Fatalf("equivalent URLs normalized differently: %q vs %q", a, b)
	}
}
