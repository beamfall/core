package graph

import (
	"context"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// File-operation resolvers (ADR-0009 §5–6): preview-first organize, move, and
// merge over Media files, sharing the dry-run / batch-undo envelope with bulk
// edit (undoBatch lives in graph_bulkedit.go and reverses every batch kind).
// Dispatch is a self-contained, manager-gated group in graph.go's execute().
//
// These are MANAGER-role manage-area mutations that touch files on disk. Every
// read and write is reveal-and-Library filtered through scope.LibraryFilter().
// organizePreview returns the old→new path change-set without touching disk;
// organizeApply moves the bytes (mkdir + rename — the content fingerprint is
// unchanged, so it is a move, never delete+create) and journals the prior path;
// mergeApply routes redundant duplicates through the audited Trash path
// (invariant #6 — recoverable, never hard-deleted) and journals each trashed id.

// FileChangeInfo is one Media's path-move preview row.
type FileChangeInfo struct {
	MediaID string `json:"mediaID"`
	Title   string `json:"title"`
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
}

// FolderEntryInfo is one node in the manager-visible server folder tree.
type FolderEntryInfo struct {
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	Kind       string            `json:"kind"`
	Size       int64             `json:"size"`
	ModifiedAt string            `json:"modifiedAt"`
	Children   []FolderEntryInfo `json:"children,omitempty"`
}

type PathPickerInfo struct {
	Runtime           string                `json:"runtime"`
	Roles             []string              `json:"roles"`
	Role              string                `json:"role"`
	PurposeKey        string                `json:"purposeKey"`
	Roots             []PathPickerRootInfo  `json:"roots"`
	Entries           []PathPickerEntryInfo `json:"entries"`
	PreviewTimeoutMs  int                   `json:"previewTimeoutMs"`
	MaxPreviewEntries int                   `json:"maxPreviewEntries"`
}

type PathPickerRootInfo struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	EntryClass string `json:"entryClass"`
}

type PathPickerEntryInfo struct {
	Name              string                `json:"name"`
	Path              string                `json:"path"`
	Kind              string                `json:"kind"`
	AccessError       string                `json:"accessError"`
	EntryClass        string                `json:"entryClass"`
	Selectable        bool                  `json:"selectable"`
	ReasonCode        string                `json:"reasonCode,omitempty"`
	MediaCount        int                   `json:"mediaCount"`
	SampleNames       []string              `json:"sampleNames,omitempty"`
	PreviewTimeoutMs  int                   `json:"previewTimeoutMs"`
	MaxPreviewEntries int                   `json:"maxPreviewEntries"`
	Truncated         bool                  `json:"truncated"`
	Children          []PathPickerEntryInfo `json:"children,omitempty"`
}

// ManagedPathInfo is a folder/file path after a managed filesystem mutation.
type ManagedPathInfo struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// ManagedTrashInfo is the recoverable managed folder/file delete result.
type ManagedTrashInfo struct {
	TrashID      string `json:"trashID"`
	OriginalPath string `json:"originalPath"`
	Kind         string `json:"kind"`
}

func fileChangeInfo(change store.FileChange) FileChangeInfo {
	return FileChangeInfo{
		MediaID: string(change.MediaID),
		Title:   change.Title,
		OldPath: change.OldPath,
		NewPath: change.NewPath,
	}
}

func managedPathInfo(path store.ManagedPath) ManagedPathInfo {
	return ManagedPathInfo{Path: path.Path, Kind: path.Kind}
}

func managedTrashInfo(trash store.ManagedTrash) ManagedTrashInfo {
	return ManagedTrashInfo{TrashID: trash.TrashID, OriginalPath: trash.OriginalPath, Kind: trash.Kind}
}

func folderEntryInfo(entry store.FolderEntry) FolderEntryInfo {
	modifiedAt := ""
	if !entry.ModifiedAt.IsZero() {
		modifiedAt = entry.ModifiedAt.UTC().Format(time.RFC3339)
	}
	info := FolderEntryInfo{
		Name:       entry.Name,
		Path:       entry.Path,
		Kind:       entry.Kind,
		Size:       entry.Size,
		ModifiedAt: modifiedAt,
	}
	if len(entry.Children) > 0 {
		info.Children = make([]FolderEntryInfo, len(entry.Children))
		for i, child := range entry.Children {
			info.Children[i] = folderEntryInfo(child)
		}
	}
	return info
}

