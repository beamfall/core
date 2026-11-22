package skipdetect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeDetector satisfies the Detector seam without FFmpeg: it returns canned
// chapters and per-Media fingerprint signatures keyed by file path, recording
// call counts so a test can assert the detector did (or did not) run.
type fakeDetector struct {
	chaptersByPath    map[string][]ffmpeg.Chapter
	fingerprintByPath map[string][]float64
	tailByPath        map[string][]float64
	cuesByPath        map[string][]ffmpeg.TextCue
	chapterCalls      int
	fingerprintCalls  int
	tailCalls         int
	cueCalls          int
	// fingerprintErr, when set, fails every self/sibling fingerprint read.
	fingerprintErr error
	// onFingerprint, when set, runs before each fingerprint read and may fail it.
	onFingerprint func(path string) error
}

func (f *fakeDetector) fingerprintFault(path string) error {
	if f.fingerprintErr != nil {
		return f.fingerprintErr
	}
	if f.onFingerprint != nil {
		return f.onFingerprint(path)
	}
	return nil
}

func (f *fakeDetector) AudioFingerprintTail(_ context.Context, path string, windowCount, _ int) ([]float64, error) {
	f.tailCalls++
	if err := f.fingerprintFault(path); err != nil {
		return nil, err
	}
	sig := f.tailByPath[path]
	if len(sig) > windowCount {
		sig = sig[len(sig)-windowCount:]
	}
	return append([]float64(nil), sig...), nil
}

func (f *fakeDetector) Chapters(_ context.Context, path string) ([]ffmpeg.Chapter, error) {
	f.chapterCalls++
	return f.chaptersByPath[path], nil
}

func (f *fakeDetector) AudioFingerprint(_ context.Context, path string, windowCount int) ([]float64, error) {
	f.fingerprintCalls++
	if err := f.fingerprintFault(path); err != nil {
		return nil, err
	}
	sig := f.fingerprintByPath[path]
	if len(sig) > windowCount {
		sig = sig[:windowCount]
	}
	out := make([]float64, len(sig))
	copy(out, sig)
	return out, nil
}

func (f *fakeDetector) OpeningTextCues(_ context.Context, path string, _ int) ([]ffmpeg.TextCue, error) {
	f.cueCalls++
	return append([]ffmpeg.TextCue(nil), f.cuesByPath[path]...), nil
}

// fixedClock returns a stable now() so written segments are deterministic.
func fixedClock() func() time.Time {
	t := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// seedVideo imports a video-class Media into a Library with a real backing file
// and returns the Media plus the file path the detector will probe.
func seedVideo(t *testing.T, st *store.Store, libraryID store.LibraryID, title string, duration int) (store.Media, string) {
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
		DurationSecondsHint: duration,
	})
	if err != nil {
		t.Fatalf("import %s: %v", title, err)
	}
	return media, path
}

// linkOrderedContainer relates each Media to one container entity with an
// ascending sort_order — the generic "ordered children of a common container"
// shape OrderedSiblingMedia walks. This is exactly the shape ENR-7 must use
// without naming any domain container.
func linkOrderedContainer(t *testing.T, st *store.Store, libraryID store.LibraryID, containerID store.EntityID, members ...store.MediaID) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, store.Entity{
		ID: containerID, LibraryID: libraryID, Kind: "container", Title: "Ordered Container",
	}); err != nil {
		t.Fatalf("upsert container: %v", err)
	}
	for i, m := range members {
		order := i
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: containerID,
			Name:         "child",
			ToMediaID:    m,
			SortOrder:    &order,
		}); err != nil {
			t.Fatalf("link %s: %v", m, err)
		}
	}
}

func enable(t *testing.T, st *store.Store, libraryID store.LibraryID) {
	t.Helper()
	if err := st.SetSkipDetectionEnabled(context.Background(), libraryID, true, time.Now().UTC()); err != nil {
		t.Fatalf("enable detection: %v", err)
	}
}

func markersOf(t *testing.T, st *store.Store, id store.MediaID) []store.Marker {
	t.Helper()
	markers, err := st.MarkersForMedia(context.Background(), id, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("markers for %s: %v", id, err)
	}
	return markers
}

func segmentOfKind(markers []store.Marker, kind string) (store.Marker, bool) {
	for _, m := range markers {
		if m.SegmentKind == kind {
			return m, true
		}
	}
	return store.Marker{}, false
}

