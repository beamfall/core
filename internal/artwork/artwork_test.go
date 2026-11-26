package artwork

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type fakeGrabber struct {
	calls         []string
	err           error
	embedded      bool
	embeddedErr   error
	embeddedCalls []string
}

func (f *fakeGrabber) FrameGrab(_ context.Context, src string, _ float64, _ int, dst string) error {
	f.calls = append(f.calls, src)
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(dst, []byte("jpeg"), 0o600)
}

func (f *fakeGrabber) ExtractEmbeddedArtwork(_ context.Context, src string, _ int, dst string) (bool, error) {
	f.embeddedCalls = append(f.embeddedCalls, src)
	if f.embeddedErr != nil || !f.embedded {
		return false, f.embeddedErr
	}
	return true, os.WriteFile(dst, []byte("embedded jpeg"), 0o600)
}

func setPrimaryFile(t *testing.T, st *store.Store, id store.MediaID) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), string(id)+".mp4")
	if err := os.WriteFile(path, []byte("media content for "+string(id)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), id, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func importImageFile(t *testing.T, st *store.Store, title, ext string) (store.MediaID, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), title+"."+ext)
	if err := os.WriteFile(path, []byte("image content for "+title), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              title,
		Container:          ext,
		Class:              store.MediaClassImage,
		ContentFingerprint: store.HashMediaFilePath(path),
		SizeBytes:          1,
		ModTime:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return media.ID, path
}

func TestGeneratePosterGrabsFrameFromPrimaryFile(t *testing.T) {
	st := storetest.Open(t)
	src := setPrimaryFile(t, st, "media-alpha")
	grabber := &fakeGrabber{}
	svc := NewService(st, grabber, t.TempDir())

	if err := svc.GeneratePoster(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GeneratePoster returned error: %v", err)
	}
	if len(grabber.calls) != 1 || grabber.calls[0] != src {
		t.Fatalf("grabber calls = %v, want [%s]", grabber.calls, src)
	}
	if !svc.HasPoster("media-alpha") {
		t.Fatal("HasPoster = false after generation")
	}
	if !svc.IsGeneratedArtwork("media-alpha", SlotPoster) {
		t.Fatal("generated poster provenance was not recorded")
	}
	if err := svc.ImportPoster(context.Background(), "media-alpha", []byte("official")); err != nil {
		t.Fatal(err)
	}
	if svc.IsGeneratedArtwork("media-alpha", SlotPoster) {
		t.Fatal("imported poster retained generated provenance")
	}
}

func TestGeneratePosterPrefersEmbeddedArtwork(t *testing.T) {
	st := storetest.Open(t)
	src := setPrimaryFile(t, st, "media-alpha")
	grabber := &fakeGrabber{embedded: true}
	svc := NewService(st, grabber, t.TempDir())
	writePoster(t, svc, "media-alpha")
	if err := os.WriteFile(generatedMarkerPath(svc.PosterPath("media-alpha")), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{"w320", "w640"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(svc.PosterPath("media-alpha")), "poster."+bucket+".jpg"), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.GeneratePoster(context.Background(), "media-alpha"); err != nil {
		t.Fatal(err)
	}
	if len(grabber.embeddedCalls) != 1 || grabber.embeddedCalls[0] != src || len(grabber.calls) != 0 {
		t.Fatalf("embedded calls = %v, frame calls = %v", grabber.embeddedCalls, grabber.calls)
	}
	if svc.IsGeneratedArtwork("media-alpha", SlotPoster) {
		t.Fatal("embedded poster carried generated provenance")
	}
	if !svc.IsEmbeddedArtwork("media-alpha", SlotPoster) {
		t.Fatal("embedded poster lost its embedded provenance")
	}
	poster, err := os.ReadFile(svc.PosterPath("media-alpha"))
	if err != nil || string(poster) != "embedded jpeg" {
		t.Fatalf("poster = %q, %v", poster, err)
	}
	for _, bucket := range []string{"w320", "w640"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(svc.PosterPath("media-alpha")), "poster."+bucket+".jpg")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale %s derivative survived: %v", bucket, err)
		}
	}
}

func TestGeneratePosterReturnsEmbeddedExtractionError(t *testing.T) {
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-alpha")
	grabber := &fakeGrabber{embeddedErr: errors.New("bad embedded image")}
	svc := NewService(st, grabber, t.TempDir())

	if err := svc.GeneratePoster(context.Background(), "media-alpha"); err == nil || err.Error() != "bad embedded image" {
		t.Fatalf("GeneratePoster error = %v", err)
	}
	if len(grabber.calls) != 0 {
		t.Fatalf("embedded extraction error fell through to frame grab: %v", grabber.calls)
	}
}

func TestGeneratePosterCoversRestrictedMedia(t *testing.T) {
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-archive")
	svc := NewService(st, &fakeGrabber{}, t.TempDir())

	// Generation is server-internal: restricted media gets a poster at rest;
	// the serving boundary reveal-filters it.
	if err := svc.GeneratePoster(context.Background(), "media-archive"); err != nil {
		t.Fatalf("GeneratePoster returned error: %v", err)
	}
	if !svc.HasPoster("media-archive") {
		t.Fatal("HasPoster = false for restricted media")
	}
}

