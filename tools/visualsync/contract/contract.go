package contract

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
)

const (
	ContractVersion = "audio-frame.v1"
	ArtifactVersion = "1.0.0"
)

var CapabilityFlags = []string{
	"analysis-gap",
	"analysis-stalled",
	"dark-room",
	"live-analysis-unavailable",
	"overlay-safe",
	"photosensitive-safe",
	"reduced-motion",
}

type Artifact struct {
	Artifact              string            `json:"artifact"`
	ArtifactVersion       string            `json:"artifactVersion"`
	FrameContractVersion  string            `json:"frameContractVersion"`
	Evolution             Evolution         `json:"evolution"`
	AudibleTimeDefinition string            `json:"audibleTimeDefinition"`
	ReferenceDSP          DSPConfig         `json:"referenceDsp"`
	Fields                []Field           `json:"fields"`
	CapabilityFlags       []string          `json:"capabilityFlags"`
	PlatformTapRules      map[string]string `json:"platformTapRules"`
}

type Evolution struct {
	AdditiveOnly bool   `json:"additiveOnly"`
	Rule         string `json:"rule"`
}

type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Meaning  string `json:"meaning"`
}

type DSPConfig struct {
	SampleRateHz          int       `json:"sampleRateHz"`
	MonoMixdown           string    `json:"monoMixdown"`
	FFTWindowSamples      int       `json:"fftWindowSamples"`
	HopSamples            int       `json:"hopSamples"`
	Window                string    `json:"window"`
	SpectrumBands         int       `json:"spectrumBands"`
	SpectrumMinHz         float64   `json:"spectrumMinHz"`
	SpectrumMaxHz         float64   `json:"spectrumMaxHz"`
	DBFloor               float64   `json:"dbFloor"`
	BallisticsAttackMS    float64   `json:"ballisticsAttackMs"`
	BallisticsReleaseMS   float64   `json:"ballisticsReleaseMs"`
	RMSWindowMS           float64   `json:"rmsWindowMs"`
	LoudnessReferenceLUFS float64   `json:"loudnessReferenceLufs"`
	Onset                 string    `json:"onset"`
	OnsetBandGroups       [][2]int  `json:"onsetBandGroups"`
	GapToleranceHops      int       `json:"gapToleranceHops"`
	OutputTimeReference   string    `json:"outputTimeReference"`
	PublishedTolerances   Tolerance `json:"publishedTolerances"`
}

type Tolerance struct {
	TimeMS   float64 `json:"timeMs"`
	Scalar   float64 `json:"scalar"`
	Spectrum float64 `json:"spectrum"`
}

type AudioFrame struct {
	ContractVersion string    `json:"contractVersion"`
	Sequence        uint64    `json:"sequence"`
	MediaTimeMS     float64   `json:"mediaTimeMs"`
	HostTimeNanos   int64     `json:"hostTimeNanos"`
	OutputLatencyMS float64   `json:"outputLatencyMs"`
	AudibleTimeMS   float64   `json:"audibleTimeMs"`
	RMS             float64   `json:"rms"`
	LoudnessDB      float64   `json:"loudnessDb"`
	Spectrum        []float64 `json:"spectrum"`
	Onset           Onset     `json:"onset"`
	Beat            *Phase    `json:"beat,omitempty"`
	Bar             *Phase    `json:"bar,omitempty"`
	Phrase          *Phase    `json:"phrase,omitempty"`
	Flags           []string  `json:"flags,omitempty"`
}

type Onset struct {
	Broadband float64 `json:"broadband"`
	Low       float64 `json:"low"`
	Mid       float64 `json:"mid"`
	High      float64 `json:"high"`
}

type Phase struct {
	Phase             float64 `json:"phase"`
	Confidence        float64 `json:"confidence"`
	NextAudibleTimeMS float64 `json:"nextAudibleTimeMs"`
	TimeToNextMS      float64 `json:"timeToNextMs"`
}

type Options struct {
	OutputLatencyMS float64
	HostStartNanos  int64
	HostStepNanos   int64
}