// TestChapterImportEmitsDetected verifies (a): a titled chapter maps to a
// `detected` segment of the right kind, with the chapter-import method and high
// confidence, gated behind the per-Library toggle.
func TestChapterImportEmitsDetected(t *testing.T) {
	st := storetest.Open(t)
	media, path := seedVideo(t, st, "library-local", "with-chapters", 1800)
	enable(t, st, "library-local")

	det := &fakeDetector{chaptersByPath: map[string][]ffmpeg.Chapter{
		path: {
			{StartSeconds: 0, EndSeconds: 30, Title: "Recap"},
			{StartSeconds: 30, EndSeconds: 90, Title: "Intro"},
			{StartSeconds: 90, EndSeconds: 1700, Title: "Chapter 1"},
			{StartSeconds: 1700, EndSeconds: 1800, Title: "End Credits"},
		},
	}}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), media.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}

	markers := markersOf(t, st, media.ID)
	for _, kind := range []string{"intro", "recap", "credits"} {
		seg, ok := segmentOfKind(markers, kind)
		if !ok {
			t.Fatalf("expected a %s segment from chapter import; markers=%+v", kind, markers)
		}
		if seg.Provenance != store.ProvenanceDetected {
			t.Errorf("%s: provenance = %q, want detected", kind, seg.Provenance)
		}
		if seg.Method != MethodChapterImport {
			t.Errorf("%s: method = %q, want %q", kind, seg.Method, MethodChapterImport)
		}
		if seg.Confidence < fingerprintAutoApplyThreshold {
			t.Errorf("%s: confidence %.2f below auto-apply threshold", kind, seg.Confidence)
		}
	}
	// "Chapter 1" names no skip kind — it must not produce a segment.
	if len(markers) != 3 {
		t.Fatalf("expected exactly 3 detected segments, got %d: %+v", len(markers), markers)
	}
}

// TestDefaultOffNoDetection verifies the per-Library toggle default-off: with
// the Library never opted in, no detection runs and no segment is written.
func TestDefaultOffNoDetection(t *testing.T) {
	st := storetest.Open(t)
	media, path := seedVideo(t, st, "library-local", "default-off", 1800)
	// NOTE: no enable() call.

	det := &fakeDetector{chaptersByPath: map[string][]ffmpeg.Chapter{
		path: {{StartSeconds: 0, EndSeconds: 60, Title: "Intro"}},
	}}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), media.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	if markers := markersOf(t, st, media.ID); len(markers) != 0 {
		t.Fatalf("default-off Library produced %d segments, want 0: %+v", len(markers), markers)
	}
	if det.chapterCalls != 0 || det.fingerprintCalls != 0 {
		t.Fatalf("detector ran on a default-off Library (chapters=%d fingerprint=%d)", det.chapterCalls, det.fingerprintCalls)
	}
}

// TestVideoOnly verifies an audio-class Media is a no-op even when its Library
// is opted in.
func TestVideoOnly(t *testing.T) {
	st := storetest.Open(t)
	enable(t, st, "library-local")
	path := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(path, []byte("audio bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: "song", Container: "mp3", Codec: "mp3",
		Class: store.MediaClassAudio, ContentFingerprint: fingerprint, DurationSecondsHint: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	det := &fakeDetector{chaptersByPath: map[string][]ffmpeg.Chapter{
		path: {{StartSeconds: 0, EndSeconds: 30, Title: "Intro"}},
	}}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), media.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	if markers := markersOf(t, st, media.ID); len(markers) != 0 {
		t.Fatalf("audio Media produced %d segments, want 0", len(markers))
	}
	if det.chapterCalls != 0 {
		t.Fatalf("detector ran on audio Media (chapters=%d)", det.chapterCalls)
	}
}

// TestManualWins verifies a `detected` write never clobbers a user's `manual`
// segment of the same kind.
func TestManualWins(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	media, path := seedVideo(t, st, "library-local", "manual-wins", 1800)
	enable(t, st, "library-local")

	// User authors a manual intro at a different time than detection would find.
	end := 45.0
	manual, err := st.CreateMarker(ctx, store.Marker{
		MediaID: media.ID, SegmentKind: "intro", StartSeconds: 10, EndSeconds: &end,
		Provenance: store.ProvenanceManual,
	}, store.LibraryFilter{Revealed: true, AllLibraries: true}, time.Now().UTC())
	if err != nil {
		t.Fatalf("create manual: %v", err)
	}

	det := &fakeDetector{chaptersByPath: map[string][]ffmpeg.Chapter{
		path: {{StartSeconds: 0, EndSeconds: 90, Title: "Intro"}},
	}}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(ctx, media.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}

	markers := markersOf(t, st, media.ID)
	intro, ok := segmentOfKind(markers, "intro")
	if !ok {
		t.Fatal("intro segment vanished")
	}
	if intro.ID != manual.ID || intro.Provenance != store.ProvenanceManual {
		t.Fatalf("manual intro was clobbered: %+v", intro)
	}
	if intro.StartSeconds != 10 {
		t.Fatalf("manual intro start changed to %v", intro.StartSeconds)
	}
	if len(markers) != 1 {
		t.Fatalf("expected only the manual segment, got %d: %+v", len(markers), markers)
	}
}

// TestIdempotent verifies a second run produces no duplicate and no drift: the
// same single detected segment of each kind, same IDs after the second run as
// after the first for unchanged input.
func TestIdempotent(t *testing.T) {
	st := storetest.Open(t)
	media, path := seedVideo(t, st, "library-local", "idempotent", 1800)
	enable(t, st, "library-local")

	det := &fakeDetector{chaptersByPath: map[string][]ffmpeg.Chapter{
		path: {{StartSeconds: 0, EndSeconds: 60, Title: "Intro"}},
	}}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), media.ID); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := markersOf(t, st, media.ID)
	if len(first) != 1 {
		t.Fatalf("first run wrote %d segments, want 1", len(first))
	}
	if err := svc.DetectForMedia(context.Background(), media.ID); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second := markersOf(t, st, media.ID)
	if len(second) != 1 {
		t.Fatalf("second run left %d segments, want 1 (duplicate written)", len(second))
	}
	if first[0].StartSeconds != second[0].StartSeconds || first[0].EndSeconds == nil || second[0].EndSeconds == nil ||
		*first[0].EndSeconds != *second[0].EndSeconds {
		t.Fatalf("segment drifted between runs: %+v -> %+v", first[0], second[0])
	}
}

