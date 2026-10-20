package contract

import (
	"hash/fnv"
	"math"
	"sort"
)

const (
	DirectorContractVersion = "visual-director.v1"

	DefaultDirectorTickMS = 250

	CueAmbient    = "ambient"
	CueIntro      = "intro"
	CueGroove     = "groove"
	CueBuild      = "build"
	CueDrop       = "drop"
	CueBreak      = "break"
	CuePhraseTurn = "phraseTurn"
	CueFinale     = "finale"

	FallbackNone                    = ""
	FallbackLowTrustAmbient         = "low-trust-ambient"
	FallbackLiveAnalysisUnavailable = "live-analysis-unavailable"
	FallbackSafetyClamp             = "safety-clamp"
	FallbackHoldReleasedByClamp     = "hold-released-by-clamp"
	FallbackRuntimeFlashGuard       = "photosensitivity-runtime-guard"
)

var DirectorCues = []string{
	CueAmbient,
	CueIntro,
	CueGroove,
	CueBuild,
	CueDrop,
	CueBreak,
	CuePhraseTurn,
	CueFinale,
}

type DirectorInput struct {
	Frames           []AudioFrame
	DurationMS       float64
	TickMS           float64
	PlaybackRate     float64
	Seed             string
	UserIntensity    float64
	Profile          DirectorProfile
	ClampSources     []DirectorClamps
	Candidates       []DirectorCandidate
	HoldSelection    *DirectorSelection
	SmartPresetMin   float64
	SmartPresetMax   float64
	Photosensitivity *PhotosensitivityBounds
}

type DirectorProfile struct {
	Surface              string
	DefaultIntensity     float64
	TransitionAggression float64
	DwellScale           float64
	ReducedMotion        bool
	PhotosensitiveSafe   bool
	OverlaySafe          bool
	DarkRoom             bool
	OLEDIdleProtection   bool
	LowPerformanceBudget bool
}

type DirectorClamps struct {
	Name                  string  `json:"name"`
	MaxIntensity          float64 `json:"maxIntensity"`
	MaxFlashHz            float64 `json:"maxFlashHz"`
	MaxLuminanceDelta     float64 `json:"maxLuminanceDelta"`
	MaxFlashArea          float64 `json:"maxFlashArea"`
	MaxCameraVelocity     float64 `json:"maxCameraVelocity"`
	MaxZoom               float64 `json:"maxZoom"`
	MaxRotation           float64 `json:"maxRotation"`
	MaxParticleDensity    float64 `json:"maxParticleDensity"`
	RequiresReducedMotion bool    `json:"requiresReducedMotion"`
	RequiresOverlaySafe   bool    `json:"requiresOverlaySafe"`
}

type DirectorCandidate struct {
	VisualID           string
	SmartPresetID      string
	Tier               string
	OverlaySafe        bool
	ReducedMotionSafe  bool
	PhotosensitiveSafe bool
	PerformanceCost    int
	SupportsCrossfade  bool
}

type DirectorSelection struct {
	VisualID      string `json:"visualId"`
	SmartPresetID string `json:"smartPresetId"`
}

type DirectorTrace struct {
	ContractVersion      string            `json:"contractVersion"`
	Tick                 int               `json:"tick"`
	EffectiveMediaTimeMS float64           `json:"effectiveMediaTimeMs"`
	Cue                  string            `json:"cue"`
	LandingTick          int               `json:"landingTick"`
	Selection            DirectorSelection `json:"selection"`
	Transition           string            `json:"transition"`
	Alignment            string            `json:"alignment"`
	FallbackReason       string            `json:"fallbackReason,omitempty"`
	Intensity            float64           `json:"intensity"`
	IntensityClamp       float64           `json:"intensityClamp"`
	Clamp                DirectorClamps    `json:"clamp"`
	LadderSteps          []string          `json:"ladderSteps,omitempty"`
}

type directorState struct {
	beatTrusted    bool
	lastCue        string
	selection      DirectorSelection
	selectionAtMS  float64
	flashTimes     []float64
	usedSelections []DirectorSelection
}

func DefaultSurfaceProfile(surface string) DirectorProfile {
	switch surface {
	case "tvAmbient":
		return DirectorProfile{Surface: surface, DefaultIntensity: 0.55, TransitionAggression: 0.35, DwellScale: 1.5, OLEDIdleProtection: true}
	case "phoneNowPlaying":
		return DirectorProfile{Surface: surface, DefaultIntensity: 0.45, TransitionAggression: 0.25, DwellScale: 1.0, LowPerformanceBudget: true}
	default:
		return DirectorProfile{Surface: "web", DefaultIntensity: 0.5, TransitionAggression: 0.4, DwellScale: 1.0}
	}
}

