package contract

// Visual-sync conformance pack.
//
// This is the producer-side behavior-matrix conformance pack for the reference
// Visual Director (RunDirector) and reference SmartPreset curve evaluator
// (EvaluateCurve). It pins, for each matrix row, a tick-indexed AudioFrame
// input plus surface profile / clamps / candidates / seed, the expected
// DirectorTrace the reference implementation must reproduce, and the per-tick
// SmartPreset evaluator samples the reference evaluator must reproduce.
//
// Authoritative contract: docs/architecture/visual-runtime.md ("Visual Director
// v1 contract", accepted). ADR-0139 is a proposed draft and is cited only for
// matrix breadth, never as a binding constraint. Fixtures live at the
// ticket-pinned path internal/conformance/testdata/visual-director/; this test
// lives in the contract package (the ticket's allowed test location) and reaches
// the fixture dir via a runtime.Caller-relative walk to the repo root.
//
// The reference implementations (director.go, smartpreset.go) are the FROZEN
// oracle: this pack never edits them. Rows described as "toggled mid-playback"
// are encoded as before/after fixture PAIRS of single runs, because RunDirector
// composes profile/clamps once before the tick loop and has no per-tick profile
// mutation.
//
// Regenerate fixtures from the live reference implementation with:
//
//	BEAMFALL_UPDATE_VISUAL_DIRECTOR_PACK=1 go test ./tools/visualsync/contract -run VisualDirectorPack

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const (
	visualDirectorPackVersion = "visual-director-pack.v1"
	updateVisualDirectorEnv   = "BEAMFALL_UPDATE_VISUAL_DIRECTOR_PACK"
	injectDriftEnv            = "BEAMFALL_VISUAL_DIRECTOR_INJECT_DRIFT"
)

// packFixture is the on-disk fixture shape. DirectorInput/DirectorProfile/
// DirectorCandidate lack JSON tags, so the pack carries its own tagged DTOs and
// maps them onto the contract structs. DirectorTrace is already fully tagged and
// is pinned verbatim.
type packFixture struct {
	Version         string          `json:"version"`
	Name            string          `json:"name"`
	MatrixRow       string          `json:"matrixRow"`
	Notes           string          `json:"notes,omitempty"`
	Input           packInput       `json:"input"`
	ExpectedTrace   []DirectorTrace `json:"expectedTrace"`
	SmartPresetEval packEval        `json:"smartPresetEval"`
}

type packInput struct {
	TickMS           float64                 `json:"tickMs"`
	DurationMS       float64                 `json:"durationMs"`
	Seed             string                  `json:"seed"`
	UserIntensity    float64                 `json:"userIntensity"`
	PlaybackRate     float64                 `json:"playbackRate"`
	SmartPresetMin   float64                 `json:"smartPresetMin"`
	SmartPresetMax   float64                 `json:"smartPresetMax"`
	Profile          packProfile             `json:"profile"`
	ClampSources     []DirectorClamps        `json:"clampSources,omitempty"`
	Candidates       []packCandidate         `json:"candidates,omitempty"`
	HoldSelection    *DirectorSelection      `json:"holdSelection,omitempty"`
	Photosensitivity *PhotosensitivityBounds `json:"photosensitivity,omitempty"`
	Frames           []packFrame             `json:"frames"`
}

type packProfile struct {
	Surface              string  `json:"surface"`
	DefaultIntensity     float64 `json:"defaultIntensity,omitempty"`
	TransitionAggression float64 `json:"transitionAggression,omitempty"`
	DwellScale           float64 `json:"dwellScale,omitempty"`
	ReducedMotion        bool    `json:"reducedMotion,omitempty"`
	PhotosensitiveSafe   bool    `json:"photosensitiveSafe,omitempty"`
	OverlaySafe          bool    `json:"overlaySafe,omitempty"`
	DarkRoom             bool    `json:"darkRoom,omitempty"`
	OLEDIdleProtection   bool    `json:"oledIdleProtection,omitempty"`
	LowPerformanceBudget bool    `json:"lowPerformanceBudget,omitempty"`
}

