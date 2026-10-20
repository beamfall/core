package webhook

import (
	"sort"
	"sync"

	"github.com/beamfall/core/internal/jsonstate"
)

// maxDeadLetters bounds the persisted dead-letter feed so the file never grows
// without limit; the oldest entries are dropped first (the operator inspects
// recent failures, and an unbounded file is its own operational hazard). The
// qBittorrent ingest ledger uses the same bounded-feed shape.
const maxDeadLetters = 500

// FileDeadLetter persists exhausted deliveries as plugin-private JSON under the
// data directory — the qBittorrent Ledger model (ADR-0020 status feed without a
// shared-schema migration). It touches neither the catalog nor the events
// SQLite schema (invariant #3), and is written 0600 because it shares the
// secrets-adjacent data tree even though a dead-letter row holds no secret.
type FileDeadLetter struct {
	path  string
	mu    sync.Mutex
	state deadLetterState
}

// deadLetterState is the persisted form: a version stamp plus the bounded
// feed, newest last.
type deadLetterState struct {
	Version int          `json:"version"`
	Cursor  int64        `json:"cursor,omitempty"`
	Entries []DeadLetter `json:"entries"`
}

// OpenDeadLetter loads (or initializes) the dead-letter file at path. A missing
// file is a fresh feed; a corrupt file is an error rather than a silent reset,
// so an operator notices a clobbered record instead of losing every prior
// failure.
func OpenDeadLetter(path string) (*FileDeadLetter, error) {
	dl := &FileDeadLetter{path: path, state: deadLetterState{Version: 1}}
	var state deadLetterState
	found, err := jsonstate.Load(path, &state)
	if err != nil {
		return nil, err
	}
	if !found {
		return dl, nil
	}
	state.Version = 1
	dl.state = state
	return dl, nil
}

// Record appends one exhausted delivery and persists atomically. It implements
// DeadLetterSink.
func (dl *FileDeadLetter) Record(entry DeadLetter) error {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	dl.state.Entries = append(dl.state.Entries, entry)
	if len(dl.state.Entries) > maxDeadLetters {
		dl.state.Entries = dl.state.Entries[len(dl.state.Entries)-maxDeadLetters:]
	}
	return dl.persistLocked()
}

func (dl *FileDeadLetter) Cursor() int64 {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	return dl.state.Cursor
}

func (dl *FileDeadLetter) SetCursor(cursor int64) error {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if cursor <= dl.state.Cursor {
		return nil
	}
	dl.state.Cursor = cursor
	return dl.persistLocked()
}

// Entries returns a copy of the current dead-letter feed, newest first — the
// operator-inspection read.
func (dl *FileDeadLetter) Entries() []DeadLetter {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	out := make([]DeadLetter, len(dl.state.Entries))
	copy(out, dl.state.Entries)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// persistLocked writes the feed atomically (temp + rename). The caller holds
// the mutex.
func (dl *FileDeadLetter) persistLocked() error {
	dl.state.Version = 1
	return jsonstate.Save(dl.path, dl.state)
}
