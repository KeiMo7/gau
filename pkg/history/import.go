package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// ImportLegacy reads an output file produced by older gau versions (plain
// text with one URL per line, or JSON Lines as emitted by --json) and builds
// a baseline Round. The data is run through the same Normalize path as live
// collection, so an imported baseline diffs correctly against new rounds.
//
// Compatibility boundary: legacy files carry no provenance, so every imported
// entry is tagged with the sentinel provider LegacyProvider. A failure of any
// real provider in the next round can therefore never "suppress" the
// disappearance of an imported URL — imported URLs are only ever confirmed
// removed when a fully successful round omits them.
func ImportLegacy(r io.Reader, targets []string, providers []string, started time.Time) (*Round, error) {
	collector := NewCollector()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		rawURL := line
		if strings.HasPrefix(line, "{") {
			var jr struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal([]byte(line), &jr); err != nil {
				return nil, fmt.Errorf("line %d: invalid JSON line: %w", lineNo, err)
			}
			rawURL = jr.URL
		}
		if rawURL != "" {
			collector.Add(rawURL, LegacyProvider)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read legacy file: %w", err)
	}

	round := &Round{
		StartedAt:  started,
		FinishedAt: time.Now().UTC(),
		Providers:  append([]string(nil), providers...),
		Targets:    append([]string(nil), targets...),
		Entries:    collector.Snapshot(),
	}
	sort.Strings(round.Providers)
	sort.Strings(round.Targets)
	return round, nil
}

// ImportLegacyFile opens path and delegates to ImportLegacy.
func ImportLegacyFile(path string, targets []string, providers []string, started time.Time) (*Round, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ImportLegacy(f, targets, providers, started)
}

// LegacyProvider is the provenance tag for entries imported from pre-history
// gau output files.
const LegacyProvider = "legacy-import"
