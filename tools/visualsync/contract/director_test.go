package contract

import (
	"reflect"
	"testing"
)

func TestDirectorDeterministicAcrossFrameRates(t *testing.T) {
	a := RunDirector(DirectorInput{
		Frames:        directorFrames(250, 90000, 0.65, 0.45),
		DurationMS:    90000,
		Seed:          "track-1",
		UserIntensity: 0.8,
		Profile:       DefaultSurfaceProfile("web"),
	})
	b := RunDirector(DirectorInput{
		Frames:        directorFrames(125, 90000, 0.65, 0.45),
		DurationMS:    90000,
		Seed:          "track-1",
		UserIntensity: 0.8,
		Profile:       DefaultSurfaceProfile("web"),
	})
	if !reflect.DeepEqual(projectTrace(a), projectTrace(b)) {
		t.Fatalf("trace differs across resampling\n250ms=%#v\n125ms=%#v", projectTrace(a), projectTrace(b))
	}
}

func TestDirectorClampCompositionAndFlashGuard(t *testing.T) {
	in := DirectorInput{
		Frames:        adversarialOnsets(),
		DurationMS:    2000,
		Seed:          "adversarial",
		UserIntensity: 1,
		Profile:       DirectorProfile{Surface: "web", DefaultIntensity: 0.5, PhotosensitiveSafe: true},
	}
	trace := RunDirector(in)
	found := false
	for _, got := range trace {
		if got.FallbackReason == FallbackRuntimeFlashGuard {
			found = true
			if got.Intensity > 0.25 {
				t.Fatalf("flash guard intensity = %v, want <= 0.25", got.Intensity)
			}
			if got.Transition != "hold" || got.Alignment != "safety" {
				t.Fatalf("flash guard transition = %s/%s, want hold/safety", got.Transition, got.Alignment)
			}
		}
		if got.Clamp.MaxFlashHz != 1 || got.Clamp.MaxLuminanceDelta != 0.35 {
			t.Fatalf("safe-mode clamp = %#v, want flashHz=1 luminanceDelta=0.35", got.Clamp)
		}
	}
	if !found {
		t.Fatalf("runtime flash guard never fired: %#v", trace)
	}
}

func TestDirectorAppliesPhotosensitivityRuntimeClamp(t *testing.T) {
	frames := directorFrames(250, 1000, 0.8, 0.3)
	for _, t := range []float64{1250, 1500, 1750} {
		frames = append(frames, directorFrame(t, 0.5, 0.86, 0.8))
	}
	bounds := PhotosensitivityBounds{MaxFlashHz: 3, MaxLuminanceDelta: 0.8, MaxFlashArea: 0.2, StrobeBanned: true}

	unsafe := RunDirector(DirectorInput{Frames: frames, DurationMS: 2500, TickMS: 250, Seed: "strobe", Profile: DefaultSurfaceProfile("web")})
	safe := RunDirector(DirectorInput{Frames: frames, DurationMS: 2500, TickMS: 250, Seed: "strobe", Profile: DefaultSurfaceProfile("web"), Photosensitivity: &bounds})

	if unsafe[7].Cue != CueDrop {
		t.Fatalf("unclamped third flash cue = %s, want drop", unsafe[7].Cue)
	}
	if safe[7].Cue != CueGroove {
		t.Fatalf("clamped third flash cue = %s, want groove", safe[7].Cue)
	}
	if safe[7].Clamp.MaxFlashHz != bounds.MaxFlashHz || safe[7].Clamp.MaxLuminanceDelta != bounds.MaxLuminanceDelta {
		t.Fatalf("runtime clamp = %#v, want photosensitivity bounds", safe[7].Clamp)
	}
}

func TestClampFlashSequenceBlocksThirdRegularFlash(t *testing.T) {
	bounds := PhotosensitivityBounds{MaxFlashHz: 3, MaxLuminanceDelta: 0.8, MaxFlashArea: 0.2, StrobeBanned: true}
	frames := []AudioFrame{
		directorFrame(0, 0.5, 0.8, 0.8),
		directorFrame(250, 0.5, 0.8, 0.8),
		directorFrame(500, 0.5, 0.8, 0.8),
	}

	clamped := ClampFlashSequence(frames, bounds)
	if clamped[2].Onset.Broadband >= flashThreshold {
		t.Fatalf("third regular flash broadband = %v, want below %v", clamped[2].Onset.Broadband, flashThreshold)
	}
	if got := AnalyzeFlashSequence(clamped); got.Exceeds(bounds) {
		t.Fatalf("clamped regular triplet still exceeds bounds: %#v", got)
	}
	if frames[2].Onset.Broadband != 0.8 {
		t.Fatalf("input frame mutated: %v", frames[2].Onset.Broadband)
	}
}

