package store

import (
	"context"
	"database/sql"
)

// copyIdentificationStateLocked copies identification rows that an earlier
// build left in the shared catalog for this Encrypted Library's Media onto the
// Library's own plane (CORE-0706). Only artwork_candidate and its slot-lock
// provenance can be stranded there: neither table has a foreign key to media,
// so an upload for a private Media committed against the shared catalog.
// media_external_id and unmatched_queue do have one and never held such rows.
//
// The copy commits on the private plane before the returned plan deletes the
// shared rows, in one transaction with the other evacuations. A crash in
// between leaves both copies; the private one wins on read and the next unseal
// repeats both steps, so the move converges.
func (s *Store) copyIdentificationStateLocked(ctx context.Context, plane catalogPlane) (evacuationPlan, error) {
	stranded, err := strandedIdentificationMediaIDs(ctx, s.catalogR)
	if err != nil || len(stranded) == 0 {
		return evacuationPlan{}, err
	}
	var owned []MediaID
	for start := 0; start < len(stranded); start += bulkWriteChunkSize {
		chunk := stranded[start:min(start+bulkWriteChunkSize, len(stranded))]
		placeholders, args := mediaIDPlaceholders(chunk)
		found, err := scanMediaIDs(ctx, plane.reader, `SELECT id FROM media WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return evacuationPlan{}, err
		}
		owned = append(owned, found...)
	}
	if len(owned) == 0 {
		return evacuationPlan{}, nil
	}
	for start := 0; start < len(owned); start += bulkWriteChunkSize {
		chunk := owned[start:min(start+bulkWriteChunkSize, len(owned))]
		if err := copyStrandedIdentificationRows(ctx, s.catalogR, plane.writer, chunk); err != nil {
			return evacuationPlan{}, err
		}
	}
	// The delete runs later, in one transaction shared with the other
	// evacuations, after every private copy has committed.
	return evacuationPlan{deleteRows: func(ctx context.Context, tx *sql.Tx) error {
		return deleteStrandedIdentificationRows(ctx, tx, owned)
	}, moved: len(owned)}, nil
}

// strandedIdentificationMediaIDs lists Media IDs that own artwork candidates
// in the shared catalog without a shared Media row.
func strandedIdentificationMediaIDs(ctx context.Context, reader *sql.DB) ([]MediaID, error) {
	return scanMediaIDs(ctx, reader, `
		SELECT DISTINCT media_id FROM artwork_candidate
		WHERE media_id NOT IN (SELECT id FROM media)
		ORDER BY media_id`)
}

func scanMediaIDs(ctx context.Context, reader *sql.DB, query string, args ...any) ([]MediaID, error) {
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type strandedProvenance struct {
	mediaID MediaID
	field   string
	source  string
}

// artworkLockFieldGlob matches the provenance fields ArtworkLockField writes.
const artworkLockFieldGlob = "artwork[A-Z]*"

func strandedArtworkCandidates(ctx context.Context, shared *sql.DB, placeholders string, args []any) ([]ArtworkCandidate, error) {
	//nolint:gosec // G202: only generated placeholders are interpolated; IDs are bound args.
	rows, err := shared.QueryContext(ctx, `
		SELECT id, media_id, slot, source_url, plugin_id, scraper_id, applied, created_at_ms
		FROM artwork_candidate WHERE media_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []ArtworkCandidate
	for rows.Next() {
		candidate, err := scanArtworkCandidate(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func strandedArtworkLocks(ctx context.Context, shared *sql.DB, placeholders string, args []any) ([]strandedProvenance, error) {
	//nolint:gosec // G202: only generated placeholders are interpolated; IDs are bound args.
	rows, err := shared.QueryContext(ctx, `
		SELECT media_id, field, source FROM media_field_provenance
		WHERE field GLOB ? AND media_id IN (`+placeholders+`)`,
		append([]any{artworkLockFieldGlob}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var locks []strandedProvenance
	for rows.Next() {
		var lock strandedProvenance
		if err := rows.Scan(&lock.mediaID, &lock.field, &lock.source); err != nil {
			return nil, err
		}
		locks = append(locks, lock)
	}
	return locks, rows.Err()
}

func copyStrandedIdentificationRows(ctx context.Context, shared, private *sql.DB, chunk []MediaID) error {
	placeholders, args := mediaIDPlaceholders(chunk)
	candidates, err := strandedArtworkCandidates(ctx, shared, placeholders, args)
	if err != nil {
		return err
	}
	locks, err := strandedArtworkLocks(ctx, shared, placeholders, args)
	if err != nil {
		return err
	}
	tx, err := private.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range candidates {
		applied := 0
		if c.Applied {
			applied = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO artwork_candidate(id, media_id, slot, source_url, plugin_id, scraper_id, applied, created_at_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, c.MediaID, c.Slot, c.SourceURL, c.PluginID, c.ScraperID, applied, timeToMillis(c.CreatedAt)); err != nil {
			return err
		}
	}
	// A private lock already present is newer than the stranded one; keep it.
	for _, lock := range locks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_field_provenance(media_id, field, source) VALUES (?, ?, ?)
			ON CONFLICT(media_id, field) DO NOTHING`,
			lock.mediaID, lock.field, lock.source); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func deleteStrandedIdentificationRows(ctx context.Context, tx *sql.Tx, ids []MediaID) error {
	for start := 0; start < len(ids); start += bulkWriteChunkSize {
		placeholders, args := mediaIDPlaceholders(ids[start:min(start+bulkWriteChunkSize, len(ids))])
		//nolint:gosec // G202: only generated placeholders are interpolated; IDs are bound args.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM artwork_candidate WHERE media_id IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
		//nolint:gosec // G202: only generated placeholders are interpolated; IDs are bound args.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM media_field_provenance WHERE field GLOB ? AND media_id IN (`+placeholders+`)`,
			append([]any{artworkLockFieldGlob}, args...)...); err != nil {
			return err
		}
	}
	return nil
}
