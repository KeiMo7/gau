package providers

import "testing"

func TestNormalizeURLKey(t *testing.T) {
	equivalent := [][]string{
		{
			"http://example.com/path",
			"http://example.com/path/",
			"http://example.com:80/path",
			"http://example.com/path#frag",
			"HTTP://EXAMPLE.COM/path",
		},
		{
			"https://example.com/",
			"https://example.com",
			"https://example.com:443/",
			"https://example.com/#top",
		},
	}
	for _, group := range equivalent {
		want := NormalizeURLKey(group[0])
		for _, raw := range group[1:] {
			if got := NormalizeURLKey(raw); got != want {
				t.Errorf("NormalizeURLKey(%q) = %q, want %q (same as %q)", raw, got, want, group[0])
			}
		}
	}

	distinct := [][2]string{
		{"http://example.com:8080/a", "http://example.com/a"},            // non-default port
		{"http://example.com/a?x=1", "http://example.com/a"},             // query matters
		{"http://example.com/a?x=1&y=2", "http://example.com/a?y=2&x=1"}, // query order matters
		{"http://example.com/a", "https://example.com/a"},                // scheme matters
		{"http://a.example.com/", "http://b.example.com/"},               // host matters
	}
	for _, pair := range distinct {
		if NormalizeURLKey(pair[0]) == NormalizeURLKey(pair[1]) {
			t.Errorf("NormalizeURLKey(%q) should differ from NormalizeURLKey(%q)", pair[0], pair[1])
		}
	}

	// unparseable input falls back to the raw string
	if got := NormalizeURLKey("not a url at all"); got != "not a url at all" {
		t.Errorf("expected raw fallback, got %q", got)
	}
}

func TestPageCollectorDedupeAndOrder(t *testing.T) {
	c := NewPageCollector("test", "example.com")
	c.AddPage(0, []string{"http://example.com/b", "http://example.com/a"})
	c.AddPage(1, []string{"http://example.com/a/", "http://example.com/c"})

	got := c.Sorted()
	want := []string{"http://example.com/a", "http://example.com/b", "http://example.com/c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if !c.Complete() {
		t.Fatal("collector with no failures should be complete")
	}

	c.FailPage(2, errTest)
	if c.Complete() {
		t.Fatal("collector with a failed page should be incomplete")
	}
	if c.IncompleteReason() != "missing page(s) [2]" {
		t.Fatalf("unexpected reason: %s", c.IncompleteReason())
	}
}

var errTest = testError("boom")

type testError string

func (e testError) Error() string { return string(e) }
