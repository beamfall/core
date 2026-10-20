package graph

import (
	"context"
	"errors"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// SavedFilterInfo is the wire shape for one SavedFilter.
type SavedFilterInfo struct {
	ID         string `json:"id"`
	ProfileID  string `json:"profileID"`
	LibraryID  string `json:"libraryID"`
	Name       string `json:"name"`
	FilterJSON string `json:"filterJSON"`
	Pinned     bool   `json:"pinned"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

// SmartCollectionInfo is the wire shape for one SmartCollection.
type SmartCollectionInfo struct {
	ID         string `json:"id"`
	LibraryID  string `json:"libraryID"`
	Name       string `json:"name"`
	SourceExpr string `json:"sourceExpr"`
	SortExpr   string `json:"sortExpr"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

type CollectionInfo struct {
	ID          string                 `json:"id"`
	LibraryID   string                 `json:"libraryID"`
	ParentID    string                 `json:"parentID,omitempty"`
	Name        string                 `json:"name"`
	SortOrder   int                    `json:"sortOrder"`
	TrashedAt   string                 `json:"trashedAt,omitempty"`
	CreatedAt   string                 `json:"createdAt,omitempty"`
	UpdatedAt   string                 `json:"updatedAt,omitempty"`
	MemberCount int                    `json:"memberCount"`
	Members     []CollectionMemberInfo `json:"members"`
}

type CollectionMemberInfo struct {
	MediaID   string `json:"mediaID"`
	SortOrder int    `json:"sortOrder"`
	AddedAt   string `json:"addedAt,omitempty"`
}

func savedFilterInfo(f store.SavedFilter) SavedFilterInfo {
	return SavedFilterInfo{
		ID:         string(f.ID),
		ProfileID:  string(f.ProfileID),
		LibraryID:  string(f.LibraryID),
		Name:       f.Name,
		FilterJSON: f.FilterJSON,
		Pinned:     f.Pinned,
		CreatedAt:  formatOptionalTime(f.CreatedAt),
	}
}

func smartCollectionInfo(c store.SmartCollection) SmartCollectionInfo {
	return SmartCollectionInfo{
		ID:         string(c.ID),
		LibraryID:  string(c.LibraryID),
		Name:       c.Name,
		SourceExpr: c.SourceExpr,
		SortExpr:   c.SortExpr,
		CreatedAt:  formatOptionalTime(c.CreatedAt),
	}
}

func formatOptionalTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatOptionalTime(*value)
}

// collectionInfo builds the wire shape for a Collection with Reveal/Sealed
// visibility applied to its ordered static membership (ADR-0098 area 4):
// inaccessible/Sealed members are dropped before ordering and counting, so
// MemberCount and Members reflect only what this scope may see — they must not
// leak through counts, gaps, or ordering.
func collectionInfo(ctx context.Context, h *Handler, scope auth.Scope, c store.Collection) (CollectionInfo, error) {
	filter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return CollectionInfo{}, err
	}
	return collectionInfoWithFilter(ctx, h, c, filter)
}

// collectionInfoWithFilter is collectionInfo with the sealed filter already
// resolved, so list callers compute it once instead of once per Collection
// (sealedFilter issues a Libraries scan when encryption is wired).
func collectionInfoWithFilter(ctx context.Context, h *Handler, c store.Collection, filter store.LibraryFilter) (CollectionInfo, error) {
	members, err := h.store.ResolveCollectionMembers(ctx, c.ID, filter)
	if err != nil {
		return CollectionInfo{}, err
	}
	info := CollectionInfo{
		ID:          string(c.ID),
		LibraryID:   string(c.LibraryID),
		ParentID:    string(c.ParentID),
		Name:        c.Name,
		SortOrder:   c.SortOrder,
		TrashedAt:   formatOptionalTimePtr(c.TrashedAt),
		CreatedAt:   formatOptionalTime(c.CreatedAt),
		UpdatedAt:   formatOptionalTime(c.UpdatedAt),
		MemberCount: len(members),
		Members:     make([]CollectionMemberInfo, len(members)),
	}
	for i, member := range members {
		info.Members[i] = CollectionMemberInfo{
			MediaID:   string(member.MediaID),
			SortOrder: member.SortOrder,
			AddedAt:   formatOptionalTime(member.AddedAt),
		}
	}
	return info, nil
}

// savedFilters returns the current Profile's saved filters for a Library.
func (h *Handler) savedFilters(ctx context.Context, scope auth.Scope, req request) ([]SavedFilterInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return nil, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, clientError("unknown library")
	}
	filters, err := h.store.SavedFilters(ctx, scope.ProfileID, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	infos := make([]SavedFilterInfo, len(filters))
	for i, f := range filters {
		infos[i] = savedFilterInfo(f)
	}
	return infos, nil
}

