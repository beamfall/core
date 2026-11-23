package graph

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// ItemStateInfo is the calling Profile's own state for one Media or Entity.
type ItemStateInfo struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Watched     bool   `json:"watched"`
	Watchlisted bool   `json:"watchlisted"`
	Rating      int    `json:"rating"`
}

// itemTarget reads the mediaID / entityID pair every item action takes.
// Exactly one must be set; the store enforces that. An Entity target reaches
// its descendants only along the parent relations and container nestings the
// plugins declare, so marking a container marks the Media of its parts.
func (h *Handler) itemTarget(req request) store.ItemTarget {
	return store.ItemTarget{
		MediaID:         store.MediaID(trimmedVariable(req, "mediaID")),
		EntityID:        store.EntityID(trimmedVariable(req, "entityID")),
		ParentRelations: h.plugins.EntityParentRelations(),
		ContainerNests:  h.containerNests,
	}
}

// itemActionError keeps a store validation message and hides every other
// cause, so a hidden target and a missing one read the same to the caller.
func itemActionError(err error) error {
	var verr store.ValidationError
	if errors.As(err, &verr) {
		return clientError(string(verr))
	}
	return clientError("unknown item")
}

func (h *Handler) itemState(ctx context.Context, scope auth.Scope, req request) (ItemStateInfo, error) {
	return h.itemStateFor(ctx, scope, h.itemTarget(req))
}

func (h *Handler) itemStateFor(ctx context.Context, scope auth.Scope, target store.ItemTarget) (ItemStateInfo, error) {
	if scope.IsPlayerBound() && !scope.WalkUpEnabled {
		if target.MediaID == "" || target.EntityID != "" {
			return ItemStateInfo{}, clientError("walk-up browsing is disabled")
		}
		if err := h.requireWalkUpOrCommandedMedia(ctx, scope, target.MediaID); err != nil {
			return ItemStateInfo{}, err
		}
	}
	state, err := h.store.ItemStateFor(ctx, scope.ProfileID, target, scope.LibraryFilter())
	if err != nil {
		return ItemStateInfo{}, itemActionError(err)
	}
	return itemStateInfo(state), nil
}

func itemStateInfo(state store.ItemState) ItemStateInfo {
	return ItemStateInfo{
		ID: state.ID, Kind: state.Kind,
		Watched: state.Watched, Watchlisted: state.Watchlisted, Rating: state.Rating,
	}
}

// itemActionDone tells the Profile's other sessions to refetch and returns the
// state the write left behind. The invalidation is best-effort: the write has
// already committed, and a client that misses the event still reads the new
// state on its next load.
func (h *Handler) itemActionDone(ctx context.Context, scope auth.Scope, target store.ItemTarget) (ItemStateInfo, error) {
	h.notifyEngagementChanged(ctx, scope)
	return h.itemStateFor(ctx, scope, target)
}

func (h *Handler) notifyEngagementChanged(ctx context.Context, scope auth.Scope) {
	// Advance the playback sequence so a heartbeat that read its prior state
	// before this change sees the gap and keeps the Profile dirty.
	h.advancePlaybackSeq(scope.ProfileID)
	_ = h.store.AppendInvalidationEvent(ctx, store.InvalidationPayload{
		ProfileID: scope.ProfileID,
		Reason:    store.InvalidationReasonEngagementChanged,
	}, time.Now().UTC())
}

func (h *Handler) setWatched(ctx context.Context, scope auth.Scope, req request) (ItemStateInfo, error) {
	target := h.itemTarget(req)
	v := newVars(req)
	watched, ok := v.Bool("watched")
	if err := v.Err(); err != nil {
		return ItemStateInfo{}, err
	}
	if !ok {
		return ItemStateInfo{}, clientError("watched is required")
	}
	changed, err := h.store.SetItemWatched(ctx, scope.ProfileID, target, watched, scope.LibraryFilter())
	if err != nil {
		return ItemStateInfo{}, itemActionError(err)
	}
	// Only a single-Media mark reaches the watched observer. Marking a whole
	// tree is bookkeeping, not a play, and must not flood an outbound service
	// with one submission per descendant.
	if h.watchedObserver != nil && target.MediaID != "" && len(changed) == 1 {
		if media, err := h.store.Media(ctx, changed[0], scope.LibraryFilter()); err == nil {
			h.notifyWatched(ctx, scope.ProfileID, media)
		}
	}
	return h.itemActionDone(ctx, scope, target)
}

func (h *Handler) setWatchlisted(ctx context.Context, scope auth.Scope, req request) (ItemStateInfo, error) {
	target := h.itemTarget(req)
	v := newVars(req)
	listed, ok := v.Bool("watchlisted")
	if err := v.Err(); err != nil {
		return ItemStateInfo{}, err
	}
	if !ok {
		return ItemStateInfo{}, clientError("watchlisted is required")
	}
	if err := h.store.SetItemWatchlisted(ctx, scope.ProfileID, target, listed, scope.LibraryFilter()); err != nil {
		return ItemStateInfo{}, itemActionError(err)
	}
	return h.itemActionDone(ctx, scope, target)
}

// setContinueWatchingHidden removes one Media from the calling Profile's
// Continue Watching shelf (hidden: true) or puts it back (hidden: false, the
// web's Undo). Progress is kept; playing the Media again also puts it back.
// It honours the optional expectedProfileID precondition (CORE-2177): an Undo
// made for one Profile is refused once the session is bound to another.
func (h *Handler) setContinueWatchingHidden(ctx context.Context, scope auth.Scope, req request) (ItemStateInfo, error) {
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	v := newVars(req)
	hidden, ok := v.Bool("hidden")
	if err := v.Err(); err != nil {
		return ItemStateInfo{}, err
	}
	if !ok {
		return ItemStateInfo{}, clientError("hidden is required")
	}
	if mediaID == "" {
		return ItemStateInfo{}, clientError("mediaID is required")
	}
	// The store reads the response state inside the write, so a library that
	// locks after the commit cannot turn a saved removal into a reported failure.
	var state store.ItemState
	err := h.withExpectedProfile(ctx, scope, req, func() error {
		var err error
		state, err = h.store.SetContinueWatchingHidden(ctx, scope.ProfileID, mediaID, hidden, scope.LibraryFilter())
		if err != nil {
			return itemActionError(err)
		}
		return nil
	})
	if err != nil {
		return ItemStateInfo{}, err
	}
	h.notifyEngagementChanged(ctx, scope)
	return itemStateInfo(state), nil
}

func (h *Handler) setRating(ctx context.Context, scope auth.Scope, req request) (ItemStateInfo, error) {
	target := h.itemTarget(req)
	v := newVars(req)
	rating, ok := v.Number("rating")
	if err := v.Err(); err != nil {
		return ItemStateInfo{}, err
	}
	if !ok {
		return ItemStateInfo{}, clientError("rating is required")
	}
	if rating != math.Trunc(rating) || rating < 0 || rating > store.ItemRatingMax {
		return ItemStateInfo{}, clientError("rating must be a whole number between 0 and 10")
	}
	if err := h.store.SetItemRating(ctx, scope.ProfileID, target, int(rating), scope.LibraryFilter()); err != nil {
		return ItemStateInfo{}, itemActionError(err)
	}
	return h.itemActionDone(ctx, scope, target)
}
