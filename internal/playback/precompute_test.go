package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestRequestPrecomputeRenditionEnqueuesWithoutStartingGeneration(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-request-precompute", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	now := time.Unix(200, 0).UTC()

	job, err := RequestPrecomputeRendition(ctx, st, PrecomputeRequest{
		MediaID:     mediaID,
		RenditionID: "precompute-request-720p",
		RungID:      "720p",
		Codec:       "h264",
		Height:      720,
		Bandwidth:   4_256_000,
		Path:        filepath.Join(t.TempDir(), "precomputed", "precompute-request-720p"),
	}, now)
	if err != nil {
		t.Fatalf("RequestPrecomputeRendition: %v", err)
	}
	if job.Kind != PrecomputeJobKind || job.Class != store.JobClassBackground {
		t.Fatalf("job kind/class = %q/%q, want %q/background", job.Kind, job.Class, PrecomputeJobKind)
	}
	if job.DedupeKey != "playback-precompute:precompute-request-720p" {
		t.Fatalf("DedupeKey = %q", job.DedupeKey)
	}

	var payload PrecomputePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload.MediaID != mediaID || payload.RenditionID != "precompute-request-720p" || payload.SourcePath == "" || payload.ContentFingerprint != "fp-request-precompute" {
		t.Fatalf("payload = %+v", payload)
	}

	if _, err := st.PlaybackRendition(ctx, "precompute-request-720p", store.LibraryFilter{AllLibraries: true, Revealed: true}); err == nil {
		t.Fatal("request published rendition state before worker admission")
	}
	ready, err := st.ReadyPlaybackRenditions(ctx, mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("ReadyPlaybackRenditions: %v", err)
	}
	if len(ready) != 0 {
		t.Fatalf("ready renditions = %v, want none while generating", ready)
	}
}

func TestRequestPrecomputeRenditionRejectsUnsafeRungBeforeEnqueue(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-request-unsafe-rung", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	base := PrecomputeRequest{
		MediaID:     mediaID,
		RenditionID: "precompute-unsafe-rung",
		Codec:       "h264",
		Height:      720,
		Bandwidth:   4_256_000,
		Path:        filepath.Join(t.TempDir(), "precomputed", "precompute-unsafe-rung"),
	}
	for _, rungID := range []string{"../720p", "/tmp/720p", "720p/index.m3u8", "bogus"} {
		req := base
		req.RungID = rungID
		if _, err := RequestPrecomputeRendition(ctx, st, req, time.Unix(200, 0).UTC()); err == nil {
			t.Fatalf("RequestPrecomputeRendition accepted rung %q", rungID)
		}
	}
}