// TestSuppressionRespected verifies a suppressed (Media, kind) is not
// resurrected by detection.
func TestSuppressionRespected(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	media, path := seedVideo(t, st, "library-local", "suppressed", 1800)
	enable(t, st, "library-local")
	if err := st.SuppressSegment(ctx, media.ID, "intro", time.Now().UTC()); err != nil {
		t.Fatalf("suppress: %v", err)
	}
	det := &fakeDetector{chaptersByPath: map[string][]ffmpeg.Chapter{
		path: {{StartSeconds: 0, EndSeconds: 60, Title: "Intro"}},
	}}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(ctx, media.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	if _, ok := segmentOfKind(markersOf(t, st, media.ID), "intro"); ok {
		t.Fatal("suppressed intro was resurrected by detection")
	}
}

// TestFingerprintSharedIntro verifies (b): two ordered siblings sharing a long
// identical leading audio signature yield a high-confidence `detected` intro
// via the fingerprint method, found over the GENERIC ordered-container graph.
func TestFingerprintSharedIntro(t *testing.T) {
	st := storetest.Open(t)
	a, pathA := seedVideo(t, st, "library-local", "sibling-a", 1800)
	b, pathB := seedVideo(t, st, "library-local", "sibling-b", 1800)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-1", a.ID, b.ID)

	// Identical 40-window leading run, then diverging content — a shared intro.
	shared := make([]float64, 40)
	for i := range shared {
		shared[i] = 0.5
	}
	sigA := append(append([]float64{}, shared...), repeat(0.1, 140)...)
	sigB := append(append([]float64{}, shared...), repeat(0.9, 140)...)
	det := &fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{}, // no chapters
		fingerprintByPath: map[string][]float64{
			pathA: sigA,
			pathB: sigB,
		},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), a.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	intro, ok := segmentOfKind(markersOf(t, st, a.ID), "intro")
	if !ok {
		t.Fatal("expected a fingerprint intro from the shared leading run")
	}
	if intro.Method != MethodFingerprint {
		t.Errorf("method = %q, want %q", intro.Method, MethodFingerprint)
	}
	if intro.Provenance != store.ProvenanceDetected {
		t.Errorf("provenance = %q, want detected", intro.Provenance)
	}
	if intro.StartSeconds != 0 || intro.EndSeconds == nil || *intro.EndSeconds != 40 {
		t.Errorf("intro range = [%v, %v], want [0, 40]", intro.StartSeconds, intro.EndSeconds)
	}
}

func TestFingerprintSharedCreditsUsesTailAndInvalidatesOldHeadResult(t *testing.T) {
	st := storetest.Open(t)
	a, pathA := seedVideo(t, st, "library-local", "credits-a", 1800)
	b, pathB := seedVideo(t, st, "library-local", "credits-b", 2100)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-credits", a.ID, b.ID)

	ctx := context.Background()
	if _, err := st.ReplaceDetectedSegment(ctx, a.ID, "credits", 0, 40,
		MethodFingerprint, 0.99, store.FlavorNone, time.Now().UTC()); err != nil {
		t.Fatalf("seed old head-based credits: %v", err)
	}
	sharedCredits := repeat(0.7, 45)
	det := &fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{
			pathA: append(repeat(0.1, 40), repeat(0.2, 140)...),
			pathB: append(repeat(0.9, 40), repeat(0.8, 140)...),
		},
		tailByPath: map[string][]float64{
			pathA: append(repeat(0.1, fingerprintTailWindows-len(sharedCredits)), sharedCredits...),
			pathB: append(repeat(0.9, fingerprintTailWindows-len(sharedCredits)), sharedCredits...),
		},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(ctx, a.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	credits, ok := segmentOfKind(markersOf(t, st, a.ID), "credits")
	if !ok {
		t.Fatal("expected credits from shared EOF-aligned tail")
	}
	if credits.StartSeconds != 1755 || credits.EndSeconds == nil || *credits.EndSeconds != 1800 {
		t.Fatalf("credits range = [%v,%v], want [1755,1800]", credits.StartSeconds, credits.EndSeconds)
	}
	if det.tailCalls != 2 {
		t.Fatalf("tail calls = %d, want self+sibling", det.tailCalls)
	}
}

func TestFingerprintSweepInvalidatesLegacyCreditsWithoutSiblings(t *testing.T) {
	st := storetest.Open(t)
	media, path := seedVideo(t, st, "library-local", "legacy-credits", 1800)
	enable(t, st, "library-local")
	if _, err := st.ReplaceDetectedSegment(context.Background(), media.ID, "credits", 0, 30,
		MethodFingerprint, 0.99, store.FlavorNone, time.Now().UTC()); err != nil {
		t.Fatalf("seed legacy credits: %v", err)
	}
	det := &fakeDetector{chaptersByPath: map[string][]ffmpeg.Chapter{}, fingerprintByPath: map[string][]float64{path: repeat(0.5, 180)}}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), media.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	if _, ok := segmentOfKind(markersOf(t, st, media.ID), "credits"); ok {
		t.Fatal("legacy head-derived credits survived next detection sweep")
	}
}

