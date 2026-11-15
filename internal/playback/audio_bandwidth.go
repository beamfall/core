package playback

import (
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// Audio-only rung peak (CORE-1849). The audio-only rung advertises the peak of
// the audio it actually carries, not a fixed stereo-AAC allowance, so a 5.1
// E-AC-3 or passthrough stream does not under-declare its BANDWIDTH (ADR-0010
// section 8, honest ABR).
const (
	// audioOnlyMuxAllowance is the container and segment overhead on top of
	// the audio rate. With stereo AAC it reproduces the ladder's
	// audioMuxReserve exactly.
	audioOnlyMuxAllowance = audioMuxReserve - 2*aacBitsPerChannel
	// aacBitsPerChannel and eac3BitsPerChannel bound FFmpeg's default encode
	// rate per channel. No -b:a is passed, and the defaults measure about
	// 66k (AAC) and 75k to 98k (E-AC-3) per channel.
	aacBitsPerChannel  = 64_000
	eac3BitsPerChannel = 96_000
	// Spec peaks of the two passthrough codecs (passthroughAudioCodecCap).
	trueHDPeakBits  = 18_000_000
	dtsHDMAPeakBits = 24_500_000
	// Stand-ins for source facts a scan never recorded.
	unknownSampleRate = 192_000
	unknownBitDepth   = 24
)

// audioOnlyBandwidth is the peak bits/s an audio-only stream encoded with
// audio from source carries. adjusted reports a pitch or tempo adjustment,
// which re-encodes even a passthrough profile (ffmpeg Rendition.audioArgs).
func audioOnlyBandwidth(audio ffmpeg.AudioProfile, source store.MediaFileStream, adjusted bool) int {
	channels := audio.Channels
	if channels <= 0 {
		channels = 2
	}
	switch {
	case audio.Codec == "copy" && !adjusted:
		return passthroughPeakBits(source) + audioOnlyMuxAllowance
	case audio.Codec == "eac3":
		return channels*eac3BitsPerChannel + audioOnlyMuxAllowance
	default:
		return channels*aacBitsPerChannel + audioOnlyMuxAllowance
	}
}

// passthroughPeakBits bounds a copied lossless stream (TrueHD or DTS-HD MA,
// the only passthrough codecs) at the PCM rate it decodes to, which lossless
// coding never exceeds, and at the codec's spec peak. Missing facts take the
// worst case.
func passthroughPeakBits(source store.MediaFileStream) int {
	codecPeak := trueHDPeakBits
	if normalizedAudioCodec(source.Codec) != "truehd" {
		codecPeak = dtsHDMAPeakBits
	}
	if source.Channels <= 0 {
		return codecPeak
	}
	rate := source.SampleRate
	if rate <= 0 {
		rate = unknownSampleRate
	}
	depth := source.BitDepth
	if depth <= 0 {
		depth = unknownBitDepth
	}
	return min(source.Channels*rate*depth, codecPeak)
}

// AudioSourceStream is the audio stream an audio-only encode maps: the
// selected stream index when one is set, otherwise the first audio stream
// (ffmpeg's 0:a:0).
func AudioSourceStream(streams []store.MediaFileStream, index *int) store.MediaFileStream {
	for _, stream := range streams {
		if stream.Type != "audio" {
			continue
		}
		if index == nil || stream.Index == *index {
			return stream
		}
	}
	return store.MediaFileStream{}
}
