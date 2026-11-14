package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/beamfall/core/internal/serverrole"
)

// MaxMediaVersionLabelLen bounds a Version label in bytes, matching the plugin
// SDK's ParsedMedia.VersionLabel contract.
const MaxMediaVersionLabelLen = 64

// ErrVersionNotAttached reports that a scanned file could not join an existing
// Media as a Version: the Media is gone or in another Library, the bytes
// already belong to another Media, or the Version slot is taken. The scan then
// imports the file as its own Media, so grouping never loses a file.
var ErrVersionNotAttached = errors.New("file not attached as a version")

// MediaVersionImport attaches a scanned file to an existing Media as a Version
// (ADR-0009). File carries the same probe facts as a normal import; its
// title, year and class are ignored because the Media row already exists.
type MediaVersionImport struct {
	MediaID MediaID
	File    ImportMediaFile
	// Label names the new file's Version; "" asks for the default slot.
	Label string
	// AnchorFingerprint and AnchorLabel relabel the Media's current default
	// file before the new file is placed, so a labelled file imported first
	// can move out of the default slot when an unlabelled sibling arrives.
	// An empty AnchorLabel leaves the anchor where it is.
	AnchorFingerprint string
	AnchorLabel       string
}

// ValidMediaVersionLabel reports whether label is usable as a Version label:
// valid UTF-8, trimmed, at most MaxMediaVersionLabelLen bytes, no control
// characters. "" is valid and names the default Version.
func ValidMediaVersionLabel(label string) bool {
	if len(label) > MaxMediaVersionLabelLen || !utf8.ValidString(label) || strings.TrimSpace(label) != label {
		return false
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ImportMediaFileVersion places a scanned file on an existing Media at Version
// slot (req.Label, part 0) in one transaction. A file whose bytes are already
// on that Media keeps its slot and is refreshed. Any conflict returns
// ErrVersionNotAttached and rolls back, including the anchor relabel.
// Server-internal scan path, like ImportMediaFile.
func (s *Store) ImportMediaFileVersion(ctx context.Context, req MediaVersionImport) (MediaFile, error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return MediaFile{}, err
	}
	if req.MediaID == "" {
		return MediaFile{}, errors.New("media ID is required")
	}
	if err := validateImportMediaFile(req.File); err != nil {
		return MediaFile{}, err
	}
	if !ValidMediaVersionLabel(req.Label) || !ValidMediaVersionLabel(req.AnchorLabel) {
		return MediaFile{}, ValidationError("invalid version label")
	}
	if req.AnchorLabel != "" && req.AnchorFingerprint == "" {
		return MediaFile{}, errors.New("anchor fingerprint is required to relabel the anchor")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MediaFile{}, err
	}
	now := time.Now().UTC()
	plane, err := s.catalogPlaneForLibraryLocked(ctx, req.File.LibraryID)
	if err != nil {
		return MediaFile{}, err
	}
	file, err := importMediaFileVersionSQL(ctx, plane, req, now)
	if err != nil {
		return MediaFile{}, err
	}
	if !plane.encrypted {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, req.MediaID, nil, now); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", req.MediaID, "error", eventErr)
		}
	}
	return file, nil
}

