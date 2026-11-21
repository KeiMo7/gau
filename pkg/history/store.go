package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CorruptionError pinpoints a history file that could not be decoded and the
// byte offset at which parsing failed, so callers can state the exact impact
// range instead of failing the whole history opaquely.
type CorruptionError struct {
	Path   string
	Offset int64
	Err    error
}

func (e *CorruptionError) Error() string {
	return fmt.Sprintf("history file %s is corrupted at byte offset %d: %v", e.Path, e.Offset, e.Err)
}

func (e *CorruptionError) Unwrap() error { return e.Err }

// Store persists rounds as one JSON document per round inside a directory:
//
//	<dir>/round-000001.json
//	<dir>/round-000002.json
//	...
//
// Files are written to a temp name in the same directory and renamed into
// place, so a kill during write never leaves a half-written round: either the
// previous complete file is still there, or the new complete file appears
// atomically.
type Store struct {
	dir string
}

// OpenStore ensures dir exists (creating it with 0o755) and returns a Store.
// It is an error if dir exists but is not a directory.
func OpenStore(dir string) (*Store, error) {
	info, err := os.Stat(dir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return nil, fmt.Errorf("history path %s exists but is not a directory", dir)
		}
	case os.IsNotExist(err):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create history directory %s: %w", dir, err)
		}
	default:
		return nil, fmt.Errorf("stat history directory %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Dir returns the backing directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) roundPath(id int) string {
	return filepath.Join(s.dir, fmt.Sprintf("round-%06d.json", id))
}

// NextID returns the round ID that Append would assign: one above the highest
// well-formed round file currently on disk.
func (s *Store) NextID() (int, error) {
	ids, _, err := s.roundIDs()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 1, nil
	}
	return ids[len(ids)-1] + 1, nil
}

// Append assigns the next round ID, writes the round atomically and returns
// the stored copy (with the ID set). The caller does not need to set Round.ID.
func (s *Store) Append(r *Round) (*Round, error) {
	id, err := s.NextID()
	if err != nil {
		return nil, err
	}
	stored := *r
	stored.ID = id

	data, err := json.MarshalIndent(&stored, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode round: %w", err)
	}
	data = append(data, '\n')

	final := s.roundPath(id)
	tmp, err := os.CreateTemp(s.dir, ".round-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp round file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("write temp round file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("sync temp round file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close temp round file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return nil, fmt.Errorf("chmod temp round file: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return nil, fmt.Errorf("rename temp round file: %w", err)
	}
	return &stored, nil
}

// LoadRound reads and decodes a single round by ID. A missing round returns
// an error satisfying os.IsNotExist.
func (s *Store) LoadRound(id int) (*Round, error) {
	f, err := os.Open(s.roundPath(id))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r Round
	cr := &countingReader{r: f}
	dec := json.NewDecoder(cr)
	if err := dec.Decode(&r); err != nil {
		offset := offsetOf(err)
		if offset == 0 {
			// Truncated files surface as io.ErrUnexpectedEOF with no offset;
			// the counting reader knows exactly how far parsing got.
			offset = cr.n
		}
		return nil, &CorruptionError{Path: s.roundPath(id), Offset: offset, Err: err}
	}
	if dec.More() {
		return nil, &CorruptionError{Path: s.roundPath(id), Offset: dec.InputOffset(), Err: fmt.Errorf("unexpected trailing data")}
	}
	if r.ID != id {
		return nil, &CorruptionError{Path: s.roundPath(id), Offset: 0, Err: fmt.Errorf("file declares round id %d but is named as round %d", r.ID, id)}
	}
	return &r, nil
}

// countingReader tracks the cumulative number of bytes consumed, so truncated
// documents can be located even when the JSON error itself carries no offset.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// LoadRecent returns the most recent rounds, oldest first. If limit > 0 at
// most limit rounds are returned; limit <= 0 returns everything.
//
// A corrupted round file does not abort the whole load: the good rounds are
// still returned and the corruption is reported via the returned errors slice
// (one *CorruptionError per bad file, each carrying the failing byte offset).
// Rounds older than a corrupted file remain readable, so the impact is scoped
// to the single bad round.
func (s *Store) LoadRecent(limit int) ([]*Round, []error, error) {
	ids, corrupt, err := s.roundIDs()
	if err != nil {
		return nil, nil, err
	}
	if limit > 0 && len(ids) > limit {
		ids = ids[len(ids)-limit:]
	}

	rounds := make([]*Round, 0, len(ids))
	var errs []error
	errs = append(errs, corrupt...)
	for _, id := range ids {
		r, err := s.LoadRound(id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rounds = append(rounds, r)
	}
	return rounds, errs, nil
}

// roundIDs lists the well-formed round-*.json filenames as ascending IDs.
// Files whose names do not match the scheme (e.g. half-written temp files,
// or a non-numeric suffix) are returned as corrupt entries rather than
// silently ignored, because they indicate interrupted or tampered history.
func (s *Store) roundIDs() ([]int, []error, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read history directory %s: %w", s.dir, err)
	}

	var ids []int
	var corrupt []error
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		// Reports and any other non-round documents are ignored silently; only
		// files that look like round documents participate in round loading.
		if !strings.HasPrefix(name, "round-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		numPart := strings.TrimSuffix(strings.TrimPrefix(name, "round-"), ".json")
		id, err := strconv.Atoi(numPart)
		if err != nil || id <= 0 {
			corrupt = append(corrupt, &CorruptionError{
				Path:   filepath.Join(s.dir, name),
				Offset: 0,
				Err:    fmt.Errorf("non-numeric round number in file name"),
			})
			continue
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids, corrupt, nil
}

// offsetOf extracts a byte offset from a JSON syntax error when available.
func offsetOf(err error) int64 {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return se.Offset
	}
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) {
		return ute.Offset
	}
	return 0
}
