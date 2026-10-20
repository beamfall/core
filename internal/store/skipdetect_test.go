package store_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func importVideo(t *testing.T, st *store.Store, title string) store.MediaID {
	t.Helper()
	path := filepath.Join(t.TempDir(), title+".mkv")
	if err := os.WriteFile(path, []byte("v-"+title), 0o600); err != nil {
		t.Fatal(err)
	}
	fp, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: title, Container: "mkv", Codec: "h264",
		Class: store.MediaClassVideo, ContentFingerprint: fp, DurationSecondsHint: 1800,
	})
	if err != nil {
		t.Fatalf("import %s: %v", title, err)
	}
	return media.ID
}

// TestSkipDetectionToggleDefaultOff verifies the per-Library toggle defaults
// OFF and round-trips through the server_config-backed store helper (no
// migration, no new column).
func TestSkipDetectionToggleDefaultOff(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	enabled, err := st.SkipDetectionEnabled(ctx, "library-local")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if enabled {
		t.Fatal("detection toggle must default OFF")
	}
	if err := st.SetSkipDetectionEnabled(ctx, "library-local", true, time.Now().UTC()); err != nil {
		t.Fatalf("enable: %v", err)
	}
	enabled, err = st.SkipDetectionEnabled(ctx, "library-local")
	if err != nil {
		t.Fatalf("read after enable: %v", err)
	}
	if !enabled {
		t.Fatal("toggle did not persist enabled")
	}
	if err := st.SetSkipDetectionEnabled(ctx, "library-missing", true, time.Now().UTC()); err == nil {
		t.Fatal("enabling an unknown Library should error")
	}
}

