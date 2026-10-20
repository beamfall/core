package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	visualsync "github.com/beamfall/core/tools/visualsync/contract"
)

// Photosensitivity release gate (NGAUDIT-M-PHOTOSENSITIVITY).
//
// This test is the release gate for Flagship visuals' photosensitivity safety,
// alongside ADR-0100's cross-surface accessibility gate. It enforces three
// contract properties over PEAT-style fixtures:
//
//  1. The manifest validator rejects an over-ceiling bounds declaration and
//     accepts a compliant one (pack-load / CI validation).
//  2. Flash analysis flags the over-ceiling frame sequence and passes the
//     compliant one.
//  3. The runtime clamp attenuates the over-ceiling sequence so its re-analysis
//     no longer exceeds the declared bounds (out-of-bounds frames are attenuated,
//     not rendered).

const photosensitivityDir = "testdata/visualsync/photosensitivity"

type flashFixture struct {
	Description string                  `json:"description"`
	Frames      []visualsync.AudioFrame `json:"frames"`
}

func loadBounds(t *testing.T, name string) visualsync.PhotosensitivityBounds {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(photosensitivityDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var b visualsync.PhotosensitivityBounds
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return b
}

func loadFlashFrames(t *testing.T, name string) []visualsync.AudioFrame {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(photosensitivityDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var f flashFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return f.Frames
}

func TestPhotosensitivityManifestValidator(t *testing.T) {
	compliant := loadBounds(t, "manifest-compliant.json")
	if err := visualsync.ValidatePhotosensitivityManifest(compliant); err != nil {
		t.Fatalf("compliant manifest rejected: %v", err)
	}

	overCeiling := loadBounds(t, "manifest-over-ceiling.json")
	if err := visualsync.ValidatePhotosensitivityManifest(overCeiling); err == nil {
		t.Fatal("over-ceiling manifest accepted, want rejection")
	}
}

func TestPhotosensitivityFlashAnalysis(t *testing.T) {
	bounds := loadBounds(t, "manifest-compliant.json")

	compliant := visualsync.AnalyzeFlashSequence(loadFlashFrames(t, "frames-compliant.json"))
	if compliant.Exceeds(bounds) {
		t.Fatalf("compliant sequence flagged: %#v", compliant)
	}

	over := visualsync.AnalyzeFlashSequence(loadFlashFrames(t, "frames-over-ceiling.json"))
	if !over.Exceeds(bounds) {
		t.Fatalf("over-ceiling sequence not flagged: %#v", over)
	}
	if !over.StrobeDetected {
		t.Fatalf("strobe not detected in over-ceiling sequence: %#v", over)
	}
}

func TestPhotosensitivityRuntimeClampAttenuates(t *testing.T) {
	bounds := loadBounds(t, "manifest-compliant.json")
	frames := loadFlashFrames(t, "frames-over-ceiling.json")
	originalFirst := frames[0].Onset.Broadband

	before := visualsync.AnalyzeFlashSequence(frames)
	if !before.Exceeds(bounds) {
		t.Fatalf("fixture is not out-of-bounds before clamp: %#v", before)
	}

	clamped := visualsync.ClampFlashSequence(frames, bounds)
	after := visualsync.AnalyzeFlashSequence(clamped)
	if after.Exceeds(bounds) {
		t.Fatalf("clamped sequence still exceeds bounds: %#v", after)
	}
	if after.PeakLuminanceDelta > bounds.MaxLuminanceDelta {
		t.Fatalf("clamp did not attenuate luminance: %v > %v", after.PeakLuminanceDelta, bounds.MaxLuminanceDelta)
	}

	// The clamp must attenuate, not drop: frame count is preserved.
	if len(clamped) != len(frames) {
		t.Fatalf("clamp changed frame count: %d != %d", len(clamped), len(frames))
	}
	// The input slice must not be mutated.
	if frames[0].Onset.Broadband != originalFirst {
		t.Fatalf("input frames mutated by clamp: %v != %v", frames[0].Onset.Broadband, originalFirst)
	}
}
