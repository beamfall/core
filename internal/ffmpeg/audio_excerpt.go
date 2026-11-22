package ffmpeg

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
)

// Audio excerpt bounds (CORE-3073). The sonic analyser reads a short decoded
// excerpt, never a whole file; these limits keep one decode inside the 16 MiB
// capture cap and refuse a request that could not be held in memory.
const (
	maxAudioExcerptSeconds    = 120
	minAudioExcerptSampleRate = 8000
	maxAudioExcerptSampleRate = 48000
	maxAudioExcerptChannels   = 2
)

// AudioExcerpt decodes up to seconds of src's first audio stream, starting at
// startSeconds, to interleaved float32 samples at sampleRate and channels.
// The samples stay in memory and nothing is written to disk. A source shorter
// than the window returns what it holds, and a window past the end returns an
// error, never an empty excerpt.
func (r *Runner) AudioExcerpt(ctx context.Context, src string, startSeconds, seconds float64, sampleRate, channels int) ([]float32, error) {
	if err := checkPath(src); err != nil {
		return nil, err
	}
	if err := validAudioExcerpt(startSeconds, seconds, sampleRate, channels); err != nil {
		return nil, err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	cmd := r.generationCommand(ctx, readConfinement(src), ffmpegPath,
		"-v", "error",
		"-ss", strconv.FormatFloat(startSeconds, 'f', 3, 64),
		"-t", strconv.FormatFloat(seconds, 'f', 3, 64),
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-map", "0:a:0",
		"-vn", "-sn", "-dn",
		"-ac", strconv.Itoa(channels),
		"-ar", strconv.Itoa(sampleRate),
		"-f", "f32le",
		"pipe:1",
	)
	stdout, stderr, err := r.run(ctx, cmd)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("ffmpeg audio excerpt: %s", captureDetail(nil, stderr, err)), survivedError(err))
	}
	samples, err := decodeFloat32LE(stdout, channels)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, errors.New("ffmpeg audio excerpt produced no samples")
	}
	return samples, nil
}

// validAudioExcerpt refuses an excerpt request the capture cap cannot hold or
// that names an unusable format.
func validAudioExcerpt(startSeconds, seconds float64, sampleRate, channels int) error {
	if math.IsNaN(startSeconds) || math.IsInf(startSeconds, 0) || startSeconds < 0 {
		return errors.New("ffmpeg audio excerpt: start must be a non-negative number of seconds")
	}
	if math.IsNaN(seconds) || seconds <= 0 || seconds > maxAudioExcerptSeconds {
		return fmt.Errorf("ffmpeg audio excerpt: length must be in (0, %d] seconds", maxAudioExcerptSeconds)
	}
	if sampleRate < minAudioExcerptSampleRate || sampleRate > maxAudioExcerptSampleRate {
		return fmt.Errorf("ffmpeg audio excerpt: sample rate must be in [%d, %d]", minAudioExcerptSampleRate, maxAudioExcerptSampleRate)
	}
	if channels < 1 || channels > maxAudioExcerptChannels {
		return fmt.Errorf("ffmpeg audio excerpt: channels must be in [1, %d]", maxAudioExcerptChannels)
	}
	if math.Ceil(seconds)*float64(sampleRate*channels*4) > captureLimitBytes {
		return errors.New("ffmpeg audio excerpt: request exceeds the capture limit")
	}
	return nil
}

// decodeFloat32LE turns raw little-endian float32 PCM into samples, dropping a
// trailing partial frame (a short final write) rather than misaligning
// channels.
func decodeFloat32LE(raw []byte, channels int) ([]float32, error) {
	frame := 4 * channels
	if frame <= 0 {
		return nil, errors.New("ffmpeg audio excerpt: channels must be positive")
	}
	n := len(raw) / frame * channels
	out := make([]float32, n)
	for i := range out {
		v := math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, errors.New("ffmpeg audio excerpt: decoded a non-finite sample")
		}
		out[i] = v
	}
	return out, nil
}
