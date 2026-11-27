package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// VersionCopyPair holds the file identities and stat data observed before
// the scanner verifies a possible copy.
type VersionCopyPair struct {
	Sample MediaFile
	Legacy MediaFile
}

// MissingLegacyVersionFingerprints lets a scan verify returning Versions
// against their reserved identities without looking up missing rows per file.
func (s *Store) MissingLegacyVersionFingerprints(ctx context.Context, libraryID LibraryID) (map[string][]StoredFingerprint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT f.path, f.content_fingerprint, f.size_bytes
		FROM media m JOIN media_file f ON f.media_id = m.id
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL
		  AND f.missing_at_ms IS NOT NULL AND f.version_label <> ''
		  AND f.content_fingerprint GLOB 'sha256:*'
		  AND NOT EXISTS (
		    SELECT 1 FROM media_file live WHERE live.media_id = f.media_id
		      AND live.version_label = f.version_label AND live.part_ordinal = f.part_ordinal
		      AND live.missing_at_ms IS NULL AND live.path <> f.path)
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fingerprints := make(map[string][]StoredFingerprint)
	for rows.Next() {
		var path string
		var fp StoredFingerprint
		if err := rows.Scan(&path, &fp.ContentFingerprint, &fp.SizeBytes); err != nil {
			return nil, err
		}
		fingerprints[path] = append(fingerprints[path], fp)
	}
	return fingerprints, rows.Err()
}

// VersionCopyCandidates finds mixed-fingerprint pairs in one Library read.
// OSHash and size select files to verify, and never establish byte identity.
func (s *Store) VersionCopyCandidates(ctx context.Context, libraryID LibraryID) ([]VersionCopyPair, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if s.trashDisabled {
		return nil, nil
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		WITH eligible AS (
			SELECT m.id FROM media m JOIN library l ON l.id = m.library_id
			WHERE l.id = ? AND l.encrypted = 0 AND l.trashed_at_ms IS NULL AND m.trashed_at_ms IS NULL AND m.extra = 0
			  AND NOT EXISTS (SELECT 1 FROM media_relation r WHERE r.from_media_id = m.id OR r.to_media_id = m.id)
			  AND NOT EXISTS (SELECT 1 FROM media_file p WHERE p.media_id = m.id AND p.part_ordinal <> 0)
			  AND NOT EXISTS (SELECT 1 FROM op_batch_item i JOIN op_batch b ON b.id = i.batch_id
			                  WHERE i.media_id = m.id AND b.kind = ? AND b.undone_at_ms <> 0)
		)
		SELECT sf.media_id, sf.path, sf.content_fingerprint, sf.mtime_ms,
		       lf.media_id, lf.path, lf.content_fingerprint, lf.mtime_ms,
		       sf.oshash, sf.size_bytes
		FROM eligible sm
		JOIN media_file sf ON sf.media_id = sm.id
		JOIN media_file lf ON lf.oshash = sf.oshash AND lf.size_bytes = sf.size_bytes
		JOIN eligible lm ON lm.id = lf.media_id AND lm.id <> sm.id
		WHERE sf.missing_at_ms IS NULL AND lf.missing_at_ms IS NULL
		  AND sf.oshash <> '' AND lf.oshash <> ''
		  AND sf.content_fingerprint GLOB 'sample1:*' AND lf.content_fingerprint GLOB 'sha256:*'
		ORDER BY sf.content_fingerprint, lf.content_fingerprint
	`, libraryID, opKindMergeVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pairs []VersionCopyPair
	for rows.Next() {
		var pair VersionCopyPair
		var sampleTime, legacyTime int64
		if err := rows.Scan(&pair.Sample.MediaID, &pair.Sample.Path, &pair.Sample.ContentFingerprint, &sampleTime,
			&pair.Legacy.MediaID, &pair.Legacy.Path, &pair.Legacy.ContentFingerprint, &legacyTime,
			&pair.Sample.OSHash, &pair.Sample.SizeBytes); err != nil {
			return nil, err
		}
		pair.Sample.ModTime = mediaFileMillisToTime(sampleTime)
		pair.Legacy.ModTime = mediaFileMillisToTime(legacyTime)
		pair.Legacy.OSHash = pair.Sample.OSHash
		pair.Legacy.SizeBytes = pair.Sample.SizeBytes
		pairs = append(pairs, pair)
	}
	return pairs, rows.Err()
}

// verifiedCopyStillCurrentTx checks that the two verified rows are still
// live and have their verified identity and ownership. Recheck Parts on both
// Media in case another writer attached one during verification.
func verifiedCopyStillCurrentTx(ctx context.Context, tx *sql.Tx, pair VersionCopyPair) (bool, error) {
	var eligible bool
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) = 2 FROM media_file f
		WHERE (
		  (f.content_fingerprint = ? AND f.content_fingerprint GLOB 'sample1:*'
		   AND f.media_id = ? AND f.path = ? AND f.mtime_ms = ?)
		  OR (f.content_fingerprint = ? AND f.content_fingerprint GLOB 'sha256:*'
		      AND f.media_id = ? AND f.path = ? AND f.mtime_ms = ?)
		) AND f.missing_at_ms IS NULL AND f.oshash <> '' AND f.oshash = ? AND f.size_bytes = ?
		  AND NOT EXISTS (SELECT 1 FROM media_file p WHERE p.media_id = f.media_id AND p.part_ordinal <> 0)
	`, pair.Sample.ContentFingerprint, pair.Sample.MediaID, pair.Sample.Path, mediaFileTimeToMillis(pair.Sample.ModTime),
		pair.Legacy.ContentFingerprint, pair.Legacy.MediaID, pair.Legacy.Path, mediaFileTimeToMillis(pair.Legacy.ModTime),
		pair.Sample.OSHash, pair.Sample.SizeBytes).Scan(&eligible)
	return eligible, err
}