func pathPickerInfo(contract store.PathPickerContract) PathPickerInfo {
	roots := make([]PathPickerRootInfo, len(contract.Roots))
	for i, root := range contract.Roots {
		roots[i] = PathPickerRootInfo{Name: root.Name, Path: root.Path, EntryClass: root.EntryClass}
	}
	entries := make([]PathPickerEntryInfo, len(contract.Entries))
	for i, entry := range contract.Entries {
		entries[i] = pathPickerEntryInfo(entry)
	}
	return PathPickerInfo{
		Runtime:           contract.Runtime,
		Roles:             contract.Roles,
		Role:              contract.Role,
		PurposeKey:        contract.PurposeKey,
		Roots:             roots,
		Entries:           entries,
		PreviewTimeoutMs:  contract.PreviewTimeoutMs,
		MaxPreviewEntries: contract.MaxPreviewEntries,
	}
}

func pathPickerEntryInfo(entry store.PathPickerEntry) PathPickerEntryInfo {
	info := PathPickerEntryInfo{
		Name:              entry.Name,
		Path:              entry.Path,
		Kind:              entry.Kind,
		AccessError:       entry.AccessError,
		EntryClass:        entry.EntryClass,
		Selectable:        entry.Selectable,
		ReasonCode:        entry.ReasonCode,
		MediaCount:        entry.MediaCount,
		SampleNames:       entry.SampleNames,
		PreviewTimeoutMs:  entry.PreviewTimeoutMs,
		MaxPreviewEntries: entry.MaxPreviewEntries,
		Truncated:         entry.Truncated,
	}
	if len(entry.Children) > 0 {
		info.Children = make([]PathPickerEntryInfo, len(entry.Children))
		for i, child := range entry.Children {
			info.Children[i] = pathPickerEntryInfo(child)
		}
	}
	return info
}

// folderTree resolves a bounded read-only folder/file tree for a visible
// Library's configured roots. It powers the Manage picker without granting
// arbitrary filesystem browsing outside Library roots.
func (h *Handler) folderTree(ctx context.Context, scope auth.Scope, req request) (FolderEntryInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return FolderEntryInfo{}, clientError("libraryID is required")
	}
	root := stringVar(req, "root")
	depth := 1
	v := newVars(req)
	if number, ok := v.Number("depth"); ok {
		depth = boundedNonNegativeInt(int(number), 3)
	}
	if err := v.Err(); err != nil {
		return FolderEntryInfo{}, err
	}
	entry, err := h.store.FolderTree(ctx, store.LibraryID(libraryID), root, depth, scope.LibraryFilter())
	if err != nil {
		return FolderEntryInfo{}, err
	}
	return folderEntryInfo(entry), nil
}

// browseServerFolders resolves a bounded, read-only, directories-only view of
// the server filesystem for the first-run setup folder picker. Unlike
// folderTree it is NOT Library-scoped — at setup time no Library exists yet — so
// the caller is gated on the MANAGER role at the dispatcher. An empty path lists
// the filesystem root; a non-empty path is canonicalized and listed one bounded
// level deep.
func (h *Handler) browseServerFolders(ctx context.Context, scope auth.Scope, req request) (FolderEntryInfo, error) {
	path := stringVar(req, "path")
	depth := 1
	v := newVars(req)
	if number, ok := v.Number("depth"); ok {
		depth = boundedNonNegativeInt(int(number), 3)
	}
	if err := v.Err(); err != nil {
		return FolderEntryInfo{}, err
	}
	entry, err := h.store.BrowseServerFolders(ctx, path, depth)
	if err != nil {
		return FolderEntryInfo{}, err
	}
	return folderEntryInfo(entry), nil
}