// createSavedFilter creates a SavedFilter for the current Profile and a
// visible Library.
func (h *Handler) createSavedFilter(ctx context.Context, scope auth.Scope, req request) (SavedFilterInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	name := stringVar(req, "name")
	filterJSON := stringVar(req, "filterJSON")
	if libraryID == "" {
		return SavedFilterInfo{}, clientError("libraryID is required")
	}
	if name == "" {
		return SavedFilterInfo{}, clientError("name is required")
	}
	if filterJSON == "" {
		filterJSON = "{}"
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return SavedFilterInfo{}, err
	}
	if !visible {
		return SavedFilterInfo{}, clientError("unknown library")
	}
	f, err := h.store.CreateSavedFilter(ctx, store.SavedFilter{
		ProfileID:  scope.ProfileID,
		LibraryID:  store.LibraryID(libraryID),
		Name:       name,
		FilterJSON: filterJSON,
	}, time.Now().UTC())
	if err != nil {
		return SavedFilterInfo{}, err
	}
	return savedFilterInfo(f), nil
}

// deleteSavedFilter removes a SavedFilter owned by the current Profile.
func (h *Handler) deleteSavedFilter(ctx context.Context, scope auth.Scope, req request) (SavedFilterInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return SavedFilterInfo{}, clientError("id is required")
	}
	// Check ownership before deleting: a Profile must only delete its own filters.
	f, err := h.store.SavedFilterByID(ctx, store.SavedFilterID(id))
	if err != nil || f.ProfileID != scope.ProfileID {
		return SavedFilterInfo{}, clientError("unknown saved filter")
	}
	removed, err := h.store.DeleteSavedFilter(ctx, store.SavedFilterID(id))
	if err != nil {
		return SavedFilterInfo{}, clientError("unknown saved filter")
	}
	if err := h.logActivity(ctx, scope, store.ActivitySavedFilterDelete, "saved_filter", string(removed.ID), removed.LibraryID); err != nil {
		return SavedFilterInfo{}, err
	}
	return savedFilterInfo(removed), nil
}

// smartCollections returns all SmartCollections for a visible Library.
func (h *Handler) smartCollections(ctx context.Context, scope auth.Scope, req request) ([]SmartCollectionInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return nil, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, clientError("unknown library")
	}
	collections, err := h.store.SmartCollections(ctx, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	infos := make([]SmartCollectionInfo, len(collections))
	for i, c := range collections {
		infos[i] = smartCollectionInfo(c)
	}
	return infos, nil
}

func (h *Handler) collections(ctx context.Context, scope auth.Scope, req request) ([]CollectionInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return nil, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, clientError("unknown library")
	}
	collections, err := h.store.Collections(ctx, store.LibraryID(libraryID), boolVar(req, "includeTrashed"))
	if err != nil {
		return nil, err
	}
	filter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	infos := make([]CollectionInfo, len(collections))
	for i, c := range collections {
		info, err := collectionInfoWithFilter(ctx, h, c, filter)
		if err != nil {
			return nil, err
		}
		infos[i] = info
	}
	return infos, nil
}

// createSmartCollection creates a SmartCollection for a visible Library.
// Manager role is required (gated in execute dispatch block).
func (h *Handler) createSmartCollection(ctx context.Context, scope auth.Scope, req request) (SmartCollectionInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	name := stringVar(req, "name")
	sourceExpr := stringVar(req, "sourceExpr")
	sortExpr := stringVar(req, "sortExpr")
	if libraryID == "" {
		return SmartCollectionInfo{}, clientError("libraryID is required")
	}
	if name == "" {
		return SmartCollectionInfo{}, clientError("name is required")
	}
	if sourceExpr == "" {
		sourceExpr = "{}"
	}
	if sortExpr == "" {
		sortExpr = "{}"
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return SmartCollectionInfo{}, err
	}
	if !visible {
		return SmartCollectionInfo{}, clientError("unknown library")
	}
	c, err := h.store.CreateSmartCollection(ctx, store.SmartCollection{
		LibraryID:        store.LibraryID(libraryID),
		Name:             name,
		SourceExpr:       sourceExpr,
		SortExpr:         sortExpr,
		CreatedByProfile: scope.ProfileID, // ADR-0036: stamp authorship so My-Data export/disown is live
	}, time.Now().UTC())
	if err != nil {
		return SmartCollectionInfo{}, err
	}
	return smartCollectionInfo(c), nil
}

