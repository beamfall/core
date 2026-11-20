//go:build unix

package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

func TestT3FinalizedSegmentServesBeforeChildReap(t *testing.T) {
	transcoder := &scriptedTranscoder{start: func(_ int, dir string, _ int) []string {
		writeArtifact(t, dir, "seg00000.m4s", mediaSegment())
		return []string{"sleep", "60"}
	}}
	s := newTestSessions(t, transcoder, t.TempDir(), nil)
	key := SessionKey{SessionID: "finalized", MediaID: "media"}
	path, err := filePath720(s, key, "720p/seg00000.m4s")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, path)); got != string(mediaSegment()) {
		t.Fatal("first segment did not serve its finalized bytes")
	}
	h := registeredSession(t, s, key)
	select {
	case <-h.run.reaped:
		t.Fatal("segment did not serve while the child was running")
	default:
	}
	if n := s.TerminateSession(key.SessionID); n != 1 {
		t.Fatalf("terminated %d instances", n)
	}
	select {
	case <-h.run.reaped:
	default:
		t.Fatal("termination did not join reap")
	}
	if len(chargedKeys(s)) != 0 {
		t.Fatal("termination retained a charge after reap")
	}
}

type blockedStartTranscoder struct {
	fakeTranscoder
	entered     chan struct{}
	returnChild <-chan struct{}
	child       *ffmpeg.HLSProcess
}

func (f *blockedStartTranscoder) StartHLSSegments(ctx context.Context, _, _ string, _, _ int, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	child, err := ffmpeg.StartHLSCommand(exec.CommandContext(context.WithoutCancel(ctx), "sleep", "60"))
	if err != nil {
		return nil, err
	}
	f.child = child
	close(f.entered)
	<-f.returnChild
	return child, nil
}

func TestT3StartWaiterLeavesBeforeUnhandedChildReturns(t *testing.T) {
	for _, cutoff := range []bool{false, true} {
		name := "cancel request"
		if cutoff {
			name = "cut off instance"
		}
		t.Run(name, func(t *testing.T) {
			entered, release := scratchLatch(t)
			f := &blockedStartTranscoder{entered: entered, returnChild: release.ch}
			s := newTestSessions(t, f, t.TempDir(), nil)
			defer release.open()
			key := SessionKey{SessionID: "starter"}
			started := make(chan error, 1)
			go func() {
				_, err := filePath720(s, key, "720p/seg00000.m4s")
				started <- err
			}()
			waitScratchSignal(t, entered)
			h := registeredSession(t, s, key)
			if _, err := filePath720(s, key, masterPlaylistName); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			waiter := make(chan error, 1)
			go func() {
				_, err := s.FilePath(ctx, key, "/src/movie.mkv", 120, 1280, 720, false, "720p/seg00001.m4s")
				waiter <- err
			}()
			// Claim setup directly to observe the waiter holding a reference
			// before steering it. This check is synchronized by the manager.
			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for {
				s.mu.Lock()
				held := h.scratch.users == 2
				s.mu.Unlock()
				if held {
					break
				}
				select {
				case <-tick.C:
				case <-deadline.C:
					t.Fatal("waiter never acquired its directory reference")
				}
			}

			want := context.Canceled
			if cutoff {
				want = ErrSessionEvicted
				if n := s.TerminateSession(key.SessionID); n != 1 {
					t.Fatalf("terminated %d instances", n)
				}
			} else {
				cancel()
			}
			if err := waitScratchResult(t, waiter); !errors.Is(err, want) {
				t.Fatalf("waiter error = %v, want %v", err, want)
			}
			s.mu.Lock()
			users, charges := h.scratch.users, s.liveCount()
			s.mu.Unlock()
			if users != 1 || charges != 1 {
				t.Fatalf("users and capacity before handoff = %d, %d", users, charges)
			}
			if !cutoff {
				s.TerminateSession(key.SessionID)
			}
			release.open()
			if err := waitScratchResult(t, started); !errors.Is(err, ErrSessionEvicted) {
				t.Fatalf("late starter = %v", err)
			}
			if f.child.Wait() == nil || len(chargedKeys(s)) != 0 {
				t.Fatal("late child did not stop and release capacity")
			}
		})
	}
}

