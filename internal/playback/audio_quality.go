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
// is choices[0], playing the source audio stream at index chosen under the
// decision's pitch/tempo adjustment. It returns nil when there is no stream or
// no chosen audio track to describe.
//
// Delivery follows what happens to the audio. The file itself (direct play)
// delivers it bit-for-bit. A live transcode does when it copies the audio
// stream: an "hls-" or "audio-copy-" choice, or a live "composed-" picture,
// whose audio profile is a copy of a track the client declared it passes
// through within its ceilings, with no adjustment, which forces a re-encode
// (ffmpeg.Rendition.audioArgs). A live composed picture maps the file's first
// audio stream, so it counts only when that is the chosen track. Anything else
// is a transcode, including a precomputed rendition, whose audio bits the
// server does not vouch are the source's.
func audioQualityFor(choices []StreamChoice, streams []store.MediaFileStream, chosen int, hlsAudio ffmpeg.AudioProfile, adjust ffmpeg.AudioAdjust, profile CapabilityProfile) *AudioQuality {
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
	if audioDeliveredDirect(choices[0], streams, source, hlsAudio, adjust, profile) {
		quality.Delivery = AudioDeliveryDirect
	}
	return quality
}

// audioDeliveredDirect reports whether the primary stream carries the source
// audio stream unchanged: direct play, or a live transcode copying that
// stream to a client that declared passthrough for it.
func audioDeliveredDirect(primary StreamChoice, streams []store.MediaFileStream, source store.MediaFileStream, hlsAudio ffmpeg.AudioProfile, adjust ffmpeg.AudioAdjust, profile CapabilityProfile) bool {
	if primary.Kind == "direct-play" {
		return true
	}
	if primary.Kind != "hls" || hlsAudio.Codec != "copy" || !adjust.IsZero() || !passthroughCovers(profile, source) {
		return false
	}
	switch {
	case strings.HasPrefix(primary.ID, "hls-"), strings.HasPrefix(primary.ID, "audio-copy-"):
		return true
	case strings.HasPrefix(primary.ID, "composed-"):
		return firstAudioIndex(streams) == source.Index
	}
	return false
}

// firstAudioIndex is the absolute index of the file's first audio stream, the
// one a live composed picture maps, or -1 when the file has none.
func firstAudioIndex(streams []store.MediaFileStream) int {
	first := -1
	for _, stream := range streams {
		if stream.Type == "audio" && (first < 0 || stream.Index < first) {
			first = stream.Index
		}
	}
	return first
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
