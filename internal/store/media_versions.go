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

// ErrVersionSlotTaken is the ErrVersionNotAttached case where another live
// file holds the Version slot the file asked for.
var ErrVersionSlotTaken = fmt.Errorf("%w: version slot is taken", ErrVersionNotAttached)

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
	// DeriveLabels, with Label and AnchorLabel empty, asks the store to
	// label the new file and the anchor from their file facts instead of
	// asking for the default slot: neither carries a parsed label, so they
	// would otherwise contend for it (CORE-2473). See derivedVersionLabels.
	DeriveLabels bool
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
	if err := s.refuseManagedAdmissionInFlight(req.File.Path); err != nil {
		return MediaFile{}, err
	}
	if !ValidMediaVersionLabel(req.Label) || !ValidMediaVersionLabel(req.AnchorLabel) {
		return MediaFile{}, ValidationError("invalid version label")
	}
	if req.AnchorLabel != "" && req.AnchorFingerprint == "" {
		return MediaFile{}, errors.New("anchor fingerprint is required to relabel the anchor")
	}
	if req.DeriveLabels && (req.Label != "" || req.AnchorLabel != "" || req.AnchorFingerprint == "") {
		return MediaFile{}, errors.New("derived labels need an anchor fingerprint and no explicit labels")
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
	file, err := importMediaFileVersionSQL(ctx, plane, req, now, !s.trashDisabled && !plane.encrypted)
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

func importMediaFileVersionSQL(ctx context.Context, plane catalogPlane, req MediaVersionImport, now time.Time, verifyCopies bool) (MediaFile, error) {
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return MediaFile{}, err
	}
	defer tx.Rollback()

	// The first Version before this import: an unprobed refresh of that same
	// file keeps the frame size the row already holds for it.
	priorFirst, err := firstVersionFingerprintSQL(ctx, tx, req.MediaID)
	if err != nil {
		return MediaFile{}, err
	}
	var libraryID LibraryID
	err = tx.QueryRowContext(ctx, "SELECT library_id FROM media WHERE id = ?", req.MediaID).Scan(&libraryID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && libraryID != req.File.LibraryID) {
		return MediaFile{}, fmt.Errorf("%w: media not in library", ErrVersionNotAttached)
	}
	if err != nil {
		return MediaFile{}, err
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
		// The same bytes re-scanned: keep both Version labels. A
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
		// Import a possible copy separately so the post-walk pass can
		// verify it and choose the sampled Media as the survivor.
		if verifyCopies && strings.HasPrefix(imported.ContentFingerprint, "sample1:") && imported.OSHash != "" {
			var possibleCopy bool
			if err := tx.QueryRowContext(ctx, `
				SELECT EXISTS (SELECT 1 FROM media_file
				  WHERE media_id = ? AND missing_at_ms IS NULL AND content_fingerprint GLOB 'sha256:*'
				    AND path <> ? AND oshash = ? AND size_bytes = ?)
			`, req.MediaID, imported.Path, imported.OSHash, imported.SizeBytes).Scan(&possibleCopy); err != nil {
				return MediaFile{}, err
			}
			if possibleCopy {
				return MediaFile{}, ErrVersionNotAttached
			}
		}
		// New bytes without a parsed label keep the slot of the file they
		// replace in place, which may hold a derived label (CORE-2473).
		replacedLabel, replaced, err := replacedVersionLabelTx(ctx, tx, req.MediaID, imported.Path, imported.ContentFingerprint)
		if err != nil {
			return MediaFile{}, err
		}
		if replaced && req.Label == "" {
			file.VersionLabel = replacedLabel
		}
		if req.AnchorLabel != "" {
			// A sampled anchor with a legacy copy keeps the default slot
			// unless the incoming Version needs it.
			if _, err := tx.ExecContext(ctx, `
				UPDATE media_file AS anchor SET version_label = ?
				WHERE content_fingerprint = ? AND media_id = ? AND version_label = '' AND part_ordinal = 0
				  AND missing_at_ms IS NULL
				  AND NOT EXISTS (
				    SELECT 1 FROM media_file
				    WHERE media_id = anchor.media_id AND version_label = ? AND part_ordinal = 0 AND missing_at_ms IS NULL)
				  AND (? = '' OR NOT EXISTS (
				    SELECT 1 FROM media_file legacy
				    WHERE legacy.media_id = anchor.media_id AND legacy.part_ordinal = 0 AND legacy.path <> anchor.path
				      AND anchor.content_fingerprint GLOB 'sample1:*' AND legacy.content_fingerprint GLOB 'sha256:*'
				      AND anchor.oshash <> '' AND anchor.oshash = legacy.oshash AND anchor.size_bytes = legacy.size_bytes))
			`, req.AnchorLabel, req.AnchorFingerprint, req.MediaID, req.AnchorLabel, file.VersionLabel); err != nil {
				return MediaFile{}, err
			}
		}
		if req.DeriveLabels && !replaced {
			label, err := placeDerivedVersionTx(ctx, tx, req, priorFirst)
			if err != nil {
				return MediaFile{}, err
			}
			file.VersionLabel = label
		}
		if err := insertMediaFileVersionTx(ctx, tx, file, now); err != nil {
			return MediaFile{}, err
		}
	default:
		return MediaFile{}, err
	}

	if imported.Streams != nil || imported.FontAttachments != nil {
		if err := replaceMediaFileInventoryTx(ctx, tx, plane.encrypted, imported.ContentFingerprint, imported.Streams, imported.FontAttachments); err != nil {
			return MediaFile{}, err
		}
	}
	if imported.Sidecars != nil {
		if err := replaceMediaFileSidecarsTx(ctx, tx, imported.ContentFingerprint, imported.Sidecars); err != nil {
			return MediaFile{}, err
		}
	}
	if err := refreshFirstVersionFactsTx(ctx, tx, req.MediaID, imported, priorFirst); err != nil {
		return MediaFile{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaFile{}, err
	}
	return file, nil
}

// refreshFirstVersionFactsTx copies the attached file's probe facts onto the
// Media row when that file now holds the Media's first Version (label order,
// part 0, counting missing files). Frame size and duration are stored only on
// the row, so playback judges the first Version by the row and treats every
// other Version's frame size as unknown; this keeps the row describing that
// first Version. A file that newly becomes first sets the frame size to its
// own, zero when it was not probed, so the row never claims the previous
// file's height for it. An unprobed refresh of the file that was already
// first (priorFirst) keeps the frame size, and an unprobed duration keeps the
// title's known one.
func refreshFirstVersionFactsTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, imported ImportMediaFile, priorFirst string) error {
	first, err := firstVersionFingerprintSQL(ctx, tx, mediaID)
	if err != nil {
		return err
	}
	if first != imported.ContentFingerprint {
		// A relabelled anchor can leave another existing file first. The
		// store holds no frame size for that file, so the row stops
		// claiming the previous first file's (CORE-2473).
		if first != priorFirst {
			_, err = tx.ExecContext(ctx, "UPDATE media SET video_width = 0, video_height = 0 WHERE id = ?", mediaID)
		}
		return err
	}
	if imported.KeepProbeFacts && first == priorFirst {
		return nil
	}
	width, height := max(imported.VideoWidthHint, 0), max(imported.VideoHeightHint, 0)
	keepFrame := priorFirst == first && width == 0 && height == 0
	_, err = tx.ExecContext(ctx, `
		UPDATE media SET
		  container = ?, codec = ?,
		  duration_seconds = CASE WHEN ? > 0 THEN ? ELSE duration_seconds END,
		  video_width = CASE WHEN ? THEN video_width ELSE ? END,
		  video_height = CASE WHEN ? THEN video_height ELSE ? END
		WHERE id = ?
	`, imported.Container, imported.Codec, imported.DurationSecondsHint, imported.DurationSecondsHint,
		keepFrame, width, keepFrame, height, mediaID)
	return err
}

