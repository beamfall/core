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

const (
	syncLanesVersion             = 1
	syncLanesBeatToleranceSecond = 0.05
)

// SyncLanesName is the wire vocabulary asset name for bar/phrase timing hints.
const SyncLanesName = "sync-lanes.json"

type SyncLanes struct {
	Version    int              `json:"version"`
	Meter      int              `json:"meter"`
	Confidence float64          `json:"confidence"`
	Bars       []SyncLaneBar    `json:"bars"`
	Phrases    []SyncLanePhrase `json:"phrases"`
}

type SyncLaneBar struct {
	Time       float64 `json:"time"`
	Confidence float64 `json:"confidence"`
}

type SyncLanePhrase struct {
	Time       float64 `json:"time"`
	Bars       int     `json:"bars"`
	Confidence float64 `json:"confidence"`
}

func (s *Service) SyncLanesPath(fingerprint string) string {
	return filepath.Join(s.assetDir(fingerprint), SyncLanesName)
}

func (s *Service) SyncLanesAsset(fingerprint string) (string, bool) {
	if fingerprint == "" || !regularFileExists(s.SyncLanesPath(fingerprint)) {
		return "", false
	}
	return SyncLanesName, true
}

func ParseSyncLanes(data []byte, grid BeatGrid) (SyncLanes, error) {
	var lanes SyncLanes
	if err := json.Unmarshal(data, &lanes); err != nil {
		return SyncLanes{}, err
	}
	if err := ValidateSyncLanes(lanes, grid); err != nil {
		return SyncLanes{}, err
	}
	return lanes, nil
}

func ValidateSyncLanes(lanes SyncLanes, grid BeatGrid) error {
	if lanes.Version != syncLanesVersion {
		return fmt.Errorf("sync lanes version %d, want %d", lanes.Version, syncLanesVersion)
	}
	if lanes.Meter <= 0 {
		return errors.New("sync lanes meter must be positive")
	}
	if !validConfidence(lanes.Confidence) {
		return errors.New("sync lanes confidence must be in [0,1]")
	}
	for i, bar := range lanes.Bars {
		if !validTime(bar.Time) {
			return fmt.Errorf("sync lanes bar %d has invalid time", i)
		}
		if !validConfidence(bar.Confidence) {
			return fmt.Errorf("sync lanes bar %d confidence must be in [0,1]", i)
		}
		if !landsOnBeat(bar.Time, grid.Beats, syncLanesBeatToleranceSecond) {
			return fmt.Errorf("sync lanes bar %d at %.3fs does not land on a beat", i, bar.Time)
		}
	}
	for i, phrase := range lanes.Phrases {
		if !validTime(phrase.Time) {
			return fmt.Errorf("sync lanes phrase %d has invalid time", i)
		}
		if phrase.Bars <= 0 {
			return fmt.Errorf("sync lanes phrase %d bars must be positive", i)
		}
		if !validConfidence(phrase.Confidence) {
			return fmt.Errorf("sync lanes phrase %d confidence must be in [0,1]", i)
		}
	}
	return nil
}

func (s *Service) GenerateSyncLanes(ctx context.Context, id store.MediaID) error {
	media, file, ok, err := s.audioSource(ctx, id)
	if err != nil || !ok {
		return err
	}
	if media.DurationSeconds <= 0 {
		return nil
	}
	dst := s.SyncLanesPath(file.ContentFingerprint)
	if regularFileExists(dst) {
		return nil
	}
	beatGridPath := s.BeatGridPath(file.ContentFingerprint)
	if !regularFileExists(beatGridPath) {
		if err := s.GenerateBeatGrid(ctx, id); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(beatGridPath)
	if err != nil {
		return nil
	}
	var grid BeatGrid
	if err := json.Unmarshal(data, &grid); err != nil {
		return nil
	}
	lanes := estimateSyncLanes(grid)
	data, err = json.Marshal(lanes)
	if err != nil {
		return err
	}
	if _, err := ParseSyncLanes(data, grid); err != nil {
		return err
	}
	if err := s.publishBytes(string(id), file.ContentFingerprint, "sync-lanes:v1", SyncLanesName, data); err != nil {
		return fmt.Errorf("publishing sync lanes for %s: %w", id, err)
	}
	return nil
}

func estimateSyncLanes(grid BeatGrid) SyncLanes {
	lanes := SyncLanes{
		Version:    syncLanesVersion,
		Meter:      4,
		Confidence: round3(clamp01(grid.Confidence * 0.7)),
		Bars:       []SyncLaneBar{},
		Phrases:    []SyncLanePhrase{},
	}
	for i, beat := range grid.Beats {
		if i%lanes.Meter != 0 {
			continue
		}
		lanes.Bars = append(lanes.Bars, SyncLaneBar{Time: beat, Confidence: lanes.Confidence})
		if len(lanes.Bars)%16 == 1 {
			lanes.Phrases = append(lanes.Phrases, SyncLanePhrase{Time: beat, Bars: 16, Confidence: lanes.Confidence})
		}
	}
	return lanes
}

func landsOnBeat(t float64, beats []float64, tolerance float64) bool {
	for _, beat := range beats {
		if math.Abs(t-beat) <= tolerance {
			return true
		}
	}
	return false
}

func validConfidence(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}

func validTime(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