func TestT3ChildlessArtifactWaitObservesCutoff(t *testing.T) {
	for _, poll := range []bool{false, true} {
		name := "watch"
		if poll {
			name = "poll"
		}
		t.Run(name, func(t *testing.T) {
			cutoff, seen := make(chan struct{}), make(chan struct{}, 1)
			wait := waitForArtifact
			if poll {
				wait = pollForArtifact
			}
			done := make(chan error, 1)
			go func() {
				done <- wait(t.Context(), filepath.Join(t.TempDir(), "missing"), nil, cutoff, func(string) bool {
					select {
					case seen <- struct{}{}:
					default:
					}
					return false
				})
			}()
			waitScratchSignal(t, seen)
			close(cutoff)
			if err := waitScratchResult(t, done); !errors.Is(err, ErrSessionEvicted) || t.Context().Err() != nil {
				t.Fatalf("live-request cutoff: %v", err)
			}
		})
	}
}

func TestT3DurationFallbackAppliesTempoOnce(t *testing.T) {
	prober := &probingTranscoder{duration: 100}
	s := newTestSessions(t, prober, t.TempDir(), nil)
	src := filepath.Join(t.TempDir(), "audio")
	seedScratchFile(t, src)
	key := SessionKey{SessionID: "tempo", MediaID: "audio"}
	shape := StreamShape{AudioOnly: true, Adjust: ffmpeg.AudioAdjust{Tempo: 1.25}}
	request := func() *LiveArtifact {
		b, err := s.BindLive(key)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Release()
		if err := b.Attach("library", "", nil); err != nil {
			t.Fatal(err)
		}
		seconds := b.ProbedDurationSeconds(t.Context(), "fingerprint", src)
		a, err := b.ArtifactShaped(t.Context(), src, seconds, 0, 0, false, false, ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, shape, PriorityNormal, "audio/index.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	first := request()
	defer first.Release()
	s.mu.Lock()
	delete(s.durationProbes, src)
	s.mu.Unlock()
	prober.probeMu.Lock()
	prober.duration = 0
	prober.probeMu.Unlock()
	next := request()
	defer next.Release()
	if next.session != first.session || next.session.sourceDurationSeconds != 100 || next.session.durationSeconds != 80 ||
		!strings.Contains(string(mustRead(t, next.path)), "#EXT-X-ENDLIST") {
		t.Fatal("cache miss reshaped the adjusted VOD")
	}
}

func TestT3BoundDurationRejectsReplacementBeforeSetup(t *testing.T) {
	prober := &probingTranscoder{duration: 120}
	s := newTestSessions(t, prober, t.TempDir(), nil)
	src := filepath.Join(t.TempDir(), "source")
	seedScratchFile(t, src)
	b, err := s.BindLive(SessionKey{SessionID: "duration"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Release()
	if err := b.Attach("library", "profile", nil); err != nil {
		t.Fatal(err)
	}
	seconds := b.ProbedDurationSeconds(t.Context(), "fp", src)
	next := filepath.Join(filepath.Dir(src), "next")
	seedScratchFile(t, next)
	if err := os.Rename(next, src); err != nil {
		t.Fatal(err)
	}
	_, err = b.ArtifactShaped(t.Context(), src, seconds, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, masterPlaylistName)
	if !errors.Is(err, errSetupUnavailable) {
		t.Fatalf("stale duration setup = %v", err)
	}
	if entries, err := fs.ReadDir(os.DirFS(s.dir), "."); err != nil || len(entries) != 0 {
		t.Fatalf("stale duration wrote artifacts: %v, %v", entries, err)
	}
}

func TestT3ByteAdmissionSharesVerifiedProfileCap(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{holdChild: true}, t.TempDir(), nil)
	src := filepath.Join(t.TempDir(), "source")
	seedScratchFile(t, src)
	s.SetTranscodeCap(3)
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	_, release := scratchLatch(t)
	defer release.open()
	load := func(_ context.Context, profile store.ProfileID) (store.Profile, error) {
		if profile != "profile" {
			t.Errorf("profile lookup = %q", profile)
		}
		calls.Add(1)
		entered <- struct{}{}
		<-release.ch
		limit := 1
		return store.Profile{MaxConcurrentStreams: &limit}, nil
	}
	keys := []SessionKey{{SessionID: "one", ProfileCapability: "standard"}, {SessionID: "two", ProfileCapability: "standard"}}
	request := func(key SessionKey, name string) error {
		b, err := s.BindLive(key)
		if err != nil {
			return err
		}
		defer b.Release()
		if err := b.Attach("library", "profile", load); err != nil {
			return err
		}
		a, err := b.ArtifactShaped(t.Context(), src, 120, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, name)
		if err == nil {
			a.Release()
		}
		return err
	}
	for _, key := range keys {
		if err := request(key, masterPlaylistName); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 0 || len(chargedKeys(s)) != 0 {
		t.Fatal("playlist preparation used profile or server capacity")
	}
	done := make(chan error, 2)
	for _, key := range keys {
		go func() { done <- request(key, "720p/seg00000.m4s") }()
	}
	waitScratchSignal(t, entered)
	waitScratchSignal(t, entered)
	release.open()
	first, second := waitScratchResult(t, done), waitScratchResult(t, done)
	oneAdmitted := first == nil && errors.Is(second, ErrTranscodeCapacity)
	if !oneAdmitted && (second != nil || !errors.Is(first, ErrTranscodeCapacity)) {
		t.Fatalf("byte starts = %v, %v", first, second)
	}
	owners := chargedKeys(s)
	if len(owners) != 1 || s.activeProfileTranscodes("profile", owners[0]) != 0 {
		t.Fatalf("capacity owners = %v", owners)
	}
	otherCapability := owners[0]
	otherCapability.ProfileCapability = "different"
	if s.activeProfileTranscodes("profile", otherCapability) != 1 {
		t.Fatal("different capability incorrectly reused the charge")
	}
	if err := request(owners[0], "720p/seg00000.m4s"); err != nil || calls.Load() != 2 {
		t.Fatalf("cached segment = %v, lookups = %d", err, calls.Load())
	}
}

func TestT3OldSetupCannotAffectItsSuccessor(t *testing.T) {
	for _, stage := range []string{"stat", "picture", "playlist"} {
		t.Run(stage, func(t *testing.T) {
			transcoder := &fakeTranscoder{holdChild: true}
			s := newTestSessions(t, transcoder, t.TempDir(), nil)
			src := filepath.Join(t.TempDir(), "same-path")
			seedScratchFile(t, src)
			key := SessionKey{SessionID: "replace-during-setup", MediaID: "composed"}
			entered, release := scratchLatch(t)
			defer release.open()
			var stats, pictures, writes atomic.Int32
			s.statSource = func(path string) (os.FileInfo, error) {
				info, err := os.Stat(path) // #nosec G703 -- path belongs to the private test fixture.
				if stats.Add(1) == 1 && stage == "stat" {
					close(entered)
					<-release.ch
				}
				return info, err
			}
			s.SetPictureComposer(func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
				call := pictures.Add(1)
				if call == 1 && stage == "picture" {
					close(entered)
					<-release.ch
					return ComposedPicture{TimedText: "old text"}, true, nil
				}
				return ComposedPicture{TimedText: "new text"}, true, nil
			})
			s.beforeSetupCommit = func() {
				if writes.Add(1) == 1 && stage == "playlist" {
					close(entered)
					<-release.ch
				}
			}
			request := func(name string) (string, error) {
				return s.FilePathShaped(t.Context(), key, src, 120, 0, 0, false, false,
					ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{ComposedPicture: true}, PriorityNormal, name)
			}
			old := make(chan error, 1)
			go func() {
				_, err := request(ComposedRungID + "/seg00000.m4s")
				old <- err
			}()
			waitScratchSignal(t, entered)
			if got := s.TerminateSession(key.SessionID); got != 1 {
				t.Fatalf("terminated %d instances", got)
			}
			replacement := filepath.Join(filepath.Dir(src), "new")
			seedScratchFile(t, replacement)
			if err := os.Rename(replacement, src); err != nil {
				t.Fatal(err)
			}
			successor, err := request(ComposedRungID + "/seg00000.m4s")
			if err != nil {
				t.Fatal(err)
			}
			release.open()
			if err := waitScratchResult(t, old); !errors.Is(err, ErrSessionEvicted) {
				t.Fatalf("old setup error = %v", err)
			}
			if got := string(mustRead(t, filepath.Join(filepath.Dir(successor), composedTimedTextFile))); got != "new text" {
				t.Fatalf("successor picture = %q", got)
			}
			if got := transcoder.segmentStartsSnapshot(); len(got) != 1 || len(chargedKeys(s)) != 1 {
				t.Fatalf("encoder starts = %v, charge owners = %v", got, chargedKeys(s))
			}
		})
	}
}

func TestT3ReapTransfersCapacityToItsAssignedStart(t *testing.T) {
	for _, cancelTarget := range []bool{false, true} {
		name := "transfer"
		if cancelTarget {
			name = "cancel before transfer"
		}
		t.Run(name, func(t *testing.T) {
			s := newTestSessions(t, &fakeTranscoder{holdChild: true}, t.TempDir(), nil)
			s.SetTranscodeCap(1)
			slow := newSlowStop(s)
			defer slow.unblock()
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			victim := SessionKey{SessionID: "victim"}
			requester := SessionKey{SessionID: "requester"}
			third := SessionKey{SessionID: "third"}
			start := func(ctx context.Context, key SessionKey, priority Priority) (*LiveArtifact, error) {
				b, err := s.BindLive(key)
				if err != nil {
					return nil, err
				}
				defer b.Release()
				if err := b.Attach("library", store.ProfileID(key.SessionID), nil); err != nil {
					return nil, err
				}
				return b.ArtifactShaped(ctx, src, 120, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, priority, "720p/seg00000.m4s")
			}
			segment, err := start(t.Context(), victim, PriorityNormal)
			if err != nil {
				t.Fatal(err)
			}
			defer segment.Release()
			slow.armed.Store(true)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			target := make(chan error, 1)
			go func() {
				a, err := start(ctx, requester, PriorityForeground)
				if err == nil {
					a.Release()
				}
				target <- err
			}()
			slow.waitEntered(t)
			if got := chargedKeys(s); len(got) != 1 || got[0] != victim {
				t.Fatalf("charges before reap = %v", got)
			}
			if _, _, err := segment.OpenPlain(); !errors.Is(err, ErrSessionEvicted) {
				t.Fatalf("victim open before reap = %v", err)
			}
			if _, err := start(t.Context(), third, PriorityForeground); !errors.Is(err, ErrTranscodeCapacity) {
				t.Fatalf("third start before reap = %v", err)
			}
			oldRun := segment.session.run
			if cancelTarget {
				cancel()
				if err := waitScratchResult(t, target); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled target = %v", err)
				}
			}
			slow.unblock()
			waitScratchSignal(t, oldRun.reaped)
			want := requester
			if cancelTarget {
				want = third
				a, err := start(t.Context(), third, PriorityForeground)
				if err != nil {
					t.Fatal(err)
				}
				a.Release()
			} else if err := waitScratchResult(t, target); err != nil {
				t.Fatal(err)
			}
			if got := chargedKeys(s); len(got) != 1 || got[0] != want {
				t.Fatalf("charges after reap = %v, want %v", got, want)
			}
		})
	}
}

type finalUploadTranscoder struct {
	fakeTranscoder
	signal   *os.File
	endpoint chan ffmpeg.EncryptedLiveEndpoint
	next     chan struct{}
	calls    atomic.Int32
}

func (f *finalUploadTranscoder) StartEncryptedHLSSegments(ctx context.Context, ep ffmpeg.EncryptedLiveEndpoint, segment, _ int, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	if err := putLive(fmt.Sprintf("%s/seg%05d.m4s", ep.Output, segment), mediaSegment()); err != nil {
		return nil, err
	}
	if f.calls.Add(1) != 1 {
		close(f.next)
		return &ffmpeg.HLSProcess{}, nil
	}
	cmd := exec.CommandContext(ctx, "cat")
	cmd.Stdin, cmd.Stdout = f.signal, io.Discard
	child, err := ffmpeg.StartHLSCommand(cmd)
	if err == nil {
		f.endpoint <- ep
	}
	return child, err
}

func TestT3ReapFreesCapacityWhileFinalUploadOwnsWrites(t *testing.T) {
	childRead, childWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer childRead.Close()
	defer childWrite.Close()
	f := &finalUploadTranscoder{fakeTranscoder: fakeTranscoder{holdChild: true}, signal: childRead,
		endpoint: make(chan ffmpeg.EncryptedLiveEndpoint, 1), next: make(chan struct{})}
	s := newTestSessions(t, f, t.TempDir(), nil)
	s.SetTranscodeCap(1)
	encrypted := SessionKey{SessionID: "encrypted"}
	plain := SessionKey{SessionID: "plain"}
	secure := testEncryptedSource(nil)
	first, err := encryptedFilePath(s, encrypted, secure, 120, -1, false, "720p/seg00000.m4s")
	if err != nil {
		t.Fatal(err)
	}
	ep := <-f.endpoint
	h := registeredSession(t, s, encrypted)
	run := h.run
	body, writer := io.Pipe()
	defer body.Close()
	defer writer.Close()
	upload := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, ep.Output+"/seg00029.m4s", body)
		if err != nil {
			upload <- err
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				err = fmt.Errorf("final upload returned HTTP %d", resp.StatusCode)
			}
		}
		upload <- err
	}()
	payload := mediaSegment()
	if _, err := writer.Write(payload[:len(payload)/2]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	part := filepath.Join(filepath.Dir(first), "seg00029.m4s.part")
	if err := waitForArtifact(ctx, part, nil, nil, func(string) bool {
		_, err := os.Stat(part)
		return err == nil
	}); err != nil {
		t.Fatalf("upload never entered writer: %v", err)
	}
	_ = childWrite.Close()
	waitScratchSignal(t, run.reaped)
	select {
	case <-run.exited:
		t.Fatal("writer ownership ended before upload")
	default:
	}
	if _, err := filePath720(s, plain, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("independent start after reap = %v", err)
	}
	seek := make(chan error, 1)
	go func() {
		_, err := encryptedFilePath(s, encrypted, secure, 120, -1, false, "720p/seg00020.m4s")
		seek <- err
	}()
	select {
	case <-f.next:
		t.Fatal("seek started writing before final upload")
	default:
	}
	s.TerminateSession(plain.SessionID)
	if _, err := writer.Write(payload[len(payload)/2:]); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := waitScratchResult(t, upload); err != nil {
		t.Fatal(err)
	}
	if err := waitScratchResult(t, seek); err != nil {
		t.Fatal(err)
	}
	waitScratchSignal(t, f.next)
}