type PCMChunk struct {
	MediaStartMS float64
	Samples      []float64
	Stall        bool
}

type Fixture struct {
	Version    string       `json:"version"`
	Source     string       `json:"source"`
	Tolerances Tolerance    `json:"tolerances"`
	Frames     []AudioFrame `json:"frames"`
}

func Build() Artifact {
	flags := append([]string(nil), CapabilityFlags...)
	sort.Strings(flags)
	return Artifact{
		Artifact:             "beamfall.visualsync.audio-frame",
		ArtifactVersion:      ArtifactVersion,
		FrameContractVersion: ContractVersion,
		Evolution: Evolution{
			AdditiveOnly: true,
			Rule:         "Consumers must ignore unknown fields and flags; producers must not remove or change the meaning of existing v1 fields.",
		},
		AudibleTimeDefinition: "audibleTimeMs = mediaTimeMs - outputLatencyMs",
		ReferenceDSP:          DefaultDSPConfig(),
		Fields: []Field{
			{"contractVersion", "string", true, "Explicit AudioFrame contract version."},
			{"sequence", "uint64", true, "Monotonic frame index in producer order."},
			{"mediaTimeMs", "number", true, "Media timeline at the end of the DSP analysis window."},
			{"hostTimeNanos", "int64", true, "Monotonic host clock sample time."},
			{"outputLatencyMs", "number", true, "Continuously updated device output-latency estimate."},
			{"audibleTimeMs", "number", true, "mediaTimeMs minus outputLatencyMs."},
			{"rms", "number", true, "300 ms mono RMS normalized to [0,1]."},
			{"loudnessDb", "number", true, "RMS level relative to the fixed LUFS reference."},
			{"spectrum", "[24]number", true, "Smoothed log-spaced 40 Hz-16 kHz band magnitudes normalized to [0,1]."},
			{"onset", "object", true, "Rectified log-spectral-flux onset strength, broadband and low/mid/high groups."},
			{"beat/bar/phrase", "object|null", false, "Optional phase and next-boundary prediction in audible time."},
			{"flags", "[]string", false, "Fixed capability/safety/degrade flags."},
		},
		CapabilityFlags: flags,
		PlatformTapRules: map[string]string{
			"android": "Tap app-owned decoded PCM before AudioTrack; do not use system Visualizer.",
			"apple":   "Tap AVAudioEngine PCM and apply live AVAudioSession.outputLatency route updates.",
			"drm":     "Protected content sets live-analysis-unavailable and uses generated assets only.",
			"web":     "Run the reference analysis in AudioWorklet; do not use AnalyserNode.",
		},
	}
}

func DefaultDSPConfig() DSPConfig {
	return DSPConfig{
		SampleRateHz:          48000,
		MonoMixdown:           "average channels",
		FFTWindowSamples:      2048,
		HopSamples:            512,
		Window:                "hann",
		SpectrumBands:         24,
		SpectrumMinHz:         40,
		SpectrumMaxHz:         16000,
		DBFloor:               -90,
		BallisticsAttackMS:    10,
		BallisticsReleaseMS:   200,
		RMSWindowMS:           300,
		LoudnessReferenceLUFS: -18,
		Onset:                 "half-wave rectified log-spectral flux with running peak normalization",
		OnsetBandGroups:       [][2]int{{0, 7}, {8, 15}, {16, 23}},
		GapToleranceHops:      1,
		OutputTimeReference:   "mediaTimeMs is the analysis-window end; audibleTimeMs subtracts outputLatencyMs",
		PublishedTolerances:   Tolerance{TimeMS: 0.02, Scalar: 0.000001, Spectrum: 0.000001},
	}
}

func DecodeFrame(data []byte) (AudioFrame, error) {
	var frame AudioFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return AudioFrame{}, err
	}
	if frame.ContractVersion != ContractVersion {
		return AudioFrame{}, fmt.Errorf("unsupported AudioFrame contract version %q", frame.ContractVersion)
	}
	if len(frame.Spectrum) != DefaultDSPConfig().SpectrumBands {
		return AudioFrame{}, fmt.Errorf("spectrum has %d bands, want %d", len(frame.Spectrum), DefaultDSPConfig().SpectrumBands)
	}
	return frame, nil
}