func DefaultDirectorCandidates() []DirectorCandidate {
	return []DirectorCandidate{
		{VisualID: "liquid-optics", SmartPresetID: "liquid-optics-calm", Tier: "Lite", OverlaySafe: true, ReducedMotionSafe: true, PhotosensitiveSafe: true, PerformanceCost: 1, SupportsCrossfade: true},
		{VisualID: "volumetric-light", SmartPresetID: "volumetric-light-balanced", Tier: "Lite", OverlaySafe: true, ReducedMotionSafe: true, PhotosensitiveSafe: true, PerformanceCost: 1, SupportsCrossfade: true},
		{VisualID: "spectral-city", SmartPresetID: "spectral-city-kinetic", Tier: "Standard", OverlaySafe: true, ReducedMotionSafe: false, PhotosensitiveSafe: false, PerformanceCost: 2},
		{VisualID: "cymatics-prism", SmartPresetID: "cymatics-prism-build", Tier: "Standard", OverlaySafe: true, ReducedMotionSafe: false, PhotosensitiveSafe: false, PerformanceCost: 2},
		{VisualID: "event-horizon", SmartPresetID: "event-horizon-drop", Tier: "Flagship", OverlaySafe: false, ReducedMotionSafe: false, PhotosensitiveSafe: false, PerformanceCost: 3},
	}
}

func RunDirector(in DirectorInput) []DirectorTrace {
	if len(in.Frames) == 0 {
		return nil
	}
	tickMS := in.TickMS
	if tickMS <= 0 {
		tickMS = DefaultDirectorTickMS
	}
	if in.DurationMS <= 0 {
		in.DurationMS = in.Frames[len(in.Frames)-1].MediaTimeMS
	}
	if in.UserIntensity <= 0 {
		in.UserIntensity = 1
	}
	in.Profile = fillDirectorProfile(in.Profile)
	if in.PlaybackRate == 0 {
		in.PlaybackRate = 1
	}
	if in.SmartPresetMax <= 0 {
		in.SmartPresetMax = 1
	}
	if in.Photosensitivity != nil {
		in.Frames = ClampFlashSequence(in.Frames, *in.Photosensitivity)
		in.ClampSources = append(in.ClampSources, in.Photosensitivity.DirectorClamps())
	}
	candidates := in.Candidates
	if len(candidates) == 0 {
		candidates = DefaultDirectorCandidates()
	}
	clamp := ComposeDirectorClamps(append(profileClamps(in.Profile), in.ClampSources...)...)
	state := directorState{lastCue: CueGroove}
	frameIndex := 0
	ticks := int(math.Floor(in.DurationMS/tickMS)) + 1
	out := make([]DirectorTrace, 0, ticks)
	for tick := 0; tick < ticks; tick++ {
		now := float64(tick) * tickMS
		for frameIndex+1 < len(in.Frames) && in.Frames[frameIndex+1].MediaTimeMS <= now {
			frameIndex++
		}
		frame := in.Frames[frameIndex]
		cue, fallback := deriveCue(frame, now, in.DurationMS, &state)
		selection, released := selectDirectorCandidate(in, candidates, clamp, cue, now, state)
		if released {
			fallback = FallbackHoldReleasedByClamp
		}
		if selection != state.selection {
			state.selectionAtMS = now
		}
		state.selection = selection
		state.usedSelections = appendSelection(state.usedSelections, selection)
		directorIntensity := cueIntensity(cue, frame)
		intensity, intensityClamp := ComposeDirectorIntensity(in.UserIntensity, directorIntensity, in.SmartPresetMin, in.SmartPresetMax, clamp.MaxIntensity)
		steps := degradationSteps(in.Profile, clamp, candidates, selection)
		transition, alignment := transitionFor(cue, frame, clamp, candidates, selection)
		if flashGuardTrips(now, cue, frame, clamp, &state) {
			intensity = math.Min(intensity, math.Min(intensityClamp, 0.25))
			intensityClamp = math.Min(intensityClamp, 0.25)
			fallback = FallbackRuntimeFlashGuard
			steps = append(steps, "runtime-flash-guard")
			transition = "hold"
			alignment = "safety"
		}
		out = append(out, DirectorTrace{
			ContractVersion:      DirectorContractVersion,
			Tick:                 tick,
			EffectiveMediaTimeMS: round6(now),
			Cue:                  cue,
			LandingTick:          tick,
			Selection:            selection,
			Transition:           transition,
			Alignment:            alignment,
			FallbackReason:       fallback,
			Intensity:            round6(intensity),
			IntensityClamp:       round6(intensityClamp),
			Clamp:                clamp,
			LadderSteps:          steps,
		})
		state.lastCue = cue
	}
	return out
}

