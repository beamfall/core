package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"
)

// OutboxAggregateBackup is the AggregateType for scheduled-backup outcome
// events (ADR-0192 §6). They exist for operator webhooks: the payload is
// content-free (an outcome, a failure class and the Backup Set's base name —
// never a path, an error string or library content), and the SSE surface does
// not deliver them to client sessions at all (internal/server/events.go).
const OutboxAggregateBackup = "backup"

// Backup outcome event kinds. A failed backup and a failed integrity check are
// distinct kinds because ADR-0192 §6 gives them distinct banners.
const (
	BackupEventCompleted       = "backup.completed"
	BackupEventFailed          = "backup.failed"
	BackupEventIntegrityFailed = "backup.integrity_failed"
)

var backupEventKinds = map[string]bool{
	BackupEventCompleted:       true,
	BackupEventFailed:          true,
	BackupEventIntegrityFailed: true,
}

// BackupEventPayload is the content-free body of a backup outcome event.
type BackupEventPayload struct {
	// FailureClass is the ADR-0192 §6 class of a failed run; empty on success.
	FailureClass string `json:"failureClass,omitempty"`
	// Set is the Backup Set directory's base name, when one was written.
	Set string `json:"set,omitempty"`
	// FromVersion and ToVersion identify a release binary upgrade transition, if any.
	FromVersion string `json:"fromVersion,omitempty"`
	ToVersion   string `json:"toVersion,omitempty"`
	// Policy records the active upgrade gate ("soft" or "hard") on upgrade events.
	Policy string `json:"policy,omitempty"`
}

// AppendBackupEvent records one scheduled-backup outcome in the event outbox,
// where the webhook emitter delivers it to subscribed endpoints. Set is
// reduced to its base name so a caller cannot leak a filesystem path.
func (s *Store) AppendBackupEvent(ctx context.Context, kind string, payload BackupEventPayload, now time.Time) error {
	if !backupEventKinds[kind] {
		return ValidationError("unknown backup event kind")
	}
	if payload.Set != "" {
		payload.Set = filepath.Base(payload.Set)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	now = normalizeTime(now)
	return s.appendOutboxRow(ctx, timeToMillis(now), kind, OutboxAggregateBackup, payload.Set, string(normalizeJSON(raw)))
}