func AnalyzeWAV(data []byte, opts Options) ([]AudioFrame, error) {
	samples, err := PCMFromWAV(data)
	if err != nil {
		return nil, err
	}
	return AnalyzePCM(samples, opts), nil
}

func AnalyzePCM(samples []float64, opts Options) []AudioFrame {
	return AnalyzePCMChunks([]PCMChunk{{Samples: samples}}, opts)
}

func AnalyzePCMChunks(chunks []PCMChunk, opts Options) []AudioFrame {
	cfg := DefaultDSPConfig()
	var state dspState
	var frames []AudioFrame
	var expectedNextMS float64
	var haveExpected bool
	for _, chunk := range chunks {
		startIndex := len(frames)
		chunkFrames := analyzeChunk(cfg, &state, chunk.Samples, opts, uint64(len(frames)), chunk.MediaStartMS)
		if len(chunkFrames) == 0 {
			continue
		}
		flags := map[string]bool{}
		if haveExpected && chunk.MediaStartMS-expectedNextMS > float64(cfg.GapToleranceHops)*hopMS(cfg) {
			flags["analysis-gap"] = true
		}
		if chunk.Stall {
			flags["analysis-stalled"] = true
		}
		if len(flags) > 0 {
			chunkFrames[0].Flags = sortedFlags(flags)
		}
		frames = append(frames, chunkFrames...)
		last := frames[len(frames)-1]
		expectedNextMS = last.MediaTimeMS + hopMS(cfg)
		haveExpected = true
		for i := startIndex; i < len(frames); i++ {
			frames[i].Sequence = uint64(i)
		}
	}
	return frames
}

func PCMFromWAV(data []byte) ([]float64, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("not a RIFF/WAVE file")
	}
	var channels, bits uint16
	var sampleRate uint32
	var pcm []byte
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		off += 8
		if off+size > len(data) {
			return nil, errors.New("truncated WAV chunk")
		}
		chunk := data[off : off+size]
		switch id {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, errors.New("short fmt chunk")
			}
			format := binary.LittleEndian.Uint16(chunk[0:2])
			if format != 1 {
				return nil, fmt.Errorf("unsupported WAV format %d", format)
			}
			channels = binary.LittleEndian.Uint16(chunk[2:4])
			sampleRate = binary.LittleEndian.Uint32(chunk[4:8])
			bits = binary.LittleEndian.Uint16(chunk[14:16])
		case "data":
			pcm = chunk
		}
		off += size
		if size%2 == 1 {
			off++
		}
	}
	cfg := DefaultDSPConfig()
	if channels == 0 || sampleRate == 0 || len(pcm) == 0 {
		return nil, errors.New("missing WAV fmt or data chunk")
	}
	if int(sampleRate) != cfg.SampleRateHz {
		return nil, fmt.Errorf("sample rate %d Hz, want %d Hz", sampleRate, cfg.SampleRateHz)
	}
	if bits != 16 {
		return nil, fmt.Errorf("bits per sample %d, want 16", bits)
	}
	frameBytes := int(channels) * 2
	if len(pcm)%frameBytes != 0 {
		return nil, errors.New("unaligned PCM data")
	}
	samples := make([]float64, len(pcm)/frameBytes)
	for i := range samples {
		var sum float64
		for ch := 0; ch < int(channels); ch++ {
			raw := int16(binary.LittleEndian.Uint16(pcm[i*frameBytes+ch*2:]))
			sum += float64(raw) / 32768
		}
		samples[i] = sum / float64(channels)
	}
	return samples, nil
}