// TestFingerprintRejectsWholeFileMatchOnShortSiblings is the CORE-3079
// regression: short siblings whose loudness never diverges (a shared constant
// audio bed) matched over their whole length, so intro and credits both
// covered the file and Skip intro jumped to the end. Neither may be written.
func TestFingerprintRejectsWholeFileMatchOnShortSiblings(t *testing.T) {
	st := storetest.Open(t)
	a, pathA := seedVideo(t, st, "library-local", "bed-a", 111)
	b, pathB := seedVideo(t, st, "library-local", "bed-b", 111)
	c, pathC := seedVideo(t, st, "library-local", "bed-c", 111)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-bed", a.ID, b.ID, c.ID)
	bed := repeat(0.5, 111)
	det := &fakeDetector{
		chaptersByPath:    map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{pathA: bed, pathB: bed, pathC: bed},
		tailByPath:        map[string][]float64{pathA: bed, pathB: bed, pathC: bed},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), b.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	markers := markersOf(t, st, b.ID)
	for _, kind := range []string{"intro", "credits"} {
		if m, ok := segmentOfKind(markers, kind); ok {
			t.Errorf("%s written over [%v, %v] from a run that never diverged", kind, m.StartSeconds, m.EndSeconds)
		}
	}
}

// TestFingerprintRejectsIntroLongerThanMax verifies a shared opening longer
// than maxIntroWindows is not written as an intro.
func TestFingerprintRejectsIntroLongerThanMax(t *testing.T) {
	st := storetest.Open(t)
	a, pathA := seedVideo(t, st, "library-local", "long-a", 1800)
	b, pathB := seedVideo(t, st, "library-local", "long-b", 1800)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-long", a.ID, b.ID)
	shared := repeat(0.5, maxIntroWindows+10)
	rest := fingerprintHeadWindows - len(shared)
	det := &fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{
			pathA: append(append([]float64{}, shared...), repeat(0.1, rest)...),
			pathB: append(append([]float64{}, shared...), repeat(0.9, rest)...),
		},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), a.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	if m, ok := segmentOfKind(markersOf(t, st, a.ID), "intro"); ok {
		t.Fatalf("intro written over [%v, %v], longer than the %d s cap", m.StartSeconds, m.EndSeconds, maxIntroWindows)
	}
}

// TestFingerprintNearDuplicateSiblingDoesNotHideRealIntro verifies the best
// sibling is chosen among plausible runs only: a near-duplicate sibling that
// matches the whole head must not displace a real 40 s intro found against
// another sibling.
func TestFingerprintNearDuplicateSiblingDoesNotHideRealIntro(t *testing.T) {
	st := storetest.Open(t)
	a, pathA := seedVideo(t, st, "library-local", "dup-a", 1800)
	dup, pathDup := seedVideo(t, st, "library-local", "dup-copy", 1800)
	c, pathC := seedVideo(t, st, "library-local", "dup-c", 1800)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-dup", a.ID, dup.ID, c.ID)
	sigA := append(repeat(0.5, 40), repeat(0.1, 140)...)
	det := &fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{
			pathA:   sigA,
			pathDup: append([]float64{}, sigA...),
			pathC:   append(repeat(0.5, 40), repeat(0.9, 140)...),
		},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), a.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	intro, ok := segmentOfKind(markersOf(t, st, a.ID), "intro")
	if !ok {
		t.Fatal("expected the 40 s intro shared with the non-duplicate sibling")
	}
	if intro.StartSeconds != 0 || intro.EndSeconds == nil || *intro.EndSeconds != 40 {
		t.Fatalf("intro range = [%v, %v], want [0, 40]", intro.StartSeconds, intro.EndSeconds)
	}
}

// TestFingerprintNearDuplicateSiblingDoesNotHideRealCredits is the tail
// counterpart: a sibling whose whole tail matches shows no credits start and
// must not displace a real 40 s credits run found against another sibling.
func TestFingerprintNearDuplicateSiblingDoesNotHideRealCredits(t *testing.T) {
	st := storetest.Open(t)
	a, pathA := seedVideo(t, st, "library-local", "tdup-a", 1800)
	dup, pathDup := seedVideo(t, st, "library-local", "tdup-copy", 1800)
	c, pathC := seedVideo(t, st, "library-local", "tdup-c", 1800)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-tdup", a.ID, dup.ID, c.ID)
	tailA := append(repeat(0.1, fingerprintTailWindows-40), repeat(0.5, 40)...)
	det := &fakeDetector{
		chaptersByPath:    map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{},
		tailByPath: map[string][]float64{
			pathA:   tailA,
			pathDup: append([]float64{}, tailA...),
			pathC:   append(repeat(0.9, fingerprintTailWindows-40), repeat(0.5, 40)...),
		},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), a.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	credits, ok := segmentOfKind(markersOf(t, st, a.ID), "credits")
	if !ok {
		t.Fatal("expected the 40 s credits shared with the non-duplicate sibling")
	}
	if credits.StartSeconds != 1760 {
		t.Fatalf("credits start = %v, want 1760", credits.StartSeconds)
	}
}

