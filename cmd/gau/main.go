package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"sync"

	"github.com/lc/gau/v2/pkg/output"
	"github.com/lc/gau/v2/pkg/provenance"
	"github.com/lc/gau/v2/pkg/providers"
	"github.com/lc/gau/v2/runner"
	"github.com/lc/gau/v2/runner/flags"
	log "github.com/sirupsen/logrus"
)

func main() {
	cfg, err := flags.New().ReadInConfig()
	if err != nil {
		log.Warnf("error reading config: %v", err)
	}

	config, err := cfg.ProviderConfig()
	if err != nil {
		log.Fatal(err)
	}

	gau := new(runner.Runner)

	if err = gau.Init(config, cfg.Providers, cfg.Filters); err != nil {
		log.Warn(err)
	}

	domains := collectDomains()

	if config.WithSources {
		runWithSources(gau, config, domains)
		return
	}
	runLegacy(gau, config, domains)
}

// runLegacy is the original streaming pipeline, unchanged: providers write
// bare URL strings into a channel consumed directly by the output writer.
func runLegacy(gau *runner.Runner, config *providers.Config, domains []string) {
	results := make(chan string)

	out := os.Stdout
	// Handle results in background
	if config.Output != "" {
		var err error
		out, err = os.OpenFile(config.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatalf("Could not open output file: %v\n", err)
		}
		defer out.Close()
	}

	var writeWg sync.WaitGroup
	writeWg.Add(1)
	go func(out io.Writer, JSON bool) {
		defer writeWg.Done()
		if JSON {
			output.WriteURLsJSON(out, results, config.Blacklist, config.RemoveParameters)
		} else if err := output.WriteURLs(out, results, config.Blacklist, config.RemoveParameters); err != nil {
			log.Fatalf("error writing results: %v\n", err)
		}
	}(out, config.JSON)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workChan := make(chan runner.Work)
	gau.Start(ctx, workChan, results)
	enqueue(workChan, gau.Providers, domains)
	close(workChan)

	// wait for providers to fetch URLS
	gau.Wait()

	// close results channel
	close(results)

	// wait for writer to finish output
	writeWg.Wait()
}

// runWithSources is the opt-in provenance pipeline. Fetching uses the same
// providers and worker pool, but every observation is attributed before any
// aggregation happens.
func runWithSources(gau *runner.Runner, config *providers.Config, domains []string) {
	collector, err := provenance.NewCollector(config.ProvState, int(config.ProvFailLimit), domains, config.Providers)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workChan := make(chan runner.Work)
	gau.StartWithSources(ctx, workChan, collector)
	enqueue(workChan, gau.Providers, domains)
	close(workChan)
	gau.Wait()

	report := collector.Finalize()

	// All providers failing for a domain is a hard error: never emit an empty
	// list that could be mistaken for "no URLs exist". Health counters are
	// still persisted so the circuit breaker remembers the failed run.
	if failed := collector.AllProvidersFailed(); len(failed) > 0 {
		if saveErr := collector.Save(report); saveErr != nil {
			log.Errorf("failed to persist provenance state: %v", saveErr)
		}
		for _, st := range failed {
			if st.Status == "skipped" {
				log.Errorf("provider %s skipped for %s after %d consecutive failures", st.Provider, st.Domain, st.ConsecutiveFails)
				continue
			}
			log.Errorf("provider %s failed for %s: %s", st.Provider, st.Domain, st.Error)
		}
		log.Fatalf("all providers failed for one or more domains; no output written")
	}

	// State is persisted before output is rendered; both writes are atomic,
	// so a crash leaves either the old or the new file, never a truncated one.
	if err := collector.Save(report); err != nil {
		log.Fatalf("failed to persist provenance state: %v", err)
	}

	if config.Output != "" {
		if err := output.WriteProvenanceFile(config.Output, report, config.Blacklist, config.RemoveParameters, config.JSON); err != nil {
			log.Fatalf("error writing results: %v", err)
		}
		return
	}
	if err := output.WriteProvenance(os.Stdout, report, config.Blacklist, config.RemoveParameters, config.JSON); err != nil {
		log.Fatalf("error writing results: %v", err)
	}
}

func collectDomains() []string {
	domains := flags.Args()
	if len(domains) > 0 {
		return domains
	}
	var out []string
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	if err := sc.Err(); err != nil {
		log.Fatal(err)
	}
	return out
}

func enqueue(workChan chan<- runner.Work, providers []providers.Provider, domains []string) {
	for _, provider := range providers {
		for _, domain := range domains {
			workChan <- runner.NewWork(domain, provider)
		}
	}
}
