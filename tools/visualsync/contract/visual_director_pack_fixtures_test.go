package contract

// Matrix-row fixture builders for the visual-director conformance pack.
//
// Each builder constructs a DirectorInput (via the tagged pack DTOs) whose
// tick-indexed AudioFrame sequence deterministically lands the intended cue /
// selection / clamp behavior under the FROZEN reference derivation rules in
// director.go, plus the mandatory per-tick SmartPreset evaluator samples. The
// expected DirectorTrace is filled in from RunDirector at generation time; these
// builders own only the inputs and metadata.
//
// Rows map onto docs/architecture/visual-runtime.md (accepted). "Toggled
// mid-playback" rows (reduced-motion) are before/after PAIRS of single runs
// because RunDirector composes profile/clamps once per run.

// stdCandidates mirrors DefaultDirectorCandidates so fixtures pin an explicit,
// stable candidate set rather than depending on the built-in default.
func stdCandidates() []packCandidate {
	return []packCandidate{
		{VisualID: "liquid-optics", SmartPresetID: "liquid-optics-calm", Tier: "Lite", OverlaySafe: true, ReducedMotionSafe: true, PhotosensitiveSafe: true, PerformanceCost: 1, SupportsCrossfade: true},
		{VisualID: "volumetric-light", SmartPresetID: "volumetric-light-balanced", Tier: "Lite", OverlaySafe: true, ReducedMotionSafe: true, PhotosensitiveSafe: true, PerformanceCost: 1, SupportsCrossfade: true},
		{VisualID: "spectral-city", SmartPresetID: "spectral-city-kinetic", Tier: "Standard", OverlaySafe: true, ReducedMotionSafe: false, PhotosensitiveSafe: false, PerformanceCost: 2},
		{VisualID: "cymatics-prism", SmartPresetID: "cymatics-prism-build", Tier: "Standard", OverlaySafe: true, ReducedMotionSafe: false, PhotosensitiveSafe: false, PerformanceCost: 2},
		{VisualID: "event-horizon", SmartPresetID: "event-horizon-drop", Tier: "Flagship", OverlaySafe: false, ReducedMotionSafe: false, PhotosensitiveSafe: false, PerformanceCost: 3},
	}
}

// beatFrame is a frame with a trusted beat/bar; broadband/high onset and RMS
// drive cue selection.
func beatFrame(t, rms, onset, high, beatConf float64) packFrame {
	return packFrame{
		MediaTimeMS: t, RMS: rms, OnsetBroadband: onset, OnsetHigh: high,
		BeatConfidence: beatConf, BarConfidence: beatConf,
		HasBeat: true, HasBar: true,
	}
}

// weakFrame is a frame with low/absent beat confidence (untrusted beat state).
func weakFrame(t, rms, onset, beatConf float64) packFrame {
	f := beatFrame(t, rms, onset, onset, beatConf)
	return f
}

// series builds a uniform frame sequence at 250ms ticks over [0,duration].
func series(duration float64, gen func(t float64) packFrame) []packFrame {
	var out []packFrame
	for t := 0.0; t <= duration; t += 250 {
		out = append(out, gen(t))
	}
	return out
}