func TestPrecomputeScheduleJobsBuildsExplicitBackgroundJobs(t *testing.T) {
	now := time.Unix(300, 0).UTC()

	rule := PrecomputeSourceRule{
		LibraryID: "library-local",
		Requests: []PrecomputeRequest{{
			MediaID:            "media-alpha",
			ContentFingerprint: "sha256:alpha",
			RenditionID:        "schedule-alpha-720p",
			RungID:             "720p",
			Codec:              "h264",
			Height:             720,
			Bandwidth:          4_256_000,
			SourcePath:         filepath.Join(t.TempDir(), "alpha.mkv"),
			Path:               filepath.Join(t.TempDir(), "schedule-alpha-720p"),
		}},
	}
	jobs, err := rule.BuildJobs(now)
	if err != nil {
		t.Fatalf("BuildJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Kind != PrecomputeJobKind || jobs[0].Class != store.JobClassBackground {
		t.Fatalf("jobs = %#v", jobs)
	}
	if got, want := rule.ScheduleID(), store.ScheduleID("playback-precompute:library-local"); got != want {
		t.Fatalf("ScheduleID = %q, want %q", got, want)
	}
}

func TestRequestPrecomputeEvictionEnqueuesBudgetJob(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(350, 0).UTC()
	cacheRoot := t.TempDir()

	job, err := RequestPrecomputeEviction(ctx, st, cacheRoot, 1024, now)
	if err != nil {
		t.Fatalf("RequestPrecomputeEviction: %v", err)
	}
	if job.Kind != PrecomputeEvictJobKind || job.Class != store.JobClassBackground {
		t.Fatalf("job kind/class = %q/%q, want %q/background", job.Kind, job.Class, PrecomputeEvictJobKind)
	}
	if job.DedupeKey != precomputeEvictDedupeKey {
		t.Fatalf("DedupeKey = %q, want %q", job.DedupeKey, precomputeEvictDedupeKey)
	}
	var payload PrecomputeEvictPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload.CacheRoot != cacheRoot || payload.MaxBytes != 1024 {
		t.Fatalf("payload = %+v", payload)
	}

	scheduled, err := PrecomputeEvictScheduleJob(cacheRoot, 1024)(now.Add(time.Hour))
	if err != nil {
		t.Fatalf("PrecomputeEvictScheduleJob: %v", err)
	}
	if len(scheduled) != 1 || scheduled[0].Kind != PrecomputeEvictJobKind || scheduled[0].DedupeKey != precomputeEvictDedupeKey {
		t.Fatalf("scheduled jobs = %#v", scheduled)
	}
}

func TestPrecomputeMediaRenditionsEnqueuesExplicitLadderJobs(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-trigger", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := t.TempDir()
	now := time.Unix(700, 0).UTC()

	jobs, err := PrecomputeMediaRenditions(ctx, st, mediaID, "h264", cacheRoot, now)
	if err != nil {
		t.Fatalf("PrecomputeMediaRenditions: %v", err)
	}
	// A 1920x1080 source backs the full 1080p/720p/480p ladder.
	if len(jobs) != 3 {
		t.Fatalf("enqueued %d jobs, want 3 ladder rungs", len(jobs))
	}
	for _, job := range jobs {
		if job.Kind != PrecomputeJobKind || job.Class != store.JobClassBackground {
			t.Fatalf("job kind/class = %q/%q, want precompute/background", job.Kind, job.Class)
		}
	}
	// Each rung carries a deterministic cache identity (regenerate-not-duplicate)
	// and path; the worker publishes Generating only after resource admission.
	wantID := PrecomputeRenditionID(mediaID, "fp-precompute-trigger", "480p")
	var rungPayload PrecomputePayload
	for _, job := range jobs {
		var payload PrecomputePayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			t.Fatalf("payload JSON: %v", err)
		}
		if payload.RenditionID == wantID {
			rungPayload = payload
		}
	}
	if rungPayload.RenditionID != wantID || !strings.HasPrefix(rungPayload.Path, cacheRoot) {
		t.Fatalf("480p payload = %+v, want deterministic ID/path under cache root", rungPayload)
	}
	if _, err := st.PlaybackRendition(ctx, wantID, store.LibraryFilter{AllLibraries: true, Revealed: true}); err == nil {
		t.Fatal("enqueue published rendition state before worker admission")
	}

	// Re-triggering reuses the same cache identity rather than duplicating: the
	// DedupeKey (per rendition ID) collapses the second enqueue.
	again, err := PrecomputeMediaRenditions(ctx, st, mediaID, "h264", cacheRoot, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("PrecomputeMediaRenditions (re-trigger): %v", err)
	}
	for i := range again {
		if again[i].ID != jobs[i].ID {
			t.Fatalf("re-trigger job %d ID = %q, want deduped to %q", i, again[i].ID, jobs[i].ID)
		}
	}
}

func TestPrecomputeMediaRenditionsRefusesEncryptedLibrary(t *testing.T) {
	// ENCPRIV-10b: a precomputed HLS rendition is a plaintext derivative written
	// to the shared cache root. For an Encrypted Library (no encrypted-plane write
	// path yet) precompute fails closed — no jobs, no rendition rows — whether the
	// Library is Sealed or Unsealed. Marking encrypted without provisioning models
	// the Unsealed-plaintext-column case (sealed predicate would report false).
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-encrypted", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	if err := st.SetLibraryBacking(ctx, "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking encrypted: %v", err)
	}
	cacheRoot := t.TempDir()

	jobs, err := PrecomputeMediaRenditions(ctx, st, mediaID, "h264", cacheRoot, time.Unix(700, 0).UTC())
	if err != nil {
		t.Fatalf("PrecomputeMediaRenditions on an encrypted Library returned error: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("enqueued %d precompute jobs for an encrypted Library; want 0", len(jobs))
	}
}

func TestPrecomputeMediaRenditionsRequiresConfiguredCacheRoot(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-noroot", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	if _, err := PrecomputeMediaRenditions(ctx, st, mediaID, "h264", "  ", time.Unix(700, 0).UTC()); err == nil {
		t.Fatal("PrecomputeMediaRenditions accepted an empty cache root")
	}
}

