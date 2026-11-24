package playback

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

type blockingDurationProber struct {
	fakeTranscoder
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	seconds int
}

func (p *blockingDurationProber) Probe(ctx context.Context, _ string) (ffmpeg.Probe, error) {
	p.calls.Add(1)
	select {
	case p.started <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
		return ffmpeg.Probe{DurationSeconds: p.seconds}, nil
	case <-ctx.Done():
		return ffmpeg.Probe{}, ctx.Err()
	}
}

func TestProbedSourceDurationSingleflightCoalescesAndHonorsDrain(t *testing.T) {
	prober := &blockingDurationProber{
		started: make(chan struct{}, 4),
		release: make(chan struct{}),
		seconds: 312,
	}
	s := newTestSessions(t, prober, t.TempDir(), nil)
	src := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(src, []byte("probe-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	op, err := s.claimLibrary(context.Background(), "lib-1", 0)
	if err != nil {
		t.Fatalf("claimLibrary: %v", err)
	}
	defer s.releaseLibrary(op)

	const waiters = 4
	results := make([]int, waiters)
	var wg sync.WaitGroup
	for i := range waiters {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = s.probedSourceDuration(context.Background(), nil, op, "media-1", "sample1:abc", src)
		}(i)
	}
	<-prober.started
	time.Sleep(25 * time.Millisecond)
	close(prober.release)
	wg.Wait()

	if got := prober.calls.Load(); got != 1 {
		t.Fatalf("Probe calls = %d, want 1 (singleflight)", got)
	}
	for i, sec := range results {
		if sec != 312 {
			t.Fatalf("waiter %d duration = %d, want 312", i, sec)
		}
	}

	// Verify op.ctx cancellation cancels an in-flight leader probe immediately.
	prober2 := &blockingDurationProber{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		seconds: 500,
	}
	s2 := newTestSessions(t, prober2, t.TempDir(), nil)
	op2, err := s2.claimLibrary(context.Background(), "lib-2", 0)
	if err != nil {
		t.Fatalf("claimLibrary: %v", err)
	}
	defer s2.releaseLibrary(op2)
	done := make(chan int, 1)
	go func() {
		done <- s2.probedSourceDuration(context.Background(), nil, op2, "media-2", "sample1:def", src)
	}()
	<-prober2.started
	op2.cancel()
	select {
	case got := <-done:
		if got != 0 {
			t.Fatalf("drained probe returned %d, want 0", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("probedSourceDuration did not cancel when library operation drained")
	}
}

func TestEncodeAheadResumeFloorsStallTimerConcurrently(t *testing.T) {
	run := &encoderRun{
		proc: &ffmpeg.HLSProcess{},
	}
	run.paused.Store(true)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run.resumeIfPaused()
			_ = run.lastProgressAt()
		}()
	}
	wg.Wait()

	if run.paused.Load() {
		t.Fatal("encoderRun remained paused after resumeIfPaused")
	}
	if time.Since(run.lastProgressAt()) > time.Second {
		t.Fatalf("lastProgressAt() = %v, expected recent floor after resume", run.lastProgressAt())
	}
}

func TestHLSAudioCopyPreservesPrecomputedStereoAACAndBypassForSurround(t *testing.T) {
	profile := CapabilityProfile{
		DownstreamAudio: "passthrough",
		AudioCodecs: []AudioCodecCap{
			{Codec: "aac", MaxChannels: 2},
			{Codec: "eac3", MaxChannels: 6},
			{Codec: "truehd", MaxChannels: 8},
		},
	}
	stereoAAC := []store.MediaFileStream{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}}
	gotStereo := hlsAudioProfile(profile, stereoAAC, 1, LoudnessData{}, false, quirkOverlay{})
	if gotStereo.Codec != "copy" || gotStereo.Channels != 2 {
		t.Fatalf("stereo AAC hlsAudioProfile = %+v, want copy/2ch", gotStereo)
	}
	if !precomputedAudioCompatible(gotStereo) {
		t.Fatal("stereo AAC copy must remain compatible with precomputed stereo AAC renditions")
	}

	surroundEAC3 := []store.MediaFileStream{{Index: 1, Type: "audio", Codec: "eac3", Channels: 6}}
	gotEAC3 := hlsAudioProfile(profile, surroundEAC3, 1, LoudnessData{}, false, quirkOverlay{})
	if gotEAC3.Codec != "copy" || gotEAC3.Channels != 0 {
		t.Fatalf("5.1 E-AC-3 hlsAudioProfile = %+v, want copy/0ch", gotEAC3)
	}
	if precomputedAudioCompatible(gotEAC3) {
		t.Fatal("5.1 E-AC-3 copy must bypass precomputed stereo AAC renditions")
	}

	trueHD := []store.MediaFileStream{{Index: 1, Type: "audio", Codec: "truehd", Channels: 8}}
	gotTrueHD := hlsAudioProfile(profile, trueHD, 1, LoudnessData{}, false, quirkOverlay{})
	if gotTrueHD.Codec != "copy" || precomputedAudioCompatible(gotTrueHD) {
		t.Fatalf("TrueHD passthrough = %+v must bypass precomputed stereo AAC renditions", gotTrueHD)
	}
}
