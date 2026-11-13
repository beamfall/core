package playback

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidRequest reports a client-supplied playback request whose shape is
	// invalid. Its text is deliberately safe for the HTTP layer to return.
	ErrInvalidRequest = errors.New("mediaID is required")
	// ErrInvalidSelector reports a malformed, forged, expired, or scope-mismatched
	// opaque selector. Details stay hidden because every case is the same
	// client-visible failure.
	ErrInvalidSelector = errors.New("invalid selector")
	// ErrNotFound reports a playback resource that may safely map to a generic
	// HTTP 404. Info/PreviewInfo still preserve their Reveal-safe unavailable
	// envelopes where absence is part of the successful response contract.
	ErrNotFound = errors.New("playback resource not found")
	// ErrSourceEnded reports an HLS segment that lies past where the media
	// source actually ends: the file is shorter than its probed duration, so the
	// synthesized playlist advertises segments no encoder can fill. It wraps
	// ErrNotFound because retrying can never produce the segment.
	ErrSourceEnded = fmt.Errorf("%w: source ended before this segment", ErrNotFound)
)