func TestPrecomputeJobHandlerRegeneratesEvictedRendition(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-regenerate-precompute", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	now := time.Unix(400, 0).UTC()
	cacheDir := filepath.Join(t.TempDir(), "precomputed", "regenerate-720p")
	if err := st.UpsertPlaybackRendition(ctx, store.PlaybackRendition{
		ID:                 "regenerate-720p",
		MediaID:            mediaID,
		ContentFingerprint: "fp-regenerate-precompute",
		Kind:               store.PlaybackRenditionKindHLS,
		Status:             store.PlaybackRenditionEvicted,
		RungID:             "720p",
		Codec:              "h264",
		Height:             720,
		Bandwidth:          4_256_000,
		Path:               cacheDir,
		SizeBytes:          4096,
		CreatedAt:          now.Add(-time.Hour),
		LastUsedAt:         now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("UpsertPlaybackRendition: %v", err)
	}
	job, err := RequestPrecomputeRendition(ctx, st, PrecomputeRequest{
		MediaID:     mediaID,
		RenditionID: "regenerate-720p",
		RungID:      "720p",
		Codec:       "h264",
		Height:      720,
		Bandwidth:   4_256_000,
		Path:        cacheDir,
	}, now)
	if err != nil {
		t.Fatalf("RequestPrecomputeRendition: %v", err)
	}

	generator := &fakePrecomputeGenerator{}
	handler := PrecomputeJobHandler{Store: st, Generator: generator, Admission: NewPrecomputeResourceGovernor(1 << 30), Now: func() time.Time { return now.Add(time.Minute) }}
	registry := jobs.NewRegistry(st, "playback-precompute-test")
	registry.Register(PrecomputeJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		return jobs.Result{}, handler.Handle(ctx, job)
	})

	claimed, err := registry.RunOne(ctx, []store.JobClass{store.JobClassBackground}, now)
	if err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job = %q, want %q", claimed.ID, job.ID)
	}
	if len(generator.calls) != 1 {
		t.Fatalf("generator calls = %d, want 1", len(generator.calls))
	}
	rendition, err := st.PlaybackRendition(ctx, "regenerate-720p", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatalf("PlaybackRendition: %v", err)
	}
	if rendition.Status != store.PlaybackRenditionReady {
		t.Fatalf("rendition status = %q, want ready", rendition.Status)
	}
	if rendition.SizeBytes == 0 {
		t.Fatal("rendition size stayed zero after regeneration")
	}
	completed, err := st.Job(ctx, job.ID)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	if completed.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed", completed.Status)
	}
}

func TestPrecomputeJobHandlerHydratesHDRSourceVideo(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-hdr", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 8, "smpte2084"),
	})
	now := time.Unix(410, 0).UTC()
	job, err := RequestPrecomputeRendition(ctx, st, PrecomputeRequest{
		MediaID:     mediaID,
		RenditionID: "precompute-hdr-720p",
		RungID:      "720p",
		Codec:       "h264",
		Height:      720,
		Bandwidth:   4_256_000,
		Path:        filepath.Join(t.TempDir(), "precompute-hdr-720p"),
	}, now)
	if err != nil {
		t.Fatalf("RequestPrecomputeRendition: %v", err)
	}

	generator := &fakePrecomputeGenerator{}
	handler := PrecomputeJobHandler{Store: st, Generator: generator, Admission: NewPrecomputeResourceGovernor(1 << 30), Now: func() time.Time { return now.Add(time.Minute) }}
	claimed, err := st.ClaimNextJob(ctx, "precompute-hdr-test", []store.JobClass{store.JobClassBackground}, now)
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job = %q, want %q", claimed.ID, job.ID)
	}
	if err := handler.Handle(ctx, claimed); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(generator.calls) != 1 {
		t.Fatalf("generator calls = %d, want 1", len(generator.calls))
	}
	got := generator.calls[0].SourceVideo
	if got.ColorTransfer != "smpte2084" || got.DoViProfile != 8 {
		t.Fatalf("SourceVideo = %+v, want HDR/DV metadata", got)
	}
}

func TestPrecomputeJobHandlerRejectsUnsafePayloadRungBeforeGeneration(t *testing.T) {
	st := storetest.Open(t)
	generator := &fakePrecomputeGenerator{}
	payload, err := json.Marshal(PrecomputePayload{
		MediaID:            "media-alpha",
		ContentFingerprint: "sha256:alpha",
		RenditionID:        "payload-unsafe-rung",
		RungID:             "../720p",
		Codec:              "h264",
		Height:             720,
		Bandwidth:          4_256_000,
		SourcePath:         filepath.Join(t.TempDir(), "alpha.mkv"),
		Path:               filepath.Join(t.TempDir(), "payload-unsafe-rung"),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := PrecomputeJobHandler{Store: st, Generator: generator}
	if err := handler.Handle(context.Background(), store.Job{Payload: payload}); err == nil {
		t.Fatal("Handle accepted unsafe payload rung")
	}
	if len(generator.calls) != 0 {
		t.Fatalf("generator calls = %d, want 0", len(generator.calls))
	}
}

func TestPrecomputeConcurrentAdmissionNeverExceedsBudget(t *testing.T) {
	governor := NewPrecomputeResourceGovernor(100)
	start := make(chan struct{})
	release := make(chan struct{})
	attempted := make(chan bool, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reservation, err := governor.Reserve(60, func() (int64, error) { return 0, nil })
			attempted <- err == nil
			if err != nil {
				return
			}
			<-release
			reservation.Release()
			reservation.Release()
		}()
	}
	close(start)
	admitted := 0
	for range 32 {
		if <-attempted {
			admitted++
		}
	}
	if admitted != 1 {
		t.Fatalf("concurrent admissions = %d, want exactly 1", admitted)
	}
	if got := governor.reservedBytes(); got != 60 {
		t.Fatalf("reserved bytes while admitted = %d, want 60", got)
	}
	close(release)
	wg.Wait()
	if got := governor.reservedBytes(); got != 0 {
		t.Fatalf("reserved bytes after idempotent release = %d, want 0", got)
	}
	if _, err := governor.Reserve(60, func() (int64, error) { return 60, nil }); !errors.Is(err, ErrPrecomputeAdmission) {
		t.Fatalf("admission against freshly published ready bytes err = %v, want ErrPrecomputeAdmission", err)
	}
}

func TestPrecomputeCancellationCleansStagingAndReleasesReservation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-cancel", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := t.TempDir()
	now := time.Unix(800, 0).UTC()
	claimed := claimPrecomputeJob(t, st, PrecomputeRequest{
		MediaID: mediaID, RenditionID: "cancel-720p", RungID: "720p", Codec: "h264",
		Height: 720, Bandwidth: 4_256_000, Path: filepath.Join(cacheRoot, "cancel-720p"),
	}, now, "cancel-worker")
	started := make(chan struct{})
	generator := precomputeGeneratorFunc(func(ctx context.Context, spec PrecomputeSpec) error {
		if err := os.MkdirAll(spec.OutputDir, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(spec.OutputDir, "partial"), []byte("partial"), 0o600); err != nil {
			return err
		}
		close(started)
		<-ctx.Done()
		return context.Cause(ctx)
	})
	admission := NewPrecomputeResourceGovernor(1 << 30)
	handler := PrecomputeJobHandler{Store: st, Generator: generator, Admission: admission, Now: func() time.Time { return now }}
	done := make(chan error, 1)
	go func() { done <- handler.Handle(ctx, claimed) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Handle cancellation err = %v, want context.Canceled", err)
	}
	assertPrecomputeResidueEmpty(t, cacheRoot)
	if got := admission.reservedBytes(); got != 0 {
		t.Fatalf("reserved bytes after cancellation = %d, want 0", got)
	}
}

