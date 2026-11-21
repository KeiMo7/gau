package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/lc/gau/v2/pkg/history"
	"github.com/lc/gau/v2/pkg/output"
	"github.com/lc/gau/v2/pkg/providers"
	"github.com/lc/gau/v2/runner"
	"github.com/lc/gau/v2/runner/flags"
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

	// --history-import: load a legacy output file as the baseline round and
	// exit without contacting any provider. Requires --history for a target
	// directory.
	if config.HistoryImport != "" {
		runImport(config)
		return
	}

	gau := new(runner.Runner)

	if err = gau.Init(config, cfg.Providers, cfg.Filters); err != nil {
		log.Warn(err)
	}

	results := make(chan providers.Result)

	out := os.Stdout
	// Handle results in background
	if config.Output != "" {
		out, err = os.OpenFile(config.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatalf("Could not open output file: %v\n", err)
		}
		defer out.Close()
	}

	// historyEnabled gates every side effect of the diff feature. When false
	// the collector and store are never created and no extra files appear.
	var (
		historyEnabled = config.HistoryDir != ""
		collector      *history.Collector
		store          *history.Store
		startedAt      = time.Now().UTC()
	)
	if historyEnabled {
		collector = history.NewCollector()
		store, err = history.OpenStore(config.HistoryDir)
		if err != nil {
			// Degradation rule: persistence failure must not stop collection.
			// Warn and continue with the feature effectively disabled.
			log.Warnf("history disabled for this run: %v", err)
			historyEnabled = false
			store = nil
		}
	}

	var writeWg sync.WaitGroup
	writeWg.Add(1)
	go func(out io.Writer, JSON bool) {
		defer writeWg.Done()
		// Fan-out: the legacy writer keeps consuming exactly what it consumed
		// before (only the URL string); the optional collector sees the tagged
		// result. The legacy output path and exit codes are unchanged.
		legacy := make(chan providers.Result)
		var fanWg sync.WaitGroup
		fanWg.Add(1)
		go func() {
			defer fanWg.Done()
			if JSON {
				output.WriteURLsJSON(out, legacy, config.Blacklist, config.RemoveParameters)
			} else if werr := output.WriteURLs(out, legacy, config.Blacklist, config.RemoveParameters); werr != nil {
				log.Fatalf("error writing results: %v\n", werr)
			}
		}()
		for result := range results {
			legacy <- result
			if historyEnabled {
				collector.Add(result.URL, result.Provider)
			}
		}
		close(legacy)
		fanWg.Wait()
	}(out, config.JSON)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workChan := make(chan runner.Work)
	gau.Start(ctx, workChan, results)
	domains := flags.Args()
	if len(domains) > 0 {
		for _, provider := range gau.Providers {
			for _, domain := range domains {
				workChan <- runner.NewWork(domain, provider)
			}
		}
	} else {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			domain := sc.Text()
			for _, provider := range gau.Providers {
				workChan <- runner.NewWork(domain, provider)
			}
		}
		if err := sc.Err(); err != nil {
			log.Fatal(err)
		}
	}
	close(workChan)

	// wait for providers to fetch URLS
	gau.Wait()

	// close results channel
	close(results)

	// wait for writer to finish output
	writeWg.Wait()

	if historyEnabled {
		finalizeHistory(store, collector, gau.Failures(), domains, config, startedAt)
	}
}

// runImport handles --history-import: no network access, just persist the
// legacy file's normalized contents as the next round.
func runImport(config *providers.Config) {
	if config.HistoryDir == "" {
		log.Fatal("--history-import requires --history <directory> to specify where the baseline round is stored")
	}
	store, err := history.OpenStore(config.HistoryDir)
	if err != nil {
		log.Fatalf("cannot open history directory: %v", err)
	}
	targets := flags.Args()
	sort.Strings(targets)
	round, err := history.ImportLegacyFile(config.HistoryImport, targets, config.Providers, time.Now().UTC())
	if err != nil {
		log.Fatalf("failed to import legacy output %s: %v", config.HistoryImport, err)
	}
	stored, err := store.Append(round)
	if err != nil {
		log.Fatalf("failed to persist imported round: %v", err)
	}
	log.Warnf("imported baseline round %d with %d URLs into %s", stored.ID, len(stored.Entries), store.Dir())
}