func importMediaFileVersionSQL(ctx context.Context, plane catalogPlane, req MediaVersionImport, now time.Time) (MediaFile, error) {
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return MediaFile{}, err
	}
	defer tx.Rollback()

	var libraryID LibraryID
	err = tx.QueryRowContext(ctx, "SELECT library_id FROM media WHERE id = ?", req.MediaID).Scan(&libraryID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && libraryID != req.File.LibraryID) {
		return MediaFile{}, fmt.Errorf("%w: media not in library", ErrVersionNotAttached)
	}
	if err != nil {
		return MediaFile{}, err
	}
	if req.AnchorLabel != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE media_file SET version_label = ?
			WHERE content_fingerprint = ? AND media_id = ? AND version_label = '' AND part_ordinal = 0
			  AND missing_at_ms IS NULL
			  AND NOT EXISTS (
			    SELECT 1 FROM media_file
			    WHERE media_id = ? AND version_label = ? AND part_ordinal = 0 AND missing_at_ms IS NULL)
		`, req.AnchorLabel, req.AnchorFingerprint, req.MediaID, req.MediaID, req.AnchorLabel); err != nil {
			return MediaFile{}, err
		}
	}

	imported := req.File
	file := MediaFile{
		MediaID:            req.MediaID,
		Path:               imported.Path,
		ContentFingerprint: imported.ContentFingerprint,
		VersionLabel:       req.Label,
		OSHash:             imported.OSHash,
		PHash:              imported.PHash,
		SizeBytes:          imported.SizeBytes,
		ModTime:            imported.ModTime,
		LastSeenAt:         now,
	}
	var existingMedia MediaID
	var existingLabel string
	var existingPart int
	var existingMissing sql.NullInt64
	err = tx.QueryRowContext(ctx,
		"SELECT media_id, version_label, part_ordinal, missing_at_ms FROM media_file WHERE content_fingerprint = ?",
		imported.ContentFingerprint,
	).Scan(&existingMedia, &existingLabel, &existingPart, &existingMissing)
	switch {
	case err == nil && existingMedia != req.MediaID:
		return MediaFile{}, fmt.Errorf("%w: bytes belong to another media", ErrVersionNotAttached)
	case err == nil:
		// The same bytes re-scanned: keep the slot they already hold. A
		// missing row cannot come back over a live file holding its slot.
		file.VersionLabel, file.PartOrdinal = existingLabel, existingPart
		if existingMissing.Valid {
			if err := requireFreeVersionSlotTx(ctx, tx, file); err != nil {
				return MediaFile{}, err
			}
		}
		if err := upsertMediaFileTx(ctx, tx, file, now); err != nil {
			return MediaFile{}, err
		}
	case errors.Is(err, sql.ErrNoRows):
		if err := insertMediaFileVersionTx(ctx, tx, file, now); err != nil {
			return MediaFile{}, err
		}
	default:
		return MediaFile{}, err
	}

	if imported.Streams != nil || imported.FontAttachments != nil {
		if err := replaceMediaFileInventoryTx(ctx, tx, imported.ContentFingerprint, imported.Streams, imported.FontAttachments); err != nil {
			return MediaFile{}, err
		}
	}
	if imported.Sidecars != nil {
		if err := replaceMediaFileSidecarsTx(ctx, tx, imported.ContentFingerprint, imported.Sidecars); err != nil {
			return MediaFile{}, err
		}
	}
	if err := refreshFirstVersionFactsTx(ctx, tx, req.MediaID, imported); err != nil {
		return MediaFile{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaFile{}, err
	}
	return file, nil
}

// refreshFirstVersionFactsTx copies the attached file's probe facts onto the
// Media row when that file now holds the Media's first Version (label order,
// part 0). Frame size and duration are stored only on the row, so playback
// judges the first Version by the row and treats every other Version's frame
// size as unknown; this keeps the row describing that first Version.
func refreshFirstVersionFactsTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, imported ImportMediaFile) error {
	var first string
	err := tx.QueryRowContext(ctx, `
		SELECT content_fingerprint FROM media_file
		WHERE media_id = ? AND part_ordinal = 0 AND missing_at_ms IS NULL
		ORDER BY version_label LIMIT 1
	`, mediaID).Scan(&first)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || first != imported.ContentFingerprint {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE media SET
		  duration_seconds = ?, container = ?, codec = ?,
		  video_width = CASE WHEN ? > 0 THEN ? ELSE video_width END,
		  video_height = CASE WHEN ? > 0 THEN ? ELSE video_height END
		WHERE id = ?
	`, max(0, imported.DurationSecondsHint), imported.Container, imported.Codec,
		imported.VideoWidthHint, imported.VideoWidthHint, imported.VideoHeightHint, imported.VideoHeightHint, mediaID)
	return err
}

// insertMediaFileVersionTx inserts new bytes at file's (Version, part 0) slot.
// Another live file at the same path is marked missing first, as on import.
func insertMediaFileVersionTx(ctx context.Context, tx *sql.Tx, file MediaFile, now time.Time) error {
	// Retire the file this path used to hold before checking the slot: a file
	// replaced in place with new bytes frees the slot its old bytes held.
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_file SET missing_at_ms = ?
		WHERE path = ? AND content_fingerprint <> ? AND missing_at_ms IS NULL
	`, timeToMillis(now), file.Path, file.ContentFingerprint); err != nil {
		return err
	}
	if err := requireFreeVersionSlotTx(ctx, tx, file); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, oshash, phash, size_bytes, mtime_ms, missing_at_ms, last_seen_at_ms)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, NULL, ?)
	`, file.ContentFingerprint, file.ContentFingerprint, file.MediaID, file.Path, file.VersionLabel,
		file.OSHash, file.PHash, file.SizeBytes, timeToMillis(file.ModTime), timeToMillis(now))
	return err
}

// requireFreeVersionSlotTx returns ErrVersionNotAttached when a live file other
// than file's own bytes holds file's (Version, Part) slot.
func requireFreeVersionSlotTx(ctx context.Context, tx *sql.Tx, file MediaFile) error {
	var taken int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM media_file
		WHERE media_id = ? AND version_label = ? AND part_ordinal = ? AND missing_at_ms IS NULL
		  AND content_fingerprint <> ?
	`, file.MediaID, file.VersionLabel, file.PartOrdinal, file.ContentFingerprint).Scan(&taken)
	if err == nil {
		return fmt.Errorf("%w: version slot %q is taken", ErrVersionNotAttached, file.VersionLabel)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// MediaFilesInDirectory returns the live files of libraryID that sit directly
// inside dir, so a scan can group a new file with a title it cataloged
// earlier. Server-internal scan read, like MediaFilesForLibrary.
func (s *Store) MediaFilesInDirectory(ctx context.Context, libraryID LibraryID, dir string) ([]MediaFile, error) {
	if libraryID == "" {
		return nil, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	// A half-open range on the path index: every path that starts with
	// dir + separator. Deeper paths are dropped below.
	prefix := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)
	upper := prefix[:len(prefix)-1] + string(rune(filepath.Separator)+1)
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT f.media_id, f.path, f.content_fingerprint, f.version_label, f.part_ordinal
		FROM media_file f
		JOIN media m ON m.id = f.media_id
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL AND f.missing_at_ms IS NULL
		  AND f.path >= ? AND f.path < ?
		ORDER BY f.path, f.media_id
	`, libraryID, prefix, upper)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []MediaFile
	for rows.Next() {
		var file MediaFile
		if err := rows.Scan(&file.MediaID, &file.Path, &file.ContentFingerprint, &file.VersionLabel, &file.PartOrdinal); err != nil {
			return nil, err
		}
		if filepath.Dir(file.Path) == filepath.Clean(dir) {
			files = append(files, file)
		}
	}
	return files, rows.Err()
}