// TestFingerprintRunLengthBoundaries pins the caps at their edges: a run of
// exactly half the runtime or exactly maxIntroWindows is kept, one window more
// is dropped.
func TestFingerprintRunLengthBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		duration int
		kind     string
		shared   int
		want     bool
	}{
		{"intro at half runtime", 100, "intro", 50, true},
		{"intro over half runtime", 100, "intro", 51, false},
		{"credits at half runtime", 100, "credits", 50, true},
		{"credits over half runtime", 100, "credits", 51, false},
		{"intro at max", 1800, "intro", maxIntroWindows, true},
		{"intro over max", 1800, "intro", maxIntroWindows + 1, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.Open(t)
			a, pathA := seedVideo(t, st, "library-local", fmt.Sprintf("edge-a-%d", i), tc.duration)
			b, pathB := seedVideo(t, st, "library-local", fmt.Sprintf("edge-b-%d", i), tc.duration)
			enable(t, st, "library-local")
			linkOrderedContainer(t, st, "library-local", store.EntityID(fmt.Sprintf("container-edge-%d", i)), a.ID, b.ID)
			region := fingerprintHeadWindows
			if tc.kind == "credits" {
				region = fingerprintTailWindows
			}
			if tc.duration < region {
				region = tc.duration
			}
			rest := region - tc.shared
			var sigA, sigB []float64
			if tc.kind == "intro" {
				sigA = append(repeat(0.5, tc.shared), repeat(0.1, rest)...)
				sigB = append(repeat(0.5, tc.shared), repeat(0.9, rest)...)
			} else {
				sigA = append(repeat(0.1, rest), repeat(0.5, tc.shared)...)
				sigB = append(repeat(0.9, rest), repeat(0.5, tc.shared)...)
			}
			det := &fakeDetector{
				chaptersByPath:    map[string][]ffmpeg.Chapter{},
				fingerprintByPath: map[string][]float64{},
				tailByPath:        map[string][]float64{},
			}
			if tc.kind == "intro" {
				det.fingerprintByPath[pathA], det.fingerprintByPath[pathB] = sigA, sigB
			} else {
				det.tailByPath[pathA], det.tailByPath[pathB] = sigA, sigB
			}
			svc := &Service{store: st, detector: det, now: fixedClock()}
			if err := svc.DetectForMedia(context.Background(), a.ID); err != nil {
				t.Fatalf("DetectForMedia: %v", err)
			}
			if _, got := segmentOfKind(markersOf(t, st, a.ID), tc.kind); got != tc.want {
				t.Fatalf("%s written = %v, want %v for a %d-window run on %d s", tc.kind, got, tc.want, tc.shared, tc.duration)
			}
		})
	}
}

// TestFingerprintLowConfidenceDropped verifies the confidence gate: a shared run
// too short to clear the minimum is dropped, not written.
func TestFingerprintLowConfidenceDropped(t *testing.T) {
	st := storetest.Open(t)
	a, pathA := seedVideo(t, st, "library-local", "short-a", 1800)
	b, pathB := seedVideo(t, st, "library-local", "short-b", 1800)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-short", a.ID, b.ID)

	// Only a 5-window shared run (below minSharedWindows) then divergence.
	shared := repeat(0.5, 5)
	sigA := append(append([]float64{}, shared...), repeat(0.1, 175)...)
	sigB := append(append([]float64{}, shared...), repeat(0.9, 175)...)
	det := &fakeDetector{
		chaptersByPath:    map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{pathA: sigA, pathB: sigB},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), a.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	if _, ok := segmentOfKind(markersOf(t, st, a.ID), "intro"); ok {
		t.Fatal("a too-short shared run should be dropped by the confidence gate")
	}
}

// TestStandaloneNoSiblingsNoFingerprint verifies a Media with no ordered
// container yields no fingerprint segment (and does not error) — only
// chapter-import can apply to standalone files.
func TestStandaloneNoSiblingsNoFingerprint(t *testing.T) {
	st := storetest.Open(t)
	media, path := seedVideo(t, st, "library-local", "standalone", 1800)
	enable(t, st, "library-local")
	det := &fakeDetector{
		chaptersByPath:    map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{path: repeat(0.5, 180)},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), media.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	if markers := markersOf(t, st, media.ID); len(markers) != 0 {
		t.Fatalf("standalone Media produced %d segments, want 0", len(markers))
	}
	if det.fingerprintCalls != 0 {
		t.Fatalf("fingerprint ran with no siblings (calls=%d)", det.fingerprintCalls)
	}
}

