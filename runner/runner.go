package runner

import (
	"context"
	"fmt"
	"sync"

	"github.com/lc/gau/v2/pkg/provenance"
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

// Starts starts the worker
func (r *Runner) Start(ctx context.Context, workChan chan Work, results chan string) {
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

func (w *Work) Do(ctx context.Context, results chan string) error {
	return w.provider.Fetch(ctx, w.domain, results)
}

// worker checks to see if the context is finished and executes the fetching process for each provider
func (r *Runner) worker(ctx context.Context, workChan chan Work, results chan string) {
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
			}
		}
	}
}

// StartWithSources runs the same worker pool as Start but routes every
// emitted URL through the provenance collector instead of a bare string
// channel. Provider interfaces and their Fetch implementations are untouched:
// a per-work adapter goroutine converts chan string observations into
// structured (domain, provider, rawURL) records, so provenance cannot be
// flattened into a string in an intermediate layer.
func (r *Runner) StartWithSources(ctx context.Context, workChan chan Work, collector *provenance.Collector) {
	for i := uint(0); i < r.threads; i++ {
		r.Add(1)
		go func() {
			defer r.Done()
			r.provenanceWorker(ctx, workChan, collector)
		}()
	}
}

func (r *Runner) provenanceWorker(ctx context.Context, workChan chan Work, collector *provenance.Collector) {
	for {
		select {
		case <-ctx.Done():
			return
		case work, ok := <-workChan:
			if !ok {
				return
			}
			domain, provider := work.domain, work.provider.Name()

			// Circuit breaker: a provider that failed failLimit consecutive
			// runs is skipped this run; the skip is recorded as an explicit
			// status distinct from a fresh failure. The alert is emitted at
			// Error level so it remains visible at the default log level.
			if skip, reason := collector.ShouldSkip(domain, provider); skip {
				logrus.WithField("provider", provider).Error(reason)
				continue
			}

			// One channel + one adapter per work item: observations from
			// different providers can never be attributed to the wrong source,
			// regardless of goroutine scheduling.
			raw := make(chan string)
			var adapterWg sync.WaitGroup
			adapterWg.Add(1)
			go func() {
				defer adapterWg.Done()
				for u := range raw {
					collector.Observe(domain, provider, u)
				}
			}()

			err := work.provider.Fetch(ctx, domain, raw)
			close(raw)
			adapterWg.Wait() // all observations are recorded before Complete

			if err != nil {
				logrus.WithField("provider", provider).Errorf("%s - %v", domain, err)
			}
			collector.Complete(domain, provider, err != nil, err)
		}
	}
}
