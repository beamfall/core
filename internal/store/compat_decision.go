package store

import (
	"context"
	"time"
)

// Durable compatibility-decision invalidation. The decision registry
// itself is in-memory and is rebuilt empty at every boot. Losing a still-live
// acceptance just makes the client handshake again, but losing a refusal or a
// generation invalidation is fail-open: after restart the subject reads back as
// "never handshook", which the Stream Token verify seam treats as benign, so an
// already-issued unexpired pre-binding token becomes usable again.
//
// These rows are the invalidation side made durable. They are tombstones, not
// decisions: they record that a subject's decision was refused or made stale by
// a generation advance, plus when the tombstone stops applying.

// CompatDecisionInvalidation is one subject's durable decision tombstone.
type CompatDecisionInvalidation struct {
	// Subject is the opaque server-minted credential identity. Core compares it
	// and never parses it for meaning.
	Subject string
	// Decision and ReasonCode are the subject's last closed-vocabulary result.
	// Refusals are non-servable; generation invalidations retain the previously
	// accepted, servable result so rehydration can report ErrStaleGeneration.
	Decision   string
	ReasonCode string
	// RecordedAt is when invalidation happened; ExpiresAt is when the tombstone
	// stops applying and becomes eligible for the prune.
	RecordedAt time.Time
	ExpiresAt  time.Time
}