type orderedDurationTranscoder struct {
	fakeTranscoder
	path    string
	calls   atomic.Int32
	entered chan int
	results [2]chan int
}

func (f *orderedDurationTranscoder) Probe(_ context.Context, path string) (ffmpeg.Probe, error) {
	if path != f.path {
		return ffmpeg.Probe{DurationSeconds: 60}, nil
	}
	index := int(f.calls.Add(1)) - 1
	f.entered <- index
	return ffmpeg.Probe{DurationSeconds: <-f.results[index]}, nil
}

func TestT3DurationWinnerSurvivesEvictionBeforeShapeCommit(t *testing.T) {
	root := t.TempDir()
	src, other := filepath.Join(root, "source"), filepath.Join(root, "other")
	seedScratchFile(t, src)
	seedScratchFile(t, other)
	prober := &orderedDurationTranscoder{path: src, entered: make(chan int, 2),
		results: [2]chan int{make(chan int, 1), make(chan int, 1)}}
	s := newTestSessions(t, prober, t.TempDir(), nil)
	entered, release := scratchLatch(t)
	defer release.open()
	var prepared atomic.Bool
	s.beforeSetupCommit = func() {
		if prepared.CompareAndSwap(false, true) {
			close(entered)
			<-release.ch
		}
	}
	binding, err := s.BindLive(SessionKey{SessionID: "first-vod"})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release()
	if err := binding.Attach("library", "", nil); err != nil {
		t.Fatal(err)
	}
	defer close(prober.results[0])
	defer close(prober.results[1])
	hls := make(chan int, 1)
	go func() { hls <- binding.ProbedDurationSeconds(t.Context(), "source", src) }()
	select {
	case i := <-prober.entered:
		if i != 0 {
			t.Fatalf("HLS probe order = %d", i)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HLS probe never started")
	}
	info := make(chan int, 1)
	go func() { info <- s.ProbedDurationSeconds(t.Context(), "source", src) }()
	select {
	case i := <-prober.entered:
		if i != 1 {
			t.Fatalf("Info probe order = %d", i)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Info probe never started")
	}
	prober.results[0] <- 0
	if got := <-hls; got != 0 {
		t.Fatalf("HLS first result = %d", got)
	}
	outcome := make(chan error, 1)
	go func() {
		a, err := binding.ArtifactShaped(t.Context(), src, 0, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, "720p/index.m3u8")
		if err == nil {
			defer a.Release()
			data, readErr := os.ReadFile(a.path) // #nosec G304 -- artifact is inside the private test scratch directory.
			if readErr != nil || a.session.sourceDurationSeconds != 120 || !strings.Contains(string(data), "#EXT-X-ENDLIST") {
				err = errors.New("VOD did not use the winning source runtime")
			}
		}
		outcome <- err
	}()
	waitScratchSignal(t, entered)
	prober.results[1] <- 120
	if got := <-info; got != 120 {
		t.Fatalf("Info duration = %d", got)
	}
	s.mu.Lock()
	for i := 1; i < durationProbeCacheLimit; i++ {
		s.durationProbes[fmt.Sprintf("unused-%d", i)] = durationProbeEntry{generation: "fixture", seconds: 60}
	}
	s.mu.Unlock()
	if got := s.ProbedDurationSeconds(t.Context(), "another", other); got != 60 {
		t.Fatalf("evicting duration publication = %d", got)
	}
	s.mu.Lock()
	_, retainedInCache := s.durationProbes[src]
	s.mu.Unlock()
	if retainedInCache {
		t.Fatal("cache did not evict the winning result")
	}
	release.open()
	if err := waitScratchResult(t, outcome); err != nil {
		t.Fatal(err)
	}
}

type replacementGridTranscoder struct {
	keyframeFakeTranscoder
	holdFirst     <-chan struct{}
	probeFinished chan struct{}
}

func (f *replacementGridTranscoder) ProbeKeyframes(_ context.Context, path string) (ffmpeg.KeyframeIndex, error) {
	if f.probes.Add(1) == 1 && f.holdFirst != nil {
		defer close(f.probeFinished)
		<-f.holdFirst
	}
	body, err := os.ReadFile(path) // #nosec G304 -- path belongs to the scratch test fixture.
	if err != nil {
		return ffmpeg.KeyframeIndex{}, err
	}
	step := 5 * time.Second
	if string(body) == "new" {
		step = 6 * time.Second
	}
	return ffmpeg.KeyframeIndex{Keyframes: []time.Duration{0, step, 2 * step}, Duration: 15 * time.Second}, nil
}

func TestT3PinnedGridRequiresTheSameFileIdentity(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "completed grid"
		if pending {
			name = "timed out grid"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			src, replacement := filepath.Join(root, "source"), filepath.Join(root, "replacement")
			if err := os.WriteFile(src, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(src)
			if err != nil {
				t.Fatal(err)
			}
			f := &replacementGridTranscoder{}
			_, release := scratchLatch(t)
			if pending {
				f.holdFirst, f.probeFinished = release.ch, make(chan struct{})
			}
			s := newTestSessions(t, f, t.TempDir(), nil)
			defer release.open()
			if pending {
				s.keyframes.firstPlayWait = 35 * time.Millisecond
			}
			key := SessionKey{SessionID: "grid"}
			request := func() string {
				path, err := s.FilePath(t.Context(), key, src, 15, 1280, 720, true, audioCopyRungID+"/index.m3u8")
				if err != nil {
					t.Fatal(err)
				}
				return path
			}
			first := request()
			if again := request(); again != first || f.probes.Load() != 1 {
				t.Fatalf("unchanged source created a different playlist or probed: %d", f.probes.Load())
			}
			want := "#EXTINF:5.000,"
			if pending {
				want = "#EXTINF:10.000,"
			}
			if !strings.Contains(string(mustRead(t, first)), want) {
				t.Fatalf("first source did not use %s", want)
			}
			if err := os.WriteFile(replacement, []byte("new"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, src); err != nil {
				t.Fatal(err)
			}
			next := request()
			if next == first || f.probes.Load() != 2 || !strings.Contains(string(mustRead(t, next)), "#EXTINF:6.000,") {
				t.Fatalf("replacement retained the old grid, probes = %d", f.probes.Load())
			}
			if pending {
				release.open()
				waitScratchSignal(t, f.probeFinished)
			}
			if again := request(); again != next || f.probes.Load() != 2 {
				t.Fatalf("replacement generation did not reuse its grid: %d", f.probes.Load())
			}
		})
	}
}

func TestT3UnreadableOutputDoesNotPublishEOF(t *testing.T) {
	for _, stage := range []string{"rung scan", "segment read"} {
		t.Run(stage, func(t *testing.T) {
			input, signal, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer signal.Close()
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			f := &eofInspectionTranscoder{input: input}
			s := newTestSessions(t, f, t.TempDir(), nil)
			key := SessionKey{SessionID: "eof"}
			path, err := s.FilePath(t.Context(), key, src, 120, 1280, 720, false, "720p/init.mp4")
			if err != nil {
				t.Fatal(err)
			}
			h := registeredSession(t, s, key)
			dir := filepath.Dir(path)
			if stage == "rung scan" {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			} else {
				writeArtifact(t, dir, "seg00000.m4s", []byte("not a complete box"))
			}
			_ = signal.Close()
			waitScratchSignal(t, h.run.exited)
			s.mu.Lock()
			known := h.sourceEndKnown
			s.mu.Unlock()
			if known || s.incompleteSources.contains(src) {
				t.Fatalf("%s established an EOF bound", stage)
			}
		})
	}
}

type eofInspectionTranscoder struct {
	fakeTranscoder
	input *os.File
}

func (f *eofInspectionTranscoder) StartHLSSegments(ctx context.Context, _, dir string, _, _ int, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	if err := os.WriteFile(filepath.Join(dir, "init.mp4"), []byte("init"), 0o600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "cat")
	cmd.Stdin, cmd.Stdout = f.input, io.Discard
	return ffmpeg.StartHLSCommand(cmd)
}

func TestT3FailedSetupWaitersKeepTheExactResultDuringCleanup(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	src := filepath.Join(t.TempDir(), "source")
	seedScratchFile(t, src)
	firstDir := filepath.Join(s.dir, fmt.Sprintf("session-%s-1", s.bootNonce))
	s.mkdirDir = func(path string, mode fs.FileMode) error {
		if err := os.Mkdir(path, mode); err != nil {
			return err
		}
		if path == firstDir {
			return os.Mkdir(filepath.Join(path, "720p"), 0o700)
		}
		return nil
	}
	removing, release := scratchLatch(t)
	defer release.open()
	s.removeDir = func(path string) error {
		if path == firstDir {
			close(removing)
			<-release.ch
		}
		return s.removeScratchFixture(path)
	}
	key := SessionKey{SessionID: "failed-setup"}
	bind := func() *LiveBinding {
		b, err := s.BindLive(key)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.Release)
		if err := b.Attach("library", "", nil); err != nil {
			t.Fatal(err)
		}
		return b
	}
	request := func(binding *LiveBinding, seconds int) error {
		a, err := binding.ArtifactShaped(t.Context(), src, seconds, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, masterPlaylistName)
		if err == nil {
			a.Release()
		}
		return err
	}
	owner, match, mismatch := bind(), bind(), bind()
	first := make(chan error, 1)
	go func() { first <- request(owner, 120) }()
	waitScratchSignal(t, removing)
	if err := request(match, 120); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("matching waiter = %v, want failed setup result", err)
	}
	fresh := bind()
	if err := request(fresh, 60); err != nil {
		t.Fatalf("fresh setup queued on failed owner: %v", err)
	}
	if err := request(mismatch, 30); !errors.Is(err, errSetupUnavailable) {
		t.Fatalf("old mismatch = %v, want retryable failure", err)
	}
	if got := s.TerminateSession(key.SessionID); got != 2 {
		t.Fatalf("scope cutoff revoked %d admitted instances, want 2", got)
	}
	if err := request(match, 120); !errors.Is(err, ErrSessionEvicted) {
		t.Fatalf("old matching continuation after cutoff = %v", err)
	}
	release.open()
	if err := waitScratchResult(t, first); !errors.Is(err, ErrSessionEvicted) {
		t.Fatalf("failed owner lost to cutoff but returned %v", err)
	}
}

