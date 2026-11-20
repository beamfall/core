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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

func TestT3FinalizedSegmentServesBeforeChildReap(t *testing.T) {
	for _, delay := range []time.Duration{0, PostSeekTranscodeLatencyBudget + 200*time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			transcoder := &scriptedTranscoder{start: func(_ int, dir string, _ int) []string {
				// Publish two segments independently while the child runs.
				writeArtifact(t, dir, "private", mediaSegment())
				writeArtifact(t, dir, "later", mediaSegment())
				return []string{"sh", "-c", `sleep "$1"; mv "$2/private" "$2/seg00000.m4s"
while [ ! -f "$2/next" ]; do sleep 0.02; done
mv "$2/later" "$2/seg00001.m4s"; exec sleep 60`,
					"fixture", fmt.Sprintf("%.3f", delay.Seconds()), dir}
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
			writeArtifact(t, filepath.Dir(path), "next", []byte("publish"))
			if _, err := filePath720(s, key, "720p/seg00001.m4s"); err != nil || transcoder.startCount() != 1 {
				t.Fatalf("the same child could not serve a later segment: %v", err)
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
		})
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

type childlessArtifactTranscoder struct {
	fakeTranscoder
	started chan struct{}
}

func (f *childlessArtifactTranscoder) StartHLSSegments(context.Context, string, string, int, int, ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	close(f.started)
	return &ffmpeg.HLSProcess{}, nil
}

func TestT3ChildlessArtifactWaitObservesCutoff(t *testing.T) {
	for _, mode := range []string{"watch", "poll"} {
		t.Run(mode, func(t *testing.T) {
			transcoder := &childlessArtifactTranscoder{started: make(chan struct{})}
			s := newTestSessions(t, transcoder, t.TempDir(), nil)
			key := SessionKey{SessionID: "childless"}
			path, err := filePath720(s, key, masterPlaylistName)
			if err != nil {
				t.Fatal(err)
			}
			h := registeredSession(t, s, key)
			if mode == "poll" {
				// The watcher cannot arm on a missing parent, so the
				// request must fall back to polling its confined path.
				if err := os.RemoveAll(filepath.Join(filepath.Dir(path), "720p")); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() {
				_, err := s.FilePath(t.Context(), key, "/src/movie.mkv", 120, 1280, 720, false, "720p/init.mp4")
				done <- err
			}()
			waitScratchSignal(t, transcoder.started)
			select {
			case err := <-done:
				t.Fatalf("missing artifact did not wait: %v", err)
			case <-time.After(25 * time.Millisecond):
			}
			s.mu.Lock()
			refs, charges := h.scratch.users, s.liveCount()
			s.mu.Unlock()
			if refs != 1 || charges != 0 {
				t.Fatalf("childless artifact held %d references and %d charges", refs, charges)
			}
			if n := s.TerminateSession(key.SessionID); n != 1 {
				t.Fatalf("terminated %d instances", n)
			}
			if err := waitScratchResult(t, done); !errors.Is(err, ErrSessionEvicted) || t.Context().Err() != nil {
				t.Fatalf("live-request cutoff: %v", err)
			}
			requireScratchMissing(t, h.dir)
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
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache hit %v", cached), func(t *testing.T) {
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
			if cached && s.ProbedDurationSeconds(t.Context(), "fp", src) != 120 {
				t.Fatal("could not warm the duration cache")
			}
			seconds := b.ProbedDurationSeconds(t.Context(), "fp", src)
			if seconds != 120 || prober.probeCount() != 1 {
				t.Fatalf("bound duration = %d, probes = %d", seconds, prober.probeCount())
			}
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
		})
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
	for _, action := range []string{"transfer", "cancel before transfer", "revoke before transfer"} {
		t.Run(action, func(t *testing.T) {
			s := newTestSessions(t, &fakeTranscoder{holdChild: true}, t.TempDir(), nil)
			s.SetTranscodeCap(1)
			slow := newSlowStop(s)
			defer slow.unblock()
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			victim := SessionKey{SessionID: "victim"}
			requester := SessionKey{SessionID: "requester"}
			third := SessionKey{SessionID: "third"}
			service := &Service{sessions: s}
			limit := 1
			policy := store.Profile{TranscodeAllowed: true, MaxConcurrentStreams: &limit}
			streamLimited := func(key SessionKey) bool {
				scope := auth.Scope{SessionID: key.SessionID, ProfileID: store.ProfileID(key.SessionID)}
				return service.transcodeRefusal(t.Context(), scope, "different-media", CapabilityProfile{}, policy, PriorityNormal) == "stream-limit-reached"
			}
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
			if !streamLimited(victim) || streamLimited(requester) || streamLimited(third) {
				t.Fatal("pending transfer charged the wrong Profile before reap")
			}
			if _, _, err := segment.OpenPlain(); !errors.Is(err, ErrSessionEvicted) {
				t.Fatalf("victim open before reap = %v", err)
			}
			if _, err := start(t.Context(), third, PriorityForeground); !errors.Is(err, ErrTranscodeCapacity) {
				t.Fatalf("third start before reap = %v", err)
			}
			oldRun := segment.session.run
			switch action {
			case "cancel before transfer":
				cancel()
				if err := waitScratchResult(t, target); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled target = %v", err)
				}
			case "revoke before transfer":
				if n := s.TerminateSession(requester.SessionID); n != 1 {
					t.Fatalf("revoked %d target instances", n)
				}
				if err := waitScratchResult(t, target); !errors.Is(err, ErrSessionEvicted) {
					t.Fatalf("revoked target = %v", err)
				}
			}
			slow.unblock()
			waitScratchSignal(t, oldRun.reaped)
			want := requester
			if action != "transfer" {
				want = third
				a, err := start(t.Context(), third, PriorityForeground)
				if err != nil {
					t.Fatal(err)
				}
				a.Release()
			} else {
				if _, err := start(t.Context(), third, PriorityForeground); !errors.Is(err, ErrTranscodeCapacity) {
					t.Fatalf("third start across reap = %v", err)
				}
				if err := waitScratchResult(t, target); err != nil {
					t.Fatal(err)
				}
			}
			if got := chargedKeys(s); len(got) != 1 || got[0] != want {
				t.Fatalf("charges after reap = %v, want %v", got, want)
			}
			if streamLimited(victim) || !streamLimited(want) {
				t.Fatal("settled transfer charged the wrong Profile")
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
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("upload failure %v", failed), func(t *testing.T) { testFinalUploadReap(t, failed) })
	}
}

func testFinalUploadReap(t *testing.T, failed bool) {
	t.Helper()
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
	payload := mediaSegment()
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, ep.Output+"/seg00029.m4s", body)
		if err != nil {
			upload <- err
			return
		}
		req.ContentLength = int64(len(payload))
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				err = fmt.Errorf("final upload returned HTTP %d", resp.StatusCode)
			}
		}
		upload <- err
	}()
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
	if _, _, delivered := s.deliveredBandwidth(encrypted.SessionID, encrypted.MediaID); !delivered {
		t.Fatal("ordinary reap discarded the last delivered rung")
	}
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
	if !failed {
		if _, err := writer.Write(payload[len(payload)/2:]); err != nil {
			t.Fatal(err)
		}
	}
	_ = writer.Close()
	if err := waitScratchResult(t, upload); (err != nil) != failed {
		t.Fatalf("final upload = %v, want failure %v", err, failed)
	}
	waitScratchSignal(t, run.exited)
	want := exitEndOfInput
	if failed {
		want = exitCrashed
	}
	if run.outcome != want {
		t.Fatalf("reaped outcome = %v, want %v", run.outcome, want)
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
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprintf("running VOD %v", running), func(t *testing.T) { testDurationWinnerBeforeShapeCommit(t, running) })
	}
}

func testDurationWinnerBeforeShapeCommit(t *testing.T, running bool) {
	t.Helper()
	root := t.TempDir()
	src, other := filepath.Join(root, "source"), filepath.Join(root, "other")
	seedScratchFile(t, src)
	seedScratchFile(t, other)
	prober := &orderedDurationTranscoder{path: src, entered: make(chan int, 2),
		results: [2]chan int{make(chan int, 1), make(chan int, 1)}}
	s := newTestSessions(t, prober, t.TempDir(), nil)
	entered, release := scratchLatch(t)
	defer release.open()
	binding, err := s.BindLive(SessionKey{SessionID: "first-vod"})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release()
	if err := binding.Attach("library", "", nil); err != nil {
		t.Fatal(err)
	}
	playlist := func(seconds int) (*LiveArtifact, error) {
		return binding.ArtifactShaped(t.Context(), src, seconds, 1280, 720, false, false,
			ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, "720p/index.m3u8")
	}
	var existing *LiveArtifact
	if running {
		existing, err = playlist(120)
		if err != nil {
			t.Fatal(err)
		}
		defer existing.Release()
	} else {
		var prepared atomic.Bool
		s.beforeSetupCommit = func() {
			if prepared.CompareAndSwap(false, true) {
				close(entered)
				<-release.ch
			}
		}
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
	if running {
		var stats atomic.Int32
		s.statSource = func(path string) (os.FileInfo, error) {
			info, err := os.Stat(path) // #nosec G703 -- source belongs to the private fixture.
			if path == src && stats.Add(1) == 2 {
				close(entered)
				<-release.ch
			}
			return info, err
		}
	}
	outcome := make(chan error, 1)
	go func() {
		a, err := playlist(0)
		if err == nil {
			defer a.Release()
			data, readErr := os.ReadFile(a.path) // #nosec G304 -- artifact is inside the private test scratch directory.
			if readErr != nil || a.session.sourceDurationSeconds != 120 || !strings.Contains(string(data), "#EXT-X-ENDLIST") {
				err = errors.New("VOD did not use the winning source runtime")
			}
			if existing != nil && (existing.session != a.session || existing.path != a.path) {
				err = errors.New("running VOD was reshaped")
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
	for _, mismatch := range []string{"shape", "generation"} {
		for _, intervening := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s intervening caller %v", mismatch, intervening), func(t *testing.T) {
				testFailedSetupReplacement(t, mismatch, intervening)
			})
		}
	}
}

func testFailedSetupReplacement(t *testing.T, mismatchKind string, intervening bool) {
	t.Helper()
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
	seconds := 30
	if mismatchKind == "generation" {
		seconds = 120
		replacement := filepath.Join(filepath.Dir(src), "replacement")
		seedScratchFile(t, replacement)
		if err := os.Rename(replacement, src); err != nil {
			t.Fatal(err)
		}
	}
	if intervening {
		fresh := bind()
		if err := request(fresh, seconds); err != nil {
			t.Fatalf("fresh setup queued on failed owner: %v", err)
		}
		if err := request(mismatch, seconds); !errors.Is(err, errSetupUnavailable) {
			t.Fatalf("old mismatch = %v, want retryable failure", err)
		}
	} else {
		if err := request(mismatch, seconds); err != nil {
			t.Fatalf("verified mismatch failed to replace its exact failed head: %v", err)
		}
		if mismatch.session == owner.session {
			t.Fatal("mismatch reused the failed instance")
		}
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

func TestT3SetupCannotRepublishSupersededSourceGeneration(t *testing.T) {
	for _, retireNew := range []bool{false, true} {
		t.Run(fmt.Sprintf("retire newer session %v", retireNew), func(t *testing.T) {
			f := &fakeTranscoder{holdChild: true}
			s := newTestSessions(t, f, t.TempDir(), nil)
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			entered, release := scratchLatch(t)
			defer release.open()
			var calls atomic.Int32
			s.statSource = func(path string) (os.FileInfo, error) {
				info, err := os.Stat(path) // #nosec G703 -- path belongs to the private test fixture.
				if calls.Add(1) == 3 {     // initial capture, pre-setup recheck, promotion recheck
					close(entered)
					<-release.ch
				}
				return info, err
			}
			old, newer := SessionKey{SessionID: "old"}, SessionKey{SessionID: "new"}
			fetch := func(key SessionKey, name string) (string, error) {
				return s.FilePath(t.Context(), key, src, 120, 1280, 720, false, name)
			}
			oldResult := make(chan error, 1)
			go func() {
				_, err := fetch(old, "720p/seg00000.m4s")
				oldResult <- err
			}()
			waitScratchSignal(t, entered)
			replacement := filepath.Join(filepath.Dir(src), "replacement")
			seedScratchFile(t, replacement)
			if err := os.Rename(replacement, src); err != nil {
				t.Fatal(err)
			}
			path, err := fetch(newer, "720p/seg00000.m4s")
			if err != nil {
				t.Fatal(err)
			}
			if retireNew {
				s.TerminateSession(newer.SessionID)
			}
			release.open()
			if err := waitScratchResult(t, oldResult); !errors.Is(err, errSetupUnavailable) {
				t.Fatalf("superseded promotion = %v", err)
			}
			if got, err := fetch(newer, "720p/seg00000.m4s"); err != nil || (got == path) == retireNew {
				t.Fatalf("new generation segment = %q, %v", got, err)
			}
			starts := 1
			if retireNew {
				starts++
			}
			if got := len(f.segmentStartsSnapshot()); got != starts {
				t.Fatalf("encoder starts = %d, want %d", got, starts)
			}
		})
	}
}

func TestT3SuccessiveReplacementsRetainUnreapedPredecessor(t *testing.T) {
	for _, cap := range []int{1, 2} {
		t.Run(fmt.Sprintf("capacity %d", cap), func(t *testing.T) {
			f := &fakeTranscoder{holdChild: true}
			s := newTestSessions(t, f, t.TempDir(), nil)
			s.SetTranscodeCap(cap)
			slow := newSlowStop(s)
			defer slow.unblock()
			key := SessionKey{SessionID: "replacements"}
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			fetch := func(duration int, name string) (string, error) {
				return s.FilePath(t.Context(), key, src, duration, 1280, 720, false, name)
			}
			if _, err := fetch(120, "720p/seg00000.m4s"); err != nil {
				t.Fatal(err)
			}
			predecessor := registeredSession(t, s, key)
			slow.armed.Store(true)
			finishesWhileBlocked(t, "successor playlist", func() error {
				_, err := fetch(121, masterPlaylistName)
				return err
			})
			slow.waitEntered(t)
			finishesWhileBlocked(t, "second successor playlist", func() error {
				_, err := fetch(122, masterPlaylistName)
				return err
			})
			result := make(chan error, 1)
			go func() {
				_, err := fetch(122, "720p/seg00000.m4s")
				result <- err
			}()
			select {
			case err := <-result:
				t.Fatalf("second successor completed before predecessor reap: %v", err)
			case <-time.After(150 * time.Millisecond):
			}
			if len(f.segmentStartsSnapshot()) != 1 || len(chargedKeys(s)) != 1 {
				t.Fatal("second successor consumed capacity before predecessor reap")
			}
			slow.unblock()
			waitScratchSignal(t, predecessor.run.reaped)
			if err := waitScratchResult(t, result); err != nil {
				t.Fatal(err)
			}
			if len(f.segmentStartsSnapshot()) != 2 || len(chargedKeys(s)) != 1 {
				t.Fatal("successor failed to acquire reaped capacity")
			}
		})
	}
}

func TestT3TransferredReservationReleasesDuringCanceledPreparation(t *testing.T) {
	for _, action := range []string{"cancel request", "revoke instance"} {
		t.Run(action, func(t *testing.T) {
			fixture := t.TempDir()
			t.Setenv("BEAMFALL_T3_FIXTURE", fixture)
			t.Setenv("PATH", fixture+string(os.PathListSeparator)+os.Getenv("PATH"))
			const tool = `#!/bin/sh
fixture="$BEAMFALL_T3_FIXTURE"
case " $* " in
  *" -h protocol=http "*)
    : > "$fixture/preparing"
    while [ ! -f "$fixture/release" ]; do /bin/sleep 0.02; done
    printf '%s\n' '-max_redirects  maximum redirects'
    : > "$fixture/resolved"
    exit 0 ;;
  *" -version "*) printf '%s\n' 'ffmpeg version T3'; exit 0 ;;
esac
for arg do output="$arg"; done
case "$output" in
  http:*) : > "$fixture/unexpected-spawn"; exit 1 ;;
esac
printf init > "${output%/*}/init.mp4"
if [ ! -f "$fixture/victim" ]; then
  : > "$fixture/victim"
  exec /bin/sleep 60
fi
`
			if err := os.WriteFile(filepath.Join(fixture, "ffmpeg"), []byte(tool), 0o700); err != nil { // #nosec G306 -- private executable test fixture.
				t.Fatal(err)
			}
			src := filepath.Join(fixture, "source")
			seedScratchFile(t, src)
			s := newTestSessions(t, ffmpeg.New(), t.TempDir(), nil)
			s.SetTranscodeCap(1)
			slow := newSlowStop(s)
			defer slow.unblock()
			// Release the shared help child even if an assertion fails.
			defer func() {
				_ = os.WriteFile(filepath.Join(fixture, "release"), []byte("release"), 0o600)
			}()
			victim, target, third := SessionKey{SessionID: "victim"}, SessionKey{SessionID: "target"}, SessionKey{SessionID: "third"}
			plain := func(key SessionKey) error {
				_, err := s.FilePath(t.Context(), key, src, 120, 1280, 720, false, "720p/init.mp4")
				return err
			}
			if err := plain(victim); err != nil {
				t.Fatal(err)
			}
			slow.armed.Store(true)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source := testEncryptedSource(nil)
			encrypted := func(ctx context.Context, name string) (*LiveArtifact, error) {
				return s.LiveArtifactShaped(ctx, target, "", 120, 1280, 720, false, false,
					ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil,
					StreamShape{Encrypted: source}, PriorityForeground, name)
			}
			playlist, err := encrypted(t.Context(), masterPlaylistName)
			if err != nil {
				t.Fatal(err)
			}
			defer playlist.Release()
			reader, err := playlist.OpenEncrypted()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			result := make(chan error, 1)
			go func() {
				a, err := encrypted(ctx, "720p/init.mp4")
				if err == nil {
					a.Release()
				}
				result <- err
			}()
			slow.waitEntered(t)
			slow.unblock()
			latchCtx, finish := context.WithTimeout(t.Context(), 5*time.Second)
			defer finish()
			// fileReady requires a nonempty file. The help marker uses an empty
			// file so use the watcher's path predicate directly.
			if err := waitForArtifact(latchCtx, filepath.Join(fixture, "preparing"), nil, nil, func(path string) bool {
				_, err := os.Stat(path) // #nosec G703 -- path is the private fixture marker.
				return err == nil
			}); err != nil {
				t.Fatalf("encrypted shared lookup did not block: %v", err)
			}
			s.mu.Lock()
			start := playlist.session.start
			registered := start != nil && start.run.live != nil && !start.permitted
			s.mu.Unlock()
			if got := chargedKeys(s); len(got) != 1 || got[0] != target || !registered {
				t.Fatalf("preparing target has charge %v, endpoint registered %v", got, registered)
			}
			endpoint := start.run.live.SourceURL()
			resp, err := http.Get(endpoint) // #nosec G107 -- endpoint is the test's loopback server.
			if err != nil {
				t.Fatal("registered endpoint did not accept source reads")
			}
			plainBytes, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil || resp.StatusCode != http.StatusOK || string(plainBytes) != "plaintext-source" {
				t.Fatal("registered endpoint returned the wrong source")
			}
			want := context.Canceled
			if action == "revoke instance" {
				want = ErrSessionEvicted
				if n := s.TerminateSession(target.SessionID); n != 1 {
					t.Fatalf("terminated %d target instances", n)
				}
				if n, err := reader.Read(make([]byte, 8)); n != 0 || err == nil {
					t.Fatalf("encrypted reader after cutoff = %d bytes, %v", n, err)
				}
			} else {
				cancel()
			}
			if err := waitScratchResult(t, result); !errors.Is(err, want) {
				t.Fatalf("preparation = %v, want %v", err, want)
			}
			if len(chargedKeys(s)) != 0 {
				t.Fatal("canceled preparation retained the transferred charge")
			}
			if resp, err := http.Get(endpoint); err == nil { // #nosec G107 -- test loopback endpoint.
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					t.Fatal("source endpoint remained accessible after preparation stopped")
				}
			}
			if err := plain(third); err != nil {
				t.Fatalf("third request did not acquire vacant capacity: %v", err)
			}
			requireScratchMissing(t, filepath.Join(fixture, "resolved"))
			requireScratchMissing(t, filepath.Join(fixture, "unexpected-spawn"))
		})
	}
}

func TestT3JoinHoldsOutputThroughPublication(t *testing.T) {
	for _, action := range []string{"seek", "cutoff"} {
		t.Run(action, func(t *testing.T) {
			f := &keyframeFakeTranscoder{index: ffmpeg.KeyframeIndex{
				Keyframes: []time.Duration{0, time.Second, 8 * time.Second, 18 * time.Second}, Duration: 24 * time.Second,
			}}
			s := newTestSessions(t, f, t.TempDir(), nil)
			key := SessionKey{SessionID: "join"}
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			fetch := func(name string) (string, error) {
				return s.FilePath(t.Context(), key, src, 24, 1280, 720, true, name)
			}
			if _, err := fetch(masterPlaylistName); err != nil {
				t.Fatal(err)
			}
			h := registeredSession(t, s, key)
			dir := filepath.Join(h.dir, audioCopyRungID)
			for _, part := range []string{"seg00000.m4s", "seg00001.m4s"} {
				writeArtifact(t, dir, part, mediaSegment())
			}
			entered, release := scratchLatch(t)
			defer release.open()
			s.join = func(dst *os.File, open func(string) (*os.File, error), parts []string) error {
				if err := joinFragments(dst, open, parts); err != nil {
					return err
				}
				close(entered)
				<-release.ch
				return nil
			}
			joined := make(chan error, 1)
			go func() {
				_, err := fetch(audioCopyRungID + "/seg00000.m4s")
				joined <- err
			}()
			waitScratchSignal(t, entered)
			output := filepath.Join(dir, "joined00000.m4s")
			requireScratchMissing(t, output)
			if action == "cutoff" {
				if got := s.TerminateSession(key.SessionID); got != 1 {
					t.Fatalf("terminated %d sessions", got)
				}
				release.open()
				if err := waitScratchResult(t, joined); !errors.Is(err, ErrSessionEvicted) {
					t.Fatalf("cutoff join = %v", err)
				}
				requireScratchMissing(t, output)
				return
			}
			seek := make(chan error, 1)
			go func() {
				// A missing init segment necessarily claims a start on this
				// instance. The start cannot enable conflicting writers yet.
				_, err := fetch(audioCopyRungID + "/init.mp4")
				seek <- err
			}()
			select {
			case err := <-seek:
				t.Fatalf("conflicting start completed during join: %v", err)
			case <-time.After(150 * time.Millisecond):
			}
			s.mu.Lock()
			version := h.artifactVersion
			s.mu.Unlock()
			if version != 0 {
				t.Fatalf("conflicting start advanced output to version %d", version)
			}
			release.open()
			if err := waitScratchResult(t, joined); err != nil {
				t.Fatal(err)
			}
			if err := waitScratchResult(t, seek); err != nil {
				t.Fatal(err)
			}
			if len(mustRead(t, output)) != 2*len(mediaSegment())-len(emptySegment()) {
				t.Fatal("joined output was not published")
			}
		})
	}
}

func TestT3OlderDurationCannotPublishAcrossNewGeneration(t *testing.T) {
	for _, stage := range []string{"capture", "completion"} {
		for _, evict := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s eviction %v", stage, evict), func(t *testing.T) {
				src := filepath.Join(t.TempDir(), "source")
				other := filepath.Join(filepath.Dir(src), "other")
				seedScratchFile(t, src)
				seedScratchFile(t, other)
				f := &orderedDurationTranscoder{path: src, entered: make(chan int, 2),
					results: [2]chan int{make(chan int, 1), make(chan int, 1)}}
				s := newTestSessions(t, f, t.TempDir(), nil)
				latched, release := scratchLatch(t)
				defer release.open()
				defer close(f.results[0])
				defer close(f.results[1])
				var stats atomic.Int32
				targetStat := int32(1)
				if stage == "completion" {
					targetStat = 2
					f.results[0] <- 100
				}
				s.statSource = func(path string) (os.FileInfo, error) {
					info, err := os.Stat(path) // #nosec G703 -- source belongs to the private fixture.
					if path == src && stats.Add(1) == targetStat {
						close(latched)
						<-release.ch
					}
					return info, err
				}
				old := make(chan int, 1)
				go func() { old <- s.ProbedDurationSeconds(t.Context(), "file", src) }()
				waitScratchSignal(t, latched)
				replacement := filepath.Join(filepath.Dir(src), "replacement")
				seedScratchFile(t, replacement)
				if err := os.Rename(replacement, src); err != nil {
					t.Fatal(err)
				}
				newProbe := 0
				if stage == "completion" {
					newProbe = 1
				}
				f.results[newProbe] <- 137
				if got := s.ProbedDurationSeconds(t.Context(), "file", src); got != 137 {
					t.Fatalf("replacement duration = %d", got)
				}
				if evict {
					s.mu.Lock()
					for i := 1; i < durationProbeCacheLimit; i++ {
						s.durationProbes[fmt.Sprintf("filler-%d", i)] = durationProbeEntry{generation: "fixture", seconds: 1}
					}
					s.mu.Unlock()
					if got := s.ProbedDurationSeconds(t.Context(), "other", other); got != 60 {
						t.Fatalf("evicting publication = %d", got)
					}
				}
				release.open()
				select {
				case got := <-old:
					if got != 0 {
						t.Fatalf("older duration = %d", got)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("old probe did not finish")
				}
				s.mu.Lock()
				entry := s.durationProbes[src]
				s.mu.Unlock()
				if evict && entry.seconds != 0 || !evict && entry.seconds != 137 {
					t.Fatalf("source cache after old publication = %+v", entry)
				}
			})
		}
	}
}

func TestT3SetupRejectsReplacementAfterGridResolution(t *testing.T) {
	for _, kind := range []string{"loaded", "timed out", "cutoff"} {
		t.Run(kind, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			gridDone, releaseGrid := scratchLatch(t)
			f := &replacementGridTranscoder{}
			if kind != "loaded" {
				f.holdFirst, f.probeFinished = releaseGrid.ch, gridDone
			}
			f.holdChild = kind == "cutoff"
			s := newTestSessions(t, f, t.TempDir(), nil)
			defer releaseGrid.open()
			if kind == "timed out" {
				s.keyframes.firstPlayWait = 25 * time.Millisecond
			}
			rechecking, releaseStat := scratchLatch(t)
			defer releaseStat.open()
			var stats atomic.Int32
			s.statSource = func(path string) (os.FileInfo, error) {
				if stats.Add(1) == 2 && kind != "cutoff" {
					close(rechecking)
					<-releaseStat.ch
				}
				return os.Stat(path) // #nosec G703 -- source belongs to the private fixture.
			}
			key := SessionKey{SessionID: "grid"}
			fetch := func(name string) (string, error) {
				return s.FilePath(t.Context(), key, src, 15, 1280, 720, true, audioCopyRungID+"/"+name)
			}
			old := make(chan error, 1)
			go func() {
				_, err := fetch("seg00000.m4s")
				old <- err
			}()
			want := errSetupUnavailable
			if kind == "cutoff" {
				// Watch enrollment of the keyframe probe before fencing.
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				tick := time.NewTicker(time.Millisecond)
				defer tick.Stop()
				for f.probes.Load() == 0 && ctx.Err() == nil {
					select {
					case <-tick.C:
					case <-ctx.Done():
					}
				}
				if f.probes.Load() != 1 || s.TerminateSession(key.SessionID) != 1 {
					t.Fatal("first probe did not enroll before cutoff")
				}
				want = ErrSessionEvicted
			} else {
				waitScratchSignal(t, rechecking)
			}
			replacement := filepath.Join(filepath.Dir(src), "replacement")
			if err := os.WriteFile(replacement, []byte("new"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, src); err != nil {
				t.Fatal(err)
			}
			releaseStat.open()
			if err := waitScratchResult(t, old); !errors.Is(err, want) {
				t.Fatalf("stale grid request = %v, want %v", err, want)
			}
			s.mu.Lock()
			paths := len(s.paths)
			s.mu.Unlock()
			if paths != 0 || len(f.segmentStartsSnapshot()) != 0 {
				t.Fatal("stale grid request created artifacts or an encoder")
			}
			path, err := fetch("index.m3u8")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(mustRead(t, path)), "#EXTINF:6.000,") {
				t.Fatal("fresh setup did not use the replacement grid")
			}
			if kind == "cutoff" {
				if _, err := fetch("seg00000.m4s"); err != nil {
					t.Fatal(err)
				}
			}
			if f.holdFirst != nil {
				releaseGrid.open()
				waitScratchSignal(t, gridDone)
			}
			if kind == "cutoff" {
				s.keyframes.probes.Wait()
				if got := chargedKeys(s); len(got) != 1 || got[0] != key {
					t.Fatalf("late keyframe result disturbed the replacement encoder: %v", got)
				}
			}
		})
	}
}

type setupWaitContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *setupWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestT3SetupWaitersKeepIndependentCancellationAndVerification(t *testing.T) {
	for _, scenario := range []string{"coalesce", "composer failure", "stat failure"} {
		t.Run(scenario, func(t *testing.T) {
			key := SessionKey{SessionID: "setup"}
			s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			entered, release := scratchLatch(t)
			defer release.open()
			fail := errors.New("fixture composer failed")
			var statCalls, pictures atomic.Int32
			s.statSource = func(path string) (os.FileInfo, error) {
				if scenario == "stat failure" && statCalls.Add(1) == 1 {
					close(entered)
					<-release.ch
					return nil, fs.ErrNotExist
				}
				return os.Stat(path) // #nosec G703 -- source belongs to the private fixture.
			}
			s.SetPictureComposer(func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
				pictures.Add(1)
				if scenario == "composer failure" {
					close(entered)
					<-release.ch
					return ComposedPicture{}, false, fail
				}
				return ComposedPicture{TimedText: "resolved once"}, true, nil
			})
			if scenario == "coalesce" {
				var commits atomic.Int32
				s.beforeSetupCommit = func() {
					if commits.Add(1) == 1 {
						close(entered)
						<-release.ch
					}
				}
			}
			bind := func() *LiveBinding {
				t.Helper()
				b, err := s.BindLive(key)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(b.Release)
				return b
			}
			verified := func() *LiveBinding {
				t.Helper()
				b := bind()
				if err := b.Attach("library", "profile", nil); err != nil {
					t.Fatal(err)
				}
				return b
			}
			fetch := func(ctx context.Context, b *LiveBinding, compose bool) error {
				a, err := b.ArtifactShaped(ctx, src, 120, 1280, 720, false, false,
					ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil,
					StreamShape{ComposedPicture: compose}, PriorityNormal, masterPlaylistName)
				if err == nil {
					a.Release()
				}
				return err
			}
			denied := bind()
			if err := fetch(t.Context(), denied, false); !errors.Is(err, errSetupUnavailable) {
				t.Fatalf("unverified binder could claim setup: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := fetch(ctx, verified(), false); !errors.Is(err, context.Canceled) {
				t.Fatalf("already canceled binder = %v", err)
			}
			owner, follower := verified(), verified()
			old, next := make(chan error, 1), make(chan error, 1)
			go func() { old <- fetch(t.Context(), owner, true) }()
			waitScratchSignal(t, entered)

			waitCtx := &setupWaitContext{Context: t.Context(), waiting: make(chan struct{})}
			go func() { next <- fetch(waitCtx, follower, scenario == "coalesce") }()
			if scenario == "stat failure" {
				if err := waitScratchResult(t, next); err != nil {
					t.Fatalf("verified contender blocked by the initial stat: %v", err)
				}
				next <- nil
			} else {
				waitScratchSignal(t, waitCtx.waiting)
			}
			if scenario == "coalesce" {
				canceled := verified()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				waitCtx := &setupWaitContext{Context: ctx, waiting: make(chan struct{})}
				done := make(chan error, 1)
				go func() { done <- fetch(waitCtx, canceled, true) }()
				waitScratchSignal(t, waitCtx.waiting)
				cancel()
				if err := waitScratchResult(t, done); !errors.Is(err, context.Canceled) {
					t.Fatalf("waiting request = %v", err)
				}
			}
			release.open()
			ownerErr, followerErr := waitScratchResult(t, old), waitScratchResult(t, next)
			switch scenario {
			case "coalesce":
				if ownerErr != nil || followerErr != nil || pictures.Load() != 1 {
					t.Fatalf("matching setup = %v, %v, composed %d times", ownerErr, followerErr, pictures.Load())
				}
			case "composer failure":
				s.mu.Lock()
				exact := s.sessions[key] == owner.session
				s.mu.Unlock()
				if !errors.Is(ownerErr, fail) || !errors.Is(followerErr, errSetupUnavailable) || !exact {
					t.Fatalf("indeterminate failure = %v, %v", ownerErr, followerErr)
				}
			case "stat failure":
				// A caller may finish verification and commit while another
				// request's initial stat is blocked. The late stat has no
				// authority to evict the successful caller.
				if !errors.Is(ownerErr, errSetupUnavailable) || followerErr != nil {
					t.Fatalf("stat failure = %v, %v", ownerErr, followerErr)
				}
			}
		})
	}
}

