package playback

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/streamtoken"
)

// PlaybackAdjustment is a client-requested pitch shift and tempo change for
// an audio Media's playback. The pitch moves without changing the tempo, and
// the tempo without changing the pitch. Zero fields change nothing: an omitted
// tempo is the recorded speed.
type PlaybackAdjustment struct {
	// PitchShiftSemitones is the key change in whole semitones, from
	// ffmpeg.MinPitchShiftSemitones to ffmpeg.MaxPitchShiftSemitones.
	PitchShiftSemitones int `json:"pitchShiftSemitones,omitempty"`
	// Tempo is the speed factor, from ffmpeg.MinTempo to ffmpeg.MaxTempo; 1
	// (or omitted) is the recorded speed.
	Tempo float64 `json:"tempo,omitempty"`
}

// validate rejects an adjustment outside the accepted ranges.
func (a PlaybackAdjustment) validate() error {
	if a.PitchShiftSemitones < ffmpeg.MinPitchShiftSemitones || a.PitchShiftSemitones > ffmpeg.MaxPitchShiftSemitones {
		return invalidRequestError(fmt.Sprintf("adjustment.pitchShiftSemitones must be between %d and %d", ffmpeg.MinPitchShiftSemitones, ffmpeg.MaxPitchShiftSemitones))
	}
	if a.Tempo != 0 && (math.IsNaN(a.Tempo) || a.Tempo < ffmpeg.MinTempo || a.Tempo > ffmpeg.MaxTempo) {
		return invalidRequestError(fmt.Sprintf("adjustment.tempo must be between %g and %g", ffmpeg.MinTempo, ffmpeg.MaxTempo))
	}
	return nil
}

// audioAdjust is the encoder form of the adjustment.
func (a PlaybackAdjustment) audioAdjust() ffmpeg.AudioAdjust {
	return ffmpeg.AudioAdjust{PitchSemitones: a.PitchShiftSemitones, Tempo: a.Tempo}
}

// playbackAdjustment is the wire form of an applied encoder adjustment: nil
// when it changes nothing.
func playbackAdjustment(adjust ffmpeg.AudioAdjust) *PlaybackAdjustment {
	if adjust.IsZero() {
		return nil
	}
	tempo := adjust.Tempo
	if tempo == 1 {
		tempo = 0
	}
	return &PlaybackAdjustment{PitchShiftSemitones: adjust.PitchSemitones, Tempo: tempo}
}

// withAudioAdjustGrant binds an adjustment to a stream grant, so the byte
// layer encodes it and a different adjustment cannot reuse the session.
func withAudioAdjustGrant(grant streamtoken.Grant, adjust ffmpeg.AudioAdjust) streamtoken.Grant {
	grant.PitchShiftSemitones = adjust.PitchSemitones
	grant.Tempo = 0
	if adjust.Tempo != 1 {
		grant.Tempo = adjust.Tempo
	}
	return grant
}

// audioAdjustSupported reports whether the resolved FFmpeg build can run the
// audio encode of an adjusted rendition. It is a variable so a test can state
// the build it means, like composedRecipeSupported.
var audioAdjustSupported = ffmpeg.AudioAdjustSupported

// adjustedAudioRendition is the audio side of an adjusted encode: what decides
// the audio filters and encoder the command names.
func adjustedAudioRendition(audio ffmpeg.AudioProfile, adjust ffmpeg.AudioAdjust) ffmpeg.Rendition {
	return ffmpeg.Rendition{AudioCodec: audio.Codec, AudioChannels: audio.Channels, AudioGainDB: audio.GainDB}.WithAudioAdjust(adjust)
}

// grantAudioAdjust reads the adjustment a stream grant carries.
func grantAudioAdjust(grant streamtoken.Grant) ffmpeg.AudioAdjust {
	return ffmpeg.AudioAdjust{PitchSemitones: grant.PitchShiftSemitones, Tempo: grant.Tempo}
}

// adjustedAudioChoice mints the HLS stream for an adjusted audio Media
// without a composed picture: an audio-only encode carrying the grant's
// adjustment.
func (s *Service) adjustedAudioChoice(ctx context.Context, grant streamtoken.Grant, suffix string, audio ffmpeg.AudioProfile, expiresAt time.Time) (StreamChoice, error) {
	grant.Target = streamtoken.TargetMediaHLS
	grant = withHLSAudioGrant(grant, audio)
	token, err := s.tokens.Issue(ctx, grant)
	if err != nil {
		return StreamChoice{}, err
	}
	rung := audioOnlyRung()
	return StreamChoice{
		ID:         "adjusted-" + suffix,
		Kind:       "hls",
		URL:        "/api/media/hls/" + token + "/" + masterPlaylistName,
		Token:      token,
		ExpiresAt:  expiresAt.Format(time.RFC3339),
		Renditions: []RenditionInfo{{ID: rung.ID, Bandwidth: rung.Bandwidth}},
	}, nil
}
