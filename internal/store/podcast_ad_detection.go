package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	AudioAdDetectorMethod     = "pod" + "cast-ad-audio"
	detectionEventSupersede   = "supersede"
	detectionEventRevoke      = "revoke"
	detectionEventUndo        = "undo"
	detectionEventResetToAuto = "reset-to-auto"
	activeRunInvalidationSQL  = `NOT EXISTS (
		SELECT 1
		FROM detection_run_lifecycle invalidation
		WHERE (
			(invalidation.run_id = detection_run.run_id
				AND invalidation.event_type IN ('supersede', 'reset-to-auto'))
			OR
			(invalidation.event_type = 'revoke'
				AND invalidation.model_id = detection_run.model_id
				AND invalidation.model_version = detection_run.model_version
				AND invalidation.model_hash = detection_run.model_hash)
		)
		AND NOT EXISTS (
			SELECT 1
			FROM detection_run_lifecycle undo
			WHERE undo.event_type = 'undo'
			  AND undo.target_event_id = invalidation.event_id
		)
	)`
	currentRunInvalidationSQL = `NOT EXISTS (
		SELECT 1
		FROM detection_run_lifecycle invalidation
		WHERE invalidation.run_id = detection_run.run_id
		  AND invalidation.event_type IN ('supersede', 'reset-to-auto')
		  AND NOT EXISTS (
			SELECT 1
			FROM detection_run_lifecycle undo
			WHERE undo.event_type = 'undo'
			  AND undo.target_event_id = invalidation.event_id
		  )
	)`
)

// AudioAdMedia is the complete byte-level input needed by one audio
// detection job. It deliberately carries no relation or container data: the
// detector is per-Media and never waits for a group to stabilize.
type AudioAdMedia struct {
	ID              MediaID
	Path            string
	DurationSeconds int
}

