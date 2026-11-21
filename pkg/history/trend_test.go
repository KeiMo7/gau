package history

import "testing"

func TestTrends_GrowingShrinkingAndDeterminism(t *testing.T) {
	// example.com: grows every round (1 -> 2 -> 3 entries).
	// shrink.io:   shrinks every round (3 -> 2 -> 1 entries).
	rounds := []*Round{
		round(1, []Entry{
			entry("https://example.com/a", nil, "example.com", "a", "wayback"),
			entry("https://shrink.io/x", nil, "shrink.io", "x", "wayback"),
			entry("https://shrink.io/y", nil, "shrink.io", "y", "wayback"),
			entry("https://shrink.io/z", nil, "shrink.io", "z", "wayback"),
		}),
		round(2, []Entry{
			entry("https://example.com/a", nil, "example.com", "a", "wayback"),
			entry("https://example.com/b", nil, "example.com", "b", "wayback"),
			entry("https://shrink.io/x", nil, "shrink.io", "x", "wayback"),
			entry("https://shrink.io/y", nil, "shrink.io", "y", "wayback"),
		}),
		round(3, []Entry{
			entry("https://example.com/a", nil, "example.com", "a", "wayback"),
			entry("https://example.com/b", nil, "example.com", "b", "wayback"),
			entry("https://example.com/c", nil, "example.com", "c", "wayback"),
			entry("https://shrink.io/x", nil, "shrink.io", "x", "wayback"),
		}),
	}

	trends := Trends(rounds)
	if len(trends) != 2 {
		t.Fatalf("expected trends for 2 domains, got %d", len(trends))
	}
	// Sorted by descending growth: example.com first.
	if trends[0].Domain != "example.com" || trends[0].Verdict != TrendGrowing {
		t.Fatalf("growing trend wrong: %+v", trends[0])
	}
	if trends[1].Domain != "shrink.io" || trends[1].Verdict != TrendShrinking {
		t.Fatalf("shrinking trend wrong: %+v", trends[1])
	}
	if len(trends[0].RoundIDs) != 2 || trends[0].RoundIDs[0] != 2 || trends[0].RoundIDs[1] != 3 {
		t.Fatalf("round alignment wrong: %+v", trends[0].RoundIDs)
	}

	// Determinism: repeated invocation over the same rounds is identical.
	again := Trends(rounds)
	if len(again) != len(trends) {
		t.Fatal("trend output is not deterministic")
	}
	for i := range trends {
		if trends[i].Domain != again[i].Domain || trends[i].Verdict != again[i].Verdict {
			t.Fatal("trend output is not deterministic across runs")
		}
	}
}

func TestTrends_StableVerdictAndSingleRound(t *testing.T) {
	// One add and one remove across the window -> equal churn -> stable.
	rounds := []*Round{
		round(1, []Entry{
			entry("https://example.com/a", nil, "example.com", "a", "wayback"),
		}),
		round(2, []Entry{
			entry("https://example.com/b", nil, "example.com", "b", "wayback"),
		}),
	}
	trends := Trends(rounds)
	if len(trends) != 1 || trends[0].Verdict != TrendStable {
		t.Fatalf("expected stable verdict, got %+v", trends)
	}

	if Trends(rounds[:1]) != nil {
		t.Fatal("a single round cannot produce a trend")
	}
}