func TestDirectorSeededSelectionAndHoldRelease(t *testing.T) {
	frames := directorFrames(250, 70000, 0.7, 0.5)
	first := RunDirector(DirectorInput{Frames: frames, DurationMS: 70000, Seed: "same", Profile: DefaultSurfaceProfile("web")})
	second := RunDirector(DirectorInput{Frames: frames, DurationMS: 70000, Seed: "same", Profile: DefaultSurfaceProfile("web")})
	if !reflect.DeepEqual(projectSelections(first), projectSelections(second)) {
		t.Fatalf("seeded selections drifted\nfirst=%#v\nsecond=%#v", projectSelections(first), projectSelections(second))
	}
	held := DirectorSelection{VisualID: "event-horizon", SmartPresetID: "event-horizon-drop"}
	safe := RunDirector(DirectorInput{
		Frames:        frames,
		DurationMS:    70000,
		Seed:          "same",
		Profile:       DirectorProfile{Surface: "web", DefaultIntensity: 0.5, PhotosensitiveSafe: true},
		HoldSelection: &held,
	})
	if safe[0].Selection == held {
		t.Fatal("held unsafe selection survived photosensitive clamp")
	}
	if safe[0].FallbackReason != FallbackHoldReleasedByClamp {
		t.Fatalf("fallback = %q, want %q", safe[0].FallbackReason, FallbackHoldReleasedByClamp)
	}
}

func TestDirectorCueHysteresisAndAmbientFallback(t *testing.T) {
	frames := []AudioFrame{
		directorFrame(40000, 0.30, 0.20, 0.59),
		directorFrame(40250, 0.30, 0.20, 0.61),
		directorFrame(40500, 0.30, 0.70, 0.41),
		directorFrame(40750, 0.30, 0.20, 0.39),
	}
	trace := RunDirector(DirectorInput{Frames: frames, DurationMS: 120000, Seed: "hysteresis", Profile: DefaultSurfaceProfile("web")})
	offset := 160
	if trace[offset].Cue != CueAmbient || trace[offset].FallbackReason != FallbackLowTrustAmbient {
		t.Fatalf("first cue = %s/%s, want ambient low-trust", trace[offset].Cue, trace[offset].FallbackReason)
	}
	if trace[offset+2].Cue != CueBuild {
		t.Fatalf("trusted cue after 0.41 confidence = %s, want build", trace[offset+2].Cue)
	}
	if trace[offset+3].Cue != CueAmbient {
		t.Fatalf("cue after confidence below low watermark = %s, want ambient", trace[offset+3].Cue)
	}
}

func directorFrames(stepMS, durationMS, beatConfidence, onset float64) []AudioFrame {
	var frames []AudioFrame
	for t := 0.0; t <= durationMS; t += stepMS {
		f := directorFrame(t, 0.45, onset, beatConfidence)
		if int(t)%16000 == 0 {
			f.Phrase = &Phase{Confidence: beatConfidence, TimeToNextMS: 0}
		}
		frames = append(frames, f)
	}
	return frames
}

func adversarialOnsets() []AudioFrame {
	var frames []AudioFrame
	for t := 0.0; t <= 2000; t += 100 {
		frames = append(frames, directorFrame(t, 0.6, 0.95, 0.8))
	}
	return frames
}

func directorFrame(t, rms, onset, beatConfidence float64) AudioFrame {
	return AudioFrame{
		ContractVersion: ContractVersion,
		MediaTimeMS:     t,
		AudibleTimeMS:   t,
		RMS:             rms,
		Onset:           Onset{Broadband: onset, High: onset},
		Beat:            &Phase{Confidence: beatConfidence, TimeToNextMS: 250},
		Bar:             &Phase{Confidence: beatConfidence, TimeToNextMS: 1000},
	}
}

func projectTrace(trace []DirectorTrace) []struct {
	Tick      int
	Cue       string
	Selection DirectorSelection
	Intensity float64
	Fallback  string
} {
	out := make([]struct {
		Tick      int
		Cue       string
		Selection DirectorSelection
		Intensity float64
		Fallback  string
	}, len(trace))
	for i, got := range trace {
		out[i].Tick = got.Tick
		out[i].Cue = got.Cue
		out[i].Selection = got.Selection
		out[i].Intensity = got.Intensity
		out[i].Fallback = got.FallbackReason
	}
	return out
}

func projectSelections(trace []DirectorTrace) []DirectorSelection {
	out := make([]DirectorSelection, len(trace))
	for i, got := range trace {
		out[i] = got.Selection
	}
	return out
}