// firstVersionFingerprintSQL returns the content fingerprint of the Media's
// first Version in label order (part 0), counting files marked missing, or ""
// when it has none. The Media row's probe facts describe this file: a missing
// first file keeps them until it returns or a Version sorting before it joins.
// A live file wins a tie with a missing one at the same label.
func firstVersionFingerprintSQL(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, mediaID MediaID) (string, error) {
	var first string
	err := q.QueryRowContext(ctx, `
		SELECT content_fingerprint FROM media_file
		WHERE media_id = ? AND part_ordinal = 0
		ORDER BY version_label, missing_at_ms IS NOT NULL LIMIT 1
	`, mediaID).Scan(&first)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return first, err
}

// MediaVersionHasAdditionalParts counts missing Parts too: the runtime of one
// available Part still does not describe the whole Version.
func (s *Store) MediaVersionHasAdditionalParts(ctx context.Context, id MediaID, label string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return false, err
	}
	var multipart bool
	err = plane.reader.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM media_file WHERE media_id = ? AND version_label = ? AND part_ordinal > 0
	)`, id, label).Scan(&multipart)
	return multipart, err
}

// placeDerivedVersionTx picks the label of a new file that, like its anchor,
// carries no parsed label (MediaVersionImport.DeriveLabels) and replaces no
// file of the Media in place (replacedVersionLabelTx). When the Media's live
// files are all labelled, the new file joins only
// under a label of the kind they are all labelled by, so it differs from each
// of them in that fact. Otherwise a lone anchor in the default slot moves to
// its own label of the first kind that tells the two files apart. A label a
// missing file holds stays reserved for its return. It returns
// ErrVersionSlotTaken when no label fits, so the caller imports the file as
// its own Media as before.
func placeDerivedVersionTx(ctx context.Context, tx *sql.Tx, req MediaVersionImport, priorFirst string) (string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT content_fingerprint, path, version_label, missing_at_ms IS NOT NULL FROM media_file
		WHERE media_id = ? AND part_ordinal = 0 AND content_fingerprint <> ?
	`, req.MediaID, req.File.ContentFingerprint)
	if err != nil {
		return "", err
	}
	type version struct {
		fingerprint, path, label string
		missing                  bool
	}
	var versions []version
	for rows.Next() {
		var v version
		if err := rows.Scan(&v.fingerprint, &v.path, &v.label, &v.missing); err != nil {
			return "", errors.Join(err, rows.Close())
		}
		versions = append(versions, v)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", err
	}
	reserved := map[string]bool{}
	var live []version
	var anchor *version
	for i, v := range versions {
		reserved[v.label] = true
		if v.missing {
			continue
		}
		live = append(live, v)
		if v.fingerprint == req.AnchorFingerprint {
			anchor = &versions[i]
		}
	}
	wanted := derivedVersionLabels(req.File.VideoWidthHint, req.File.VideoHeightHint, req.File.Path)
	if len(live) > 0 && !reserved[""] {
		// The anchor itself may have been replaced earlier in this scan; the
		// Media's labelled files decide, as for an explicit label.
		for kind, label := range wanted {
			if label == "" || reserved[label] {
				continue
			}
			sameKind := true
			for _, v := range versions {
				if !derivedLabelOfKind(kind, v.label, v.path) {
					sameKind = false
					break
				}
			}
			if sameKind {
				return label, nil
			}
		}
		return "", fmt.Errorf("%w: no derived label tells the file from the Media's Versions", ErrVersionSlotTaken)
	}
	if anchor == nil || anchor.label != "" || len(versions) != 1 {
		// The anchor is gone, or other files share the Media with an
		// unlabelled one: the store holds no facts that tell them apart.
		return "", fmt.Errorf("%w: no lone unlabelled anchor to label", ErrVersionSlotTaken)
	}
	// The Media row's frame size is its first Version's; it is the anchor's
	// only while the anchor is that first Version.
	var width, height sql.NullInt64
	if priorFirst == req.AnchorFingerprint {
		if err := tx.QueryRowContext(ctx, "SELECT video_width, video_height FROM media WHERE id = ?", req.MediaID).Scan(&width, &height); err != nil {
			return "", err
		}
	}
	anchorLabels := derivedVersionLabels(int(width.Int64), int(height.Int64), anchor.path)
	for kind, label := range wanted {
		if label == "" || anchorLabels[kind] == "" || label == anchorLabels[kind] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE media_file SET version_label = ?
			WHERE content_fingerprint = ? AND media_id = ? AND version_label = '' AND part_ordinal = 0
			  AND missing_at_ms IS NULL
		`, anchorLabels[kind], req.AnchorFingerprint, req.MediaID); err != nil {
			return "", err
		}
		return label, nil
	}
	return "", fmt.Errorf("%w: no derived label tells the files apart", ErrVersionSlotTaken)
}

// replacedVersionLabelTx returns the label of the Media's file at path that
// other bytes held, live first, then the most recently missing, and whether
// there is one.
func replacedVersionLabelTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, path, fingerprint string) (string, bool, error) {
	var label string
	err := tx.QueryRowContext(ctx, `
		SELECT version_label FROM media_file
		WHERE media_id = ? AND path = ? AND part_ordinal = 0 AND content_fingerprint <> ?
		ORDER BY missing_at_ms IS NOT NULL, missing_at_ms DESC LIMIT 1
	`, mediaID, path, fingerprint).Scan(&label)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return label, err == nil, err
}

// derivedLabelOfKind reports whether label is a derived label of the given
// kind (see derivedVersionLabels) for the file at path: a resolution class,
// or that file's own container.
func derivedLabelOfKind(kind int, label, path string) bool {
	switch kind {
	case 0:
		switch label {
		case "4320p", "2160p", "1440p", "1080p", "720p", "SD":
			return true
		}
		return false
	default:
		return label != "" && label == derivedVersionLabels(0, 0, path)[1]
	}
}

// derivedVersionLabels lists the labels a file without a parsed one can take,
// most telling first: its resolution class, then its container. An entry is
// "" when the fact is unknown or does not make a valid label. Both are facts
// of the encoding, not of the content: files of one title that differ only by
// name may be different items a plugin could not tell apart, so they stay
// apart. They are generic file facts, so no domain knowledge is needed to
// name them.
func derivedVersionLabels(width, height int, path string) [2]string {
	labels := [2]string{resolutionVersionLabel(width, height)}
	if container := strings.ToUpper(strings.TrimPrefix(filepath.Ext(path), ".")); ValidMediaVersionLabel(container) {
		labels[1] = container
	}
	return labels
}

// resolutionVersionLabel names a probed frame's resolution class, or "" when
// the frame size is unknown. Width counts as well as height, so a letterboxed
// 1920x800 frame is 1080p.
func resolutionVersionLabel(width, height int) string {
	switch {
	case width <= 0 || height <= 0:
		return ""
	case width >= 6000 || height >= 3600:
		return "4320p"
	case width >= 3200 || height >= 1800:
		return "2160p"
	case width >= 2200 || height >= 1300:
		return "1440p"
	case width >= 1600 || height >= 900:
		return "1080p"
	case width >= 1100 || height >= 650:
		return "720p"
	default:
		return "SD"
	}
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
		file.OSHash, file.PHash, file.SizeBytes, mediaFileTimeToMillis(file.ModTime), timeToMillis(now))
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
		return fmt.Errorf("%w: %q", ErrVersionSlotTaken, file.VersionLabel)
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

// MediaVersionPartCount returns how many Parts a Media's Version has
// catalogued, counting files marked missing, so a caller holding only the
// available files can tell a complete Version from one with a missing Part.
func (s *Store) MediaVersionPartCount(ctx context.Context, id MediaID, versionLabel string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return 0, err
	}
	var parts int
	err = plane.reader.QueryRowContext(ctx,
		"SELECT COUNT(DISTINCT part_ordinal) FROM media_file WHERE media_id = ? AND version_label = ?", id, versionLabel,
	).Scan(&parts)
	return parts, err
}