func TestReverseFingerprintEmitsRecapFromScatteredPriorMatches(t *testing.T) {
	st := storetest.Open(t)
	first, firstPath := seedVideo(t, st, "library-local", "prior-first", 300)
	second, secondPath := seedVideo(t, st, "library-local", "prior-second", 300)
	current, currentPath := seedVideo(t, st, "library-local", "current", 300)
	later, laterPath := seedVideo(t, st, "library-local", "later", 300)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-recap", first.ID, second.ID, current.ID, later.ID)

	firstSig := repeat(0.05, 300)
	secondSig := repeat(0.75, 300)
	currentSig := repeat(0.95, 300)
	laterSig := repeat(0.15, 300)
	copy(currentSig[2:6], repeat(0.25, 4))
	copy(currentSig[7:11], repeat(0.35, 4))
	copy(currentSig[12:16], repeat(0.45, 4))
	copy(firstSig[60:64], repeat(0.25, 4))
	copy(firstSig[180:184], repeat(0.45, 4))
	copy(secondSig[120:124], repeat(0.35, 4))
	copy(laterSig[40:44], repeat(0.55, 4))
	det := &fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{
			firstPath: firstSig, secondPath: secondSig, currentPath: currentSig, laterPath: laterSig,
		},
		tailByPath: map[string][]float64{},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), current.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	recap, ok := segmentOfKind(markersOf(t, st, current.ID), "recap")
	if !ok {
		t.Fatal("expected recap from scattered matches across prior ordered siblings")
	}
	if recap.Method != MethodReverseFingerprint || recap.Provenance != store.ProvenanceDetected {
		t.Fatalf("recap provenance = %+v", recap)
	}
	if recap.Confidence < fingerprintAutoApplyThreshold {
		t.Fatalf("recap confidence = %.3f, below auto-apply threshold", recap.Confidence)
	}
	if recap.StartSeconds != 2 || recap.EndSeconds == nil || *recap.EndSeconds != 16 {
		t.Fatalf("recap range = [%v,%v], want [2,16]", recap.StartSeconds, recap.EndSeconds)
	}
	if err := svc.DetectForMedia(context.Background(), current.ID); err != nil {
		t.Fatalf("second DetectForMedia: %v", err)
	}
	markers := markersOf(t, st, current.ID)
	if len(markers) != 1 || markers[0].Method != MethodReverseFingerprint {
		t.Fatalf("reverse-fingerprint rerun was not idempotent: %+v", markers)
	}
	det.chaptersByPath[currentPath] = []ffmpeg.Chapter{{StartSeconds: 0, EndSeconds: 18, Title: "Recap"}}
	if err := svc.DetectForMedia(context.Background(), current.ID); err != nil {
		t.Fatalf("chapter-priority DetectForMedia: %v", err)
	}
	markers = markersOf(t, st, current.ID)
	if len(markers) != 1 || markers[0].Method != MethodChapterImport {
		t.Fatalf("reverse fingerprint replaced deterministic chapter recap: %+v", markers)
	}
}

func TestReverseFingerprintDoesNotUseLaterOrderedSibling(t *testing.T) {
	st := storetest.Open(t)
	first, firstPath := seedVideo(t, st, "library-local", "ordered-first", 300)
	current, currentPath := seedVideo(t, st, "library-local", "ordered-current", 300)
	later, laterPath := seedVideo(t, st, "library-local", "ordered-later", 300)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-prior-only", first.ID, current.ID, later.ID)

	currentSig := repeat(0.95, 300)
	copy(currentSig[:12], repeat(0.35, 12))
	firstSig := repeat(0.05, 300)
	laterSig := repeat(0.75, 300)
	copy(firstSig[60:66], repeat(0.35, 6))
	copy(laterSig[140:146], repeat(0.35, 6))
	det := &fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{
			firstPath: firstSig, currentPath: currentSig, laterPath: laterSig,
		},
		tailByPath: map[string][]float64{},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), current.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	if _, ok := segmentOfKind(markersOf(t, st, current.ID), "recap"); ok {
		t.Fatal("later ordered sibling contributed reverse-fingerprint evidence")
	}
}