// placeMergedVersionsTx moves the losing Media's Versions and reservations. Existing
// labels are kept when available and after the survivor's first Version.
// Otherwise an ordinal distinguishes the additional Version. Missing slots
// on the survivor remain reserved.
func placeMergedVersionsTx(ctx context.Context, tx *sql.Tx, survivor, loser MediaID, first string) (map[string]string, []string, error) {
	labels, err := partZeroLabelsTx(ctx, tx, survivor)
	if err != nil {
		return nil, nil, err
	}
	firstLabel := labels[first]
	used := make(map[string]bool, len(labels))
	for _, label := range labels {
		used[label] = true
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT content_fingerprint, version_label, missing_at_ms IS NOT NULL FROM media_file
		WHERE media_id = ? ORDER BY version_label, missing_at_ms IS NOT NULL, content_fingerprint
	`, loser)
	if err != nil {
		return nil, nil, err
	}
	type version struct {
		fingerprint, label string
		missing            bool
	}
	var files []version
	for rows.Next() {
		var file version
		if err := rows.Scan(&file.fingerprint, &file.label, &file.missing); err != nil {
			return nil, nil, errors.Join(err, rows.Close())
		}
		files = append(files, file)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}

	moved := make(map[string]string, len(files))
	placements := make(map[string]string)
	var missing []string
	for _, file := range files {
		label, placed := placements[file.label]
		if !placed {
			label = file.label
			if label <= firstLabel || used[label] {
				label, err = mergedVersionLabel(firstLabel, used)
				if err != nil {
					return nil, nil, err
				}
			}
			placements[file.label] = label
			used[label] = true
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE media_file SET media_id = ?, version_label = ? WHERE content_fingerprint = ? AND media_id = ?
		`, survivor, label, file.fingerprint, loser); err != nil {
			return nil, nil, err
		}
		moved[file.fingerprint] = file.label
		if file.missing {
			missing = append(missing, file.fingerprint)
		}
	}
	return moved, missing, nil
}

func mergedVersionLabel(first string, used map[string]bool) (string, error) {
	for n := 2; n <= len(used)+2; n++ {
		ordinal := strconv.Itoa(n)
		for _, label := range []string{ordinal, first + " " + ordinal, "~" + ordinal} {
			if label > first && !used[label] && ValidMediaVersionLabel(label) {
				return label, nil
			}
		}
	}
	return "", fmt.Errorf("no available Version label after %q", first)
}
