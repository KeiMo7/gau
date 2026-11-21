package runner

import (
	"context"
	"fmt"
	"sync"

	"github.com/lc/gau/v2/pkg/providers"
	"github.com/lc/gau/v2/pkg/providers/commoncrawl"
	"github.com/lc/gau/v2/pkg/providers/otx"
	"github.com/lc/gau/v2/pkg/providers/urlscan"
	"github.com/lc/gau/v2/pkg/providers/wayback"
	"github.com/sirupsen/logrus"
)

type Runner struct {
	sync.WaitGroup

	Providers []providers.Provider
	threads   uint

	failMu sync.Mutex
	// failures records every (provider, domain) fetch that returned an error.
	// The history feature consults it to distinguish "source stopped providing
	// a URL" from "source was unreachable this round".
	failures []providers.Failure
}

// Init initializes the runner
func (r *Runner) Init(c *providers.Config, providers []string, filters providers.Filters) error {
	r.threads = c.Threads
	for _, name := range providers {
		switch name {
		case "urlscan":
			r.Providers = append(r.Providers, urlscan.New(c))
		case "otx":
			r.Providers = append(r.Providers, otx.New(c))
		case "wayback":
			r.Providers = append(r.Providers, wayback.New(c, filters))
		case "commoncrawl":
			cc, err := commoncrawl.New(c, filters)
			if err != nil {
				return fmt.Errorf("error instantiating commoncrawl: %v\n", err)
			}
			r.Providers = append(r.Providers, cc)
		}
	}

	return nil
}

// Starts starts the workers. The results channel now carries tagged
// providers.Result values; the Provider Fetch interface is unchanged.
func (r *Runner) Start(ctx context.Context, workChan chan Work, results chan providers.Result) {
	for i := uint(0); i < r.threads; i++ {
		r.Add(1)
		go func() {
			defer r.Done()
			r.worker(ctx, workChan, results)
		}()
	}
}

type Work struct {
	domain   string
	provider providers.Provider
}

func NewWork(domain string, provider providers.Provider) Work {
	return Work{domain, provider}
}

// Do runs the fetch on a provider-scoped channel, re-tags every emitted URL
// with its provider and domain, and relays it onto the shared results channel.
// A short-lived relay goroutine preserves the original back-pressure: the
// fetch still blocks until the consumer accepts each URL, so behavior under a
// stalled or cancelled consumer is identical to the old string channel.
func (w *Work) Do(ctx context.Context, results chan providers.Result) error {
	raw := make(chan string)
	done := make(chan error, 1)

	go func() {
		// Providers never close the channel they write to (it is owned by the
		// runner), so the relay closes it once Fetch returns. Otherwise this
		// goroutine would leak on every failing or cancelled fetch.
		defer close(raw)
		done <- w.provider.Fetch(ctx, w.domain, raw)
		close(done)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case u, ok := <-raw:
			if !ok {
				return <-done
			}
			select {
			case results <- providers.Result{URL: u, Provider: w.provider.Name(), Domain: w.domain}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// Failures returns a snapshot of the (provider, domain) fetches that errored
// during the run.
func (r *Runner) Failures() []providers.Failure {
	r.failMu.Lock()
	defer r.failMu.Unlock()
	out := make([]providers.Failure, len(r.failures))
	copy(out, r.failures)
	return out
}

func (r *Runner) recordFailure(work Work, err error) {
	r.failMu.Lock()
	r.failures = append(r.failures, providers.Failure{
		Provider: work.provider.Name(),
		Domain:   work.domain,
		Error:    err.Error(),
	})
	r.failMu.Unlock()
}

// worker checks to see if the context is finished and executes the fetching process for each provider
func (r *Runner) worker(ctx context.Context, workChan chan Work, results chan providers.Result) {
	for {
		select {
		case <-ctx.Done():
			return
		case work, ok := <-workChan:
			if !ok {
				return
			}
			if err := work.Do(ctx, results); err != nil {
				logrus.WithField("provider", work.provider.Name()).Warnf("%s - %v", work.domain, err)
				r.recordFailure(work, err)
			}
		}
	}
}
