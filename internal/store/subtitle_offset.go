package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Subtitle offset nudges. A viewer's in-player nudge persists
// silently, per Profile, keyed on the subtitle-track fingerprint — the content
// hash of the track bytes (SubtitleTrackFingerprint), never the media item and
// never a path. That keying survives renames and remuxes and invalidates
// structurally when a permanent timing fix rewrites the bytes.

// MaxSubtitleOffsetMillis bounds a stored nudge. A legitimate sync correction is
// seconds, not minutes; the cap keeps a malformed client from persisting an
// absurd shift that would push every cue off the timeline. It is exported so the
// HTTP boundary refuses an out-of-range value against the SAME bound the store
// enforces, rather than duplicating the number and drifting from it.
const MaxSubtitleOffsetMillis = 10 * 60 * 1000 // ±10 minutes

// PutSubtitleOffset upserts the Profile's nudge for one subtitle track. A zero
// offset DELETES the row: "no shift" is the absence of a nudge, so a viewer who
// nudges back to centre leaves no residue to garbage-collect later.
func (s *Store) PutSubtitleOffset(ctx context.Context, profileID ProfileID, fingerprint string, offsetMS int, now time.Time) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	if fingerprint == "" {
		return ValidationError("subtitle track fingerprint is required")
	}
	if offsetMS > MaxSubtitleOffsetMillis || offsetMS < -MaxSubtitleOffsetMillis {
		return ValidationError("subtitle offset is out of range")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if exists, err := s.profileExistsSQL(ctx, profileID); err != nil {
		return err
	} else if !exists {
		return ValidationError("unknown profile")
	}
	if offsetMS == 0 {
		_, err := s.catalogW.ExecContext(ctx,
			"DELETE FROM subtitle_offset_nudge WHERE profile_id = ? AND track_fingerprint = ?",
			profileID, fingerprint)
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO subtitle_offset_nudge(profile_id, track_fingerprint, offset_ms, updated_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(profile_id, track_fingerprint) DO UPDATE SET
		  offset_ms = excluded.offset_ms,
		  updated_at_ms = excluded.updated_at_ms
	`, profileID, fingerprint, offsetMS, now.UTC().UnixMilli())
	return err
}

// SubtitleOffset returns the Profile's stored nudge for a subtitle-track
// fingerprint. A track with no nudge returns 0 — the caller applies no shift,
// which is the same outcome as an explicitly centred track.
func (s *Store) SubtitleOffset(ctx context.Context, profileID ProfileID, fingerprint string) (int, error) {
	if profileID == "" || fingerprint == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	var offsetMS int
	err := s.catalogR.QueryRowContext(ctx,
		"SELECT offset_ms FROM subtitle_offset_nudge WHERE profile_id = ? AND track_fingerprint = ?",
		profileID, fingerprint).Scan(&offsetMS)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, err
	}
	return offsetMS, nil
}

// SubtitleOffsetRetention is the grace period before an UNREACHABLE nudge is
// eligible for the prune sweep. A reachable track is retained regardless of
// age; the cutoff only keeps a transiently unavailable track from being reaped
// immediately.
const SubtitleOffsetRetention = 365 * 24 * time.Hour

// LiveSubtitleSidecar identifies one currently catalogued caption file whose
// bytes can make a persisted offset fingerprint reachable.
type LiveSubtitleSidecar struct {
	MediaPath string
	RelPath   string
	Format    string
}

// LiveSubtitleSidecars returns the caption sidecars attached to media files
// that are not marked missing. The prune job re-confines each relative path and
// hashes the guarded bytes; the store deliberately returns paths rather than
// pretending the catalog's media-file fingerprint is the track fingerprint.
func (s *Store) LiveSubtitleSidecars(ctx context.Context) ([]LiveSubtitleSidecar, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT media_file.path, media_file_sidecar_caption.rel_path, media_file_sidecar_caption.format
		FROM media_file_sidecar_caption
		JOIN media_file
		  ON media_file.content_fingerprint = media_file_sidecar_caption.content_fingerprint
		WHERE media_file.missing_at_ms IS NULL
		  AND media_file_sidecar_caption.kind != ?
		ORDER BY media_file.path, media_file_sidecar_caption.rel_path
	`, SidecarKindInteractive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sidecars []LiveSubtitleSidecar
	for rows.Next() {
		var sidecar LiveSubtitleSidecar
		if err := rows.Scan(&sidecar.MediaPath, &sidecar.RelPath, &sidecar.Format); err != nil {
			return nil, err
		}
		sidecars = append(sidecars, sidecar)
	}
	return sidecars, rows.Err()
}

// PruneSubtitleOffsets deletes old nudge rows whose track fingerprint is absent
// from reachableFingerprints and reports how many were removed — the GC for
// orphaned rows. Reachability is derived from the live sidecar
// inventory by the scheduled job immediately before this call; age alone is
// never sufficient to delete a row.
func (s *Store) PruneSubtitleOffsets(ctx context.Context, before time.Time, reachableFingerprints []string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	reachable := make(map[string]struct{}, len(reachableFingerprints))
	for _, fingerprint := range reachableFingerprints {
		reachable[fingerprint] = struct{}{}
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT profile_id, track_fingerprint
		FROM subtitle_offset_nudge
		WHERE updated_at_ms < ?
	`, before.UTC().UnixMilli())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type staleNudge struct {
		profileID   ProfileID
		fingerprint string
	}
	var stale []staleNudge
	for rows.Next() {
		var nudge staleNudge
		if err := rows.Scan(&nudge.profileID, &nudge.fingerprint); err != nil {
			return 0, err
		}
		if _, ok := reachable[nudge.fingerprint]; !ok {
			stale = append(stale, nudge)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var removed int64
	for _, nudge := range stale {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM subtitle_offset_nudge
			WHERE profile_id = ? AND track_fingerprint = ? AND updated_at_ms < ?
		`, nudge.profileID, nudge.fingerprint, before.UTC().UnixMilli())
		if err != nil {
			return removed, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return removed, err
		}
		removed += count
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return removed, nil
}
