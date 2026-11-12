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
// Exactly one must be set; the store enforces that.
func itemTarget(req request) store.ItemTarget {
	return store.ItemTarget{
		MediaID:  store.MediaID(trimmedVariable(req, "mediaID")),
		EntityID: store.EntityID(trimmedVariable(req, "entityID")),
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
	return h.itemStateFor(ctx, scope, itemTarget(req))
}

func (h *Handler) itemStateFor(ctx context.Context, scope auth.Scope, target store.ItemTarget) (ItemStateInfo, error) {
	state, err := h.store.ItemStateFor(ctx, scope.ProfileID, target, scope.LibraryFilter())
	if err != nil {
		return ItemStateInfo{}, itemActionError(err)
	}
	return ItemStateInfo{
		ID: state.ID, Kind: state.Kind,
		Watched: state.Watched, Watchlisted: state.Watchlisted, Rating: state.Rating,
	}, nil
}

// itemActionDone tells the Profile's other sessions to refetch and returns the
// state the write left behind. The invalidation is best-effort: the write has
// already committed, and a client that misses the event still reads the new
// state on its next load.
func (h *Handler) itemActionDone(ctx context.Context, scope auth.Scope, target store.ItemTarget) (ItemStateInfo, error) {
	_ = h.store.AppendInvalidationEvent(ctx, store.InvalidationPayload{
		ProfileID: scope.ProfileID,
		Reason:    store.InvalidationReasonEngagementChanged,
	}, time.Now().UTC())
	return h.itemStateFor(ctx, scope, target)
}

func (h *Handler) setWatched(ctx context.Context, scope auth.Scope, req request) (ItemStateInfo, error) {
	target := itemTarget(req)
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
	target := itemTarget(req)
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

func (h *Handler) setRating(ctx context.Context, scope auth.Scope, req request) (ItemStateInfo, error) {
	target := itemTarget(req)
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
