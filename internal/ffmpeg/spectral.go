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

// SpectralFrameSeconds is the hop between two spectral sub-fingerprints. Eight
// frames make one FingerprintWindowSeconds window, so the detector can search
// relative offsets at frame resolution and still report whole-second segments.
const SpectralFrameSeconds = 0.125

const (
	// spectralFrameSamples is the FFT size: 256 ms at fingerprintSampleRate,
	// long enough to resolve the low bands, short enough to follow a melody.
	spectralFrameSamples = 2048
	// spectralHopSamples is SpectralFrameSeconds in samples.
	spectralHopSamples = int(SpectralFrameSeconds * fingerprintSampleRate)
	// spectralBands log-spaced bands between spectralLowHz and spectralHighHz
	// give spectralBands-1 = 32 adjacent-band comparisons, one per hash bit.
	spectralBands  = 33
	spectralLowHz  = 250.0
	spectralHighHz = 3500.0
	// spectralFloor is the band energy floor relative to the frame's loudest
	// band (-50 dB). Bands below it compare equal, so the bits of near-empty
	// bands do not flip with encoder noise.
	spectralFloor = 1e-5
	// spectralSilenceMeanSquare is the frame mean square (-60 dBFS RMS) below
	// which a frame is silent and hashes to SpectralSilentFrame.
	spectralSilenceMeanSquare = 1e-6
	// spectralBytesPerSecond is the decoded PCM rate: mono s16le.
	spectralBytesPerSecond = fingerprintSampleRate * 2
)

// SpectralSilentFrame is the hash of a silent frame. Callers treat it as
// matching nothing: two files that are both silent at the same moment share no
// audio. Audible audio can also hash to zero (a lone tone in the top band sets
// no bit), so callers trust only frames with enough bits set and unset.
const SpectralSilentFrame uint32 = 0

// SpectralFingerprintMaxSeconds is the longest single-spawn spectral capture
// that stays within captureLimitBytes at 8 kHz mono s16le PCM.
const SpectralFingerprintMaxSeconds = captureLimitBytes / spectralBytesPerSecond

// SpectralFingerprint returns one 32-bit spectral sub-fingerprint per
// SpectralFrameSeconds frame over the first seconds of src's primary audio
// stream. FFmpeg decodes the bounded region to 8 kHz mono PCM on stdout through
// the hardened spawn seam (read-only confinement to the input's directory); the
// hash runs here in Go. Each bit is the sign of the energy difference between
// two adjacent log-spaced bands, so the hash follows the sound's spectral shape
// and is unchanged by gain. It replaces the per-second loudness signature where
// two files share a loudness curve but not their sound (CORE-3111).
func (r *Runner) SpectralFingerprint(ctx context.Context, src string, seconds int) ([]uint32, error) {
	return r.spectralFingerprint(ctx, src, seconds, nil)
}

// SpectralFingerprintRange returns the same signature as SpectralFingerprint
// over [startSeconds, startSeconds+seconds) of src's primary audio stream so
// callers scanning long files can decode bounded candidate spans within the
// capture limit.
//
//io:ignore ffmpeg-spectral-fingerprint-range — seek wrapper over the spectralFingerprint I/O journey.
func (r *Runner) SpectralFingerprintRange(ctx context.Context, src string, startSeconds, seconds int) ([]uint32, error) {
	if startSeconds < 0 {
		return nil, errors.New("ffmpeg spectral fingerprint: startSeconds must be non-negative")
	}
	if startSeconds == 0 {
		return r.spectralFingerprint(ctx, src, seconds, nil)
	}
	return r.spectralFingerprint(ctx, src, seconds, []string{"-ss", strconv.Itoa(startSeconds)})
}

// SpectralFingerprintTail returns the same signature as SpectralFingerprint
// over the final seconds of src, ending at EOF like AudioFingerprintTail.
//
//io:ignore ffmpeg-spectral-fingerprint-tail — seek wrapper over the spectralFingerprint I/O journey.
func (r *Runner) SpectralFingerprintTail(ctx context.Context, src string, seconds, durationSeconds int) ([]uint32, error) {
	return withTailSeek(float64(seconds), durationSeconds, func(seek []string) ([]uint32, error) {
		return r.spectralFingerprint(ctx, src, seconds, seek)
	})
}

func (r *Runner) spectralFingerprint(ctx context.Context, src string, seconds int, inputSeek []string) ([]uint32, error) {
	if err := checkPath(src); err != nil {
		return nil, err
	}
	if seconds <= 0 {
		return nil, errors.New("ffmpeg spectral fingerprint: seconds must be positive")
	}
	if seconds > SpectralFingerprintMaxSeconds {
		return nil, fmt.Errorf("ffmpeg spectral fingerprint: %d s exceeds the capture limit", seconds)
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	args := []string{"-v", "error"}
	args = append(args, inputSeek...)
	args = append(args,
		"-t", strconv.Itoa(seconds),
		"-protocol_whitelist", productionProtocolAllowlist,
		"-i", src,
		"-map", "0:a:0",
		// Raw PCM carries no timestamps: pad any delay before the stream's
		// first sample (and any gap inside it) with silence, so sample 0 is
		// the seek point on the media timeline, not the first decoded sample.
		"-af", "aresample=async=1:first_pts=0",
		"-ac", "1",
		"-ar", strconv.Itoa(fingerprintSampleRate),
		"-c:a", "pcm_s16le",
		"-f", "s16le",
		"pipe:1",
	)
	cmd := r.generationCommand(ctx, readConfinement(src), ffmpegPath, args...)
	stdout, stderr, err := r.run(ctx, cmd)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("ffmpeg spectral fingerprint: %s", captureDetail(nil, stderr, err)), survivedError(err))
	}
	hashes := spectralHashes(stdout)
	if len(hashes) == 0 {
		return nil, errors.New("ffmpeg spectral fingerprint produced no frames")
	}
	return hashes, nil
}

