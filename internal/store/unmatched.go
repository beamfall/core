package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
)

// UnmatchedItem is one Media item the identification pipeline (PIPE-1, ADR-0003
// §4) could not auto-apply: its top candidate scored below the plugin's per-plugin
// auto-apply threshold, so rather than guess, the pipeline parks it
// here for an operator to resolve with the "Fix match" mutation. The shape is
// generic (invariant #1) — the plugin id carries the species, never a domain word.
type UnmatchedItem struct {
	MediaID        MediaID
	Title          string
	PluginID       string
	ScraperID      string
	Confidence     float64
	Threshold      float64
	CandidateID    string
	CandidateTitle string
	QueuedAt       time.Time
}

// QueueUnmatched records (or replaces) one Media item's Unmatched-queue row. It
// is the below-threshold branch of the scan-time pipeline: a confident match
// auto-applies and never reaches here. The row is keyed by Media id (the queue is
// a current-state set, not a log), so a rescan that re-scores the same item
// UPSERTs its fresh confidence rather than duplicating it. candidateID/Title may
// be empty when the search found no candidate at all. Server-internal write —
// visibility gating belongs to the read side.
func (s *Store) QueueUnmatched(ctx context.Context, item UnmatchedItem) error {
	if item.MediaID == "" {
		return errors.New("media ID is required")
	}
	if item.PluginID == "" || item.ScraperID == "" {
		return errors.New("plugin ID and scraper ID are required")
	}
	queuedAt := item.QueuedAt
	if queuedAt.IsZero() {
		queuedAt = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	// The row lives on the Media's plane; a sealed Encrypted Library's Media
	// is refused as not found rather than queued in the shared catalog.
	plane, err := s.catalogPlaneForMediaLocked(ctx, item.MediaID)
	if err != nil {
		return err
	}
	_, err = plane.writer.ExecContext(ctx, `
		INSERT INTO unmatched_queue(
			media_id, plugin_id, scraper_id, confidence, threshold,
			candidate_id, candidate_title, queued_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(media_id) DO UPDATE SET
			plugin_id       = excluded.plugin_id,
			scraper_id      = excluded.scraper_id,
			confidence      = excluded.confidence,
			threshold       = excluded.threshold,
			candidate_id    = excluded.candidate_id,
			candidate_title = excluded.candidate_title,
			queued_at_ms    = excluded.queued_at_ms
	`, item.MediaID, item.PluginID, item.ScraperID, item.Confidence, item.Threshold,
		item.CandidateID, item.CandidateTitle, timeToMillis(queuedAt.UTC()))
	return err
}

// ClearUnmatched removes one Media item's Unmatched-queue row. It is called when
// the item is resolved — a later rescan that auto-applies, or an operator's "Fix
// match" mutation. A no-op when the item was never queued. Reports whether a row
// was removed, mirroring SetMatchPinned's found-convention.
func (s *Store) ClearUnmatched(ctx context.Context, mediaID MediaID) (bool, error) {
	if mediaID == "" {
		return false, errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if errors.Is(err, errPlaneMediaNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result, err := plane.writer.ExecContext(ctx,
		"DELETE FROM unmatched_queue WHERE media_id = ?", mediaID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// IsUnmatched reports whether a Media item currently sits in the Unmatched queue
// (the test seam PIPE-1's "below threshold queues, does NOT apply" assertion
// reads). Server-internal, like the other file-plane reads.
func (s *Store) IsUnmatched(ctx context.Context, mediaID MediaID) (bool, error) {
	if mediaID == "" {
		return false, errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if errors.Is(err, errPlaneMediaNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var one int
	err = plane.reader.QueryRowContext(ctx,
		"SELECT 1 FROM unmatched_queue WHERE media_id = ?", mediaID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// UnmatchedItems returns one bounded page of queue rows whose live Media the
// session may see, newest first. Visibility and certification are SQL
// predicates before LIMIT/OFFSET so hidden rows cannot shift visible pages.
// Graph callers supply limit and offset; omission preserves the internal
// all-rows compatibility path.
func (s *Store) UnmatchedItems(ctx context.Context, filter LibraryFilter, page ...int) ([]UnmatchedItem, error) {
	limit, offset := boundedReadPage(page)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	visibleLibraries, err := s.visibleLibraryIDsLocked(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(visibleLibraries) == 0 {
		return []UnmatchedItem{}, nil
	}
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	var private []catalogPlane
	for _, plane := range s.registeredEncryptedPlanesLocked() {
		if slices.Contains(visibleLibraries, plane.libraryID) {
			private = append(private, plane)
		}
	}
	if len(private) == 0 {
		return unmatchedItemsFrom(ctx, s.catalogR, visibleLibraries, certClause, certArgs, limit, offset)
	}
	// Each visible open Encrypted Library keeps its queue on its own plane.
	// Read the first offset+limit rows of every plane, then merge and page in
	// the same order the single-plane query uses.
	window := -1
	if limit >= 0 {
		window = limit + offset
	}
	out, err := unmatchedItemsFrom(ctx, s.catalogR, visibleLibraries, certClause, certArgs, window, 0)
	if err != nil {
		return nil, err
	}
	for _, plane := range private {
		library, err := s.libraryByIDLocked(ctx, plane.libraryID)
		if err != nil {
			return nil, err
		}
		pluginID := ""
		if library.PluginID != nil {
			pluginID = *library.PluginID
		}
		planeCert, planeArgs := planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
		items, err := unmatchedItemsFrom(ctx, plane.reader, []LibraryID{plane.libraryID}, planeCert, planeArgs, window, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	slices.SortFunc(out, func(a, b UnmatchedItem) int {
		if c := b.QueuedAt.Compare(a.QueuedAt); c != 0 {
			return c
		}
		return strings.Compare(string(a.MediaID), string(b.MediaID))
	})
	out = out[min(offset, len(out)):]
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// unmatchedItemsFrom reads one plane's visible queue rows, newest first.
func unmatchedItemsFrom(ctx context.Context, reader *sql.DB, visibleLibraries []LibraryID, certClause string, certArgs []any, limit, offset int) ([]UnmatchedItem, error) {
	libraries, libraryArgs := libraryIDPlaceholders(visibleLibraries)
	args := make([]any, 0, len(libraryArgs)+len(certArgs)+2)
	args = append(args, libraryArgs...)
	args = append(args, certArgs...)
	args = append(args, limit, offset)
	//nolint:gosec // G202: only generated placeholders and the fixed certification fragment are interpolated; values are bound args.
	rows, err := reader.QueryContext(ctx, `
		SELECT q.media_id, m.title, q.plugin_id, q.scraper_id, q.confidence, q.threshold,
		       q.candidate_id, q.candidate_title, q.queued_at_ms
		FROM unmatched_queue q
		JOIN media m ON m.id = q.media_id
		WHERE m.trashed_at_ms IS NULL
		  AND m.library_id IN (`+libraries+`)`+certClause+`
		ORDER BY q.queued_at_ms DESC, q.media_id
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UnmatchedItem{}
	for rows.Next() {
		var (
			item     UnmatchedItem
			queuedMS int64
		)
		if err := rows.Scan(&item.MediaID, &item.Title, &item.PluginID, &item.ScraperID,
			&item.Confidence, &item.Threshold, &item.CandidateID, &item.CandidateTitle,
			&queuedMS); err != nil {
			return nil, err
		}
		item.QueuedAt = millisToTime(queuedMS)
		out = append(out, item)
	}
	return out, rows.Err()
}
