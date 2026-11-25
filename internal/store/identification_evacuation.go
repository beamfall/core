package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// unmatchedReviewEvacuations are the catalog.db rows an earlier build wrote
// for an Encrypted Library's Unmatched Records: the record (source handle,
// candidate title) and every suppression naming it. Records are listed first;
// deletes run in reverse.
var unmatchedReviewEvacuations = []encryptedLibraryEvacuation{
	{
		table: "unmatched_record",
		columns: []string{"id", "origin", "library_id", "media_id", "import_run_id", "import_importer_id",
			"source_handle", "candidate_id", "candidate_title", "confidence", "failure_reason",
			"queued_at_ms", "resolved_at_ms", "resolved_media_id"},
		where: "library_id = ?",
	},
	{
		table:   "unmatched_suppression",
		columns: []string{"scope", "scope_key", "record_id", "source_handle", "actor_profile_id", "created_at_ms"},
		where:   "record_id IN (SELECT id FROM unmatched_record WHERE library_id = ?)",
	},
}

// copyUnmatchedReviewStateLocked copies this Encrypted Library's Unmatched
// Records and their suppressions from the shared catalog onto its plane
// (CORE-0706), like copyIdentificationStateLocked. If any record names a
// Media the plane does not hold, every row stays in the shared catalog, as
// private as that Media already is, rather than losing the reference.
// File-scope suppressions that name no record carry no Library and stay.
func (s *Store) copyUnmatchedReviewStateLocked(ctx context.Context, plane catalogPlane) (evacuationPlan, error) {
	pending := make([]evacuationTableRows, 0, len(unmatchedReviewEvacuations))
	for _, spec := range unmatchedReviewEvacuations {
		rows, err := readEvacuationRows(ctx, s.catalogR, spec, plane.libraryID)
		if err != nil {
			return evacuationPlan{}, fmt.Errorf("read shared %s rows: %w", spec.table, err)
		}
		if len(rows) > 0 {
			pending = append(pending, evacuationTableRows{spec: spec, rows: rows})
		}
	}
	if len(pending) == 0 || pending[0].spec.table != "unmatched_record" {
		return evacuationPlan{}, nil
	}
	records := pending[0]
	for _, column := range []string{"media_id", "resolved_media_id"} {
		index := columnIndex(records.spec.columns, column)
		for _, row := range records.rows {
			if row[index] == nil {
				continue
			}
			var one int
			err := plane.reader.QueryRowContext(ctx, `SELECT 1 FROM media WHERE id = ?`, row[index]).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				if s.logger != nil {
					s.logger.WarnContext(ctx, "shared unmatched records kept in catalog: they name media outside the protected plane", "libraryID", plane.libraryID)
				}
				return evacuationPlan{}, nil
			}
			if err != nil {
				return evacuationPlan{}, fmt.Errorf("check unmatched record media in protected plane: %w", err)
			}
		}
	}
	if err := copyEvacuationRows(ctx, plane.writer, pending); err != nil {
		return evacuationPlan{}, err
	}
	libraryID := plane.libraryID
	return evacuationPlan{deleteRows: func(ctx context.Context, tx *sql.Tx) error {
		for i := len(pending) - 1; i >= 0; i-- {
			spec := pending[i].spec
			//nolint:gosec // G201: table and where are closed literals from unmatchedReviewEvacuations.
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s`, spec.table, spec.where), libraryID); err != nil {
				return fmt.Errorf("delete shared %s rows: %w", spec.table, err)
			}
		}
		return nil
	}, moved: len(records.rows)}, nil
}
