package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStore_AppendAtomicAndLoadRecent(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 3; i++ {
		stored, err := store.Append(round(i, []Entry{
			entry("https://example.com/r"+string(rune('0'+i)), nil, "example.com", "s", "wayback"),
		}))
		if err != nil {
			t.Fatal(err)
		}
		if stored.ID != i {
			t.Fatalf("expected round id %d, got %d", i, stored.ID)
		}
	}

	// No temp files may remain after successful writes.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("leftover temp file after atomic write: %s", e.Name())
		}
	}

	rounds, errs, err := store.LoadRecent(0)
	if err != nil || len(errs) != 0 {
		t.Fatalf("unexpected load error: %v errs=%v", err, errs)
	}
	if len(rounds) != 3 || rounds[0].ID != 1 || rounds[2].ID != 3 {
		t.Fatalf("round order wrong: %+v", rounds)
	}

	limited, _, _ := store.LoadRecent(2)
	if len(limited) != 2 || limited[0].ID != 2 {
		t.Fatalf("limit window wrong: %+v", limited)
	}

	r2, err := store.LoadRound(2)
	if err != nil || r2.ID != 2 {
		t.Fatalf("LoadRound failed: %v", err)
	}
}

func TestStore_CorruptionIsLocatedAndScoped(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(round(1, []Entry{entry("https://example.com/a", nil, "example.com", "a", "wayback")})); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(round(2, []Entry{entry("https://example.com/b", nil, "example.com", "b", "wayback")})); err != nil {
		t.Fatal(err)
	}

	// Truncate round 2 mid-document to simulate a torn write of legacy/non-atomic data.
	bad := filepath.Join(dir, "round-000002.json")
	good, err := os.ReadFile(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, good[:len(good)/2], 0o644); err != nil {
		t.Fatal(err)
	}

	rounds, errs, err := store.LoadRecent(0)
	if err != nil {
		t.Fatal(err)
	}
	// Round 1 must remain readable; the bad round is reported with a location.
	if len(rounds) != 1 || rounds[0].ID != 1 {
		t.Fatalf("good round was lost to corruption: %+v", rounds)
	}
	if len(errs) != 1 {
		t.Fatalf("expected exactly one corruption error, got %d", len(errs))
	}
	ce, ok := errs[0].(*CorruptionError)
	if !ok {
		t.Fatalf("expected *CorruptionError, got %T: %v", errs[0], errs[0])
	}
	if ce.Path != bad || ce.Offset <= 0 {
		t.Fatalf("corruption not located precisely: %+v", ce)
	}
}

func TestStore_MissingHistoryMeansNoRounds(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "fresh"))
	if err != nil {
		t.Fatal(err)
	}
	rounds, errs, err := store.LoadRecent(0)
	if err != nil || len(errs) != 0 || len(rounds) != 0 {
		t.Fatalf("empty store should load cleanly: rounds=%v errs=%v err=%v", rounds, errs, err)
	}
	if id, _ := store.NextID(); id != 1 {
		t.Fatalf("first round id should be 1, got %d", id)
	}
}

func TestStore_RejectsFilePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("expected error when history path is a regular file")
	}
}

func TestCollector_ConcurrentDedupAndSortedSnapshot(t *testing.T) {
	c := NewCollector()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			c.Add("https://example.com/a?z=9&a=1", "wayback")
			c.Add("https://example.com/b", "otx")
		}
		close(done)
	}()
	for i := 0; i < 500; i++ {
		c.Add("https://example.com/a?a=1&z=9", "otx")
	}
	<-done

	snap := c.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 deduped identities, got %d", len(snap))
	}
	if snap[0].ID >= snap[1].ID {
		t.Fatal("snapshot is not sorted by ID")
	}
	var a *Entry
	for i := range snap {
		if strings.HasSuffix(snap[i].ID, "/a") {
			a = &snap[i]
		}
	}
	if a == nil || len(a.Providers) != 2 {
		t.Fatalf("provider union wrong: %+v", snap)
	}
	if len(a.QueryKeys) != 2 || a.QueryKeys[0] != "a" || a.QueryKeys[1] != "z" {
		t.Fatalf("query keys wrong: %v", a.QueryKeys)
	}
}

func TestImportLegacy_TextAndJSON(t *testing.T) {
	plain := strings.NewReader("https://example.com/a?x=1\nhttps://example.com/b\n\n")
	r, err := ImportLegacy(plain, []string{"example.com"}, []string{"wayback"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(r.Entries))
	}
	if len(r.Entries[0].Providers) != 1 || r.Entries[0].Providers[0] != LegacyProvider {
		t.Fatal("imported entries must carry the legacy provenance tag")
	}

	jsonLines := strings.NewReader(`{"url":"https://example.com/c?y=2"}` + "\n")
	r2, err := ImportLegacy(jsonLines, nil, nil, time.Now().UTC())
	if err != nil || len(r2.Entries) != 1 {
		t.Fatalf("json lines import failed: %v %+v", err, r2)
	}

	bad := strings.NewReader("{not json}\n")
	if _, err := ImportLegacy(bad, nil, nil, time.Now().UTC()); err == nil {
		t.Fatal("expected error on malformed JSON line")
	}
}
