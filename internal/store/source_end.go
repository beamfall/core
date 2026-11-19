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

// pendingSourceEnd is owned by Store.mu and survives session/cache eviction.
// It is removed only after a successful write (or source-generation replacement).
type pendingSourceEnd struct {
	mediaID                       MediaID
	path, fingerprint, generation string
	libraryID                     LibraryID
}

// RecordSourceEnd keeps an observed premature EOF in the source's catalog plane.
func (s *Store) RecordSourceEnd(ctx context.Context, mediaID MediaID, path, fingerprint, generation string, endSeconds float64) error {
	return s.ObserveSourceEnd(ctx, mediaID, path, fingerprint, generation, func() (float64, bool) { return endSeconds, true })
}

// ObserveSourceEnd serializes EOF detection and persistence with catalog sealing.
// observe must only inspect the encoder output; it must not call Store methods.
// If sealing already won, observe is not called. Otherwise a failed write stays
// pending and must flush before sealing or closing may discard the open catalog.
func (s *Store) ObserveSourceEnd(ctx context.Context, mediaID MediaID, path, fingerprint, generation string, observe func() (float64, bool)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return err
	}
	endSeconds, observed := observe()
	if !observed || generation == "" {
		return nil
	}
	if endSeconds < 0 {
		return errors.New("negative source end")
	}
	key := pendingSourceEnd{mediaID: mediaID, path: path, fingerprint: fingerprint, generation: generation, libraryID: plane.libraryID}
	if s.pendingSourceEnds == nil {
		s.pendingSourceEnds = make(map[pendingSourceEnd]float64)
	}
	if previous, ok := s.pendingSourceEnds[key]; !ok || endSeconds < previous {
		s.pendingSourceEnds[key] = endSeconds
	}
	return s.flushSourceEndLocked(ctx, key)
}

// An empty libraryID flushes every plane. Keep failures pending and fail closed:
// neither a failed seal nor Close may throw away the only copy of an observation.
func (s *Store) flushSourceEndsLocked(ctx context.Context, libraryID LibraryID) error {
	for key := range s.pendingSourceEnds {
		if libraryID != "" && key.libraryID != libraryID {
			continue
		}
		if err := s.flushSourceEndLocked(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) flushSourceEndLocked(ctx context.Context, key pendingSourceEnd) error {
	file, plane, err := s.sourceEndFileLocked(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		delete(s.pendingSourceEnds, key)
		return nil
	}
	if err != nil {
		return err
	}
	current := currentSourceGeneration(file, plane.encrypted)
	if current == "" || current == key.generation {
		// An unreadable file still gets the observed generation durably recorded;
		// reads remain blocked until they can verify that the bytes were replaced.
		_, err = plane.writer.ExecContext(ctx, `INSERT INTO source_end_observation(content_fingerprint, generation, end_seconds) VALUES (?, ?, ?) ON CONFLICT(content_fingerprint, generation) DO UPDATE SET end_seconds = MIN(end_seconds, excluded.end_seconds)`, file.ContentFingerprint, key.generation, s.pendingSourceEnds[key])
		if err != nil {
			return err
		}
	}
	delete(s.pendingSourceEnds, key)
	return nil
}

func (s *Store) sourceEndFileLocked(ctx context.Context, key pendingSourceEnd) (MediaFile, catalogPlane, error) {
	plane, err := s.catalogPlaneForLibraryLocked(ctx, key.libraryID)
	if err != nil {
		// A purged Library has no source left to observe; trash/archive retain the
		// row and therefore retain their pending observations too.
		var exists bool
		if queryErr := s.catalogR.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM library WHERE id = ?)`, key.libraryID).Scan(&exists); queryErr == nil && !exists {
			err = sql.ErrNoRows
		}
		return MediaFile{}, plane, err
	}
	file := MediaFile{MediaID: key.mediaID}
	err = plane.reader.QueryRowContext(ctx, `SELECT path, content_fingerprint FROM media_file WHERE media_id = ? AND ((? <> '' AND content_fingerprint = ?) OR (? = '' AND path = ?))`, key.mediaID, key.fingerprint, key.fingerprint, key.fingerprint, key.path).Scan(&file.Path, &file.ContentFingerprint)
	return file, plane, err
}

// MediaFileSourceIncomplete applies durable observations even before a Sessions
// manager is created. An unreadable observed source stays blocked until its
// generation can be revalidated; eviction and restart cannot make it playable.
func (s *Store) MediaFileSourceIncomplete(ctx context.Context, file MediaFile) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, file.MediaID)
	if err != nil {
		return false, err
	}
	for key := range s.pendingSourceEnds {
		if key.libraryID != plane.libraryID || key.mediaID != file.MediaID {
			continue
		}
		if (key.fingerprint != "" && key.fingerprint != file.ContentFingerprint) || (key.fingerprint == "" && key.path != file.Path) {
			continue
		}
		if err := s.flushSourceEndLocked(ctx, key); err != nil {
			return false, err
		}
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
	for key := range s.pendingSourceEnds {
		file, plane, err := s.sourceEndFileLocked(ctx, key)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		current := currentSourceGeneration(file, plane.encrypted)
		if current == "" || current == key.generation {
			blocked[key.mediaID] = true
		}
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
