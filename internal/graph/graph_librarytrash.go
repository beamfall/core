package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// TrashedLibraryInfo is one soft-deleted Library on the wire (CORE-1201).
type TrashedLibraryInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	TrashedAt string `json:"trashedAt"`
	// MediaCount is the number of Media a restore brings back.
	MediaCount int `json:"mediaCount"`
	// Purgeable is false for a Custodial Library, which holds the only copy of
	// its media, and for an encrypted one, whose private catalog a purge does
	// not remove; neither is removed permanently.
	Purgeable bool `json:"purgeable"`
	// PurgeAfter is when the retention sweep removes the Library; empty when it
	// is not Purgeable.
	PurgeAfter string `json:"purgeAfter"`
}

// LibraryPurgeResult reports what a permanent Library removal took with it.
type LibraryPurgeResult struct {
	LibraryID       string `json:"libraryID"`
	Title           string `json:"title"`
	MediaRemoved    int    `json:"mediaRemoved"`
	EntitiesRemoved int    `json:"entitiesRemoved"`
}

// trashedLibraries lists the soft-deleted Libraries the scope may see, most
// recently deleted first. Admin-Session gated in execute().
func (h *Handler) trashedLibraries(ctx context.Context, scope auth.Scope) ([]TrashedLibraryInfo, error) {
	trashed, err := h.store.TrashedLibraries(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	resp := make([]TrashedLibraryInfo, len(trashed))
	for i, entry := range trashed {
		info := TrashedLibraryInfo{
			ID:         string(entry.Library.ID),
			Title:      entry.Library.Title,
			TrashedAt:  entry.TrashedAt.UTC().Format(time.RFC3339),
			MediaCount: entry.MediaCount,
			Purgeable:  entry.Purgeable(),
		}
		if purgeAfter, ok := entry.PurgeAfter(); ok {
			info.PurgeAfter = purgeAfter.UTC().Format(time.RFC3339)
		}
		resp[i] = info
	}
	return resp, nil
}

// trashedLibrary resolves a soft-deleted Library through the scope's own
// trash listing, so a Library the scope cannot see is indistinguishable from
// one that does not exist.
func (h *Handler) trashedLibrary(ctx context.Context, scope auth.Scope, id store.LibraryID) (store.TrashedLibrary, bool, error) {
	trashed, err := h.store.TrashedLibraries(ctx, scope.LibraryFilter())
	if err != nil {
		return store.TrashedLibrary{}, false, err
	}
	for _, entry := range trashed {
		if entry.Library.ID == id {
			return entry, true, nil
		}
	}
	return store.TrashedLibrary{}, false, nil
}

// restoreLibrary brings a soft-deleted Library back. Its Media, watch history,
// grants, root folders and scan schedule were never removed, so they return
// with it; the observer then resumes watching and queues a rescan.
// Admin-Session gated in execute().
func (h *Handler) restoreLibrary(ctx context.Context, scope auth.Scope, req request) error {
	libraryID := store.LibraryID(trimmedVariable(req, "libraryID"))
	if libraryID == "" {
		return clientError("libraryID is required")
	}
	if _, found, err := h.trashedLibrary(ctx, scope, libraryID); err != nil {
		return err
	} else if !found {
		return clientError("unknown deleted library")
	}
	if err := h.store.RestoreLibrary(ctx, libraryID); err != nil {
		return err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRestore, "library", string(libraryID), libraryID); err != nil {
		return err
	}
	if h.libraryObserver != nil {
		h.libraryObserver.LibraryRestored(ctx, libraryID)
	}
	return nil
}

// purgeLibrary permanently removes a Library that is already soft-deleted, and
// reports what went with it. It removes catalog, history and schedule records
// only: no file under the Library's root folders is touched. A live Library is
// refused, so no single call goes from a working Library to an unrecoverable
// one. Admin-Session gated in execute().
func (h *Handler) purgeLibrary(ctx context.Context, scope auth.Scope, req request) (LibraryPurgeResult, error) {
	libraryID := store.LibraryID(trimmedVariable(req, "libraryID"))
	if libraryID == "" {
		return LibraryPurgeResult{}, clientError("libraryID is required")
	}
	v := newVars(req)
	confirm, ok := v.Bool("confirm")
	if err := v.Err(); err != nil {
		return LibraryPurgeResult{}, err
	}
	if !ok || !confirm {
		return LibraryPurgeResult{}, clientError("confirm is required to delete a library permanently")
	}
	if _, found, err := h.trashedLibrary(ctx, scope, libraryID); err != nil {
		return LibraryPurgeResult{}, err
	} else if !found {
		return LibraryPurgeResult{}, clientError("unknown deleted library")
	}
	purge, err := h.store.PurgeLibrary(ctx, libraryID)
	if err != nil {
		return LibraryPurgeResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryPurge, "library", string(libraryID), purge.ActivityLibraryID()); err != nil {
		return LibraryPurgeResult{}, err
	}
	if h.libraryObserver != nil {
		h.libraryObserver.LibraryPurged(ctx, purge)
	}
	return LibraryPurgeResult{
		LibraryID:       string(purge.LibraryID),
		Title:           purge.Title,
		MediaRemoved:    len(purge.MediaIDs),
		EntitiesRemoved: len(purge.EntityIDs),
	}, nil
}