type packCandidate struct {
	VisualID           string `json:"visualId"`
	SmartPresetID      string `json:"smartPresetId"`
	Tier               string `json:"tier"`
	OverlaySafe        bool   `json:"overlaySafe"`
	ReducedMotionSafe  bool   `json:"reducedMotionSafe"`
	PhotosensitiveSafe bool   `json:"photosensitiveSafe"`
	PerformanceCost    int    `json:"performanceCost"`
	SupportsCrossfade  bool   `json:"supportsCrossfade"`
}

// packFrame is the tick-indexed AudioFrame input, carrying only the fields the
// reference Director actually reads (RMS, Onset broadband/high, Beat/Bar/Phrase
// confidence + time-to-next, media time, and the single Flag
// "live-analysis-unavailable"). Beatgrid / sync-lane matrix rows are encoded
// through these read fields, never through arbitrary unread flags.
type packFrame struct {
	MediaTimeMS    float64  `json:"mediaTimeMs"`
	RMS            float64  `json:"rms"`
	OnsetBroadband float64  `json:"onsetBroadband"`
	OnsetHigh      float64  `json:"onsetHigh"`
	BeatConfidence float64  `json:"beatConfidence,omitempty"`
	BarConfidence  float64  `json:"barConfidence,omitempty"`
	PhraseConf     float64  `json:"phraseConfidence,omitempty"`
	PhraseToNextMS float64  `json:"phraseTimeToNextMs,omitempty"`
	HasBeat        bool     `json:"hasBeat"`
	HasBar         bool     `json:"hasBar"`
	HasPhrase      bool     `json:"hasPhrase"`
	Flags          []string `json:"flags,omitempty"`
}

// packEval pins the per-tick SmartPreset evaluator samples. Every matrix fixture
// carries evaluator samples (mandatory) covering param0..2 plus hint channels,
// across multiple tick-indexed CurveInputs, so two platforms cannot ease the same
// parameter differently while passing the Director traces.
type packEval struct {
	PresetID string       `json:"presetId"`
	VisualID string       `json:"visualId"`
	Samples  []packSample `json:"samples"`
}

type packSample struct {
	Target string     `json:"target"`
	Curve  Curve      `json:"curve"`
	Input  CurveInput `json:"input"`
	Want   float64    `json:"want"`
}

func (p packInput) toDirectorInput() DirectorInput {
	in := DirectorInput{
		TickMS:           p.TickMS,
		DurationMS:       p.DurationMS,
		Seed:             p.Seed,
		UserIntensity:    p.UserIntensity,
		PlaybackRate:     p.PlaybackRate,
		SmartPresetMin:   p.SmartPresetMin,
		SmartPresetMax:   p.SmartPresetMax,
		Profile:          p.Profile.toProfile(),
		ClampSources:     p.ClampSources,
		HoldSelection:    p.HoldSelection,
		Photosensitivity: p.Photosensitivity,
	}
	for _, c := range p.Candidates {
		in.Candidates = append(in.Candidates, DirectorCandidate(c))
	}
	for _, f := range p.Frames {
		in.Frames = append(in.Frames, f.toFrame())
	}
	return in
}

func (p packProfile) toProfile() DirectorProfile {
	return DirectorProfile{
		Surface:              p.Surface,
		DefaultIntensity:     p.DefaultIntensity,
		TransitionAggression: p.TransitionAggression,
		DwellScale:           p.DwellScale,
		ReducedMotion:        p.ReducedMotion,
		PhotosensitiveSafe:   p.PhotosensitiveSafe,
		OverlaySafe:          p.OverlaySafe,
		DarkRoom:             p.DarkRoom,
		OLEDIdleProtection:   p.OLEDIdleProtection,
		LowPerformanceBudget: p.LowPerformanceBudget,
	}
}

