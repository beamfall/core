package store

import (
	"context"
	"fmt"
	"time"
)

// Ops observability reads. These back /health readiness and the
// /metrics gauges. All three are aggregate counts over state the store already
// owns — no new bookkeeping. Like /metrics itself they are reveal-unfiltered
// aggregates (the surface is classed reveal.Aggregate): a bare total, never a
// title or id, so it leaks no per-Library existence.

// Ready reports whether the store is serviceable: both backing databases are
// reachable and their migrations have been applied. A nil error means ready; a
// non-nil error names which database failed and why, for the /health readiness
// probe (Docker HEALTHCHECK, uptime-kuma). Liveness — the process being up — is
// answered by the handler responding at all, so it needs no store call.
//
// Migrations-applied is checked by confirming each database's schema_migrations
// table exists and holds at least one row. The migrate() pass that runs at Open
// populates that table per migration, so a populated table is the durable
// evidence that the boot migration step ran; a reachable handle over an empty or
// missing table means the store is up but not migrated.
func (s *Store) Ready(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	for _, db := range []struct {
		name   string
		handle queryRower
	}{
		{"catalog", s.catalogR},
		{"events", s.eventsR},
	} {
		var applied int
		if err := db.handle.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
			return fmt.Errorf("%s database not ready: %w", db.name, err)
		}
		if applied == 0 {
			return fmt.Errorf("%s database has no migrations applied", db.name)
		}
	}
	return nil
}

// ActiveSessionCount returns the number of distinct playback sessions currently
// holding a live media Stream Token. It reuses the same ledger the
// live-sessions dashboard reads (the outstanding-Stream-Token model, ADR-0010 /
// ADR-0036): a live playback is exactly an unexpired media-direct/media-hls
// token, so a distinct-session count over those rows is the active-session
// gauge. Expired rows are swept first, matching ActiveSessions, so the count is
// what is actually live.
func (s *Store) ActiveSessionCount(ctx context.Context, now time.Time) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	nowMS := timeToMillis(now)
	_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM stream_token_ledger WHERE expires_at_ms <= ?`, nowMS)
	var count int
	if err := s.eventsR.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT session_id)
		FROM stream_token_ledger
		WHERE expires_at_ms > ? AND target IN ('media-direct', 'media-hls')
	`, nowMS).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// LibraryMediaTotal returns the total number of non-trashed Media across every
// Library (ADR-0013 §6 "library sizes"). It is a reveal-unfiltered aggregate —
// the bare catalog size for the operator's capacity view, the same count the
// per-Library index status sums — not a per-Library or per-caller figure, so it
// is safe on the reveal.Aggregate /metrics surface. Trashed Media are excluded
// to match every other catalog size read.
func (s *Store) LibraryMediaTotal(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	var total int
	if err := s.catalogR.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media WHERE trashed_at_ms IS NULL`).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}
