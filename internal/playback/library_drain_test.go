package playback

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const drainLibraryA store.LibraryID = "library-local"

type testDecisionLookup struct {
	decisionLookup
	onProfile    func()
	onMedia      func(store.MediaID)
	onRestricted func(store.LibraryID)
}

func (s *testDecisionLookup) Profile(ctx context.Context, id store.ProfileID) (store.Profile, error) {
	if s.onProfile != nil {
		s.onProfile()
	}
	return s.decisionLookup.Profile(ctx, id)
}

func (s *testDecisionLookup) Media(ctx context.Context, id store.MediaID, filter store.LibraryFilter) (store.Media, error) {
	media, err := s.decisionLookup.Media(ctx, id, filter)
	if s.onMedia != nil {
		s.onMedia(id)
	}
	return media, err
}

func (s *testDecisionLookup) LibraryRestricted(ctx context.Context, id store.LibraryID) (bool, error) {
	if s.onRestricted != nil {
		s.onRestricted(id)
	}
	return s.decisionLookup.LibraryRestricted(ctx, id)
}

type drainRecorder func(store.MediaID)

func (r drainRecorder) RecordStreamToken(_ context.Context, _ store.SessionID, _ store.ProfileID, _ store.DeviceID, _ string, media store.MediaID, _ bool, _ time.Time) error {
	r(media)
	return nil
}

func importDrainMedia(t *testing.T, st *store.Store, library store.LibraryID, class store.MediaClass, duration int) (store.MediaID, string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "source")
	seedScratchFile(t, src)
	container, codec := "mp4", "h264"
	streams := []store.MediaFileStream{videoStream(codec, "High", 0, ""), audioStream(1, "aac", 2)}
	if class == store.MediaClassAudio {
		container, codec = "mp3", "mp3"
		streams = []store.MediaFileStream{audioStream(0, "mp3", 2)}
	}
	item, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID: library, Path: src, Title: "Source", Class: class, Container: container, Codec: codec,
		ContentFingerprint: filepath.Base(filepath.Dir(src)), DurationSecondsHint: duration, Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item.ID, src
}

func drainTestService(t *testing.T, prober Transcoder) (*Service, *Sessions, auth.Scope, store.LibraryID) {
	t.Helper()
	service, st, scope := capabilityTestService(t)
	b, err := st.CreateLibrary(t.Context(), "Other Library", false, "", scope.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	scope.LibraryIDs = append(scope.LibraryIDs, b.ID)
	s := newTestSessions(t, prober, t.TempDir(), nil)
	s.SetTranscodeCap(4)
	service.SetSessions(s)
	return service, s, scope, b.ID
}

func bindDrainLibrary(t *testing.T, s *Sessions, library store.LibraryID, key SessionKey) *LiveBinding {
	t.Helper()
	b, err := s.BindLive(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Release)
	if err := b.Attach(library, "", nil); err != nil {
		t.Fatal(err)
	}
	return b
}

type drainResult struct {
	fence DrainFence
	err   error
}

func startTestDrain(ctx context.Context, s *Sessions, library store.LibraryID, fence DrainFence) <-chan drainResult {
	result := make(chan drainResult, 1)
	go func() {
		fence, err := s.Drain(ctx, library, fence)
		result <- drainResult{fence, err}
	}()
	return result
}

func testResult[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for playback result")
		var zero T
		return zero
	}
}

func assertDrainWaiting(t *testing.T, result <-chan drainResult) {
	t.Helper()
	select {
	case early := <-result:
		t.Fatalf("drain returned before its owner finished: %+v", early)
	default:
	}
}

func drainFile(ctx context.Context, s *Sessions, library store.LibraryID, key SessionKey, source string, copyVideo bool, name string) (string, error) {
	return s.FilePathShaped(ctx, key, source, 120, 1280, 720, copyVideo, false, ladderCaps{}, VideoSignal{},
		-1, ffmpeg.AudioProfile{}, nil, StreamShape{LibraryID: library}, PriorityNormal, name)
}