func (h *Handler) pathPicker(ctx context.Context, scope auth.Scope, req request) (PathPickerInfo, error) {
	path := stringVar(req, "path")
	role := trimmedVariable(req, "role")
	libraryID := store.LibraryID(trimmedVariable(req, "libraryID"))
	distributionClass, err := store.NarrowDeviceDistributionClass(scope.DistributionClass, trimmedVariable(req, "distributionClass"))
	if err != nil {
		return PathPickerInfo{}, err
	}
	depth := 1
	v := newVars(req)
	if number, ok := v.Number("depth"); ok {
		depth = boundedNonNegativeInt(int(number), 3)
	}
	if err := v.Err(); err != nil {
		return PathPickerInfo{}, err
	}
	contract, err := h.store.PathPicker(ctx, store.PathPickerRequest{
		Role:              role,
		Path:              path,
		LibraryID:         libraryID,
		DistributionClass: string(distributionClass),
		Depth:             depth,
	})
	if err != nil {
		return PathPickerInfo{}, err
	}
	return pathPickerInfo(contract), nil
}

func (h *Handler) addPathPickerRoot(ctx context.Context, scope auth.Scope, req request) (PathPickerRootInfo, error) {
	path := stringVar(req, "path")
	if path == "" {
		return PathPickerRootInfo{}, clientError("path is required")
	}
	root, err := h.store.AddPathPickerRoot(ctx, path)
	if err != nil {
		return PathPickerRootInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityPathPickerRootAdded, "path", root.Path, ""); err != nil {
		return PathPickerRootInfo{}, err
	}
	return PathPickerRootInfo{Name: root.Name, Path: root.Path, EntryClass: root.EntryClass}, nil
}

// registerImportSource records an out-of-Library source locator and mints the
// SERVER-side import token the native authority hands back to the JS realm (apple
// ADR-0003 §1/§3). It is reachable ONLY over the authorized loopback bearer — the
// dispatch gates it on the Admin Session class (the bearer the native Companion
// holds, ADR-0003 §3), so the JS realm cannot self-mint a token. The locator is
// the security-scoped URL the native side already resolved; the store binds it to
// an fd at import time, so a path-level swap after registration is
// inert. The response carries only the opaque token, never the path back out.
func (h *Handler) registerImportSource(ctx context.Context, scope auth.Scope, req request) (string, error) {
	sourcePath := stringVar(req, "sourcePath")
	if sourcePath == "" {
		return "", clientError("sourcePath is required")
	}
	token, err := h.store.RegisterImportSource(sourcePath)
	if err != nil {
		return "", err
	}
	return token, nil
}

// importManagedFile stages an out-of-Library source — addressed by the
// server-minted import token (registerImportSource), NEVER a JS-supplied path —
// under a destination Library root, turning it into a cataloged managed Media the
// existing convert primitive accepts (apple ADR-0003 §1, POWERTOOLS-6). Gated on
// the Admin Session class in dispatch (the ADR-0003 §3 authorized loopback
// bearer): only the native authority that holds that bearer can reach it. The
// store refuses any token it did not vend ("unknown import token"), so a raw path
// string from the JS realm — which holds no valid token — can never be imported.
func (h *Handler) importManagedFile(ctx context.Context, scope auth.Scope, req request) (ManagedPathInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	importToken := trimmedVariable(req, "importToken")
	destParentPath := stringVar(req, "destinationParentPath")
	container := trimmedVariable(req, "container")
	codec := trimmedVariable(req, "codec")
	class := trimmedVariable(req, "class")
	if libraryID == "" || importToken == "" || destParentPath == "" {
		return ManagedPathInfo{}, clientError("libraryID, importToken, and destinationParentPath are required")
	}
	imported, err := h.store.ImportManagedFile(ctx, store.LibraryID(libraryID), importToken, destParentPath, container, codec, store.MediaClass(class), scope.LibraryFilter())
	if err != nil {
		return ManagedPathInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityManagedFileCreate, "path", imported.Path, store.LibraryID(libraryID)); err != nil {
		return ManagedPathInfo{}, err
	}
	return managedPathInfo(imported), nil
}

