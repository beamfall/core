package ffmpeg

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"math/rand/v2"
	"reflect"
	"testing"
)

// melodyPCM renders seconds of mono s16le PCM at fingerprintSampleRate: a new
// three-partial chord every 250 ms, drawn from seed, scaled by gain.
func melodyPCM(seed uint64, seconds, gain float64) []byte {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) // #nosec G404 -- deterministic test signal
	n := int(seconds * fingerprintSampleRate)
	pcm := make([]byte, 2*n)
	var freqs [3]float64
	for i := 0; i < n; i++ {
		if i%(fingerprintSampleRate/4) == 0 {
			for p := range freqs {
				freqs[p] = 300 + rng.Float64()*3000
			}
		}
		t := float64(i) / fingerprintSampleRate
		var x float64
		for p, f := range freqs {
			x += math.Sin(2*math.Pi*f*t) / float64(p+1)
		}
		v := int16(math.Round(gain * x / 2 * 32767))
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(v)) // #nosec G115 -- s16le encode of a signed sample
	}
	return pcm
}

func meanHamming(a, b []uint32) float64 {
	var total int
	for i := range a {
		total += bits.OnesCount32(a[i] ^ b[i])
	}
	return float64(total) / float64(len(a))
}

func TestSpectralHashesFrameCount(t *testing.T) {
	pcm := melodyPCM(1, 3, 0.8)
	// Three seconds give 24 frames: the final second is fully covered.
	if got, want := len(spectralHashes(pcm)), int(3/SpectralFrameSeconds); got != want {
		t.Fatalf("frames = %d, want %d", got, want)
	}
	if got := spectralHashes(pcm[:2*(spectralHopSamples-1)]); got != nil {
		t.Fatalf("sub-hop input hashed to %d frames, want none", len(got))
	}
}

func TestSpectralHashesGainInvariant(t *testing.T) {
	loud := spectralHashes(melodyPCM(7, 4, 0.9))
	quiet := spectralHashes(melodyPCM(7, 4, 0.05))
	if d := meanHamming(loud, quiet); d > 1 {
		t.Fatalf("mean Hamming distance across a 25 dB gain change = %.2f bits, want <= 1", d)
	}
}

func TestSpectralHashesSeparateDifferentAudio(t *testing.T) {
	a := spectralHashes(melodyPCM(7, 4, 0.8))
	b := spectralHashes(melodyPCM(8, 4, 0.8))
	if d := meanHamming(a, b); d < 8 {
		t.Fatalf("mean Hamming distance between different melodies = %.2f bits, want >= 8", d)
	}
}

func TestSpectralHashesSilence(t *testing.T) {
	hashes := spectralHashes(make([]byte, 2*fingerprintSampleRate))
	for i, h := range hashes {
		if h != SpectralSilentFrame {
			t.Fatalf("silent frame %d hashed to %#x", i, h)
		}
	}
	for i, h := range spectralHashes(melodyPCM(3, 1, 0.5)) {
		if h == SpectralSilentFrame {
			t.Fatalf("audible frame %d hashed to the silent value", i)
		}
	}
}

func TestSpectralFFTMatchesDFT(t *testing.T) {
	h := newSpectralHasher()
	rng := rand.New(rand.NewPCG(1, 2)) // #nosec G404 -- deterministic test signal
	input := make([]float64, spectralFrameSamples)
	for i := range input {
		input[i] = rng.Float64()*2 - 1
		h.buf[i] = complex(input[i], 0)
	}
	h.fft()
	for _, k := range []int{0, 1, 17, 300, 1023, 2047} {
		var want complex128
		for i, x := range input {
			angle := -2 * math.Pi * float64(k*i) / spectralFrameSamples
			want += complex(x*math.Cos(angle), x*math.Sin(angle))
		}
		if diff := h.buf[k] - want; math.Hypot(real(diff), imag(diff)) > 1e-6 {
			t.Fatalf("bin %d = %v, want %v", k, h.buf[k], want)
		}
	}
}

func TestWithTailSeekPrefersCatalogDuration(t *testing.T) {
	var seeks [][]string
	record := func(seek []string) (int, error) {
		seeks = append(seeks, seek)
		return 42, nil
	}
	if out, err := withTailSeek(240, 1000, record); err != nil || out != 42 {
		t.Fatalf("withTailSeek = %d, %v", out, err)
	}
	if _, err := withTailSeek(240, 100, record); err != nil {
		t.Fatal(err)
	}
	if _, err := withTailSeek(240, 0, record); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"-ss", "760.000"}, {"-ss", "0.000"}, {"-sseof", "-240.000"}}
	if !reflect.DeepEqual(seeks, want) {
		t.Fatalf("seeks = %q, want %q", seeks, want)
	}
	if _, err := withTailSeek(240, 1000, func([]string) (int, error) {
		return 0, errors.New("Invalid data found when processing input")
	}); err == nil {
		t.Fatal("failure did not propagate")
	}
}
