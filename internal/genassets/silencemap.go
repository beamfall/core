package genassets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/beamfall/core/internal/store"
)

// Silence-map spec (ADR-0052 §2.4(a), amends ADR-0033 §2): the quiet regions of
// an audio-class Media item's primary file, as a list of {start,end} spans in
// seconds. It is structurally identical to ADR-0048's video skip-segments — a
// list of time ranges a client seeks over — but derived from FFmpeg
// silencedetect rather than container shape, so a podcast player can skip the
// between-segment dead air computed once server-side. The detection
// threshold/duration are server tunables in the FFmpeg runner; the wire contract
// is only the span shape and this manifest version.
const silenceMapVersion = 1

// SilenceMapName is the wire vocabulary asset name for the silence map — the
// closed validAssetName set and the .json content-type branch in the serving
// route both cover it, exactly like the waveform envelope.
const SilenceMapName = "silence-map.json"

// SilenceSpan is one quiet region in seconds from the start of the stream. The
// {start,end} shape matches ADR-0048's video skip-segments deliberately, so a
// client's seek-over-spans logic is the same for both.
type SilenceSpan struct {
	StartSeconds float64 `json:"startSeconds"`
	EndSeconds   float64 `json:"endSeconds"`
}

// SilenceMap is the JSON document clients consume: the version they branch on
// and the ordered, non-overlapping silence spans. An audio file with no silence
// above the threshold yields a present manifest with an empty span list — the
// asset's existence is the "analysed" marker, distinct from "not yet analysed".
type SilenceMap struct {
	Version int           `json:"version"`
	Spans   []SilenceSpan `json:"spans"`
}

// SilenceMapPath returns where a fingerprint's silence map lives (whether or not
// it exists yet).
func (s *Service) SilenceMapPath(fingerprint string) string {
	return filepath.Join(s.assetDir(fingerprint), SilenceMapName)
}

// SilenceMapAsset reports the silence-map name when one exists for the
// fingerprint (the trim-silence availability seam).
func (s *Service) SilenceMapAsset(fingerprint string) (string, bool) {
	if fingerprint == "" || !regularFileExists(s.SilenceMapPath(fingerprint)) {
		return "", false
	}
	return SilenceMapName, true
}

// GenerateSilenceMap produces the silence map for one audio-class Media item's
// primary file. Like every other Generated Asset it is keyed by content
// fingerprint: a quality-upgrade replacement (new bytes → new fingerprint → new
// directory) regenerates, while a metadata edit (same bytes, same fingerprint,
// same directory) never does. The manifest is written last, atomically — it is
// the publication marker, so a present file with an empty span list means
// "analysed, no silence found", not "not yet analysed".
func (s *Service) GenerateSilenceMap(ctx context.Context, id store.MediaID) error {
	if s.gen == nil {
		return errors.New("no generator configured")
	}
	_, file, ok, err := s.audioSource(ctx, id)
	if err != nil || !ok {
		return err
	}
	dst := s.SilenceMapPath(file.ContentFingerprint)
	if regularFileExists(dst) {
		return nil
	}
	detected, err := s.gen.SilenceSpans(ctx, file.Path)
	if err != nil {
		return err
	}
	spans := make([]SilenceSpan, 0, len(detected))
	for _, span := range detected {
		spans = append(spans, SilenceSpan{StartSeconds: span[0], EndSeconds: span[1]})
	}
	data, err := json.Marshal(SilenceMap{Version: silenceMapVersion, Spans: spans})
	if err != nil {
		return err
	}
	if err := s.publishBytes(string(id), file.ContentFingerprint, "silence-map:v1", SilenceMapName, data); err != nil {
		return fmt.Errorf("publishing silence map for %s: %w", id, err)
	}
	return nil
}