func TestT4PlaybackInfoLibraryFence(t *testing.T) {
	for _, tc := range []struct {
		stage string
		joins bool
	}{
		{"EOF cache stat", true}, {"graphics stat", true}, {"restriction lookup", true},
		{"initial media lookup", false}, {"profile lookup", false}, {"profile lookup for next", false},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			prober := &probingTranscoder{duration: 120}
			svc, s, scope, other := drainTestService(t, prober)
			class, duration := store.MediaClassVideo, 120
			if tc.stage == "graphics stat" {
				class = store.MediaClassAudio
			}
			if !tc.joins {
				duration = 0
			}
			a, src := importDrainMedia(t, svc.store, drainLibraryA, class, duration)
			b, _ := importDrainMedia(t, svc.store, other, store.MediaClassVideo, 120)
			var grants atomic.Int32
			svc.tokens.SetRecorder(drainRecorder(func(media store.MediaID) {
				if media == a {
					grants.Add(1)
				}
			}))
			lookup := &testDecisionLookup{decisionLookup: svc.decisionLookup}
			svc.decisionLookup = lookup
			entered, release := scratchLatch(t)
			defer release.open()
			var blocked atomic.Bool
			latch := func() {
				if blocked.CompareAndSwap(false, true) {
					close(entered)
					<-release.ch
				}
			}
			switch tc.stage {
			case "EOF cache stat":
				info, err := os.Stat(src)
				if err != nil {
					t.Fatal(err)
				}
				s.incompleteSources.rememberVerified(src, info)
				replacement := filepath.Join(filepath.Dir(src), "replacement")
				seedScratchFile(t, replacement)
				if err := os.Rename(replacement, src); err != nil {
					t.Fatal(err)
				}
				s.statSource = func(path string) (os.FileInfo, error) {
					if path == src {
						latch()
					}
					return os.Stat(path) // #nosec G703 -- fixture uses its private source and sidecar.
				}
			case "graphics stat":
				graphics := filepath.Join(t.TempDir(), "graphics")
				seedScratchFile(t, graphics)
				svc.SetPictureComposer(staticComposer(ComposedPicture{GraphicsPath: graphics}, true, nil))
				s.statSource = func(path string) (os.FileInfo, error) {
					if path == graphics {
						latch()
					}
					return os.Stat(path) // #nosec G703 -- fixture uses its private source and sidecar.
				}
			case "restriction lookup":
				lookup.onRestricted = func(id store.LibraryID) {
					if id == drainLibraryA {
						latch()
					}
				}
			case "initial media lookup":
				lookup.onMedia = func(id store.MediaID) {
					if id == a {
						latch()
					}
				}
			default:
				lookup.onProfile = latch
			}
			req := InfoRequest{MediaID: string(a), CapabilityProfile: videoClient}
			next := tc.stage == "profile lookup for next"
			if next {
				req.MediaID, req.NextMediaID = string(b), string(a)
			}
			witness := bindDrainLibrary(t, s, drainLibraryA, SessionKey{SessionID: "fence witness"})
			type infoResult struct {
				resp InfoResponse
				err  error
			}
			result := make(chan infoResult, 1)
			go func() {
				resp, err := svc.Info(t.Context(), scope, req)
				result <- infoResult{resp, err}
			}()
			waitScratchSignal(t, entered)
			drained := startTestDrain(t.Context(), s, drainLibraryA, 0)
			waitScratchSignal(t, witness.session.cutoffCtx.Done())
			if tc.joins {
				assertDrainWaiting(t, drained)
			}
			if resp, err := svc.Info(t.Context(), scope, InfoRequest{MediaID: string(b), CapabilityProfile: videoClient}); err != nil || len(resp.Streams) == 0 {
				t.Fatalf("other library could not proceed: %s, %v", resp.Decision, err)
			}
			if tc.joins {
				release.open()
			}
			d := testResult(t, drained)
			if d.err != nil {
				t.Fatal(d.err)
			}
			if err := s.Reopen(drainLibraryA, d.fence); err != nil {
				t.Fatal(err)
			}
			release.open()
			old := testResult(t, result)
			if old.err != nil || old.resp.Next != nil || (!next && len(old.resp.Streams) != 0) ||
				(next && len(old.resp.Streams) == 0) || grants.Load() != 0 || prober.probeCount() != 0 {
				t.Fatalf("old decision = %s, streams = %d, next = %t, err = %v, A grants/probes = %d/%d",
					old.resp.Decision, len(old.resp.Streams), old.resp.Next != nil, old.err, grants.Load(), prober.probeCount())
			}
			fresh, err := svc.Info(t.Context(), scope, InfoRequest{MediaID: string(a), CapabilityProfile: videoClient})
			if err != nil || len(fresh.Streams) == 0 || grants.Load() == 0 {
				t.Fatalf("fresh decision = %s, grants = %d, err = %v", fresh.Decision, grants.Load(), err)
			}
		})
	}
}

