package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

// SourceFileGeneration identifies the bytes an encoder opened. Size and mtime
// alone cannot distinguish an atomic replacement with preserved timestamps.
func SourceFileGeneration(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	identity, ok := mountProbeFileIdentity(info)
	if !ok {
		return ""
	}
	return fmt.Sprintf("file:%s:%d:%d", identity, info.Size(), info.ModTime().UnixNano())
}

// EncryptedSourceGeneration uses the immutable, content-addressed object identity,
// never a transient decrypting reader, endpoint URL, or session cache path.
func EncryptedSourceGeneration(fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	return "content:" + fingerprint
}

func currentSourceGeneration(file MediaFile, encrypted bool) string {
	if encrypted {
		return EncryptedSourceGeneration(file.ContentFingerprint)
	}
	info, err := os.Stat(file.Path)
	if err != nil {
		return ""
	}
	return SourceFileGeneration(info)
}

// RecordSourceEnd keeps an observed premature EOF in the source's catalog plane.
// The generation is captured before encoding and checked again here; a late
// encoder from a replaced/growing source cannot poison its replacement.
func (s *Store) RecordSourceEnd(ctx context.Context, mediaID MediaID, path, fingerprint, generation string, endSeconds float64) error {
	if generation == "" {
		return nil
	}
	if endSeconds < 0 {
		return errors.New("negative source end")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return err
	}
	var file MediaFile
	file.MediaID = mediaID
	err = plane.reader.QueryRowContext(ctx, `SELECT path, content_fingerprint FROM media_file WHERE media_id = ? AND missing_at_ms IS NULL AND ((? <> '' AND content_fingerprint = ?) OR (? = '' AND path = ?))`, mediaID, fingerprint, fingerprint, fingerprint, path).Scan(&file.Path, &file.ContentFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if currentSourceGeneration(file, plane.encrypted) != generation {
		return nil
	}
	_, err = plane.writer.ExecContext(ctx, `INSERT INTO source_end_observation(content_fingerprint, generation, end_seconds) VALUES (?, ?, ?) ON CONFLICT(content_fingerprint, generation) DO UPDATE SET end_seconds = MIN(end_seconds, excluded.end_seconds)`, file.ContentFingerprint, generation, endSeconds)
	return err
}

// MediaFileSourceIncomplete applies durable observations even before a Sessions
// manager is created. An unreadable observed source stays blocked until its
// generation can be revalidated; eviction and restart cannot make it playable.
func (s *Store) MediaFileSourceIncomplete(ctx context.Context, file MediaFile) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, file.MediaID)
	if err != nil {
		return false, err
	}
	rows, err := plane.reader.QueryContext(ctx, `SELECT generation FROM source_end_observation WHERE content_fingerprint = ?`, file.ContentFingerprint)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	current := ""
	checked := false
	for rows.Next() {
		var observed string
		if err := rows.Scan(&observed); err != nil {
			return false, err
		}
		if !checked {
			current = currentSourceGeneration(file, plane.encrypted)
			checked = true
		}
		if current == "" || current == observed {
			return true, nil
		}
	}
	return false, rows.Err()
}

// incompleteMediaIDsSQL filters before engagement pagination. Only observed
// sources require a stat; each open catalog plane is queried once per batch.
// A shelf has no negotiated Version, so an incomplete available Version keeps
// the item off the shelf without preventing explicit playback of another one.
func (s *Store) incompleteMediaIDsSQL(ctx context.Context, ids []MediaID) (map[MediaID]bool, error) {
	blocked := map[MediaID]bool{}
	if len(ids) == 0 {
		return blocked, nil
	}
	planes := []catalogPlane{{reader: s.catalogR}}
	for _, db := range s.encryptedDBs {
		if db != nil && db.state == "unsealed-registered" && db.reader != nil {
			planes = append(planes, catalogPlane{reader: db.reader, encrypted: true})
		}
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	for _, plane := range planes {
		// #nosec G202 -- only generated question-mark placeholders are concatenated; all values are bound.
		rows, err := plane.reader.QueryContext(ctx, `SELECT f.media_id, f.path, f.content_fingerprint, o.generation FROM media_file f JOIN source_end_observation o ON o.content_fingerprint = f.content_fingerprint WHERE f.missing_at_ms IS NULL AND f.media_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var file MediaFile
			var observed string
			if err := rows.Scan(&file.MediaID, &file.Path, &file.ContentFingerprint, &observed); err != nil {
				return nil, errors.Join(err, rows.Close())
			}
			current := currentSourceGeneration(file, plane.encrypted)
			if current == "" || current == observed {
				blocked[file.MediaID] = true
			}
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return nil, err
		}
	}
	return blocked, nil
}
