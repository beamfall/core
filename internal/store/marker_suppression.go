package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DeleteMarker soft-deletes a Marker whose Media the caller can see and returns
// the removed row. Marker tags remain attached for accidental-delete recovery.
func (s *Store) DeleteMarker(ctx context.Context, id MarkerID, filter LibraryFilter) (Marker, error) {
	if id == "" {
		return Marker{}, ValidationError("marker ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Marker{}, err
	}
	removed, err := s.visibleMarkerSQL(ctx, id, filter)
	if err != nil {
		return Marker{}, err
	}
	now := time.Now().UTC()
	result, err := s.catalogW.ExecContext(ctx, `UPDATE marker SET trashed_at_ms = ?, updated_at_ms = ? WHERE id = ? AND trashed_at_ms IS NULL`, now.UnixMilli(), now.UnixMilli(), id)
	if err != nil {
		return Marker{}, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return Marker{}, ErrMarkerNotFound
	}
	removed.TrashedAt = now
	removed.UpdatedAt = now
	return removed, nil
}

// RestoreMarker restores a soft-deleted Marker whose Media the caller can see.
func (s *Store) RestoreMarker(ctx context.Context, id MarkerID, filter LibraryFilter, now time.Time) (Marker, error) {
	if id == "" {
		return Marker{}, ValidationError("marker ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Marker{}, err
	}
	marker, err := scanMarkerRow(s.catalogR.QueryRowContext(ctx, markerColumns+` WHERE id = ? AND trashed_at_ms IS NOT NULL`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Marker{}, ErrMarkerNotFound
	}
	if err != nil {
		return Marker{}, err
	}
	if _, err := s.visibleMediaSQL(ctx, marker.MediaID, filter); err != nil {
		return Marker{}, ErrMarkerNotFound
	}
	restoredAt := now.UTC()
	result, err := s.catalogW.ExecContext(ctx, `UPDATE marker SET trashed_at_ms = NULL, updated_at_ms = ? WHERE id = ? AND trashed_at_ms IS NOT NULL`, restoredAt.UnixMilli(), id)
	if err != nil {
		return Marker{}, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return Marker{}, ErrMarkerNotFound
	}
	marker.TrashedAt = time.Time{}
	marker.UpdatedAt = restoredAt
	if marker.Tags, err = s.markerTagsSQL(ctx, marker.ID); err != nil {
		return Marker{}, err
	}
	return marker, nil
}

// ReplaceDetectedMarkers replaces scan/detector-owned Markers for one method
// while preserving manual/user-promoted rows.
func (s *Store) ReplaceDetectedMarkers(ctx context.Context, mediaID MediaID, method string, markers []Marker, filter LibraryFilter, now time.Time) error {
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	method = strings.TrimSpace(method)
	if method == "" {
		return ValidationError("marker method is required")
	}
	prepared := make([]Marker, 0, len(markers))
	for i, marker := range markers {
		marker.ID = deterministicDetectedMarkerID(mediaID, method, i)
		marker.MediaID = mediaID
		marker.SegmentKind = ""
		marker.Provenance = ProvenanceDetected
		marker.Method = method
		marker.Confidence = 1
		marker.CreatedByProfile = ""
		if err := validateMarker(&marker); err != nil {
			return err
		}
		prepared = append(prepared, marker)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.visibleMediaSQL(ctx, mediaID, filter); err != nil {
		return err
	}
	createdAt := now.UTC()
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lifecycleRuns int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM detection_run
		WHERE media_id = ? AND detector_method = ?
	`, mediaID, method).Scan(&lifecycleRuns); err != nil {
		return err
	}
	if lifecycleRuns > 0 {
		return ValidationError("lifecycle-managed markers require ApplyDetectionRun")
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM marker
		WHERE media_id = ? AND provenance = ? AND method = ?
	`, mediaID, string(ProvenanceDetected), method); err != nil {
		return err
	}
	for _, marker := range prepared {
		result, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO marker(id, media_id, title, start_seconds, end_seconds, segment_kind, provenance, method, confidence, flavor, created_by_profile_id, created_at_ms, updated_at_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, marker.ID, marker.MediaID, marker.Title, marker.StartSeconds,
			nullFloat(marker.EndSeconds), nullString(marker.SegmentKind),
			string(marker.Provenance), marker.Method, marker.Confidence, string(marker.Flavor),
			nil, createdAt.UnixMilli(), createdAt.UnixMilli())
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 0 {
			continue
		}
		if err := insertMarkerTags(ctx, tx, marker.ID, marker.Tags); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SuppressSegment removes the matching segment markers and records the
// reversible manual-ownership lock in one catalog transaction.
func (s *Store) SuppressSegment(ctx context.Context, mediaID MediaID, kind string, now time.Time) error {
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	if !slices.Contains(SegmentKinds, kind) {
		return ValidationError("unknown segment kind " + kind)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.visibleMediaSQL(ctx, mediaID, LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		return ErrMarkerNotFound
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM marker WHERE media_id = ? AND segment_kind = ?`, mediaID, kind); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO marker_suppression(media_id, segment_kind, created_at_ms) VALUES (?, ?, ?)
		ON CONFLICT(media_id, segment_kind) DO UPDATE SET created_at_ms = excluded.created_at_ms
	`, mediaID, kind, now.UTC().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetSegmentToAuto clears suppression. For detector-authored ads it also
// invalidates the current immutable receipt through an append-only event.
func (s *Store) ResetSegmentToAuto(ctx context.Context, mediaID MediaID, kind string) error {
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	if !slices.Contains(SegmentKinds, kind) {
		return ValidationError("unknown segment kind " + kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.visibleMediaSQL(ctx, mediaID, LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		return ErrMarkerNotFound
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM marker_suppression WHERE media_id = ? AND segment_kind = ?`,
		mediaID, kind); err != nil {
		return err
	}
	if kind == SegmentKindAd {
		if err := resetDetectionRunTx(ctx, tx, mediaID, PodcastAdDetectorMethod, s.now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SegmentSuppressed reports the internal suppression predicate.
func (s *Store) SegmentSuppressed(ctx context.Context, mediaID MediaID, kind string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var one int
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT 1 FROM marker_suppression WHERE media_id = ? AND segment_kind = ?`, mediaID, kind).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func nullFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func randomMarkerID() (MarkerID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return MarkerID("marker-" + hex.EncodeToString(raw[:])), nil
}

func deterministicDetectedMarkerID(mediaID MediaID, method string, ordinal int) MarkerID {
	sum := sha256.Sum256([]byte(string(mediaID) + "\x00" + method + "\x00" + strconv.Itoa(ordinal)))
	return MarkerID("marker-detected-" + hex.EncodeToString(sum[:16]))
}
