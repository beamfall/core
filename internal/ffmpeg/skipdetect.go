package ffmpeg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Skip-segment detection FFmpeg seam (ENR-7 / ADR-0048 Decision 3/4). Two
// read-only probes the detector drives, both routed through the same hardened
// spawn seam (generationCommand / readConfinement) as the waveform and silence
// passes: Chapters reads the container's embedded chapter list (ffprobe), and
// AudioFingerprint extracts a coarse per-window loudness signature the detector
// correlates across sibling Media to find the shared intro/credits span. The
// fingerprint produces NO media bytes (the analysis runs under the null muxer)
// and never leaves the box — there is no community lookup (Decision 4 rejected
// it on privacy).

// Chapter is one embedded container chapter: a titled [start,end] range in
// seconds. Title may be empty (an untitled chapter mark); the detector maps a
// titled chapter to a skip-segment by a title heuristic, so an untitled chapter
// is simply ignored. The struct is the detector's seam shape — no client
// commitment.
type Chapter struct {
	StartSeconds float64
	EndSeconds   float64
	Title        string
}

// TextCue is one time-bounded subtitle or transcript cue used only as local
// corroborating evidence by skip detection. It is an internal analysis shape,
// not a client-facing subtitle contract.
type TextCue struct {
	StartSeconds float64
	EndSeconds   float64
	Text         string
}

// ffprobeChapters mirrors the subset of ffprobe -show_chapters JSON the
// detector reads: each chapter carries string-encoded start/end times and a
// tags.title. Times are seconds as strings (ffprobe's start_time/end_time);
// the title lives under tags.
type ffprobeChapters struct {
	Chapters []struct {
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		Tags      struct {
			Title string `json:"title"`
		} `json:"tags"`
	} `json:"chapters"`
}

// Chapters reads the container's embedded chapter list via ffprobe
// (-show_chapters), returning each chapter's [start,end] seconds and title. A
// file with no chapters returns an empty slice, not an error — most files have
// none, and that is the common, non-failing case. The probe is read-only and
// confined to the input file's directory (the same confinement as Probe).
func (r *Runner) Chapters(ctx context.Context, path string) ([]Chapter, error) {
	if err := checkPath(path); err != nil {
		return nil, err
	}
	r.resolveOnce.Do(func() {
		r.ffprobePath, r.resolveErr = exec.LookPath("ffprobe")
	})
	if r.resolveErr != nil {
		return nil, fmt.Errorf("ffprobe is not available: %w", r.resolveErr)
	}
	cmd := r.hardenedCommand(ctx, r.ffprobePath, readConfinement(path).ReadOnly, nil,
		append(append([]string{
			"-v", "error",
			"-print_format", "json",
			"-show_chapters",
		}, inputProtocolArgs()...), "--", path))
	output, stderr, err := r.run(ctx, cmd)
	if err != nil {
		if detail := captureDetail(output, stderr, nil); detail != "" {
			return nil, fmt.Errorf("ffprobe chapters: %s", detail)
		}
		return nil, fmt.Errorf("ffprobe chapters: %w", err)
	}
	var parsed ffprobeChapters
	if err := json.Unmarshal(output, &parsed); err != nil {
		return nil, fmt.Errorf("ffprobe chapters: %w", err)
	}
	chapters := make([]Chapter, 0, len(parsed.Chapters))
	for _, c := range parsed.Chapters {
		start, startOK := parseBoundedNumber(c.StartTime, "", 0, maxMediaSeconds)
		end, endOK := parseBoundedNumber(c.EndTime, "", 0, maxMediaSeconds)
		if !startOK || !endOK || end <= start {
			continue // a malformed chapter row is skipped, never fatal
		}
		chapters = append(chapters, Chapter{
			StartSeconds: start,
			EndSeconds:   end,
			Title:        strings.TrimSpace(c.Tags.Title),
		})
	}
	return chapters, nil
}

// fingerprintSampleRate is the mono resample rate the fingerprint analysis runs
// at — low enough to make the loudness signature cheap, high enough to resolve
// per-second structure.
const fingerprintSampleRate = 8000

