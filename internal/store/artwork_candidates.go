package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

var artworkCandidateMutationMu sync.Mutex

// ArtworkCandidateID derives the stable candidate identity:
// sha256(media_id|slot|source_url), with NUL separators to avoid ambiguous
// concatenations while preserving the ADR's logical tuple.
func ArtworkCandidateID(mediaID MediaID, slot, sourceURL string) string {
	sum := sha256.Sum256([]byte(string(mediaID) + "\x00" + slot + "\x00" + sourceURL))
	return hex.EncodeToString(sum[:])
}

// UpsertArtworkCandidate keeps one scraped source URL for a Media artwork
// slot. It is additive and idempotent: later scrapes never remove earlier
// candidates the user may have curated.
func (s *Store) UpsertArtworkCandidate(ctx context.Context, mediaID MediaID, slot, sourceURL, pluginID, scraperID string) error {
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	slot = strings.TrimSpace(slot)
	sourceURL = strings.TrimSpace(sourceURL)
	if slot == "" {
		return ValidationError("artwork slot is required")
	}
	if sourceURL == "" {
		return ValidationError("artwork source URL is required")
	}
	artworkCandidateMutationMu.Lock()
	defer artworkCandidateMutationMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	// A candidate's source URL names what the Media is, so it lives on the
	// Media's own plane; a sealed Encrypted Library's Media is not found.
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return err
	}
	_, err = plane.writer.ExecContext(ctx, `
		INSERT OR IGNORE INTO artwork_candidate(id, media_id, slot, source_url, plugin_id, scraper_id, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ArtworkCandidateID(mediaID, slot, sourceURL), mediaID, slot, sourceURL,
		strings.TrimSpace(pluginID), strings.TrimSpace(scraperID), timeToMillis(time.Now().UTC()))
	return err
}

// MarkArtworkCandidateApplied marks one candidate as the materialized image
// for its Media/slot pair and clears every sibling in the same transaction.
func (s *Store) MarkArtworkCandidateApplied(ctx context.Context, candidateID string) error {
	if strings.TrimSpace(candidateID) == "" {
		return ValidationError("artwork candidate ID is required")
	}
	artworkCandidateMutationMu.Lock()
	defer artworkCandidateMutationMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, plane, err := s.findArtworkCandidateLocked(ctx, candidateID)
	if err != nil {
		return err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := markArtworkCandidateAppliedSQL(ctx, tx, candidateID); err != nil {
		return err
	}
	return tx.Commit()
}

// findArtworkCandidateLocked locates a candidate on whichever readable plane
// holds it. Candidate IDs hash the Media ID, so only the owning plane should
// match; private planes are asked first so a stranded shared copy awaiting
// evacuation never shadows the private row.
func (s *Store) findArtworkCandidateLocked(ctx context.Context, candidateID string) (ArtworkCandidate, catalogPlane, error) {
	planes := append(s.registeredEncryptedPlanesLocked(), catalogPlane{writer: s.catalogW, reader: s.catalogR})
	for _, plane := range planes {
		candidate, err := scanArtworkCandidate(plane.reader.QueryRowContext(ctx, `
			SELECT id, media_id, slot, source_url, plugin_id, scraper_id, applied, created_at_ms
			FROM artwork_candidate
			WHERE id = ?`, candidateID))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return ArtworkCandidate{}, catalogPlane{}, err
		}
		return candidate, plane, nil
	}
	return ArtworkCandidate{}, catalogPlane{}, ValidationError("artwork candidate not found")
}

// ArtworkCandidateApply carries the prepared file mutation and audit context
// for one uploaded candidate. The store owns execution so its catalog changes,
// the file callbacks, and the append-only activity row share one failure
// boundary.
type ArtworkCandidateApply struct {
	MediaID   MediaID
	Slot      string
	SourceURL string
	Activity  ActivityEntry
	// ApplyArtwork captures the current file state inside the store's
	// serialized mutation boundary, publishes the replacement, and returns a
	// callback that restores the captured state. The rollback must be usable
	// even when publication returns an error after changing files.
	ApplyArtwork func() (rollback func() error, err error)
}

// ApplyArtworkCandidate is the failure-atomic boundary for an uploaded image.
// The file callback runs before catalog publication and returns its exact
// rollback. If any catalog or append-only activity write fails, both catalog
// and files are restored. The deterministic candidate ID makes an immediate
// retry after a lost response converge without re-importing; each real
// applied-state transition receives its own activity operation ID.
func (s *Store) ApplyArtworkCandidate(ctx context.Context, apply ArtworkCandidateApply) error {
	mediaID := apply.MediaID
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	slot := strings.TrimSpace(apply.Slot)
	sourceURL := strings.TrimSpace(apply.SourceURL)
	if slot == "" {
		return ValidationError("artwork slot is required")
	}
	if sourceURL == "" {
		return ValidationError("artwork source URL is required")
	}
	if apply.ApplyArtwork == nil {
		return ValidationError("artwork apply is required")
	}
	candidateID := ArtworkCandidateID(mediaID, slot, sourceURL)
	activity := apply.Activity
	activity.Action = ActivityArtworkPin
	activity.TargetType = "media"
	activity.TargetID = string(mediaID)
	if activity.OccurredAt.IsZero() {
		activity.OccurredAt = s.now().UTC()
	}
	if err := validateActivityEntry(&activity); err != nil {
		return err
	}

	artworkCandidateMutationMu.Lock()
	defer artworkCandidateMutationMu.Unlock()
	applied, err := s.artworkCandidateApplied(ctx, mediaID, candidateID)
	if err != nil {
		return err
	}
	if applied {
		// A retry, or a re-upload after "revert to provider" unlocked the
		// slot: the upload is still a manual pick, so keep the slot locked.
		return s.lockArtworkSlot(ctx, mediaID, slot)
	}
	operationID, err := newArtworkCandidateApplyOperationID()
	if err != nil {
		return err
	}
	activity.OperationID = operationID
	rollbackArtwork, err := apply.ApplyArtwork()
	if err != nil {
		return errors.Join(err, rollbackArtworkCandidate(rollbackArtwork))
	}
	if rollbackArtwork == nil {
		return ValidationError("artwork rollback is required")
	}

	// ImportArtwork may consult the store, so it runs without s.mu held. From
	// this point through all database writes and compensation, keep a read
	// lease so Close cannot close or nil a handle mid-apply.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return errors.Join(err, rollbackArtworkCandidate(rollbackArtwork))
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return errors.Join(err, rollbackArtworkCandidate(rollbackArtwork))
	}
	if plane.encrypted {
		// The shared activity index keeps only the opaque operation and
		// Library identifiers; the Media ID stays on the private plane.
		activity.TargetID = ""
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(err, rollbackArtworkCandidate(rollbackArtwork))
	}
	defer tx.Rollback()
	prior, err := loadArtworkCandidateApplyState(ctx, tx, mediaID, slot, candidateID)
	if err != nil {
		return errors.Join(err, tx.Rollback(), rollbackArtworkCandidate(rollbackArtwork))
	}
	if prior.candidateApplied {
		return errors.Join(tx.Rollback(), rollbackArtworkCandidate(rollbackArtwork))
	}
	// An upload is a manual pick: lock the slot in the same transaction so an
	// identification, refresh or rescan does not replace it.
	if err := recordFieldProvenanceTx(ctx, tx, mediaID, ArtworkLockField(slot), MutationSourceManual); err != nil {
		return errors.Join(err, tx.Rollback(), rollbackArtworkCandidate(rollbackArtwork))
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO artwork_candidate(id, media_id, slot, source_url, plugin_id, scraper_id, created_at_ms)
		VALUES (?, ?, ?, ?, '', '', ?)`,
		candidateID, mediaID, slot, sourceURL, timeToMillis(s.now().UTC())); err != nil {
		return errors.Join(err, tx.Rollback(), rollbackArtworkCandidate(rollbackArtwork))
	}
	if err := markArtworkCandidateAppliedSQL(ctx, tx, candidateID); err != nil {
		return errors.Join(err, tx.Rollback(), rollbackArtworkCandidate(rollbackArtwork))
	}
	if err := tx.Commit(); err != nil {
		rollbackErr := rollbackArtworkCandidateTransaction(tx)
		restoreErr := restoreArtworkCandidateApplyState(
			context.WithoutCancel(ctx), plane.writer, mediaID, slot, candidateID, prior,
		)
		return errors.Join(err, rollbackErr, restoreErr, rollbackArtworkCandidate(rollbackArtwork))
	}
	if err := appendActivitySQL(ctx, s.eventsW, activity); err != nil {
		restoreErr := restoreArtworkCandidateApplyState(
			context.WithoutCancel(ctx), plane.writer, mediaID, slot, candidateID, prior,
		)
		return errors.Join(err, restoreErr, rollbackArtworkCandidate(rollbackArtwork))
	}
	return nil
}

func newArtworkCandidateApplyOperationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "artwork-apply-" + hex.EncodeToString(raw[:]), nil
}

func (s *Store) artworkCandidateApplied(ctx context.Context, mediaID MediaID, candidateID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return false, err
	}
	var applied int
	err = plane.reader.QueryRowContext(ctx, `SELECT applied FROM artwork_candidate WHERE id = ?`, candidateID).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return applied != 0, err
}

func rollbackArtworkCandidate(rollback func() error) error {
	if rollback == nil {
		return nil
	}
	return rollback()
}

func rollbackArtworkCandidateTransaction(tx *sql.Tx) error {
	err := tx.Rollback()
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

type artworkCandidateApplyState struct {
	candidateExisted bool
	candidateApplied bool
	appliedIDs       []string
	// lockSource is the slot's recorded provenance source before the apply,
	// or "" when it had none.
	lockSource string
}

func loadArtworkCandidateApplyState(
	ctx context.Context,
	tx *sql.Tx,
	mediaID MediaID,
	slot, candidateID string,
) (artworkCandidateApplyState, error) {
	var state artworkCandidateApplyState
	var applied int
	err := tx.QueryRowContext(ctx, `SELECT applied FROM artwork_candidate WHERE id = ?`, candidateID).Scan(&applied)
	switch {
	case err == nil:
		state.candidateExisted = true
		state.candidateApplied = applied != 0
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return artworkCandidateApplyState{}, err
	}
	err = tx.QueryRowContext(ctx,
		`SELECT source FROM media_field_provenance WHERE media_id = ? AND field = ?`,
		mediaID, ArtworkLockField(slot)).Scan(&state.lockSource)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return artworkCandidateApplyState{}, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM artwork_candidate
		WHERE media_id = ? AND slot = ? AND applied = 1`, mediaID, slot)
	if err != nil {
		return artworkCandidateApplyState{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return artworkCandidateApplyState{}, err
		}
		state.appliedIDs = append(state.appliedIDs, id)
	}
	return state, rows.Err()
}

func markArtworkCandidateAppliedSQL(ctx context.Context, tx *sql.Tx, candidateID string) error {
	var mediaID MediaID
	var slot string
	if err := tx.QueryRowContext(ctx,
		`SELECT media_id, slot FROM artwork_candidate WHERE id = ?`, candidateID,
	).Scan(&mediaID, &slot); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ValidationError("artwork candidate not found")
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE artwork_candidate SET applied = 0 WHERE media_id = ? AND slot = ?`, mediaID, slot,
	); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE artwork_candidate SET applied = 1 WHERE id = ?`, candidateID)
	return err
}

func restoreArtworkCandidateApplyState(
	ctx context.Context,
	writer *sql.DB,
	mediaID MediaID,
	slot, candidateID string,
	state artworkCandidateApplyState,
) error {
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE artwork_candidate SET applied = 0 WHERE media_id = ? AND slot = ?`, mediaID, slot,
	); err != nil {
		return err
	}
	for _, appliedID := range state.appliedIDs {
		if _, err := tx.ExecContext(ctx,
			`UPDATE artwork_candidate SET applied = 1 WHERE id = ?`, appliedID,
		); err != nil {
			return err
		}
	}
	if !state.candidateExisted {
		if _, err := tx.ExecContext(ctx, `DELETE FROM artwork_candidate WHERE id = ?`, candidateID); err != nil {
			return err
		}
	}
	lockField := ArtworkLockField(slot)
	if state.lockSource == "" {
		_, err = tx.ExecContext(ctx,
			`DELETE FROM media_field_provenance WHERE media_id = ? AND field = ?`, mediaID, lockField)
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE media_field_provenance SET source = ? WHERE media_id = ? AND field = ?`,
			state.lockSource, mediaID, lockField)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// lockArtworkSlot records a manual source for one Media artwork slot.
func (s *Store) lockArtworkSlot(ctx context.Context, mediaID MediaID, slot string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recordFieldProvenanceTx(ctx, tx, mediaID, ArtworkLockField(slot), MutationSourceManual); err != nil {
		return err
	}
	return tx.Commit()
}

// ArtworkCandidates lists the candidate set for a visible Media/slot pair.
// Hidden and unknown Media both return an empty list so the picker is never an
// existence oracle.
func (s *Store) ArtworkCandidates(ctx context.Context, mediaID MediaID, slot string, filter LibraryFilter) ([]ArtworkCandidate, error) {
	if mediaID == "" || strings.TrimSpace(slot) == "" {
		return []ArtworkCandidate{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return []ArtworkCandidate{}, nil
	}
	if _, err := s.visibleMediaSQLFrom(ctx, plane.reader, mediaID, filter); err != nil {
		return []ArtworkCandidate{}, nil
	}
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT id, media_id, slot, source_url, plugin_id, scraper_id, applied, created_at_ms
		FROM artwork_candidate
		WHERE media_id = ? AND slot = ?
		ORDER BY created_at_ms, id`, mediaID, strings.TrimSpace(slot))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []ArtworkCandidate{}
	for rows.Next() {
		candidate, err := scanArtworkCandidate(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

// ArtworkCandidate returns one candidate only when its owning Media is visible
// to the caller. Hidden and absent candidates fail with the same stable error.
func (s *Store) ArtworkCandidate(ctx context.Context, candidateID string, filter LibraryFilter) (ArtworkCandidate, error) {
	if strings.TrimSpace(candidateID) == "" {
		return ArtworkCandidate{}, ValidationError("artwork candidate not found")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ArtworkCandidate{}, err
	}
	candidate, plane, err := s.findArtworkCandidateLocked(ctx, strings.TrimSpace(candidateID))
	if err != nil {
		return ArtworkCandidate{}, err
	}
	if _, err := s.visibleMediaSQLFrom(ctx, plane.reader, candidate.MediaID, filter); err != nil {
		return ArtworkCandidate{}, ValidationError("artwork candidate not found")
	}
	return candidate, nil
}

// AllArtworkCandidateIDs returns every live candidate ID, unfiltered. The
// retention purge uses this set to reconcile the candidate-preview cache on
// disk against the table — caching keys previews by candidate ID, so an ID
// that survives here is a file that must stay (server-internal callers only).
func (s *Store) AllArtworkCandidateIDs(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	// Open Encrypted Libraries contribute their own candidates. A sealed one's
	// IDs are unreadable, so its cached previews are not protected here.
	var ids []string
	for _, plane := range s.allCatalogPlanesLocked() {
		found, err := artworkCandidateIDsFrom(ctx, plane.reader)
		if err != nil {
			return nil, err
		}
		ids = append(ids, found...)
	}
	return ids, nil
}

func artworkCandidateIDsFrom(ctx context.Context, reader *sql.DB) ([]string, error) {
	rows, err := reader.QueryContext(ctx, `SELECT id FROM artwork_candidate`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type artworkCandidateScanner interface {
	Scan(dest ...any) error
}

func scanArtworkCandidate(scanner artworkCandidateScanner) (ArtworkCandidate, error) {
	var candidate ArtworkCandidate
	var applied int
	var createdAt int64
	if err := scanner.Scan(&candidate.ID, &candidate.MediaID, &candidate.Slot, &candidate.SourceURL,
		&candidate.PluginID, &candidate.ScraperID, &applied, &createdAt); err != nil {
		return ArtworkCandidate{}, err
	}
	candidate.Applied = applied != 0
	candidate.CreatedAt = millisToTime(createdAt)
	return candidate, nil
}

// ArtworkSlotManual reports whether an operator owns a Media artwork slot:
// its lock field carries manual provenance, set by a pick or an upload. A
// curated sidecar must not replace such a slot.
func (s *Store) ArtworkSlotManual(ctx context.Context, mediaID MediaID, slot string) (bool, error) {
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
	var source string
	err = plane.writer.QueryRowContext(ctx,
		`SELECT source FROM media_field_provenance WHERE media_id = ? AND field = ?`,
		mediaID, ArtworkLockField(slot)).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && MutationSource(source) == MutationSourceManual, err
}

// ClearArtworkSlotApplied clears the applied mark of every candidate in one
// Media slot, so a reverted operator upload stops owning it. The candidates
// stay and can be chosen again.
func (s *Store) ClearArtworkSlotApplied(ctx context.Context, mediaID MediaID, slot string) error {
	artworkCandidateMutationMu.Lock()
	defer artworkCandidateMutationMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if errors.Is(err, errPlaneMediaNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = plane.writer.ExecContext(ctx,
		`UPDATE artwork_candidate SET applied = 0 WHERE media_id = ? AND slot = ?`, mediaID, slot)
	return err
}
