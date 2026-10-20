package decodeverify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeVerifier satisfies the Verifier seam without FFmpeg. broken maps a source
// path to the decode verdict it returns; runErr (when set for a path) simulates
// an inability to RUN ffmpeg (never a corrupt-file verdict).
type fakeVerifier struct {
	broken map[string]bool
	runErr map[string]error
	calls  []string
}

type noopEncryptedLibraryDBCipher struct{}

func (noopEncryptedLibraryDBCipher) UnsealDBFile(context.Context, store.LibraryID, string, string) error {
	return nil
}

func (noopEncryptedLibraryDBCipher) SealDBFile(context.Context, store.LibraryID, string, string) error {
	return nil
}

func (f *fakeVerifier) DecodeVerify(_ context.Context, src string, _ int) (bool, error) {
	f.calls = append(f.calls, src)
	if f.runErr != nil {
		if err := f.runErr[src]; err != nil {
			return false, err
		}
	}
	return f.broken[src], nil
}

// seedVideo imports a video-class Media item backed by path into library-local
// (the storetest-seeded local Library), returning its ID.
func seedVideo(t *testing.T, st *store.Store, id, path string) store.MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               id,
		Container:           "mp4",
		Codec:               "h264",
		Class:               store.MediaClassVideo,
		ContentFingerprint:  "sha256:" + id,
		DurationSecondsHint: 100,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(%s): %v", id, err)
	}
	return media.ID
}

func brokenCount(t *testing.T, st *store.Store) int {
	t.Helper()
	counts, err := st.LibraryHealthCounts(context.Background(), store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("LibraryHealthCounts: %v", err)
	}
	total := 0
	for _, c := range counts {
		total += c.Counts[store.HealthBroken]
	}
	return total
}

// TestVerifyFlagsBrokenLeavesHealthy is acceptance (a)+(c): the job flags a
// corrupt/truncated file as broken and leaves a healthy file unflagged, and the
// broken signal surfaces in the Library Health query.
func TestVerifyFlagsBrokenLeavesHealthy(t *testing.T) {
	// feature:decode-verify feature:library-health
	st := storetest.Open(t)
	healthy := seedVideo(t, st, "healthy", "/m/healthy.mp4")
	broken := seedVideo(t, st, "broken", "/m/broken.mp4")

	// The development seed includes Media with no available file (already broken
	// on the "bytes are gone" branch); capture that baseline and assert the
	// decode-verify delta against it. Both freshly-seeded items start sound
	// (file present, decode_broken default 0), so neither is in the baseline.
	baseline := brokenCount(t, st)

	verifier := &fakeVerifier{broken: map[string]bool{"/m/broken.mp4": true}}
	svc := NewService(st, verifier)
	now := func() time.Time { return time.Unix(1000, 0).UTC() }

	for _, id := range []store.MediaID{healthy, broken} {
		gotBroken, ok, err := svc.VerifyMedia(context.Background(), id, now)
		if err != nil {
			t.Fatalf("VerifyMedia(%s): %v", id, err)
		}
		if !ok {
			t.Fatalf("VerifyMedia(%s) ok = false, want true (video-class with a file)", id)
		}
		want := id == broken
		if gotBroken != want {
			t.Fatalf("VerifyMedia(%s) broken = %v, want %v", id, gotBroken, want)
		}
	}

	// (c): exactly one more item (the decode-broken one) surfaces in the broken
	// dimension over the baseline, and the broken set contains it.
	if got := brokenCount(t, st) - baseline; got != 1 {
		t.Fatalf("decode-broken delta after verify = %d, want 1", got)
	}
	items, err := st.LibraryHealthItems(context.Background(), "library-local", store.HealthBroken, store.LibraryFilter{Revealed: true, AllLibraries: true}, 0)
	if err != nil {
		t.Fatalf("LibraryHealthItems: %v", err)
	}
	found := false
	for _, it := range items {
		if it.MediaID == broken {
			found = true
		}
		if it.MediaID == healthy {
			t.Fatalf("healthy item %s surfaced as broken", healthy)
		}
	}
	if !found {
		t.Fatalf("decode-broken item %s not in broken dimension: %#v", broken, items)
	}
}

// TestPreviouslyBrokenClears is acceptance (b): a previously-broken file clears
// when it later decodes cleanly (self-healing).
func TestPreviouslyBrokenClears(t *testing.T) {
	// feature:decode-verify feature:library-health
	st := storetest.Open(t)
	id := seedVideo(t, st, "flaky", "/m/flaky.mp4")
	baseline := brokenCount(t, st)
	now := func() time.Time { return time.Unix(2000, 0).UTC() }

	// First pass: broken.
	brokenVerifier := &fakeVerifier{broken: map[string]bool{"/m/flaky.mp4": true}}
	if _, _, err := NewService(st, brokenVerifier).VerifyMedia(context.Background(), id, now); err != nil {
		t.Fatalf("first VerifyMedia: %v", err)
	}
	if got := brokenCount(t, st) - baseline; got != 1 {
		t.Fatalf("after broken pass, delta = %d, want 1", got)
	}

	// Second pass: now decodes cleanly — the flag clears.
	soundVerifier := &fakeVerifier{broken: map[string]bool{}}
	gotBroken, _, err := NewService(st, soundVerifier).VerifyMedia(context.Background(), id, now)
	if err != nil {
		t.Fatalf("second VerifyMedia: %v", err)
	}
	if gotBroken {
		t.Fatal("second pass reported broken, want sound")
	}
	if got := brokenCount(t, st) - baseline; got != 0 {
		t.Fatalf("after sound pass, delta = %d, want 0 (flag cleared)", got)
	}
}