type drainDurationProbe struct {
	fakeTranscoder
	probe func(context.Context, string) (ffmpeg.Probe, error)
}

func (f *drainDurationProbe) Probe(ctx context.Context, path string) (ffmpeg.Probe, error) {
	return f.probe(ctx, path)
}

func TestT4DurationOwnership(t *testing.T) {
	for _, cutoff := range []string{"Drain", "Close"} {
		for _, caller := range []string{"HLS", "playback-info"} {
			t.Run(cutoff+"/"+caller, func(t *testing.T) {
				entered, release := scratchLatch(t)
				cancelled := make(chan struct{})
				f := &drainDurationProbe{}
				svc, s, scope, other := drainTestService(t, f)
				defer release.open()
				a, src := importDrainMedia(t, svc.store, drainLibraryA, store.MediaClassVideo, 0)
				b, _ := importDrainMedia(t, svc.store, other, store.MediaClassVideo, 0)
				var grants atomic.Int32
				svc.tokens.SetRecorder(drainRecorder(func(id store.MediaID) {
					if id == a {
						grants.Add(1)
					}
				}))
				f.probe = func(ctx context.Context, path string) (ffmpeg.Probe, error) {
					if path == src {
						close(entered)
						<-ctx.Done()
						close(cancelled)
						<-release.ch // Simulate a running child whose reap is held.
						return ffmpeg.Probe{}, ctx.Err()
					}
					return ffmpeg.Probe{DurationSeconds: 120}, nil
				}
				binding := bindDrainLibrary(t, s, drainLibraryA, SessionKey{SessionID: "duration"})
				requestA := make(chan error, 1)
				go func() {
					if caller == "HLS" {
						if seconds := binding.ProbedDurationSeconds(t.Context(), "source", src); seconds != 0 {
							requestA <- fmt.Errorf("cut-off HLS duration = %d", seconds)
							return
						}
						requestA <- nil
						return
					}
					resp, err := svc.Info(t.Context(), scope, InfoRequest{MediaID: string(a), CapabilityProfile: videoClient})
					if err == nil && len(resp.Streams) != 0 {
						err = errors.New("cut-off decision published streams")
					}
					requestA <- err
				}()
				waitScratchSignal(t, entered)
				closed := make(chan error, 1)
				var drained <-chan drainResult
				if cutoff == "Close" {
					go func() { closed <- s.Close() }()
				} else {
					drained = startTestDrain(t.Context(), s, drainLibraryA, 0)
				}
				waitScratchSignal(t, cancelled)
				if cutoff == "Close" {
					select {
					case err := <-closed:
						t.Fatalf("Close returned before probe exit: %v", err)
					default:
					}
				} else {
					assertDrainWaiting(t, drained)
					resp, err := svc.Info(t.Context(), scope, InfoRequest{MediaID: string(b), CapabilityProfile: videoClient})
					if err != nil || resp.Timeline.DurationSeconds != 120 || len(resp.Streams) == 0 {
						t.Fatalf("B duration decision = %d, streams = %d, %v", resp.Timeline.DurationSeconds, len(resp.Streams), err)
					}
				}
				release.open()
				if cutoff == "Close" {
					if err := testResult(t, closed); err != nil {
						t.Fatal(err)
					}
				} else if d := testResult(t, drained); d.err != nil {
					t.Fatal(d.err)
				}
				if err := testResult(t, requestA); err != nil || grants.Load() != 0 {
					t.Fatalf("A returned %v with %d grants", err, grants.Load())
				}
			})
		}
	}
}