func WAV16Mono(samples []float64) []byte {
	var data bytes.Buffer
	_ = binary.Write(&data, binary.LittleEndian, []byte("RIFF"))
	pcmBytes := uint32(len(samples) * 2)
	_ = binary.Write(&data, binary.LittleEndian, uint32(36)+pcmBytes)
	_ = binary.Write(&data, binary.LittleEndian, []byte("WAVEfmt "))
	_ = binary.Write(&data, binary.LittleEndian, uint32(16))
	_ = binary.Write(&data, binary.LittleEndian, uint16(1))
	_ = binary.Write(&data, binary.LittleEndian, uint16(1))
	_ = binary.Write(&data, binary.LittleEndian, uint32(DefaultDSPConfig().SampleRateHz))
	_ = binary.Write(&data, binary.LittleEndian, uint32(DefaultDSPConfig().SampleRateHz*2))
	_ = binary.Write(&data, binary.LittleEndian, uint16(2))
	_ = binary.Write(&data, binary.LittleEndian, uint16(16))
	_ = binary.Write(&data, binary.LittleEndian, []byte("data"))
	_ = binary.Write(&data, binary.LittleEndian, pcmBytes)
	for _, sample := range samples {
		sample = math.Max(-1, math.Min(1, sample))
		_ = binary.Write(&data, binary.LittleEndian, int16(math.Round(sample*32767)))
	}
	return data.Bytes()
}

type dspState struct {
	smoothedBands []float64
	prevBands     []float64
	onsetPeak     float64
	groupPeaks    [3]float64
}

func analyzeChunk(cfg DSPConfig, state *dspState, samples []float64, opts Options, baseSeq uint64, mediaStartMS float64) []AudioFrame {
	if len(samples) < cfg.FFTWindowSamples {
		return nil
	}
	if opts.HostStepNanos == 0 {
		opts.HostStepNanos = int64(float64(cfg.HopSamples) / float64(cfg.SampleRateHz) * 1e9)
	}
	if state.smoothedBands == nil {
		state.smoothedBands = make([]float64, cfg.SpectrumBands)
		state.prevBands = make([]float64, cfg.SpectrumBands)
	}
	window := hann(cfg.FFTWindowSamples)
	rmsWindow := int(float64(cfg.SampleRateHz) * cfg.RMSWindowMS / 1000)
	var frames []AudioFrame
	for start := 0; start+cfg.FFTWindowSamples <= len(samples); start += cfg.HopSamples {
		rawBands := spectrumBands(cfg, samples[start:start+cfg.FFTWindowSamples], window)
		smoothed := smoothBands(cfg, state.smoothedBands, rawBands)
		onset := onset(cfg, state, rawBands)
		mediaTimeMS := mediaStartMS + float64(start+cfg.FFTWindowSamples)/float64(cfg.SampleRateHz)*1000
		mediaTimeMS = round6(mediaTimeMS)
		outputLatencyMS := round6(opts.OutputLatencyMS)
		rms := rmsAt(samples, start+cfg.FFTWindowSamples, rmsWindow)
		frame := AudioFrame{
			ContractVersion: ContractVersion,
			Sequence:        baseSeq + uint64(len(frames)),
			MediaTimeMS:     mediaTimeMS,
			HostTimeNanos:   opts.HostStartNanos + int64(baseSeq+uint64(len(frames)))*opts.HostStepNanos,
			OutputLatencyMS: outputLatencyMS,
			AudibleTimeMS:   round6(mediaTimeMS - outputLatencyMS),
			RMS:             round6(rms),
			LoudnessDB:      round6(20*math.Log10(math.Max(rms, 1e-9)) - cfg.LoudnessReferenceLUFS),
			Spectrum:        roundSlice(smoothed),
			Onset:           onset,
		}
		frames = append(frames, frame)
	}
	return frames
}

func hann(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return out
}