// AudioAdMediaAfter returns enabled audio Media in stable keyset order.
// The per-Library toggle, live primary-file requirement, and audio-only v1
// scope are applied in SQL so a disabled or ineligible Library creates no
// child jobs. Detection receipts are intentionally checked by the caller
// against its complete artifact identity.
func (s *Store) AudioAdMediaAfter(ctx context.Context, after MediaID, limit int) ([]AudioAdMedia, error) {
	if limit <= 0 {
		return nil, ValidationError("limit must be positive")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, audioAdMediaSelect+`
		AND (? = '' OR media.id > ?)
		ORDER BY media.id
		LIMIT ?
	`, skipDetectionLibraryKeyPrefix, string(after), string(after), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	media := make([]AudioAdMedia, 0, limit)
	for rows.Next() {
		var item AudioAdMedia
		if err := rows.Scan(&item.ID, &item.Path, &item.DurationSeconds); err != nil {
			return nil, err
		}
		media = append(media, item)
	}
	return media, rows.Err()
}

// AudioAdMediaByID rechecks the same eligibility boundary immediately
// before inference. A toggle change or file disappearance between arm and
// claim therefore degrades to an inert completed job rather than stale work.
func (s *Store) AudioAdMediaByID(ctx context.Context, id MediaID) (AudioAdMedia, bool, error) {
	if id == "" {
		return AudioAdMedia{}, false, ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return AudioAdMedia{}, false, err
	}
	var item AudioAdMedia
	err := s.catalogR.QueryRowContext(ctx, audioAdMediaSelect+`
		AND media.id = ?
	`, skipDetectionLibraryKeyPrefix, id).Scan(&item.ID, &item.Path, &item.DurationSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return AudioAdMedia{}, false, nil
	}
	return item, err == nil, err
}

const audioAdMediaSelect = `
	SELECT media.id,
	       (
	         SELECT media_file.path
	         FROM media_file
	         WHERE media_file.media_id = media.id
	           AND media_file.missing_at_ms IS NULL
	         ORDER BY media_file.version_label, media_file.part_ordinal
	         LIMIT 1
	       ),
	       media.duration_seconds
	FROM media
	JOIN server_config
	  ON server_config.key = ? || media.library_id
	 AND server_config.value = '1'
	WHERE media.media_class = 'audio'
	  AND media.duration_seconds > 0
	  AND media.trashed_at_ms IS NULL
	  AND EXISTS (
	    SELECT 1 FROM media_file
	    WHERE media_file.media_id = media.id
	      AND media_file.missing_at_ms IS NULL
	  )
`

// DetectionRunEligible reports whether this exact complete identity lacks an
// active completed receipt. Inactive superseded, reset, or revoked receipts do
// not suppress a replacement run.
func (s *Store) DetectionRunEligible(ctx context.Context, mediaID MediaID, method string, identity DetectionIdentity) (bool, error) {
	method = strings.TrimSpace(method)
	if mediaID == "" {
		return false, ValidationError("media ID is required")
	}
	if method == "" {
		return false, ValidationError("detector method is required")
	}
	if err := validateDetectionIdentity(&identity); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var eligible bool
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT
		  NOT EXISTS (
		    SELECT 1
		    FROM detection_run_lifecycle revoke
		    WHERE revoke.event_type = 'revoke'
		      AND revoke.model_id = ? AND revoke.model_version = ? AND revoke.model_hash = ?
		      AND NOT EXISTS (
		        SELECT 1 FROM detection_run_lifecycle undo
		        WHERE undo.event_type = 'undo' AND undo.target_event_id = revoke.event_id
		      )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM detection_run
		    WHERE media_id = ? AND detector_method = ?
		      AND model_id = ? AND model_version = ? AND model_hash = ?
		      AND artifact_identity = ? AND feature_set_version = ?
		      AND calibration_id = ? AND calibration_version = ? AND calibration_hash = ?
		      AND `+activeRunInvalidationSQL+`
		  )
	`, identity.ModelID, identity.ModelVersion, identity.ModelHash,
		mediaID, method, identity.ModelID, identity.ModelVersion, identity.ModelHash,
		identity.ArtifactIdentity, identity.FeatureSetVersion, identity.CalibrationID,
		identity.CalibrationVersion, identity.CalibrationHash).Scan(&eligible)
	return eligible, err
}

// ApplyDetectionRun validates everything before writing, then atomically
// appends the receipt, supersession, candidate evidence, and admitted markers.
func (s *Store) ApplyDetectionRun(ctx context.Context, run DetectionRun) error {
	if err := validateDetectionRun(&run); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireDetectionMediaTx(ctx, tx, run.MediaID); err != nil {
		return err
	}
	revoked, err := modelRevokedSQL(ctx, tx, DetectionModelIdentity{
		ModelID: run.Identity.ModelID, ModelVersion: run.Identity.ModelVersion, ModelHash: run.Identity.ModelHash,
	})
	if err != nil {
		return err
	}
	if revoked {
		return ErrDetectionModelAlreadyRevoked
	}
	completed, err := exactActiveRunExistsTx(ctx, tx, run)
	if err != nil {
		return err
	}
	if completed {
		return ErrDetectionRunAlreadyCompleted
	}
	priorRunID, priorCompletedAtMS, err := currentRunTx(ctx, tx, run.MediaID, run.DetectorMethod)
	if err != nil {
		return err
	}
	if priorRunID != "" && detectionRunNotNewer(run, priorRunID, priorCompletedAtMS) {
		return ErrDetectionRunStale
	}
	if err := insertDetectionRunTx(ctx, tx, run); err != nil {
		return err
	}
	if priorRunID != "" {
		if err := appendRunEventTx(ctx, tx, detectionEventSupersede, priorRunID, run.RunID, run.CompletedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM marker WHERE detection_run_id = ? AND provenance = 'detected'`,
			priorRunID); err != nil {
			return err
		}
	}
	blocked := make(map[string]bool)
	for ordinal, candidate := range run.Candidates {
		isBlocked, ok := blocked[candidate.SegmentKind]
		if !ok {
			isBlocked, err = candidateBlockedTx(ctx, tx, run.MediaID, candidate.SegmentKind)
			if err != nil {
				return err
			}
			blocked[candidate.SegmentKind] = isBlocked
		}
		served := candidate.BoundaryEligible && candidate.Flavor != FlavorHostRead && !isBlocked
		if err := insertDetectionCandidateTx(ctx, tx, run, ordinal, candidate, served); err != nil {
			return err
		}
		if served {
			if err := insertDetectionMarkerTx(ctx, tx, run, ordinal, candidate); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// RevokeDetectionModel appends one model-scoped lifecycle event and removes
// only detected markers originating from that exact model version/hash.
func (s *Store) RevokeDetectionModel(ctx context.Context, model DetectionModelIdentity, now time.Time) (string, error) {
	if err := validateDetectionModelIdentity(&model); err != nil {
		return "", err
	}
	if now.IsZero() {
		return "", ValidationError("revocation time is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	revoked, err := modelRevokedSQL(ctx, tx, model)
	if err != nil {
		return "", err
	}
	if revoked {
		return "", ErrDetectionModelAlreadyRevoked
	}
	eventID, err := randomDetectionID("detection-event-")
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO detection_run_lifecycle(
			event_id, event_type, model_id, model_version, model_hash, occurred_at_ms
		) VALUES (?, 'revoke', ?, ?, ?, ?)
	`, eventID, model.ModelID, model.ModelVersion, model.ModelHash, now.UTC().UnixMilli()); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM marker
		WHERE provenance = 'detected'
		  AND detection_run_id IN (
			SELECT run_id FROM detection_run
			WHERE model_id = ? AND model_version = ? AND model_hash = ?
		  )
	`, model.ModelID, model.ModelVersion, model.ModelHash); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return eventID, nil
}

// UndoDetectionRevocation appends an undo event, then restores only candidates
// that were actually served from still-current runs and remain unblocked by
// manual ownership or suppression.
func (s *Store) UndoDetectionRevocation(ctx context.Context, revokeEventID string, now time.Time) (string, error) {
	revokeEventID = strings.TrimSpace(revokeEventID)
	if revokeEventID == "" {
		return "", ValidationError("revocation event ID is required")
	}
	if now.IsZero() {
		return "", ValidationError("undo time is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	model, err := activeRevocationModelTx(ctx, tx, revokeEventID)
	if err != nil {
		return "", err
	}
	eventID, err := randomDetectionID("detection-event-")
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO detection_run_lifecycle(event_id, event_type, target_event_id, occurred_at_ms)
		VALUES (?, 'undo', ?, ?)
	`, eventID, revokeEventID, now.UTC().UnixMilli()); err != nil {
		return "", err
	}
	candidates, err := restorableCandidatesTx(ctx, tx, model)
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		blocked, err := candidateBlockedTx(ctx, tx, candidate.run.MediaID, candidate.candidate.SegmentKind)
		if err != nil {
			return "", err
		}
		if blocked {
			continue
		}
		if err := insertDetectionMarkerTx(ctx, tx, candidate.run, candidate.ordinal, candidate.candidate); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return eventID, nil
}

func requireDetectionMediaTx(ctx context.Context, tx *sql.Tx, mediaID MediaID) error {
	var one int
	err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM media WHERE id = ? AND trashed_at_ms IS NULL`, mediaID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMarkerNotFound
	}
	return err
}

func exactActiveRunExistsTx(ctx context.Context, tx *sql.Tx, run DetectionRun) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM detection_run
		WHERE media_id = ? AND detector_method = ?
		  AND model_id = ? AND model_version = ? AND model_hash = ?
		  AND artifact_identity = ? AND feature_set_version = ?
		  AND calibration_id = ? AND calibration_version = ? AND calibration_hash = ?
		  AND `+activeRunInvalidationSQL+`
		LIMIT 1
	`, run.MediaID, run.DetectorMethod, run.Identity.ModelID, run.Identity.ModelVersion,
		run.Identity.ModelHash, run.Identity.ArtifactIdentity, run.Identity.FeatureSetVersion,
		run.Identity.CalibrationID, run.Identity.CalibrationVersion,
		run.Identity.CalibrationHash).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func currentRunTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, method string) (string, int64, error) {
	var runID string
	var completedAtMS int64
	err := tx.QueryRowContext(ctx, `
		SELECT run_id, completed_at_ms FROM detection_run
		WHERE media_id = ? AND detector_method = ?
		  AND `+currentRunInvalidationSQL+`
		ORDER BY completed_at_ms DESC, run_id DESC LIMIT 1
	`, mediaID, method).Scan(&runID, &completedAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return runID, completedAtMS, err
}

func detectionRunNotNewer(run DetectionRun, priorRunID string, priorCompletedAtMS int64) bool {
	completedAtMS := run.CompletedAt.UnixMilli()
	if completedAtMS != priorCompletedAtMS {
		return completedAtMS < priorCompletedAtMS
	}
	return run.RunID <= priorRunID
}

func insertDetectionRunTx(ctx context.Context, tx *sql.Tx, run DetectionRun) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO detection_run(
			run_id, media_id, detector_method,
			model_id, model_version, model_hash, artifact_identity,
			feature_set_version, calibration_id, calibration_version, calibration_hash,
			signal_tier, candidate_count, completion_outcome, completed_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.RunID, run.MediaID, run.DetectorMethod,
		run.Identity.ModelID, run.Identity.ModelVersion, run.Identity.ModelHash,
		run.Identity.ArtifactIdentity, run.Identity.FeatureSetVersion,
		run.Identity.CalibrationID, run.Identity.CalibrationVersion, run.Identity.CalibrationHash,
		run.SignalTier, len(run.Candidates), DetectionOutcomeCompleted, run.CompletedAt.UnixMilli())
	return err
}

func appendRunEventTx(ctx context.Context, tx *sql.Tx, eventType, runID, replacementRunID string, now time.Time) error {
	eventID, err := randomDetectionID("detection-event-")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO detection_run_lifecycle(
			event_id, event_type, run_id, replacement_run_id, occurred_at_ms
		) VALUES (?, ?, ?, NULLIF(?, ''), ?)
	`, eventID, eventType, runID, replacementRunID, now.UTC().UnixMilli())
	return err
}

func candidateBlockedTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, kind string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM marker
		WHERE media_id = ? AND segment_kind = ? AND provenance = 'manual'
	`, mediaID, kind).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM marker_suppression
		WHERE media_id = ? AND segment_kind = ?
	`, mediaID, kind).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func insertDetectionCandidateTx(ctx context.Context, tx *sql.Tx, run DetectionRun, ordinal int, candidate DetectionCandidate, served bool) error {
	id := deterministicDetectionID("detection-", run.RunID, ordinal)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO segment_detection(
			id, media_id, segment_kind, start_seconds, end_seconds, method, flavor,
			confidence, created_at_ms, detection_run_id, candidate_ordinal,
			boundary_score, boundary_eligible, served
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, run.MediaID, candidate.SegmentKind, candidate.StartSeconds,
		candidate.EndSeconds, run.DetectorMethod, string(candidate.Flavor),
		candidate.ClassConfidence, run.CompletedAt.UnixMilli(), run.RunID, ordinal,
		candidate.BoundaryScore, boolInt(candidate.BoundaryEligible), boolInt(served))
	return err
}

func insertDetectionMarkerTx(ctx context.Context, tx *sql.Tx, run DetectionRun, ordinal int, candidate DetectionCandidate) error {
	id := deterministicDetectionID("marker-detection-", run.RunID, ordinal)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO marker(
			id, media_id, title, start_seconds, end_seconds, segment_kind,
			provenance, method, confidence, flavor, created_by_profile_id,
			created_at_ms, updated_at_ms, detection_run_id, detection_candidate_ordinal
		) VALUES (?, ?, '', ?, ?, ?, 'detected', ?, ?, ?, NULL, ?, ?, ?, ?)
	`, id, run.MediaID, candidate.StartSeconds, candidate.EndSeconds,
		candidate.SegmentKind, run.DetectorMethod, candidate.ClassConfidence,
		string(candidate.Flavor), run.CompletedAt.UnixMilli(), run.CompletedAt.UnixMilli(),
		run.RunID, ordinal)
	return err
}

type detectionQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func modelRevokedSQL(ctx context.Context, db detectionQueryRower, model DetectionModelIdentity) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, `
		SELECT 1
		FROM detection_run_lifecycle revoke
		WHERE revoke.event_type = 'revoke'
		  AND revoke.model_id = ? AND revoke.model_version = ? AND revoke.model_hash = ?
		  AND NOT EXISTS (
			SELECT 1 FROM detection_run_lifecycle undo
			WHERE undo.event_type = 'undo' AND undo.target_event_id = revoke.event_id
		  )
		LIMIT 1
	`, model.ModelID, model.ModelVersion, model.ModelHash).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func activeRevocationModelTx(ctx context.Context, tx *sql.Tx, eventID string) (DetectionModelIdentity, error) {
	var model DetectionModelIdentity
	err := tx.QueryRowContext(ctx, `
		SELECT model_id, model_version, model_hash
		FROM detection_run_lifecycle revoke
		WHERE revoke.event_id = ? AND revoke.event_type = 'revoke'
		  AND NOT EXISTS (
			SELECT 1 FROM detection_run_lifecycle undo
			WHERE undo.event_type = 'undo' AND undo.target_event_id = revoke.event_id
		  )
	`, eventID).Scan(&model.ModelID, &model.ModelVersion, &model.ModelHash)
	if errors.Is(err, sql.ErrNoRows) {
		return DetectionModelIdentity{}, ErrDetectionLifecycleNotFound
	}
	return model, err
}

type restorableDetectionCandidate struct {
	run       DetectionRun
	ordinal   int
	candidate DetectionCandidate
}

func restorableCandidatesTx(ctx context.Context, tx *sql.Tx, model DetectionModelIdentity) ([]restorableDetectionCandidate, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT detection_run.run_id, detection_run.media_id, detection_run.detector_method,
		       detection_run.completed_at_ms,
		       segment_detection.candidate_ordinal, segment_detection.segment_kind,
		       segment_detection.start_seconds, segment_detection.end_seconds,
		       segment_detection.flavor, segment_detection.confidence,
		       segment_detection.boundary_score
		FROM detection_run
		JOIN segment_detection
		  ON segment_detection.detection_run_id = detection_run.run_id
		WHERE detection_run.model_id = ?
		  AND detection_run.model_version = ?
		  AND detection_run.model_hash = ?
		  AND segment_detection.served = 1
		  AND `+activeRunInvalidationSQL+`
		ORDER BY detection_run.completed_at_ms, detection_run.run_id,
		         segment_detection.candidate_ordinal
	`, model.ModelID, model.ModelVersion, model.ModelHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []restorableDetectionCandidate
	for rows.Next() {
		var item restorableDetectionCandidate
		var completedAtMS int64
		var flavor string
		if err := rows.Scan(&item.run.RunID, &item.run.MediaID, &item.run.DetectorMethod,
			&completedAtMS, &item.ordinal, &item.candidate.SegmentKind,
			&item.candidate.StartSeconds, &item.candidate.EndSeconds,
			&flavor, &item.candidate.ClassConfidence,
			&item.candidate.BoundaryScore); err != nil {
			return nil, err
		}
		item.run.CompletedAt = time.UnixMilli(completedAtMS).UTC()
		item.candidate.Flavor = SegmentFlavor(flavor)
		item.candidate.BoundaryEligible = true
		candidates = append(candidates, item)
	}
	return candidates, rows.Err()
}

func resetDetectionRunTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, method string, now time.Time) error {
	runID, _, err := currentRunTx(ctx, tx, mediaID, method)
	if err != nil || runID == "" {
		return err
	}
	if err := appendRunEventTx(ctx, tx, detectionEventResetToAuto, runID, "", now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`DELETE FROM marker WHERE detection_run_id = ? AND provenance = 'detected'`, runID)
	return err
}

func deterministicDetectionID(prefix, runID string, ordinal int) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + strconv.Itoa(ordinal)))
	return prefix + hex.EncodeToString(sum[:16])
}

func randomDetectionID(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw), nil
}