// stdEval pins per-tick SmartPreset evaluator samples covering param0..2 plus the
// hint channels (palette/motion/camera/overlay/intensity), each across two
// tick-indexed CurveInputs, so per-tick easing is pinned. Want values are filled
// in from EvaluateCurve at generation time via fillEvalWants.
func stdEval(presetID, visualID string) packEval {
	f := func(v float64) *float64 { return &v }
	e := packEval{
		PresetID: presetID, VisualID: visualID,
		Samples: []packSample{
			{Target: "param0", Curve: Curve{Kind: "constant", Value: f(0.3), Min: f(0), Max: f(1)}, Input: CurveInput{}},
			{Target: "param1", Curve: Curve{Kind: "easing", From: f(0), To: f(1), Duration: 2, Unit: "seconds", Easing: "easeInOut"}, Input: CurveInput{ElapsedSeconds: 0.5}},
			{Target: "param1", Curve: Curve{Kind: "easing", From: f(0), To: f(1), Duration: 2, Unit: "seconds", Easing: "easeInOut"}, Input: CurveInput{ElapsedSeconds: 1.5}},
			{Target: "param2", Curve: Curve{Kind: "envelope", Value: f(1), Attack: 0.5, Release: 0.5, ReleaseAt: 1, Unit: "seconds"}, Input: CurveInput{ElapsedSeconds: 0.25}},
			{Target: "param2", Curve: Curve{Kind: "envelope", Value: f(1), Attack: 0.5, Release: 0.5, ReleaseAt: 1, Unit: "seconds"}, Input: CurveInput{ElapsedSeconds: 1.25}},
			{Target: "motion", Curve: Curve{Kind: "lfo", Shape: "sine", Period: 4, Unit: "beats", Min: f(0), Max: f(1)}, Input: CurveInput{ElapsedBeats: 1}},
			{Target: "motion", Curve: Curve{Kind: "lfo", Shape: "sine", Period: 4, Unit: "beats", Min: f(0), Max: f(1)}, Input: CurveInput{ElapsedBeats: 3}},
			{Target: "camera", Curve: Curve{Kind: "lfo", Shape: "triangle", Period: 8, Unit: "seconds", Min: f(0.2), Max: f(0.8)}, Input: CurveInput{ElapsedSeconds: 2}},
			{Target: "palette", Curve: Curve{Kind: "easing", From: f(0.1), To: f(0.9), Duration: 4, Unit: "seconds", Easing: "linear"}, Input: CurveInput{ElapsedSeconds: 2}},
			{Target: "overlay", Curve: Curve{Kind: "constant", Value: f(0.5), Min: f(0), Max: f(1)}, Input: CurveInput{}},
			{Target: "intensity", Curve: Curve{Kind: "envelope", Value: f(0.8), Attack: 1, Release: 2, ReleaseAt: 3, Unit: "seconds"}, Input: CurveInput{ElapsedSeconds: 2}},
		},
	}
	return e
}

// fillEvalWants computes each sample's Want from the reference evaluator, so the
// evaluator side is pinned to the live implementation exactly like the traces.
func fillEvalWants(e packEval) packEval {
	for i := range e.Samples {
		got, err := EvaluateCurve(e.Samples[i].Curve, e.Samples[i].Input)
		if err != nil {
			// A malformed curve is a builder bug; surface it as an impossible want
			// so the pack test fails loudly rather than silently pinning 0.
			e.Samples[i].Want = -1
			continue
		}
		e.Samples[i].Want = got
	}
	return e
}

func fixture(name, row, notes string, in packInput, eval packEval) packFixture {
	if in.Photosensitivity == nil {
		in.Photosensitivity = defaultPhotosensitivityBounds()
	}
	return packFixture{
		Version:         visualDirectorPackVersion,
		Name:            name,
		MatrixRow:       row,
		Notes:           notes,
		Input:           in,
		SmartPresetEval: fillEvalWants(eval),
	}
}

func defaultPhotosensitivityBounds() *PhotosensitivityBounds {
	return &PhotosensitivityBounds{
		MaxFlashHz:        MaxDeclaredFlashHz,
		MaxLuminanceDelta: MaxDeclaredLuminanceDelta,
		MaxFlashArea:      MaxDeclaredFlashArea,
		StrobeBanned:      true,
	}
}