func (h *Handler) updateSmartCollection(ctx context.Context, scope auth.Scope, req request) (SmartCollectionInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return SmartCollectionInfo{}, clientError("id is required")
	}
	c, err := h.store.SmartCollectionByID(ctx, store.SmartCollectionID(id))
	if err != nil {
		return SmartCollectionInfo{}, clientError("unknown smart collection")
	}
	visible, err := h.visibleLibrary(ctx, scope, c.LibraryID)
	if err != nil {
		return SmartCollectionInfo{}, err
	}
	if !visible {
		return SmartCollectionInfo{}, clientError("unknown smart collection")
	}
	updated, err := h.store.UpdateSmartCollection(ctx, store.SmartCollection{
		ID:         store.SmartCollectionID(id),
		Name:       stringVar(req, "name"),
		SourceExpr: stringVar(req, "sourceExpr"),
		SortExpr:   stringVar(req, "sortExpr"),
	})
	if err != nil {
		return SmartCollectionInfo{}, err
	}
	return smartCollectionInfo(updated), nil
}

// deleteSmartCollection removes a SmartCollection for a visible Library.
// Manager role is required (gated in execute dispatch block).
func (h *Handler) deleteSmartCollection(ctx context.Context, scope auth.Scope, req request) (SmartCollectionInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return SmartCollectionInfo{}, clientError("id is required")
	}
	// Check library visibility before deleting — reveal gate.
	c, err := h.store.SmartCollectionByID(ctx, store.SmartCollectionID(id))
	if err != nil {
		return SmartCollectionInfo{}, clientError("unknown smart collection")
	}
	visible, err := h.visibleLibrary(ctx, scope, c.LibraryID)
	if err != nil {
		return SmartCollectionInfo{}, err
	}
	if !visible {
		return SmartCollectionInfo{}, clientError("unknown smart collection")
	}
	removed, err := h.store.DeleteSmartCollection(ctx, store.SmartCollectionID(id))
	if err != nil {
		return SmartCollectionInfo{}, clientError("unknown smart collection")
	}
	if err := h.logActivity(ctx, scope, store.ActivitySmartCollectionDelete, "smart_collection", string(removed.ID), removed.LibraryID); err != nil {
		return SmartCollectionInfo{}, err
	}
	return smartCollectionInfo(removed), nil
}

