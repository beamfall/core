// Package decodeverify runs the decode-verify sampling job (FEAT-8 / ADR-0009
// §9): a low-priority background sweep that sample-decodes each playable Media
// item's bytes via the hardened FFmpeg seam and flags the corrupt or truncated
// ones as broken in Library Health. A file whose bytes still resolve
// on disk but won't decode is "broken" in the generic, domain-agnostic sense —
// it cannot play — so the verdict folds into the existing `broken` health
// dimension rather than adding a new one (invariant #1/#3).
//
// The sweep mirrors genassets' sweep shape: a deduped background job that
// checkpoints by Media ID, decodes a bounded sample per file (cheap on a large
// library), and self-heals — a file that decodes cleanly on a later pass clears
// any prior broken flag. The composition root gates the handler behind the
// resource governor so a realtime transcode always preempts it.
package decodeverify

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/store"
)

// Verifier is decodeverify's slice of the FFmpeg seam (interface at the
// consumer; the real *ffmpeg.Runner implements it). DecodeVerify sample-decodes
// src and reports whether the bytes are broken (corrupt/truncated). An error is
// an inability to RUN ffmpeg, never a corrupt-file verdict — so a misconfigured
// host can never mass-flag a healthy library.
type Verifier interface {
	DecodeVerify(ctx context.Context, src string, durationSeconds int) (bool, error)
}

// Service flags decode-broken Media by sample-decoding each item's primary file.
type Service struct {
	store    *store.Store
	verifier Verifier
}

type mediaVerifyError struct {
	id  store.MediaID
	err error
}

func (e mediaVerifyError) Error() string {
	return "decode verify failed for media " + string(e.id)
}

func (e mediaVerifyError) Unwrap() error {
	return e.err
}

// NewService creates a decode-verify service over the catalog store and FFmpeg
// seam.
func NewService(st *store.Store, verifier Verifier) *Service {
	return &Service{store: st, verifier: verifier}
}

// serverFilter is the server-internal read scope: the sweep sees every Library;
// reveal filtering happens at the serving boundary (like genassets and artwork).
var serverFilter = store.LibraryFilter{Revealed: true, AllLibraries: true}

// timeNow is injected so the handler controls the checked-at clock (and tests
// pin it), mirroring how the rest of the job substrate threads `now`.
type timeNow func() time.Time

// VerifyMedia sample-decodes one Media item's primary file and records the
// result on its file row(s): broken=true on a decode error/truncation,
// broken=false when it decodes cleanly (self-healing). ok=false means the item
// was skipped — not a decodable class (image), or no available file (already
// broken on the "bytes are gone" branch) — so there was nothing for the
// decode-verify branch to add and no flag was written. A failure to run FFmpeg
// (not a corrupt-file verdict) is returned as an error and never flags the file.
func (s *Service) VerifyMedia(ctx context.Context, id store.MediaID, now timeNow) (broken, ok bool, err error) {
	if s.verifier == nil {
		return false, false, nil
	}
	media, err := s.store.Media(ctx, id, serverFilter)
	if err != nil {
		return false, false, err
	}
	// Image-class Media has no decodable timeline; only video/audio bytes are
	// decode-verifiable. A zero/negative runtime means the duration is unknown —
	// still decodable from the start, so it is not excluded here.
	if media.Class != store.MediaClassVideo && media.Class != store.MediaClassAudio {
		return false, false, nil
	}
	file, err := s.store.PrimaryMediaFile(ctx, id, serverFilter)
	if err != nil || file.Path == "" {
		// No available (non-missing) file: the Media is already broken on the
		// "bytes are gone" branch; nothing for decode-verify to do.
		return false, false, nil
	}
	broken, err = s.verifier.DecodeVerify(ctx, file.Path, media.DurationSeconds)
	if err != nil {
		return false, false, mediaVerifyError{id: id, err: err}
	}
	if _, err := s.store.SetMediaFileDecodeBroken(ctx, id, broken, now()); err != nil {
		return false, false, err
	}
	return broken, true, nil
}