// buildPackFixtures returns every matrix-row fixture. Fixture names sort in a
// stable order (used to pair with the committed files).
func buildPackFixtures() []packFixture {
	web := packProfile{Surface: "web"}
	var out []packFixture

	// 01 beat-heavy: trusted beats, high broadband onset -> drop/build/groove.
	out = append(out, fixture("01-beat-heavy", "beat-heavy",
		"Trusted beats with strong broadband onset drive drop/build cues.",
		packInput{DurationMS: 60000, Seed: "beat-heavy", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.5, 0.9, 0.7, 0.8) })},
		stdEval("event-horizon-drop", "event-horizon")))

	// 02 quiet: normalized-quiet reference; low RMS but trusted -> break/groove.
	out = append(out, fixture("02-quiet", "quiet",
		"Loudness-normalized quiet track (reference amplitude pinned in metadata); low RMS yields break/groove.",
		packInput{DurationMS: 60000, Seed: "quiet", UserIntensity: 0.6, Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.10, 0.30, 0.20, 0.7) })},
		stdEval("liquid-optics-calm", "liquid-optics")))

	// 03 silence: no energy, untrusted -> ambient (low-trust).
	out = append(out, fixture("03-silence", "silence",
		"Silence: untrusted beat, near-zero RMS and onset -> ambient low-trust.",
		packInput{DurationMS: 60000, Seed: "silence", Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return weakFrame(t, 0.02, 0.05, 0.0) })},
		stdEval("liquid-optics-calm", "liquid-optics")))

	// 04 low-confidence beatgrid: confidence below trust watermark -> ambient.
	out = append(out, fixture("04-low-confidence-beatgrid", "low-confidence-beatgrid",
		"Beatgrid present but confidence below the 0.60 enter watermark -> untrusted, ambient path.",
		packInput{DurationMS: 60000, Seed: "low-conf", Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.15, 0.30, 0.20, 0.45) })},
		stdEval("liquid-optics-calm", "liquid-optics")))

	// 05 beatgrid arrives mid-playback: confidence rises across ticks within one
	// run (per-frame data the oracle reads) -> untrusted then trusted.
	out = append(out, fixture("05-beatgrid-arrives-mid", "beatgrid-arrives-mid-playback",
		"Beat confidence rises past the 0.60 enter watermark mid-run: cue transitions from ambient to trusted cues.",
		packInput{DurationMS: 60000, Seed: "grid-arrival", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame {
				conf := 0.20
				onset := 0.30
				if t >= 30000 {
					conf = 0.8
					onset = 0.9
				}
				return beatFrame(t, 0.5, onset, 0.7, conf)
			})},
		stdEval("event-horizon-drop", "event-horizon")))

	// 06 absent sync-lanes: no Phrase at all -> phraseTurn never fires.
	out = append(out, fixture("06-absent-sync-lanes", "absent-sync-lanes",
		"No phrase boundaries (Phrase==nil): phraseTurn cue never fires; steady groove/build.",
		packInput{DurationMS: 60000, Seed: "no-lanes", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.4, 0.6, 0.5, 0.7) })},
		stdEval("cymatics-prism-build", "cymatics-prism")))

	// 07 misaligned sync-lanes: Phrase present but low confidence / not
	// tick-aligned time-to-next -> phraseTurn suppressed, alignment degrades.
	out = append(out, fixture("07-misaligned-sync-lanes", "misaligned-sync-lanes",
		"Phrase present but low confidence and TimeToNextMS beyond a tick: phraseTurn suppressed, bestAlignment falls back.",
		packInput{DurationMS: 60000, Seed: "misaligned", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame {
				f := beatFrame(t, 0.5, 0.9, 0.7, 0.8)
				f.HasPhrase = true
				f.PhraseConf = 0.40
				f.PhraseToNextMS = 900
				return f
			})},
		stdEval("event-horizon-drop", "event-horizon")))

	// 08 variable-tempo: onset/confidence vary tick-to-tick, exercising the
	// hysteresis and per-frame cue derivation without wall-clock assumptions.
	out = append(out, fixture("08-variable-tempo", "variable-tempo",
		"Onset and confidence vary per tick (local phase, no client BPM inference): cues track per-frame derivation.",
		packInput{DurationMS: 60000, Seed: "var-tempo", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame {
				step := int(t/250) % 4
				onset := []float64{0.9, 0.5, 0.3, 0.7}[step]
				conf := []float64{0.8, 0.7, 0.5, 0.75}[step]
				return beatFrame(t, 0.4, onset, 0.6, conf)
			})},
		stdEval("cymatics-prism-build", "cymatics-prism")))

	// 09 ambient (non-rhythmic): untrusted, low-energy material -> ambient path.
	out = append(out, fixture("09-ambient-non-rhythmic", "ambient-non-rhythmic",
		"Non-rhythmic material: no trusted beat, low onset -> ambient/groove energy-reactive path.",
		packInput{DurationMS: 60000, Seed: "ambient", Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return weakFrame(t, 0.16, 0.30, 0.30) })},
		stdEval("volumetric-light-balanced", "volumetric-light")))

	// 10 seek mid-phrase: the frame sequence the client feeds after a seek jump
	// (media time jumps forward mid-run); the oracle is stateless, so this is the
	// post-seek sequence.
	out = append(out, fixture("10-seek-mid-phrase", "seek-mid-phrase",
		"Post-seek frame sequence (media time jumps to a later phrase): Director resumes cue derivation from the fed frames.",
		packInput{DurationMS: 60000, Seed: "seek", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: seekFrames()},
		stdEval("event-horizon-drop", "event-horizon")))

	// 11 pause/resume: a media-time gap between two runs of frames modeled as one
	// fed sequence with a hold in media time then resume.
	out = append(out, fixture("11-pause-resume", "pause-resume",
		"Pause then resume: frames repeat the same media time during pause, then advance on resume.",
		packInput{DurationMS: 60000, Seed: "pause", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: pauseResumeFrames()},
		stdEval("cymatics-prism-build", "cymatics-prism")))

	// 12 track-change mid-phrase: a fresh track's opening frames (new seed).
	out = append(out, fixture("12-track-change", "track-change-mid-phrase",
		"Track change: fresh frame sequence with a new selection seed; cue derivation restarts at intro.",
		packInput{DurationMS: 60000, Seed: "track-2", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.5, 0.9, 0.7, 0.8) })},
		stdEval("event-horizon-drop", "event-horizon")))

	// 13 reduced-motion OFF (before half of the toggle pair). Moderate onset
	// (below the 0.80 flash-guard threshold) so the reduced-motion clamp's effect
	// on intensity/selection is observable rather than masked by the flash guard.
	out = append(out, fixture("13-reduced-motion-off", "reduced-motion-before",
		"Reduced-motion OFF (before-half of the toggle pair): full candidate set, no reduced-motion clamp.",
		packInput{DurationMS: 60000, Seed: "rm-toggle", UserIntensity: 0.9, Profile: web, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.4, 0.6, 0.5, 0.8) })},
		stdEval("cymatics-prism-build", "cymatics-prism")))

	// 14 reduced-motion ON (after half): same frames + seed, run-start
	// reduced-motion. The pair asserts the degrade the toggle would cause
	// (reduced-motion clamp lowers the intensity ceiling and filters
	// non-reduced-motion-safe candidates).
	out = append(out, fixture("14-reduced-motion-on", "reduced-motion-after",
		"Reduced-motion ON (after-half of the toggle pair): identical frames/seed, reduced-motion clamp degrades selection and intensity.",
		packInput{DurationMS: 60000, Seed: "rm-toggle", UserIntensity: 0.9, Profile: packProfile{Surface: "web", ReducedMotion: true}, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.4, 0.6, 0.5, 0.8) })},
		stdEval("liquid-optics-calm", "liquid-optics")))

	// 15 overlay-safe: overlay-safe profile filters non-overlay-safe candidates.
	out = append(out, fixture("15-overlay-safe", "overlay-safe",
		"Overlay-safe mode: only overlay-safe candidates are compatible; intensity clamped.",
		packInput{DurationMS: 60000, Seed: "overlay", UserIntensity: 0.9, Profile: packProfile{Surface: "web", OverlaySafe: true}, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.5, 0.9, 0.7, 0.8) })},
		stdEval("spectral-city-kinetic", "spectral-city")))

	// 16 adversarial strobe: synthetic dense broadband onsets -> runtime flash
	// guard fires under photosensitive-safe clamp.
	out = append(out, fixture("16-adversarial-strobe", "adversarial-strobe",
		"Synthetic dense onsets exercising the Director rate limit under photosensitive-safe: runtime flash guard fires.",
		packInput{DurationMS: 4000, Seed: "strobe", UserIntensity: 1, Profile: packProfile{Surface: "web", PhotosensitiveSafe: true}, Candidates: stdCandidates(),
			Frames: strobeFrames()},
		stdEval("liquid-optics-calm", "liquid-optics")))

	// 17 over-budget: low-performance budget -> degradation-ladder steps, in order.
	out = append(out, fixture("17-over-budget", "over-budget",
		"Sustained over-budget (low performance budget): degradation-ladder steps taken in order.",
		packInput{DurationMS: 60000, Seed: "budget", UserIntensity: 0.9, Profile: packProfile{Surface: "web", LowPerformanceBudget: true}, Candidates: stdCandidates(),
			Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.5, 0.9, 0.7, 0.8) })},
		stdEval("liquid-optics-calm", "liquid-optics")))

	// 18 AudioFrame gap/stall and resume: a frame flagged live-analysis-unavailable
	// (the sole Flag the Director reads) mid-run -> ambient during the gap.
	out = append(out, fixture("18-audioframe-gap", "audioframe-gap-stall-resume",
		"AudioFrame gap: frames marked live-analysis-unavailable force ambient during the gap, then resume.",
		packInput{DurationMS: 60000, Seed: "gap", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: gapFrames()},
		stdEval("liquid-optics-calm", "liquid-optics")))

	// 19 Bluetooth-scale latency: large output latency; audible time shifts but
	// media-time cue derivation is unchanged (documents latency independence).
	out = append(out, fixture("19-bluetooth-latency", "bluetooth-scale-latency",
		"Bluetooth-scale output latency: audibleTime shifts but media-time cue derivation is unaffected.",
		packInput{DurationMS: 60000, Seed: "bt-latency", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: btLatencyFrames()},
		stdEval("event-horizon-drop", "event-horizon")))

	// 20 transition cadence: phrase boundaries at intervals -> phraseTurn cues +
	// crossfade/cut transitions on boundary alignment.
	out = append(out, fixture("20-transition-cadence", "transition-cadence",
		"Aligned phrase boundaries at intervals: phraseTurn cues with boundary-aligned transitions.",
		packInput{DurationMS: 80000, Seed: "cadence", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			Frames: cadenceFrames()},
		stdEval("cymatics-prism-build", "cymatics-prism")))

	// 21 hold-override: a held compatible selection persists across boundaries.
	held := DirectorSelection{VisualID: "volumetric-light", SmartPresetID: "volumetric-light-balanced"}
	out = append(out, fixture("21-hold-override", "hold-override",
		"'Hold this look': a compatible held selection persists across qualifying boundaries.",
		packInput{DurationMS: 80000, Seed: "hold", UserIntensity: 0.8, Profile: web, Candidates: stdCandidates(),
			HoldSelection: &held, Frames: cadenceFrames()},
		stdEval("volumetric-light-balanced", "volumetric-light")))

	// 22 hold-release-on-clamp: an unsafe held selection is released under the
	// photosensitive clamp (fallback hold-released-by-clamp).
	// Moderate onset (below the 0.80 flash-guard threshold) so the
	// hold-released-by-clamp fallback is observable rather than overwritten by the
	// runtime flash guard's own fallback reason.
	unsafe := DirectorSelection{VisualID: "event-horizon", SmartPresetID: "event-horizon-drop"}
	out = append(out, fixture("22-hold-release-on-clamp", "hold-release-on-clamp",
		"Held unsafe selection released by photosensitive clamp: fallback hold-released-by-clamp.",
		packInput{DurationMS: 60000, Seed: "hold-clamp", UserIntensity: 0.8, Profile: packProfile{Surface: "web", PhotosensitiveSafe: true}, Candidates: stdCandidates(),
			HoldSelection: &unsafe, Frames: series(60000, func(t float64) packFrame { return beatFrame(t, 0.4, 0.6, 0.5, 0.8) })},
		stdEval("liquid-optics-calm", "liquid-optics")))

	return out
}

