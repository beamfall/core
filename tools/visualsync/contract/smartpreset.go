package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

const (
	SmartPresetContractVersion = "smart-preset.v1"
	SmartPresetArtifactVersion = "1.0.0"
)

var (
	SmartPresetCues   = []string{"ambient", "intro", "groove", "build", "drop", "break", "phraseTurn", "finale"}
	SmartPresetInputs = []string{
		"audioFrame",
		"beatGrid",
		"syncLanes",
		"currentImage",
		"generatedAssets",
	}
	SmartPresetTargets    = []string{"param0", "param1", "param2", "palette", "motion", "camera", "overlay", "intensity"}
	SmartPresetCurveKinds = []string{"constant", "easing", "envelope", "lfo"}
	SmartPresetEasings    = []string{"linear", "easeIn", "easeOut", "easeInOut"}
	SmartPresetLFOShapes  = []string{"sine", "triangle", "square"}
	SmartPresetUnits      = []string{"seconds", "beats"}
)

type SmartPresetManifest struct {
	ContractVersion        string                   `json:"contractVersion"`
	ID                     string                   `json:"id"`
	VisualID               string                   `json:"visualId"`
	Defaults               map[string]float64       `json:"defaults"`
	RequiredInputs         []InputRequirement       `json:"requiredInputs"`
	IntensityBounds        Bounds                   `json:"intensityBounds"`
	CueResponses           map[string][]CueResponse `json:"cueResponses"`
	Palette                ChannelBehavior          `json:"palette"`
	Motion                 ChannelBehavior          `json:"motion"`
	Camera                 ChannelBehavior          `json:"camera"`
	Overlay                OverlayBehavior          `json:"overlay"`
	ReducedMotion          ReducedMotionBehavior    `json:"reducedMotion"`
	Performance            PerformanceRequirement   `json:"performance"`
	CapabilityRequirements []string                 `json:"capabilityRequirements,omitempty"`
}

type InputRequirement struct {
	Input    string `json:"input"`
	Fallback string `json:"fallback"`
}

type Bounds struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type CueResponse struct {
	Target string `json:"target"`
	Curve  Curve  `json:"curve"`
}

type ChannelBehavior struct {
	Mode string `json:"mode"`
}

type OverlayBehavior struct {
	Allowed bool   `json:"allowed"`
	Blend   string `json:"blend"`
}

type ReducedMotionBehavior struct {
	Mode     string `json:"mode"`
	Fallback string `json:"fallback"`
}

type PerformanceRequirement struct {
	MinTier          string `json:"minTier"`
	MaxPasses        int    `json:"maxPasses"`
	TextureBudgetMiB int    `json:"textureBudgetMiB"`
}

type Curve struct {
	Kind             string   `json:"kind"`
	Value            *float64 `json:"value,omitempty"`
	From             *float64 `json:"from,omitempty"`
	To               *float64 `json:"to,omitempty"`
	Duration         float64  `json:"duration,omitempty"`
	Unit             string   `json:"unit,omitempty"`
	Easing           string   `json:"easing,omitempty"`
	Attack           float64  `json:"attack,omitempty"`
	Release          float64  `json:"release,omitempty"`
	ReleaseAt        float64  `json:"releaseAt,omitempty"`
	Shape            string   `json:"shape,omitempty"`
	Period           float64  `json:"period,omitempty"`
	Phase            float64  `json:"phase,omitempty"`
	Min              *float64 `json:"min,omitempty"`
	Max              *float64 `json:"max,omitempty"`
	Wrap             bool     `json:"wrap,omitempty"`
	SmoothingSeconds float64  `json:"smoothingSeconds,omitempty"`
}

type CurveInput struct {
	ElapsedSeconds float64 `json:"elapsedSeconds,omitempty"`
	ElapsedBeats   float64 `json:"elapsedBeats,omitempty"`
	BeatPhase      float64 `json:"beatPhase,omitempty"`
	DeltaSeconds   float64 `json:"deltaSeconds,omitempty"`
	PreviousValue  float64 `json:"previousValue,omitempty"`
	HasPrevious    bool    `json:"hasPrevious,omitempty"`
}

func DecodeSmartPresetManifest(data []byte) (SmartPresetManifest, error) {
	var manifest SmartPresetManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return SmartPresetManifest{}, err
	}
	if err := ValidateSmartPresetManifest(manifest); err != nil {
		return SmartPresetManifest{}, err
	}
	return manifest, nil
}

func ValidateSmartPresetManifest(manifest SmartPresetManifest) error {
	if manifest.ContractVersion != SmartPresetContractVersion {
		return fmt.Errorf("unsupported SmartPreset contract version %q", manifest.ContractVersion)
	}
	if manifest.ID == "" || manifest.VisualID == "" {
		return errors.New("id and visualId are required")
	}
	if err := validateDefaults(manifest.Defaults); err != nil {
		return err
	}
	if manifest.IntensityBounds.Min < 0 || manifest.IntensityBounds.Max > 1 || manifest.IntensityBounds.Min > manifest.IntensityBounds.Max {
		return errors.New("intensityBounds must be ordered within [0,1]")
	}
	for _, req := range manifest.RequiredInputs {
		if !known(SmartPresetInputs, req.Input) {
			return fmt.Errorf("unknown input %q", req.Input)
		}
		if req.Fallback == "" {
			return fmt.Errorf("input %q missing fallback", req.Input)
		}
	}
	for cue, responses := range manifest.CueResponses {
		if !known(SmartPresetCues, cue) {
			return fmt.Errorf("unknown cue %q", cue)
		}
		for _, response := range responses {
			if !known(SmartPresetTargets, response.Target) {
				return fmt.Errorf("unknown response target %q", response.Target)
			}
			if err := validateCurve(response.Curve); err != nil {
				return fmt.Errorf("%s.%s: %w", cue, response.Target, err)
			}
		}
	}
	if manifest.ReducedMotion.Mode == "" || manifest.ReducedMotion.Fallback == "" {
		return errors.New("reducedMotion mode and fallback are required")
	}
	if manifest.Performance.MinTier == "" || manifest.Performance.MaxPasses <= 0 || manifest.Performance.TextureBudgetMiB <= 0 {
		return errors.New("performance minTier, maxPasses, and textureBudgetMiB are required")
	}
	return nil
}