func TestGeneratePosterDecodesImageWithScaler(t *testing.T) {
	st := storetest.Open(t)
	id, src := importImageFile(t, st, "media-image-heic", "heic")
	scaler := &fakeScaler{}
	svc := NewService(st, scaler, t.TempDir())

	if err := svc.GeneratePoster(context.Background(), id); err != nil {
		t.Fatalf("GeneratePoster returned error: %v", err)
	}
	if len(scaler.scaleCalls) != 1 || scaler.scaleCalls[0] != posterWidth {
		t.Fatalf("scaleCalls = %v, want one call at %d", scaler.scaleCalls, posterWidth)
	}
	if len(scaler.calls) != 0 {
		t.Fatalf("frame grab calls = %v, want none for still image %s", scaler.calls, src)
	}
	fields, err := st.MediaFields(context.Background(), id, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mediaFieldFlag(fields, PreviewUnavailableField); got {
		t.Fatalf("%s = true after successful decode, want false", PreviewUnavailableField)
	}
}

func TestGeneratePosterFlagsUndecodableImage(t *testing.T) {
	st := storetest.Open(t)
	id, _ := importImageFile(t, st, "media-image-raw", "dng")
	svc := NewService(st, &fakeScaler{scaleErr: errors.New("unsupported codec")}, t.TempDir())

	if err := svc.GeneratePoster(context.Background(), id); err == nil {
		t.Fatal("GeneratePoster returned nil for undecodable image")
	}
	if svc.HasPoster(id) {
		t.Fatal("undecodable image wrote a poster")
	}
	fields, err := st.MediaFields(context.Background(), id, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mediaFieldFlag(fields, PreviewUnavailableField); !got {
		t.Fatalf("%s = false, want true", PreviewUnavailableField)
	}
}

func mediaFieldFlag(fields []store.MediaField, name string) bool {
	for _, field := range fields {
		if field.Name == name && field.Type == store.EntityFieldFlag {
			return field.Flag
		}
	}
	return false
}

func TestSweepJobGeneratesMissingPostersAndCompletes(t *testing.T) {
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-alpha")
	setPrimaryFile(t, st, "media-archive")
	grabber := &fakeGrabber{}
	svc := NewService(st, grabber, t.TempDir())

	now := time.Unix(100, 0).UTC()
	job, err := NewSweepJob(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(SweepJobKind, SweepHandler(svc))
	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}

	if !svc.HasPoster("media-alpha") || !svc.HasPoster("media-archive") {
		t.Fatal("sweep left posters missing")
	}
}

func TestSweepJobSurvivesGrabFailures(t *testing.T) {
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-alpha")
	svc := NewService(st, &fakeGrabber{err: errors.New("boom")}, t.TempDir())

	now := time.Unix(100, 0).UTC()
	job, err := NewSweepJob(now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.EnqueueJob(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(SweepJobKind, SweepHandler(svc))
	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	loaded, err := st.Job(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed despite grab failures", loaded.Status)
	}
}

func TestSweepHandlerCompletesExactFinalBatchWithoutExtraInvocation(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	ids, err := st.MediaIDsAfter(ctx, "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	needed := (sweepJobBatchSize - len(ids)%sweepJobBatchSize) % sweepJobBatchSize
	for i := 0; i < needed; i++ {
		importImageFile(t, st, fmt.Sprintf("batch-fill-%d", i), "jpg")
	}
	ids, err = st.MediaIDsAfter(ctx, "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 || len(ids)%sweepJobBatchSize != 0 {
		t.Fatalf("fixture count = %d, want positive multiple of %d", len(ids), sweepJobBatchSize)
	}
	created, err := st.EnqueueJob(ctx, store.Job{ID: "job-artwork-exact", Kind: SweepJobKind, Class: store.JobClassBackground, DedupeKey: "artwork-exact", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimNextJob(ctx, "worker", []store.JobClass{store.JobClassBackground}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != created.ID {
		t.Fatalf("claimed job %s, want %s", job.ID, created.ID)
	}
	handler := SweepHandler(NewService(st, &fakeScaler{}, t.TempDir()))
	invocations := 0
	for {
		invocations++
		result, err := handler(ctx, job)
		if err == nil {
			break
		}
		if !errors.Is(err, jobs.ErrYielded) {
			t.Fatal(err)
		}
		job.Checkpoint = result.Checkpoint
	}
	if want := len(ids) / sweepJobBatchSize; invocations != want {
		t.Fatalf("handler invocations = %d, want %d", invocations, want)
	}
}

// fakeScaler is a FrameGrabber that also implements ImageScaler, like the
// real *ffmpeg.Runner.
type fakeScaler struct {
	fakeGrabber
	scaleCalls []int
	scaleErr   error
}

func (f *fakeScaler) ScaleImage(_ context.Context, _ string, width int, dst string) error {
	f.scaleCalls = append(f.scaleCalls, width)
	if f.scaleErr != nil {
		return f.scaleErr
	}
	return os.WriteFile(dst, []byte("scaled jpeg"), 0o600)
}

func writePoster(t *testing.T, svc *Service, id store.MediaID) {
	t.Helper()
	path := svc.PosterPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("full jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidBucket(t *testing.T) {
	// w400/w1920 are the image-Media derivatives: the 400px
	// grid thumbnail and the 1920px lightbox preview, alongside the poster
	// buckets.
	for _, name := range []string{"", "original", "w320", "w400", "w640", "w1280", "w1920"} {
		if !ValidBucket(name) {
			t.Errorf("ValidBucket(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"w999", "320", "poster", "W320", "thumb"} {
		if ValidBucket(name) {
			t.Errorf("ValidBucket(%q) = true, want false", name)
		}
	}
}

func TestValidSlot(t *testing.T) {
	for _, name := range []string{SlotPoster, SlotBackdrop, SlotThumb, SlotLogo, SlotBanner, SlotClearart, SlotDisc, SlotContainerPoster, "season-poster"} {
		if !ValidSlot(name) {
			t.Errorf("ValidSlot(%q) = false, want true", name)
		}
	}
	// portrait is an SDK slot Core does not serve yet.
	for _, name := range []string{"", "portrait", "Poster", "Logo", "w320"} {
		if ValidSlot(name) {
			t.Errorf("ValidSlot(%q) = true, want false", name)
		}
	}
}

func TestPosterDerivativeGeneratesLazilyAndCaches(t *testing.T) {
	scaler := &fakeScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	writePoster(t, svc, "media-alpha")

	path, err := svc.PosterDerivative(context.Background(), "media-alpha", "w320")
	if err != nil {
		t.Fatalf("PosterDerivative returned error: %v", err)
	}
	if want := filepath.Join(filepath.Dir(svc.PosterPath("media-alpha")), "poster.w320.jpg"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "scaled jpeg" {
		t.Fatalf("derivative file = %q, %v; want cached scaled bytes", data, err)
	}

	// Second request is a cache hit: no further scaling.
	again, err := svc.PosterDerivative(context.Background(), "media-alpha", "w320")
	if err != nil || again != path {
		t.Fatalf("second PosterDerivative = %q, %v; want cached %q", again, err, path)
	}
	if len(scaler.scaleCalls) != 1 || scaler.scaleCalls[0] != 320 {
		t.Fatalf("scaleCalls = %v, want one call at width 320", scaler.scaleCalls)
	}
}

func TestPosterDerivativeOriginalBypassesScaling(t *testing.T) {
	scaler := &fakeScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	writePoster(t, svc, "media-alpha")

	for _, bucket := range []string{"", "original"} {
		path, err := svc.PosterDerivative(context.Background(), "media-alpha", bucket)
		if err != nil || path != svc.PosterPath("media-alpha") {
			t.Fatalf("PosterDerivative(%q) = %q, %v; want full-size path", bucket, path, err)
		}
	}
	if len(scaler.scaleCalls) != 0 {
		t.Fatalf("scaleCalls = %v, want none for original", scaler.scaleCalls)
	}
}

func TestPosterDerivativeRejectsUnknownBucket(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeScaler{}, t.TempDir())
	writePoster(t, svc, "media-alpha")

	if _, err := svc.PosterDerivative(context.Background(), "media-alpha", "w999"); err == nil {
		t.Fatal("PosterDerivative accepted unknown bucket")
	}
}

func TestPosterDerivativeFallsBackToOriginalOnScaleFailure(t *testing.T) {
	scaler := &fakeScaler{scaleErr: errors.New("boom")}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	writePoster(t, svc, "media-alpha")

	path, err := svc.PosterDerivative(context.Background(), "media-alpha", "w320")
	if err != nil || path != svc.PosterPath("media-alpha") {
		t.Fatalf("PosterDerivative = %q, %v; want full-size fallback", path, err)
	}
}

func TestPosterDerivativeFallsBackWithoutScaler(t *testing.T) {
	// A grabber that doesn't implement ImageScaler (or a nil grabber) leaves
	// the service scaler-less; buckets honestly serve the full-size file.
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	writePoster(t, svc, "media-alpha")

	path, err := svc.PosterDerivative(context.Background(), "media-alpha", "w320")
	if err != nil || path != svc.PosterPath("media-alpha") {
		t.Fatalf("PosterDerivative = %q, %v; want full-size fallback", path, err)
	}
}

func TestArtworkDerivativeUsesRequestedSlot(t *testing.T) {
	scaler := &fakeScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	if err := svc.ImportArtwork(context.Background(), "media-alpha", SlotBackdrop, []byte("backdrop")); err != nil {
		t.Fatalf("ImportArtwork returned error: %v", err)
	}

	path, err := svc.ArtworkDerivative(context.Background(), "media-alpha", SlotBackdrop, "w320")
	if err != nil {
		t.Fatalf("ArtworkDerivative returned error: %v", err)
	}
	if want := filepath.Join(filepath.Dir(svc.PosterPath("media-alpha")), "backdrop.w320.jpg"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

// copyScaler is an ImageScaler that copies the source bytes verbatim into the
// derivative, so a regenerated bucket observably reflects the current original.
type copyScaler struct {
	fakeGrabber
	calls int
}

type transitionScaler struct {
	fakeGrabber
	calls   int
	onScale func(int)
}

func (s *transitionScaler) ScaleImage(_ context.Context, src string, _ int, dst string) error {
	s.calls++
	if s.onScale != nil {
		s.onScale(s.calls)
	}
	// #nosec G304 -- src is a test-owned temporary image path.
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// #nosec G703 -- dst is a test-owned temporary derivative path.
	return os.WriteFile(dst, data, 0o600)
}

type gatedScaler struct {
	fakeGrabber
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
	calls    atomic.Int64
	inFlight atomic.Int64
	peak     atomic.Int64
}

func (s *gatedScaler) ScaleImage(_ context.Context, src string, _ int, dst string) error {
	s.calls.Add(1)
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for peak := s.peak.Load(); n > peak && !s.peak.CompareAndSwap(peak, n); peak = s.peak.Load() {
	}
	s.once.Do(func() { close(s.started) })
	<-s.release
	// #nosec G304 -- src is a test-owned temporary image path.
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// #nosec G703 -- dst is a test-owned temporary derivative path.
	return os.WriteFile(dst, data, 0o600)
}

func (c *copyScaler) ScaleImage(_ context.Context, src string, _ int, dst string) error {
	c.calls++
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func TestDeriveImageIsFingerprintResumableAndRegeneratesChangedSource(t *testing.T) {
	st := storetest.Open(t)
	id, path := importImageFile(t, st, "photo", "jpg")
	scaler := &copyScaler{}
	svc := NewService(st, scaler, t.TempDir())
	if err := svc.DeriveImage(context.Background(), id, path, "fingerprint-1"); err != nil {
		t.Fatal(err)
	}
	if scaler.calls != 3 {
		t.Fatalf("initial scale calls = %d, want poster + two buckets", scaler.calls)
	}
	if err := svc.DeriveImage(context.Background(), id, path, "fingerprint-1"); err != nil {
		t.Fatal(err)
	}
	if scaler.calls != 3 {
		t.Fatalf("resumed scale calls = %d, want fresh derivatives skipped", scaler.calls)
	}
	if err := os.WriteFile(path, []byte("changed image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeriveImage(context.Background(), id, path, "fingerprint-2"); err != nil {
		t.Fatal(err)
	}
	if scaler.calls != 6 {
		t.Fatalf("changed-source scale calls = %d, want one regeneration", scaler.calls)
	}
	preview := filepath.Join(filepath.Dir(svc.PosterPath(id)), SlotPoster+".w1920.jpg")
	// #nosec G304 -- preview is derived from the test-owned temporary artwork root.
	data, err := os.ReadFile(preview)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "changed image" {
		t.Fatalf("preview = %q, want changed source", data)
	}
}

func TestDeriveImageRegeneratesDerivativesFromAnOlderScaler(t *testing.T) {
	st := storetest.Open(t)
	id, path := importImageFile(t, st, "still", "jpg")
	scaler := &copyScaler{}
	svc := NewService(st, scaler, t.TempDir())
	if err := svc.DeriveImage(context.Background(), id, path, "fingerprint-1"); err != nil {
		t.Fatal(err)
	}
	// A marker written before imageDerivativeVersion held the bare fingerprint.
	if err := os.WriteFile(svc.PosterPath(id)+".source-fingerprint", []byte("fingerprint-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := svc.DeriveImage(context.Background(), id, path, "fingerprint-1"); err != nil {
			t.Fatal(err)
		}
	}
	if scaler.calls != 6 {
		t.Fatalf("scale calls = %d, want one regeneration for the unversioned marker", scaler.calls)
	}
}

func TestDeriveImageVersionBumpKeepsAnImportedPoster(t *testing.T) {
	for _, c := range []struct {
		name    string
		marker  string   // a bare fingerprint is the marker a pre-version scaler wrote
		present []string // buckets already cached before the scan
		scales  int
	}{
		{"pre-version, no buckets", "fingerprint-1", nil, 2},
		{"pre-version, one bucket", "fingerprint-1", []string{"w400"}, 2},
		{"pre-version, all buckets", "fingerprint-1", []string{"w400", "w1920"}, 2},
		{"current, one bucket", imageDerivativeVersion + ":fingerprint-1", []string{"w400"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := storetest.Open(t)
			id, path := importImageFile(t, st, "still", "jpg")
			scaler := &copyScaler{}
			svc := NewService(st, scaler, t.TempDir())
			if err := svc.DeriveImage(context.Background(), id, path, "fingerprint-1"); err != nil {
				t.Fatal(err)
			}
			// Importing removes the buckets.
			if err := svc.ImportArtwork(context.Background(), id, SlotPoster, []byte("chosen poster")); err != nil {
				t.Fatal(err)
			}
			marker := svc.PosterPath(id) + ".source-fingerprint"
			if err := os.WriteFile(marker, []byte(c.marker+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(svc.PosterPath(id))
			content := "old scaler bucket"
			if c.marker != "fingerprint-1" {
				content = "chosen poster"
			}
			for _, bucket := range c.present {
				if err := os.WriteFile(filepath.Join(dir, SlotPoster+"."+bucket+".jpg"), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := scaler.calls
			if err := svc.DeriveImage(context.Background(), id, path, "fingerprint-1"); err != nil {
				t.Fatal(err)
			}
			if scaler.calls != before+c.scales {
				t.Fatalf("scale calls = %d, want %d", scaler.calls, before+c.scales)
			}
			for _, name := range []string{SlotPoster + ".jpg", SlotPoster + ".w400.jpg", SlotPoster + ".w1920.jpg"} {
				// #nosec G304 -- the path is under the test-owned artwork root.
				if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != "chosen poster" {
					t.Fatalf("%s = %q, %v; want the imported poster", name, data, err)
				}
			}
			// #nosec G304 -- the path is under the test-owned artwork root.
			if data, err := os.ReadFile(marker); err != nil || string(data) != imageDerivativeVersion+":fingerprint-1\n" {
				t.Fatalf("marker = %q, %v; want the current stamp", data, err)
			}
			// The rebuilt buckets carry the current stamp: the next scan is a no-op.
			before = scaler.calls
			if err := svc.DeriveImage(context.Background(), id, path, "fingerprint-1"); err != nil {
				t.Fatal(err)
			}
			if scaler.calls != before {
				t.Fatalf("scale calls = %d after a current rescan, want %d", scaler.calls, before)
			}
		})
	}
}

func TestDeriveImageWritesNothingForProtectedLibrary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*Service)
	}{
		{name: "sealed", configure: func(s *Service) { s.SetSealed(func(store.LibraryID) bool { return true }) }},
		{name: "encrypted", configure: func(s *Service) { s.SetEncrypted(func(store.LibraryID) bool { return true }) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.Open(t)
			id, path := importImageFile(t, st, "photo", "jpg")
			scaler := &fakeScaler{}
			svc := NewService(st, scaler, t.TempDir())
			tc.configure(svc)
			if err := svc.DeriveImage(context.Background(), id, path, "fingerprint"); err != nil {
				t.Fatal(err)
			}
			if len(scaler.scaleCalls) != 0 || svc.HasPoster(id) {
				t.Fatalf("protected library wrote plaintext derivatives: calls=%v poster=%v", scaler.scaleCalls, svc.HasPoster(id))
			}
		})
	}
}

func TestDeriveImagePurgesOutputsWhenLibraryBecomesProtectedMidDerive(t *testing.T) {
	st := storetest.Open(t)
	id, path := importImageFile(t, st, "photo", "jpg")
	var protected atomic.Bool
	scaler := &transitionScaler{onScale: func(call int) {
		if call == 3 {
			protected.Store(true)
		}
	}}
	svc := NewService(st, scaler, t.TempDir())
	svc.SetSealed(func(store.LibraryID) bool { return protected.Load() })
	if err := svc.DeriveImage(context.Background(), id, path, "fingerprint"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(svc.PosterPath(id))); !os.IsNotExist(err) {
		t.Fatalf("protected transition left plaintext artwork set: %v", err)
	}
}

func TestDeriveImagePurgesOutputWhenProtectionChangesAfterRename(t *testing.T) {
	st := storetest.Open(t)
	id, path := importImageFile(t, st, "photo", "jpg")
	scaler := &copyScaler{}
	svc := NewService(st, scaler, t.TempDir())
	var checks atomic.Int64
	svc.SetSealed(func(store.LibraryID) bool { return checks.Add(1) >= 3 })
	if err := svc.DeriveImage(context.Background(), id, path, "fingerprint"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(svc.PosterPath(id))); !os.IsNotExist(err) {
		t.Fatalf("post-rename protection transition left plaintext artwork set: %v", err)
	}
}

func TestDeriveImageAndLazyDerivativeShareMediaLock(t *testing.T) {
	st := storetest.Open(t)
	id, path := importImageFile(t, st, "photo", "jpg")
	scaler := &gatedScaler{started: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(st, scaler, t.TempDir())
	poster := svc.PosterPath(id)
	if err := os.MkdirAll(filepath.Dir(poster), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poster, []byte("old poster"), 0o600); err != nil {
		t.Fatal(err)
	}
	deriveDone := make(chan error, 1)
	go func() { deriveDone <- svc.DeriveImage(context.Background(), id, path, "fingerprint") }()
	<-scaler.started
	lazyDone := make(chan error, 1)
	go func() {
		_, err := svc.PosterDerivative(context.Background(), id, "w640")
		lazyDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if calls := scaler.calls.Load(); calls != 1 {
		t.Fatalf("lazy derivative entered scaler concurrently: calls=%d", calls)
	}
	close(scaler.release)
	if err := <-deriveDone; err != nil {
		t.Fatal(err)
	}
	if err := <-lazyDone; err != nil {
		t.Fatal(err)
	}
	if peak := scaler.peak.Load(); peak != 1 {
		t.Fatalf("peak concurrent same-media scaling = %d, want 1", peak)
	}
}

func TestArtworkVersionIsContentAddressed(t *testing.T) {
	svc := NewService(storetest.Open(t), nil, t.TempDir())
	poster := svc.PosterPath("media-alpha")
	if err := os.MkdirAll(filepath.Dir(poster), 0o700); err != nil {
		t.Fatal(err)
	}

	// Missing slot has no version.
	if v, ok := svc.ArtworkVersion("media-alpha", SlotPoster); ok {
		t.Fatalf("ArtworkVersion before write = (%q, true), want absent", v)
	}

	if err := os.WriteFile(poster, []byte("poster bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, ok := svc.ArtworkVersion("media-alpha", SlotPoster)
	if !ok || first == "" {
		t.Fatalf("ArtworkVersion after write = (%q, %v), want non-empty token", first, ok)
	}

	// ASSET-2 acceptance: the URL must be STABLE across a re-scrape of identical
	// bytes. A re-identify/re-scrape rewrites the file (touching mtime) but with
	// the same bytes — a content-addressed token must NOT change, or every
	// re-scrape needlessly busts every cache. os.Chtimes simulates the mtime
	// churn deterministically; the content is unchanged.
	newer := time.Now().Add(time.Hour)
	if err := os.Chtimes(poster, newer, newer); err != nil {
		t.Fatal(err)
	}
	rescraped, ok := svc.ArtworkVersion("media-alpha", SlotPoster)
	if !ok {
		t.Fatal("ArtworkVersion after re-scrape = absent, want present")
	}
	if rescraped != first {
		t.Fatalf("ArtworkVersion changed across re-scrape of identical bytes: %q -> %q "+
			"(must be content-addressed, not mtime)", first, rescraped)
	}

	// New bytes (genuinely new artwork) must yield a DIFFERENT token — the
	// cache-busting half of the content-address contract.
	if err := os.WriteFile(poster, []byte("different poster bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, ok := svc.ArtworkVersion("media-alpha", SlotPoster)
	if !ok {
		t.Fatal("ArtworkVersion after byte change = absent, want present")
	}
	if changed == first {
		t.Fatalf("ArtworkVersion did not change after the bytes changed: %q", changed)
	}

	// An unknown slot name is never path-addressable.
	if v, ok := svc.ArtworkVersion("media-alpha", "bogus"); ok {
		t.Fatalf("ArtworkVersion(unknown slot) = (%q, true), want absent", v)
	}
}

func TestResolveSlotFallsBackToPoster(t *testing.T) {
	svc := NewService(storetest.Open(t), nil, t.TempDir())

	// No artwork at all: nothing resolves for any slot.
	for _, slot := range KnownSlots {
		if served, ok := svc.ResolveSlot("media-alpha", slot); ok {
			t.Fatalf("ResolveSlot(%q) before any artwork = (%q, true), want absent", slot, served)
		}
	}

	writePoster(t, svc, "media-alpha")

	// The requested slot's own file wins when present.
	if served, ok := svc.ResolveSlot("media-alpha", SlotPoster); !ok || served != SlotPoster {
		t.Fatalf("ResolveSlot(poster) = (%q, %v), want (poster, true)", served, ok)
	}
	// A slot with no file falls back to the poster — resolved server-side
	// so the client never re-implements the chain.
	for _, slot := range []string{SlotBackdrop, SlotThumb} {
		served, ok := svc.ResolveSlot("media-alpha", slot)
		if !ok || served != SlotPoster {
			t.Fatalf("ResolveSlot(%q) = (%q, %v), want (poster, true)", slot, served, ok)
		}
	}
	// A dedicated backdrop, once present, serves itself rather than the poster.
	if err := svc.ImportArtwork(context.Background(), "media-alpha", SlotBackdrop, []byte("backdrop")); err != nil {
		t.Fatal(err)
	}
	if served, ok := svc.ResolveSlot("media-alpha", SlotBackdrop); !ok || served != SlotBackdrop {
		t.Fatalf("ResolveSlot(backdrop) with own file = (%q, %v), want (backdrop, true)", served, ok)
	}
	// An unknown slot never resolves.
	if served, ok := svc.ResolveSlot("media-alpha", "bogus"); ok {
		t.Fatalf("ResolveSlot(unknown) = (%q, true), want absent", served)
	}
}

func TestWidenedSlotsResolveDeriveAndFallBack(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &fakeScaler{}, t.TempDir())
	writePoster(t, svc, "media-alpha")

	// The container poster falls back to the poster; logo, banner, clearart
	// and disc have no fallback and serve nothing while absent.
	if served, ok := svc.ResolveSlot("media-alpha", SlotContainerPoster); !ok || served != SlotPoster {
		t.Fatalf("ResolveSlot(season-poster) = (%q, %v), want (poster, true)", served, ok)
	}
	for _, slot := range []string{SlotLogo, SlotBanner, SlotClearart, SlotDisc} {
		if served, ok := svc.ResolveSlot("media-alpha", slot); ok {
			t.Fatalf("ResolveSlot(%q) without its file = (%q, true), want absent", slot, served)
		}
	}

	// Imported bytes win over the fallback, and every new slot derives
	// its own bucket next to its original.
	for _, slot := range []string{SlotLogo, SlotBanner, SlotClearart, SlotDisc, SlotContainerPoster} {
		if err := svc.ImportArtwork(ctx, "media-alpha", slot, []byte(slot+"-bytes")); err != nil {
			t.Fatalf("ImportArtwork(%q): %v", slot, err)
		}
		if served, ok := svc.ResolveSlot("media-alpha", slot); !ok || served != slot {
			t.Fatalf("ResolveSlot(%q) with own file = (%q, %v), want (%q, true)", slot, served, ok, slot)
		}
		if _, ok := svc.ArtworkVersion("media-alpha", slot); !ok {
			t.Fatalf("ArtworkVersion(%q) absent after import", slot)
		}
		path, err := svc.ArtworkDerivative(ctx, "media-alpha", slot, "w640")
		if err != nil {
			t.Fatalf("ArtworkDerivative(%q): %v", slot, err)
		}
		// Logo, clearart and disc are PNG; their buckets are too.
		ext := filepath.Ext(svc.ArtworkPath("media-alpha", slot))
		if want := filepath.Join(filepath.Dir(svc.PosterPath("media-alpha")), slot+".w640"+ext); path != want {
			t.Fatalf("ArtworkDerivative(%q) = %q, want %q", slot, path, want)
		}
	}

	// A slot outside the vocabulary is not importable or addressable.
	if err := svc.ImportArtwork(ctx, "media-alpha", "portrait", []byte("x")); err == nil {
		t.Fatal("ImportArtwork(portrait) succeeded, want unknown-slot error")
	}
	if _, err := svc.ArtworkDerivative(ctx, "media-alpha", "portrait", "w640"); err == nil {
		t.Fatal("ArtworkDerivative(portrait) succeeded, want unknown-slot error")
	}
}

func TestResolveEntitySlotFallsBackToPoster(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	if err := svc.ImportEntityPoster(context.Background(), "entity-maker", []byte("poster")); err != nil {
		t.Fatal(err)
	}
	if served, ok := svc.ResolveEntitySlot("entity-maker", SlotPoster); !ok || served != SlotPoster {
		t.Fatalf("ResolveEntitySlot(poster) = (%q, %v), want (poster, true)", served, ok)
	}
	if served, ok := svc.ResolveEntitySlot("entity-maker", SlotBackdrop); !ok || served != SlotPoster {
		t.Fatalf("ResolveEntitySlot(backdrop) = (%q, %v), want (poster fallback, true)", served, ok)
	}
	if served, ok := svc.ResolveEntitySlot("entity-empty", SlotPoster); ok {
		t.Fatalf("ResolveEntitySlot with no artwork = (%q, true), want absent", served)
	}
}

func TestArtworkDerivativeRegeneratesWhenOriginalIsNewer(t *testing.T) {
	scaler := &copyScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())

	original := svc.PosterPath("media-alpha")
	if err := os.MkdirAll(filepath.Dir(original), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := svc.PosterDerivative(context.Background(), "media-alpha", "w320")
	if err != nil {
		t.Fatalf("PosterDerivative returned error: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "first" {
		t.Fatalf("derivative = %q, %v; want bytes of first original", data, err)
	}
	if scaler.calls != 1 {
		t.Fatalf("scale calls = %d after first request, want 1", scaler.calls)
	}

	// Replace the original with newer content + a strictly newer mtime: the
	// next request must regenerate the bucket from the new original (the URL's
	// ?v changes but the cached derivative bytes would otherwise be stale).
	if err := os.WriteFile(original, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	newer := time.Now().Add(time.Hour)
	if err := os.Chtimes(original, newer, newer); err != nil {
		t.Fatal(err)
	}
	path, err = svc.PosterDerivative(context.Background(), "media-alpha", "w320")
	if err != nil {
		t.Fatalf("PosterDerivative (after rewrite) returned error: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "second" {
		t.Fatalf("derivative after rewrite = %q, %v; want bytes of new original", data, err)
	}
	if scaler.calls != 2 {
		t.Fatalf("scale calls = %d after rewrite, want 2 (stale bucket regenerated)", scaler.calls)
	}
}

func TestImportArtworkInvalidatesOnlyRequestedSlot(t *testing.T) {
	scaler := &fakeScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	dir := filepath.Dir(svc.PosterPath("media-alpha"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	posterDerivative := filepath.Join(dir, "poster.w320.jpg")
	backdropDerivative := filepath.Join(dir, "backdrop.w320.jpg")
	if err := os.WriteFile(posterDerivative, []byte("old poster derivative"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backdropDerivative, []byte("old backdrop derivative"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := svc.ImportArtwork(context.Background(), "media-alpha", SlotBackdrop, []byte("downloaded")); err != nil {
		t.Fatalf("ImportArtwork returned error: %v", err)
	}

	if _, err := os.Stat(backdropDerivative); !os.IsNotExist(err) {
		t.Fatalf("backdrop derivative survived import (stat err = %v)", err)
	}
	if _, err := os.Stat(posterDerivative); err != nil {
		t.Fatalf("poster derivative was invalidated by backdrop import: %v", err)
	}
}

func TestGenerateDerivativesFillsBucketSet(t *testing.T) {
	scaler := &fakeScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	if err := os.MkdirAll(filepath.Dir(svc.PosterPath("media-alpha")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.PosterPath("media-alpha"), []byte("jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}

	svc.GenerateDerivatives(context.Background(), "media-alpha")

	for bucket := range bucketWidths {
		path := filepath.Join(filepath.Dir(svc.PosterPath("media-alpha")), SlotPoster+"."+bucket+".jpg")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("bucket %s derivative missing: %v", bucket, err)
		}
	}

	// Second pass is all cache hits — no further scale calls.
	calls := len(scaler.scaleCalls)
	svc.GenerateDerivatives(context.Background(), "media-alpha")
	if len(scaler.scaleCalls) != calls {
		t.Fatalf("scale calls = %d after cached pass, want %d", len(scaler.scaleCalls), calls)
	}
}

func TestImportPosterNormalizesAndInvalidatesDerivatives(t *testing.T) {
	scaler := &fakeScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	dst := svc.PosterPath("media-alpha")

	// A stale derivative from a previous poster must not survive the import.
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(filepath.Dir(dst), "poster.w320.jpg")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := svc.ImportPoster(context.Background(), "media-alpha", []byte("downloaded")); err != nil {
		t.Fatalf("ImportPoster returned error: %v", err)
	}
	if len(scaler.scaleCalls) != 1 || scaler.scaleCalls[0] != posterWidth {
		t.Fatalf("scale calls = %v, want one at %d", scaler.scaleCalls, posterWidth)
	}
	if !svc.HasPoster("media-alpha") {
		t.Fatal("HasPoster = false after import")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale derivative survived import (stat err = %v)", err)
	}
}

func TestImportPosterWithoutScalerStoresRawBytes(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	if err := svc.ImportPoster(context.Background(), "media-alpha", []byte("raw-jpeg")); err != nil {
		t.Fatalf("ImportPoster returned error: %v", err)
	}
	data, err := os.ReadFile(svc.PosterPath("media-alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "raw-jpeg" {
		t.Fatalf("stored poster = %q, want raw bytes", data)
	}
}

func TestImportPosterRejectsEmptyData(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	if err := svc.ImportPoster(context.Background(), "media-alpha", nil); err == nil {
		t.Fatal("ImportPoster accepted empty data")
	}
}

func TestImportEntityPosterRoundTrip(t *testing.T) {
	scaler := &fakeScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	if svc.HasEntityPoster("entity-maker") {
		t.Fatal("HasEntityPoster = true before import")
	}

	if err := svc.ImportEntityPoster(context.Background(), "entity-maker", []byte("downloaded")); err != nil {
		t.Fatalf("ImportEntityPoster returned error: %v", err)
	}

	if !svc.HasEntityPoster("entity-maker") {
		t.Fatal("HasEntityPoster = false after import")
	}
	// Entity posters live in their own namespace: a Media item with the same
	// base ID never shares a path.
	if svc.EntityPosterPath("entity-maker") == svc.PosterPath("entity-maker") {
		t.Fatal("entity poster path collides with media poster path")
	}
	// The shared derivative pipeline serves bucketed entity posters too.
	path, err := svc.EntityPosterDerivative(context.Background(), "entity-maker", "w320")
	if err != nil {
		t.Fatalf("EntityPosterDerivative returned error: %v", err)
	}
	if want := filepath.Join(filepath.Dir(svc.EntityPosterPath("entity-maker")), "poster.w320.jpg"); path != want {
		t.Fatalf("derivative path = %q, want %q", path, want)
	}
}

func TestEntityPosterDerivativeFallsBackWithoutScaler(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	if err := svc.ImportEntityPoster(context.Background(), "entity-maker", []byte("raw-jpeg")); err != nil {
		t.Fatalf("ImportEntityPoster returned error: %v", err)
	}

	path, err := svc.EntityPosterDerivative(context.Background(), "entity-maker", "w320")
	if err != nil || path != svc.EntityPosterPath("entity-maker") {
		t.Fatalf("EntityPosterDerivative = %q, %v; want full-size fallback", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "raw-jpeg" {
		t.Fatalf("entity poster = %q, %v; want raw bytes", data, err)
	}
}

func TestRemoveEntityArtworkDeletesPosterAndDerivatives(t *testing.T) {
	scaler := &fakeScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	if err := svc.ImportEntityPoster(context.Background(), "entity-maker", []byte("downloaded")); err != nil {
		t.Fatalf("ImportEntityPoster returned error: %v", err)
	}
	if _, err := svc.EntityPosterDerivative(context.Background(), "entity-maker", "w320"); err != nil {
		t.Fatalf("EntityPosterDerivative returned error: %v", err)
	}

	if err := svc.RemoveEntityArtwork("entity-maker"); err != nil {
		t.Fatalf("RemoveEntityArtwork returned error: %v", err)
	}
	if svc.HasEntityPoster("entity-maker") {
		t.Fatal("poster survived removal")
	}
	if _, err := os.Stat(filepath.Dir(svc.EntityPosterPath("entity-maker"))); !os.IsNotExist(err) {
		t.Fatalf("entity artwork directory survived removal: %v", err)
	}

	// Removing artwork that never existed is a no-op, not an error.
	if err := svc.RemoveEntityArtwork("entity-never"); err != nil {
		t.Fatalf("RemoveEntityArtwork on missing dir = %v", err)
	}
}

// TestSealedLibrarySkipsPosterGenerationAndImport: a Sealed Library gets no
// plaintext poster on disk from either the generate path or the artwork import
// path (ENCPRIV-10b-LIVE / ADR-0098 area 2). Both fail closed — no frame is
// grabbed and no import file is written.
func TestSealedLibrarySkipsPosterGenerationAndImport(t *testing.T) {
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-alpha")
	grabber := &fakeGrabber{}
	svc := NewService(st, grabber, t.TempDir())
	libraryID, err := st.MediaLibraryID(context.Background(), "media-alpha")
	if err != nil {
		t.Fatalf("MediaLibraryID returned error: %v", err)
	}
	svc.SetSealed(func(id store.LibraryID) bool { return id == libraryID })

	if err := svc.GeneratePoster(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GeneratePoster on sealed-Library media returned error: %v", err)
	}
	if len(grabber.calls) != 0 {
		t.Fatalf("frame grabber called %d times for a sealed Library; want 0", len(grabber.calls))
	}
	if svc.HasPoster("media-alpha") {
		t.Fatal("sealed-Library media got a plaintext generated poster")
	}

	if err := svc.ImportPoster(context.Background(), "media-alpha", []byte("plaintext poster bytes")); err != nil {
		t.Fatalf("ImportPoster on sealed-Library media returned error: %v", err)
	}
	if svc.HasPoster("media-alpha") {
		t.Fatal("sealed-Library media got a plaintext imported poster")
	}
	if _, err := os.Stat(svc.PosterPath("media-alpha")); !os.IsNotExist(err) {
		t.Fatalf("plaintext poster file exists for a sealed Library: %v", err)
	}
}

func TestEncryptedUnsealedLibrarySkipsPosterGenerationAndImport(t *testing.T) {
	// ENCPRIV-10b: a generated/imported poster is a plaintext derivative of the
	// user's Media. An Encrypted Library that is currently Unsealed (sealed
	// predicate false) must still fail closed — no frame grab, no file.
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-alpha")
	grabber := &fakeGrabber{}
	svc := NewService(st, grabber, t.TempDir())
	libraryID, err := st.MediaLibraryID(context.Background(), "media-alpha")
	if err != nil {
		t.Fatalf("MediaLibraryID returned error: %v", err)
	}
	svc.SetSealed(func(store.LibraryID) bool { return false })
	svc.SetEncrypted(func(id store.LibraryID) bool { return id == libraryID })

	if err := svc.GeneratePoster(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GeneratePoster on encrypted-Library media returned error: %v", err)
	}
	if len(grabber.calls) != 0 {
		t.Fatalf("frame grabber called %d times for an Unsealed encrypted Library; want 0", len(grabber.calls))
	}
	if svc.HasPoster("media-alpha") {
		t.Fatal("Unsealed encrypted-Library media got a plaintext generated poster")
	}

	if err := svc.ImportPoster(context.Background(), "media-alpha", []byte("plaintext poster bytes")); err != nil {
		t.Fatalf("ImportPoster on encrypted-Library media returned error: %v", err)
	}
	if svc.HasPoster("media-alpha") {
		t.Fatal("Unsealed encrypted-Library media got a plaintext imported poster")
	}
	if _, err := os.Stat(svc.PosterPath("media-alpha")); !os.IsNotExist(err) {
		t.Fatalf("plaintext poster file exists for an Unsealed encrypted Library: %v", err)
	}
}

// TestImportCandidateArtworkBypassesSealedGate: a fetched artwork *candidate*
// preview caches under its synthetic candidate-cache ID even with a Sealed
// predicate wired (ENCPRIV-10b-LIVE regression). The synthetic ID resolves to
// no Media/Library, so the fail-closed isSealed verdict must NOT apply here —
// candidate previews are remote art, not encrypted-Media derivatives, and the
// owning Media's Sealed state is enforced at the mediahttp serving boundary.
func TestImportCandidateArtworkBypassesSealedGate(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	// Seal *everything*: even under a maximally paranoid predicate the synthetic
	// candidate cache must still import, since it is not a Media derivative.
	svc.SetSealed(func(store.LibraryID) bool { return true })

	cacheID := CandidateCacheID("cand-123")
	if err := svc.ImportCandidateArtwork(context.Background(), cacheID, []byte("remote-jpeg")); err != nil {
		t.Fatalf("ImportCandidateArtwork returned error: %v", err)
	}
	if !svc.HasPoster(cacheID) {
		t.Fatal("candidate preview did not cache under its synthetic ID")
	}
	// Regression proof: routing the same synthetic ID through the Media-gated
	// ImportArtwork *is* fail-closed (the bug's old code path), so the two paths
	// are genuinely different — only the candidate path is exempt.
	other := CandidateCacheID("cand-456")
	if err := svc.ImportArtwork(context.Background(), other, SlotPoster, []byte("remote-jpeg")); err != nil {
		t.Fatalf("ImportArtwork returned error: %v", err)
	}
	if svc.HasPoster(other) {
		t.Fatal("Media-gated ImportArtwork wrote a poster for an unresolvable synthetic ID; sealed gate not fail-closed")
	}
}

// TestImportArtworkClearsEmbeddedProvenance (CORE-2901): fetched artwork over
// an embedded cover is no longer marked embedded, so identify keeps it.
func TestImportArtworkClearsEmbeddedProvenance(t *testing.T) {
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-alpha")
	svc := NewService(st, &fakeGrabber{embedded: true}, t.TempDir())
	found, err := svc.ImportEmbeddedPoster(context.Background(), "media-alpha")
	if err != nil || !found {
		t.Fatalf("ImportEmbeddedPoster = %v, %v; want true, nil", found, err)
	}
	if !svc.IsEmbeddedArtwork("media-alpha", SlotPoster) {
		t.Fatal("embedded poster not marked embedded")
	}
	if err := svc.ImportArtwork(context.Background(), "media-alpha", SlotPoster, []byte("fetched poster")); err != nil {
		t.Fatal(err)
	}
	if svc.IsEmbeddedArtwork("media-alpha", SlotPoster) {
		t.Fatal("fetched poster still marked embedded")
	}
	if !svc.IsImportedArtwork("media-alpha", SlotPoster) {
		t.Fatal("fetched poster not marked imported")
	}
	// The embedded cover fills only a missing or generated poster.
	if found, err := svc.ImportEmbeddedPoster(context.Background(), "media-alpha"); err != nil || found {
		t.Fatalf("ImportEmbeddedPoster over a fetched poster = %v, %v; want false, nil", found, err)
	}
	// A conditional import that no longer wants the slot leaves it alone.
	stored, err := svc.ImportArtworkIf(context.Background(), "media-alpha", SlotPoster, []byte("late poster"), func() bool { return false })
	if err != nil || stored {
		t.Fatalf("ImportArtworkIf with a false condition = %v, %v; want false, nil", stored, err)
	}
	poster, err := os.ReadFile(svc.PosterPath("media-alpha"))
	if err != nil || string(poster) != "fetched poster" {
		t.Fatalf("poster = %q, %v; want the fetched poster", poster, err)
	}
	if !svc.IsImportedArtwork("media-alpha", SlotPoster) || svc.IsEmbeddedArtwork("media-alpha", SlotPoster) {
		t.Fatal("provenance changed by refused imports")
	}
}

// TestKeepUploadedCandidateRoundTripsAndVerifies: an uploaded candidate's bytes
// come back only under the SourceURL they hash to (CORE-2937), live under the
// owning Media, and go with that Media's artwork when it is purged.
func TestKeepUploadedCandidateRoundTripsAndVerifies(t *testing.T) {
	svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
	ctx := context.Background()
	data := []byte("uploaded poster bytes")
	sourceURL := UploadedSourceURL(data)
	if !IsUploadedSource(sourceURL) || IsUploadedSource("https://img.example.test/a.jpg") {
		t.Fatalf("IsUploadedSource misclassifies %q", sourceURL)
	}
	if _, err := svc.UploadedCandidate("media-alpha", sourceURL); !errors.Is(err, ErrUploadedCandidateMissing) {
		t.Fatalf("UploadedCandidate before keep = %v, want ErrUploadedCandidateMissing", err)
	}
	if err := svc.KeepUploadedCandidate(ctx, "media-alpha", data); err != nil {
		t.Fatalf("KeepUploadedCandidate: %v", err)
	}
	if err := svc.KeepUploadedCandidate(ctx, "media-alpha", data); err != nil {
		t.Fatalf("repeat KeepUploadedCandidate: %v", err)
	}
	got, err := svc.UploadedCandidate("media-alpha", sourceURL)
	if err != nil || string(got) != string(data) {
		t.Fatalf("UploadedCandidate = %q, %v; want the kept bytes", got, err)
	}
	path := svc.uploadedCandidatePath("media-alpha", sourceURL)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("kept upload mode = %v, %v; want 0600", info, err)
	}
	if _, err := svc.UploadedCandidate("media-alpha", UploadedSourceURL([]byte("other"))); !errors.Is(err, ErrUploadedCandidateMissing) {
		t.Fatalf("UploadedCandidate under another digest = %v, want ErrUploadedCandidateMissing", err)
	}
	for _, bad := range []string{"https://img.example.test/a.jpg", UploadedSourcePrefix + "../../x", UploadedSourcePrefix + strings.ToUpper(sourceURL[len(UploadedSourcePrefix):])} {
		if _, err := svc.UploadedCandidate("media-alpha", bad); !errors.Is(err, ErrUploadedCandidateMissing) {
			t.Fatalf("UploadedCandidate(%q) = %v, want ErrUploadedCandidateMissing", bad, err)
		}
	}
	if _, err := svc.SweepCandidatePreviewCache(nil); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := svc.UploadedCandidate("media-alpha", sourceURL); err != nil {
		t.Fatalf("candidate-preview sweep removed a kept upload: %v", err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.KeepUploadedCandidate(ctx, "media-alpha", data); err != nil {
		t.Fatalf("KeepUploadedCandidate over a corrupt original: %v", err)
	}
	if got, err := svc.UploadedCandidate("media-alpha", sourceURL); err != nil || string(got) != string(data) {
		t.Fatalf("UploadedCandidate after repair = %q, %v", got, err)
	}
	if err := svc.PurgeMediaArtwork("media-alpha"); err != nil {
		t.Fatalf("PurgeMediaArtwork: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("purged Media kept its uploaded original: %v", err)
	}
}

// TestKeepUploadedCandidateSkipsSealedAndEncryptedMedia: no plaintext upload
// copy lands on disk for Media in a Sealed or encrypted Library.
func TestKeepUploadedCandidateSkipsSealedAndEncryptedMedia(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sealed    bool
		encrypted bool
	}{{name: "sealed", sealed: true}, {name: "encrypted", encrypted: true}} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(storetest.Open(t), &fakeGrabber{}, t.TempDir())
			svc.SetSealed(func(store.LibraryID) bool { return tc.sealed })
			svc.SetEncrypted(func(store.LibraryID) bool { return tc.encrypted })
			if err := svc.KeepUploadedCandidate(context.Background(), "media-alpha", []byte("plaintext")); err != nil {
				t.Fatalf("KeepUploadedCandidate: %v", err)
			}
			if _, err := os.Stat(svc.uploadedCandidatePath("media-alpha", UploadedSourceURL([]byte("plaintext")))); !os.IsNotExist(err) {
				t.Fatalf("plaintext upload kept for %s media: %v", tc.name, err)
			}
		})
	}
}

func writeJPEG(t *testing.T, path string, w int) {
	t.Helper()
	f, err := os.Create(path) // #nosec G304 -- test-owned path.
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, w, w)), nil); err != nil {
		t.Fatal(err)
	}
}

func TestDerivativeSkipsBucketsAtOrAboveSourceWidth(t *testing.T) {
	scaler := &fakeScaler{}
	svc := NewService(storetest.Open(t), scaler, t.TempDir())
	writePoster(t, svc, "media-narrow")
	orig := svc.PosterPath("media-narrow")
	writeJPEG(t, orig, 500)
	stale := filepath.Join(filepath.Dir(orig), "poster.w1280.jpg")
	if err := os.WriteFile(stale, []byte("old copy"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := svc.PosterDerivative(context.Background(), "media-narrow", "w1280")
	if err != nil || got != orig {
		t.Fatalf("w1280 = %q, %v; want original %q", got, err, orig)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("redundant copy still present: %v", err)
	}
	if len(scaler.scaleCalls) != 0 {
		t.Fatalf("scaleCalls = %v, want none", scaler.scaleCalls)
	}
	if got, err = svc.PosterDerivative(context.Background(), "media-narrow", "w320"); err != nil || got == orig {
		t.Fatalf("w320 = %q, %v; want a generated derivative", got, err)
	}
}
