package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// ManualMedia describes a HAND-AUTHORED Media (METAPARITY-6, ADR-0068
// associate-vs-create): the operator reviewed an Unmatched Record, decided no
// provider candidate is right, and typed the identity themselves. The defining
// property is the absence: no external ID is ever minted for it — Beamfall never
// implies it found or requires a provider identifier for this Media.
type ManualMedia struct {
	LibraryID LibraryID
	// Path is the file the operator selected from the Unmatched Record's
	// context. Empty creates a file-less Media (browsable, not yet playable).
	Path  string
	Title string
	// Year is optional; 0 means the operator left it blank.
	Year      int
	Container string
	Codec     string
	Class     MediaClass
}

// manualMediaID derives a stable, durable Media ID for a hand-authored Media.
// Like provisionalMediaID it is derived rather than random so a retried create
// (the mockup's idempotent save-error recovery) lands on the SAME Media instead
// of forking a duplicate. The key is (Library, path-or-title) — the identity the
// operator actually selected.
func manualMediaID(libraryID LibraryID, path, title string) MediaID {
	key := path
	if key == "" {
		key = title
	}
	sum := sha256.Sum256([]byte(string(libraryID) + "\x00" + key))
	return MediaID("media-manual-" + hex.EncodeToString(sum[:])[:20])
}

// manualFingerprint is the placeholder content_fingerprint a hand-authored
// media_file carries. content_fingerprint is the media_file PRIMARY KEY, so it
// must be unique per row; deriving it from the Media ID guarantees that and
// keeps it collision-free against any real "sha256:" fingerprint, exactly as
// provisionalFingerprint does.
func manualFingerprint(mediaID MediaID) string {
	return "manual:" + string(mediaID)
}

// CreateManualMedia materializes the hand-authored Media METAPARITY-6 requires
// and returns its durable ID. It is idempotent on the derived ID: a retry after
// a partial failure returns the existing Media rather than a second copy, which
// is what makes the operator-facing "save failed, try again" path safe. The
// title/year are written at the MANUAL mutation tier — the highest source
// precedence — so a later re-scan or re-identify cannot overwrite
// what the operator typed. NO media_external_id row is written, ever: that
// absence is the feature. Server-internal like CreateProvisionalMedia; the
// calling surface resolves visibility and authorization.
func (s *Store) CreateManualMedia(ctx context.Context, m ManualMedia) (MediaID, error) {
	// Authority face: authoring a catalog-identity row is main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return "", err
	}
	if m.LibraryID == "" {
		return "", ValidationError("library ID is required")
	}
	if strings.TrimSpace(m.Title) == "" {
		return "", ValidationError("media title is required")
	}
	if m.Container == "" {
		m.Container = "mkv"
	}
	if m.Codec == "" {
		m.Codec = "h264"
	}
	if m.Class == "" {
		m.Class = MediaClassVideo
	}
	mediaID := manualMediaID(m.LibraryID, m.Path, m.Title)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	created, err := s.createManualMediaSQL(ctx, mediaID, m, now)
	if err != nil {
		return "", err
	}
	if created {
		s.appendManualMediaCreatedEvent(ctx, mediaID, now)
	}
	return mediaID, nil
}

const unmatchedCreateStatePrefix = "unmatched-create:"

var unmatchedCreateMutationMu sync.Mutex

type unmatchedCreateState struct {
	Fingerprint string  `json:"fingerprint"`
	MediaID     MediaID `json:"mediaID"`
}

type unmatchedCreateReservation struct {
	MediaID     MediaID
	fingerprint string
	finalized   bool
}

// CreateManualMediaFromUnmatched durably reserves the request fingerprint and
// Media, runs an optional stage such as artwork publication, then resolves the
// source record. A failed stage leaves the visible reservation pending for an
// identical retry; a conflicting retry cannot replace it.
func (s *Store) CreateManualMediaFromUnmatched(
	ctx context.Context,
	recordID string,
	libraryID LibraryID,
	title string,
	year int,
	artworkDigest string,
	now time.Time,
	beforeFinalize ...func(MediaID) error,
) (MediaID, error) {
	if len(beforeFinalize) > 1 {
		return "", ValidationError("at most one pre-finalization stage is allowed")
	}
	if len(beforeFinalize) == 1 && beforeFinalize[0] == nil {
		return "", ValidationError("pre-finalization stage is required")
	}
	unmatchedCreateMutationMu.Lock()
	defer unmatchedCreateMutationMu.Unlock()
	reservation, err := s.reserveManualMediaFromUnmatched(
		ctx, recordID, libraryID, title, year, artworkDigest, now,
	)
	if err != nil {
		return "", err
	}
	if len(beforeFinalize) == 1 && !reservation.finalized {
		if err := beforeFinalize[0](reservation.MediaID); err != nil {
			return "", err
		}
	}
	if err := s.finalizeManualMediaFromUnmatched(ctx, recordID, reservation, now); err != nil {
		return "", err
	}
	return reservation.MediaID, nil
}

