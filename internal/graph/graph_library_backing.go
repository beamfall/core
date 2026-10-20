package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

func (h *Handler) setLibraryBacking(ctx context.Context, scope auth.Scope, req request) (LibraryInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return LibraryInfo{}, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return LibraryInfo{}, err
	}
	if !visible {
		return LibraryInfo{}, clientError("unknown library")
	}
	v := newVars(req)
	custodial, hasCustodial := v.Bool("custodial")
	encrypted, hasEncrypted := v.Bool("encrypted")
	if err := v.Err(); err != nil {
		return LibraryInfo{}, err
	}
	if !hasCustodial {
		return LibraryInfo{}, clientError("custodial is required")
	}
	if !hasEncrypted {
		return LibraryInfo{}, clientError("encrypted is required")
	}
	if !encrypted && h.libEncrypt != nil && h.libEncrypt.Provisioned(libraryID) {
		return LibraryInfo{}, clientError("encrypted library is already provisioned")
	}
	if err := h.store.SetLibraryBacking(ctx, store.LibraryID(libraryID), custodial, encrypted); err != nil {
		return LibraryInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "library-backing", libraryID, store.LibraryID(libraryID)); err != nil {
		return LibraryInfo{}, err
	}
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return LibraryInfo{}, err
	}
	for _, library := range libraries {
		if library.ID == store.LibraryID(libraryID) {
			return h.libraryInfo(ctx, library)
		}
	}
	return LibraryInfo{}, clientError("unknown library")
}