func TestT4FencedPlaybackInfoProbeLeavesNoCachedMiss(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	marker := filepath.Join(dir, "probe-started")
	t.Setenv("BEAMFALL_T4_PROBE_STARTED", marker)
	const tool = `#!/bin/sh
printf started > "$BEAMFALL_T4_PROBE_STARTED"
printf '%s\n' '{"format":{"duration":"120"}}'
`
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(tool), 0o700); err != nil { // #nosec G306 -- private executable test fixture.
		t.Fatal(err)
	}
	runner := ffmpeg.New()
	f := &drainDurationProbe{}
	svc, s, scope, _ := drainTestService(t, f)
	probeEntered, releaseProbe := scratchLatch(t)
	cancelEntered, releaseCancel := scratchLatch(t)
	defer releaseProbe.open()
	defer releaseCancel.open()
	a, _ := importDrainMedia(t, svc.store, drainLibraryA, store.MediaClassVideo, 0)
	var probeCalls, grants atomic.Int32
	probeResult := make(chan error, 1)
	f.probe = func(ctx context.Context, path string) (ffmpeg.Probe, error) {
		if probeCalls.Add(1) != 1 {
			return runner.Probe(ctx, path)
		}
		close(probeEntered)
		<-releaseProbe.ch
		probe, err := runner.Probe(ctx, path)
		probeResult <- err
		return probe, err
	}
	svc.tokens.SetRecorder(drainRecorder(func(store.MediaID) { grants.Add(1) }))
	info := func() (InfoResponse, error) {
		return svc.Info(t.Context(), scope, InfoRequest{MediaID: string(a), CapabilityProfile: videoClient})
	}
	request := make(chan error, 1)
	go func() {
		resp, err := info()
		if err == nil && len(resp.Streams) != 0 {
			err = fmt.Errorf("fenced decision returned %d streams", len(resp.Streams))
		}
		request <- err
	}()
	waitScratchSignal(t, probeEntered)
	s.mu.Lock()
	claims := len(s.libraryOps)
	for op := range s.libraryOps {
		cancel := op.cancel
		var calls atomic.Int32
		op.cancel = func() {
			if calls.Add(1) == 1 {
				close(cancelEntered)
				<-releaseCancel.ch
			}
			cancel()
		}
	}
	s.mu.Unlock()
	if claims != 1 {
		t.Fatalf("playback-info owns %d claims, want one", claims)
	}
	drained := startTestDrain(t.Context(), s, drainLibraryA, 0)
	waitScratchSignal(t, cancelEntered) // Admission is closed, but the probe context is live.
	releaseProbe.open()
	if err := testResult(t, probeResult); !errors.Is(err, ErrSessionEvicted) {
		t.Fatalf("probe permit returned %v, want eviction", err)
	}
	if err := testResult(t, request); err != nil || grants.Load() != 0 {
		t.Fatalf("fenced decision returned %v and recorded %d grants", err, grants.Load())
	}
	requireScratchMissing(t, marker)
	releaseCancel.open()
	d := testResult(t, drained)
	if d.err != nil {
		t.Fatal(d.err)
	}
	if err := s.Reopen(drainLibraryA, d.fence); err != nil {
		t.Fatal(err)
	}
	resp, err := info()
	if err != nil || resp.Timeline.DurationSeconds != 120 || len(resp.Streams) == 0 || probeCalls.Load() != 2 {
		t.Fatalf("fresh decision returned duration %d, %d streams, %d probe calls, error %v",
			resp.Timeline.DurationSeconds, len(resp.Streams), probeCalls.Load(), err)
	}
}

func TestT4DrainJoinsOnlyTargetEncryptedRevocationPoll(t *testing.T) {
	s := newTestSessions(t, &fakeEncryptedTranscoder{}, t.TempDir(), nil)
	s.revocationPoll = time.Millisecond
	entered, release := scratchLatch(t)
	defer release.open()
	source := testEncryptedSource(nil)
	source.Revoked = func() bool {
		close(entered)
		<-release.ch
		return true
	}
	key := SessionKey{SessionID: "encrypted drain"}
	if _, err := encryptedFilePath(s, key, source, 120, -1, false, masterPlaylistName); err != nil {
		t.Fatal(err)
	}
	waitScratchSignal(t, entered)
	library := store.LibraryID(source.LibraryID)
	s.mu.Lock()
	session := s.sessions[key]
	s.mu.Unlock()
	drained := startTestDrain(t.Context(), s, library, 0)

	// Session teardown can retire while the key-store callback is blocked.
	// Verify the poll itself still owns work at that point.
	for {
		s.mu.Lock()
		retired := session.state == sessionRetired
		busy, _ := s.libraryWorkLocked(library, nil)
		if s.libraryChanged == nil && !retired {
			s.libraryChanged = make(chan struct{})
		}
		changed := s.libraryChanged
		s.mu.Unlock()
		if retired {
			if !busy {
				t.Fatal("encrypted revocation poll lost ownership after session retirement")
			}
			break
		}
		waitScratchSignal(t, changed)
	}
	assertDrainWaiting(t, drained)
	const other store.LibraryID = "unrelated library"
	if _, err := drainFile(t.Context(), s, other, SessionKey{SessionID: "unrelated"}, fakeSourcePath, false, masterPlaylistName); err != nil {
		t.Fatal(err)
	}
	if d := testResult(t, startTestDrain(t.Context(), s, other, 0)); d.err != nil {
		t.Fatal(d.err)
	}
	assertDrainWaiting(t, drained)
	release.open()
	if d := testResult(t, drained); d.err != nil {
		t.Fatal(d.err)
	}
}

