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
	if ok, err := s.mediaExistsSQL(ctx, mediaID); err != nil {
		return err
	} else if !ok {
		return ValidationError("media not found")
	}
	_, err := s.catalogW.ExecContext(ctx, `
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
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := markArtworkCandidateAppliedSQL(ctx, tx, candidateID); err != nil {
		return err
	}
	return tx.Commit()
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
	applied, err := s.artworkCandidateApplied(ctx, candidateID)
	if err != nil {
		return err
	}
	if applied {
		return nil
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
	tx, err := s.catalogW.BeginTx(ctx, nil)
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
		restoreErr := s.restoreArtworkCandidateApplyState(
			context.WithoutCancel(ctx), mediaID, slot, candidateID, prior,
		)
		return errors.Join(err, rollbackErr, restoreErr, rollbackArtworkCandidate(rollbackArtwork))
	}
	if err := appendActivitySQL(ctx, s.eventsW, activity); err != nil {
		restoreErr := s.restoreArtworkCandidateApplyState(
			context.WithoutCancel(ctx), mediaID, slot, candidateID, prior,
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

func (s *Store) artworkCandidateApplied(ctx context.Context, candidateID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var applied int
	err := s.catalogR.QueryRowContext(ctx, `SELECT applied FROM artwork_candidate WHERE id = ?`, candidateID).Scan(&applied)
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

func (s *Store) restoreArtworkCandidateApplyState(
	ctx context.Context,
	mediaID MediaID,
	slot, candidateID string,
	state artworkCandidateApplyState,
) error {
	tx, err := s.catalogW.BeginTx(ctx, nil)
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
	if _, err := s.visibleMediaSQL(ctx, mediaID, filter); err != nil {
		return []ArtworkCandidate{}, nil
	}
	rows, err := s.catalogR.QueryContext(ctx, `
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
	candidate, err := scanArtworkCandidate(s.catalogR.QueryRowContext(ctx, `
		SELECT id, media_id, slot, source_url, plugin_id, scraper_id, applied, created_at_ms
		FROM artwork_candidate
		WHERE id = ?`, strings.TrimSpace(candidateID)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ArtworkCandidate{}, ValidationError("artwork candidate not found")
		}
		return ArtworkCandidate{}, err
	}
	if _, err := s.visibleMediaSQL(ctx, candidate.MediaID, filter); err != nil {
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
	rows, err := s.catalogR.QueryContext(ctx, `SELECT id FROM artwork_candidate`)
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