func (h *Handler) createManagedFolder(ctx context.Context, scope auth.Scope, req request) (ManagedPathInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	parentPath := stringVar(req, "parentPath")
	name := trimmedVariable(req, "name")
	if libraryID == "" || parentPath == "" || name == "" {
		return ManagedPathInfo{}, clientError("libraryID, parentPath, and name are required")
	}
	created, err := h.store.CreateManagedFolder(ctx, store.LibraryID(libraryID), parentPath, name, scope.LibraryFilter())
	if err != nil {
		return ManagedPathInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityManagedFileCreate, "path", created.Path, store.LibraryID(libraryID)); err != nil {
		return ManagedPathInfo{}, err
	}
	return managedPathInfo(created), nil
}

func (h *Handler) renameManagedPath(ctx context.Context, scope auth.Scope, req request) (ManagedPathInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	path := stringVar(req, "path")
	name := trimmedVariable(req, "name")
	if libraryID == "" || path == "" || name == "" {
		return ManagedPathInfo{}, clientError("libraryID, path, and name are required")
	}
	renamed, err := h.store.RenameManagedPath(ctx, store.LibraryID(libraryID), path, name, scope.LibraryFilter())
	if err != nil {
		return ManagedPathInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityManagedFileMove, "path", renamed.Path, store.LibraryID(libraryID)); err != nil {
		return ManagedPathInfo{}, err
	}
	return managedPathInfo(renamed), nil
}

func (h *Handler) batchRenameManagedPaths(ctx context.Context, scope auth.Scope, req request) (BatchResultInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return BatchResultInfo{}, clientError("libraryID is required")
	}
	renames, err := managedPathRenameArgs(req)
	if err != nil {
		return BatchResultInfo{}, err
	}
	batch, err := h.store.BatchRenameManagedPaths(ctx, store.LibraryID(libraryID), renames, scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return BatchResultInfo{}, err
	}
	if batch.ID != "" {
		if err := h.logActivity(ctx, scope, store.ActivityManagedFileMove, "batch", batch.ID, store.LibraryID(libraryID)); err != nil {
			return BatchResultInfo{}, err
		}
	}
	return batchResultInfo(batch), nil
}

func (h *Handler) moveManagedPath(ctx context.Context, scope auth.Scope, req request) (ManagedPathInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	path := stringVar(req, "path")
	destinationParentPath := stringVar(req, "destinationParentPath")
	if libraryID == "" || path == "" || destinationParentPath == "" {
		return ManagedPathInfo{}, clientError("libraryID, path, and destinationParentPath are required")
	}
	moved, err := h.store.MoveManagedPath(ctx, store.LibraryID(libraryID), path, destinationParentPath, scope.LibraryFilter())
	if err != nil {
		return ManagedPathInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityManagedFileMove, "path", moved.Path, store.LibraryID(libraryID)); err != nil {
		return ManagedPathInfo{}, err
	}
	return managedPathInfo(moved), nil
}

func (h *Handler) deleteManagedPath(ctx context.Context, scope auth.Scope, req request) (ManagedTrashInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	path := stringVar(req, "path")
	if libraryID == "" || path == "" {
		return ManagedTrashInfo{}, clientError("libraryID and path are required")
	}
	trash, err := h.store.DeleteManagedPath(ctx, store.LibraryID(libraryID), path, scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return ManagedTrashInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityManagedFileDelete, "managed-trash", trash.TrashID, store.LibraryID(libraryID)); err != nil {
		return ManagedTrashInfo{}, err
	}
	return managedTrashInfo(trash), nil
}

func (h *Handler) restoreManagedPath(ctx context.Context, scope auth.Scope, req request) (ManagedPathInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	trashID := trimmedVariable(req, "trashID")
	if libraryID == "" || trashID == "" {
		return ManagedPathInfo{}, clientError("libraryID and trashID are required")
	}
	restored, err := h.store.RestoreManagedPath(ctx, store.LibraryID(libraryID), trashID, scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return ManagedPathInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityManagedFileRestore, "path", restored.Path, store.LibraryID(libraryID)); err != nil {
		return ManagedPathInfo{}, err
	}
	return managedPathInfo(restored), nil
}