func TestPrecomputeDiskFullCleansStagingAndReleasesReservation(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-disk-full", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := t.TempDir()
	now := time.Unix(810, 0).UTC()
	claimed := claimPrecomputeJob(t, st, PrecomputeRequest{
		MediaID: mediaID, RenditionID: "disk-full-720p", RungID: "720p", Codec: "h264",
		Height: 720, Bandwidth: 4_256_000, Path: filepath.Join(cacheRoot, "disk-full-720p"),
	}, now, "disk-full-worker")
	generator := precomputeGeneratorFunc(func(_ context.Context, spec PrecomputeSpec) error {
		if err := os.MkdirAll(spec.OutputDir, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(spec.OutputDir, "partial"), []byte("partial"), 0o600); err != nil {
			return err
		}
		return syscall.ENOSPC
	})
	admission := NewPrecomputeResourceGovernor(1 << 30)
	handler := PrecomputeJobHandler{Store: st, Generator: generator, Admission: admission, Now: func() time.Time { return now }}
	if err := handler.Handle(ctx, claimed); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Handle disk-full err = %v, want ENOSPC", err)
	}
	assertPrecomputeResidueEmpty(t, cacheRoot)
	if got := admission.reservedBytes(); got != 0 {
		t.Fatalf("reserved bytes after disk-full = %d, want 0", got)
	}
}

func TestPrecomputeRejectsIncompleteArtifactBeforePublication(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-incomplete", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := t.TempDir()
	now := time.Unix(820, 0).UTC()
	claimed := claimPrecomputeJob(t, st, PrecomputeRequest{
		MediaID: mediaID, RenditionID: "incomplete-720p", RungID: "720p", Codec: "h264",
		Height: 720, Bandwidth: 4_256_000, Path: filepath.Join(cacheRoot, "incomplete-720p"),
	}, now, "incomplete-worker")
	generator := precomputeGeneratorFunc(func(_ context.Context, spec PrecomputeSpec) error {
		return os.MkdirAll(filepath.Join(spec.OutputDir, spec.RungID), 0o700)
	})
	handler := PrecomputeJobHandler{Store: st, Generator: generator, Admission: NewPrecomputeResourceGovernor(1 << 30), Now: func() time.Time { return now }}
	if err := handler.Handle(ctx, claimed); !errors.Is(err, ErrPrecomputeIncomplete) {
		t.Fatalf("Handle incomplete err = %v, want ErrPrecomputeIncomplete", err)
	}
	assertPrecomputeResidueEmpty(t, cacheRoot)
}

func TestPrecomputeRejectsPlaylistWithMissingReferencedSegment(t *testing.T) {
	root := t.TempDir()
	rungDir := filepath.Join(root, "720p")
	if err := os.MkdirAll(rungDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "master.m3u8"), []byte("#EXTM3U\n720p/index.m3u8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	playlist := "#EXTM3U\n#EXTINF:4,\nmissing.m4s\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(filepath.Join(rungDir, "index.m3u8"), []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rungDir, "unrelated.m4s"), []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyPrecomputeArtifact(root, "720p"); !errors.Is(err, ErrPrecomputeIncomplete) {
		t.Fatalf("verifyPrecomputeArtifact err = %v, want ErrPrecomputeIncomplete", err)
	}
}

