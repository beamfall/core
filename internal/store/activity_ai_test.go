package store

import (
	"context"
	"testing"
	"time"
)

func TestDetectionRunLinkAndStaleTransitionGuards(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	first := detectionRunFixture("run-link-a", "media-alpha", detectionIdentityFixture("link-a"))
	first.Candidates = []DetectionCandidate{insertedCandidate(10, 40)}
	other := detectionRunFixture("run-link-b", "media-archive", detectionIdentityFixture("link-b"))
	other.Candidates = []DetectionCandidate{insertedCandidate(20, 50)}
	for _, run := range []DetectionRun{first, other} {
		if err := st.ApplyDetectionRun(ctx, run); err != nil {
			t.Fatalf("ApplyDetectionRun %s: %v", run.RunID, err)
		}
	}
	nowMS := time.Now().UTC().UnixMilli()
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO segment_detection(
			id, media_id, segment_kind, start_seconds, end_seconds, method, flavor,
			confidence, created_at_ms, detection_run_id, candidate_ordinal,
			boundary_score, boundary_eligible, served
		) VALUES ('bad-candidate-link', 'media-archive', 'ad', 1, 2,
		          'podcast-ad-audio', 'inserted', 1, ?, ?, 9, 1, 1, 1)
	`, nowMS, first.RunID); err == nil {
		t.Fatal("schema accepted candidate linked across Media")
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO marker(
			id, media_id, title, start_seconds, end_seconds, segment_kind,
			provenance, method, confidence, flavor, created_at_ms, updated_at_ms,
			detection_run_id, detection_candidate_ordinal
		) VALUES ('bad-marker-link', 'media-alpha', '', 11, 40, 'ad',
		          'detected', 'podcast-ad-audio', 0.9, 'inserted', ?, ?, ?, 0)
	`, nowMS, nowMS, first.RunID); err == nil {
		t.Fatal("schema accepted marker that did not match its served candidate")
	}

	replacement := detectionRunFixture("run-link-replacement", "media-alpha", detectionIdentityFixture("link-c"))
	replacement.CompletedAt = first.CompletedAt.Add(time.Second)
	if err := st.ApplyDetectionRun(ctx, replacement); err != nil {
		t.Fatalf("ApplyDetectionRun replacement: %v", err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO detection_run_lifecycle(
			event_id, event_type, run_id, replacement_run_id, occurred_at_ms
		) VALUES ('bad-stale-supersede', 'supersede', ?, ?, ?)
	`, first.RunID, replacement.RunID, nowMS); err == nil {
		t.Fatal("schema accepted supersession from an already-invalidated run")
	}
}

func TestDetectionRunUpdateGuardsPreserveManualPromotion(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	run := detectionRunFixture("run-update-guard", "media-alpha", detectionIdentityFixture("update"))
	run.Candidates = []DetectionCandidate{insertedCandidate(10, 40)}
	if err := st.ApplyDetectionRun(ctx, run); err != nil {
		t.Fatalf("ApplyDetectionRun: %v", err)
	}
	nowMS := time.Now().UTC().UnixMilli()
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO segment_detection(
			id, media_id, segment_kind, start_seconds, end_seconds,
			method, flavor, confidence, created_at_ms
		) VALUES ('legacy-candidate', 'media-alpha', 'ad', 50, 60,
		          'podcast-ad-audio', 'inserted', 0.8, ?)
	`, nowMS); err != nil {
		t.Fatalf("seed legacy candidate: %v", err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		UPDATE segment_detection
		SET detection_run_id = ?, candidate_ordinal = 9
		WHERE id = 'legacy-candidate'
	`, run.RunID); err == nil {
		t.Fatal("schema attached legacy candidate to immutable receipt")
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		UPDATE marker SET start_seconds = 11 WHERE detection_run_id = ?
	`, run.RunID); err == nil {
		t.Fatal("schema mutated linked detected marker")
	}

	markers, err := st.MarkersForMedia(ctx, run.MediaID, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil || len(markers) != 1 {
		t.Fatalf("MarkersForMedia = %d, %v", len(markers), err)
	}
	end := 42.0
	promoted, err := st.UpdateMarker(ctx, Marker{
		ID: markers[0].ID, SegmentKind: SegmentKindAd,
		StartSeconds: 12, EndSeconds: &end,
	}, LibraryFilter{Revealed: true, AllLibraries: true}, time.Now().UTC())
	if err != nil {
		t.Fatalf("UpdateMarker manual promotion: %v", err)
	}
	if promoted.Provenance != ProvenanceManual {
		t.Fatalf("promoted provenance = %q", promoted.Provenance)
	}
	assertCatalogCount(t, st, 1, `
		SELECT COUNT(*) FROM marker
		WHERE id = ? AND provenance = 'manual' AND detection_run_id IS NULL
		  AND detection_candidate_ordinal IS NULL
	`, promoted.ID)
}