// finalizeHistory persists the round just collected and emits the diff
// against the previous round. All failures are reported as warnings: a write
// or read failure never changes the process exit code, collection output has
// already been delivered by the time this runs.
func finalizeHistory(store *history.Store, collector *history.Collector, failures []providers.Failure,
	targets []string, config *providers.Config, startedAt time.Time) {

	roundFailures := make([]history.Failure, 0, len(failures))
	for _, f := range failures {
		roundFailures = append(roundFailures, history.Failure{Provider: f.Provider, Domain: f.Domain, Error: f.Error})
	}
	sort.Slice(roundFailures, func(i, j int) bool {
		if roundFailures[i].Provider != roundFailures[j].Provider {
			return roundFailures[i].Provider < roundFailures[j].Provider
		}
		return roundFailures[i].Domain < roundFailures[j].Domain
	})

	sortedTargets := append([]string(nil), targets...)
	sort.Strings(sortedTargets)
	sortedProviders := append([]string(nil), config.Providers...)
	sort.Strings(sortedProviders)

	round := &history.Round{
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		Providers:  sortedProviders,
		Targets:    sortedTargets,
		Failures:   roundFailures,
		Entries:    collector.Snapshot(),
	}

	stored, err := store.Append(round)
	if err != nil {
		log.Warnf("failed to persist history round (collection output is unaffected): %v", err)
		return
	}
	log.Warnf("history: persisted round %d (%d URLs, %d provider failures) to %s",
		stored.ID, len(stored.Entries), len(stored.Failures), store.Dir())

	// Load the window used for diffing/trends. The current round is included
	// so the last pair always diffs even when --history-rounds is small.
	rounds, loadErrs, err := store.LoadRecent(config.HistoryRounds)
	if err != nil {
		log.Warnf("history: cannot read previous rounds, this run is persisted but no diff was produced: %v", err)
		return
	}
	for _, loadErr := range loadErrs {
		log.Warnf("history: %v (round skipped; surrounding rounds remain usable)", loadErr)
	}

	report := buildReport(rounds, stored)
	writeReport(store, report)
	printSummary(report, rounds)
}

func buildReport(rounds []*history.Round, current *history.Round) *history.Report {
	var prev *history.Round
	for i := len(rounds) - 1; i >= 0; i-- {
		if rounds[i].ID < current.ID {
			prev = rounds[i]
			break
		}
	}
	return history.Diff(prev, current)
}

func writeReport(store *history.Store, report *history.Report) {
	path := history.ReportPath(store.Dir(), report.CurrentRound)
	if err := history.WriteReportAtomic(path, report); err != nil {
		log.Warnf("history: failed to write diff report (round data is unaffected): %v", err)
		return
	}
	log.Warnf("history: diff report written to %s", path)
}

func printSummary(report *history.Report, rounds []*history.Round) {
	if report.Baseline {
		log.Warnf("history: no previous round found — round %d is the baseline; only a full snapshot is available, re-run after the next collection to get an incremental diff",
			report.CurrentRound)
		return
	}
	log.Warnf("history: round %d vs %d — added=%d removed=%d persistent=%d changed=%d suppressed(down-source)=%d",
		report.CurrentRound, report.PreviousRound,
		len(report.Added), len(report.Removed), len(report.Persistent), len(report.Changed), len(report.Suppressed))

	// Trend aggregation needs at least one earlier pair beyond the baseline.
	if len(rounds) >= 2 {
		trends := history.Trends(rounds)
		for _, t := range trends {
			log.Warnf("history: trend domain=%s verdict=%s totals=%v added=%v removed=%v",
				t.Domain, t.Verdict, t.Total, t.Added, t.Removed)
		}
	}
}
