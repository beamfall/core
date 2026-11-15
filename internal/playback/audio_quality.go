package playback

import (
	"strings"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// Audio delivery values (CORE-1627): the closed vocabulary of
// AudioQuality.Delivery, enumerated by AudioDeliveries.
const (
	// AudioDeliveryDirect means the client receives the source audio
	// bit-for-bit: the file itself on direct play, or the source stream copied
	// into a live HLS transcode.
	AudioDeliveryDirect = "direct"
	// AudioDeliveryTranscode means the server re-encodes the audio, or the
	// stream comes from a rendition whose audio the server does not vouch is
	// the source's bits.
	AudioDeliveryTranscode = "transcode"
)

// AudioDeliveries is the closed vocabulary of AudioQuality.Delivery. Growing it
// is a deliberate wire-contract change.
func AudioDeliveries() []string {
	return []string{AudioDeliveryDirect, AudioDeliveryTranscode}
}

// AudioQuality states the source quality of the audio the primary stream
// plays, and whether that stream delivers it unchanged (CORE-1627). Codec,
// SampleRate and BitDepth describe the selected source audio track as the
// probe recorded it; SampleRate (Hz) and BitDepth (bits per sample) are
// omitted when the probe recorded none, which is usual for a lossy codec's bit
// depth. Lossless reports a lossless source codec. Delivery describes
// Streams[0]: "direct" when the client receives the source audio bit-for-bit,
// "transcode" otherwise. Lossless direct play is Lossless with Delivery
// "direct".
type AudioQuality struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sampleRate,omitempty"`
	BitDepth   int    `json:"bitDepth,omitempty"`
	Lossless   bool   `json:"lossless,omitempty"`
	Delivery   string `json:"delivery"`
}

// audioQualityFor derives the AudioQuality of a decision whose primary stream
// is choices[0], playing the source audio stream at index chosen. It returns
// nil when there is no stream or no chosen audio track to describe.
//
// The primary stream delivers the source audio bit-for-bit when it is the
// file itself (direct play), or when it is a live transcode (an "hls-" or
// "audio-copy-" choice) whose audio profile copies the stream and the chosen
// track is one the client declared it passes through within its ceilings. A
// pitch/tempo adjustment, a composed picture and a precomputed rendition are
// always reported as a transcode: the first two re-time or re-encode the
// audio, and the server does not vouch for a rendition's audio bits.
func audioQualityFor(choices []StreamChoice, streams []store.MediaFileStream, chosen int, hlsAudio ffmpeg.AudioProfile, profile CapabilityProfile) *AudioQuality {
	if len(choices) == 0 || chosen < 0 {
		return nil
	}
	source, ok := audioStreamAt(streams, chosen)
	if !ok {
		return nil
	}
	quality := &AudioQuality{
		Codec:      source.Codec,
		SampleRate: source.SampleRate,
		BitDepth:   source.BitDepth,
		Lossless:   losslessAudioStream(source),
		Delivery:   AudioDeliveryTranscode,
	}
	primary := choices[0]
	switch {
	case primary.Kind == "direct-play":
		quality.Delivery = AudioDeliveryDirect
	case primary.Kind == "hls" && (strings.HasPrefix(primary.ID, "hls-") || strings.HasPrefix(primary.ID, "audio-copy-")):
		if hlsAudio.Codec == "copy" && passthroughCovers(profile, source) {
			quality.Delivery = AudioDeliveryDirect
		}
	}
	return quality
}

// passthroughCovers reports whether the client declared passthrough for the
// source stream's codec within its sample-rate and bit-depth ceilings, so a
// copied stream reaches it unchanged.
func passthroughCovers(profile CapabilityProfile, source store.MediaFileStream) bool {
	if !strings.EqualFold(profile.DownstreamAudio, "passthrough") {
		return false
	}
	codecCap, ok := passthroughAudioCodecCap(profile.AudioCodecs, source)
	return ok && !exceedsAudioResolution(codecCap, source)
}

func audioStreamAt(streams []store.MediaFileStream, index int) (store.MediaFileStream, bool) {
	for _, stream := range streams {
		if stream.Type == "audio" && stream.Index == index {
			return stream, true
		}
	}
	return store.MediaFileStream{}, false
}

// losslessAudioCodecs are the ffprobe codec names (as normalizedAudioCodec
// spells them) of lossless audio codecs, apart from the PCM and DSD families
// and DTS, which losslessAudioStream recognises by prefix and profile.
var losslessAudioCodecs = map[string]bool{
	"flac":        true,
	"alac":        true,
	"ape":         true,
	"wavpack":     true,
	"tta":         true,
	"tak":         true,
	"truehd":      true,
	"mlp":         true,
	"wmalossless": true,
	"shorten":     true,
	"dts-hd-ma":   true,
}

// losslessAudioStream reports whether the stream's codec is lossless. PCM is
// lossless except the companded G.711 variants (A-law, mu-law); DTS counts only
// with a DTS-HD Master Audio profile, since its core is lossy.
func losslessAudioStream(stream store.MediaFileStream) bool {
	codec := normalizedAudioCodec(stream.Codec)
	switch {
	case losslessAudioCodecs[codec]:
		return true
	case strings.HasPrefix(codec, "pcm-"):
		return codec != "pcm-alaw" && codec != "pcm-mulaw"
	case strings.HasPrefix(codec, "dsd-"):
		return true
	case codec == "dts" || codec == "dca":
		return dtsHDMasterAudioProfile(stream.Profile)
	}
	return false
}
