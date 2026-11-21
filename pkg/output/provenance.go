package output

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/lc/gau/v2/pkg/provenance"
)

// WriteProvenance writes a full provenance report deterministically. The
// report already holds structured per-source data; nothing is flattened into
// an unparsed string before this point.
//
// Text layout (tab-separated, one URL per line, '#' lines are metadata):
//
//	# gau provenance ...
//	# status: domain=... provider=... status=ok|empty|failed|skipped ...
//	<normalized-url>\t<source-count>\tfirst_seen=<ts>\tadded=<p,...>\tremoved=<p,...>\t<source-entries>
//
// where each source entry is
//
//	prov=<name>|first=<ts>|last=<ts>|raw=<url,url,...>
//
// entries are ';'-joined and ordered by provider name. Tabs/newlines inside a
// raw form are percent-escaped so every record stays on one line. The JSON
// layout is the machine-readable form and preserves all fields exactly.
func WriteProvenance(w io.Writer, report *provenance.Report, blacklistMap mapset.Set[string], removeParameters bool, asJSON bool) error {
	filtered := filterRecords(report, blacklistMap, removeParameters)
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(filtered)
	}
	return writeProvenanceText(w, filtered)
}

// WriteProvenanceFile is the atomic variant for file destinations: the whole
// report is rendered to a sibling temp file and renamed over the target, so a
// failure mid-write can never leave a half-written output file.
func WriteProvenanceFile(filePath string, report *provenance.Report, blacklistMap mapset.Set[string], removeParameters bool, asJSON bool) error {
	var b strings.Builder
	if err := WriteProvenance(&b, report, blacklistMap, removeParameters, asJSON); err != nil {
		return err
	}
	return provenance.AtomicExport(filePath, []byte(b.String()))
}

// filteredReport is a shallow copy with blacklisted / fp-deduplicated records
// removed; statuses and metadata are untouched.
func filterRecords(report *provenance.Report, blacklistMap mapset.Set[string], removeParameters bool) *provenance.Report {
	out := *report
	out.Records = make([]provenance.Record, 0, len(report.Records))
	seenEndpoints := mapset.NewThreadUnsafeSet[string]()
	for _, rec := range report.Records {
		u, err := url.Parse(rec.URL)
		if err != nil {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), ".")
		if ext != "" && blacklistMap.Contains(ext) {
			continue
		}
		if removeParameters {
			endpoint := u.Host + u.Path
			if seenEndpoints.Contains(endpoint) {
				continue
			}
			seenEndpoints.Add(endpoint)
		}
		out.Records = append(out.Records, rec)
	}
	return &out
}

func writeProvenanceText(w io.Writer, report *provenance.Report) error {
	if _, err := fmt.Fprintf(w, "# gau provenance report generated_at=%s domains=%s providers=%s\n",
		report.GeneratedAt, strings.Join(report.Domains, ","), strings.Join(report.Providers, ",")); err != nil {
		return err
	}
	for _, st := range report.Statuses {
		if _, err := fmt.Fprintf(w, "# status: domain=%s provider=%s status=%s observations=%d malformed=%d consecutive_fails=%d",
			st.Domain, st.Provider, st.Status, st.Observations, st.Malformed, st.ConsecutiveFails); err != nil {
			return err
		}
		if st.Error != "" {
			if _, err := fmt.Fprintf(w, " error=%q", st.Error); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}

	for _, rec := range report.Records {
		names := make([]string, 0, len(rec.Sources))
		for name := range rec.Sources {
			names = append(names, name)
		}
		sort.Strings(names)

		entries := make([]string, 0, len(names))
		for _, name := range names {
			src := rec.Sources[name]
			entries = append(entries, fmt.Sprintf("prov=%s|first=%s|last=%s|raw=%s",
				name, src.FirstSeen, src.LastSeen, encodeRaw(src.RawForms)))
		}
		line := fmt.Sprintf("%s\t%d\tfirst_seen=%s\tadded=%s\tremoved=%s\t%s\n",
			rec.URL,
			len(rec.Sources),
			rec.FirstSeen,
			strings.Join(rec.AddedSources, ","),
			strings.Join(rec.RemovedSources, ","),
			strings.Join(entries, ";"))
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
	}
	return nil
}

// encodeRaw makes raw forms safe for single-line TSV: tabs, newlines and
// carriage returns are percent-escaped (the characters themselves cannot
// occur unescaped in a valid URL, so decoding is unambiguous).
func encodeRaw(forms []string) string {
	escaped := make([]string, len(forms))
	for i, f := range forms {
		f = strings.ReplaceAll(f, "\t", "%09")
		f = strings.ReplaceAll(f, "\r", "%0D")
		f = strings.ReplaceAll(f, "\n", "%0A")
		escaped[i] = f
	}
	return strings.Join(escaped, ",")
}
