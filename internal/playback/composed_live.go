package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/beamfall/core/internal/ffmpeg"
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
	// ComposedPicture replaces the session's ladder with one composed rung
	// that draws the Media's composed picture over its audio: the live form
	// of a precomputed composed rendition (ADR-0057).
	ComposedPicture bool
}

// StreamShapeFromGrant reads the StreamShape a stream grant carries.
func StreamShapeFromGrant(grant streamtoken.Grant) StreamShape {
	return StreamShape{ComposedPicture: grant.ComposedPicture}
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
func writeLivePicture(rungDir string, picture ComposedPicture) (*ffmpeg.ComposedPicture, error) {
	if picture.TimedText == "" {
		return &ffmpeg.ComposedPicture{GraphicsPath: picture.GraphicsPath}, nil
	}
	path := filepath.Join(rungDir, composedTimedTextFile)
	if err := os.WriteFile(path, []byte(picture.TimedText), 0o600); err != nil {
		return nil, err
	}
	return &ffmpeg.ComposedPicture{TimedTextPath: path}, nil
}

// composedLiveRung is the single rung of a composed-picture session, matching
// the precomputed composed rendition's shape.
func composedLiveRung() Rung {
	return Rung{
		ID:        ComposedRungID,
		Height:    composedHeight,
		Width:     composedHeight * 16 / 9,
		VideoKbps: composedBandwidth / 1000,
		Bandwidth: composedBandwidth,
	}
}