// TestRunErrorNeverFlags guards the safety property: an inability to RUN ffmpeg
// is an error, never a broken verdict — a misconfigured host cannot mass-flag a
// healthy library.
func TestRunErrorNeverFlags(t *testing.T) {
	// feature:decode-verify
	st := storetest.Open(t)
	id := seedVideo(t, st, "unreadable", "/m/unreadable.mp4")
	baseline := brokenCount(t, st)
	verifier := &fakeVerifier{runErr: map[string]error{"/m/unreadable.mp4": errors.New("ffmpeg not available")}}

	_, ok, err := NewService(st, verifier).VerifyMedia(context.Background(), id, time.Now)
	if err == nil {
		t.Fatal("VerifyMedia returned nil error on ffmpeg run failure, want error")
	}
	if ok {
		t.Fatal("VerifyMedia ok = true on run failure, want false")
	}
	if got := brokenCount(t, st) - baseline; got != 0 {
		t.Fatalf("run failure flagged the file broken: delta = %d, want 0", got)
	}
}

func TestRunErrorReportsOpaqueMediaIDOnly(t *testing.T) {
	// feature:decode-verify feature:encrypted-library
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.SetLibraryBacking(ctx, "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, "library-local", noopEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	path := "/encrypted/root/private-title.mp4"
	id := seedVideo(t, st, "private-title", path)
	sourceErr := errors.New(`ffmpeg: decode failed for "Private Title" at /encrypted/root/private-title.mp4; EXIF Make=SecretCam GPSLatitude=43.6532`)
	verifier := &fakeVerifier{runErr: map[string]error{path: sourceErr}}

	_, _, err = NewService(st, verifier).VerifyMedia(ctx, id, time.Now)
	if err == nil {
		t.Fatal("VerifyMedia returned nil error on run failure, want opaque error")
	}
	if !errors.Is(err, sourceErr) {
		t.Fatalf("VerifyMedia error does not unwrap source error: %v", err)
	}
	text := err.Error()
	if !strings.Contains(text, string(id)) {
		t.Fatalf("opaque error = %q, want media ID %q", text, id)
	}
	for _, leak := range []string{"/encrypted/root", "private-title.mp4", "Private Title", "SecretCam", "GPSLatitude", "43.6532"} {
		if strings.Contains(text, leak) {
			t.Fatalf("opaque error leaked %q: %q", leak, text)
		}
	}
}

// TestSweepHandlerCheckpointsAndCounts exercises the durable sweep: it iterates
// every Media, checkpoints by ID across yields, and tallies broken vs sound.
func TestSweepHandlerCheckpointsAndCounts(t *testing.T) {
	// feature:decode-verify
	st := storetest.Open(t)
	// Three video items: one broken, two sound. (storetest seeds its own Media
	// too; those are video-class with no real file path, so DecodeVerify is asked
	// about whatever path they carry — the fake returns sound for unknown paths.)
	seedVideo(t, st, "v-broken", "/m/v-broken.mp4")
	seedVideo(t, st, "v-ok-1", "/m/v-ok-1.mp4")
	seedVideo(t, st, "v-ok-2", "/m/v-ok-2.mp4")

	// storetest may seed its own Media (some with no backing file) that already
	// count as broken on the "bytes are gone" branch — diff against the captured
	// baseline rather than an absolute count so the test isolates the decode
	// signal the sweep adds.
	baseline := brokenCount(t, st)

	verifier := &fakeVerifier{broken: map[string]bool{"/m/v-broken.mp4": true}}
	svc := NewService(st, verifier)
	handler := SweepHandler(svc)

	// Drive the sweep to completion across yields (batch size is small). The job
	// must be persisted so UpdateJobCheckpoint inside the handler succeeds.
	if _, err := st.EnqueueJob(context.Background(), store.Job{
		ID: "job-dv", Kind: SweepJobKind, Class: store.JobClassBackground,
		DedupeKey: string(SweepJobKind), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	job, err := st.ClaimNextJob(context.Background(), "w", []store.JobClass{store.JobClassBackground}, time.Now().UTC())
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	for i := 0; i < 100; i++ {
		res, err := handler(context.Background(), job)
		if err == nil {
			break // sweep finished (no yield)
		}
		if !errors.Is(err, jobs.ErrYielded) {
			t.Fatalf("sweep handler error: %v", err)
		}
		job.Checkpoint = res.Checkpoint
	}

	if got := brokenCount(t, st) - baseline; got != 1 {
		t.Fatalf("decode-broken delta after sweep = %d, want 1", got)
	}
}

func TestSweepHandlerCompletesExactFinalBatchWithoutExtraInvocation(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	ids, err := st.MediaIDsAfter(ctx, "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	fillNames := []string{"batch-fill-a", "batch-fill-b"}
	for i := 0; i < (sweepJobBatchSize-len(ids)%sweepJobBatchSize)%sweepJobBatchSize; i++ {
		seedVideo(t, st, fillNames[i], "/m/"+fillNames[i]+".mp4")
	}
	ids, err = st.MediaIDsAfter(ctx, "", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 || len(ids)%sweepJobBatchSize != 0 {
		t.Fatalf("fixture media count = %d, want a positive multiple of %d", len(ids), sweepJobBatchSize)
	}

	if _, err := st.EnqueueJob(ctx, store.Job{
		ID: "job-dv-exact", Kind: SweepJobKind, Class: store.JobClassBackground,
		DedupeKey: "decode-verify-exact", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimNextJob(ctx, "w", []store.JobClass{store.JobClassBackground}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := SweepHandler(NewService(st, &fakeVerifier{}))
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
	want := len(ids) / sweepJobBatchSize
	if invocations != want {
		t.Fatalf("handler invocations = %d, want %d", invocations, want)
	}
}