type endpointFailureTranscoder struct {
	fakeTranscoder
	calls atomic.Int32
}

func (f *endpointFailureTranscoder) StartEncryptedHLSSegments(ctx context.Context, ep ffmpeg.EncryptedLiveEndpoint, _, _ int, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	f.calls.Add(1)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Source, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			return nil, fmt.Errorf("invalid source method returned HTTP %d", resp.StatusCode)
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, errors.New("endpoint did not cancel preparation")
	}
}

func TestT3EndpointFailureDuringPreparationUsesRetryBudget(t *testing.T) {
	transcoder := &endpointFailureTranscoder{}
	s := newTestSessions(t, transcoder, t.TempDir(), nil)
	key := SessionKey{SessionID: "endpoint"}
	source := testEncryptedSource(nil)
	for attempt := 0; attempt < encoderMaxFailedRuns; attempt++ {
		_, err := encryptedFilePath(s, key, source, 120, -1, false, "720p/seg00000.m4s")
		if err == nil {
			t.Fatalf("attempt %d unexpectedly served", attempt)
		}
		if count := len(chargedKeys(s)); count != 0 {
			t.Fatalf("attempt %d retained %d capacity charges", attempt, count)
		}
	}
	_, err := encryptedFilePath(s, key, source, 120, -1, false, "720p/seg00000.m4s")
	if !errors.Is(err, ErrTranscodeFailed) || int(transcoder.calls.Load()) != encoderMaxFailedRuns {
		t.Fatalf("failure budget result = %v, preparation calls = %d", err, transcoder.calls.Load())
	}
}