func ComposeDirectorClamps(clamps ...DirectorClamps) DirectorClamps {
	out := DirectorClamps{
		Name:               "composed",
		MaxIntensity:       1,
		MaxFlashHz:         3,
		MaxLuminanceDelta:  0.8,
		MaxFlashArea:       0.25,
		MaxCameraVelocity:  1,
		MaxZoom:            1,
		MaxRotation:        1,
		MaxParticleDensity: 1,
	}
	for _, clamp := range clamps {
		out.MaxIntensity = minPositive(out.MaxIntensity, clamp.MaxIntensity)
		out.MaxFlashHz = minPositive(out.MaxFlashHz, clamp.MaxFlashHz)
		out.MaxLuminanceDelta = minPositive(out.MaxLuminanceDelta, clamp.MaxLuminanceDelta)
		out.MaxFlashArea = minPositive(out.MaxFlashArea, clamp.MaxFlashArea)
		out.MaxCameraVelocity = minPositive(out.MaxCameraVelocity, clamp.MaxCameraVelocity)
		out.MaxZoom = minPositive(out.MaxZoom, clamp.MaxZoom)
		out.MaxRotation = minPositive(out.MaxRotation, clamp.MaxRotation)
		out.MaxParticleDensity = minPositive(out.MaxParticleDensity, clamp.MaxParticleDensity)
		out.RequiresReducedMotion = out.RequiresReducedMotion || clamp.RequiresReducedMotion
		out.RequiresOverlaySafe = out.RequiresOverlaySafe || clamp.RequiresOverlaySafe
	}
	return out
}

func ComposeDirectorIntensity(user, director, presetMin, presetMax, safetyMax float64) (float64, float64) {
	if presetMax <= 0 {
		presetMax = 1
	}
	if safetyMax <= 0 {
		safetyMax = 1
	}
	ceiling := math.Min(presetMax, safetyMax)
	v := math.Min(user, director)
	v = math.Max(presetMin, math.Min(ceiling, v))
	return v, ceiling
}

func deriveCue(frame AudioFrame, now, duration float64, state *directorState) (string, string) {
	fallback := FallbackNone
	liveUnavailable := hasFlag(frame.Flags, "live-analysis-unavailable")
	if liveUnavailable {
		return CueAmbient, FallbackLiveAnalysisUnavailable
	}
	confidence := 0.0
	if frame.Beat != nil {
		confidence = frame.Beat.Confidence
	}
	if state.beatTrusted {
		state.beatTrusted = confidence >= 0.40
	} else {
		state.beatTrusted = confidence >= 0.60
	}
	if !state.beatTrusted && (frame.RMS < 0.18 || frame.Onset.Broadband < 0.35) {
		return CueAmbient, FallbackLowTrustAmbient
	}
	if now <= math.Min(30000, duration*0.10) {
		return CueIntro, fallback
	}
	if duration > 0 && duration-now <= math.Min(30000, duration*0.10) {
		return CueFinale, fallback
	}
	if frame.Phrase != nil && frame.Phrase.Confidence >= 0.60 && frame.Phrase.TimeToNextMS <= DefaultDirectorTickMS {
		return CuePhraseTurn, fallback
	}
	if frame.RMS < 0.12 {
		return CueBreak, fallback
	}
	if state.beatTrusted && frame.Onset.Broadband >= 0.85 {
		return CueDrop, fallback
	}
	if state.beatTrusted && (frame.Onset.Broadband >= 0.55 || frame.Onset.High >= 0.65) {
		return CueBuild, fallback
	}
	return CueGroove, fallback
}