func EvaluateCurve(curve Curve, in CurveInput) (float64, error) {
	if err := validateCurve(curve); err != nil {
		return 0, err
	}
	var v float64
	switch curve.Kind {
	case "constant":
		v = *curve.Value
	case "easing":
		progress := clamp01(elapsed(in, curve.Unit) / curve.Duration)
		v = *curve.From + (*curve.To-*curve.From)*ease(curve.Easing, progress)
	case "envelope":
		peak := 1.0
		if curve.Value != nil {
			peak = *curve.Value
		}
		t := elapsed(in, curve.Unit)
		switch {
		case curve.Attack > 0 && t < curve.Attack:
			v = peak * t / curve.Attack
		case curve.ReleaseAt > 0 && t > curve.ReleaseAt:
			v = peak * math.Max(0, 1-(t-curve.ReleaseAt)/curve.Release)
		default:
			v = peak
		}
	case "lfo":
		cycles := elapsed(in, curve.Unit)/curve.Period + curve.Phase
		if curve.Unit == "beats" {
			cycles += in.BeatPhase
		}
		wave := lfo(curve.Shape, cycles)
		v = *curve.Min + (*curve.Max-*curve.Min)*wave
	}
	return postProcess(curve, v, in), nil
}

func validateDefaults(defaults map[string]float64) error {
	if len(defaults) != 3 {
		return errors.New("defaults must declare param0, param1, and param2")
	}
	for _, param := range []string{"param0", "param1", "param2"} {
		if _, ok := defaults[param]; !ok {
			return fmt.Errorf("missing default %s", param)
		}
	}
	for param := range defaults {
		if !known([]string{"param0", "param1", "param2"}, param) {
			return fmt.Errorf("unknown default %q", param)
		}
	}
	return nil
}

func validateCurve(curve Curve) error {
	if !known(SmartPresetCurveKinds, curve.Kind) {
		return fmt.Errorf("unknown curve kind %q", curve.Kind)
	}
	if curve.Unit != "" && !known(SmartPresetUnits, curve.Unit) {
		return fmt.Errorf("unknown curve unit %q", curve.Unit)
	}
	if curve.SmoothingSeconds < 0 {
		return errors.New("smoothingSeconds must be non-negative")
	}
	if curve.Min != nil && curve.Max != nil && *curve.Min > *curve.Max {
		return errors.New("min must be <= max")
	}
	switch curve.Kind {
	case "constant":
		if curve.Value == nil {
			return errors.New("constant curve requires value")
		}
	case "easing":
		if curve.From == nil || curve.To == nil || curve.Duration <= 0 || !known(SmartPresetEasings, curve.Easing) || curve.Unit == "" {
			return errors.New("easing curve requires from, to, duration, unit, and easing")
		}
	case "envelope":
		if curve.Attack < 0 || curve.Release <= 0 || curve.Unit == "" {
			return errors.New("envelope curve requires non-negative attack, positive release, and unit")
		}
	case "lfo":
		if curve.Period <= 0 || curve.Min == nil || curve.Max == nil || curve.Unit == "" || !known(SmartPresetLFOShapes, curve.Shape) {
			return errors.New("lfo curve requires shape, period, unit, min, and max")
		}
	}
	return nil
}

func elapsed(in CurveInput, unit string) float64 {
	if unit == "beats" {
		return in.ElapsedBeats
	}
	return in.ElapsedSeconds
}

func ease(kind string, t float64) float64 {
	switch kind {
	case "easeIn":
		return t * t
	case "easeOut":
		return 1 - (1-t)*(1-t)
	case "easeInOut":
		if t < 0.5 {
			return 2 * t * t
		}
		return 1 - math.Pow(-2*t+2, 2)/2
	default:
		return t
	}
}

func lfo(shape string, cycles float64) float64 {
	phase := cycles - math.Floor(cycles)
	switch shape {
	case "triangle":
		return 1 - math.Abs(2*phase-1)
	case "square":
		if phase < 0.5 {
			return 1
		}
		return 0
	default:
		return (math.Sin(2*math.Pi*phase) + 1) / 2
	}
}

func postProcess(curve Curve, v float64, in CurveInput) float64 {
	if curve.Min != nil && curve.Max != nil {
		if curve.Wrap && *curve.Max > *curve.Min {
			span := *curve.Max - *curve.Min
			v = math.Mod(v-*curve.Min, span)
			if v < 0 {
				v += span
			}
			v += *curve.Min
		} else {
			v = math.Max(*curve.Min, math.Min(*curve.Max, v))
		}
	}
	if curve.SmoothingSeconds > 0 && in.HasPrevious && in.DeltaSeconds > 0 {
		alpha := 1 - math.Exp(-in.DeltaSeconds/curve.SmoothingSeconds)
		v = in.PreviousValue + alpha*(v-in.PreviousValue)
	}
	return round6(v)
}

func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

func known(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
