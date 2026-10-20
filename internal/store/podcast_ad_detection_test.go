package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestAudioAdMediaCursorIsAudioOnlyToggleGatedAndRelationIndependent(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	audioA := importDetectionMedia(t, st, "cursor-a", MediaClassAudio)
	audioB := importDetectionMedia(t, st, "cursor-b", MediaClassAudio)
	_ = importDetectionMedia(t, st, "cursor-video", MediaClassVideo)

	if got, err := st.AudioAdMediaAfter(ctx, "", 10); err != nil || len(got) != 0 {
		t.Fatalf("disabled cursor = %+v, %v; want empty", got, err)
	}
	if err := st.SetSkipDetectionEnabled(ctx, "library-local", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `DELETE FROM entity_relation`); err != nil {
		t.Fatal(err)
	}

	ids := []MediaID{audioA.ID, audioB.ID}
	slices.Sort(ids)
	first, err := st.AudioAdMediaAfter(ctx, "", 1)
	if err != nil || len(first) != 1 || first[0].ID != ids[0] {
		t.Fatalf("first cursor = %+v, %v; want %s", first, err, ids[0])
	}
	second, err := st.AudioAdMediaAfter(ctx, first[0].ID, 10)
	if err != nil || len(second) != 1 || second[0].ID != ids[1] {
		t.Fatalf("second cursor = %+v, %v; want %s", second, err, ids[1])
	}
	item, ok, err := st.AudioAdMediaByID(ctx, ids[1])
	if err != nil || !ok || item.ID != ids[1] || item.Path == "" || item.DurationSeconds != 120 {
		t.Fatalf("by ID = %+v, %v, %v", item, ok, err)
	}
	if err := st.SetSkipDetectionEnabled(ctx, "library-local", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.AudioAdMediaByID(ctx, ids[1]); err != nil || ok {
		t.Fatalf("disabled by ID = %v, %v; want false", ok, err)
	}
}

func importDetectionMedia(t *testing.T, st *Store, title string, class MediaClass) Media {
	t.Helper()
	path := filepath.Join(t.TempDir(), title+".m4a")
	if err := os.WriteFile(path, []byte(title), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: title,
		Container: "m4a", Codec: "aac", Class: class,
		ContentFingerprint: fingerprint, DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	return media
}

func TestApplyDetectionRunZeroCandidateAndBoundaryAdmission(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	base := detectionRunFixture("run-zero", "media-alpha", detectionIdentityFixture("cal-1"))

	assertDetectionEligibility(t, st, base, true)
	if err := st.ApplyDetectionRun(ctx, base); err != nil {
		t.Fatalf("ApplyDetectionRun zero candidate: %v", err)
	}
	assertDetectionEligibility(t, st, base, false)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM detection_run WHERE run_id = ?`, base.RunID)
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM segment_detection WHERE detection_run_id = ?`, base.RunID)
	assertDetectionProvenance(t, st, base)
	assertLateCandidateRejected(t, st, base, 0)
	for _, identity := range detectionIdentityVariants(base.Identity) {
		variant := base
		variant.Identity = identity
		assertDetectionEligibility(t, st, variant, true)
	}

	changed := detectionRunFixture("run-candidates", "media-alpha", detectionIdentityFixture("cal-2"))
	changed.CompletedAt = base.CompletedAt.Add(time.Second)
	changed.Candidates = []DetectionCandidate{
		{
			SegmentKind: SegmentKindAd, StartSeconds: 10, EndSeconds: 40,
			Flavor: FlavorInserted, ClassConfidence: 0.01,
			BoundaryScore: 0.99, BoundaryEligible: true,
		},
		{
			SegmentKind: SegmentKindAd, StartSeconds: 50, EndSeconds: 80,
			Flavor: FlavorInserted, ClassConfidence: 0.99,
			BoundaryScore: 0.2, BoundaryEligible: false,
		},
		{
			SegmentKind: SegmentKindAd, StartSeconds: 90, EndSeconds: 120,
			Flavor: FlavorHostRead, ClassConfidence: 0.95,
			BoundaryScore: 0.1, BoundaryEligible: false,
		},
	}
	assertDetectionEligibility(t, st, changed, true)
	if err := st.ApplyDetectionRun(ctx, changed); err != nil {
		t.Fatalf("ApplyDetectionRun candidates: %v", err)
	}
	assertCatalogCount(t, st, 3, `SELECT COUNT(*) FROM segment_detection WHERE detection_run_id = ?`, changed.RunID)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM segment_detection WHERE detection_run_id = ? AND served = 1`, changed.RunID)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, changed.RunID)
	assertCatalogCount(t, st, 1, `
		SELECT COUNT(*) FROM segment_detection
		WHERE detection_run_id = ? AND confidence = 0.01
		  AND boundary_score = 0.99 AND boundary_eligible = 1 AND served = 1
	`, changed.RunID)
	assertCatalogCount(t, st, 1, `
		SELECT COUNT(*) FROM segment_detection
		WHERE detection_run_id = ? AND confidence = 0.99
		  AND boundary_eligible = 0 AND served = 0
	`, changed.RunID)
	assertCatalogCount(t, st, 1, `
		SELECT COUNT(*) FROM segment_detection
		WHERE detection_run_id = ? AND flavor = 'host-read' AND served = 0
	`, changed.RunID)
	assertCatalogCount(t, st, 1, `
		SELECT COUNT(*) FROM detection_run_lifecycle
		WHERE event_type = 'supersede' AND run_id = ? AND replacement_run_id = ?
	`, base.RunID, changed.RunID)
	assertLateCandidateRejected(t, st, changed, len(changed.Candidates))
}

func TestApplyDetectionRunRejectsOutOfOrderCompletion(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	newer := detectionRunFixture("run-newer", "media-alpha", detectionIdentityFixture("cal-newer"))
	newer.CompletedAt = newer.CompletedAt.Add(time.Minute)
	newer.Candidates = []DetectionCandidate{insertedCandidate(10, 40)}
	if err := st.ApplyDetectionRun(ctx, newer); err != nil {
		t.Fatalf("ApplyDetectionRun newer: %v", err)
	}
	older := detectionRunFixture("run-older", "media-alpha", detectionIdentityFixture("cal-older"))
	older.Candidates = []DetectionCandidate{insertedCandidate(50, 80)}
	if err := st.ApplyDetectionRun(ctx, older); !errors.Is(err, ErrDetectionRunStale) {
		t.Fatalf("ApplyDetectionRun older = %v, want ErrDetectionRunStale", err)
	}
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, newer.RunID)
	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM detection_run WHERE run_id = ?`, older.RunID)
}