func TestPrecomputeReplacementFencesStalePublisher(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-replace", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := t.TempDir()
	now := time.Unix(830, 0).UTC()
	previousPath := filepath.Join(cacheRoot, "previous-ready")
	writeCacheFile(t, previousPath, "previous")
	if err := st.UpsertPlaybackRendition(ctx, store.PlaybackRendition{
		ID: "replace-720p", MediaID: mediaID, ContentFingerprint: "fp-precompute-replace",
		Kind: store.PlaybackRenditionKindHLS, Status: store.PlaybackRenditionReady,
		RungID: "720p", Codec: "h264", Height: 720, Bandwidth: 4_256_000,
		Path: previousPath, SizeBytes: 8, CreatedAt: now.Add(-time.Hour), LastUsedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed previous rendition: %v", err)
	}
	req := PrecomputeRequest{
		MediaID: mediaID, RenditionID: "replace-720p", RungID: "720p", Codec: "h264",
		Height: 720, Bandwidth: 4_256_000, Path: filepath.Join(cacheRoot, "replace-720p"),
	}
	stale := claimPrecomputeJob(t, st, req, now, "stale-worker")
	staleStarted := make(chan struct{})
	releaseStale := make(chan struct{})
	staleGenerator := precomputeGeneratorFunc(func(_ context.Context, spec PrecomputeSpec) error {
		close(staleStarted)
		<-releaseStale
		return writeCompletePrecomputeArtifact(spec, "stale")
	})
	admission := NewPrecomputeResourceGovernor(1 << 30)
	staleHandler := PrecomputeJobHandler{Store: st, Generator: staleGenerator, Admission: admission, Now: func() time.Time { return now }}
	staleDone := make(chan error, 1)
	go func() { staleDone <- staleHandler.Handle(ctx, stale) }()
	<-staleStarted
	if err := st.CancelJob(ctx, stale.ID, now.Add(time.Second)); err != nil {
		t.Fatalf("CancelJob stale generation: %v", err)
	}
	replacementJob, err := st.RetryJob(ctx, stale.ID, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("RetryJob replacement: %v", err)
	}
	replacement, err := st.ClaimNextJob(ctx, "replacement-worker", []store.JobClass{store.JobClassBackground}, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ClaimNextJob replacement: %v", err)
	}
	if replacement.ID != replacementJob.ID {
		t.Fatalf("replacement claim = %q, want %q", replacement.ID, replacementJob.ID)
	}
	replacementHandler := PrecomputeJobHandler{
		Store: st, Admission: admission, Now: func() time.Time { return now.Add(3 * time.Second) },
		Generator: precomputeGeneratorFunc(func(_ context.Context, spec PrecomputeSpec) error {
			return writeCompletePrecomputeArtifact(spec, "replacement")
		}),
	}
	if err := replacementHandler.Handle(ctx, replacement); err != nil {
		t.Fatalf("replacement Handle: %v", err)
	}
	close(releaseStale)
	if err := <-staleDone; !errors.Is(err, store.ErrJobLeaseLost) && !errors.Is(err, store.ErrPlaybackRenditionGenerationStale) {
		t.Fatalf("stale Handle err = %v, want lease-lost or stale-generation refusal", err)
	}
	lateGeneratorCalls := 0
	lateHandler := PrecomputeJobHandler{
		Store: st, Admission: admission, Now: func() time.Time { return now.Add(4 * time.Second) },
		Generator: precomputeGeneratorFunc(func(context.Context, PrecomputeSpec) error {
			lateGeneratorCalls++
			return nil
		}),
	}
	if err := lateHandler.Handle(ctx, stale); !errors.Is(err, store.ErrJobLeaseLost) {
		t.Fatalf("late stale Handle err = %v, want ErrJobLeaseLost", err)
	}
	if lateGeneratorCalls != 0 {
		t.Fatalf("late stale generator calls = %d, want 0", lateGeneratorCalls)
	}
	rendition, err := st.PlaybackRendition(ctx, req.RenditionID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatalf("PlaybackRendition: %v", err)
	}
	if rendition.Status != store.PlaybackRenditionReady {
		t.Fatalf("rendition status = %q, want ready replacement", rendition.Status)
	}
	segment, err := os.ReadFile(filepath.Join(rendition.Path, req.RungID, "seg00000.m4s"))
	if err != nil {
		t.Fatalf("read replacement segment: %v", err)
	}
	if string(segment) != "replacement" {
		t.Fatalf("published segment = %q, want replacement", segment)
	}
	if _, err := os.Stat(previousPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous publication stat err = %v, want not exist", err)
	}
	assertPublishedGenerationCount(t, cacheRoot, 1)
	if got := admission.reservedBytes(); got != 0 {
		t.Fatalf("reserved bytes after replacement race = %d, want 0", got)
	}
}

func TestPrecomputeInputGenerationChangeCannotPublish(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-input-a", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	files, err := st.MediaFilesForMedia(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || len(files) != 1 {
		t.Fatalf("MediaFilesForMedia = %v, %v", files, err)
	}
	cacheRoot := t.TempDir()
	now := time.Unix(835, 0).UTC()
	claimed := claimPrecomputeJob(t, st, PrecomputeRequest{
		MediaID: mediaID, RenditionID: "input-change-720p", RungID: "720p", Codec: "h264",
		Height: 720, Bandwidth: 4_256_000, Path: filepath.Join(cacheRoot, "input-change-720p"),
	}, now, "input-change-worker")
	started := make(chan struct{})
	finish := make(chan struct{})
	generator := precomputeGeneratorFunc(func(_ context.Context, spec PrecomputeSpec) error {
		close(started)
		<-finish
		return writeCompletePrecomputeArtifact(spec, "obsolete input")
	})
	handler := PrecomputeJobHandler{Store: st, Generator: generator, Admission: NewPrecomputeResourceGovernor(1 << 30), Now: func() time.Time { return now }}
	done := make(chan error, 1)
	go func() { done <- handler.Handle(ctx, claimed) }()
	<-started
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: files[0].Path, Title: "Replaced input", Container: "mkv", Codec: "hevc",
		Class: store.MediaClassVideo, ContentFingerprint: "fp-precompute-input-b", DurationSecondsHint: 120,
		VideoWidthHint: 1920, VideoHeightHint: 1080, Streams: []store.MediaFileStream{videoStream("hevc", "Main 10", 0, "")},
	}); err != nil {
		t.Fatalf("replace input generation: %v", err)
	}
	close(finish)
	if err := <-done; !errors.Is(err, ErrPrecomputeInputChanged) {
		t.Fatalf("Handle changed input err = %v, want ErrPrecomputeInputChanged", err)
	}
	assertPrecomputeResidueEmpty(t, cacheRoot)
}