func (f packFrame) toFrame() AudioFrame {
	frame := AudioFrame{
		ContractVersion: ContractVersion,
		MediaTimeMS:     f.MediaTimeMS,
		AudibleTimeMS:   f.MediaTimeMS,
		RMS:             f.RMS,
		Onset:           Onset{Broadband: f.OnsetBroadband, High: f.OnsetHigh},
		Flags:           f.Flags,
	}
	if f.HasBeat {
		frame.Beat = &Phase{Confidence: f.BeatConfidence, TimeToNextMS: 250}
	}
	if f.HasBar {
		frame.Bar = &Phase{Confidence: f.BarConfidence, TimeToNextMS: 1000}
	}
	if f.HasPhrase {
		frame.Phrase = &Phase{Confidence: f.PhraseConf, TimeToNextMS: f.PhraseToNextMS}
	}
	return frame
}

// TestVisualDirectorPack verifies the reference Director + evaluator reproduce
// every committed fixture. Under the update env var it (re)generates the fixture
// files from the live reference implementation.
func TestVisualDirectorPack(t *testing.T) {
	dir := packDir(t)
	fixtures := buildPackFixtures()

	if os.Getenv(updateVisualDirectorEnv) == "1" {
		writePackFixtures(t, dir, fixtures)
	}

	names := packFixtureFiles(t, dir)
	if len(names) != len(fixtures) {
		t.Fatalf("committed pack has %d fixtures, generator has %d — regenerate with %s=1",
			len(names), len(fixtures), updateVisualDirectorEnv)
	}

	inject := os.Getenv(injectDriftEnv) == "1"
	for _, f := range fixtures {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			committed := readPackFixture(t, filepath.Join(dir, f.Name+".fixture.json"))
			assertFixtureMetadata(t, committed, f)
			if err := validatePackFixture(committed); err != nil {
				t.Fatalf("%s failed pack-load validation: %v", f.Name, err)
			}

			live := runFixture(f)
			if inject {
				live = injectTraceDrift(live)
			}
			if !reflect.DeepEqual(committed.ExpectedTrace, live) {
				t.Fatalf("%s Director trace drifted from reference; regenerate with %s=1\nfirst diff:\n%s",
					f.Name, updateVisualDirectorEnv, firstTraceDiff(committed.ExpectedTrace, live))
			}

			if len(committed.SmartPresetEval.Samples) == 0 {
				t.Fatalf("%s has no SmartPreset evaluator samples — every fixture must pin per-tick evaluator params", f.Name)
			}
			assertEvalSamples(t, committed, inject)
		})
	}
}

func TestVisualDirectorPackRejectsInvalidPhotosensitivityManifest(t *testing.T) {
	f := packFixture{
		Name: "invalid-photosensitivity",
		Input: packInput{Photosensitivity: &PhotosensitivityBounds{
			MaxFlashHz: 8, MaxLuminanceDelta: 0.95, MaxFlashArea: 0.5,
		}},
	}
	if err := validatePackFixture(f); err == nil {
		t.Fatal("pack-load validation accepted invalid photosensitivity bounds")
	}
}

func TestVisualDirectorPackRejectsMissingPhotosensitivityManifest(t *testing.T) {
	if err := validatePackFixture(packFixture{Name: "missing-photosensitivity"}); err == nil {
		t.Fatal("pack-load validation accepted missing photosensitivity bounds")
	}
}