type drainKeyframeProbe struct {
	keyframeFakeTranscoder
	probe func(context.Context, string) (ffmpeg.KeyframeIndex, error)
}

func (f *drainKeyframeProbe) ProbeKeyframes(ctx context.Context, path string) (ffmpeg.KeyframeIndex, error) {
	return f.probe(ctx, path)
}

func TestT4TimedOutKeyframeLoadsBelongToLibrary(t *testing.T) {
	for _, sameSource := range []bool{false, true} {
		t.Run(fmt.Sprintf("same source %t", sameSource), func(t *testing.T) {
			f := &drainKeyframeProbe{}
			s := newTestSessions(t, f, t.TempDir(), nil)
			s.keyframes.firstPlayWait = 25 * time.Millisecond
			aEntered, aRelease := scratchLatch(t)
			bEntered, bRelease := scratchLatch(t)
			defer aRelease.open()
			defer bRelease.open()
			aCancelled, bCancelled := make(chan struct{}), make(chan struct{})
			aSrc := filepath.Join(t.TempDir(), "A")
			seedScratchFile(t, aSrc)
			bSrc := aSrc
			if !sameSource {
				bSrc = filepath.Join(t.TempDir(), "B")
				seedScratchFile(t, bSrc)
			}
			var calls atomic.Int32
			f.probe = func(ctx context.Context, _ string) (ffmpeg.KeyframeIndex, error) {
				entered, cancelled, release := aEntered, aCancelled, aRelease
				if calls.Add(1) == 2 {
					entered, cancelled, release = bEntered, bCancelled, bRelease
				}
				close(entered)
				select {
				case <-ctx.Done():
					close(cancelled)
					<-release.ch
					return ffmpeg.KeyframeIndex{}, ctx.Err()
				case <-release.ch:
					return ffmpeg.KeyframeIndex{Keyframes: []time.Duration{0, 4 * time.Second}, Duration: 120 * time.Second}, nil
				}
			}
			request := func(library, path string) <-chan error {
				done := make(chan error, 1)
				go func() {
					_, err := drainFile(t.Context(), s, store.LibraryID(library), SessionKey{SessionID: store.SessionID(library)}, path, true, masterPlaylistName)
					done <- err
				}()
				return done
			}
			a := request("A", aSrc)
			waitScratchSignal(t, aEntered)
			b := request("B", bSrc)
			waitScratchSignal(t, bEntered)
			for _, result := range []<-chan error{a, b} {
				if err := waitScratchResult(t, result); err != nil {
					t.Fatalf("keyframe timeout did not fall back to a playlist: %v", err)
				}
			}
			drained := startTestDrain(t.Context(), s, "A", 0)
			waitScratchSignal(t, aCancelled)
			assertDrainWaiting(t, drained)
			aRelease.open()
			if result := testResult(t, drained); result.err != nil {
				t.Fatal(result.err)
			}
			select {
			case <-bCancelled:
				t.Fatal("A drain canceled B's load")
			default:
			}
			s.mu.Lock()
			var bPending bool
			for key := range s.keyframes.inflight {
				bPending = bPending || key.library == "B"
			}
			s.mu.Unlock()
			if !bPending {
				t.Fatal("B's load did not remain in flight")
			}
		})
	}
}

