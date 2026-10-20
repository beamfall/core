package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/beamfall/core/internal/playback"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// pictureComposerHook is the optional parser method a plugin implements to
// describe a picture for Media that have none (ADR-0057 composed renditions):
// a graphics sidecar path, or a timed-text (ASS) document. It is declared here,
// at its consumer, and discovered by type assertion on the plugin's compiled
// parser — the PartMarkerTitle pattern — so Core names no plugin and the
// plugin needs nothing beyond basic types.
type pictureComposerHook interface {
	ComposedPicture(path string) (graphicsPath, timedText string, ok bool)
}

// pictureComposers resolves a Media's composed picture through its Library's
// plugin binding.
type pictureComposers struct {
	store   *store.Store
	parsers map[string]scanpkg.FilenameParser
}

// newPictureComposers returns nil when no activated parser can compose, so
// playback and precompute skip the seam entirely.
func newPictureComposers(st *store.Store, parsers map[string]scanpkg.FilenameParser) *pictureComposers {
	for _, parser := range parsers {
		if _, ok := parser.(pictureComposerHook); ok {
			return &pictureComposers{store: st, parsers: parsers}
		}
	}
	return nil
}

func (c *pictureComposers) forMedia(ctx context.Context, media store.Media, sourcePath string) (playback.ComposedPicture, bool, error) {
	pluginID, err := c.store.LibraryPluginID(ctx, media.LibraryID)
	if err != nil || pluginID == "" {
		return playback.ComposedPicture{}, false, err
	}
	hook, ok := c.parsers[pluginID].(pictureComposerHook)
	if !ok {
		return playback.ComposedPicture{}, false, nil
	}
	graphics, text, ok := hook.ComposedPicture(sourcePath)
	if !ok {
		return playback.ComposedPicture{}, false, nil
	}
	return playback.ComposedPicture{GraphicsPath: graphics, TimedText: text}, true, nil
}

func (c *pictureComposers) forMediaID(ctx context.Context, mediaID store.MediaID, sourcePath string) (playback.ComposedPicture, bool, error) {
	media, err := c.store.Media(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return playback.ComposedPicture{}, false, err
	}
	return c.forMedia(ctx, media, sourcePath)
}

// playbackComposer and generationComposer adapt a possibly-nil set into the
// playback seams (nil disables the feature there).
func (c *pictureComposers) playbackComposer() playback.PictureComposer {
	if c == nil {
		return nil
	}
	return c.forMedia
}

func (c *pictureComposers) generationComposer() playback.GenerationComposer {
	if c == nil {
		return nil
	}
	return c.forMediaID
}

// composedSweepLimit caps how many composed renders may wait in the job queue
// at once. A sweep enqueues only up to the headroom left by renders still
// queued, running, or yielded (admission yields a job while the cache budget is
// full), so adding a large library spreads its renders over several sweeps
// and a full budget cannot pile up jobs.
const composedSweepLimit = 200

// sweep enqueues the missing composed rendition of every Media in a Library
// whose plugin composes pictures (ADR-0057 §2: the seeded Schedule is the
// explicit trigger). One Media's failure is logged and skipped so it cannot
// stall the rest; disk use stays governed by the precompute admission budget.
// It walks every Media of those Libraries each run (resolving the picture to
// detect edited sidecars), which is cheap at songbook sizes.
func (c *pictureComposers) sweep(ctx context.Context, cacheRoot string, now time.Time, logger *slog.Logger) (int, error) {
	if c == nil {
		return 0, nil
	}
	pending, err := c.pendingPrecomputeJobs(ctx)
	if err != nil {
		return 0, err
	}
	headroom := composedSweepLimit - pending
	libraries, err := c.store.Libraries(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return 0, err
	}
	enqueued, skipped := 0, 0
	defer func() {
		if logger != nil && (enqueued > 0 || skipped > 0) {
			logger.Info("composed rendition sweep", "enqueued", enqueued, "skipped", skipped, "pending", pending)
		}
	}()
	for _, library := range libraries {
		if library.PluginID == nil {
			continue
		}
		if _, ok := c.parsers[*library.PluginID].(pictureComposerHook); !ok {
			continue
		}
		mediaIDs, err := c.store.MediaIDsForLibrary(ctx, library.ID)
		if err != nil {
			return enqueued, err
		}
		for _, mediaID := range mediaIDs {
			if err := ctx.Err(); err != nil {
				return enqueued, err
			}
			if enqueued >= headroom {
				return enqueued, nil
			}
			added, err := playback.EnsureComposedRendition(ctx, c.store, mediaID, c.forMedia, cacheRoot, now)
			if err != nil {
				skipped++
				if logger != nil {
					logger.Warn("composed rendition sweep skipped media", "media", mediaID, "error", err)
				}
				continue
			}
			if added {
				enqueued++
			}
		}
	}
	return enqueued, nil
}

// pendingPrecomputeJobs counts precompute renders not yet finished.
func (c *pictureComposers) pendingPrecomputeJobs(ctx context.Context) (int, error) {
	active, err := c.store.ListJobs(ctx, store.JobStatusQueued, store.JobStatusRunning, store.JobStatusYielded)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, job := range active {
		if job.Kind == playback.PrecomputeJobKind {
			n++
		}
	}
	return n, nil
}
