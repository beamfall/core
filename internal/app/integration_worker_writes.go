package app

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/store"
)

// integrationWatchStateWriter adapts the engagement write for the
// write-watch-state Host method (CORE-1695). It marks one media item for the
// grant's bound Profile and tells that Profile's clients to refresh. It does not
// notify watched-state observers (scrobblers): a Worker that syncs watch state
// from an outside service must not have its own write echoed back to it. The
// invalidation is stamped with the Host's now, which binds it to the Worker's
// causation chain.
type integrationWatchStateWriter struct {
	st *store.Store
}

func (w integrationWatchStateWriter) SetWatched(ctx context.Context, profileID store.ProfileID, mediaID store.MediaID, watched bool, filter store.LibraryFilter, now time.Time) error {
	if _, err := w.st.SetItemWatched(ctx, profileID, store.ItemTarget{MediaID: mediaID}, watched, filter); err != nil {
		return err
	}
	// Best effort, as on the client mutation path: the mark is committed, and a
	// failed refresh hint must not make the Worker retry a write that landed.
	_ = w.st.AppendInvalidationEvent(ctx, store.InvalidationPayload{
		ProfileID: profileID,
		Reason:    store.InvalidationReasonEngagementChanged,
	}, now)
	return nil
}

// setArtworkWriter attaches the artwork service to every supervised worker's
// Host deps. Call it before Run; a nil writer leaves write-artwork dormant.
func (m *integrationManager) setArtworkWriter(w integrationhost.ArtworkWriter) {
	if m == nil || w == nil {
		return
	}
	m.mu.Lock()
	m.deps.Artwork = w
	m.mu.Unlock()
	for _, worker := range m.workers {
		worker.setArtworkWriter(w)
	}
}
