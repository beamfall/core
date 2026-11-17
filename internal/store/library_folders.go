package store

import (
	"cmp"
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
)

// Library folder view (CORE-2359): a viewer browses one Library by the folders
// its Media files sit in. The view is read from the catalog, never from the
// filesystem: a folder exists here only because a visible Media file sits at or
// below it, so it names nothing the caller could not already reach and never
// leaves the Library's configured roots.

const (
	// LibraryFolderPageDefault is how many Media one folder read returns when
	// the caller names no limit.
	LibraryFolderPageDefault = 200
	// LibraryFolderPageMax bounds one folder read's page of Media.
	LibraryFolderPageMax = 500
)

// LibraryFolderQuery names one folder of one Library. An empty Path asks for
// the Library's roots; a Library with a single root holding visible Media
// opens that root directly.
type LibraryFolderQuery struct {
	LibraryID LibraryID
	ProfileID ProfileID
	Path      string
	Limit     int
	Offset    int
}

// LibraryFolder is one folder of a Library folder view and how many visible
// Media sit at or below it.
type LibraryFolder struct {
	Path       string
	Name       string
	MediaCount int
}

// LibraryFolderCrumb is one step of the path from a Library root down to the
// open folder.
type LibraryFolderCrumb struct {
	Path string
	Name string
}

// LibraryFolderResult is one folder of a Library: its subfolders, the visible
// Media directly in it (one page of them) and the path back to its root. Path
// is empty, and Crumbs too, when the result lists the Library's roots.
// RootCount is how many roots that list holds, so a client offers a way back
// to it only when it lists more than one.
type LibraryFolderResult struct {
	Path      string
	RootCount int
	Crumbs    []LibraryFolderCrumb
	Folders   []LibraryFolder
	Total     int
	Items     []SourceItem
}

// libraryFolderFile is one present file of a visible Media.
type libraryFolderFile struct {
	mediaID MediaID
	path    string
}

func emptyLibraryFolder() LibraryFolderResult {
	return LibraryFolderResult{Crumbs: []LibraryFolderCrumb{}, Folders: []LibraryFolder{}, Items: []SourceItem{}}
}

// LibraryFolder returns one folder of a Library the caller may see. Visibility
// is the Library browse's (libraryVisibleSetLocked), so a folder never lists or
// counts an item a rail would hide. A Path outside every configured root is a
// validation error; a Path inside a root with no visible Media is an empty
// folder, so the answer never tells a caller what the server holds but hides.
func (s *Store) LibraryFolder(ctx context.Context, filter LibraryFilter, query LibraryFolderQuery) (LibraryFolderResult, error) {
	empty := emptyLibraryFolder()
	filter, ok := narrowLibraryFilter(filter, query.LibraryID)
	if !ok {
		return empty, ValidationError("unknown library")
	}
	requested := strings.TrimSpace(query.Path)
	if requested != "" {
		requested = filepath.Clean(requested)
		if !filepath.IsAbs(requested) {
			return empty, ValidationError("folder path must be absolute")
		}
	}
	limit := query.Limit
	if limit <= 0 {
		limit = LibraryFolderPageDefault
	}
	limit = min(limit, LibraryFolderPageMax)
	offset := max(query.Offset, 0)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return empty, err
	}
	set, err := s.libraryVisibleSetLocked(ctx, filter, query.ProfileID)
	if err != nil {
		return empty, err
	}
	if len(set.visible) == 0 {
		return empty, ValidationError("unknown library")
	}
	configured, err := libraryRootsSQL(ctx, s.catalogR, query.LibraryID)
	if err != nil {
		return empty, err
	}
	roots := make([]string, 0, len(configured))
	for _, root := range configured {
		if root = filepath.Clean(strings.TrimSpace(root)); filepath.IsAbs(root) {
			roots = append(roots, root)
		}
	}
	files, err := libraryFolderFiles(ctx, set.reader, query.LibraryID, set.candidates)
	if err != nil {
		return empty, err
	}

	rootFolders := libraryRootFolders(roots, files)
	if requested == "" {
		if len(rootFolders) != 1 {
			result := emptyLibraryFolder()
			result.RootCount = len(rootFolders)
			result.Folders = rootFolders
			return result, nil
		}
		requested = rootFolders[0].Path
	}
	root := libraryFolderRoot(roots, requested)
	if root == "" {
		return empty, ValidationError("folder is outside the library roots")
	}

	result := emptyLibraryFolder()
	result.Path = requested
	result.RootCount = len(rootFolders)
	result.Crumbs = libraryFolderCrumbs(roots, root, requested)
	prefix := requested
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	children := map[string]map[MediaID]bool{}
	// direct maps each Media directly in the folder to the file name it sorts
	// by: its first file there, for a Media in several parts.
	direct := map[MediaID]string{}
	for _, file := range files {
		rel, ok := strings.CutPrefix(file.path, prefix)
		if !ok || rel == "" {
			continue
		}
		if name, _, nested := strings.Cut(rel, string(filepath.Separator)); nested {
			if children[name] == nil {
				children[name] = map[MediaID]bool{}
			}
			children[name][file.mediaID] = true
			continue
		}
		if known, seen := direct[file.mediaID]; !seen || strings.ToLower(rel) < strings.ToLower(known) {
			direct[file.mediaID] = rel
		}
	}
	for name, media := range children {
		result.Folders = append(result.Folders, LibraryFolder{Path: prefix + name, Name: name, MediaCount: len(media)})
	}
	slices.SortFunc(result.Folders, func(a, b LibraryFolder) int {
		return cmp.Or(compareFold(a.Name, b.Name), strings.Compare(a.Name, b.Name))
	})

	items := make([]SourceItem, 0, len(direct))
	for _, c := range set.candidates {
		if _, ok := direct[c.item.ID]; ok {
			items = append(items, c.item)
		}
	}
	slices.SortFunc(items, func(a, b SourceItem) int {
		return cmp.Or(
			compareFold(direct[a.ID], direct[b.ID]),
			compareFold(a.Title, b.Title),
			strings.Compare(string(a.ID), string(b.ID)),
		)
	})
	result.Total = len(items)
	if offset < len(items) {
		result.Items = slices.Clone(items[offset:min(offset+limit, len(items))])
	}
	if err := s.annotateLibraryItemsLocked(ctx, set.reader, filter, query.ProfileID, result.Items); err != nil {
		return empty, err
	}
	return result, nil
}