func TestRecapCueFastPathsButNeverReplacesReverseEvidence(t *testing.T) {
	st := storetest.Open(t)
	first, firstPath := seedVideo(t, st, "library-local", "cue-first", 300)
	second, secondPath := seedVideo(t, st, "library-local", "cue-second", 300)
	current, currentPath := seedVideo(t, st, "library-local", "cue-current", 300)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-cue", first.ID, second.ID, current.ID)

	firstSig := repeat(0.05, 300)
	secondSig := repeat(0.75, 300)
	currentSig := repeat(0.95, 300)
	copy(currentSig[4:7], repeat(0.25, 3))
	copy(currentSig[9:12], repeat(0.45, 3))
	copy(firstSig[60:63], repeat(0.25, 3))
	copy(secondSig[150:153], repeat(0.45, 3))
	det := &fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{
			firstPath: firstSig, secondPath: secondSig, currentPath: currentSig,
		},
		tailByPath: map[string][]float64{},
		cuesByPath: map[string][]ffmpeg.TextCue{
			currentPath: {{StartSeconds: 1, EndSeconds: 3, Text: "Previously, on…"}},
		},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), current.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	recap, ok := segmentOfKind(markersOf(t, st, current.ID), "recap")
	if !ok {
		t.Fatal("high-precision opening cue did not fast-path corroborated reverse evidence")
	}
	if recap.StartSeconds != 1 || recap.EndSeconds == nil || *recap.EndSeconds != 12 {
		t.Fatalf("cue-assisted recap range = [%v,%v], want [1,12]", recap.StartSeconds, recap.EndSeconds)
	}

	currentOnly, currentOnlyPath := seedVideo(t, st, "library-local", "cue-only", 300)
	linkOrderedContainer(t, st, "library-local", "container-cue-only", first.ID, second.ID, currentOnly.ID)
	det.fingerprintByPath[currentOnlyPath] = repeat(0.95, 300)
	det.cuesByPath[currentOnlyPath] = []ffmpeg.TextCue{{StartSeconds: 0, EndSeconds: 2, Text: "Last time on this program"}}
	if err := svc.DetectForMedia(context.Background(), currentOnly.ID); err != nil {
		t.Fatalf("cue-only DetectForMedia: %v", err)
	}
	if _, ok := segmentOfKind(markersOf(t, st, currentOnly.ID), "recap"); ok {
		t.Fatal("subtitle cue emitted a standalone recap without reverse evidence")
	}
}

func TestReverseFingerprintRejectsSharedIntro(t *testing.T) {
	st := storetest.Open(t)
	first, firstPath := seedVideo(t, st, "library-local", "intro-first", 300)
	second, secondPath := seedVideo(t, st, "library-local", "intro-second", 300)
	current, currentPath := seedVideo(t, st, "library-local", "intro-current", 300)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", "container-shared-intro", first.ID, second.ID, current.ID)

	shared := repeat(0.4, 40)
	det := &fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{
			firstPath:   append(append([]float64{}, shared...), repeat(0.1, 260)...),
			secondPath:  append(append([]float64{}, shared...), repeat(0.7, 260)...),
			currentPath: append(append([]float64{}, shared...), repeat(0.9, 260)...),
		},
		tailByPath: map[string][]float64{},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(context.Background(), current.ID); err != nil {
		t.Fatalf("DetectForMedia: %v", err)
	}
	markers := markersOf(t, st, current.ID)
	if _, ok := segmentOfKind(markers, "intro"); !ok {
		t.Fatal("shared leading run did not emit intro")
	}
	if _, ok := segmentOfKind(markers, "recap"); ok {
		t.Fatal("shared intro was misclassified as recap")
	}
}

func TestRecapCuePhraseIsWordBounded(t *testing.T) {
	for _, text := range []string{"Previously, on…", "LAST TIME ON", "Earlier on this program"} {
		if !recapCuePhrase(text) {
			t.Errorf("recapCuePhrase(%q) = false", text)
		}
	}
	for _, text := range []string{"previously online", "on previously", "ordinary dialogue"} {
		if recapCuePhrase(text) {
			t.Errorf("recapCuePhrase(%q) = true", text)
		}
	}
}

func repeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// fingerprintRetryFixture seeds two ordered siblings sharing a 40-window intro
// and a 45-window EOF-aligned credits run, runs one successful detection for
// a, then adds a manual recap and an ad suppression lock to a.
func fingerprintRetryFixture(t *testing.T, name string) (*store.Store, *Service, *fakeDetector, store.MediaID, string, string, store.Marker) {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	a, pathA := seedVideo(t, st, "library-local", name+"-a", 1800)
	b, pathB := seedVideo(t, st, "library-local", name+"-b", 1800)
	enable(t, st, "library-local")
	linkOrderedContainer(t, st, "library-local", store.EntityID("container-"+name), a.ID, b.ID)
	sharedCredits := repeat(0.7, 45)
	det := &fakeDetector{
		chaptersByPath: map[string][]ffmpeg.Chapter{},
		fingerprintByPath: map[string][]float64{
			pathA: append(repeat(0.5, 40), repeat(0.1, 140)...),
			pathB: append(repeat(0.5, 40), repeat(0.9, 140)...),
		},
		tailByPath: map[string][]float64{
			pathA: append(repeat(0.1, fingerprintTailWindows-len(sharedCredits)), sharedCredits...),
			pathB: append(repeat(0.9, fingerprintTailWindows-len(sharedCredits)), sharedCredits...),
		},
	}
	svc := &Service{store: st, detector: det, now: fixedClock()}
	if err := svc.DetectForMedia(ctx, a.ID); err != nil {
		t.Fatalf("initial DetectForMedia: %v", err)
	}
	markers := markersOf(t, st, a.ID)
	if _, ok := segmentOfKind(markers, "intro"); !ok {
		t.Fatal("fixture: expected a fingerprint intro")
	}
	if _, ok := segmentOfKind(markers, "credits"); !ok {
		t.Fatal("fixture: expected fingerprint credits")
	}
	end := 20.0
	manual, err := st.CreateMarker(ctx, store.Marker{
		MediaID: a.ID, SegmentKind: "recap", StartSeconds: 5, EndSeconds: &end,
		Provenance: store.ProvenanceManual,
	}, store.LibraryFilter{Revealed: true, AllLibraries: true}, time.Now().UTC())
	if err != nil {
		t.Fatalf("create manual recap: %v", err)
	}
	if err := st.SuppressSegment(ctx, a.ID, store.SegmentKindAd, time.Now().UTC()); err != nil {
		t.Fatalf("suppress ad: %v", err)
	}
	return st, svc, det, a.ID, pathA, pathB, manual
}