// FingerprintWindowSeconds is the analysis window width in seconds — one coarse
// loudness value per window. Exported so the detector reasons in the same time
// unit when it maps a matched window run back to a [start,end] segment.
const FingerprintWindowSeconds = 1.0

// AudioFingerprint returns a coarse per-window loudness signature for src's
// primary audio stream over [0, windowCount) — one [0,1] magnitude per
// FingerprintWindowSeconds window, downmixed to mono at a low rate (ADR-0048
// Decision 4 method (b): the cross-Media comparison signal). The detector
// correlates two siblings' signatures to find their long shared run (the shared
// theme/intro) without any per-frame audio leaving the analysis. Like the
// waveform pass it runs under the null muxer (-f null) so NO media bytes are
// produced — only the printed per-window RMS metadata, parsed here — and is
// confined read-only to the input's directory. windowCount bounds the analysis
// length so a long file does not produce an unbounded signature; the detector
// passes the number of leading (and, for credits, trailing) windows it needs.
func (r *Runner) AudioFingerprint(ctx context.Context, src string, windowCount int) ([]float64, error) {
	return r.audioFingerprint(ctx, src, windowCount, nil)
}

// OpeningTextCues extracts text subtitle cues whose start lies in the bounded
// opening region. Bitmap subtitle streams are ignored: OCR is deliberately not
// part of this high-precision signal. Each text stream is decoded locally to
// WebVTT through stdout; no cue text leaves the host boundary.
func (r *Runner) OpeningTextCues(ctx context.Context, src string, maxSeconds int) ([]TextCue, error) {
	if maxSeconds <= 0 {
		return nil, errors.New("ffmpeg opening cues: max seconds must be positive")
	}
	probe, err := r.Probe(ctx, src)
	if err != nil {
		return nil, err
	}
	var cues []TextCue
	var decodeErrs []error
	for _, stream := range probe.Streams {
		if stream.Type != "subtitle" || isBitmapSubtitleCodec(stream.Codec) {
			continue
		}
		streamCues, err := r.openingTextCuesForStream(ctx, src, stream.Index, maxSeconds)
		if err != nil {
			decodeErrs = append(decodeErrs, err)
			continue
		}
		cues = append(cues, streamCues...)
	}
	if len(cues) > 0 {
		return cues, nil
	}
	return nil, errors.Join(decodeErrs...)
}

func (r *Runner) openingTextCuesForStream(ctx context.Context, src string, streamIndex, maxSeconds int) ([]TextCue, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	cmd := r.generationCommand(ctx, readConfinement(src), ffmpegPath,
		"-v", "error",
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-t", strconv.Itoa(maxSeconds),
		"-map", fmt.Sprintf("0:%d", streamIndex),
		"-c:s", "webvtt",
		"-f", "webvtt",
		"pipe:1",
	)
	stdout, stderr, err := r.run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg opening cues: %s", captureDetail(stdout, stderr, err))
	}
	return parseOpeningTextCues(string(stdout), float64(maxSeconds)), nil
}

func parseOpeningTextCues(vtt string, maxSeconds float64) []TextCue {
	vtt = strings.ReplaceAll(vtt, "\r\n", "\n")
	vtt = strings.ReplaceAll(vtt, "\r", "\n")
	var cues []TextCue
	for _, block := range strings.Split(vtt, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		for i, line := range lines {
			startText, endText, ok := strings.Cut(line, "-->")
			if !ok {
				continue
			}
			endFields := strings.Fields(endText)
			if len(endFields) == 0 {
				break
			}
			start, startOK := parseCueTimestamp(strings.TrimSpace(startText))
			end, endOK := parseCueTimestamp(endFields[0])
			if !startOK || !endOK || end <= start || start > maxSeconds {
				break
			}
			text := strings.TrimSpace(strings.Join(lines[i+1:], " "))
			if text != "" {
				cues = append(cues, TextCue{StartSeconds: start, EndSeconds: end, Text: text})
			}
			break
		}
	}
	return cues
}