// TestOrderedSiblingMedia verifies the generic sibling walk: members of one
// ordered container see each other (self excluded), an unordered relation does
// NOT make siblings, and a Media in no ordered container has none.
func TestOrderedSiblingMedia(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	a, b, c := importVideo(t, st, "a"), importVideo(t, st, "b"), importVideo(t, st, "c")
	standalone := importVideo(t, st, "standalone")

	if err := st.UpsertEntity(ctx, store.Entity{ID: "cont", LibraryID: "library-local", Kind: "container", Title: "C"}); err != nil {
		t.Fatal(err)
	}
	for i, m := range []store.MediaID{a, b, c} {
		order := i
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: "cont", Name: "child", ToMediaID: m, SortOrder: &order,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// An unordered relation (no sort_order) to a separate entity must not create
	// siblings — only ordered containers are the comparison group.
	if err := st.UpsertEntity(ctx, store.Entity{ID: "tag", LibraryID: "library-local", Kind: "tag", Title: "T"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "tag", Name: "tagged", ToMediaID: standalone,
	}); err != nil {
		t.Fatal(err)
	}

	sibs, err := st.OrderedSiblingMedia(ctx, a)
	if err != nil {
		t.Fatalf("siblings of a: %v", err)
	}
	if len(sibs) != 2 || sibs[0] != b || sibs[1] != c {
		t.Fatalf("siblings of a = %v, want [%s %s]", sibs, b, c)
	}
	none, err := st.OrderedSiblingMedia(ctx, standalone)
	if err != nil {
		t.Fatalf("siblings of standalone: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("unordered-only Media has siblings: %v", none)
	}
}

func TestPriorOrderedSiblingMedia(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	a, b, c, d := importVideo(t, st, "prior-a"), importVideo(t, st, "prior-b"), importVideo(t, st, "prior-c"), importVideo(t, st, "prior-d")

	if err := st.UpsertEntity(ctx, store.Entity{ID: "ordered", LibraryID: "library-local", Kind: "container", Title: "C"}); err != nil {
		t.Fatal(err)
	}
	for i, m := range []store.MediaID{a, b, c, d} {
		order := i
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: "ordered", Name: "child", ToMediaID: m, SortOrder: &order,
		}); err != nil {
			t.Fatal(err)
		}
	}

	prior, err := st.PriorOrderedSiblingMedia(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(prior, []store.MediaID{a, b}) {
		t.Fatalf("prior siblings = %v, want [%s %s]", prior, a, b)
	}
	first, err := st.PriorOrderedSiblingMedia(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 0 {
		t.Fatalf("first member has prior siblings: %v", first)
	}
}

// TestReplaceDetectedSegmentIdempotentAndManualWins verifies the store write
// primitive directly: idempotent over its own detected output, and a no-op
// (returning false) when a manual segment of the kind exists.
func TestReplaceDetectedSegmentIdempotentAndManualWins(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	id := importVideo(t, st, "seg")
	now := time.Now().UTC()

	wrote, err := st.ReplaceDetectedSegment(ctx, id, "intro", 0, 60, "fingerprint", 0.9, store.FlavorNone, now)
	if err != nil || !wrote {
		t.Fatalf("first detected write: wrote=%v err=%v", wrote, err)
	}
	wrote, err = st.ReplaceDetectedSegment(ctx, id, "intro", 0, 60, "fingerprint", 0.9, store.FlavorNone, now)
	if err != nil || !wrote {
		t.Fatalf("re-run detected write: wrote=%v err=%v", wrote, err)
	}
	markers, err := st.MarkersForMedia(ctx, id, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 1 {
		t.Fatalf("idempotent re-run left %d intro segments, want 1", len(markers))
	}

	// Promote to manual via an edit, then a detected write must stand aside.
	end := 60.0
	if _, err := st.UpdateMarker(ctx, store.Marker{
		ID: markers[0].ID, SegmentKind: "intro", StartSeconds: 0, EndSeconds: &end,
		Provenance: store.ProvenanceManual,
	}, store.LibraryFilter{Revealed: true, AllLibraries: true}, now); err != nil {
		t.Fatalf("promote to manual: %v", err)
	}
	wrote, err = st.ReplaceDetectedSegment(ctx, id, "intro", 5, 70, "fingerprint", 0.95, store.FlavorNone, now)
	if err != nil {
		t.Fatalf("detected-over-manual: %v", err)
	}
	if wrote {
		t.Fatal("detected write clobbered a manual segment")
	}
}

// TestReplaceDetectedSegmentRequiresAdFlavor pins the two-way correspondence
// ADR-0193 Decision 2 draws between the `ad` kind and detection flavor.
func TestReplaceDetectedSegmentRequiresAdFlavor(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	id := importVideo(t, st, "ad-flavor")
	now := time.Now().UTC()

	if _, err := st.ReplaceDetectedSegment(ctx, id, "ad", 30, 90, "podcast-ad-audio", 0.9, store.FlavorNone, now); err == nil {
		t.Fatal("a flavourless `ad` detection was accepted")
	}
	if _, err := st.ReplaceDetectedSegment(ctx, id, "intro", 0, 60, "fingerprint", 0.9, store.FlavorInserted, now); err == nil {
		t.Fatal("a flavored `intro` detection was accepted")
	}
	if _, err := st.ReplaceDetectedSegment(ctx, id, "ad", 30, 90, "podcast-ad-audio", 0.9, store.SegmentFlavor("sponsor"), now); err == nil {
		t.Fatal("an unknown flavor was accepted")
	}
}

// TestReplaceDetectedSegmentRecordsFlavorConfidence proves a served `ad` marker
// records the ADR-0193 canonical method plus its flavor and per-flavor
// confidence. The seam stays method-generic (ADR-0048 chapter-import/fingerprint
// and ADR-0179's classifier share it); what is pinned is the round-trip.
func TestReplaceDetectedSegmentRecordsFlavorConfidence(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	id := importVideo(t, st, "ad-provenance")
	now := time.Now().UTC()
	revealed := store.LibraryFilter{Revealed: true, AllLibraries: true}

	wrote, err := st.ReplaceDetectedSegment(ctx, id, "ad", 30, 90, "podcast-ad-audio", 0.93, store.FlavorInserted, now)
	if err != nil || !wrote {
		t.Fatalf("inserted ad write: wrote=%v err=%v", wrote, err)
	}
	markers, err := st.MarkersForMedia(ctx, id, revealed)
	if err != nil || len(markers) != 1 {
		t.Fatalf("MarkersForMedia: %d markers, err=%v", len(markers), err)
	}
	got := markers[0]
	if got.SegmentKind != "ad" || got.Provenance != store.ProvenanceDetected {
		t.Fatalf("marker = %+v, want a detected ad", got)
	}
	if got.Method != "podcast-ad-audio" || got.Flavor != store.FlavorInserted || got.Confidence != 0.93 {
		t.Fatalf("provenance = method %q flavor %q confidence %v, want podcast-ad-audio/inserted/0.93",
			got.Method, got.Flavor, got.Confidence)
	}
}

// TestReplaceDetectedSegmentDropsHostReadAndClearsPriorOutput is the ADR-0193
// Decision 5 transition proof. A host-read detection is NOT an error at this
// seam — it is the "admit nothing" outcome — and crucially it still performs the
// idempotent replace, so a re-run that reclassifies a previously-`inserted` ad
// leaves no stale, live, auto-skippable marker behind.
func TestReplaceDetectedSegmentDropsHostReadAndClearsPriorOutput(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	id := importVideo(t, st, "ad-reclassified")
	now := time.Now().UTC()
	revealed := store.LibraryFilter{Revealed: true, AllLibraries: true}

	if _, err := st.ReplaceDetectedSegment(ctx, id, "ad", 30, 90, "podcast-ad-audio", 0.93, store.FlavorInserted, now); err != nil {
		t.Fatalf("seed inserted ad: %v", err)
	}
	wrote, err := st.ReplaceDetectedSegment(ctx, id, "ad", 30, 92, "podcast-ad-audio", 0.41, store.FlavorHostRead, now)
	if err != nil {
		t.Fatalf("host-read re-detection returned an error: %v", err)
	}
	if wrote {
		t.Fatal("host-read re-detection reported a written marker; it must surface nothing")
	}
	markers, err := st.MarkersForMedia(ctx, id, revealed)
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 0 {
		t.Fatalf("markers after host-read reclassification = %+v, want none (no stale auto-skippable row)", markers)
	}
}

// TestHostReadDropDoesNotDisturbManualOrSuppression proves the drop path still
// honours the two ADR-0048 Decision 5 guards it shares with a normal write.
func TestHostReadDropDoesNotDisturbManualOrSuppression(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revealed := store.LibraryFilter{Revealed: true, AllLibraries: true}

	// Manual wins: a user-authored ad (born detected, promoted by an edit) stands.
	manualID := importVideo(t, st, "ad-manual")
	if _, err := st.ReplaceDetectedSegment(ctx, manualID, "ad", 30, 90, "podcast-ad-audio", 0.93, store.FlavorInserted, now); err != nil {
		t.Fatalf("seed detected ad: %v", err)
	}
	seeded, err := st.MarkersForMedia(ctx, manualID, revealed)
	if err != nil || len(seeded) != 1 {
		t.Fatalf("seed read back: %d markers, err=%v", len(seeded), err)
	}
	end := 90.0
	if _, err := st.UpdateMarker(ctx, store.Marker{
		ID: seeded[0].ID, SegmentKind: "ad", StartSeconds: 30, EndSeconds: &end,
	}, revealed, now); err != nil {
		t.Fatalf("promote to manual: %v", err)
	}
	if _, err := st.ReplaceDetectedSegment(ctx, manualID, "ad", 30, 92, "podcast-ad-audio", 0.41, store.FlavorHostRead, now); err != nil {
		t.Fatalf("host-read over manual: %v", err)
	}
	if markers, _ := st.MarkersForMedia(ctx, manualID, revealed); len(markers) != 1 || markers[0].Provenance != store.ProvenanceManual {
		t.Fatalf("manual ad after a host-read drop = %+v, want it untouched", markers)
	}

	// Suppression wins: a suppressed (media, ad) is a no-op either way.
	suppressedID := importVideo(t, st, "ad-suppressed")
	if err := st.SuppressSegment(ctx, suppressedID, "ad", now); err != nil {
		t.Fatalf("suppress ad: %v", err)
	}
	if _, err := st.ReplaceDetectedSegment(ctx, suppressedID, "ad", 30, 92, "podcast-ad-audio", 0.41, store.FlavorHostRead, now); err != nil {
		t.Fatalf("host-read over suppression: %v", err)
	}
	if markers, _ := st.MarkersForMedia(ctx, suppressedID, revealed); len(markers) != 0 {
		t.Fatalf("suppressed media gained markers: %+v", markers)
	}
}

// TestSegmentDetectionRecordsBothFlavorsWithoutServingAMarker is the other half
// of ADR-0193 Decision 5: the dropped host-read detection's flavor and
// confidence ARE "recorded for eval" — in segment_detection, which nothing
// serves — while the served marker table stays empty.
func TestSegmentDetectionRecordsBothFlavorsWithoutServingAMarker(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	id := importVideo(t, st, "ad-eval")
	now := time.Now().UTC()

	// Seeded out of start order, to prove the read orders deterministically.
	records := []store.SegmentDetection{
		{MediaID: id, SegmentKind: "ad", StartSeconds: 300, EndSeconds: 330, Method: "podcast-ad-audio", Flavor: store.FlavorHostRead, Confidence: 0.42},
		{MediaID: id, SegmentKind: "ad", StartSeconds: 30, EndSeconds: 90, Method: "podcast-ad-audio", Flavor: store.FlavorInserted, Confidence: 0.93},
	}
	for _, record := range records {
		if err := st.RecordSegmentDetection(ctx, record, now); err != nil {
			t.Fatalf("RecordSegmentDetection %+v: %v", record, err)
		}
	}

	stored, err := st.SegmentDetections(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("SegmentDetections returned %d records, want 2", len(stored))
	}
	if stored[0].StartSeconds != 30 || stored[1].StartSeconds != 300 {
		t.Fatalf("SegmentDetections order = %v/%v, want ascending start", stored[0].StartSeconds, stored[1].StartSeconds)
	}
	if stored[0].Flavor != store.FlavorInserted || stored[0].Confidence != 0.93 {
		t.Fatalf("inserted record = %+v", stored[0])
	}
	if stored[1].Flavor != store.FlavorHostRead || stored[1].Confidence != 0.42 {
		t.Fatalf("host-read record = %+v; its flavor + confidence must survive for eval", stored[1])
	}

	markers, err := st.MarkersForMedia(ctx, id, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 0 {
		t.Fatalf("recording detections served %d markers; the eval record must never surface", len(markers))
	}
}

// TestRecordSegmentDetectionValidation covers every guard on the eval seam.
func TestRecordSegmentDetectionValidation(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	id := importVideo(t, st, "ad-eval-validation")
	now := time.Now().UTC()

	valid := store.SegmentDetection{
		MediaID: id, SegmentKind: "ad", StartSeconds: 30, EndSeconds: 90,
		Method: "podcast-ad-audio", Flavor: store.FlavorInserted, Confidence: 0.9,
	}
	cases := []struct {
		name  string
		mutef func(*store.SegmentDetection)
	}{
		{"unknown kind", func(d *store.SegmentDetection) { d.SegmentKind = "sponsor" }},
		{"unknown flavor", func(d *store.SegmentDetection) { d.Flavor = store.SegmentFlavor("mid-roll") }},
		{"flavored non-ad", func(d *store.SegmentDetection) { d.SegmentKind = "intro" }},
		{"flavourless ad", func(d *store.SegmentDetection) { d.Flavor = store.FlavorNone }},
		{"empty method", func(d *store.SegmentDetection) { d.Method = " " }},
		{"end before start", func(d *store.SegmentDetection) { d.EndSeconds = 10 }},
		{"negative start", func(d *store.SegmentDetection) { d.StartSeconds = -1 }},
		{"confidence above one", func(d *store.SegmentDetection) { d.Confidence = 1.5 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detection := valid
			tc.mutef(&detection)
			if err := st.RecordSegmentDetection(ctx, detection, now); err == nil {
				t.Fatalf("RecordSegmentDetection accepted %s", tc.name)
			}
		})
	}
}
