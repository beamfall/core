package graph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/skipdetect"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestSetSkipDetectionEnabledRequiresAdminAndFlipsToggle: the skip-detection
// toggle is an operator surface over the existing
// store.SkipDetectionEnabled/SetSkipDetectionEnabled — a non-admin manager
// session is refused, and once an Admin Session is open the mutation flips the
// toggle and the read reflects it.
func TestSetSkipDetectionEnabledRequiresAdminAndFlipsToggle(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)

	body := `{"query":"mutation S($libraryID:String!,$enabled:Boolean!){ setSkipDetectionEnabled(libraryID:$libraryID,enabled:$enabled){ libraryID enabled } }","variables":{"libraryID":"library-local","enabled":true}}`

	// Non-admin manager: refused.
	if _, errs := post(t, h, body, authorizeManager); len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("setSkipDetectionEnabled without an admin session must be refused, errs=%#v", errs)
	}

	// With an Admin Session: flips the toggle.
	openManagerAdminSession(t, st)
	data, errs := post(t, h, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setSkipDetectionEnabled (admin) errors = %#v", errs)
	}
	var info SkipDetectionInfo
	mustUnmarshal(t, data["setSkipDetectionEnabled"], &info)
	if info.LibraryID != "library-local" || !info.Enabled {
		t.Fatalf("setSkipDetectionEnabled result = %+v, want library-local enabled=true", info)
	}

	// The read reflects the flipped toggle.
	readData, errs := post(t, h, `{"query":"{ skipDetectionEnabled { libraryID enabled } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("skipDetectionEnabled read errors = %#v", errs)
	}
	var infos []SkipDetectionInfo
	mustUnmarshal(t, readData["skipDetectionEnabled"], &infos)
	var found bool
	for _, i := range infos {
		if i.LibraryID == "library-local" {
			found = true
			if !i.Enabled {
				t.Fatalf("library-local should read enabled after toggle: %+v", infos)
			}
		}
	}
	if !found {
		t.Fatalf("skipDetectionEnabled did not include library-local: %+v", infos)
	}
}

// TestSkipDetectionEnabledReadRequiresAdmin: the read side is the same
// operator tier as the write side — a non-admin manager session is refused.
func TestSkipDetectionEnabledReadRequiresAdmin(t *testing.T) {
	h := newTestHandler(t)
	if _, errs := post(t, h, `{"query":"{ skipDetectionEnabled { libraryID enabled } }"}`, authorizeManager); len(errs) == 0 ||
		!strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("skipDetectionEnabled without an admin session must be refused, errs=%#v", errs)
	}
}

// TestSetSkipDetectionEnabledRejectsUnknownLibrary: an unknown libraryID is a
// client error, not a silent no-op — the toggle can never be used as an
// existence oracle for a hidden Library.
func TestSetSkipDetectionEnabledRejectsUnknownLibrary(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	body := `{"query":"mutation { setSkipDetectionEnabled(libraryID:\"no-such-library\",enabled:true){ libraryID enabled } }"}`
	if _, errs := post(t, h, body, authorizeManager); len(errs) == 0 {
		t.Fatal("setSkipDetectionEnabled for an unknown library should error")
	}
}

// fakeChapterDetector satisfies skipdetect.Detector with canned titled
// chapters, keyed by file path — enough to exercise the chapter-import method
// without FFmpeg. AudioFingerprint is unused by this test (no siblings).
type fakeChapterDetector struct {
	chaptersByPath map[string][]ffmpeg.Chapter
}

func (f *fakeChapterDetector) Chapters(_ context.Context, path string) ([]ffmpeg.Chapter, error) {
	return f.chaptersByPath[path], nil
}

func (f *fakeChapterDetector) AudioFingerprint(_ context.Context, _ string, _ int) ([]float64, error) {
	return nil, nil
}

// seedChapteredVideo imports a video-class Media with a real backing file and
// a titled "Intro" chapter — the chapter-import method's input shape.
func seedChapteredVideo(t *testing.T, st *store.Store, libraryID store.LibraryID, title string) (store.Media, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), title+".mkv")
	if err := os.WriteFile(path, []byte("video-"+title), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           libraryID,
		Path:                path,
		Title:               title,
		Container:           "mkv",
		Codec:               "h264",
		Class:               store.MediaClassVideo,
		ContentFingerprint:  fingerprint,
		DurationSecondsHint: 1800,
	})
	if err != nil {
		t.Fatalf("import %s: %v", title, err)
	}
	return media, path
}

// setSkipDetectionViaMutation flips the toggle through the GraphQL mutation
// (the newly-exposed operator surface), never the store setter directly, and
// fails the test on any GraphQL error.
func setSkipDetectionViaMutation(t *testing.T, h *Handler, libraryID string, enabled bool) {
	t.Helper()
	body := `{"query":"mutation S($libraryID:String!,$enabled:Boolean!){ setSkipDetectionEnabled(libraryID:$libraryID,enabled:$enabled){ libraryID enabled } }","variables":{"libraryID":"` +
		libraryID + `","enabled":` + boolLiteral(enabled) + `}}`
	if _, errs := post(t, h, body, authorizeManager); len(errs) != 0 {
		t.Fatalf("setSkipDetectionEnabled(%s, %v) errors = %#v", libraryID, enabled, errs)
	}
}

func boolLiteral(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestSetSkipDetectionEnabledGatesDetectionSweepEndToEnd closes the ONE PARTIAL
// left by the NGAUDIT-B10-DETECT-SURFACE review: it proves the newly-exposed
// GraphQL mutation actually gates the detection sweep end-to-end, not just the
// stored flag. It flips the toggle exclusively through the mutation handler
// (never store.SetSkipDetectionEnabled directly), then runs the real
// skipdetect.Service against the same store: enabled, a titled-chapter Media
// yields a detected segment; disabled, an identical Media yields none.
func TestSetSkipDetectionEnabledGatesDetectionSweepEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	chapters := []ffmpeg.Chapter{{StartSeconds: 0, EndSeconds: 60, Title: "Intro"}}

	// Enabled via the mutation: the sweep detects and writes a segment.
	setSkipDetectionViaMutation(t, h, "library-local", true)
	enabledMedia, enabledPath := seedChapteredVideo(t, st, "library-local", "enabled-sweep")
	svc := skipdetect.NewService(st, &fakeChapterDetector{chaptersByPath: map[string][]ffmpeg.Chapter{
		enabledPath: chapters,
	}})
	if err := svc.DetectForMedia(ctx, enabledMedia.ID); err != nil {
		t.Fatalf("DetectForMedia (enabled): %v", err)
	}
	markers, err := st.MarkersForMedia(ctx, enabledMedia.ID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("markers for enabled media: %v", err)
	}
	if len(markers) != 1 || markers[0].SegmentKind != "intro" {
		t.Fatalf("enabled Library: expected one intro segment, got %+v", markers)
	}

	// Disabled via the mutation: an identical fixture yields no segment.
	setSkipDetectionViaMutation(t, h, "library-local", false)
	disabledMedia, disabledPath := seedChapteredVideo(t, st, "library-local", "disabled-sweep")
	svc = skipdetect.NewService(st, &fakeChapterDetector{chaptersByPath: map[string][]ffmpeg.Chapter{
		disabledPath: chapters,
	}})
	if err := svc.DetectForMedia(ctx, disabledMedia.ID); err != nil {
		t.Fatalf("DetectForMedia (disabled): %v", err)
	}
	markers, err = st.MarkersForMedia(ctx, disabledMedia.ID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("markers for disabled media: %v", err)
	}
	if len(markers) != 0 {
		t.Fatalf("disabled Library via mutation: expected no segments, got %+v", markers)
	}
}

// forbiddenMarkerWireFields are the detector-provenance fields that must never
// appear in any served marker shape. ADR-0193 Decision 2/5: flavor is internal
// provenance, "never as a public marker kind" and never on the client wire; the
// served shape carries no per-marker gate, which is exactly why auto-skip can
// key on the segment kind alone.
var forbiddenMarkerWireFields = []string{"flavor", "provenance", "method", "confidence"}

// TestServedMarkerShapesOmitDetectorProvenance is the PODAD-1 conformance
// assertion. It is a DENYLIST over every serialized marker shape the graph
// produces, not a frozen tag set: an unrelated additive field must not fail it,
// but a leaked provenance field must — in any of the three shapes.
func TestServedMarkerShapesOmitDetectorProvenance(t *testing.T) {
	shapes := []struct {
		name  string
		value any
	}{
		{"MarkerInfo", MarkerInfo{}},
		{"ExportMarkerInfo", ExportMarkerInfo{}},
		{"MyMarkerInfo", MyMarkerInfo{}},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			typ := reflect.TypeOf(shape.value)
			for i := range typ.NumField() {
				tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
				if slices.Contains(forbiddenMarkerWireFields, tag) {
					t.Fatalf("%s serializes %q; detector provenance must never reach a client", shape.name, tag)
				}
			}
		})
	}
}

