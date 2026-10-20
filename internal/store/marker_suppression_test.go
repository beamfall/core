package store

// Coverage for the suppression / reset-to-auto seam that moved out of
// markers.go into marker_suppression.go: manual and suppressed ownership beats
// a detection run, a failed apply rolls the whole catalog mutation back, and
// model revocation, undo, and reset-to-auto stay isolated from manual Markers.

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApplyDetectionRunManualSuppressionAndAtomicRollback(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO marker(
			id, media_id, title, start_seconds, end_seconds, segment_kind,
			provenance, method, confidence, flavor, created_at_ms, updated_at_ms
		) VALUES ('manual-ad', 'media-alpha', '', 1, 2, 'ad',
		          'manual', '', 0, '', ?, ?)
	`, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed manual ad: %v", err)
	}
	manual := detectionRunFixture("run-manual", "media-alpha", detectionIdentityFixture("cal-manual"))
	manual.Candidates = []DetectionCandidate{insertedCandidate(10, 40)}
	if err := st.ApplyDetectionRun(ctx, manual); err != nil {
		t.Fatalf("ApplyDetectionRun manual ownership: %v", err)
	}
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM segment_detection WHERE detection_run_id = ?`, manual.RunID)
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, manual.RunID)

	if err := st.SuppressSegment(ctx, "media-archive", SegmentKindAd, now); err != nil {
		t.Fatalf("SuppressSegment: %v", err)
	}
	suppressed := detectionRunFixture("run-suppressed", "media-archive", detectionIdentityFixture("cal-suppressed"))
	suppressed.Candidates = []DetectionCandidate{insertedCandidate(10, 40)}
	if err := st.ApplyDetectionRun(ctx, suppressed); err != nil {
		t.Fatalf("ApplyDetectionRun suppressed: %v", err)
	}
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM segment_detection WHERE detection_run_id = ?`, suppressed.RunID)
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, suppressed.RunID)

	failed := detectionRunFixture("run-rollback", "media-archive", detectionIdentityFixture("cal-fail"))
	failed.CompletedAt = suppressed.CompletedAt.Add(time.Second)
	failed.Candidates = []DetectionCandidate{insertedCandidate(50, 80)}
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER fail_detection_candidate
		BEFORE INSERT ON segment_detection
		WHEN NEW.detection_run_id = 'run-rollback'
		BEGIN SELECT RAISE(ABORT, 'fixture failure'); END
	`); err != nil {
		t.Fatalf("install rollback trigger: %v", err)
	}
	if err := st.ApplyDetectionRun(ctx, failed); err == nil {
		t.Fatal("ApplyDetectionRun succeeded despite candidate failure")
	}
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM detection_run WHERE run_id = ?`, failed.RunID)
	assertCatalogCount(t, st, 0, `
		SELECT COUNT(*) FROM detection_run_lifecycle WHERE replacement_run_id = ?
	`, failed.RunID)
	assertCatalogCount(t, st, 1, `
		SELECT COUNT(*) FROM detection_run
		WHERE run_id = ? AND completion_outcome = 'completed'
	`, suppressed.RunID)

	prior := detectionRunFixture("run-rollback-prior", "media-alpha", detectionIdentityFixture("cal-prior"))
	prior.CompletedAt = failed.CompletedAt.Add(time.Second)
	prior.Candidates = []DetectionCandidate{{
		SegmentKind: "intro", StartSeconds: 10, EndSeconds: 40,
		Flavor: FlavorNone, ClassConfidence: 0.9,
		BoundaryScore: 0.95, BoundaryEligible: true,
	}}
	if err := st.ApplyDetectionRun(ctx, prior); err != nil {
		t.Fatalf("ApplyDetectionRun rollback prior: %v", err)
	}
	replacement := detectionRunFixture("run-rollback-marker", "media-alpha", detectionIdentityFixture("cal-replacement"))
	replacement.CompletedAt = prior.CompletedAt.Add(time.Second)
	replacement.Candidates = []DetectionCandidate{{
		SegmentKind: "intro", StartSeconds: 50, EndSeconds: 80,
		Flavor: FlavorNone, ClassConfidence: 0.9,
		BoundaryScore: 0.95, BoundaryEligible: true,
	}}
	if _, err := st.catalogW.ExecContext(ctx, `
		DROP TRIGGER fail_detection_candidate;
		CREATE TRIGGER fail_detection_candidate
		BEFORE INSERT ON segment_detection
		WHEN NEW.detection_run_id = 'run-rollback-marker'
		BEGIN SELECT RAISE(ABORT, 'fixture failure'); END
	`); err != nil {
		t.Fatalf("replace rollback trigger: %v", err)
	}
	if err := st.ApplyDetectionRun(ctx, replacement); err == nil {
		t.Fatal("ApplyDetectionRun succeeded despite replacement candidate failure")
	}
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, prior.RunID)
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM detection_run WHERE run_id = ?`, replacement.RunID)
}

