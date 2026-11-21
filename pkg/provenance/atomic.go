package provenance

import (
	"os"
	"path/filepath"
)

// atomicWrite writes data to path without ever leaving a partial file behind:
// it writes a sibling temp file, flushes it to disk, then atomically renames
// it over the destination. A crash mid-write leaves the previous file intact
// and an unlinked temp file, never a truncated target.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".gau-prov-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