func TestApplyDetectionRunScopesSupersessionByMediaAndMethod(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	first := detectionRunFixture("run-first", "media-alpha", detectionIdentityFixture("cal-a"))
	first.Candidates = []DetectionCandidate{insertedCandidate(10, 40)}
	otherMedia := detectionRunFixture("run-other-media", "media-archive", detectionIdentityFixture("cal-a"))
	otherMedia.Candidates = []DetectionCandidate{insertedCandidate(20, 50)}
	otherMethod := detectionRunFixture("run-other-method", "media-alpha", detectionIdentityFixture("cal-a"))
	otherMethod.DetectorMethod = "chapter-boundary-model"
	otherMethod.Candidates = []DetectionCandidate{{
		SegmentKind: "intro", StartSeconds: 0, EndSeconds: 8,
		Flavor: FlavorNone, ClassConfidence: 0.8,
		BoundaryScore: 0.9, BoundaryEligible: true,
	}}
	for _, run := range []DetectionRun{first, otherMedia, otherMethod} {
		if err := st.ApplyDetectionRun(ctx, run); err != nil {
			t.Fatalf("ApplyDetectionRun %s: %v", run.RunID, err)
		}
	}
	replacement := detectionRunFixture("run-replacement", "media-alpha", detectionIdentityFixture("cal-b"))
	replacement.CompletedAt = first.CompletedAt.Add(time.Second)
	replacement.Candidates = []DetectionCandidate{insertedCandidate(60, 90)}
	if err := st.ApplyDetectionRun(ctx, replacement); err != nil {
		t.Fatalf("ApplyDetectionRun replacement: %v", err)
	}

	assertCatalogCount(t, st, 0, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, first.RunID)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, replacement.RunID)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, otherMedia.RunID)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, otherMethod.RunID)
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM segment_detection WHERE detection_run_id = ?`, first.RunID)
	if err := st.ReplaceDetectedMarkers(ctx, first.MediaID, first.DetectorMethod, nil,
		LibraryFilter{Revealed: true, AllLibraries: true}, replacement.CompletedAt); err == nil {
		t.Fatal("ReplaceDetectedMarkers bypassed lifecycle-managed markers")
	}
	assertCatalogCount(t, st, 1, `SELECT COUNT(*) FROM marker WHERE detection_run_id = ?`, replacement.RunID)
	assertCatalogCount(t, st, 1, `
		SELECT COUNT(*) FROM detection_run_lifecycle
		WHERE event_type = 'supersede' AND run_id = ? AND replacement_run_id = ?
	`, first.RunID, replacement.RunID)
	assertCatalogCount(t, st, 0, `
		SELECT COUNT(*) FROM detection_run_lifecycle
		WHERE event_type = 'supersede' AND run_id IN (?, ?)
	`, otherMedia.RunID, otherMethod.RunID)
}

func TestDetectionCandidateImmutabilityAllowsAuditedMediaPurge(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	run := detectionRunFixture("run-purge", "media-alpha", detectionIdentityFixture("cal-purge"))
	run.Candidates = []DetectionCandidate{insertedCandidate(10, 40)}
	if err := st.ApplyDetectionRun(ctx, run); err != nil {
		t.Fatalf("ApplyDetectionRun: %v", err)
	}
	if err := st.TrashMedia(ctx, run.MediaID, LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatalf("TrashMedia: %v", err)
	}
	purged, err := st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || purged != 1 {
		t.Fatalf("PurgeTrashedBefore = %d, %v; want 1, nil", purged, err)
	}
	assertCatalogCount(t, st, 0,
		`SELECT COUNT(*) FROM segment_detection WHERE detection_run_id = ?`, run.RunID)
	assertCatalogCount(t, st, 0,
		`SELECT COUNT(*) FROM detection_run WHERE run_id = ?`, run.RunID)
}

func TestDetectionRunValidationPrecedesWrites(t *testing.T) {
	st := openSeeded(t)
	base := detectionRunFixture("run-invalid", "media-alpha", detectionIdentityFixture("cal-invalid"))
	cases := []DetectionCandidate{
		{
			SegmentKind: SegmentKindAd, StartSeconds: 1, EndSeconds: 2,
			Flavor: FlavorInserted, ClassConfidence: math.NaN(), BoundaryScore: 1,
			BoundaryEligible: true,
		},
		{
			SegmentKind: SegmentKindAd, StartSeconds: 1, EndSeconds: 2,
			Flavor: FlavorHostRead, ClassConfidence: 1, BoundaryScore: 1,
			BoundaryEligible: true,
		},
	}
	for i, candidate := range cases {
		run := base
		run.RunID += string(rune('a' + i))
		run.Candidates = []DetectionCandidate{candidate}
		if err := st.ApplyDetectionRun(context.Background(), run); err == nil {
			t.Fatalf("invalid candidate %d was accepted", i)
		}
	}
	assertCatalogCount(t, st, 0,
		`SELECT COUNT(*) FROM detection_run WHERE run_id LIKE 'run-invalid%'`)
}

func detectionIdentityFixture(calibrationVersion string) DetectionIdentity {
	return DetectionIdentity{
		ModelID:            "podcast-ad",
		ModelVersion:       "1",
		ModelHash:          "model-hash-1",
		ArtifactIdentity:   "artifact-1",
		FeatureSetVersion:  "features-1",
		CalibrationID:      "boundary-holdout",
		CalibrationVersion: calibrationVersion,
		CalibrationHash:    "calibration-hash-" + calibrationVersion,
	}
}

func detectionRunFixture(runID string, mediaID MediaID, identity DetectionIdentity) DetectionRun {
	return DetectionRun{
		RunID:          runID,
		MediaID:        mediaID,
		DetectorMethod: AudioAdDetectorMethod,
		Identity:       identity,
		SignalTier:     "audio",
		CompletedAt:    time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
	}
}

func insertedCandidate(start, end float64) DetectionCandidate {
	return DetectionCandidate{
		SegmentKind: SegmentKindAd, StartSeconds: start, EndSeconds: end,
		Flavor: FlavorInserted, ClassConfidence: 0.9,
		BoundaryScore: 0.95, BoundaryEligible: true,
	}
}

func assertDetectionEligibility(t *testing.T, st *Store, run DetectionRun, want bool) {
	t.Helper()
	got, err := st.DetectionRunEligible(context.Background(), run.MediaID, run.DetectorMethod, run.Identity)
	if err != nil {
		t.Fatalf("DetectionRunEligible: %v", err)
	}
	if got != want {
		t.Fatalf("DetectionRunEligible(%s) = %v, want %v", run.RunID, got, want)
	}
}

func assertCatalogCount(t *testing.T, st *Store, want int, query string, args ...any) {
	t.Helper()
	var got int
	if err := st.catalogR.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("catalog count: %v", err)
	}
	if got != want {
		t.Fatalf("catalog count = %d, want %d for %s", got, want, query)
	}
}

func TestDetectionLifecycleErrorsAreStable(t *testing.T) {
	st := openSeeded(t)
	_, err := st.UndoDetectionRevocation(context.Background(), "missing", time.Now())
	if !errors.Is(err, ErrDetectionLifecycleNotFound) {
		t.Fatalf("UndoDetectionRevocation error = %v", err)
	}
}

func TestAudioAdDetectionMigrationUpgradesCurrentCatalog(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	before := migrationsBefore(t, catalogMigrations, "migrate/catalog", "0100")
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", before,
		"migrate/catalog", filepath.Join(t.TempDir(), "before")); err != nil {
		t.Fatalf("migrate through 0099: %v", err)
	}
	nowMS := time.Now().UTC().UnixMilli()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO library(id, title) VALUES ('library-old', 'Old');
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec)
		VALUES ('media-old', 'library-old', 'Old episode', 600, 'mp4', 'aac');
		INSERT INTO segment_detection(
			id, media_id, segment_kind, start_seconds, end_seconds,
			method, flavor, confidence, created_at_ms
		) VALUES ('detection-old', 'media-old', 'ad', 10, 40,
		          'podcast-ad-audio', 'host-read', 0.7, ?);
	`, nowMS); err != nil {
		t.Fatalf("seed 0099 rows: %v", err)
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", catalogMigrations,
		"migrate/catalog", filepath.Join(t.TempDir(), "after")); err != nil {
		t.Fatalf("migrate through 0108: %v", err)
	}

	var runID sql.NullString
	var boundary, eligible, served int
	if err := db.QueryRowContext(ctx, `
		SELECT detection_run_id, boundary_score, boundary_eligible, served
		FROM segment_detection WHERE id = 'detection-old'
	`).Scan(&runID, &boundary, &eligible, &served); err != nil {
		t.Fatalf("read upgraded row: %v", err)
	}
	if runID.Valid || boundary != 0 || eligible != 0 || served != 0 {
		t.Fatalf("upgraded row = run:%v boundary:%d eligible:%d served:%d", runID, boundary, eligible, served)
	}
	for _, table := range []string{"detection_run", "detection_run_lifecycle"} {
		if !tableExists(t, db, table) {
			t.Fatalf("%s missing after 0108", table)
		}
	}
}

