package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// probingTranscoder is a fake transcoder that can also probe, counting probes.
type probingTranscoder struct {
	fakeTranscoder
	probeMu  sync.Mutex
	probes   int
	duration int
}

func (p *probingTranscoder) Probe(context.Context, string) (ffmpeg.Probe, error) {
	p.probeMu.Lock()
	defer p.probeMu.Unlock()
	p.probes++
	return ffmpeg.Probe{DurationSeconds: p.duration}, nil
}

func (p *probingTranscoder) probeCount() int {
	p.probeMu.Lock()
	defer p.probeMu.Unlock()
	return p.probes
}

func TestInfoProbesAMissingRuntimeOnce(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	prober := &probingTranscoder{duration: 7152}
	sessions := newTestSessions(t, prober, t.TempDir(), nil)
	defer sessions.Close()
	service.SetSessions(sessions)
	writes := 0
	record := sessions.recordDuration
	sessions.recordDuration = func(ctx context.Context, libraryID store.LibraryID, mediaID store.MediaID, fingerprint, path string, seconds int) (bool, error) {
		writes++
		if writes == 1 {
			return false, errors.New("test runtime write failure")
		}
		if writes == 2 {
			return false, nil // the row was ineligible when the UPDATE ran
		}
		return record(ctx, libraryID, mediaID, fingerprint, path, seconds)
	}

	path := filepath.Join(t.TempDir(), "no-runtime.bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := store.SampleMediaFileContentChecked(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "No runtime",
		Container:          "mkv",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: fingerprint,
		VideoWidthHint:     1920,
		VideoHeightHint:    1080,
		Streams:            []store.MediaFileStream{videoStream("h264", "High", 0, ""), audioStream(1, "aac", 2)},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if media.DurationSeconds != 0 {
		t.Fatalf("stored DurationSeconds = %d, want 0 for this fixture", media.DurationSeconds)
	}

	for range 4 {
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: main10_1080p})
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if resp.Timeline.DurationSeconds != 7152 {
			t.Fatalf("Timeline.DurationSeconds = %d, want the probed 7152", resp.Timeline.DurationSeconds)
		}
	}
	if got := prober.probeCount(); got != 1 {
		t.Fatalf("probes = %d, want 1 (cached by file generation)", got)
	}
	if writes != 3 {
		t.Fatalf("write-back attempts = %d, want 3 (one error, one no-op, one update)", writes)
	}
}

func TestInfoReturnsBeforeLegacyRuntimeVerification(t *testing.T) {
	for _, name := range []string{"matching", "replaced"} {
		t.Run(name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			prober := &probingTranscoder{duration: 180}
			sessions := newTestSessions(t, prober, t.TempDir(), nil)
			service.SetSessions(sessions)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			path := filepath.Join(t.TempDir(), "legacy.mkv")
			seedScratchFile(t, path)
			fingerprint, err := store.HashMediaFileContent(path)
			if err != nil {
				t.Fatal(err)
			}
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: "library-local", Path: path, Title: "Legacy runtime", Container: "mkv",
				Class: store.MediaClassVideo, Codec: "h264", ContentFingerprint: fingerprint,
			})
			if err != nil {
				t.Fatal(err)
			}
			if name == "replaced" {
				if err := os.WriteFile(path, []byte("different source"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			verificationStarted, release := scratchLatch(t)
			defer release.open()
			var verifications atomic.Int32
			sessions.matchDurationFile = func(ctx context.Context, path, generation, fingerprint string) (bool, error) {
				if verifications.Add(1) == 1 {
					close(verificationStarted)
				}
				<-release.ch
				return store.MediaFileMatchesFingerprint(ctx, path, generation, fingerprint)
			}
			play := func() <-chan int {
				result := make(chan int, 1)
				go func() {
					info, err := service.Info(ctx, scope, InfoRequest{MediaID: string(media.ID)})
					if err != nil {
						t.Errorf("Info: %v", err)
					}
					result <- info.Timeline.DurationSeconds
				}()
				return result
			}
			first := play()
			waitScratchSignal(t, verificationStarted)
			for _, result := range []<-chan int{first, play()} {
				if got := testResult(t, result); got != 180 {
					t.Fatalf("playback runtime = %d, want 180 while verification is blocked", got)
				}
			}
			cancel() // The completed requests no longer own the write.
			checkStored := func(want int) {
				t.Helper()
				got, err := st.Media(t.Context(), media.ID, scope.LibraryFilter())
				if err != nil || got.DurationSeconds != want {
					t.Fatalf("stored runtime = %d, %v; want %d", got.DurationSeconds, err, want)
				}
			}
			checkStored(0)
			sessions.mu.Lock()
			if sessions.libraryChanged == nil {
				sessions.libraryChanged = make(chan struct{})
			}
			retired := sessions.libraryChanged
			sessions.mu.Unlock()
			release.open()
			waitScratchSignal(t, retired)
			want := 180
			if name == "replaced" {
				want = 0
			}
			checkStored(want)
			if got := verifications.Load(); got != 1 || prober.probeCount() != 1 {
				t.Fatalf("got %d verifications and %d probes, want one each", got, prober.probeCount())
			}
		})
	}
}

