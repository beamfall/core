package genassets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/beamfall/core/internal/store"
)

// Loudness spec (ADR-0098 area 3, amends ADR-0033 §2): the EBU R128 loudness of
// an audio-class Media item's primary file, computed once server-side and keyed
// by content fingerprint exactly like the waveform, silence map, and beat grid.
// It is generic audio-analysis data — a property of any audio's bytes, not a
// music-only schema column — so a podcast, audiobook, or music track all get one.
// The measurement is read from trusted ReplayGain/R128 container tags when
// present and otherwise computed by an ebur128 analysis pass; Source records which.
const loudnessVersion = 1

// LoudnessName is the wire vocabulary asset name for the loudness measurement —
// it joins the closed validAssetName set and the .json content-type branch in
// the serving route, exactly like the waveform envelope and silence map.
const LoudnessName = "loudness.json"

// loudnessReferenceLUFS is the normalization target the served gain drives toward
// (-18 LUFS, the ReplayGain 2.0 reference). GainDB = reference - integrated, so a
// client or transcoder applies a single dB offset to reach the reference.
const loudnessReferenceLUFS = -18.0

// loudnessGainClampDB bounds the served gain so a wildly mis-tagged or pathological
// source cannot drive an absurd offset; ±15 dB spans every realistic mastering
// level while never amplifying noise into clipping.
const loudnessGainClampDB = 15.0

// Source provenance values for a loudness measurement (closed vocabulary).
const (
	LoudnessSourceTags = "tags" // recovered from trusted ReplayGain/R128 tags
	LoudnessSourceR128 = "r128" // computed by an ebur128 analysis pass
)

// LoudnessAnalysis is the JSON document clients consume: the version they branch
// on, the measured integrated loudness / range / true peak, the reference the
// gain targets, the precomputed normalization GainDB, and the Source provenance.
// All loudness values are LUFS/LU/dBTP; GainDB is the dB offset to apply for
// normalization to ReferenceLUFS.
type LoudnessAnalysis struct {
	Version         int     `json:"version"`
	Source          string  `json:"source"`
	IntegratedLUFS  float64 `json:"integratedLUFS"`
	LoudnessRangeLU float64 `json:"loudnessRangeLU"`
	TruePeakDBTP    float64 `json:"truePeakDBTP"`
	ReferenceLUFS   float64 `json:"referenceLUFS"`
	GainDB          float64 `json:"gainDB"`
}

// LoudnessPath returns where a fingerprint's loudness measurement lives (whether
// or not it exists yet).
func (s *Service) LoudnessPath(fingerprint string) string {
	return filepath.Join(s.assetDir(fingerprint), LoudnessName)
}

// LoudnessAsset reports the loudness measurement name when one exists for the
// fingerprint (the normalization-capability availability seam).
func (s *Service) LoudnessAsset(fingerprint string) (string, bool) {
	if fingerprint == "" || !regularFileExists(s.LoudnessPath(fingerprint)) {
		return "", false
	}
	return LoudnessName, true
}

// LoudnessForFingerprint reads and unmarshals the stored loudness measurement for
// a fingerprint, returning ok=false when none exists or it cannot be read/parsed.
// This is the content seam (not just existence) playback reads to populate the
// rung-specific loudness/normalization state — playback never touches the on-disk
// path, only this parsed result.
func (s *Service) LoudnessForFingerprint(fingerprint string) (LoudnessAnalysis, bool) {
	if fingerprint == "" {
		return LoudnessAnalysis{}, false
	}
	data, err := os.ReadFile(s.LoudnessPath(fingerprint))
	if err != nil {
		return LoudnessAnalysis{}, false
	}
	var analysis LoudnessAnalysis
	if err := json.Unmarshal(data, &analysis); err != nil {
		return LoudnessAnalysis{}, false
	}
	return analysis, true
}

// GenerateLoudness produces the loudness measurement for one audio-class Media
// item's primary file. Like every other Generated Asset it is keyed by content
// fingerprint: a quality-upgrade replacement (new bytes → new fingerprint → new
// directory) regenerates, while a metadata edit (same bytes, same fingerprint,
// same directory) never does. The measurement reads trusted ReplayGain/R128 tags
// when present (Source "tags", no decode) and otherwise runs an ebur128 pass
// (Source "r128"). The document is written last, atomically — its existence is
// the "analysed" marker.
func (s *Service) GenerateLoudness(ctx context.Context, id store.MediaID) error {
	if s.gen == nil {
		return errors.New("no generator configured")
	}
	_, file, ok, err := s.audioSource(ctx, id)
	if err != nil || !ok {
		return err
	}
	dst := s.LoudnessPath(file.ContentFingerprint)
	if regularFileExists(dst) {
		return nil
	}
	integratedLUFS, rangeLU, truePeakDBTP, fromTags, err := s.gen.LoudnessMeasure(ctx, file.Path)
	if err != nil {
		return err
	}
	source := LoudnessSourceR128
	if fromTags {
		source = LoudnessSourceTags
	}
	analysis := LoudnessAnalysis{
		Version:         loudnessVersion,
		Source:          source,
		IntegratedLUFS:  integratedLUFS,
		LoudnessRangeLU: rangeLU,
		TruePeakDBTP:    truePeakDBTP,
		ReferenceLUFS:   loudnessReferenceLUFS,
		GainDB:          clampGain(loudnessReferenceLUFS - integratedLUFS),
	}
	data, err := json.Marshal(analysis)
	if err != nil {
		return err
	}
	if err := s.publishBytes(string(id), file.ContentFingerprint, "loudness:v1", LoudnessName, data); err != nil {
		return fmt.Errorf("publishing loudness for %s: %w", id, err)
	}
	return nil
}

// clampGain bounds a normalization gain to ±loudnessGainClampDB so a mis-tagged
// or pathological source cannot drive an absurd offset.
func clampGain(gain float64) float64 {
	if math.IsNaN(gain) {
		return 0
	}
	if gain > loudnessGainClampDB {
		return loudnessGainClampDB
	}
	if gain < -loudnessGainClampDB {
		return -loudnessGainClampDB
	}
	return gain
}