func TestPrecomputeAtomicPublicationRejectsReplacedInput(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-atomic-a", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	files, err := st.MediaFilesForMedia(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || len(files) != 1 {
		t.Fatalf("MediaFilesForMedia = %v, %v", files, err)
	}
	now := time.Unix(837, 0).UTC()
	req := PrecomputeRequest{
		MediaID: mediaID, ContentFingerprint: files[0].ContentFingerprint, SourcePath: files[0].Path,
		RenditionID: "atomic-input-720p", RungID: "720p", Codec: "h264",
		Height: 720, Bandwidth: 4_256_000, Path: filepath.Join(t.TempDir(), "atomic-input-720p"),
	}
	claimed := claimPrecomputeJob(t, st, req, now, "atomic-input-worker")
	generation, _, err := st.StartPlaybackRenditionGeneration(
		ctx, req.playbackRendition(store.PlaybackRenditionGenerating, 0, now), claimed.ID, claimed.LeaseOwner,
	)
	if err != nil {
		t.Fatalf("StartPlaybackRenditionGeneration: %v", err)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: req.SourcePath, Title: "Replaced input", Container: "mkv", Codec: "hevc",
		Class: store.MediaClassVideo, ContentFingerprint: "fp-precompute-atomic-b", DurationSecondsHint: 120,
		VideoWidthHint: 1920, VideoHeightHint: 1080, Streams: []store.MediaFileStream{videoStream("hevc", "Main 10", 0, "")},
	}); err != nil {
		t.Fatalf("replace input after verification: %v", err)
	}
	ready := req.playbackRendition(store.PlaybackRenditionReady, 100, now.Add(time.Second))
	ready.CreatedAt = time.UnixMilli(generation).UTC()
	if err := st.PublishPlaybackRenditionGeneration(ctx, ready, req.SourcePath, generation, claimed.ID, claimed.LeaseOwner); !errors.Is(err, store.ErrPlaybackRenditionInputStale) {
		t.Fatalf("PublishPlaybackRenditionGeneration err = %v, want ErrPlaybackRenditionInputStale", err)
	}
}

func TestSweepPrecomputeResidueRecoversCrashWithoutDeletingReadyGeneration(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-crash", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := t.TempDir()
	mediaDir := fmt.Sprintf("%x", []byte(mediaID))
	readyPath := filepath.Join(cacheRoot, ".precompute-published", mediaDir, "ready")
	orphanPath := filepath.Join(cacheRoot, ".precompute-published", mediaDir, "rename-before-cas")
	stagingPath := filepath.Join(cacheRoot, ".precompute-staging", "generator-crash.stage")
	writeCacheFile(t, readyPath, "ready")
	writeCacheFile(t, orphanPath, "orphan")
	writeCacheFile(t, stagingPath, "partial")
	now := time.Unix(840, 0).UTC()
	insertPrecomputeCacheRow(t, st, mediaID, "crash-ready", readyPath, 100, now)
	removed, err := SweepPrecomputeResidue(ctx, st, cacheRoot)
	if err != nil {
		t.Fatalf("SweepPrecomputeResidue: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed crash entries = %d, want 2", removed)
	}
	if _, err := os.Stat(readyPath); err != nil {
		t.Fatalf("ready generation removed: %v", err)
	}
	if _, err := os.Stat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan publication stat err = %v, want not exist", err)
	}
	if _, err := os.Stat(stagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("crash staging stat err = %v, want not exist", err)
	}
}