func TestT4DrainChildlessArtifact(t *testing.T) {
	for _, mode := range []string{"watch", "poll"} {
		t.Run(mode, func(t *testing.T) {
			f := &childlessArtifactTranscoder{started: make(chan struct{})}
			s := newTestSessions(t, f, t.TempDir(), nil)
			key := SessionKey{SessionID: "childless drain"}
			path, err := drainFile(t.Context(), s, "A", key, fakeSourcePath, false, masterPlaylistName)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "poll" {
				if err := os.RemoveAll(filepath.Join(filepath.Dir(path), "720p")); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() {
				_, err := drainFile(t.Context(), s, "A", key, fakeSourcePath, false, "720p/init.mp4")
				done <- err
			}()
			waitScratchSignal(t, f.started)
			drained := testResult(t, startTestDrain(t.Context(), s, "A", 0))
			if drained.err != nil {
				t.Fatal(drained.err)
			}
			if err := waitScratchResult(t, done); !errors.Is(err, ErrSessionEvicted) || t.Context().Err() != nil {
				t.Fatalf("childless request = %v", err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("drained directory still exists: %v", err)
			}
		})
	}
}

func TestT4DrainIgnoresUnrelatedScratchWork(t *testing.T) {
	for _, stage := range []string{"B setup", "B removal", "root sweep"} {
		t.Run(stage, func(t *testing.T) {
			s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
			entered, release := scratchLatch(t)
			defer release.open()
			aKey, bKey := SessionKey{SessionID: "A"}, SessionKey{SessionID: "B"}
			if _, err := drainFile(t.Context(), s, "A", aKey, fakeSourcePath, false, masterPlaylistName); err != nil {
				t.Fatal(err)
			}
			bDone := make(chan error, 1)
			switch stage {
			case "B setup":
				s.writeSetupPlaylist = func(h *hlsSession, name string, data []byte) error {
					if h.key == bKey && name == masterPlaylistName {
						close(entered)
						<-release.ch
					}
					return h.writePlaylist(name, data)
				}
				go func() {
					_, err := drainFile(t.Context(), s, "B", bKey, fakeSourcePath, false, masterPlaylistName)
					bDone <- err
				}()
			case "B removal":
				path, err := drainFile(t.Context(), s, "B", bKey, fakeSourcePath, false, masterPlaylistName)
				if err != nil {
					t.Fatal(err)
				}
				bDir := filepath.Dir(path)
				s.removeDir = func(path string) error {
					if path == bDir {
						close(entered)
						<-release.ch
					}
					return os.RemoveAll(path) // #nosec G703 -- fixture uses its locked scratch root.
				}
				go func() {
					_, err := s.Drain(t.Context(), "B", 0)
					bDone <- err
				}()
			case "root sweep":
				s.readScratch = func(root fs.FS, path string) ([]fs.DirEntry, error) {
					close(entered)
					<-release.ch
					return fs.ReadDir(root, path)
				}
				go func() { bDone <- s.PurgeOrphanedTranscodeDirs() }()
			}
			waitScratchSignal(t, entered)
			drained := testResult(t, startTestDrain(t.Context(), s, "A", 0))
			if drained.err != nil {
				t.Fatal(drained.err)
			}
			release.open()
			if err := waitScratchResult(t, bDone); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestT4DrainIgnoresUnrelatedIdleReap(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{holdChild: true}, t.TempDir(), nil)
	for _, library := range []store.LibraryID{"A", "B"} {
		key := SessionKey{SessionID: store.SessionID(library)}
		if _, err := drainFile(t.Context(), s, library, key, fakeSourcePath, false, "720p/seg00000.m4s"); err != nil {
			t.Fatal(err)
		}
	}
	a := registeredSession(t, s, SessionKey{SessionID: "A"})
	b := registeredSession(t, s, SessionKey{SessionID: "B"})
	s.mu.Lock()
	aRun, bRun := a.run, b.run
	a.lastAccess, b.lastAccess = time.Time{}, time.Time{}
	s.mu.Unlock()
	if aRun == nil || bRun == nil || aRun.proc == nil || bRun.proc == nil {
		t.Fatal("both idle sessions must have a child to reap")
	}
	entered, release := scratchLatch(t)
	s.stopEncoder = func(proc *ffmpeg.HLSProcess) {
		if proc == bRun.proc {
			close(entered)
			<-release.ch
		}
		proc.Stop()
	}
	idlePass := make(chan struct{})
	go func() {
		s.reap()
		close(idlePass)
	}()
	defer func() {
		release.open()
		waitScratchSignal(t, idlePass)
	}()
	waitScratchSignal(t, entered)
	drained := testResult(t, startTestDrain(t.Context(), s, "A", 0))
	if drained.err != nil {
		t.Fatal(drained.err)
	}
	select {
	case <-aRun.reaped:
	default:
		t.Fatal("library A drained before its child was reaped")
	}
	select {
	case <-bRun.reaped:
		t.Fatal("library B's reap finished with its stop still blocked")
	default:
	}
	if _, err := os.Stat(a.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("drained library A directory still exists: %v", err)
	}
}

func TestT4DrainScratchDebtAndFenceRetry(t *testing.T) {
	for _, failure := range []string{"root collision", "owned removal", "canceled wait"} {
		t.Run(failure, func(t *testing.T) {
			s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
			collision := filepath.Join(s.dir, fmt.Sprintf("session-%s-1", s.bootNonce))
			if failure == "root collision" {
				if err := os.Mkdir(collision, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			key := SessionKey{SessionID: "fence retry"}
			path, err := drainFile(t.Context(), s, "A", key, fakeSourcePath, false, masterPlaylistName)
			if err != nil {
				t.Fatal(err)
			}
			ownedDir := filepath.Dir(path)
			target := ownedDir
			if failure == "root collision" {
				target = collision
			}
			entered, release := scratchLatch(t)
			defer release.open()
			var removeCalls atomic.Int32
			s.removeDir = func(path string) error {
				if path == target && removeCalls.Add(1) == 1 {
					if failure == "canceled wait" {
						close(entered)
						<-release.ch
					} else {
						return fs.ErrPermission
					}
				}
				return os.RemoveAll(path) // #nosec G703 -- fixture uses its locked scratch root.
			}
			if failure == "root collision" {
				if err := s.PurgeOrphanedTranscodeDirs(); !errors.Is(err, fs.ErrPermission) {
					t.Fatalf("sweep did not report root-only collision: %v", err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first := startTestDrain(ctx, s, "A", 0)
			if failure == "canceled wait" {
				waitScratchSignal(t, entered)
				cancel()
			}
			d := testResult(t, first)
			switch failure {
			case "root collision":
				if d.err != nil || removeCalls.Load() != 1 {
					t.Fatalf("A drain retried or inherited root debt: %+v, removals = %d", d, removeCalls.Load())
				}
			case "owned removal", "canceled wait":
				want := error(fs.ErrPermission)
				if failure == "canceled wait" {
					want = context.Canceled
				}
				if !errors.Is(d.err, want) {
					t.Fatalf("first drain = %v, want %v", d.err, want)
				}
				if err := s.Reopen("A", d.fence); !errors.Is(err, ErrDrainFence) {
					t.Fatalf("reopened a failed drain: %v", err)
				}
				if fence, err := s.Drain(t.Context(), "A", 0); fence != d.fence || !errors.Is(err, ErrDrainFence) {
					t.Fatalf("zero retry = %d, %v", fence, err)
				}
				if _, err := drainFile(t.Context(), s, "A", key, fakeSourcePath, false, masterPlaylistName); !errors.Is(err, ErrSessionEvicted) {
					t.Fatalf("failed drain left admission open: %v", err)
				}
				retry := startTestDrain(t.Context(), s, "A", d.fence)
				release.open()
				if r := testResult(t, retry); r.err != nil || r.fence != d.fence {
					t.Fatalf("same-fence retry: %+v", r)
				}
			}
			retryRelease := make(chan struct{})
			delayed := make(chan drainResult, 1)
			go func() {
				<-retryRelease
				fence, err := s.Drain(t.Context(), "A", d.fence)
				delayed <- drainResult{fence, err}
			}()
			releaseRetry := sync.OnceFunc(func() { close(retryRelease) })
			defer releaseRetry()
			if err := s.Reopen("A", d.fence); err != nil {
				t.Fatal(err)
			}
			fresh, err := drainFile(t.Context(), s, "A", key, fakeSourcePath, false, masterPlaylistName)
			if err != nil || fresh == path {
				t.Fatalf("fresh setup = %q, %v", fresh, err)
			}
			releaseRetry()
			if r := testResult(t, delayed); !errors.Is(r.err, ErrDrainFence) {
				t.Fatalf("delayed retry = %+v", r)
			}
			if current, err := drainFile(t.Context(), s, "A", key, fakeSourcePath, false, masterPlaylistName); err != nil || current != fresh {
				t.Fatalf("stale fence disturbed fresh playback: %q, %v", current, err)
			}
		})
	}
}

func TestT4DrainOrdersEOFStoreHandoff(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(fmt.Sprintf("EOF claim first %t", prior), func(t *testing.T) {
			dataDir := storetest.MigratedDataDir(t)
			st := storetest.OpenAt(t, dataDir)
			id, source := importDrainMedia(t, st, drainLibraryA, store.MediaClassVideo, 120)
			s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
			entered, release := scratchLatch(t)
			defer release.open()
			if prior {
				s.sourceStore = st
			} else {
				s.sourceStore = &orderedSourceEndStore{
					sourceEndObserver: st, firstCallback: entered, release: release.ch,
					firstReturned: make(chan struct{}),
				}
			}
			key := SessionKey{SessionID: "EOF drain", MediaID: id}
			artifact, err := s.LiveArtifactShaped(t.Context(), key, source, 120, 1280, 720, false, false, ladderCaps{},
				VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{LibraryID: drainLibraryA}, PriorityNormal, masterPlaylistName)
			if err != nil {
				t.Fatal(err)
			}
			h := artifact.session
			var reads atomic.Int32
			observed := make(chan error, 1)
			go func() {
				defer artifact.Release()
				_, _, err := s.observeIncompleteSource(t.Context(), key, h, artifactObservation{version: artifact.version}, false, func() (time.Duration, bool) {
					reads.Add(1)
					if prior {
						close(entered)
						<-release.ch
					}
					return 20 * time.Second, true
				})
				observed <- err
			}()
			waitScratchSignal(t, entered)
			d := startTestDrain(t.Context(), s, drainLibraryA, 0)
			waitScratchSignal(t, h.cutoffCtx.Done())
			assertDrainWaiting(t, d)
			release.open()
			if err := waitScratchResult(t, observed); !errors.Is(err, ErrSessionEvicted) {
				t.Fatalf("old EOF caller returned %v", err)
			}
			if result := testResult(t, d); result.err != nil {
				t.Fatal(result.err)
			}
			file, err := st.PrimaryMediaFile(t.Context(), id, store.LibraryFilter{AllLibraries: true})
			if err != nil {
				t.Fatal(err)
			}
			incomplete, err := st.MediaFileSourceIncomplete(t.Context(), file)
			if err != nil || incomplete != prior || (reads.Load() == 1) != prior {
				t.Fatalf("EOF inspection reads = %d, incomplete = %t, err = %v", reads.Load(), incomplete, err)
			}
			if prior {
				if end := storetest.RecordedSourceEnd(t, dataDir, file.ContentFingerprint, h.sourceGeneration); end != 20 {
					t.Fatalf("staged source end = %v, want 20", end)
				}
			}
		})
	}
}

type stagedGrantAssets struct {
	fakeAssetsLocator
	beforeAssets func()
}

func (a stagedGrantAssets) TrickplayAsset(fingerprint string) (string, bool) {
	a.beforeAssets()
	return a.fakeAssetsLocator.TrickplayAsset(fingerprint)
}

func TestT4DrainOrdersGrantBatch(t *testing.T) {
	for _, commitFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("grant commit first %t", commitFirst), func(t *testing.T) {
			svc, s, scope, _ := drainTestService(t, &fakeTranscoder{})
			a, _ := importDrainMedia(t, svc.store, drainLibraryA, store.MediaClassVideo, 120)
			witness := bindDrainLibrary(t, s, drainLibraryA, SessionKey{SessionID: "grant fence witness"})
			entered, release := scratchLatch(t)
			defer release.open()
			latch := sync.OnceFunc(func() {
				close(entered)
				<-release.ch
			})
			var grants atomic.Int32
			if !commitFirst {
				// assetsInfo signs its grant before asking the locator. The
				// parked decision has already staged a token privately.
				svc.SetAssets(stagedGrantAssets{beforeAssets: latch})
			}
			svc.tokens.SetRecorder(drainRecorder(func(id store.MediaID) {
				if id == a {
					if commitFirst {
						latch()
					}
					grants.Add(1)
				}
			}))
			request := make(chan error, 1)
			go func() {
				res, err := svc.Info(t.Context(), scope, InfoRequest{
					MediaID: string(a), CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
				})
				if err == nil && (len(res.Streams) != 0) != commitFirst {
					err = fmt.Errorf("decision %s published %d streams", res.Decision, len(res.Streams))
				}
				request <- err
			}()
			waitScratchSignal(t, entered)
			if grants.Load() != 0 {
				t.Fatalf("recorded %d grants before releasing the latch", grants.Load())
			}
			drained := startTestDrain(t.Context(), s, drainLibraryA, 0)
			waitScratchSignal(t, witness.session.cutoffCtx.Done())
			assertDrainWaiting(t, drained)
			release.open()
			if err := waitScratchResult(t, request); err != nil {
				t.Fatal(err)
			}
			if d := testResult(t, drained); d.err != nil {
				t.Fatal(d.err)
			}
			want := int32(0)
			if commitFirst {
				want = 2 // direct and manual HLS
			}
			if got := grants.Load(); got != want {
				t.Fatalf("recorded %d grants, want %d", got, want)
			}
		})
	}
}
