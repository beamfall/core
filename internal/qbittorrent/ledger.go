package qbittorrent

import (
	"sort"
	"sync"
	"time"

	"github.com/beamfall/core/internal/jsonstate"
)

// Outcome is the closed status vocabulary the ingest status feed reports per
// torrent.
type Outcome string

const (
	// OutcomeImported: payload linked into a Library and a scan enqueued.
	OutcomeImported Outcome = "imported"
	// OutcomeUnrouted: the torrent's category has no mapping; ignored, never
	// guessed (§3). Listed so the operator can add a mapping.
	OutcomeUnrouted Outcome = "unrouted"
	// OutcomeAlreadyInCatalog: an exact-fingerprint duplicate; nothing linked,
	// the seed untouched (§5).
	OutcomeAlreadyInCatalog Outcome = "already-in-catalog"
	// OutcomeError: relocation or filesystem failure; visible for retry (§5).
	OutcomeError Outcome = "error"
)

// StatusEntry is one row of the ingest status feed.
type StatusEntry struct {
	TorrentHash string       `json:"torrent_hash"`
	ReleaseName string       `json:"release_name"`
	Category    string       `json:"category"`
	Outcome     Outcome      `json:"outcome"`
	LibraryID   string       `json:"library_id,omitempty"`
	Mode        RelocateMode `json:"mode,omitempty"`
	// MediaPointer names the existing Media a duplicate matched (§5: "pointing
	// at the existing Media").
	MediaPointer string `json:"media_pointer,omitempty"`
	// Detail is a short, secret-free explanation for errors/unrouted.
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// ledgerState is the persisted form: the processed-hash set (idempotency) plus
// the most recent status entries.
type ledgerState struct {
	Version int `json:"version"`
	// Processed maps a torrent hash to the time it was finalized. A hash here
	// is never re-imported (§5).
	Processed map[string]time.Time `json:"processed"`
	// Status is the recent status feed, newest last, bounded by maxStatus.
	Status []StatusEntry `json:"status"`
}

// maxStatus bounds the persisted status feed so the ledger file never grows
// without limit; the processed set is the durable idempotency record.
const maxStatus = 500

// Ledger persists processed torrent hashes and the status feed under the data
// directory. It is the plugin's private durable state — it touches neither the
// catalog nor the events SQLite schema (ADR-0020 status-feed mechanism without
// a shared-schema migration). Concurrent ingest jobs do not run, but the mutex
// guards against an accidental overlapping caller.
type Ledger struct {
	path  string
	mu    sync.Mutex
	state ledgerState
}

// OpenLedger loads (or initializes) the ledger at <path>. A missing file is a
// fresh ledger; a corrupt file is an error so a silent reset cannot drop the
// processed-hash record (which would re-import everything).
func OpenLedger(path string) (*Ledger, error) {
	l := &Ledger{path: path, state: ledgerState{Version: 1, Processed: map[string]time.Time{}}}
	var state ledgerState
	found, err := jsonstate.Load(path, &state)
	if err != nil {
		return nil, err
	}
	if !found {
		return l, nil
	}
	if state.Processed == nil {
		state.Processed = map[string]time.Time{}
	}
	state.Version = 1
	l.state = state
	return l, nil
}

// Processed reports whether a torrent hash has already been finalized.
func (l *Ledger) Processed(hash string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.state.Processed[hash]
	return ok
}

// Record finalizes one torrent: it marks the hash processed (idempotency) for
// every terminal outcome EXCEPT a transient error — an error stays
// unprocessed so the next run retries it (§5 retry-with-backoff). It appends a
// status entry and persists atomically.
func (l *Ledger) Record(entry StatusEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	if entry.Outcome != OutcomeError && entry.Outcome != OutcomeUnrouted && entry.TorrentHash != "" {
		l.state.Processed[entry.TorrentHash] = entry.At
	}
	l.state.Status = append(l.state.Status, entry)
	if len(l.state.Status) > maxStatus {
		l.state.Status = l.state.Status[len(l.state.Status)-maxStatus:]
	}
	return l.persistLocked()
}

// Status returns a copy of the current status feed, newest first.
func (l *Ledger) Status() []StatusEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]StatusEntry, len(l.state.Status))
	copy(out, l.state.Status)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// persistLocked writes the ledger atomically (temp + rename). The caller holds
// the mutex. The file is 0600: processed hashes are not secret, but the ledger
// shares the secrets-adjacent data tree and a conservative mode costs nothing.
func (l *Ledger) persistLocked() error {
	l.state.Version = 1
	return jsonstate.Save(l.path, l.state)
}