func TestFFmpegPrecomputeGeneratorRejectsUnsafeSpecBeforeTouchingRungDir(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "output")
	victim := filepath.Join(root, "victim", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(victim), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := (FFmpegPrecomputeGenerator{}).PrecomputeHLS(context.Background(), PrecomputeSpec{
		MediaID:            "media-alpha",
		ContentFingerprint: "sha256:alpha",
		RenditionID:        "spec-unsafe-rung",
		RungID:             "../victim",
		Codec:              "h264",
		Height:             720,
		Bandwidth:          4_256_000,
		SourcePath:         filepath.Join(root, "alpha.mkv"),
		OutputDir:          outputDir,
	})
	if err == nil {
		t.Fatal("PrecomputeHLS accepted unsafe spec rung")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim stat err = %v, want retained", err)
	}
}

func TestPrecomputeEvictJobHandlerRemovesOverBudgetCaches(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-evict-job-precompute", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := t.TempDir()
	base := time.Unix(450, 0).UTC()
	oldPath := filepath.Join(cacheRoot, "job-old-480p")
	newPath := filepath.Join(cacheRoot, "job-new-480p")
	writeCacheFile(t, oldPath, "old segment")
	writeCacheFile(t, newPath, "new segment")
	insertPrecomputeCacheRow(t, st, mediaID, "job-old-480p", oldPath, 100, base)
	insertPrecomputeCacheRow(t, st, mediaID, "job-new-480p", newPath, 100, base.Add(time.Second))

	job, err := RequestPrecomputeEviction(ctx, st, cacheRoot, 100, base.Add(time.Minute))
	if err != nil {
		t.Fatalf("RequestPrecomputeEviction: %v", err)
	}
	handler := PrecomputeEvictJobHandler{Store: st, Now: func() time.Time { return base.Add(time.Hour) }}
	registry := jobs.NewRegistry(st, "playback-precompute-evict-test")
	registry.Register(PrecomputeEvictJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		return jobs.Result{}, handler.Handle(ctx, job)
	})

	claimed, err := registry.RunOne(ctx, []store.JobClass{store.JobClassBackground}, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job = %q, want %q", claimed.ID, job.ID)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old cache path stat err = %v, want not exist", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new cache path stat err = %v, want retained", err)
	}
	completed, err := st.Job(ctx, job.ID)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	if completed.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q, want completed", completed.Status)
	}
}

func TestEvictPrecomputedRenditionsOverBudgetRemovesCacheDirectories(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-evict-precompute", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := t.TempDir()
	base := time.Unix(500, 0).UTC()
	oldPath := filepath.Join(cacheRoot, "old-480p")
	newPath := filepath.Join(cacheRoot, "new-480p")
	writeCacheFile(t, oldPath, "old segment")
	writeCacheFile(t, newPath, "new segment")
	insertPrecomputeCacheRow(t, st, mediaID, "old-480p", oldPath, 100, base)
	insertPrecomputeCacheRow(t, st, mediaID, "new-480p", newPath, 100, base.Add(time.Second))

	evicted, err := EvictPrecomputedRenditionsOverBudget(ctx, st, cacheRoot, 100, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("EvictPrecomputedRenditionsOverBudget: %v", err)
	}
	if len(evicted) != 1 || evicted[0].ID != "old-480p" {
		t.Fatalf("evicted = %+v, want old-480p", evicted)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old cache path stat err = %v, want not exist", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new cache path stat err = %v, want retained", err)
	}
}

func TestEvictPrecomputedRenditionsOverBudgetRejectsUnsafeRungBeforeDelete(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-evict-unsafe-rung", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := t.TempDir()
	cachePath := filepath.Join(cacheRoot, "unsafe-rung")
	writeCacheFile(t, cachePath, "keep segment")
	now := time.Unix(550, 0).UTC()
	if err := st.UpsertPlaybackRendition(ctx, store.PlaybackRendition{
		ID:                 "unsafe-rung",
		MediaID:            mediaID,
		ContentFingerprint: "sha256:unsafe-rung",
		Kind:               store.PlaybackRenditionKindHLS,
		Status:             store.PlaybackRenditionReady,
		RungID:             "/720p",
		Codec:              "h264",
		Height:             720,
		Bandwidth:          4_256_000,
		Path:               cachePath,
		SizeBytes:          100,
		CreatedAt:          now,
		LastUsedAt:         now,
	}); err != nil {
		t.Fatalf("UpsertPlaybackRendition: %v", err)
	}

	evicted, err := EvictPrecomputedRenditionsOverBudget(ctx, st, cacheRoot, 0, now.Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), "not a clean relative segment") {
		t.Fatalf("EvictPrecomputedRenditionsOverBudget err = %v, want unsafe rung", err)
	}
	if len(evicted) != 1 || evicted[0].ID != "unsafe-rung" {
		t.Fatalf("evicted = %+v, want unsafe-rung", evicted)
	}
	if _, err := os.Stat(filepath.Join(cachePath, "segment.m4s")); err != nil {
		t.Fatalf("cache file stat err = %v, want retained", err)
	}
}

