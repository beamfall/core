package webhook

import (
	"sort"
	"sync"
	"time"

	"github.com/beamfall/core/internal/jsonstate"
)

// maxDeadLetters bounds the persisted dead-letter feed so the file never grows
// without limit; the oldest entries are dropped first (the operator inspects
// recent failures, and an unbounded file is its own operational hazard). The
// qBittorrent ingest ledger uses the same bounded-feed shape.
const maxDeadLetters = 500

// maxDeliveries bounds the delivery log the console shows.
const maxDeliveries = 200

// Delivery outcomes recorded in the delivery log.
const (
	OutcomeDelivered = "delivered"
	OutcomeFailed    = "failed"
)

// Delivery is one delivery-log row: which endpoint, which event kind, how it
// ended, and how long it took. It deliberately holds no URL, secret,
// signature, payload or response body, only the response status class.
type Delivery struct {
	EndpointID  string    `json:"endpoint_id"`
	EventKind   string    `json:"event_kind"`
	Outcome     string    `json:"outcome"`
	StatusClass string    `json:"status_class,omitempty"`
	Attempts    int       `json:"attempts"`
	DurationMs  int64     `json:"duration_ms"`
	At          time.Time `json:"at"`
	Test        bool      `json:"test,omitempty"`
}

// FileDeadLetter persists exhausted deliveries as plugin-private JSON under the
// data directory — the qBittorrent Ledger model (ADR-0020 status feed without a
// shared-schema migration). It touches neither the catalog nor the events
// SQLite schema (invariant #3), and is written 0600 because it shares the
// secrets-adjacent data tree even though a dead-letter row holds no secret.
type FileDeadLetter struct {
	path  string
	mu    sync.Mutex
	state deadLetterState
	// dirty marks delivery-log rows not yet written; Flush writes them once
	// per emitter tick instead of once per delivery.
	dirty bool
}

// deadLetterState is the persisted form: a version stamp plus the bounded
// feed, newest last.
type deadLetterState struct {
	Version int          `json:"version"`
	Cursor  int64        `json:"cursor,omitempty"`
	Entries []DeadLetter `json:"entries"`
	// ActivityCursor is the last activity-log id the emitter derived events
	// from; Seeded records that both cursors were moved to the tail when the
	// first endpoint was enabled, so a restart does not replay history.
	ActivityCursor int64      `json:"activity_cursor,omitempty"`
	Seeded         bool       `json:"seeded,omitempty"`
	Deliveries     []Delivery `json:"deliveries,omitempty"`
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

// ActivityCursor returns the last activity-log id derived from.
func (dl *FileDeadLetter) ActivityCursor() int64 {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	return dl.state.ActivityCursor
}

// SetActivityCursor advances the activity cursor (never backwards).
func (dl *FileDeadLetter) SetActivityCursor(cursor int64) error {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if cursor <= dl.state.ActivityCursor {
		return nil
	}
	dl.state.ActivityCursor = cursor
	return dl.persistLocked()
}

// Seeded reports whether the cursors were moved to the tail.
func (dl *FileDeadLetter) Seeded() bool {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	return dl.state.Seeded
}

// SetSeeded records whether the cursors are seeded; the emitter clears it
// when the last endpoint is removed, so re-enabling one starts at the tail.
func (dl *FileDeadLetter) SetSeeded(seeded bool) error {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if dl.state.Seeded == seeded {
		return nil
	}
	dl.state.Seeded = seeded
	return dl.persistLocked()
}

// RecordDelivery appends one delivery-log row in memory; Flush persists it.
func (dl *FileDeadLetter) RecordDelivery(entry Delivery) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	dl.state.Deliveries = append(dl.state.Deliveries, entry)
	if len(dl.state.Deliveries) > maxDeliveries {
		dl.state.Deliveries = dl.state.Deliveries[len(dl.state.Deliveries)-maxDeliveries:]
	}
	dl.dirty = true
}

// Flush writes pending delivery-log rows.
func (dl *FileDeadLetter) Flush() error {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if !dl.dirty {
		return nil
	}
	return dl.persistLocked()
}

// Deliveries returns the delivery log, newest first.
func (dl *FileDeadLetter) Deliveries() []Delivery {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	out := make([]Delivery, len(dl.state.Deliveries))
	for i, entry := range dl.state.Deliveries {
		out[len(out)-1-i] = entry
	}
	return out
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
	if err := jsonstate.Save(dl.path, dl.state); err != nil {
		return err
	}
	dl.dirty = false
	return nil
}