// withTailSeek runs analyse with an input seek to the final tailSeconds of the
// file. A known catalog duration gives an absolute -ss: FFmpeg silently
// ignores -sseof on an input with no container duration (a live-written
// Matroska) and decodes from the start, which would pass the opening off as the
// tail. Without a duration it seeks with -sseof.
func withTailSeek[T any](tailSeconds float64, durationSeconds int, analyse func(seek []string) (T, error)) (T, error) {
	if durationSeconds > 0 {
		start := math.Max(0, float64(durationSeconds)-tailSeconds)
		return analyse([]string{"-ss", strconv.FormatFloat(start, 'f', 3, 64)})
	}
	return analyse([]string{"-sseof", "-" + strconv.FormatFloat(tailSeconds, 'f', 3, 64)})
}

// spectralHashes hashes mono s16le PCM at fingerprintSampleRate into one
// sub-fingerprint per whole spectralHopSamples hop. Each frame is centred on its
// hop, with zero padding past both ends, so N seconds give exactly
// N/SpectralFrameSeconds frames and frame k describes [k, k+1) hops: the final
// second of a region is as matchable as the first.
func spectralHashes(pcm []byte) []uint32 {
	const pad = (spectralFrameSamples - spectralHopSamples) / 2
	count := len(pcm) / 2 / spectralHopSamples
	if count == 0 {
		return nil
	}
	samples := make([]float64, pad+len(pcm)/2+spectralFrameSamples)
	for i := range len(pcm) / 2 {
		samples[pad+i] = float64(int16(binary.LittleEndian.Uint16(pcm[2*i:]))) / 32768 // #nosec G115 -- s16le: reinterpreting the bits as signed is the decode
	}
	h := newSpectralHasher()
	hashes := make([]uint32, count)
	for k := range hashes {
		off := k * spectralHopSamples
		hashes[k] = h.hash(samples[off : off+spectralFrameSamples])
	}
	return hashes
}

// spectralHasher holds the per-run tables and scratch buffers for hashing.
type spectralHasher struct {
	window  []float64
	edges   [spectralBands + 1]int
	twiddle []complex128
	buf     []complex128
	bands   [spectralBands]float64
}

func newSpectralHasher() *spectralHasher {
	h := &spectralHasher{
		window:  make([]float64, spectralFrameSamples),
		twiddle: make([]complex128, spectralFrameSamples/2),
		buf:     make([]complex128, spectralFrameSamples),
	}
	for i := range h.window {
		h.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(spectralFrameSamples-1))
	}
	for k := range h.twiddle {
		angle := -2 * math.Pi * float64(k) / spectralFrameSamples
		h.twiddle[k] = complex(math.Cos(angle), math.Sin(angle))
	}
	for b := 0; b <= spectralBands; b++ {
		hz := spectralLowHz * math.Pow(spectralHighHz/spectralLowHz, float64(b)/spectralBands)
		bin := int(math.Round(hz * spectralFrameSamples / fingerprintSampleRate))
		if b > 0 && bin <= h.edges[b-1] {
			bin = h.edges[b-1] + 1
		}
		h.edges[b] = bin
	}
	return h
}

// hash returns frame's sub-fingerprint: bit b is set when band b holds more
// energy than band b+1, both floored at spectralFloor below the loudest band.
func (h *spectralHasher) hash(frame []float64) uint32 {
	var meanSquare float64
	for i, x := range frame {
		meanSquare += x * x
		h.buf[i] = complex(x*h.window[i], 0)
	}
	if meanSquare/float64(len(frame)) < spectralSilenceMeanSquare {
		return SpectralSilentFrame
	}
	h.fft()
	loudest := 0.0
	for b := range h.bands {
		var energy float64
		for k := h.edges[b]; k < h.edges[b+1]; k++ {
			re, im := real(h.buf[k]), imag(h.buf[k])
			energy += re*re + im*im
		}
		h.bands[b] = energy
		loudest = math.Max(loudest, energy)
	}
	floor := loudest * spectralFloor
	var hash uint32
	for b := 0; b < spectralBands-1; b++ {
		if math.Max(h.bands[b], floor) > math.Max(h.bands[b+1], floor) {
			hash |= 1 << b
		}
	}
	return hash
}

// fft transforms h.buf in place (iterative radix-2, length spectralFrameSamples).
func (h *spectralHasher) fft() {
	a := h.buf
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, step := size/2, n/size
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				u := a[start+k]
				v := a[start+k+half] * h.twiddle[k*step]
				a[start+k], a[start+k+half] = u+v, u-v
			}
		}
	}
}