func spectrumBands(cfg DSPConfig, samples, window []float64) []float64 {
	bins := cfg.FFTWindowSamples/2 + 1
	mags := make([]float64, bins)
	for k := 0; k < bins; k++ {
		var real, imag float64
		for n, sample := range samples {
			angle := 2 * math.Pi * float64(k*n) / float64(cfg.FFTWindowSamples)
			x := sample * window[n]
			real += x * math.Cos(angle)
			imag -= x * math.Sin(angle)
		}
		mags[k] = math.Sqrt(real*real+imag*imag) / float64(cfg.FFTWindowSamples/2)
	}
	out := make([]float64, cfg.SpectrumBands)
	ratio := math.Pow(cfg.SpectrumMaxHz/cfg.SpectrumMinHz, 1/float64(cfg.SpectrumBands))
	for band := range out {
		lo := cfg.SpectrumMinHz * math.Pow(ratio, float64(band))
		hi := cfg.SpectrumMinHz * math.Pow(ratio, float64(band+1))
		start := int(math.Ceil(lo * float64(cfg.FFTWindowSamples) / float64(cfg.SampleRateHz)))
		end := int(math.Floor(hi * float64(cfg.FFTWindowSamples) / float64(cfg.SampleRateHz)))
		if end < start {
			end = start
		}
		var sum float64
		var count int
		for bin := start; bin <= end && bin < len(mags); bin++ {
			sum += mags[bin]
			count++
		}
		if count == 0 {
			out[band] = 0
			continue
		}
		db := 20 * math.Log10(math.Max(sum/float64(count), math.Pow(10, cfg.DBFloor/20)))
		out[band] = math.Max(0, math.Min(1, (db-cfg.DBFloor)/-cfg.DBFloor))
	}
	return out
}

func smoothBands(cfg DSPConfig, prev, raw []float64) []float64 {
	hopSec := float64(cfg.HopSamples) / float64(cfg.SampleRateHz)
	attack := math.Exp(-hopSec / (cfg.BallisticsAttackMS / 1000))
	release := math.Exp(-hopSec / (cfg.BallisticsReleaseMS / 1000))
	out := make([]float64, len(raw))
	for i := range raw {
		coeff := release
		if raw[i] > prev[i] {
			coeff = attack
		}
		out[i] = raw[i] + coeff*(prev[i]-raw[i])
		prev[i] = out[i]
	}
	return out
}

func onset(cfg DSPConfig, state *dspState, bands []float64) Onset {
	if state.onsetPeak == 0 {
		copy(state.prevBands, bands)
		state.onsetPeak = 1
		return Onset{}
	}
	var flux float64
	groupFlux := [3]float64{}
	for i, band := range bands {
		delta := math.Max(0, band-state.prevBands[i])
		flux += delta
		for gi, span := range cfg.OnsetBandGroups {
			if i >= span[0] && i <= span[1] {
				groupFlux[gi] += delta
			}
		}
		state.prevBands[i] = band
	}
	flux /= float64(len(bands))
	state.onsetPeak = math.Max(flux, state.onsetPeak*0.995)
	out := Onset{Broadband: round6(flux / math.Max(state.onsetPeak, 1e-9))}
	for gi := range groupFlux {
		width := float64(cfg.OnsetBandGroups[gi][1] - cfg.OnsetBandGroups[gi][0] + 1)
		groupFlux[gi] /= width
		state.groupPeaks[gi] = math.Max(groupFlux[gi], state.groupPeaks[gi]*0.995)
		value := round6(groupFlux[gi] / math.Max(state.groupPeaks[gi], 1e-9))
		switch gi {
		case 0:
			out.Low = value
		case 1:
			out.Mid = value
		case 2:
			out.High = value
		}
	}
	return out
}

func rmsAt(samples []float64, end, window int) float64 {
	start := end - window
	if start < 0 {
		start = 0
	}
	var sum float64
	for _, sample := range samples[start:end] {
		sum += sample * sample
	}
	return math.Sqrt(sum / float64(end-start))
}

func hopMS(cfg DSPConfig) float64 {
	return float64(cfg.HopSamples) / float64(cfg.SampleRateHz) * 1000
}

func sortedFlags(flags map[string]bool) []string {
	out := make([]string, 0, len(flags))
	for flag := range flags {
		out = append(out, flag)
	}
	sort.Strings(out)
	return out
}

func roundSlice(in []float64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = round6(v)
	}
	return out
}

func round6(v float64) float64 {
	return math.Round(v*1_000_000) / 1_000_000
}