func TestT3TerminationReapsSiblingDuringPlaylistWrite(t *testing.T) {
	transcoder := &fakeTranscoder{holdChild: true}
	s := newTestSessions(t, transcoder, t.TempDir(), nil)
	active := SessionKey{SessionID: "viewer", MediaID: "active"}
	preparing := SessionKey{SessionID: "viewer", MediaID: "setup"}
	if _, err := filePath720(s, active, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	run := registeredSession(t, s, active).run
	entered, release := scratchLatch(t)
	defer release.open()
	s.beforeSetupCommit = func() {
		close(entered)
		<-release.ch
	}
	request := make(chan error, 1)
	go func() {
		_, err := filePath720(s, preparing, masterPlaylistName)
		request <- err
	}()
	waitScratchSignal(t, entered)
	finishesWhileBlocked(t, "TerminateSession", func() error {
		if n := s.TerminateSession(active.SessionID); n != 2 {
			return fmt.Errorf("terminated %d instances", n)
		}
		select {
		case <-run.reaped:
			return nil
		default:
			return errors.New("termination left the active sibling unreaped")
		}
	})
	release.open()
	if err := waitScratchResult(t, request); !errors.Is(err, ErrSessionEvicted) {
		t.Fatalf("cut-off setup = %v", err)
	}
}

func TestT3ProfileLookupFailureRefusesOnlyNewStarts(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{holdChild: true}, t.TempDir(), nil)
	src := filepath.Join(t.TempDir(), "source")
	seedScratchFile(t, src)
	var calls atomic.Int32
	failure := errors.New("injected profile lookup error")
	load := func(context.Context, store.ProfileID) (store.Profile, error) {
		if calls.Add(1) > 1 {
			return store.Profile{}, failure
		}
		return store.Profile{}, nil
	}
	request := func(key SessionKey, name string) error {
		b, err := s.BindLive(key)
		if err != nil {
			return err
		}
		defer b.Release()
		if err := b.Attach("library", "profile", load); err != nil {
			return err
		}
		a, err := b.ArtifactShaped(t.Context(), src, 120, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, name)
		if err == nil {
			a.Release()
		}
		return err
	}
	key := SessionKey{SessionID: "existing"}
	const segment = "720p/seg00000.m4s"
	for _, name := range []string{segment, masterPlaylistName, segment} {
		if err := request(key, name); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("cached artifact fetched profile policy %d times", calls.Load())
	}
	if err := request(SessionKey{SessionID: "new"}, segment); !errors.Is(err, failure) {
		t.Fatalf("start with failed profile lookup = %v", err)
	}
	if n := len(chargedKeys(s)); n != 1 {
		t.Fatalf("failed profile lookup left %d charges", n)
	}
}