// reserveManualMediaFromUnmatched atomically binds the source record to the
// request fingerprint and creates its Media without resolving the record. The
// reservation survives restart, so an identical retry resumes the same Media
// while a request with different identity or artwork cannot take it over.
func (s *Store) reserveManualMediaFromUnmatched(
	ctx context.Context,
	recordID string,
	libraryID LibraryID,
	title string,
	year int,
	artworkDigest string,
	now time.Time,
) (unmatchedCreateReservation, error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return unmatchedCreateReservation{}, err
	}
	title = strings.TrimSpace(title)
	if recordID == "" || libraryID == "" || title == "" {
		return unmatchedCreateReservation{}, ValidationError("record ID, library ID, and title are required")
	}
	if year < 0 {
		return unmatchedCreateReservation{}, ValidationError("year must be non-negative")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return unmatchedCreateReservation{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return unmatchedCreateReservation{}, err
	}
	defer tx.Rollback()

	var (
		sourceHandle    string
		attachedMediaID sql.NullString
		resolvedAt      sql.NullInt64
		resolvedMediaID sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT source_handle, media_id, resolved_at_ms, resolved_media_id
		FROM unmatched_record WHERE id = ?
	`, recordID).Scan(&sourceHandle, &attachedMediaID, &resolvedAt, &resolvedMediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return unmatchedCreateReservation{}, ErrUnmatchedRecordNotFound
	}
	if err != nil {
		return unmatchedCreateReservation{}, err
	}
	fingerprint, err := unmatchedCreateFingerprint(libraryID, sourceHandle, title, year, artworkDigest)
	if err != nil {
		return unmatchedCreateReservation{}, err
	}
	state, stateFound, err := loadUnmatchedCreateState(ctx, tx, recordID)
	if err != nil {
		return unmatchedCreateReservation{}, err
	}
	if stateFound {
		if state.Fingerprint != fingerprint {
			return unmatchedCreateReservation{}, ErrUnmatchedCreateConflict
		}
		if resolvedAt.Valid && (!resolvedMediaID.Valid || state.MediaID != MediaID(resolvedMediaID.String)) {
			return unmatchedCreateReservation{}, ErrUnmatchedCreateConflict
		}
		if !resolvedAt.Valid && resolvedMediaID.Valid {
			return unmatchedCreateReservation{}, ErrUnmatchedCreateConflict
		}
		if !resolvedAt.Valid && (!attachedMediaID.Valid || state.MediaID != MediaID(attachedMediaID.String)) {
			return unmatchedCreateReservation{}, ErrUnmatchedCreateConflict
		}
		var one int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM media WHERE id = ?", state.MediaID).Scan(&one); err != nil {
			return unmatchedCreateReservation{}, fmt.Errorf("reserved unmatched Media is unavailable: %w", err)
		}
		return unmatchedCreateReservation{
			MediaID: state.MediaID, fingerprint: state.Fingerprint, finalized: resolvedAt.Valid,
		}, nil
	}
	if resolvedAt.Valid || resolvedMediaID.Valid {
		return unmatchedCreateReservation{}, ErrUnmatchedCreateConflict
	}

	manual := ManualMedia{
		LibraryID: libraryID,
		Path:      sourceHandle,
		Title:     title,
		Year:      year,
		Container: "mkv",
		Codec:     "h264",
		Class:     MediaClassVideo,
	}
	mediaID := manualMediaID(libraryID, sourceHandle, title)
	created, err := createManualMediaTx(ctx, tx, mediaID, manual, now)
	if err != nil {
		return unmatchedCreateReservation{}, err
	}
	if !created {
		var (
			existingLibrary LibraryID
			existingTitle   string
			existingYear    int
		)
		if err := tx.QueryRowContext(ctx, `
			SELECT library_id, title, year FROM media WHERE id = ?
		`, mediaID).Scan(&existingLibrary, &existingTitle, &existingYear); err != nil {
			return unmatchedCreateReservation{}, err
		}
		if existingLibrary != libraryID || existingTitle != title || existingYear != year {
			return unmatchedCreateReservation{}, ErrUnmatchedCreateConflict
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE unmatched_record
		SET library_id = ?, media_id = ?
		WHERE id = ? AND resolved_at_ms IS NULL
	`, libraryID, mediaID, recordID)
	if err != nil {
		return unmatchedCreateReservation{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return unmatchedCreateReservation{}, err
	}
	if affected != 1 {
		return unmatchedCreateReservation{}, ErrUnmatchedCreateConflict
	}
	state = unmatchedCreateState{Fingerprint: fingerprint, MediaID: mediaID}
	rawState, err := json.Marshal(state)
	if err != nil {
		return unmatchedCreateReservation{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, ?, ?)
	`, unmatchedCreateStatePrefix+recordID, string(rawState), timeToMillis(now)); err != nil {
		return unmatchedCreateReservation{}, err
	}
	if err := tx.Commit(); err != nil {
		return unmatchedCreateReservation{}, err
	}
	if created {
		s.appendManualMediaCreatedEvent(ctx, mediaID, now)
	}
	return unmatchedCreateReservation{MediaID: mediaID, fingerprint: fingerprint}, nil
}

// finalizeManualMediaFromUnmatched resolves a record only for its durable
// reservation. Repeating a completed finalization is a safe no-op.
func (s *Store) finalizeManualMediaFromUnmatched(
	ctx context.Context,
	recordID string,
	reservation unmatchedCreateReservation,
	now time.Time,
) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if recordID == "" || reservation.MediaID == "" || reservation.fingerprint == "" {
		return ValidationError("record ID and create reservation are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var (
		attachedMediaID sql.NullString
		resolvedAt      sql.NullInt64
		resolvedMediaID sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT media_id, resolved_at_ms, resolved_media_id
		FROM unmatched_record WHERE id = ?
	`, recordID).Scan(&attachedMediaID, &resolvedAt, &resolvedMediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnmatchedRecordNotFound
	}
	if err != nil {
		return err
	}
	state, stateFound, err := loadUnmatchedCreateState(ctx, tx, recordID)
	if err != nil {
		return err
	}
	if !stateFound || state.Fingerprint != reservation.fingerprint || state.MediaID != reservation.MediaID {
		return ErrUnmatchedCreateConflict
	}
	if resolvedAt.Valid {
		if !resolvedMediaID.Valid || MediaID(resolvedMediaID.String) != reservation.MediaID {
			return ErrUnmatchedCreateConflict
		}
		return nil
	}
	if resolvedMediaID.Valid || !attachedMediaID.Valid || MediaID(attachedMediaID.String) != reservation.MediaID {
		return ErrUnmatchedCreateConflict
	}
	var one int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM media WHERE id = ?", reservation.MediaID).Scan(&one); err != nil {
		return fmt.Errorf("reserved unmatched Media is unavailable: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE unmatched_record
		SET resolved_at_ms = ?, resolved_media_id = ?
		WHERE id = ? AND resolved_at_ms IS NULL
	`, timeToMillis(now), reservation.MediaID, recordID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrUnmatchedCreateConflict
	}
	return tx.Commit()
}

func unmatchedCreateFingerprint(libraryID LibraryID, sourceHandle, title string, year int, artworkDigest string) (string, error) {
	payload, err := json.Marshal(struct {
		LibraryID     LibraryID `json:"libraryID"`
		SourceHandle  string    `json:"sourceHandle"`
		Title         string    `json:"title"`
		Year          int       `json:"year"`
		ArtworkDigest string    `json:"artworkDigest"`
	}{
		LibraryID:     libraryID,
		SourceHandle:  sourceHandle,
		Title:         title,
		Year:          year,
		ArtworkDigest: artworkDigest,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func loadUnmatchedCreateState(ctx context.Context, tx *sql.Tx, recordID string) (unmatchedCreateState, bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx,
		"SELECT value FROM server_config WHERE key = ?",
		unmatchedCreateStatePrefix+recordID,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return unmatchedCreateState{}, false, nil
	}
	if err != nil {
		return unmatchedCreateState{}, false, err
	}
	var state unmatchedCreateState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return unmatchedCreateState{}, false, fmt.Errorf("decode unmatched create state: %w", err)
	}
	if state.Fingerprint == "" || state.MediaID == "" {
		return unmatchedCreateState{}, false, errors.New("invalid unmatched create state")
	}
	return state, true, nil
}

func (s *Store) appendManualMediaCreatedEvent(ctx context.Context, mediaID MediaID, now time.Time) {
	if eventErr := s.appendMediaEventLocked(ctx, MediaEventCreated, mediaID, nil, now); eventErr != nil {
		// The Media is already persisted; a failed outbox append must not fail the
		// create (the same posture CreateProvisionalMedia takes). Log it so the
		// resulting catalog/outbox divergence is visible to the operator.
		slog.Warn("media outbox event append failed", "kind", MediaEventCreated, "media", mediaID, "error", eventErr)
	}
}

func (s *Store) createManualMediaSQL(ctx context.Context, mediaID MediaID, m ManualMedia, now time.Time) (bool, error) {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := createManualMediaTx(ctx, tx, mediaID, m, now)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

func createManualMediaTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, m ManualMedia, now time.Time) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM library WHERE id = ?", m.LibraryID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errors.New("library not found")
	}
	if err != nil {
		return false, err
	}
	// Idempotent on the derived ID: a retry must neither fork the Media nor
	// clobber an identity the operator has since corrected.
	result, err := tx.ExecContext(ctx, `
		INSERT INTO media(id, library_id, title, year, duration_seconds, container, codec, media_class, title_identified, mutated_by)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?, 1, ?)
		ON CONFLICT(id) DO NOTHING
	`, mediaID, m.LibraryID, m.Title, m.Year, m.Container, m.Codec, m.Class, MutationSourceManual)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if m.Path != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_file(id, content_fingerprint, media_id, path, last_seen_at_ms)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(content_fingerprint) DO NOTHING
		`, manualFingerprint(mediaID), manualFingerprint(mediaID), mediaID, m.Path, timeToMillis(now)); err != nil {
			return false, err
		}
	}
	return affected == 1, nil
}