func assertManualAndSuppressionKept(t *testing.T, st *store.Store, id store.MediaID, manual store.Marker) {
	t.Helper()
	recap, ok := segmentOfKind(markersOf(t, st, id), "recap")
	if !ok || recap.ID != manual.ID || recap.Provenance != store.ProvenanceManual {
		t.Fatalf("manual recap not preserved: got %+v (present=%v)", recap, ok)
	}
	suppressed, err := st.SegmentSuppressed(context.Background(), id, store.SegmentKindAd)
	if err != nil {
		t.Fatalf("SegmentSuppressed: %v", err)
	}
	if !suppressed {
		t.Fatal("ad suppression lock was lost")
	}
}

// TestAuditFingerprintFailureKeepsPriorMarkers verifies CORE-0752: a retry
// whose analysis fails (detector error) or is canceled mid-analysis returns
// an error and leaves the prior detector markers, manual rows and suppression
// state untouched.
func TestAuditFingerprintFailureKeepsPriorMarkers(t *testing.T) {
	st, svc, det, id, _, pathB, manual := fingerprintRetryFixture(t, "retry-fail")
	before := markersOf(t, st, id)
	priorIntro, _ := segmentOfKind(before, "intro")
	priorCredits, _ := segmentOfKind(before, "credits")
	assertPriorKept := func(stage string) {
		t.Helper()
		after := markersOf(t, st, id)
		intro, ok := segmentOfKind(after, "intro")
		if !ok || intro.ID != priorIntro.ID {
			t.Fatalf("%s: prior fingerprint intro erased or replaced: %+v (present=%v)", stage, intro, ok)
		}
		credits, ok := segmentOfKind(after, "credits")
		if !ok || credits.ID != priorCredits.ID {
			t.Fatalf("%s: prior fingerprint credits erased or replaced: %+v (present=%v)", stage, credits, ok)
		}
		assertManualAndSuppressionKept(t, st, id, manual)
	}

	det.fingerprintErr = errors.New("transient decode failure")
	if err := svc.DetectForMedia(context.Background(), id); err == nil {
		t.Fatal("failed analysis returned nil error")
	}
	assertPriorKept("detector error")

	// Cancellation during a sibling read: the per-sibling failure is skipped,
	// so without the staged check the empty result would erase prior markers.
	det.fingerprintErr = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	det.onFingerprint = func(path string) error {
		if path == pathB {
			cancel()
			return context.Canceled
		}
		return nil
	}
	if err := svc.DetectForMedia(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled analysis error = %v, want context.Canceled", err)
	}
	assertPriorKept("cancellation")
}

// TestFingerprintSuccessReplacesPriorMarkers verifies CORE-0752's other half:
// a completed analysis that changes or finds nothing replaces the prior
// detector markers, still preserving manual rows and suppression state.
func TestFingerprintSuccessReplacesPriorMarkers(t *testing.T) {
	st, svc, det, id, pathA, pathB, manual := fingerprintRetryFixture(t, "retry-ok")

	// Changed analysis: a shorter shared intro and diverging tails.
	det.fingerprintByPath[pathA] = append(repeat(0.5, 30), repeat(0.1, 150)...)
	det.fingerprintByPath[pathB] = append(repeat(0.5, 30), repeat(0.9, 150)...)
	det.tailByPath[pathA] = repeat(0.1, fingerprintTailWindows)
	det.tailByPath[pathB] = repeat(0.9, fingerprintTailWindows)
	if err := svc.DetectForMedia(context.Background(), id); err != nil {
		t.Fatalf("changed DetectForMedia: %v", err)
	}
	markers := markersOf(t, st, id)
	intro, ok := segmentOfKind(markers, "intro")
	if !ok || intro.Method != MethodFingerprint || intro.EndSeconds == nil || *intro.EndSeconds != 30 {
		t.Fatalf("changed analysis intro = %+v (present=%v), want fingerprint [0,30]", intro, ok)
	}
	if credits, ok := segmentOfKind(markers, "credits"); ok {
		t.Fatalf("stale credits survived a successful analysis: %+v", credits)
	}
	assertManualAndSuppressionKept(t, st, id, manual)

	// Empty analysis: nothing shared any more.
	det.fingerprintByPath[pathA] = repeat(0.1, 180)
	det.fingerprintByPath[pathB] = repeat(0.9, 180)
	if err := svc.DetectForMedia(context.Background(), id); err != nil {
		t.Fatalf("empty DetectForMedia: %v", err)
	}
	markers = markersOf(t, st, id)
	if intro, ok := segmentOfKind(markers, "intro"); ok {
		t.Fatalf("stale intro survived an empty successful analysis: %+v", intro)
	}
	assertManualAndSuppressionKept(t, st, id, manual)
}