func TestDetectionLifecycleSchemaRejectsInvalidTransitions(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	first := detectionRunFixture("run-transition-a", "media-alpha", detectionIdentityFixture("cal-a"))
	second := detectionRunFixture("run-transition-b", "media-archive", detectionIdentityFixture("cal-b"))
	for _, run := range []DetectionRun{first, second} {
		if err := st.ApplyDetectionRun(ctx, run); err != nil {
			t.Fatalf("ApplyDetectionRun %s: %v", run.RunID, err)
		}
	}
	nowMS := time.Now().UTC().UnixMilli()
	statements := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "self supersession",
			sql: `INSERT INTO detection_run_lifecycle(
				event_id, event_type, run_id, replacement_run_id, occurred_at_ms
			) VALUES ('bad-self', 'supersede', ?, ?, ?)`,
			args: []any{first.RunID, first.RunID, nowMS},
		},
		{
			name: "cross-media supersession",
			sql: `INSERT INTO detection_run_lifecycle(
				event_id, event_type, run_id, replacement_run_id, occurred_at_ms
			) VALUES ('bad-cross', 'supersede', ?, ?, ?)`,
			args: []any{first.RunID, second.RunID, nowMS},
		},
		{
			name: "undo non-revocation",
			sql: `INSERT INTO detection_run_lifecycle(
				event_id, event_type, target_event_id, occurred_at_ms
			) VALUES ('bad-undo', 'undo', ?, ?)`,
			args: []any{"missing", nowMS},
		},
	}
	for _, statement := range statements {
		if _, err := st.catalogW.ExecContext(ctx, statement.sql, statement.args...); err == nil {
			t.Fatalf("schema accepted %s", statement.name)
		}
	}

	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO detection_run(
			run_id, media_id, detector_method, model_id, model_version, model_hash,
			artifact_identity, feature_set_version, calibration_id, calibration_version,
			calibration_hash, signal_tier, candidate_count, completion_outcome, completed_at_ms
		) VALUES (
			'run-contradiction', 'media-alpha', 'podcast-ad-audio', 'model', '1', 'hash',
			'artifact', 'features', 'calibration', '1', 'calibration-hash',
			'audio', 1, 'completed', ?
		)
	`, nowMS); err != nil {
		t.Fatalf("seed contradiction receipt: %v", err)
	}
	for _, evidence := range []struct {
		name             string
		flavor           string
		boundaryEligible int
	}{
		{name: "ineligible served candidate", flavor: "inserted", boundaryEligible: 0},
		{name: "host-read served candidate", flavor: "host-read", boundaryEligible: 1},
	} {
		if _, err := st.catalogW.ExecContext(ctx, `
			INSERT INTO segment_detection(
				id, media_id, segment_kind, start_seconds, end_seconds, method, flavor,
				confidence, created_at_ms, detection_run_id, candidate_ordinal,
				boundary_score, boundary_eligible, served
			) VALUES (?, 'media-alpha', 'ad', 10, 40, 'podcast-ad-audio', ?,
			          0.9, ?, 'run-contradiction', 0, 0.9, ?, 1)
		`, "bad-"+evidence.name, evidence.flavor, nowMS, evidence.boundaryEligible); err == nil {
			t.Fatalf("schema accepted %s", evidence.name)
		}
	}
}

func assertDetectionProvenance(t *testing.T, st *Store, run DetectionRun) {
	t.Helper()
	var got DetectionRun
	var completedAtMS int64
	var candidateCount int
	if err := st.catalogR.QueryRowContext(context.Background(), `
		SELECT run_id, media_id, detector_method,
		       model_id, model_version, model_hash, artifact_identity, feature_set_version,
		       calibration_id, calibration_version, calibration_hash,
		       signal_tier, candidate_count, completed_at_ms
		FROM detection_run WHERE run_id = ?
	`, run.RunID).Scan(
		&got.RunID, &got.MediaID, &got.DetectorMethod,
		&got.Identity.ModelID, &got.Identity.ModelVersion, &got.Identity.ModelHash,
		&got.Identity.ArtifactIdentity, &got.Identity.FeatureSetVersion,
		&got.Identity.CalibrationID, &got.Identity.CalibrationVersion, &got.Identity.CalibrationHash,
		&got.SignalTier, &candidateCount, &completedAtMS,
	); err != nil {
		t.Fatalf("read detection provenance: %v", err)
	}
	if got.RunID != run.RunID || got.MediaID != run.MediaID || got.DetectorMethod != run.DetectorMethod ||
		got.Identity != run.Identity || got.SignalTier != run.SignalTier ||
		candidateCount != len(run.Candidates) || completedAtMS != run.CompletedAt.UnixMilli() {
		t.Fatalf("detection provenance = %+v count=%d completed=%d, want %+v", got, candidateCount, completedAtMS, run)
	}
}

func assertLateCandidateRejected(t *testing.T, st *Store, run DetectionRun, ordinal int) {
	t.Helper()
	if _, err := st.catalogW.ExecContext(context.Background(), `
		INSERT INTO segment_detection(
			id, media_id, segment_kind, start_seconds, end_seconds, method, flavor,
			confidence, created_at_ms, detection_run_id, candidate_ordinal,
			boundary_score, boundary_eligible, served
		) VALUES (?, ?, 'ad', 1, 2, ?, 'inserted', 0.9, ?, ?, ?, 0.9, 1, 1)
	`, "late-"+run.RunID, run.MediaID, run.DetectorMethod, run.CompletedAt.UnixMilli(), run.RunID, ordinal); err == nil {
		t.Fatalf("completed receipt %s accepted late candidate %d", run.RunID, ordinal)
	}
}

func detectionIdentityVariants(identity DetectionIdentity) []DetectionIdentity {
	variants := make([]DetectionIdentity, 8)
	for i := range variants {
		variants[i] = identity
	}
	variants[0].ModelID += "-changed"
	variants[1].ModelVersion += "-changed"
	variants[2].ModelHash += "-changed"
	variants[3].ArtifactIdentity += "-changed"
	variants[4].FeatureSetVersion += "-changed"
	variants[5].CalibrationID += "-changed"
	variants[6].CalibrationVersion += "-changed"
	variants[7].CalibrationHash += "-changed"
	return variants
}
