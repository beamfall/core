package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/serverrole"
	"github.com/beamfall/core/internal/store"
)

type fakeReclassStore struct {
	libraries    []store.Library
	states       map[store.LibraryID]string
	candidates   map[store.LibraryID][]store.ReclassifyCandidate
	listErr      map[store.LibraryID]error
	classes      map[store.MediaID]store.MediaClass
	codecs       map[store.MediaID]string
	reclassErr   error
	stateErr     error
	roots        map[store.LibraryID][]string
	jobs         []store.Job
	stateHistory []string
}

func (f *fakeReclassStore) Libraries(context.Context, store.LibraryFilter) ([]store.Library, error) {
	return f.libraries, nil
}

func (f *fakeReclassStore) AttachedPictureReclassState(_ context.Context, id store.LibraryID) (string, error) {
	if f.stateErr != nil {
		return "", f.stateErr
	}
	return f.states[id], nil
}

func (f *fakeReclassStore) SetAttachedPictureReclassState(_ context.Context, id store.LibraryID, state string, _ time.Time) error {
	if f.states[id] != store.AttachedPictureReclassDone {
		f.states[id] = state
	}
	f.stateHistory = append(f.stateHistory, string(id)+"="+state)
	return nil
}

func (f *fakeReclassStore) AttachedPictureCandidatesInLibrary(_ context.Context, id store.LibraryID) ([]store.ReclassifyCandidate, error) {
	if err := f.listErr[id]; err != nil {
		return nil, err
	}
	var out []store.ReclassifyCandidate
	for _, c := range f.candidates[id] {
		if f.classes[c.MediaID] == store.MediaClassVideo {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeReclassStore) ReclassifyVideoMediaAsAudio(_ context.Context, id store.MediaID, codec string) (bool, error) {
	if f.reclassErr != nil {
		return false, f.reclassErr
	}
	if f.classes[id] != store.MediaClassVideo {
		return false, nil
	}
	f.classes[id] = store.MediaClassAudio
	f.codecs[id] = codec
	return true, nil
}

func (f *fakeReclassStore) LibraryRoots(_ context.Context, id store.LibraryID) ([]string, error) {
	return f.roots[id], nil
}

func (f *fakeReclassStore) EnqueueJob(_ context.Context, job store.Job) (store.Job, error) {
	f.jobs = append(f.jobs, job)
	return job, nil
}

type fakeReclassProber struct {
	probes map[string]ffmpeg.Probe
	errs   map[string]error
	calls  []string
}

func (p *fakeReclassProber) Probe(_ context.Context, path string) (ffmpeg.Probe, error) {
	p.calls = append(p.calls, path)
	if err := p.errs[path]; err != nil {
		return ffmpeg.Probe{}, err
	}
	return p.probes[path], nil
}

func coverArtAudio(codec string) ffmpeg.Probe {
	return ffmpeg.Probe{AudioCodec: codec, Streams: []ffmpeg.ProbeStream{{Type: "audio", Codec: codec}, {Type: "video", Codec: "mjpeg", AttachedPicture: true}}}
}

func newFakeReclassStore() *fakeReclassStore {
	return &fakeReclassStore{
		libraries: []store.Library{{ID: "lib-music"}},
		states:    map[store.LibraryID]string{},
		candidates: map[store.LibraryID][]store.ReclassifyCandidate{"lib-music": {
			{MediaID: "m-cover", Path: "/music/cover.mp3"},
			{MediaID: "m-camera", Path: "/music/camera.avi"},
			{MediaID: "m-gone", Path: "/music/unreadable.mp3"},
		}},
		listErr: map[store.LibraryID]error{},
		classes: map[store.MediaID]store.MediaClass{"m-cover": store.MediaClassVideo, "m-camera": store.MediaClassVideo, "m-gone": store.MediaClassVideo},
		codecs:  map[store.MediaID]string{},
		roots:   map[store.LibraryID][]string{"lib-music": {"/music"}},
	}
}

func newFakeReclassProber() *fakeReclassProber {
	return &fakeReclassProber{
		probes: map[string]ffmpeg.Probe{
			"/music/cover.mp3":  coverArtAudio("mp3"),
			"/music/camera.avi": {HasVideo: true, VideoCodec: "mjpeg", AudioCodec: "pcm_s16le"},
		},
		errs: map[string]error{"/music/unreadable.mp3": errors.New("ffprobe: no such file")},
	}
}

func assertOneBackfillScan(t *testing.T, jobs []store.Job, libraryID store.LibraryID) {
	t.Helper()
	if len(jobs) != 1 || jobs[0].Kind != scanpkg.JobKind ||
		!strings.HasSuffix(jobs[0].DedupeKey, ":backfill") || !strings.Contains(jobs[0].DedupeKey, string(libraryID)) {
		t.Fatalf("queued jobs = %+v, want one backfill scan of %s", jobs, libraryID)
	}
}

// TestReclassifyAttachedPictureAudio (CORE-1262): only the cover-art audio
// moves to audio with its audio codec, a real MJPEG video and an unreadable
// file stay as they are, a backfill scan is queued so the moved track
// hydrates, and the Library is done: a second start probes nothing.
func TestReclassifyAttachedPictureAudio(t *testing.T) {
	st, prober := newFakeReclassStore(), newFakeReclassProber()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	reclassifyAttachedPictureAudio(context.Background(), st, prober, logger)

	if st.classes["m-cover"] != store.MediaClassAudio || st.codecs["m-cover"] != "mp3" {
		t.Fatalf("cover-art track = %q/%q, want audio/mp3", st.classes["m-cover"], st.codecs["m-cover"])
	}
	if st.classes["m-camera"] != store.MediaClassVideo || st.classes["m-gone"] != store.MediaClassVideo {
		t.Fatalf("non-audio rows changed: %v", st.classes)
	}
	assertOneBackfillScan(t, st.jobs, "lib-music")
	if st.states["lib-music"] != store.AttachedPictureReclassDone {
		t.Fatalf("state = %q, want done (history %v)", st.states["lib-music"], st.stateHistory)
	}
	if want := []string{"lib-music=pending", "lib-music=done"}; len(st.stateHistory) != 2 || st.stateHistory[0] != want[0] || st.stateHistory[1] != want[1] {
		t.Fatalf("state history = %v, want %v", st.stateHistory, want)
	}

	prober.calls = nil
	reclassifyAttachedPictureAudio(context.Background(), st, prober, logger)
	if len(prober.calls) != 0 || len(st.jobs) != 1 {
		t.Fatalf("second start probed %v and queued %d jobs, want nothing", prober.calls, len(st.jobs))
	}
}

// TestReclassifyAttachedPictureAudioResumesAfterInterruption: a run that moved
// a row and then failed stays pending, and the next start, which finds no
// candidate left, still queues the hydration scan before recording done.
func TestReclassifyAttachedPictureAudioResumesAfterInterruption(t *testing.T) {
	st, prober := newFakeReclassStore(), newFakeReclassProber()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st.candidates["lib-music"] = []store.ReclassifyCandidate{{MediaID: "m-cover", Path: "/music/cover.mp3"}, {MediaID: "m-cover-2", Path: "/music/cover2.mp3"}}
	st.classes["m-cover-2"] = store.MediaClassVideo
	prober.probes["/music/cover2.mp3"] = coverArtAudio("flac")

	// The first row moves, then the store fails before the second.
	calls := 0
	failing := &failAfterReclassStore{fakeReclassStore: st, allowed: 1, calls: &calls}
	reclassifyAttachedPictureAudio(context.Background(), failing, prober, logger)
	if st.states["lib-music"] != store.AttachedPictureReclassPending || len(st.jobs) != 0 {
		t.Fatalf("after interruption state=%q jobs=%d, want pending and no scan", st.states["lib-music"], len(st.jobs))
	}

	// Simulate the restart having nothing left to move for the first row.
	reclassifyAttachedPictureAudio(context.Background(), st, prober, logger)
	if st.classes["m-cover"] != store.MediaClassAudio || st.classes["m-cover-2"] != store.MediaClassAudio {
		t.Fatalf("classes after resume = %v, want both audio", st.classes)
	}
	assertOneBackfillScan(t, st.jobs, "lib-music")
	if st.states["lib-music"] != store.AttachedPictureReclassDone {
		t.Fatalf("state after resume = %q, want done", st.states["lib-music"])
	}

	// A pending Library with no candidate left at all still gets its scan.
	st2, prober2 := newFakeReclassStore(), newFakeReclassProber()
	st2.states["lib-music"] = store.AttachedPictureReclassPending
	st2.candidates["lib-music"] = nil
	reclassifyAttachedPictureAudio(context.Background(), st2, prober2, logger)
	assertOneBackfillScan(t, st2.jobs, "lib-music")
	if st2.states["lib-music"] != store.AttachedPictureReclassDone {
		t.Fatalf("pending-only state = %q, want done", st2.states["lib-music"])
	}
}

type failAfterReclassStore struct {
	*fakeReclassStore
	allowed int
	calls   *int
}

func (f *failAfterReclassStore) ReclassifyVideoMediaAsAudio(ctx context.Context, id store.MediaID, codec string) (bool, error) {
	*f.calls++
	if *f.calls > f.allowed {
		return false, errors.New("database is locked")
	}
	return f.fakeReclassStore.ReclassifyVideoMediaAsAudio(ctx, id, codec)
}

// TestReclassifyAttachedPictureAudioLeavesLibrariesUnmarked: a sealed encrypted
// Library, a Library with no root to scan, a cancelled context and an Attached
// node all leave the repair to a later start.
func TestReclassifyAttachedPictureAudioLeavesLibrariesUnmarked(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	sealed, prober := newFakeReclassStore(), newFakeReclassProber()
	sealed.listErr["lib-music"] = store.ErrEncryptedLibraryDBDisabled
	reclassifyAttachedPictureAudio(context.Background(), sealed, prober, logger)
	if sealed.states["lib-music"] != "" || len(prober.calls) != 0 {
		t.Fatalf("sealed Library state=%q probes=%v, want untouched", sealed.states["lib-music"], prober.calls)
	}

	rootless := newFakeReclassStore()
	rootless.roots = map[store.LibraryID][]string{}
	reclassifyAttachedPictureAudio(context.Background(), rootless, newFakeReclassProber(), logger)
	if rootless.states["lib-music"] != store.AttachedPictureReclassPending || len(rootless.jobs) != 0 {
		t.Fatalf("rootless Library state=%q jobs=%d, want pending with no scan", rootless.states["lib-music"], len(rootless.jobs))
	}

	cancelled := newFakeReclassStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reclassifyAttachedPictureAudio(ctx, cancelled, newFakeReclassProber(), logger)
	if cancelled.states["lib-music"] != "" || cancelled.classes["m-cover"] != store.MediaClassVideo {
		t.Fatalf("cancelled run state=%q class=%q, want untouched", cancelled.states["lib-music"], cancelled.classes["m-cover"])
	}

	attached := newFakeReclassStore()
	attached.stateErr = serverrole.ErrAttachedAuthorityRefused
	attachedProber := newFakeReclassProber()
	reclassifyAttachedPictureAudio(context.Background(), attached, attachedProber, logger)
	if len(attachedProber.calls) != 0 {
		t.Fatalf("Attached node probed %v, want nothing", attachedProber.calls)
	}
}
