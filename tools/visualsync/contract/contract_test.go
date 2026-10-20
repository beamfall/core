package contract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestFrameRoundTripAndVersioning(t *testing.T) {
	frame := AnalyzePCM(Sine(440, 0.08, 0.5), Options{OutputLatencyMS: 87.5})[0]
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeFrame(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContractVersion != ContractVersion || got.AudibleTimeMS != frame.AudibleTimeMS {
		t.Fatalf("round trip drifted: got %#v want %#v", got, frame)
	}
	withFutureField := strings.TrimSuffix(string(data), "}") + `,"futureField":true}`
	if _, err := DecodeFrame([]byte(withFutureField)); err != nil {
		t.Fatalf("additive field rejected: %v", err)
	}
	badVersion := strings.Replace(string(data), ContractVersion, "audio-frame.v2", 1)
	if _, err := DecodeFrame([]byte(badVersion)); err == nil {
		t.Fatal("unsupported version accepted")
	}
}

func TestWAVRoundTrip(t *testing.T) {
	want := Sine(220, 0.08, 0.35)
	got, err := PCMFromWAV(WAV16Mono(want))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("sample count = %d, want %d", len(got), len(want))
	}
	frames, err := AnalyzeWAV(WAV16Mono(want), Options{OutputLatencyMS: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	if math.Abs(frames[0].AudibleTimeMS-(frames[0].MediaTimeMS-25)) > DefaultDSPConfig().PublishedTolerances.TimeMS {
		t.Fatalf("latency not applied: %#v", frames[0])
	}
}

func TestGapAndStallFlags(t *testing.T) {
	frames := AnalyzePCMChunks([]PCMChunk{
		{Samples: Sine(110, 0.08, 0.25)},
		{MediaStartMS: 400, Samples: Sine(110, 0.08, 0.25), Stall: true},
	}, Options{})
	if len(frames) < 2 {
		t.Fatal("not enough frames")
	}
	found := false
	for _, flag := range frames[len(AnalyzePCM(Sine(110, 0.08, 0.25), Options{}))].Flags {
		if flag == "analysis-gap" || flag == "analysis-stalled" {
			found = true
		}
	}
	if !found {
		t.Fatalf("gap/stall flags missing: %#v", frames)
	}
}

func Sine(freq, seconds, amp float64) []float64 {
	cfg := DefaultDSPConfig()
	n := int(seconds * float64(cfg.SampleRateHz))
	out := make([]float64, n)
	for i := range out {
		out[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/float64(cfg.SampleRateHz))
	}
	return out
}