func TestApplyDetectionRunMarkerConflictRollsBack(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	prior := detectionRunFixture("run-conflict-prior", "media-alpha", detectionIdentityFixture("conflict-prior"))
	prior.Candidates = []DetectionCandidate{{
		SegmentKind: "intro", StartSeconds: 10, EndSeconds: 40,
		Flavor: FlavorNone, ClassConfidence: 0.9,
		BoundaryScore: 0.95, BoundaryEligible: true,
	}}
	if err := st.ApplyDetectionRun(ctx, prior); err != nil {
		t.Fatalf("ApplyDetectionRun prior: %v", err)
	}

	replacement := detectionRunFixture("run-marker-conflict", "media-alpha", detectionIdentityFixture("conflict-replacement"))
	replacement.CompletedAt = prior.CompletedAt.Add(time.Second)
	replacement.Candidates = []DetectionCandidate{{
		SegmentKind: "intro", StartSeconds: 50, EndSeconds: 80,
		Flavor: FlavorNone, ClassConfidence: 0.9,
		BoundaryScore: 0.95, BoundaryEligible: true,
	}}
	markerID := deterministicDetectionID("marker-detection-", replacement.RunID, 0)
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO marker(
			id, media_id, title, start_seconds, end_seconds, segment_kind,
			provenance, method, confidence, flavor, created_at_ms, updated_at_ms
		) VALUES (?, 'media-alpha', '', 90, 100, 'credits',
		          'manual', '', 0, '', ?, ?)
	`, markerID, replacement.CompletedAt.UnixMilli(), replacement.CompletedAt.UnixMilli()); err != nil {
		t.Fatalf("seed conflicting manual marker: %v", err)
	}

	if err := st.ApplyDetectionRun(ctx, replacement); err == nil {
		t.Fatal("ApplyDetectionRun ignored a failed served-marker insert")
	}
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, prior.RunID)
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM detection_run WHERE run_id = ?`, replacement.RunID)
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM segment_detection WHERE detection_run_id = ?`, replacement.RunID)
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM detection_run_lifecycle WHERE replacement_run_id = ?`, replacement.RunID)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE id = ? AND provenance = 'manual'`, markerID)
}

func TestDetectionModelRevokeUndoAndResetIsolation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	modelA := detectionIdentityFixture("cal-a")
	modelB := detectionIdentityFixture("cal-b")
	modelB.ModelVersion = "2"
	modelB.ModelHash = "model-hash-2"

	runA := detectionRunFixture("run-model-a", "media-alpha", modelA)
	runA.Candidates = []DetectionCandidate{insertedCandidate(10, 40)}
	runB := detectionRunFixture("run-model-b", "media-archive", modelB)
	runB.Candidates = []DetectionCandidate{insertedCandidate(20, 50)}
	for _, run := range []DetectionRun{runA, runB} {
		if err := st.ApplyDetectionRun(ctx, run); err != nil {
			t.Fatalf("ApplyDetectionRun %s: %v", run.RunID, err)
		}
	}
	revokeModel := DetectionModelIdentity{
		ModelID: modelA.ModelID, ModelVersion: modelA.ModelVersion, ModelHash: modelA.ModelHash,
	}
	revokeID, err := st.RevokeDetectionModel(ctx, revokeModel, now)
	if err != nil {
		t.Fatalf("RevokeDetectionModel: %v", err)
	}
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, runA.RunID)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, runB.RunID)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM segment_detection WHERE detection_run_id = ?`, runA.RunID)
	assertDetectionEligibility(t, st, runA, false)
	rejected := runA
	rejected.RunID = "run-revoked-rejected"
	rejected.CompletedAt = runA.CompletedAt.Add(time.Second)
	if err := st.ApplyDetectionRun(ctx, rejected); !errors.Is(err, ErrDetectionModelAlreadyRevoked) {
		t.Fatalf("ApplyDetectionRun revoked identity = %v", err)
	}
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM detection_run WHERE run_id = ?`, rejected.RunID)

	undoID, err := st.UndoDetectionRevocation(ctx, revokeID, now.Add(time.Second))
	if err != nil {
		t.Fatalf("UndoDetectionRevocation: %v", err)
	}
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, runA.RunID)
	assertCatalogCount(t, st, 1, `
		SELECT COUNT(*) FROM detection_run_lifecycle
		WHERE event_id = ? AND event_type = 'undo' AND target_event_id = ?
	`, undoID, revokeID)

	secondRevoke, err := st.RevokeDetectionModel(ctx, revokeModel, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("second RevokeDetectionModel: %v", err)
	}
	if err := st.SuppressSegment(ctx, runA.MediaID, SegmentKindAd, now.Add(3*time.Second)); err != nil {
		t.Fatalf("SuppressSegment after revoke: %v", err)
	}
	if _, err := st.UndoDetectionRevocation(ctx, secondRevoke, now.Add(4*time.Second)); err != nil {
		t.Fatalf("suppressed UndoDetectionRevocation: %v", err)
	}
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, runA.RunID)

	if err := st.ResetSegmentToAuto(ctx, runA.MediaID, SegmentKindAd); err != nil {
		t.Fatalf("ResetSegmentToAuto: %v", err)
	}
	assertDetectionEligibility(t, st, runA, true)
	assertCatalogCount(t, st, 1, `
		SELECT COUNT(*) FROM detection_run_lifecycle
		WHERE event_type = 'reset-to-auto' AND run_id = ?
	`, runA.RunID)

	if _, err := st.catalogW.ExecContext(ctx,
		`UPDATE detection_run SET signal_tier = 'rewritten' WHERE run_id = ?`, runA.RunID); err == nil {
		t.Fatal("immutable receipt accepted UPDATE")
	}
	if _, err := st.catalogW.ExecContext(ctx,
		`DELETE FROM detection_run_lifecycle WHERE event_id = ?`, revokeID); err == nil {
		t.Fatal("append-only lifecycle accepted DELETE")
	}
	if _, err := st.catalogW.ExecContext(ctx,
		`UPDATE detection_run_lifecycle SET occurred_at_ms = occurred_at_ms + 1 WHERE event_id = ?`, secondRevoke); err == nil {
		t.Fatal("append-only lifecycle accepted UPDATE")
	}
	if _, err := st.catalogW.ExecContext(ctx,
		`UPDATE segment_detection SET served = 0 WHERE detection_run_id = ?`, runA.RunID); err == nil {
		t.Fatal("immutable candidate evidence accepted UPDATE")
	}
	if _, err := st.catalogW.ExecContext(ctx,
		`DELETE FROM segment_detection WHERE detection_run_id = ?`, runA.RunID); err == nil {
		t.Fatal("immutable candidate evidence accepted DELETE")
	}
}