// PutCompatDecisionInvalidation records (or refreshes) a subject's tombstone and
// prunes every tombstone that has expired at now.
func (s *Store) PutCompatDecisionInvalidation(ctx context.Context, entry CompatDecisionInvalidation, now time.Time) error {
	if entry.Subject == "" {
		return ValidationError("compatibility subject is required")
	}
	if !entry.ExpiresAt.After(now) {
		return ValidationError("compatibility invalidation expiry must be in the future")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if err := s.pruneCompatDecisionInvalidationsLocked(ctx, now); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO compat_decision_invalidation(subject, decision, reason_code, recorded_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(subject) DO UPDATE SET
		  decision = excluded.decision,
		  reason_code = excluded.reason_code,
		  recorded_at_ms = excluded.recorded_at_ms,
		  expires_at_ms = excluded.expires_at_ms
	`, entry.Subject, entry.Decision, entry.ReasonCode,
		timeToMillis(entry.RecordedAt), timeToMillis(entry.ExpiresAt))
	return err
}

// StageCompatDecisionInvalidation durably stages one pending tombstone without
// touching the rest of the staged set. It is the refusal fallback commit point:
// when the direct tombstone write fails, this staged row is what makes the
// refusal durable before its outcome is reported, and rehydration promotes it
// exactly like a staged generation batch.
func (s *Store) StageCompatDecisionInvalidation(ctx context.Context, entry CompatDecisionInvalidation, now time.Time) error {
	if entry.Subject == "" {
		return ValidationError("compatibility subject is required")
	}
	if !entry.ExpiresAt.After(now) {
		return ValidationError("compatibility invalidation expiry must be in the future")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO compat_decision_invalidation_staged(subject, decision, reason_code, recorded_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(subject) DO UPDATE SET
		  decision = excluded.decision,
		  reason_code = excluded.reason_code,
		  recorded_at_ms = excluded.recorded_at_ms,
		  expires_at_ms = excluded.expires_at_ms
	`, entry.Subject, entry.Decision, entry.ReasonCode,
		timeToMillis(entry.RecordedAt), timeToMillis(entry.ExpiresAt))
	return err
}

// StageCompatDecisionInvalidations durably stores an invalidation batch in one
// transaction, replacing any previously staged batch. Staging is the commit
// point of a generation advance: once this returns, the whole batch survives
// process death, because rehydration promotes staged rows before it answers. A
// superseding advance recomputes the batch from the same records, so replacing
// the staged set wholesale is correct — but any row already staged (a pending
// refusal from StageCompatDecisionInvalidation) is promoted first, inside the
// same transaction, so replacement can never un-commit durable state from the
// other staging path.
func (s *Store) StageCompatDecisionInvalidations(ctx context.Context, entries []CompatDecisionInvalidation, now time.Time) error {
	for _, entry := range entries {
		if entry.Subject == "" {
			return ValidationError("compatibility subject is required")
		}
		if !entry.ExpiresAt.After(now) {
			return ValidationError("compatibility invalidation expiry must be in the future")
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO compat_decision_invalidation(subject, decision, reason_code, recorded_at_ms, expires_at_ms)
		SELECT subject, decision, reason_code, recorded_at_ms, expires_at_ms
		FROM compat_decision_invalidation_staged WHERE TRUE
		ON CONFLICT(subject) DO UPDATE SET
		  decision = excluded.decision,
		  reason_code = excluded.reason_code,
		  recorded_at_ms = excluded.recorded_at_ms,
		  expires_at_ms = excluded.expires_at_ms
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM compat_decision_invalidation_staged"); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO compat_decision_invalidation_staged(subject, decision, reason_code, recorded_at_ms, expires_at_ms)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(subject) DO UPDATE SET
			  decision = excluded.decision,
			  reason_code = excluded.reason_code,
			  recorded_at_ms = excluded.recorded_at_ms,
			  expires_at_ms = excluded.expires_at_ms
		`, entry.Subject, entry.Decision, entry.ReasonCode,
			timeToMillis(entry.RecordedAt), timeToMillis(entry.ExpiresAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CommitStagedCompatDecisionInvalidations promotes the staged batch into the
// tombstone table and clears the staging table, atomically. Process death
// before or during this call loses nothing: the staged rows survive and the
// next rehydration promotes them.
func (s *Store) CommitStagedCompatDecisionInvalidations(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.commitStagedCompatDecisionInvalidationsLocked(ctx)
}

func (s *Store) commitStagedCompatDecisionInvalidationsLocked(ctx context.Context) error {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO compat_decision_invalidation(subject, decision, reason_code, recorded_at_ms, expires_at_ms)
		SELECT subject, decision, reason_code, recorded_at_ms, expires_at_ms
		FROM compat_decision_invalidation_staged WHERE TRUE
		ON CONFLICT(subject) DO UPDATE SET
		  decision = excluded.decision,
		  reason_code = excluded.reason_code,
		  recorded_at_ms = excluded.recorded_at_ms,
		  expires_at_ms = excluded.expires_at_ms
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM compat_decision_invalidation_staged"); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteCompatDecisionInvalidation clears a subject's tombstone. It runs when a
// fresh handshake produces a servable decision for that subject — the only event
// that makes the subject valid again. The staged row goes with it, in the same
// transaction: a still-staged pending refusal left behind would be replayed at
// the next rehydration and durably refuse a subject the server just accepted.
func (s *Store) DeleteCompatDecisionInvalidation(ctx context.Context, subject string) error {
	if subject == "" {
		return ValidationError("compatibility subject is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM compat_decision_invalidation WHERE subject = ?", subject); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM compat_decision_invalidation_staged WHERE subject = ?", subject); err != nil {
		return err
	}
	return tx.Commit()
}

// CompatDecisionInvalidations prunes expired tombstones and returns the ones
// still in force at now, sorted by subject. This is the boot-time rehydration
// read: the registry cannot answer a single resolve until it has run. It
// promotes any staged batch first, so a batch whose promotion was interrupted
// by process death is completed before resolution serves traffic.
func (s *Store) CompatDecisionInvalidations(ctx context.Context, now time.Time) ([]CompatDecisionInvalidation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if err := s.commitStagedCompatDecisionInvalidationsLocked(ctx); err != nil {
		return nil, err
	}
	if err := s.pruneCompatDecisionInvalidationsLocked(ctx, now); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT subject, decision, reason_code, recorded_at_ms, expires_at_ms
		FROM compat_decision_invalidation
		ORDER BY subject`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []CompatDecisionInvalidation
	for rows.Next() {
		var entry CompatDecisionInvalidation
		var recordedMs, expiresMs int64
		if err := rows.Scan(&entry.Subject, &entry.Decision, &entry.ReasonCode, &recordedMs, &expiresMs); err != nil {
			return nil, err
		}
		entry.RecordedAt = millisToTime(recordedMs)
		entry.ExpiresAt = millisToTime(expiresMs)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// pruneCompatDecisionInvalidationsLocked deletes every tombstone whose expiry
// has passed. Bounding the set is a correctness requirement, not housekeeping:
// without it a server that has invalidated many short-lived credentials would
// carry their tombstones forever.
func (s *Store) pruneCompatDecisionInvalidationsLocked(ctx context.Context, now time.Time) error {
	_, err := s.catalogW.ExecContext(ctx,
		"DELETE FROM compat_decision_invalidation WHERE expires_at_ms <= ?", timeToMillis(now))
	return err
}
