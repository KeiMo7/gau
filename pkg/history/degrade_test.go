package history

import (
	"os"
	"path/filepath"
	"testing"
)

// A garbled round with a syntax error must report the exact byte offset.
func TestStore_SyntaxErrorOffset(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "round-000001.json")
	payload := []byte(`{"id":1,"entries": NOTJSON}`)
	if err := os.WriteFile(bad, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	_, errs, err := store.LoadRecent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 corruption error, got %d", len(errs))
	}
	ce := errs[0].(*CorruptionError)
	// "N" of NOTJSON is at byte offset 20 (0-indexed: the char after the space).
	if ce.Offset != 20 {
		t.Fatalf("expected offset 20, got %d (%v)", ce.Offset, ce)
	}
}

// Writing into a read-only directory must surface an error rather than panic;
// the collector built beforehand keeps working, modeling "warn then continue".
func TestStore_WriteFailureIsRecoverable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	parent := t.TempDir()
	ro := filepath.Join(parent, "readonly")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o755)

	store, err := OpenStore(ro)
	if err != nil {
		t.Fatal(err)
	}
	collector := NewCollector()
	collector.Add("https://example.com/a", "wayback")
	round := &Round{Entries: collector.Snapshot()}

	if _, err := store.Append(round); err == nil {
		t.Fatal("expected write failure in read-only directory")
	}
	// The collector is unaffected: collection output can still proceed.
	if got := collector.Len(); got != 1 {
		t.Fatalf("collector state lost after write failure: %d", got)
	}
}

// Reports live beside round files but never disturb round loading.
func TestStore_ReportsAreIgnoredByLoader(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(round(1, []Entry{entry("https://example.com/a", nil, "example.com", "a", "wayback")})); err != nil {
		t.Fatal(err)
	}
	if err := WriteReportAtomic(ReportPath(dir, 1), &Report{CurrentRound: 1}); err != nil {
		t.Fatal(err)
	}
	rounds, errs, err := store.LoadRecent(0)
	if err != nil || len(errs) != 0 {
		t.Fatalf("report file must not be treated as corrupt round: err=%v errs=%v", err, errs)
	}
	if len(rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(rounds))
	}
}
