package output

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/lc/gau/v2/pkg/provenance"
)

func sampleReport() *provenance.Report {
	return &provenance.Report{
		GeneratedAt: "2026-01-01T00:00:00Z",
		Domains:     []string{"d.com"},
		Providers:   []string{"otx", "wayback"},
		Records: []provenance.Record{
			{
				URL:       "http://d.com/three",
				FirstSeen: "2026-01-01T00:00:00Z",
				Sources: map[string]provenance.SourceInfo{
					"wayback": {FirstSeen: "2026-01-01T00:00:00Z", LastSeen: "2026-01-01T00:00:00Z", RawForms: []string{"http://d.com/three"}},
					"otx":     {FirstSeen: "2026-01-01T00:00:00Z", LastSeen: "2026-01-01T00:00:00Z", RawForms: []string{"http://d.com/three"}},
				},
			},
			{
				URL:       "http://d.com/pic.png",
				FirstSeen: "2026-01-01T00:00:00Z",
				Sources: map[string]provenance.SourceInfo{
					"wayback": {FirstSeen: "2026-01-01T00:00:00Z", LastSeen: "2026-01-01T00:00:00Z", RawForms: []string{"http://d.com/pic.png"}},
				},
			},
		},
		Statuses: []provenance.ProviderStatus{
			{Provider: "otx", Domain: "d.com", Status: "ok", Observations: 1},
			{Provider: "wayback", Domain: "d.com", Status: "ok", Observations: 2},
		},
	}
}

func TestWriteProvenanceTextContainsStructuredSources(t *testing.T) {
	var b bytes.Buffer
	bl := mapset.NewThreadUnsafeSet[string]("png")
	if err := WriteProvenance(&b, sampleReport(), bl, false, false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "# status: domain=d.com provider=otx status=ok") {
		t.Errorf("status header missing:\n%s", out)
	}
	if !strings.Contains(out, "prov=wayback|") || !strings.Contains(out, "prov=otx|") {
		t.Errorf("per-source entries missing:\n%s", out)
	}
	if strings.Contains(out, "pic.png") {
		t.Errorf("blacklisted extension must be filtered:\n%s", out)
	}
	// multi-source URL ranks first (priority by source count).
	lines := nonCommentLines(out)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "http://d.com/three\t2\t") {
		t.Errorf("unexpected ordered output: %v", lines)
	}
}

func TestWriteProvenanceJSONRoundTrip(t *testing.T) {
	var b bytes.Buffer
	bl := mapset.NewThreadUnsafeSet[string]()
	if err := WriteProvenance(&b, sampleReport(), bl, false, true); err != nil {
		t.Fatal(err)
	}
	var rep provenance.Report
	if err := json.Unmarshal(b.Bytes(), &rep); err != nil {
		t.Fatalf("JSON output must be parseable: %v", err)
	}
	if len(rep.Records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(rep.Records))
	}
	if _, ok := rep.Records[0].Sources["wayback"]; !ok {
		t.Error("structured source info lost in JSON output")
	}
}

func TestWriteProvenanceFileAtomicNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	bl := mapset.NewThreadUnsafeSet[string]()
	if err := WriteProvenanceFile(path, sampleReport(), bl, false, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || !strings.HasPrefix(string(data), "# gau provenance report") {
		t.Fatalf("output file contents unexpected: %q", data)
	}
	// no temp files left behind after a successful write.
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("expected exactly one file (no temp leftovers), got %v", entries)
	}
}

func TestRemoveParametersDedup(t *testing.T) {
	rep := &provenance.Report{
		GeneratedAt: "2026-01-01T00:00:00Z",
		Records: []provenance.Record{
			{URL: "http://d.com/x?a=1", FirstSeen: "2026-01-01T00:00:00Z", Sources: map[string]provenance.SourceInfo{"p": {}}},
			{URL: "http://d.com/x?b=2", FirstSeen: "2026-01-01T00:00:00Z", Sources: map[string]provenance.SourceInfo{"p": {}}},
		},
	}
	var b bytes.Buffer
	bl := mapset.NewThreadUnsafeSet[string]()
	if err := WriteProvenance(&b, rep, bl, true, false); err != nil {
		t.Fatal(err)
	}
	if got := len(nonCommentLines(b.String())); got != 1 {
		t.Errorf("fp mode should keep one endpoint, got %d lines", got)
	}
}

func nonCommentLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}
