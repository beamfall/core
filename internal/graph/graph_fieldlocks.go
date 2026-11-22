package graph

import (
	"context"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Per-field source-precedence locks, graph half. These
// are Manage-area mutations over one Media's metadata-field locks: a set lock
// pins the field's value against re-scans and scraper passes. Manager-role
// gated like the other Console curation mutations; reads and writes are
// reveal-and-Library filtered through scope.LibraryFilter(), so a hidden Media
// is "not found" — never an existence oracle. The fields are named by the
// generic metadata shape (title / summary / year), never by domain (invariant
// #1).

// FieldLockInfo is one Media field's source-precedence lock state on the wire.
type FieldLockInfo struct {
	Field  string `json:"field"`
	Locked bool   `json:"locked"`
}

func fieldLockInfos(locks []store.MediaFieldLock) []FieldLockInfo {
	out := make([]FieldLockInfo, len(locks))
	for i, lock := range locks {
		out[i] = FieldLockInfo{Field: lock.Field, Locked: lock.Locked}
	}
	return out
}

// mediaFieldLocks resolves the read: the per-field lock state for one visible
// Media, in the store's stable field order.
func (h *Handler) mediaFieldLocks(ctx context.Context, scope auth.Scope, req request) ([]FieldLockInfo, error) {
	if h.store == nil {
		return []FieldLockInfo{}, nil
	}
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	locks, err := h.store.MediaFieldLocks(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	return fieldLockInfos(locks), nil
}

// manualArtworkReverter gives an artwork slot back from an operator pick.
type manualArtworkReverter interface {
	RevertManualArtwork(ctx context.Context, id store.MediaID, slot string) error
}

// artworkSlotForLockField returns the artwork slot whose lock field is field.
func artworkSlotForLockField(field string) (string, bool) {
	for _, slot := range artwork.KnownSlots {
		if store.ArtworkLockField(slot) == field {
			return slot, true
		}
	}
	return "", false
}

// setMediaFieldLock resolves the write: set or clear one field's lock on a
// visible Media, returning the refreshed lock set. The lock decision routes
// through the audited activity log as a media field write. revert, with
// locked false on an artwork field, also gives the slot back from the
// operator's pick (a plain unlock keeps the picked image until a refresh
// replaces it): the slot's applied candidate is cleared and the sidecar, or
// the provider's image on the next refresh, takes the slot again.
func (h *Handler) setMediaFieldLock(ctx context.Context, scope auth.Scope, req request) ([]FieldLockInfo, error) {
	if h.store == nil {
		return nil, clientError("store unavailable")
	}
	mediaID := trimmedVariable(req, "mediaID")
	field := trimmedVariable(req, "field")
	if mediaID == "" || field == "" {
		return nil, clientError("mediaID and field are required")
	}
	locked, ok := newVars(req).Bool("locked")
	if !ok {
		return nil, clientError("locked is required")
	}
	revert, _ := newVars(req).Bool("revert")
	locks, err := h.store.SetMediaFieldLock(ctx, store.MediaID(mediaID), field, locked, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	slot, isArtwork := artworkSlotForLockField(field)
	revertArtwork := revert && !locked && isArtwork
	if revertArtwork {
		if err := h.store.ClearArtworkSlotApplied(ctx, store.MediaID(mediaID), slot); err != nil {
			return nil, err
		}
	}
	// A failed Library lookup still logs the decision, without the Library.
	library, _ := h.store.MediaLibrary(ctx, store.MediaID(mediaID))
	if err := h.logActivity(ctx, scope, store.ActivityMediaFieldLock, "media", mediaID, library); err != nil {
		return nil, err
	}
	if reverter, ok := h.candidateImport.(manualArtworkReverter); ok && revertArtwork {
		if err := reverter.RevertManualArtwork(ctx, store.MediaID(mediaID), slot); err != nil {
			return nil, err
		}
	}
	return fieldLockInfos(locks), nil
}