// organizePreview resolves the old→new path change-set a naming template WOULD
// produce over one visible Library (ADR-0009 §5 preview-first). No disk touch.
func (h *Handler) organizePreview(ctx context.Context, scope auth.Scope, req request) ([]FileChangeInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	template := trimmedVariable(req, "template")
	if libraryID == "" || template == "" {
		return nil, clientError("libraryID and template are required")
	}
	changes, err := h.store.OrganizePreview(ctx, store.LibraryID(libraryID), template, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	out := make([]FileChangeInfo, len(changes))
	for i, c := range changes {
		out[i] = fileChangeInfo(c)
	}
	return out, nil
}

// organizeApply moves each templated file on disk and commits the moves as one
// undo-able batch, logging one media.organize activity entry.
func (h *Handler) organizeApply(ctx context.Context, scope auth.Scope, req request) (BatchResultInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	template := trimmedVariable(req, "template")
	if libraryID == "" || template == "" {
		return BatchResultInfo{}, clientError("libraryID and template are required")
	}
	batch, err := h.store.OrganizeApply(ctx, store.LibraryID(libraryID), template, scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return BatchResultInfo{}, err
	}
	if batch.ID != "" {
		if err := h.logActivity(ctx, scope, store.ActivityMediaOrganize, "batch", batch.ID, store.LibraryID(libraryID)); err != nil {
			return BatchResultInfo{}, err
		}
	}
	return batchResultInfo(batch), nil
}

// mergeApply resolves a duplicate cluster: keep keepMediaID, trash every other
// byte-identical member through the audited Trash path, as one undo-able batch.
// Logs one media.merge activity entry.
func (h *Handler) mergeApply(ctx context.Context, scope auth.Scope, req request) (BatchResultInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	keep := trimmedVariable(req, "keepMediaID")
	if libraryID == "" || keep == "" {
		return BatchResultInfo{}, clientError("libraryID and keepMediaID are required")
	}
	batch, err := h.store.MergeApply(ctx, store.LibraryID(libraryID), store.MediaID(keep), scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return BatchResultInfo{}, err
	}
	if batch.ID != "" {
		if err := h.logActivity(ctx, scope, store.ActivityMediaMerge, "batch", batch.ID, store.LibraryID(libraryID)); err != nil {
			return BatchResultInfo{}, err
		}
	}
	return batchResultInfo(batch), nil
}

func managedPathRenameArgs(req request) ([]store.ManagedPathRename, error) {
	raw, _ := req.Variables["renames"].([]any)
	if len(raw) == 0 {
		return nil, clientError("renames is required")
	}
	renames := make([]store.ManagedPathRename, 0, len(raw))
	for _, entry := range raw {
		obj, ok := entry.(map[string]any)
		if !ok {
			return nil, clientError("each rename must be an object")
		}
		path, _ := obj["path"].(string)
		name, _ := obj["name"].(string)
		path = strings.TrimSpace(path)
		name = strings.TrimSpace(name)
		if path == "" || name == "" {
			return nil, clientError("each rename requires path and name")
		}
		renames = append(renames, store.ManagedPathRename{Path: path, Name: name})
	}
	return renames, nil
}

// mergeEntities folds the loser Entity into the winner of the same Kind (FEAT-6
// / ADR-0005 §3): re-points the loser's relations to the winner (deduping
// collisions), trashes the loser through the audited soft-delete (invariant #6),
// all as one undo-able batch. Logs one entity.merge activity entry. Reveal-gated
// like every read — a winner or loser the caller cannot see is "entity not
// found", never an existence oracle.
func (h *Handler) mergeEntities(ctx context.Context, scope auth.Scope, req request) (BatchResultInfo, error) {
	winner := trimmedVariable(req, "winnerEntityID")
	loser := trimmedVariable(req, "loserEntityID")
	if winner == "" || loser == "" {
		return BatchResultInfo{}, clientError("winnerEntityID and loserEntityID are required")
	}
	batch, err := h.store.MergeEntities(ctx, store.EntityID(winner), store.EntityID(loser), scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return BatchResultInfo{}, err
	}
	if batch.ID != "" {
		if err := h.logActivity(ctx, scope, store.ActivityEntityMerge, "batch", batch.ID, ""); err != nil {
			return BatchResultInfo{}, err
		}
	}
	return batchResultInfo(batch), nil
}