func selectDirectorCandidate(in DirectorInput, candidates []DirectorCandidate, clamp DirectorClamps, cue string, now float64, state directorState) (DirectorSelection, bool) {
	compatible := compatibleCandidates(candidates, in.Profile, clamp)
	if len(compatible) == 0 {
		return DirectorSelection{VisualID: "dark-room", SmartPresetID: "dark-room"}, false
	}
	if in.HoldSelection != nil {
		for _, c := range compatible {
			if c.VisualID == in.HoldSelection.VisualID && c.SmartPresetID == in.HoldSelection.SmartPresetID {
				return *in.HoldSelection, false
			}
		}
	}
	if state.selection.VisualID != "" && cue != CuePhraseTurn && cue != CueFinale {
		return state.selection, false
	}
	minDwellMS := 45000 * in.Profile.DwellScale
	maxDwellMS := 240000 * in.Profile.DwellScale
	if state.selection.VisualID != "" && now-state.selectionAtMS < minDwellMS {
		return state.selection, false
	}
	if state.selection.VisualID != "" && now-state.selectionAtMS < maxDwellMS && cue != CuePhraseTurn && cue != CueFinale {
		return state.selection, false
	}
	sort.Slice(compatible, func(i, j int) bool {
		a := compatible[i].VisualID + "/" + compatible[i].SmartPresetID
		b := compatible[j].VisualID + "/" + compatible[j].SmartPresetID
		return a < b
	})
	offset := int(hashDirector(in.Seed, cue, state.selection.VisualID) % uint64(len(compatible)))
	for i := range compatible {
		c := compatible[(offset+i)%len(compatible)]
		s := DirectorSelection{VisualID: c.VisualID, SmartPresetID: c.SmartPresetID}
		if !recentSelection(state.usedSelections, s, 3) {
			return s, in.HoldSelection != nil
		}
	}
	c := compatible[offset]
	return DirectorSelection{VisualID: c.VisualID, SmartPresetID: c.SmartPresetID}, in.HoldSelection != nil
}

func compatibleCandidates(candidates []DirectorCandidate, profile DirectorProfile, clamp DirectorClamps) []DirectorCandidate {
	out := make([]DirectorCandidate, 0, len(candidates))
	for _, c := range candidates {
		if (profile.OverlaySafe || clamp.RequiresOverlaySafe) && !c.OverlaySafe {
			continue
		}
		if (profile.ReducedMotion || clamp.RequiresReducedMotion) && !c.ReducedMotionSafe {
			continue
		}
		if profile.PhotosensitiveSafe && !c.PhotosensitiveSafe {
			continue
		}
		if profile.LowPerformanceBudget && c.PerformanceCost > 1 {
			continue
		}
		out = append(out, c)
	}
	return out
}

func transitionFor(cue string, frame AudioFrame, clamp DirectorClamps, candidates []DirectorCandidate, selection DirectorSelection) (string, string) {
	if cue == CueFinale || cue == CuePhraseTurn {
		if clamp.MaxIntensity <= 0.55 || !selectionSupportsCrossfade(candidates, selection) {
			return "cut", bestAlignment(frame)
		}
		return "crossfade", "phrase"
	}
	if cue == CueDrop || cue == CueBuild {
		return "cut", bestAlignment(frame)
	}
	return "none", "none"
}

func bestAlignment(frame AudioFrame) string {
	if frame.Phrase != nil && frame.Phrase.Confidence >= 0.60 {
		return "phrase"
	}
	if frame.Bar != nil && frame.Bar.Confidence >= 0.60 {
		return "bar"
	}
	if frame.Beat != nil && frame.Beat.Confidence >= 0.60 {
		return "beat"
	}
	return "tick"
}

func flashGuardTrips(now float64, cue string, frame AudioFrame, clamp DirectorClamps, state *directorState) bool {
	flash := frame.Onset.Broadband >= 0.80 || (cue == CueDrop && state.lastCue != CueDrop)
	if !flash {
		return false
	}
	cutoff := now - 1000
	kept := state.flashTimes[:0]
	for _, t := range state.flashTimes {
		if t >= cutoff {
			kept = append(kept, t)
		}
	}
	state.flashTimes = append(kept, now)
	ceiling := clamp.MaxFlashHz
	if ceiling <= 0 {
		ceiling = 3
	}
	return float64(len(state.flashTimes)) > ceiling || frame.Onset.Broadband > clamp.MaxLuminanceDelta
}

