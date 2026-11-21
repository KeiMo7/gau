package runner

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/lc/gau/v2/pkg/providers"
)

type fakeProvider struct {
	name    string
	urls    map[string][]string // domain -> urls to emit
	failFor map[string]error    // domains that must return an error
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Fetch(ctx context.Context, domain string, results chan string) error {
	if err, ok := f.failFor[domain]; ok {
		return err
	}
	for _, u := range f.urls[domain] {
		select {
		case results <- u:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func TestRunner_TaggingAndFailureRecording(t *testing.T) {
	r := new(Runner)
	boom := errors.New("connection reset by peer")
	good := &fakeProvider{
		name: "wayback",
		urls: map[string][]string{
			"example.com": {"https://example.com/a", "https://example.com/b"},
		},
	}
	bad := &fakeProvider{
		name:    "otx",
		failFor: map[string]error{"example.com": boom},
	}
	r.Providers = []providers.Provider{good, bad}
	r.threads = 2

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workChan := make(chan Work)
	results := make(chan providers.Result)
	r.Start(ctx, workChan, results)

	var collected []providers.Result
	done := make(chan struct{})
	go func() {
		for res := range results {
			collected = append(collected, res)
		}
		close(done)
	}()

	workChan <- NewWork("example.com", good)
	workChan <- NewWork("example.com", bad)
	close(workChan)
	r.Wait()
	close(results)
	<-done

	if len(collected) != 2 {
		t.Fatalf("expected 2 tagged results, got %d", len(collected))
	}
	for _, res := range collected {
		if res.Provider != "wayback" || res.Domain != "example.com" {
			t.Fatalf("provenance tags wrong: %+v", res)
		}
	}

	failures := r.Failures()
	if len(failures) != 1 || failures[0].Provider != "otx" || failures[0].Domain != "example.com" {
		t.Fatalf("failure recording wrong: %+v", failures)
	}
	if failures[0].Error != boom.Error() {
		t.Fatalf("failure message not preserved: %+v", failures[0])
	}

	// Snapshot is independent of call timing.
	again := r.Failures()
	sort.Slice(again, func(i, j int) bool { return again[i].Provider < again[j].Provider })
	if again[0].Provider != failures[0].Provider {
		t.Fatal("failure snapshot is unstable")
	}
}

func TestRunner_ContextCancellationIsDeterministic(t *testing.T) {
	// A provider that blocks until the context is cancelled must return
	// promptly with no goroutine leak and no partial result panic.
	r := new(Runner)
	blocker := &blockingProvider{name: "wayback"}
	r.Providers = []providers.Provider{blocker}
	r.threads = 1

	ctx, cancel := context.WithCancel(context.Background())
	workChan := make(chan Work)
	results := make(chan providers.Result)
	r.Start(ctx, workChan, results)

	go func() {
		workChan <- NewWork("example.com", blocker)
	}()
	cancel()
	r.Wait()
	// Closing the results channel after Wait must not panic: the relay must
	// have exited via the cancelled context.
	close(results)
}

type blockingProvider struct{ name string }

func (b *blockingProvider) Name() string { return b.name }

func (b *blockingProvider) Fetch(ctx context.Context, domain string, results chan string) error {
	<-ctx.Done()
	return ctx.Err()
}