func parseCueTimestamp(value string) (float64, bool) {
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	seconds, ok := parseBoundedNumber(parts[len(parts)-1], "", 0, 60)
	if !ok || seconds >= 60 {
		return 0, false
	}
	minutes, err := strconv.Atoi(parts[len(parts)-2])
	if err != nil || minutes < 0 || minutes >= 60 {
		return 0, false
	}
	hours := int64(0)
	if len(parts) == 3 {
		hours, ok = parseBoundedInteger(parts[0], 0, int64(maxMediaSeconds/3600))
		if !ok {
			return 0, false
		}
	}
	total := float64(hours)*3600 + float64(minutes)*60 + seconds
	return total, total <= maxMediaSeconds
}

// AudioFingerprintTail returns the same bounded signature as AudioFingerprint,
// but reads the final windowCount windows of src. FFmpeg's input-side -sseof
// keeps differently-sized siblings aligned to EOF for credits comparison. Old
// FFmpeg builds without -sseof retry with an absolute seek derived from the
// catalog duration.
//
//io:ignore ffmpeg-audio-fingerprint-tail — seek wrapper over the audioFingerprint I/O journey.
func (r *Runner) AudioFingerprintTail(ctx context.Context, src string, windowCount, durationSeconds int) ([]float64, error) {
	tailSeconds := FingerprintWindowSeconds * float64(windowCount)
	signature, err := r.audioFingerprint(ctx, src, windowCount, []string{
		"-sseof", "-" + strconv.FormatFloat(tailSeconds, 'f', 3, 64),
	})
	if err == nil || !sseofUnavailable(err.Error()) {
		return signature, err
	}
	start := math.Max(0, float64(durationSeconds)-tailSeconds)
	return r.audioFingerprint(ctx, src, windowCount, []string{
		"-ss", strconv.FormatFloat(start, 'f', 3, 64),
	})
}

func sseofUnavailable(detail string) bool {
	detail = strings.ToLower(detail)
	return strings.Contains(detail, "unrecognized option 'sseof'") ||
		strings.Contains(detail, "unrecognized option \"sseof\"") ||
		strings.Contains(detail, "option sseof not found") ||
		strings.Contains(detail, "option not found: sseof")
}

func (r *Runner) audioFingerprint(ctx context.Context, src string, windowCount int, inputSeek []string) ([]float64, error) {
	if err := checkPath(src); err != nil {
		return nil, err
	}
	if windowCount <= 0 {
		return nil, errors.New("ffmpeg fingerprint: window count must be positive")
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	windowSamples := int(FingerprintWindowSeconds * fingerprintSampleRate)
	filter := fmt.Sprintf(
		// astats injects the metadata key as "lavfi.astats.Overall.RMS_level";
		// ametadata's `key=` option matches the FULL key, so `key=Overall.RMS_level`
		// matches nothing and emits zero windows. Print all astats metadata and let
		// parseWaveformAmplitudes filter by the full prefix (rmsLevelPrefix) — robust
		// across ffmpeg versions.
		"aformat=channel_layouts=mono:sample_rates=%d,asetnsamples=n=%d:p=0,astats=metadata=1:reset=1,ametadata=print:file=-",
		fingerprintSampleRate, windowSamples)
	args := []string{
		"-v", "error",
	}
	args = append(args, inputSeek...)
	args = append(args,
		"-t", strconv.FormatFloat(FingerprintWindowSeconds*float64(windowCount), 'f', 3, 64),
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-map", "0:a:0",
		"-af", filter,
		"-f", "null",
		"--", os.DevNull,
	)
	cmd := r.generationCommand(ctx, readConfinement(src), ffmpegPath, args...)
	stdout, stderr, err := r.run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg fingerprint analysis: %s", captureDetail(stdout, stderr, err))
	}
	signature := parseWaveformAmplitudes(string(stdout) + string(stderr))
	if len(signature) == 0 {
		return nil, errors.New("ffmpeg fingerprint analysis produced no windows")
	}
	if len(signature) > windowCount {
		signature = signature[:windowCount]
	}
	// Guard against a NaN slipping in from a degenerate window (parseWaveform
	// already clamps to [0,1] and maps silence to 0, but be defensive).
	for i := range signature {
		if math.IsNaN(signature[i]) {
			signature[i] = 0
		}
	}
	return signature, nil
}