func TestInfoPersistsCachedRuntimeAfterVersionMergeUndo(t *testing.T) {
	ctx := t.Context()
	service, st, scope := capabilityTestService(t)
	prober := &probingTranscoder{duration: 180}
	sessions := newTestSessions(t, prober, t.TempDir(), nil)
	service.SetSessions(sessions)
	dir := t.TempDir()
	importSource := func(name string, width, height int) store.Media {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		fingerprint, err := store.SampleMediaFileContentChecked(ctx, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: "library-local", Path: path, Title: "Runtime fixture", Container: "mkv",
			Class: store.MediaClassVideo, Codec: "h264", ContentFingerprint: fingerprint,
			VideoWidthHint: width, VideoHeightHint: height,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media
	}
	primary := importSource("Runtime Fixture.2026.1080p.Remux.mkv", 1920, 800)
	other := importSource("Runtime Fixture.2026.2160p.WEB-DL.mkv", 3840, 1600)
	now := time.Now()
	merge, err := st.MergeSplitVersions(ctx, primary.LibraryID, []store.MediaID{primary.ID, other.ID}, now, store.VersionMergeOptions{Survivor: primary.ID})
	if err != nil || merge.BatchID == "" || merge.Survivor != primary.ID || len(merge.Merged) != 1 || merge.Merged[0] != other.ID {
		t.Fatalf("merge = %+v, %v", merge, err)
	}
	filter := scope.LibraryFilter()
	files, err := st.MediaFilesForMedia(ctx, primary.ID, filter)
	if err != nil || len(files) != 2 || filepath.Base(files[0].Path) != "Runtime Fixture.2026.1080p.Remux.mkv" ||
		filepath.Base(files[1].Path) != "Runtime Fixture.2026.2160p.WEB-DL.mkv" {
		t.Fatalf("merged files = %+v, %v; want the survivor's original source as primary", files, err)
	}
	play := func(id store.MediaID, version string) {
		t.Helper()
		info, err := service.Info(ctx, scope, InfoRequest{MediaID: string(id), SelectedVersion: version})
		if err != nil || info.Timeline.DurationSeconds != 180 {
			t.Fatalf("playback runtime = %d, %v; want 180", info.Timeline.DurationSeconds, err)
		}
	}
	play(primary.ID, versionID(primary.ID, files[1].VersionLabel))
	if got, err := st.Media(ctx, primary.ID, filter); err != nil || got.DurationSeconds != 0 {
		t.Fatalf("survivor runtime = %d, %v; want 0 after alternate playback", got.DurationSeconds, err)
	}
	play(primary.ID, "")
	if got, err := st.Media(ctx, primary.ID, filter); err != nil || got.DurationSeconds != 180 {
		t.Fatalf("survivor runtime = %d, %v; want 180 after primary playback", got.DurationSeconds, err)
	}
	if _, err := st.UndoBatch(ctx, merge.BatchID, filter, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Media(ctx, primary.ID, filter); err != nil || got.DurationSeconds != 180 {
		t.Fatalf("survivor runtime = %d, %v; want 180 after undo", got.DurationSeconds, err)
	}
	play(other.ID, "")
	if got, err := st.Media(ctx, other.ID, filter); err != nil || got.DurationSeconds != 180 || prober.probeCount() != 2 {
		t.Fatalf("restored runtime = %d, %v with %d probes; want 180 with one probe per source", got.DurationSeconds, err, prober.probeCount())
	}
}

func TestInfoKeepsAStoredRuntimeWithoutProbing(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	prober := &probingTranscoder{duration: 7152}
	sessions := newTestSessions(t, prober, t.TempDir(), nil)
	defer sessions.Close()
	service.SetSessions(sessions)
	mediaID := importProbedMedia(t, st, "fp-has-runtime", "mkv", "h264", 1920, 1080, []store.MediaFileStream{
		videoStream("h264", "High", 0, ""), audioStream(1, "aac", 2),
	})

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: main10_1080p})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Timeline.DurationSeconds != 120 || prober.probeCount() != 0 {
		t.Fatalf("Timeline.DurationSeconds = %d, probes = %d; want the stored 120 and no probe", resp.Timeline.DurationSeconds, prober.probeCount())
	}
}

func probeDurationWithLibraryClaim(ctx context.Context, sessions *Sessions, fingerprint, path string) int {
	op, err := sessions.claimLibrary(ctx, "duration-test", sessions.admissionStamp())
	if err != nil {
		return 0
	}
	defer sessions.releaseLibrary(op)
	return sessions.probedSourceDuration(sessions.libraryProbeContext(op), nil, op, "", fingerprint, path)
}

func TestProbedDurationSecondsRemembersAMissForAWhile(t *testing.T) {
	x, y := filepath.Join(t.TempDir(), "x.mkv"), filepath.Join(t.TempDir(), "y.mkv")
	seedScratchFile(t, x)
	seedScratchFile(t, y)
	prober := &probingTranscoder{}
	now := time.Unix(1000, 0)
	sessions := newTestSessions(t, prober, t.TempDir(), func() time.Time { return now })
	defer sessions.Close()

	if got := probeDurationWithLibraryClaim(t.Context(), sessions, "fp", x); got != 0 {
		t.Fatalf("first probe = %d, want 0", got)
	}
	_ = probeDurationWithLibraryClaim(t.Context(), sessions, "fp", x)
	if prober.probeCount() != 1 {
		t.Fatalf("probes = %d, want 1 within the miss TTL", prober.probeCount())
	}
	now = now.Add(durationProbeMissTTL)
	prober.duration = 60
	if got := probeDurationWithLibraryClaim(t.Context(), sessions, "fp", x); got != 60 || prober.probeCount() != 2 {
		t.Fatalf("after TTL got %d with %d probes, want 60 with 2", got, prober.probeCount())
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	prober.duration = 0
	if got := probeDurationWithLibraryClaim(cancelled, sessions, "fp-other", y); got != 0 {
		t.Fatalf("cancelled probe = %d, want 0", got)
	}
	prober.duration = 30
	if got := probeDurationWithLibraryClaim(t.Context(), sessions, "fp-other", y); got != 30 {
		t.Fatalf("probe after a cancelled request = %d, want 30 (a cancelled miss is not cached)", got)
	}
}