// libraryFolderFiles reads the present files of the visible Media, from the
// plane the Media were read from.
func libraryFolderFiles(ctx context.Context, reader *sql.DB, libraryID LibraryID, candidates []libraryBrowseCandidate) ([]libraryFolderFile, error) {
	visible := make(map[MediaID]bool, len(candidates))
	for _, c := range candidates {
		visible[c.item.ID] = true
	}
	rows, err := reader.QueryContext(ctx, `
		SELECT f.media_id, f.path
		FROM media_file f
		JOIN media m ON m.id = f.media_id
		WHERE m.library_id = ? AND f.missing_at_ms IS NULL`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []libraryFolderFile{}
	for rows.Next() {
		var file libraryFolderFile
		if err := rows.Scan(&file.mediaID, &file.path); err != nil {
			return nil, err
		}
		if !visible[file.mediaID] {
			continue
		}
		file.path = filepath.Clean(file.path)
		files = append(files, file)
	}
	return files, rows.Err()
}

// libraryRootFolders lists the configured roots that hold visible Media, in
// root order. A Library with no visible Media lists every root, so an empty
// Library still names where it looks.
func libraryRootFolders(roots []string, files []libraryFolderFile) []LibraryFolder {
	folders := []LibraryFolder{}
	for _, root := range roots {
		media := map[MediaID]bool{}
		for _, file := range files {
			if pathWithinRoot(root, file.path) {
				media[file.mediaID] = true
			}
		}
		if len(media) > 0 {
			folders = append(folders, LibraryFolder{Path: root, Name: libraryRootName(roots, root), MediaCount: len(media)})
		}
	}
	if len(folders) > 0 || len(files) > 0 {
		return folders
	}
	for _, root := range roots {
		folders = append(folders, LibraryFolder{Path: root, Name: libraryRootName(roots, root)})
	}
	return folders
}

// libraryFolderRoot is the innermost configured root holding path, or "" when
// none does.
func libraryFolderRoot(roots []string, path string) string {
	best := ""
	for _, root := range roots {
		if pathWithinRoot(root, path) && len(root) > len(best) {
			best = root
		}
	}
	return best
}

// libraryFolderCrumbs walks from root down to path, one folder per step.
func libraryFolderCrumbs(roots []string, root, path string) []LibraryFolderCrumb {
	crumbs := []LibraryFolderCrumb{{Path: root, Name: libraryRootName(roots, root)}}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return crumbs
	}
	current := root
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, name)
		crumbs = append(crumbs, LibraryFolderCrumb{Path: current, Name: name})
	}
	return crumbs
}

// libraryRootName is a root's last path element, or its whole path when
// another root shares that last element.
func libraryRootName(roots []string, root string) string {
	name := filepath.Base(root)
	for _, other := range roots {
		if other != root && filepath.Base(other) == name {
			return root
		}
	}
	return name
}

func compareFold(a, b string) int {
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}