func (h *Handler) createCollection(ctx context.Context, scope auth.Scope, req request) (CollectionInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	name := stringVar(req, "name")
	if libraryID == "" {
		return CollectionInfo{}, clientError("libraryID is required")
	}
	if name == "" {
		return CollectionInfo{}, clientError("name is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return CollectionInfo{}, err
	}
	if !visible {
		return CollectionInfo{}, clientError("unknown library")
	}
	c, err := h.store.CreateCollection(ctx, store.Collection{
		LibraryID: store.LibraryID(libraryID),
		ParentID:  store.CollectionID(trimmedVariable(req, "parentID")),
		Name:      name,
		SortOrder: intVar(req, "sortOrder"),
	}, time.Now().UTC())
	if err != nil {
		return CollectionInfo{}, err
	}
	return collectionInfo(ctx, h, scope, c)
}

func (h *Handler) updateCollection(ctx context.Context, scope auth.Scope, req request) (CollectionInfo, error) {
	id := store.CollectionID(trimmedVariable(req, "id"))
	if id == "" {
		return CollectionInfo{}, clientError("id is required")
	}
	current, err := h.store.CollectionByID(ctx, id, true)
	if err != nil {
		return CollectionInfo{}, clientError("unknown collection")
	}
	visible, err := h.visibleLibrary(ctx, scope, current.LibraryID)
	if err != nil {
		return CollectionInfo{}, err
	}
	if !visible {
		return CollectionInfo{}, clientError("unknown collection")
	}
	patch := store.CollectionPatch{IncludeTrash: true}
	if _, ok := req.Variables["name"]; ok {
		name := stringVar(req, "name")
		patch.Name = &name
	}
	if _, ok := req.Variables["parentID"]; ok {
		parent := store.CollectionID(trimmedVariable(req, "parentID"))
		if parent == "" {
			patch.MoveToRoot = true
		} else {
			patch.ParentID = &parent
		}
	}
	if _, ok := req.Variables["sortOrder"]; ok {
		sortOrder := intVar(req, "sortOrder")
		patch.SortOrder = &sortOrder
	}
	updated, err := h.store.UpdateCollection(ctx, id, patch, time.Now().UTC())
	if err != nil {
		return CollectionInfo{}, err
	}
	return collectionInfo(ctx, h, scope, updated)
}

func (h *Handler) deleteCollection(ctx context.Context, scope auth.Scope, req request) (CollectionInfo, error) {
	id := store.CollectionID(trimmedVariable(req, "id"))
	if id == "" {
		return CollectionInfo{}, clientError("id is required")
	}
	current, err := h.store.CollectionByID(ctx, id, true)
	if err != nil {
		return CollectionInfo{}, clientError("unknown collection")
	}
	visible, err := h.visibleLibrary(ctx, scope, current.LibraryID)
	if err != nil {
		return CollectionInfo{}, err
	}
	if !visible {
		return CollectionInfo{}, clientError("unknown collection")
	}
	removed, err := h.store.TrashCollection(ctx, id, time.Now().UTC())
	if err != nil {
		return CollectionInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityCollectionTrash, "collection", string(removed.ID), removed.LibraryID); err != nil {
		return CollectionInfo{}, err
	}
	return collectionInfo(ctx, h, scope, removed)
}

func (h *Handler) restoreCollection(ctx context.Context, scope auth.Scope, req request) (CollectionInfo, error) {
	id := store.CollectionID(trimmedVariable(req, "id"))
	if id == "" {
		return CollectionInfo{}, clientError("id is required")
	}
	current, err := h.store.CollectionByID(ctx, id, true)
	if err != nil {
		return CollectionInfo{}, clientError("unknown collection")
	}
	visible, err := h.visibleLibrary(ctx, scope, current.LibraryID)
	if err != nil {
		return CollectionInfo{}, err
	}
	if !visible {
		return CollectionInfo{}, clientError("unknown collection")
	}
	restored, err := h.store.RestoreCollection(ctx, id, time.Now().UTC())
	if err != nil {
		return CollectionInfo{}, err
	}
	return collectionInfo(ctx, h, scope, restored)
}

func (h *Handler) addCollectionMedia(ctx context.Context, scope auth.Scope, req request) (CollectionInfo, error) {
	id := store.CollectionID(trimmedVariable(req, "collectionID"))
	if id == "" {
		id = store.CollectionID(trimmedVariable(req, "id"))
	}
	updated, err := h.store.AddCollectionMedia(ctx, id, mediaIDsFromVariables(req, "mediaIDs"), scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrCollectionNotFound) {
			return CollectionInfo{}, clientError("unknown collection")
		}
		return CollectionInfo{}, err
	}
	visible, err := h.visibleLibrary(ctx, scope, updated.LibraryID)
	if err != nil {
		return CollectionInfo{}, err
	}
	if !visible {
		return CollectionInfo{}, clientError("unknown collection")
	}
	return collectionInfo(ctx, h, scope, updated)
}

func (h *Handler) removeCollectionMedia(ctx context.Context, scope auth.Scope, req request) (CollectionInfo, error) {
	id := store.CollectionID(trimmedVariable(req, "collectionID"))
	if id == "" {
		id = store.CollectionID(trimmedVariable(req, "id"))
	}
	updated, err := h.store.RemoveCollectionMedia(ctx, id, mediaIDsFromVariables(req, "mediaIDs"), scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrCollectionNotFound) {
			return CollectionInfo{}, clientError("unknown collection")
		}
		return CollectionInfo{}, err
	}
	visible, err := h.visibleLibrary(ctx, scope, updated.LibraryID)
	if err != nil {
		return CollectionInfo{}, err
	}
	if !visible {
		return CollectionInfo{}, clientError("unknown collection")
	}
	return collectionInfo(ctx, h, scope, updated)
}

func (h *Handler) reorderCollectionMedia(ctx context.Context, scope auth.Scope, req request) (CollectionInfo, error) {
	id := store.CollectionID(trimmedVariable(req, "collectionID"))
	if id == "" {
		id = store.CollectionID(trimmedVariable(req, "id"))
	}
	updated, err := h.store.ReorderCollectionMedia(ctx, id, mediaIDsFromVariables(req, "mediaIDs"), scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrCollectionNotFound) {
			return CollectionInfo{}, clientError("unknown collection")
		}
		return CollectionInfo{}, err
	}
	visible, err := h.visibleLibrary(ctx, scope, updated.LibraryID)
	if err != nil {
		return CollectionInfo{}, err
	}
	if !visible {
		return CollectionInfo{}, clientError("unknown collection")
	}
	return collectionInfo(ctx, h, scope, updated)
}

func mediaIDsFromVariables(req request, name string) []store.MediaID {
	raw := stringSliceVar(req, name)
	ids := make([]store.MediaID, 0, len(raw))
	for _, value := range raw {
		ids = append(ids, store.MediaID(value))
	}
	return ids
}