// TestVisualDirectorPackRedProof is the RED-proving fixture (ADR-0121 §2,
// ADR-0122 injected-drift rule). It re-invokes the REAL pack test in a child
// `go test` process with drift injected into the live trace/eval output, and
// asserts the child gate EXITS NONZERO. This proves the conformance gate itself
// fails on injected implementation drift — not merely that a helper could return
// false. Without the injection env, the same test passes (proving the gate is
// green on a faithful implementation).
func TestVisualDirectorPackRedProof(t *testing.T) {
	if os.Getenv(injectDriftEnv) == "1" {
		t.Skip("child run: injection handled inside TestVisualDirectorPack")
	}
	cmd := exec.Command("go", "test", "-run", "TestVisualDirectorPack$", "-count=1", ".")
	cmd.Env = append(os.Environ(), injectDriftEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("RED proof failed: pack gate PASSED with injected drift — the gate cannot fail.\n%s", out)
	}
	if !strings.Contains(string(out), "drifted") && !strings.Contains(string(out), "FAIL") {
		t.Fatalf("RED proof: child failed but not via the drift assertion:\n%s", out)
	}
}

// injectTraceDrift stands in for a divergent platform implementation: it shifts
// one cue label and one landing tick beyond the pinned trace. Applied only in
// the injected-drift child run.
func injectTraceDrift(trace []DirectorTrace) []DirectorTrace {
	out := make([]DirectorTrace, len(trace))
	copy(out, trace)
	if len(out) > 0 {
		out[0].Cue = "DRIFTED-CUE"
		out[0].LandingTick += 7
	}
	return out
}

func assertEvalSamples(t *testing.T, f packFixture, inject bool) {
	t.Helper()
	for i, s := range f.SmartPresetEval.Samples {
		got, err := EvaluateCurve(s.Curve, s.Input)
		if err != nil {
			t.Fatalf("%s eval sample %d (%s) invalid curve: %v", f.Name, i, s.Target, err)
		}
		if inject && i == 0 {
			got += 0.5 // stand-in for a divergent evaluator implementation
		}
		if got != s.Want {
			t.Fatalf("%s eval sample %d (%s): EvaluateCurve = %v, want %v", f.Name, i, s.Target, got, s.Want)
		}
	}
}

func runFixture(f packFixture) []DirectorTrace {
	return RunDirector(f.Input.toDirectorInput())
}

func validatePackFixture(f packFixture) error {
	if f.Input.Photosensitivity == nil {
		return fmt.Errorf("%s: missing photosensitivity bounds", f.Name)
	}
	return ValidatePhotosensitivityManifest(*f.Input.Photosensitivity)
}

func assertFixtureMetadata(t *testing.T, committed, gen packFixture) {
	t.Helper()
	if committed.Version != visualDirectorPackVersion {
		t.Fatalf("%s version = %q, want %q", gen.Name, committed.Version, visualDirectorPackVersion)
	}
	if committed.Name != gen.Name || committed.MatrixRow != gen.MatrixRow {
		t.Fatalf("%s metadata drifted: committed name/row = %q/%q, generator = %q/%q",
			gen.Name, committed.Name, committed.MatrixRow, gen.Name, gen.MatrixRow)
	}
}

func firstTraceDiff(want, got []DirectorTrace) string {
	n := len(want)
	if len(got) < n {
		n = len(got)
	}
	for i := 0; i < n; i++ {
		if !reflect.DeepEqual(want[i], got[i]) {
			return fmt.Sprintf("tick %d:\n want=%#v\n got =%#v", i, want[i], got[i])
		}
	}
	if len(want) != len(got) {
		return fmt.Sprintf("length: want %d ticks, got %d", len(want), len(got))
	}
	return "(no element diff)"
}

func writePackFixtures(t *testing.T, dir string, fixtures []packFixture) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		f.ExpectedTrace = runFixture(f)
		data, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, '\n')
		if err := os.WriteFile(filepath.Join(dir, f.Name+".fixture.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readPackFixture(t *testing.T, path string) packFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v — regenerate with %s=1", path, err, updateVisualDirectorEnv)
	}
	var f packFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return f
}

func packFixtureFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read pack dir %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".fixture.json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// packDir walks from this source file to the repo root and returns the
// ticket-pinned fixture directory under internal/conformance/testdata.
func packDir(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// self = <root>/tools/visualsync/contract/visual_director_pack_test.go
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(self))))
	return filepath.Join(root, "internal", "conformance", "testdata", "visual-director")
}
