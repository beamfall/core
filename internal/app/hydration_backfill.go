package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/beamfall/core/internal/graph"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/serverrole"
	"github.com/beamfall/core/internal/store"
)

// hydrationBackfillStore is the store surface behind the one-time hydration
// backfill of Libraries whose Media predate their plugin's mapper.
type hydrationBackfillStore interface {
	Libraries(ctx context.Context, filter store.LibraryFilter) ([]store.Library, error)
	HydrationBackfillChecked(ctx context.Context, libraryID store.LibraryID, markerField string) (bool, error)
	MarkHydrationBackfillChecked(ctx context.Context, libraryID store.LibraryID, markerField string, now time.Time) error
	MediaLackingFieldInLibrary(ctx context.Context, libraryID store.LibraryID, class store.MediaClass, fieldName string) (map[store.MediaID]struct{}, error)
	LibraryRoots(ctx context.Context, libraryID store.LibraryID) ([]string, error)
	EnqueueJob(ctx context.Context, job store.Job) (store.Job, error)
}

// queueHydrationBackfills checks each Library of a plugin with a metadata
// hydrator once per marker field. An ordinary scan never hydrates an
// unchanged file, so Media imported before the mapper existed would otherwise
// never be hydrated: when any Media of a hydrated class lacks the marker, a
// backfill scan is queued for every root of the Library. The check is
// recorded after the scans are queued, so a crash in between checks again on
// the next start, and a file the backfill cannot hydrate (an unreadable probe)
// does not queue another scan on every start. A failure only delays the check
// to the next start; an Attached node never authors catalog rows and skips it.
// A Library without roots stays unchecked, and so does an encrypted Library
// whose database is still sealed: the check runs after the boot unseal and
// again after each runtime unseal (hydrationBackfillOnUnseal).
func queueHydrationBackfills(ctx context.Context, st hydrationBackfillStore, hydrators map[string]scanpkg.MetadataHydrator, logger *slog.Logger) {
	if len(hydrators) == 0 {
		return
	}
	libraries, err := st.Libraries(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		logger.Warn("hydration backfill check failed; retrying next start", "error", err)
		return
	}
	for _, library := range libraries {
		if library.PluginID == nil {
			continue
		}
		hydrator := hydrators[*library.PluginID]
		if hydrator == nil {
			continue
		}
		capability := hydrator.Capability()
		if capability.MarkerField == "" {
			continue
		}
		checked, err := st.HydrationBackfillChecked(ctx, library.ID, capability.MarkerField)
		switch {
		case errors.Is(err, serverrole.ErrAttachedAuthorityRefused):
			return
		case err != nil:
			logger.Warn("hydration backfill check failed; retrying next start", "library", library.ID, "error", err)
			continue
		case checked:
			continue
		}
		lacking, err := mediaLackingMarker(ctx, st, library.ID, capability)
		if err != nil {
			logger.Warn("hydration backfill check failed; retrying next start", "library", library.ID, "error", err)
			continue
		}
		if lacking {
			if err := queueLibraryBackfill(ctx, st, library.ID); errors.Is(err, errNoLibraryRoots) {
				continue // nothing to scan yet; checked again once a root exists
			} else if err != nil {
				logger.Warn("hydration backfill scan not queued; retrying next start", "library", library.ID, "error", err)
				continue
			}
			logger.Info("hydration backfill queued for media imported before the plugin's mapper", "library", library.ID, "plugin", *library.PluginID)
		}
		if err := st.MarkHydrationBackfillChecked(ctx, library.ID, capability.MarkerField, time.Now().UTC()); err != nil {
			logger.Warn("hydration backfill check not recorded", "library", library.ID, "error", err)
		}
	}
}

func mediaLackingMarker(ctx context.Context, st hydrationBackfillStore, libraryID store.LibraryID, capability scanpkg.MetadataHydrationCapability) (bool, error) {
	for _, class := range capability.MediaClasses {
		if class == "" {
			continue
		}
		lacking, err := st.MediaLackingFieldInLibrary(ctx, libraryID, class, capability.MarkerField)
		if err != nil {
			return false, err
		}
		if len(lacking) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// encryptedLibraryDBLifecycle is the store surface the graph handler uses for
// encrypted Library databases, including the staged-provision abort it
// asserts for.
type encryptedLibraryDBLifecycle interface {
	graph.EncryptedLibraryDBLifecycle
	AbortEncryptedLibraryDBProvision(ctx context.Context, libraryID store.LibraryID) error
}

// hydrationBackfillOnUnseal runs the hydration backfill check after an
// encrypted Library's database is unsealed at runtime. A sealed Library's
// Media are unreadable at start, so its check is deferred until an operator
// unseals it.
type hydrationBackfillOnUnseal struct {
	encryptedLibraryDBLifecycle
	backfill func(context.Context)
}

func (l hydrationBackfillOnUnseal) UnsealEncryptedLibraryDB(ctx context.Context, libraryID store.LibraryID, cipher store.EncryptedLibraryDBCipher) error {
	if err := l.encryptedLibraryDBLifecycle.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		return err
	}
	l.backfill(context.WithoutCancel(ctx))
	return nil
}
