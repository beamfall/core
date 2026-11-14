package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/serverrole"
	"github.com/beamfall/core/internal/store"
)

// attachedPictureReclassStore is the store surface behind the one-time repair
// of audio Media misclassed as video by their embedded cover art (CORE-1262).
type attachedPictureReclassStore interface {
	backfillQueuer
	Libraries(ctx context.Context, filter store.LibraryFilter) ([]store.Library, error)
	AttachedPictureReclassState(ctx context.Context, libraryID store.LibraryID) (string, error)
	SetAttachedPictureReclassState(ctx context.Context, libraryID store.LibraryID, state string, now time.Time) error
	AttachedPictureCandidatesInLibrary(ctx context.Context, libraryID store.LibraryID) ([]store.ReclassifyCandidate, error)
	ReclassifyVideoMediaAsAudio(ctx context.Context, id store.MediaID, audioCodec string) (bool, error)
}

// attachedPictureProber is the FFmpeg probe the repair re-reads files with.
type attachedPictureProber interface {
	Probe(ctx context.Context, path string) (ffmpeg.Probe, error)
}

// reclassifyAttachedPictureAudio repairs, once per Library, audio Media that a
// pre-CORE-1151 import classed as video because of embedded cover art. A rescan
// never reclassifies an unchanged file, so without it those tracks keep the
// video player and never hydrate. It re-probes only video-class Media whose
// stored codec is a still-image codec, moves each one the probe shows is audio
// with an attached picture, and queues a backfill scan so the hydration
// backfill fills their tags. The Library is recorded pending before its first
// reclassification and done only after that scan is queued: an interrupted
// repair (a stop, a cancelled context, a store error) runs again on the next
// start and still queues the scan for rows an earlier run already moved, and a
// reclassified row is never touched twice.
// An unreadable file is logged and left as it is, so it does not hold the
// marker back forever. A sealed encrypted Library is skipped until it is
// unsealed (the caller runs this again on unseal); an Attached node never
// authors catalog rows and skips the repair.
func reclassifyAttachedPictureAudio(ctx context.Context, st attachedPictureReclassStore, prober attachedPictureProber, logger *slog.Logger) {
	if prober == nil {
		return
	}
	libraries, err := st.Libraries(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		logger.Warn("attached-picture reclassification check failed; retrying next start", "error", err)
		return
	}
	for _, library := range libraries {
		if ctx.Err() != nil {
			return
		}
		state, err := st.AttachedPictureReclassState(ctx, library.ID)
		switch {
		case errors.Is(err, serverrole.ErrAttachedAuthorityRefused):
			return
		case err != nil:
			logger.Warn("attached-picture reclassification check failed; retrying next start", "library", library.ID, "error", err)
			continue
		case state == store.AttachedPictureReclassDone:
			continue
		}
		pending := state == store.AttachedPictureReclassPending
		reclassified, complete := reclassifyLibraryAttachedPictures(ctx, st, prober, library.ID, pending, logger)
		if !complete {
			continue
		}
		if pending || reclassified > 0 {
			if err := queueLibraryBackfill(ctx, st, library.ID); errors.Is(err, errNoLibraryRoots) {
				continue // nothing to scan yet; checked again once a root exists
			} else if err != nil {
				logger.Warn("attached-picture reclassification hydration scan not queued; retrying next start", "library", library.ID, "error", err)
				continue
			}
			logger.Info("reclassified audio media misclassed as video by embedded cover art", "library", library.ID, "media", reclassified)
		}
		if err := st.SetAttachedPictureReclassState(ctx, library.ID, store.AttachedPictureReclassDone, time.Now().UTC()); err != nil {
			logger.Warn("attached-picture reclassification check not recorded", "library", library.ID, "error", err)
		}
	}
}

// reclassifyLibraryAttachedPictures re-probes one Library's candidates,
// recording the Library pending before the first row moves unless it already
// is. complete is false when the pass stopped early (a cancelled context or a
// store error), so the caller leaves the Library unmarked.
func reclassifyLibraryAttachedPictures(ctx context.Context, st attachedPictureReclassStore, prober attachedPictureProber, libraryID store.LibraryID, pending bool, logger *slog.Logger) (int, bool) {
	candidates, err := st.AttachedPictureCandidatesInLibrary(ctx, libraryID)
	if errors.Is(err, store.ErrEncryptedLibraryDBDisabled) {
		return 0, false // sealed: repaired after its unseal
	}
	if err != nil {
		logger.Warn("attached-picture reclassification check failed; retrying next start", "library", libraryID, "error", err)
		return 0, false
	}
	reclassified := 0
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return reclassified, false
		}
		probe, err := prober.Probe(ctx, candidate.Path)
		if err != nil {
			if ctx.Err() != nil {
				return reclassified, false
			}
			logger.Warn("attached-picture reclassification probe failed; leaving the media as it is", "library", libraryID, "media", candidate.MediaID, "error", err)
			continue
		}
		if !probe.AudioWithAttachedPicture() {
			continue
		}
		if !pending {
			if err := st.SetAttachedPictureReclassState(ctx, libraryID, store.AttachedPictureReclassPending, time.Now().UTC()); err != nil {
				logger.Warn("attached-picture reclassification not started; retrying next start", "library", libraryID, "error", err)
				return reclassified, false
			}
			pending = true
		}
		changed, err := st.ReclassifyVideoMediaAsAudio(ctx, candidate.MediaID, probe.AudioCodec)
		if err != nil {
			logger.Warn("attached-picture reclassification failed; retrying next start", "library", libraryID, "media", candidate.MediaID, "error", err)
			return reclassified, false
		}
		if changed {
			reclassified++
		}
	}
	return reclassified, true
}