// TestMarkerInfoDropsFlavorAndConfidenceOfAServedAd is the round-trip half of
// the same proof: a stored detected `ad` carrying flavor and a per-flavor
// confidence serializes to a MarkerInfo that leaks neither. The fixture is an
// `inserted` ad because a `host-read` row cannot exist — the store refuses it
// — so using one would assert against an impossible state.
func TestMarkerInfoDropsFlavorAndConfidenceOfAServedAd(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	if _, err := st.ReplaceDetectedSegment(ctx, "media-alpha", store.SegmentKindAd, 30, 90,
		"podcast-ad-audio", 0.42, store.FlavorInserted, now); err != nil {
		t.Fatalf("seed detected ad: %v", err)
	}
	markers, err := st.MarkersForMedia(ctx, "media-alpha", store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil || len(markers) != 1 {
		t.Fatalf("seed read back: %d markers, err=%v", len(markers), err)
	}
	if markers[0].Flavor != store.FlavorInserted || markers[0].Confidence != 0.42 {
		t.Fatalf("stored marker lost its provenance: %+v", markers[0])
	}

	encoded, err := json.Marshal(markerInfo(markers[0]))
	if err != nil {
		t.Fatal(err)
	}
	wire := string(encoded)
	if !strings.Contains(wire, `"segmentKind":"ad"`) {
		t.Fatalf("served marker = %s, want the `ad` kind on the wire", wire)
	}
	for _, leak := range []string{"flavor", "inserted", "0.42", "confidence", "podcast-ad-audio"} {
		if strings.Contains(wire, leak) {
			t.Fatalf("served marker leaked %q: %s", leak, wire)
		}
	}
}

// TestCreateMarkerMutationCannotAuthorAdKind is the graph-level half of the
// detector-only rule: createMarker forwards a client-supplied segmentKind
// straight into the store, so the refusal has to hold through the real
// dispatcher, not only at the store API.
func TestCreateMarkerMutationCannotAuthorAdKind(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)

	body := `{"query":"mutation C($mediaID:String!,$segmentKind:String!){ createMarker(mediaID:$mediaID,segmentKind:$segmentKind){ id segmentKind } }",` +
		`"variables":{"mediaID":"media-alpha","segmentKind":"ad"}}`
	if _, errs := post(t, h, body, authorizeManager); len(errs) == 0 {
		t.Fatal("createMarker authored an `ad` segment; `ad` is Core-detected only, never externally writable")
	}

	// The three ADR-0048 kinds are unaffected.
	writable := `{"query":"mutation C($mediaID:String!,$segmentKind:String!){ createMarker(mediaID:$mediaID,segmentKind:$segmentKind){ id segmentKind } }",` +
		`"variables":{"mediaID":"media-alpha","segmentKind":"intro"}}`
	if _, errs := post(t, h, writable, authorizeManager); len(errs) != 0 {
		t.Fatalf("createMarker intro errors = %#v", errs)
	}
}