func seekFrames() []packFrame {
	var out []packFrame
	// Pre-seek: first ~15s of a track.
	for t := 0.0; t <= 15000; t += 250 {
		out = append(out, beatFrame(t, 0.5, 0.9, 0.7, 0.8))
	}
	// Seek jump: media time jumps to 40s and continues.
	for t := 40000.0; t <= 60000; t += 250 {
		out = append(out, beatFrame(t, 0.5, 0.9, 0.7, 0.8))
	}
	return out
}

func pauseResumeFrames() []packFrame {
	var out []packFrame
	for t := 0.0; t <= 20000; t += 250 {
		out = append(out, beatFrame(t, 0.5, 0.7, 0.6, 0.75))
	}
	// Pause: media time holds at 20000 for several frames.
	for i := 0; i < 8; i++ {
		out = append(out, beatFrame(20000, 0.5, 0.7, 0.6, 0.75))
	}
	// Resume.
	for t := 20250.0; t <= 60000; t += 250 {
		out = append(out, beatFrame(t, 0.5, 0.7, 0.6, 0.75))
	}
	return out
}

func strobeFrames() []packFrame {
	var out []packFrame
	for t := 0.0; t <= 4000; t += 100 {
		out = append(out, beatFrame(t, 0.6, 0.95, 0.9, 0.8))
	}
	return out
}

func gapFrames() []packFrame {
	var out []packFrame
	for t := 0.0; t <= 60000; t += 250 {
		f := beatFrame(t, 0.5, 0.9, 0.7, 0.8)
		if t >= 25000 && t <= 30000 {
			f.Flags = []string{"live-analysis-unavailable"}
		}
		out = append(out, f)
	}
	return out
}

func btLatencyFrames() []packFrame {
	var out []packFrame
	for t := 0.0; t <= 60000; t += 250 {
		f := beatFrame(t, 0.5, 0.9, 0.7, 0.8)
		// Large Bluetooth-scale output latency: audible time trails media time.
		f.MediaTimeMS = t
		out = append(out, f)
	}
	return out
}

func cadenceFrames() []packFrame {
	var out []packFrame
	for t := 0.0; t <= 80000; t += 250 {
		f := beatFrame(t, 0.5, 0.6, 0.5, 0.8)
		// Aligned phrase boundary every 16s, landing within a tick.
		if int(t)%16000 == 0 && t > 0 {
			f.HasPhrase = true
			f.PhraseConf = 0.8
			f.PhraseToNextMS = 0
		}
		out = append(out, f)
	}
	return out
}
