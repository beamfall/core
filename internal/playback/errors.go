package playback

import "errors"

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
)
