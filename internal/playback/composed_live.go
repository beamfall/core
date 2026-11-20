package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// ErrComposedPictureUnavailable reports a composed-picture stream whose picture
// no longer resolves (the plugin declines it now, or no composer is wired). The
// grant was minted when it did; the byte layer refuses rather than silently
// serving a different stream under the same URL.
var ErrComposedPictureUnavailable = errors.New("composed picture is unavailable")

// StreamShape is the signed grant's reshaping of an HLS session beyond its
// ladder, decided at mint and carried by the byte layer. The zero value shapes
// nothing.
type StreamShape struct {
	// LibraryID attributes the live directory to its authoritative Library.
	LibraryID store.LibraryID

	// ComposedPicture replaces the session's ladder with one composed rung
	// that draws the Media's composed picture over its audio: the live form
	// of a precomputed composed rendition (ADR-0057).
	ComposedPicture bool
	// Adjust is the pitch shift and tempo change the session encodes. A
	// non-zero adjustment without a composed picture replaces the ladder with
	// one audio-only rung, and stretches the session's timeline by the tempo.
	Adjust ffmpeg.AudioAdjust
	// AudioOnly replaces the session's ladder with one audio-only rung: the
	// Media is audio-class, so any picture stream it carries is embedded art,
	// not video to re-encode. A composed picture still takes precedence.
	AudioOnly bool
	// AudioSource is the source audio stream the session maps
	// (AudioSourceStream). It sizes the audio-only rung's BANDWIDTH to match
	// what the mint advertised (CORE-1849). The zero value takes the worst
	// case for a copied stream.
	AudioSource store.MediaFileStream

	// Encrypted, when set, is the plaintext source of a session on an
	// Encrypted Library: the session keeps only encrypted files and FFmpeg
	// reaches the source through a loopback endpoint (ADR-0236). It is set by
	// the byte layer, never carried in a grant.
	Encrypted *EncryptedSource
}

// StreamShapeFromGrant reads the StreamShape a stream grant carries for
// media, the Media the grant names.
func StreamShapeFromGrant(grant streamtoken.Grant, media store.Media) StreamShape {
	return StreamShape{
		ComposedPicture: grant.ComposedPicture,
		Adjust:          grantAudioAdjust(grant),
		AudioOnly:       media.Class == store.MediaClassAudio,
	}
}

// SetPictureComposer wires the resolver composed-picture sessions draw their
// picture from. Without one, such sessions are refused.
func (s *Sessions) SetPictureComposer(composer GenerationComposer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.composer = composer
}

// livePicture resolves the picture a composed-picture session draws. It runs
// outside the sessions lock (resolving reads plugin sidecars) and is skipped
// when the key already has a composed session for src, so only the request
// that creates a session pays for it.
func (s *Sessions) livePicture(ctx context.Context, key SessionKey, src string, shape StreamShape) (*ComposedPicture, error) {
	if !shape.ComposedPicture {
		return nil, nil
	}
	s.mu.Lock()
	composer := s.composer
	var resolved *ComposedPicture
	if existing, ok := s.sessions[key]; ok && existing.src == src && existing.composed != nil {
		resolved = existing.composed
	}
	s.mu.Unlock()
	if resolved != nil {
		return resolved, nil
	}
	if composer == nil {
		return nil, ErrComposedPictureUnavailable
	}
	picture, ok, err := composer(ctx, key.MediaID, src)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrComposedPictureUnavailable
	}
	return &picture, nil
}

// writeLivePicture lays a resolved picture out for the encoder: a graphics
// track is read in place beside the source, and timed text is written into the
// rung directory, inside the encode's read-write confinement.
func writeLivePicture(dir string, picture ComposedPicture) (*ffmpeg.ComposedPicture, error) {
	if picture.TimedText == "" {
		return &ffmpeg.ComposedPicture{GraphicsPath: picture.GraphicsPath}, nil
	}
	path := filepath.Join(dir, ComposedRungID, composedTimedTextFile)
	if err := os.WriteFile(path, []byte(picture.TimedText), 0o600); err != nil {
		return nil, err
	}
	return &ffmpeg.ComposedPicture{TimedTextPath: path}, nil
}

// composedLiveRungs is the closed vocabulary of live composed shapes, best
// first, each named by its video maxrate. The 720p shape encodes at the
// precomputed composed rendition's maxrate; the smaller ones serve a client or
// quirk height or bitrate limit with the same picture scaled down
// (ffmpeg.ComposedCanvas).
var composedLiveRungs = []Rung{
	composedRung(composedHeight, composedVideoKbps),
	composedRung(480, 1000),
	composedRung(360, 600),
}

// composedRung is one live composed shape. Its Bandwidth is the whole peak
// stream rate, video plus the stereo-AAC-and-mux allowance the transcode
// ladder reserves (bandwidthFor), so a bitrate limit is held against what is
// actually sent and the video maxrate never takes the audio's share.
func composedRung(height, videoKbps int) Rung {
	width, height := ffmpeg.ComposedCanvas(height)
	return Rung{
		ID:        ComposedRungID,
		Height:    height,
		Width:     width,
		VideoKbps: videoKbps,
		Bandwidth: bandwidthFor(videoKbps),
	}
}

// composedLiveRungWithin is the best live composed shape no taller than
// maxHeight (0 = no height limit) and with a peak bandwidth no higher than
// maxBitrate (0 = no bitrate limit). ok is false when none fits.
func composedLiveRungWithin(maxHeight, maxBitrate int) (Rung, bool) {
	for _, rung := range composedLiveRungs {
		if (maxHeight > 0 && rung.Height > maxHeight) || (maxBitrate > 0 && rung.Bandwidth > maxBitrate) {
			continue
		}
		return rung, true
	}
	return Rung{}, false
}

// composedLiveRung is the single rung of a composed-picture session: the shape
// decided at mint, which the grant carries as its client height ceiling.
func composedLiveRung(caps ladderCaps) (Rung, error) {
	rung, ok := composedLiveRungWithin(caps.ClientMaxHeight, 0)
	if !ok {
		return Rung{}, ErrComposedPictureUnavailable
	}
	return rung, nil
}
