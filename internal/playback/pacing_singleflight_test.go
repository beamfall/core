package playback

import (
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

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