func cueIntensity(cue string, frame AudioFrame) float64 {
	base := map[string]float64{
		CueAmbient:    0.20,
		CueIntro:      0.35,
		CueGroove:     0.55,
		CueBuild:      0.75,
		CueDrop:       0.95,
		CueBreak:      0.25,
		CuePhraseTurn: 0.65,
		CueFinale:     0.70,
	}[cue]
	return clamp01(base + frame.Onset.Broadband*0.10)
}

func profileClamps(profile DirectorProfile) []DirectorClamps {
	var clamps []DirectorClamps
	if profile.ReducedMotion {
		clamps = append(clamps, DirectorClamps{Name: "reduced-motion", MaxIntensity: 0.55, MaxCameraVelocity: 0.25, MaxZoom: 0.25, MaxRotation: 0.15, RequiresReducedMotion: true})
	}
	if profile.PhotosensitiveSafe {
		clamps = append(clamps, DirectorClamps{Name: "photosensitive-safe", MaxIntensity: 0.50, MaxFlashHz: 1, MaxLuminanceDelta: 0.35, MaxFlashArea: 0.10})
	}
	if profile.OverlaySafe {
		clamps = append(clamps, DirectorClamps{Name: "overlay-safe", MaxIntensity: 0.70, MaxLuminanceDelta: 0.45, RequiresOverlaySafe: true})
	}
	if profile.DarkRoom {
		clamps = append(clamps, DirectorClamps{Name: "dark-room", MaxIntensity: 0.15, MaxFlashHz: 0.5, MaxLuminanceDelta: 0.10, MaxCameraVelocity: 0.05, MaxZoom: 0.05, MaxRotation: 0.05})
	}
	if profile.OLEDIdleProtection {
		clamps = append(clamps, DirectorClamps{Name: "oled-idle", MaxIntensity: 0.65, MaxLuminanceDelta: 0.55})
	}
	if profile.LowPerformanceBudget {
		clamps = append(clamps, DirectorClamps{Name: "performance", MaxIntensity: 0.75, MaxParticleDensity: 0.50})
	}
	return clamps
}

func fillDirectorProfile(profile DirectorProfile) DirectorProfile {
	defaults := DefaultSurfaceProfile(profile.Surface)
	if profile.Surface == "" {
		profile.Surface = defaults.Surface
	}
	if profile.DefaultIntensity <= 0 {
		profile.DefaultIntensity = defaults.DefaultIntensity
	}
	if profile.TransitionAggression <= 0 {
		profile.TransitionAggression = defaults.TransitionAggression
	}
	if profile.DwellScale <= 0 {
		profile.DwellScale = defaults.DwellScale
	}
	profile.OLEDIdleProtection = profile.OLEDIdleProtection || defaults.OLEDIdleProtection
	profile.LowPerformanceBudget = profile.LowPerformanceBudget || defaults.LowPerformanceBudget
	return profile
}

func degradationSteps(profile DirectorProfile, clamp DirectorClamps, candidates []DirectorCandidate, selection DirectorSelection) []string {
	var steps []string
	if profile.LowPerformanceBudget {
		steps = append(steps, "resolution-scale", "derived-texture-quality", "particle-count")
	}
	if clamp.MaxIntensity < 1 {
		steps = append(steps, "smart-preset-intensity-cap")
	}
	if selection.VisualID == "dark-room" {
		steps = append(steps, "lower-tier-visual", "dark-room")
	}
	return steps
}

func minPositive(a, b float64) float64 {
	if b <= 0 {
		return a
	}
	if a <= 0 || b < a {
		return b
	}
	return a
}

func appendSelection(in []DirectorSelection, s DirectorSelection) []DirectorSelection {
	if s.VisualID == "" {
		return in
	}
	if len(in) > 0 && in[len(in)-1] == s {
		return in
	}
	return append(in, s)
}

func recentSelection(in []DirectorSelection, s DirectorSelection, window int) bool {
	for i := len(in) - 1; i >= 0 && len(in)-i <= window; i-- {
		if in[i] == s {
			return true
		}
	}
	return false
}

func selectionSupportsCrossfade(candidates []DirectorCandidate, selection DirectorSelection) bool {
	for _, c := range candidates {
		if c.VisualID == selection.VisualID && c.SmartPresetID == selection.SmartPresetID {
			return c.SupportsCrossfade
		}
	}
	return false
}

func hashDirector(parts ...string) uint64 {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

func hasFlag(flags []string, want string) bool {
	for _, flag := range flags {
		if flag == want {
			return true
		}
	}
	return false
}