func TestT3OpenReacquiresArtifactAfterSameInstanceSeek(t *testing.T) {
	f := &fakeTranscoder{holdChild: true}
	s := newTestSessions(t, f, t.TempDir(), nil)
	src := filepath.Join(t.TempDir(), "source")
	seedScratchFile(t, src)
	key := SessionKey{SessionID: "seek-before-open"}
	first, err := s.LiveArtifactShaped(t.Context(), key, src, 120, 1280, 720, false, false,
		ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, "720p/seg00000.m4s")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := s.FilePath(t.Context(), key, src, 120, 1280, 720, false, "720p/seg00020.m4s"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	same := s.sessions[key] == first.session && first.session.artifactVersion != first.version
	s.mu.Unlock()
	if !same {
		t.Fatal("seek did not replace the exact run")
	}
	reader, _, err := first.OpenPlain()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if data, err := io.ReadAll(reader); err != nil || len(data) == 0 {
		t.Fatalf("reacquired segment = %d bytes, %v", len(data), err)
	}
	if got := len(f.segmentStartsSnapshot()); got != 2 {
		t.Fatalf("reopening a cached segment started another child: %d starts", got)
	}
}

func TestT3PlaybackInfoDurationWinsPendingHLSMiss(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	ctx := t.Context()
	src := filepath.Join(t.TempDir(), "source")
	seedScratchFile(t, src)
	const fingerprint = "duration-publication-order"
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: src, Title: "Source",
		Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: fingerprint, VideoWidthHint: 1280, VideoHeightHint: 720,
		Streams: []store.MediaFileStream{videoStream("h264", "High", 0, ""), audioStream(1, "aac", 2)},
	})
	if err != nil || media.DurationSeconds != 0 {
		t.Fatalf("duration fixture = %d, %v", media.DurationSeconds, err)
	}
	f := &orderedDurationTranscoder{path: src, entered: make(chan int, 2),
		results: [2]chan int{make(chan int, 1), make(chan int, 1)}}
	s := newTestSessions(t, f, t.TempDir(), nil)
	defer close(f.results[0])
	defer close(f.results[1])
	service.SetSessions(s)
	key := SessionKey{SessionID: scope.SessionID, MediaID: media.ID}
	bind, err := s.BindLive(key)
	if err != nil {
		t.Fatal(err)
	}
	defer bind.Release()
	if err := bind.Attach(media.LibraryID, scope.ProfileID, st.Profile); err != nil {
		t.Fatal(err)
	}
	playlist := func(seconds int) *LiveArtifact {
		t.Helper()
		a, err := bind.ArtifactShaped(ctx, src, seconds, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1,
			ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, "720p/index.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(a.Release)
		return a
	}
	first := playlist(120)
	hls := make(chan int, 1)
	go func() { hls <- bind.ProbedDurationSeconds(ctx, fingerprint, src) }()
	waitProbe := func(want int) {
		t.Helper()
		select {
		case got := <-f.entered:
			if got != want {
				t.Fatalf("probe index = %d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("duration probe did not start")
		}
	}
	waitProbe(0)
	info := make(chan error, 1)
	go func() {
		resp, err := service.Info(ctx, scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: main10_1080p})
		if err == nil && resp.Timeline.DurationSeconds != 120 {
			err = fmt.Errorf("playback info runtime = %d", resp.Timeline.DurationSeconds)
		}
		info <- err
	}()
	waitProbe(1)
	f.results[1] <- 120
	if err := waitScratchResult(t, info); err != nil {
		t.Fatal(err)
	}
	// Eviction while the miss is pending must not erase the winning bound value.
	s.mu.Lock()
	for i := 1; i < durationProbeCacheLimit; i++ {
		s.durationProbes[fmt.Sprintf("fixture-%d", i)] = durationProbeEntry{generation: "fixture", seconds: 1}
	}
	s.mu.Unlock()
	other := filepath.Join(filepath.Dir(src), "other")
	seedScratchFile(t, other)
	if got := s.ProbedDurationSeconds(ctx, "other", other); got != 60 {
		t.Fatalf("evicting publication = %d", got)
	}
	f.results[0] <- 0
	var seconds int
	select {
	case seconds = <-hls:
	case <-time.After(5 * time.Second):
		t.Fatal("HLS duration did not complete")
	}
	if seconds != 120 {
		t.Fatalf("HLS miss lost the retained positive: %d", seconds)
	}
	again := playlist(seconds)
	if again.session != first.session || again.path != first.path || !strings.Contains(string(mustRead(t, again.path)), "#EXT-X-ENDLIST") {
		t.Fatal("the running VOD was reshaped after duration eviction")
	}
}

func TestT3PlaybackWaitsLeaveSharedHDRPreparation(t *testing.T) {
	for _, mode := range []string{"live cutoff", "playback info cancellation"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("BEAMFALL_T3_FIXTURE", dir)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			const tool = `#!/bin/sh
fixture="$BEAMFALL_T3_FIXTURE"
case " $* " in
  *" -version "*)
    printf started > "$fixture/identity"
    while [ ! -f "$fixture/release" ]; do /bin/sleep 0.02; done
    printf '%s\n' 'ffmpeg version t3-hdr'
    printf done > "$fixture/resolved"
    exit 0 ;;
esac
printf spawn > "$fixture/unexpected-spawn"
exit 1
`
			if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(tool), 0o700); err != nil { // #nosec G306 -- private executable test fixture.
				t.Fatal(err)
			}
			src := filepath.Join(dir, "source")
			seedScratchFile(t, src)
			release := func() {
				_ = os.WriteFile(filepath.Join(dir, "release"), []byte("release"), 0o600)
			}
			defer release()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s := newTestSessions(t, ffmpeg.New(), t.TempDir(), nil)
			key := SessionKey{SessionID: "hdr"}
			result := make(chan error, 1)
			want := error(ErrSessionEvicted)
			var stop func()
			if mode == "live cutoff" {
				go func() {
					_, err := s.FilePathPriority(ctx, key, src, 120, 1280, 720, false, false,
						VideoSignal{ColorTransfer: "smpte2084"}, PriorityNormal, "720p/init.mp4")
					result <- err
				}()
				stop = func() {
					if n := s.TerminateSession(key.SessionID); n != 1 {
						t.Fatalf("terminated %d HDR instances", n)
					}
				}
			} else {
				service, st, scope := capabilityTestService(t)
				service.SetSessions(s)
				prior := toneMapSupported
				toneMapSupported = ffmpeg.ToneMapSupportedContext
				t.Cleanup(func() { toneMapSupported = prior })
				stream := videoStream("hevc", "Main 10", 0, "")
				stream.ColorTransfer = "smpte2084"
				item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
					LibraryID: "library-local", Path: src, Title: "HDR",
					Container: "mkv", Codec: "hevc", Class: store.MediaClassVideo,
					ContentFingerprint: "t3-hdr", DurationSecondsHint: 120,
					VideoWidthHint: 1280, VideoHeightHint: 720, Streams: []store.MediaFileStream{stream, audioStream(1, "aac", 2)},
				})
				if err != nil {
					t.Fatal(err)
				}
				go func() {
					_, err := service.Info(ctx, scope, InfoRequest{MediaID: string(item.ID), CapabilityProfile: incapableCaps})
					result <- err
				}()
				stop, want = cancel, context.Canceled
			}
			latchCtx, stopLatch := context.WithTimeout(t.Context(), 5*time.Second)
			defer stopLatch()
			if err := waitForFile(latchCtx, filepath.Join(dir, "identity")); err != nil {
				t.Fatalf("HDR preparation did not reach shared binary identity: %v", err)
			}
			stop()
			err := waitScratchResult(t, result)
			// Info can return a refusal after the capability check is canceled.
			// Its caller still returned while tool-owned resolution was blocked.
			if mode == "live cutoff" && !errors.Is(err, want) ||
				mode != "live cutoff" && err != nil && !errors.Is(err, want) {
				t.Fatalf("canceled HDR request = %v", err)
			}
			if len(chargedKeys(s)) != 0 {
				t.Fatal("HDR preparation retained a charge")
			}
			requireScratchMissing(t, filepath.Join(dir, "unexpected-spawn"))
			requireScratchMissing(t, filepath.Join(dir, "resolved"))
			release()
			if _, err := ffmpeg.ResolveFFmpegIdentity(latchCtx); err != nil {
				t.Fatalf("surviving identity waiter: %v", err)
			}
			requireScratchMissing(t, filepath.Join(dir, "unexpected-spawn"))
		})
	}
}

func TestT3OrdinaryAdmissionCannotReplaceDegradedOverflow(t *testing.T) {
	s := newTestSessions(t, &fakeTranscoder{holdChild: true}, t.TempDir(), nil)
	s.SetTranscodeCap(1)
	normal, overflow, newcomer := SessionKey{SessionID: "normal"}, SessionKey{SessionID: "overflow"}, SessionKey{SessionID: "newcomer"}
	if _, err := filePath720Priority(s, normal, PriorityForeground, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	if _, err := filePath720Degraded(s, overflow, "480p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	if s.CanAdmitPriority(newcomer, PriorityForeground) {
		t.Fatal("ordinary admission predicted capacity while over the server cap")
	}
	if _, err := filePath720Priority(s, newcomer, PriorityForeground, "720p/seg00000.m4s"); !errors.Is(err, ErrTranscodeCapacity) {
		t.Fatalf("ordinary start with only degraded victim = %v", err)
	}
	if len(chargedKeys(s)) != 2 {
		t.Fatal("ordinary refusal disturbed the current children")
	}
}
