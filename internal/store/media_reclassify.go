package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// Attached-picture reclassification (CORE-1262). Before CORE-1151 an audio
// file with embedded cover art probed as video (the art is an attached_pic
// "video" stream), so its Media row kept media_class video: music hydration
// skipped it and it opened in the video player. A rescan never reclassifies an
// unchanged file, so a one-time repair re-probes the video-class Media of each
// Library and moves the ones that are really audio. These are its store
// operations; the marker below records that a Library was repaired.

// attachedPictureReclassPrefix prefixes the server_config rows recording that
// a Library's video-class Media were checked once for attached-picture audio.
const attachedPictureReclassPrefix = "reclass-attached-picture."

func attachedPictureReclassKey(libraryID LibraryID) string {
	return attachedPictureReclassPrefix + string(libraryID)
}

// attachedPictureCodecs are the still-image codecs ffprobe reports for embedded
// cover art. A pre-CORE-1151 import stored the cover art's codec as the Media
// codec, so only video-class rows carrying one of these can be misclassed; the
// repair re-probes those and never a library's real videos (the probe still
// decides, so an MJPEG camera clip stays video).
var attachedPictureCodecs = []any{"mjpeg", "png", "bmp", "gif", "webp", "tiff"}

// ReclassifyCandidate is one live video-class Media and the path of its first
// available file, which the repair re-probes.
type ReclassifyCandidate struct {
	MediaID MediaID
	Path    string
}

// AttachedPictureCandidatesInLibrary lists the live (non-trashed, non-Extra)
// video-class Media of a Library whose codec is a still-image codec and that
// still have an available file, with that file's path (the lowest part
// ordinal), ordered by Media ID.
func (s *Store) AttachedPictureCandidatesInLibrary(ctx context.Context, libraryID LibraryID) ([]ReclassifyCandidate, error) {
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
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT m.id, (
		  SELECT f.path FROM media_file f
		  WHERE f.media_id = m.id AND f.missing_at_ms IS NULL
		  ORDER BY f.part_ordinal, f.path
		  LIMIT 1
		) AS path
		FROM media m
		WHERE m.library_id = ?
		  AND m.media_class = ?
		  AND m.trashed_at_ms IS NULL
		  AND m.extra = 0
		  AND m.codec IN (?, ?, ?, ?, ?, ?)
		  AND EXISTS (
		    SELECT 1 FROM media_file f
		    WHERE f.media_id = m.id AND f.missing_at_ms IS NULL
		  )
		ORDER BY m.id
	`, append([]any{libraryID, MediaClassVideo}, attachedPictureCodecs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReclassifyCandidate
	for rows.Next() {
		var candidate ReclassifyCandidate
		if err := rows.Scan(&candidate.MediaID, &candidate.Path); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

// ReclassifyVideoMediaAsAudio moves one Media from class video to audio with
// the probed audio codec, clearing the cover art's frame size. It changes only
// a row that is still video, so a repeat (an interrupted repair resuming) or a
// row a scan already fixed is a no-op; the bool reports whether it changed.
func (s *Store) ReclassifyVideoMediaAsAudio(ctx context.Context, id MediaID, audioCodec string) (bool, error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return false, err
	}
	if id == "" {
		return false, errors.New("media ID is required")
	}
	if audioCodec == "" {
		return false, errors.New("audio codec is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return false, err
	}
	result, err := plane.writer.ExecContext(ctx, `
		UPDATE media SET media_class = ?, codec = ?, video_width = 0, video_height = 0
		WHERE id = ? AND media_class = ?
	`, MediaClassAudio, audioCodec, id, MediaClassVideo)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// Attached-picture repair states recorded per Library in server_config.
const (
	// AttachedPictureReclassPending: the repair started reclassifying rows but
	// has not yet queued the hydration scan for them.
	AttachedPictureReclassPending = "pending"
	// AttachedPictureReclassDone: the Library was repaired and its hydration
	// scan queued; the repair never runs on it again.
	AttachedPictureReclassDone = "done"
)

// AttachedPictureReclassState reports the repair state recorded for a Library:
// "" (never started), AttachedPictureReclassPending or AttachedPictureReclassDone.
func (s *Store) AttachedPictureReclassState(ctx context.Context, libraryID LibraryID) (string, error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return "", err
	}
	if libraryID == "" {
		return "", errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	var state string
	err := s.catalogR.QueryRowContext(ctx, `SELECT value FROM server_config WHERE key = ?`, attachedPictureReclassKey(libraryID)).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return state, err
}

// SetAttachedPictureReclassState records a Library's repair state. The repair
// records pending before its first reclassification, so a restart after rows
// moved but before their hydration scan was queued still queues it, and done
// only after that scan is queued. Done is final: pending never replaces it.
func (s *Store) SetAttachedPictureReclassState(ctx context.Context, libraryID LibraryID, state string, now time.Time) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	if state != AttachedPictureReclassPending && state != AttachedPictureReclassDone {
		return fmt.Errorf("unknown attached-picture repair state %q", state)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at_ms = excluded.updated_at_ms
		WHERE server_config.value <> ?
	`, attachedPictureReclassKey(libraryID), state, timeToMillis(now), AttachedPictureReclassDone)
	return err
}