func TestEvictPrecomputedRenditionsOverBudgetRefusesOutOfRootCleanup(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	mediaID := importProbedMedia(t, st, "fp-evict-outside-precompute", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(t.TempDir(), "user-owned")
	writeCacheFile(t, outsidePath, "do not remove")
	now := time.Unix(600, 0).UTC()
	insertPrecomputeCacheRow(t, st, mediaID, "outside-480p", outsidePath, 100, now)

	evicted, err := EvictPrecomputedRenditionsOverBudget(ctx, st, cacheRoot, 0, now.Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), "outside cache root") {
		t.Fatalf("EvictPrecomputedRenditionsOverBudget err = %v, want outside cache root", err)
	}
	if len(evicted) != 1 || evicted[0].ID != "outside-480p" {
		t.Fatalf("evicted = %+v, want outside-480p", evicted)
	}
	if _, err := os.Stat(filepath.Join(outsidePath, "segment.m4s")); err != nil {
		t.Fatalf("outside cache file stat err = %v, want retained", err)
	}
	rendition, err := st.PlaybackRendition(ctx, "outside-480p", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatalf("PlaybackRendition: %v", err)
	}
	if rendition.Status != store.PlaybackRenditionEvicted {
		t.Fatalf("rendition status = %q, want evicted despite cleanup refusal", rendition.Status)
	}
}

type fakePrecomputeGenerator struct {
	calls []PrecomputeSpec
}

type precomputeGeneratorFunc func(context.Context, PrecomputeSpec) error

func (f precomputeGeneratorFunc) PrecomputeHLS(ctx context.Context, spec PrecomputeSpec) error {
	return f(ctx, spec)
}

func (f *fakePrecomputeGenerator) PrecomputeHLS(_ context.Context, spec PrecomputeSpec) error {
	f.calls = append(f.calls, spec)
	return writeCompletePrecomputeArtifact(spec, "regenerated segment")
}

func writeCompletePrecomputeArtifact(spec PrecomputeSpec, segment string) error {
	if err := os.MkdirAll(filepath.Join(spec.OutputDir, spec.RungID), 0o700); err != nil {
		return err
	}
	master := fmt.Sprintf("#EXTM3U\n%s/index.m3u8\n", spec.RungID)
	if err := os.WriteFile(filepath.Join(spec.OutputDir, "master.m3u8"), []byte(master), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(spec.OutputDir, spec.RungID, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:4,\nseg00000.m4s\n#EXT-X-ENDLIST\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(spec.OutputDir, spec.RungID, "seg00000.m4s"), []byte(segment), 0o600)
}

func claimPrecomputeJob(t *testing.T, st *store.Store, req PrecomputeRequest, now time.Time, worker string) store.Job {
	t.Helper()
	job, err := RequestPrecomputeRendition(context.Background(), st, req, now)
	if err != nil {
		t.Fatalf("RequestPrecomputeRendition: %v", err)
	}
	claimed, err := st.ClaimNextJob(context.Background(), worker, []store.JobClass{store.JobClassBackground}, now)
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job = %q, want %q", claimed.ID, job.ID)
	}
	return claimed
}

func assertPrecomputeResidueEmpty(t *testing.T, cacheRoot string) {
	t.Helper()
	for _, name := range []string{".precompute-staging", ".precompute-published"} {
		root := filepath.Join(cacheRoot, name)
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("ReadDir(%s): %v", root, err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s residue = %v, want empty", name, entries)
		}
	}
}

func assertPublishedGenerationCount(t *testing.T, cacheRoot string, want int) {
	t.Helper()
	count := 0
	root := filepath.Join(cacheRoot, ".precompute-published")
	mediaDirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir published root: %v", err)
	}
	for _, mediaDir := range mediaDirs {
		generations, err := os.ReadDir(filepath.Join(root, mediaDir.Name()))
		if err != nil {
			t.Fatalf("ReadDir published media: %v", err)
		}
		count += len(generations)
	}
	if count != want {
		t.Fatalf("published generation count = %d, want %d", count, want)
	}
}

func writeCacheFile(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "segment.m4s"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func insertPrecomputeCacheRow(t *testing.T, st *store.Store, mediaID store.MediaID, id string, path string, sizeBytes int64, lastUsed time.Time) {
	t.Helper()
	if err := st.UpsertPlaybackRendition(context.Background(), store.PlaybackRendition{
		ID:                 store.PlaybackRenditionID(id),
		MediaID:            mediaID,
		ContentFingerprint: "sha256:" + id,
		Kind:               store.PlaybackRenditionKindHLS,
		Status:             store.PlaybackRenditionReady,
		RungID:             "480p",
		Codec:              "h264",
		Height:             480,
		Bandwidth:          1_756_000,
		Path:               path,
		SizeBytes:          sizeBytes,
		CreatedAt:          lastUsed,
		LastUsedAt:         lastUsed,
	}); err != nil {
		t.Fatalf("UpsertPlaybackRendition %s: %v", id, err)
	}
}
