package conformance

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	visualsync "github.com/beamfall/core/tools/visualsync/contract"
)

const (
	audioFrameContractPath = "testdata/visualsync/audio-frame-contract-v1.json"
	audioFrameSinePath     = "testdata/visualsync/sine-440.frames.golden.json"
	audioFrameGapPath      = "testdata/visualsync/gap-stall-latency.frames.golden.json"
)

func TestAudioFrameContractArtifact(t *testing.T) {
	live := visualsync.Build()
	if !live.Evolution.AdditiveOnly {
		t.Fatal("AudioFrame contract must be additive-only")
	}
	if live.FrameContractVersion != visualsync.ContractVersion {
		t.Fatalf("frame version = %q, want %q", live.FrameContractVersion, visualsync.ContractVersion)
	}

	assertPinnedJSON(t, audioFrameContractPath, live)
}

func TestAudioFrameGoldens(t *testing.T) {
	cases := []struct {
		path string
		live visualsync.Fixture
	}{
		{
			path: audioFrameSinePath,
			live: visualsync.Fixture{
				Version:    visualsync.ContractVersion,
				Source:     "48kHz mono WAV, 440Hz sine, 0.16s, amplitude 0.50",
				Tolerances: visualsync.DefaultDSPConfig().PublishedTolerances,
				Frames: mustAnalyzeWAV(t,
					visualsync.WAV16Mono(sine(440, 0.16, 0.50)),
					visualsync.Options{OutputLatencyMS: 37.5, HostStartNanos: 1_000_000}),
			},
		},
		{
			path: audioFrameGapPath,
			live: visualsync.Fixture{
				Version:    visualsync.ContractVersion,
				Source:     "two 48kHz mono WAV-derived chunks, 400ms timeline gap, second chunk marked stalled",
				Tolerances: visualsync.DefaultDSPConfig().PublishedTolerances,
				Frames: visualsync.AnalyzePCMChunks([]visualsync.PCMChunk{
					{Samples: sine(110, 0.09, 0.25)},
					{MediaStartMS: 400, Samples: sine(660, 0.09, 0.25), Stall: true},
				}, visualsync.Options{OutputLatencyMS: 125, HostStartNanos: 2_000_000}),
			},
		},
	}
	for _, tc := range cases {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			assertPinnedFrames(t, tc.path, tc.live)
		})
	}
}

func mustAnalyzeWAV(t *testing.T, data []byte, opts visualsync.Options) []visualsync.AudioFrame {
	t.Helper()
	frames, err := visualsync.AnalyzeWAV(data, opts)
	if err != nil {
		t.Fatal(err)
	}
	return frames
}

func assertPinnedJSON(t *testing.T, path string, live any) {
	t.Helper()
	liveJSON, err := json.MarshalIndent(live, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	liveJSON = append(liveJSON, '\n')
	if os.Getenv("BEAMFALL_UPDATE_AUDIOFRAME_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, liveJSON, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(committed) != string(liveJSON) {
		t.Fatalf("%s drifted; rerun with BEAMFALL_UPDATE_AUDIOFRAME_GOLDENS=1", path)
	}
}

func assertPinnedFrames(t *testing.T, path string, live visualsync.Fixture) {
	t.Helper()
	if os.Getenv("BEAMFALL_UPDATE_AUDIOFRAME_GOLDENS") == "1" {
		assertPinnedJSON(t, path, live)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var golden visualsync.Fixture
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.Version != live.Version || golden.Source != live.Source {
		t.Fatalf("%s metadata drifted", path)
	}
	if !withinFrames(golden.Frames, live.Frames, golden.Tolerances) {
		t.Fatalf("%s frames drifted beyond tolerance", path)
	}
}

func withinFrames(a, b []visualsync.AudioFrame, tol visualsync.Tolerance) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ContractVersion != y.ContractVersion || x.Sequence != y.Sequence || x.HostTimeNanos != y.HostTimeNanos || !reflect.DeepEqual(x.Flags, y.Flags) {
			return false
		}
		if !near(x.MediaTimeMS, y.MediaTimeMS, tol.TimeMS) ||
			!near(x.OutputLatencyMS, y.OutputLatencyMS, tol.TimeMS) ||
			!near(x.AudibleTimeMS, y.AudibleTimeMS, tol.TimeMS) ||
			!near(x.RMS, y.RMS, tol.Scalar) ||
			!near(x.LoudnessDB, y.LoudnessDB, tol.Scalar) ||
			!near(x.Onset.Broadband, y.Onset.Broadband, tol.Scalar) ||
			!near(x.Onset.Low, y.Onset.Low, tol.Scalar) ||
			!near(x.Onset.Mid, y.Onset.Mid, tol.Scalar) ||
			!near(x.Onset.High, y.Onset.High, tol.Scalar) {
			return false
		}
		if len(x.Spectrum) != len(y.Spectrum) {
			return false
		}
		for j := range x.Spectrum {
			if !near(x.Spectrum[j], y.Spectrum[j], tol.Spectrum) {
				return false
			}
		}
	}
	return true
}

func near(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func sine(freq, seconds, amp float64) []float64 {
	cfg := visualsync.DefaultDSPConfig()
	n := int(seconds * float64(cfg.SampleRateHz))
	out := make([]float64, n)
	for i := range out {
		out[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/float64(cfg.SampleRateHz))
	}
	return out
}