func TestT3HandoffRechecksBothCapsAtReap(t *testing.T) {
	for _, kind := range []string{"lowered server cap", "filled profile cap", "available"} {
		t.Run(kind, func(t *testing.T) {
			s := &Sessions{sessions: make(map[SessionKey]*hlsSession), cap: 2}
			victim := newSessionReservation(SessionKey{SessionID: "victim"})
			peer := newSessionReservation(SessionKey{SessionID: "peer"})
			target := newSessionReservation(SessionKey{SessionID: "target"})
			victim.state, peer.state, target.state = sessionRevoking, sessionRunning, sessionRunning
			target.profile = "target-profile"
			limit := 1
			start := &encoderStart{session: target, ctx: t.Context(), limit: &limit}
			target.start = start
			victimRun := &encoderRun{session: victim, reaped: make(chan struct{}), transfer: start}
			victim.run = victimRun
			s.mu.Lock()
			defer s.mu.Unlock()
			s.reserveEncoderLocked(victim)
			s.reserveEncoderLocked(peer)
			switch kind {
			case "lowered server cap":
				s.cap = 1
			case "filled profile cap":
				peer.profile = target.profile
			}
			s.childReapedLocked(victimRun)
			if victim.charged || target.charged != (kind == "available") {
				t.Fatalf("reap charge, victim %v, requester %v", victim.charged, target.charged)
			}
		})
	}
}
