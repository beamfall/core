package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const (
	AudioAdFeatureSetVersion    = "audio-ad-audio-features-v1"
	AudioAdFeatureWindowSeconds = 1.0
	audioAdSampleRate           = 8192
	audioAdSilenceRMSDBFS       = -120.0
	audioAdTimestampEpsilon     = 1e-4
)

const (
	audioAdRMSKey      = "lavfi.astats.Overall.RMS_level"
	audioAdCentroidKey = "lavfi.aspectralstats.1.centroid"
	audioAdFlatnessKey = "lavfi.aspectralstats.1.flatness"
	audioAdRolloffKey  = "lavfi.aspectralstats.1.rolloff"
)

// AudioAdFeatureFrame is one fixed-width, household-local audio analysis row.
type AudioAdFeatureFrame struct {
	StartSeconds       float64
	RMSDBFS            float64
	SpectralCentroidHz float64
	SpectralFlatness   float64
	SpectralRolloffHz  float64
}

// AudioAdFeatureSet identifies the exact feature layout consumed by a model.
type AudioAdFeatureSet struct {
	Version       string
	WindowSeconds float64
	Frames        []AudioAdFeatureFrame
}

// AudioAdFeatures extracts at most maxWindows one-second feature frames. FFmpeg
// runs through the generation confinement seam and the null muxer; only text
// metadata is captured and no media derivative is written.
//
//io:ignore ffmpeg-audio-ad-features — foundation seam; PODAD-2-JOB-1 owns the lifecycle journey.
func (r *Runner) AudioAdFeatures(ctx context.Context, src string, maxWindows int) (AudioAdFeatureSet, error) {
	if err := checkPath(src); err != nil {
		return AudioAdFeatureSet{}, err
	}
	if maxWindows <= 0 {
		return AudioAdFeatureSet{}, errors.New("ffmpeg audio ad features: max windows must be positive")
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return AudioAdFeatureSet{}, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	filter := fmt.Sprintf(
		"aformat=channel_layouts=mono:sample_rates=%d,"+
			"asetnsamples=n=%d:p=1,"+
			"astats=metadata=1:reset=1:measure_perchannel=none:measure_overall=RMS_level,"+
			"aspectralstats=win_size=%d:overlap=0:measure=centroid+flatness+rolloff,"+
			"ametadata=print:file=-",
		audioAdSampleRate, audioAdSampleRate, audioAdSampleRate,
	)
	cmd := r.generationCommand(ctx, readConfinement(src), ffmpegPath,
		"-v", "error",
		"-t", strconv.Itoa(maxWindows),
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-map", "0:a:0",
		"-af", filter,
		"-f", "null",
		"--", os.DevNull,
	)
	stdout, stderr, err := r.run(ctx, cmd)
	if err != nil {
		return AudioAdFeatureSet{}, fmt.Errorf("ffmpeg audio ad analysis: %s", captureDetail(stdout, stderr, err))
	}
	features, err := parseAudioAdFeatureMetadata(string(stdout)+string(stderr), maxWindows)
	if err != nil {
		return AudioAdFeatureSet{}, err
	}
	if len(features.Frames) == 0 {
		return AudioAdFeatureSet{}, errors.New("ffmpeg audio ad analysis produced no frames")
	}
	return features, nil
}

type audioAdMetadataFrame struct {
	index int
	start float64
	raw   [4]string
	seen  [4]bool
}

func parseAudioAdFeatureMetadata(out string, maxWindows int) (AudioAdFeatureSet, error) {
	set := AudioAdFeatureSet{
		Version:       AudioAdFeatureSetVersion,
		WindowSeconds: AudioAdFeatureWindowSeconds,
	}
	var current *audioAdMetadataFrame
	for _, rawLine := range strings.Split(out, "\n") {
		line := strings.TrimSpace(rawLine)
		index, start, header, err := parseAudioAdFrameHeader(line)
		if err != nil {
			return AudioAdFeatureSet{}, err
		}
		if header {
			if current != nil {
				if err := appendAudioAdFrame(&set, current, maxWindows); err != nil {
					return AudioAdFeatureSet{}, err
				}
			}
			current = &audioAdMetadataFrame{index: index, start: start}
			continue
		}
		metric, value, known := parseAudioAdMetric(line)
		if !known {
			continue
		}
		if current == nil {
			return AudioAdFeatureSet{}, errors.New("ffmpeg audio ad metadata appears before a frame header")
		}
		if current.seen[metric] {
			return AudioAdFeatureSet{}, fmt.Errorf("ffmpeg audio ad frame %d has duplicate metadata", current.index)
		}
		current.seen[metric] = true
		current.raw[metric] = value
	}
	if current != nil {
		if err := appendAudioAdFrame(&set, current, maxWindows); err != nil {
			return AudioAdFeatureSet{}, err
		}
	}
	return set, nil
}

func parseAudioAdFrameHeader(line string) (index int, start float64, ok bool, err error) {
	if !strings.HasPrefix(line, "frame:") {
		return 0, 0, false, nil
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return 0, 0, false, errors.New("ffmpeg audio ad metadata has an empty frame header")
	}
	index, err = strconv.Atoi(strings.TrimPrefix(fields[0], "frame:"))
	if err != nil || index < 0 {
		return 0, 0, false, fmt.Errorf("ffmpeg audio ad metadata has invalid frame id %q", fields[0])
	}
	for _, field := range fields[1:] {
		if !strings.HasPrefix(field, "pts_time:") {
			continue
		}
		start, ok = parseBoundedNumber(strings.TrimPrefix(field, "pts_time:"), "", 0, maxMediaSeconds)
		if !ok {
			return 0, 0, false, fmt.Errorf("ffmpeg audio ad frame %d has invalid pts_time", index)
		}
		return index, start, true, nil
	}
	return 0, 0, false, fmt.Errorf("ffmpeg audio ad frame %d has no pts_time", index)
}

func parseAudioAdMetric(line string) (metric int, value string, ok bool) {
	for i, key := range [...]string{
		audioAdRMSKey,
		audioAdCentroidKey,
		audioAdFlatnessKey,
		audioAdRolloffKey,
	} {
		prefix := key + "="
		if strings.HasPrefix(line, prefix) {
			return i, strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}
	return 0, "", false
}

func appendAudioAdFrame(set *AudioAdFeatureSet, raw *audioAdMetadataFrame, maxWindows int) error {
	if raw.index != len(set.Frames) {
		return fmt.Errorf("ffmpeg audio ad frame id %d is out of sequence", raw.index)
	}
	if len(set.Frames) >= maxWindows {
		return fmt.Errorf("ffmpeg audio ad analysis exceeded %d frames", maxWindows)
	}
	if len(set.Frames) > 0 {
		previous := set.Frames[len(set.Frames)-1].StartSeconds
		if math.Abs(raw.start-previous-AudioAdFeatureWindowSeconds) > audioAdTimestampEpsilon {
			return fmt.Errorf("ffmpeg audio ad frame %d has a non-contiguous timestamp", raw.index)
		}
	}
	for _, seen := range raw.seen {
		if !seen {
			return fmt.Errorf("ffmpeg audio ad frame %d is incomplete", raw.index)
		}
	}
	silent := strings.EqualFold(raw.raw[0], "-inf")
	rms, ok := parseBoundedNumber(raw.raw[0], "", audioAdSilenceRMSDBFS, 0)
	if silent {
		rms = audioAdSilenceRMSDBFS
		ok = true
	}
	if !ok {
		return fmt.Errorf("ffmpeg audio ad frame %d has invalid RMS", raw.index)
	}
	var err error
	spectral := [3]float64{}
	for i := range spectral {
		spectral[i], err = parseAudioAdSpectralValue(raw.raw[i+1], silent)
		if err != nil {
			return fmt.Errorf("ffmpeg audio ad frame %d has invalid spectral metadata", raw.index)
		}
	}
	if spectral[0] < 0 || spectral[0] > audioAdSampleRate/2 {
		return fmt.Errorf("ffmpeg audio ad frame %d has invalid centroid", raw.index)
	}
	if spectral[1] < 0 || spectral[1] > 1 {
		return fmt.Errorf("ffmpeg audio ad frame %d has invalid flatness", raw.index)
	}
	if spectral[2] < 0 || spectral[2] > audioAdSampleRate/2 {
		return fmt.Errorf("ffmpeg audio ad frame %d has invalid rolloff", raw.index)
	}
	set.Frames = append(set.Frames, AudioAdFeatureFrame{
		StartSeconds:       raw.start,
		RMSDBFS:            rms,
		SpectralCentroidHz: spectral[0],
		SpectralFlatness:   spectral[1],
		SpectralRolloffHz:  spectral[2],
	})
	return nil
}

// parseAudioAdSpectralValue parses one spectral metric. A silent window's
// spectral statistics are mathematically undefined and ffmpeg reports them
// as NaN; that specific, expected case is normalized to zero so NaN never
// enters the emitted feature set. Any other unparsable or non-finite value
// (including Inf, silent or not) fails closed.
func parseAudioAdSpectralValue(raw string, silent bool) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, errors.New("unparsable metric")
	}
	if silent && math.IsNaN(value) {
		return 0, nil
	}
	if !finite(value) {
		return 0, errors.New("non-finite metric")
	}
	return value, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
