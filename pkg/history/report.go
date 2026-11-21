package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReportPath returns the on-disk location of the diff report for round id,
// alongside the round files inside the history directory.
func ReportPath(dir string, id int) string {
	return filepath.Join(dir, fmt.Sprintf("report-%06d.json", id))
}

// WriteReportAtomic writes the report to final via a temp file + rename in the
// same directory, matching the round files' no-half-files guarantee.
func WriteReportAtomic(final string, report *Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(final)
	tmp, err := os.CreateTemp(dir, ".report-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp report file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp report file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp report file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp report file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod temp report file: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("rename temp report file: %w", err)
	}
	return nil
}
