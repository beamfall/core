package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// LibraryFolderEntry is one folder of a Library folder view: its path, the
// name to display for it and how many visible Media sit at or below it.
type LibraryFolderEntry struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	MediaCount int    `json:"mediaCount"`
}

// LibraryFolderCrumb is one step from a Library root down to the open folder.
type LibraryFolderCrumb struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// LibraryFolderResponse is one folder of a Library (CORE-2359): its
// subfolders, one page of the Media directly in it and the crumbs back to its
// root. Path and Crumbs are empty when the response lists the Library's roots;
// RootCount says how many roots that list holds, so a client links back to it
// only when there is more than one. Total counts the folder's direct Media.
type LibraryFolderResponse struct {
	Path      string               `json:"path"`
	RootCount int                  `json:"rootCount"`
	Crumbs    []LibraryFolderCrumb `json:"crumbs"`
	Folders   []LibraryFolderEntry `json:"folders"`
	Total     int                  `json:"total"`
	Items     []SourceItem         `json:"items"`
}

func emptyLibraryFolder() LibraryFolderResponse {
	return LibraryFolderResponse{Crumbs: []LibraryFolderCrumb{}, Folders: []LibraryFolderEntry{}, Items: []SourceItem{}}
}

// libraryFolder answers one folder of a Library the caller may browse. It is
// read from the catalog with the Library browse's visibility, so it never
// names a path outside the Library's roots or an item a rail would hide.
func (h *Handler) libraryFolder(ctx context.Context, scope auth.Scope, req request) (LibraryFolderResponse, error) {
	if h.store == nil {
		return emptyLibraryFolder(), nil
	}
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return LibraryFolderResponse{}, clientError("libraryID is required")
	}
	result, err := h.store.LibraryFolder(ctx, scope.LibraryFilter(), store.LibraryFolderQuery{
		LibraryID: store.LibraryID(libraryID),
		ProfileID: scope.ProfileID,
		Path:      stringVar(req, "path"),
		Limit:     boundedIntVar(req, "limit", store.LibraryFolderPageDefault, store.LibraryFolderPageMax),
		Offset:    nonNegativeIntVar(req, "offset"),
	})
	if err != nil {
		return LibraryFolderResponse{}, err
	}
	if err := h.annotateCards(ctx, scope, result.Items); err != nil {
		return LibraryFolderResponse{}, err
	}
	resp := emptyLibraryFolder()
	resp.Path = result.Path
	resp.RootCount = result.RootCount
	resp.Total = result.Total
	for _, crumb := range result.Crumbs {
		resp.Crumbs = append(resp.Crumbs, LibraryFolderCrumb{Path: crumb.Path, Name: crumb.Name})
	}
	for _, folder := range result.Folders {
		resp.Folders = append(resp.Folders, LibraryFolderEntry{Path: folder.Path, Name: folder.Name, MediaCount: folder.MediaCount})
	}
	resp.Items = make([]SourceItem, len(result.Items))
	for i, item := range result.Items {
		resp.Items[i] = h.sourceItem(item, scope.Revealed)
	}
	return resp, nil
}
