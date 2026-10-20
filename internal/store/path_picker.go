package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	PathRoleLibraryRoot         = "libraryRoot"
	PathRoleIncomingFolder      = "incomingFolder"
	PathRoleDownloadDestination = "downloadDestination"
	PathRoleBackupDestination   = "backupDestination"

	PathPickerRuntimeBare      = "bare"
	PathPickerRuntimeContainer = "container"
	PathPickerRuntimeUnknown   = "unknown"

	pathPickerPreviewTimeout = 75 * time.Millisecond
	pathPickerMaxPreview     = 5
)

var (
	pathPickerGOOS        = runtime.GOOS
	pathPickerCgroupPath  = "/proc/1/cgroup"
	pathPickerDockerEnv   = "/.dockerenv"
	pathPickerPodmanEnv   = "/run/.containerenv"
	pathPickerVolumesRoot = "/Volumes"

	pathPickerOpenRoot = os.OpenRoot
	pathPickerReadDir  = os.ReadDir

	pathPickerPreviewTimeoutDuration = pathPickerPreviewTimeout
)

type PathPickerRequest struct {
	Role              string
	Path              string
	LibraryID         LibraryID
	DistributionClass string
	Depth             int
}

type PathPickerContract struct {
	Runtime           string
	Roles             []string
	Role              string
	PurposeKey        string
	Roots             []PathPickerRoot
	Entries           []PathPickerEntry
	PreviewTimeoutMs  int
	MaxPreviewEntries int
}

type PathPickerRoot struct {
	Name       string
	Path       string
	EntryClass string
}

type PathPickerEntry struct {
	Name              string
	Path              string
	Kind              string
	AccessError       string
	EntryClass        string
	Selectable        bool
	ReasonCode        string
	MediaCount        int
	SampleNames       []string
	PreviewTimeoutMs  int
	MaxPreviewEntries int
	Truncated         bool
	Children          []PathPickerEntry
}

type pathPickerConfig struct {
	AllowedRoots []string `json:"allowed_roots"`
}

func loadPathPickerAllowedRoots(dataDir string, configured []string) ([]string, error) {
	roots := append([]string{}, configured...)
	path := pathPickerConfigPath(dataDir)
	raw, err := os.ReadFile(path)
	if err == nil {
		var cfg pathPickerConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
		roots = append(roots, cfg.AllowedRoots...)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return normalizeAllowedRoots(dataDir, roots)
}

func pathPickerConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "config", "path-picker.json")
}

func normalizeAllowedRoots(dataDir string, roots []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, root := range roots {
		cleaned, err := cleanPathPickerRoot(root)
		if err != nil {
			return nil, err
		}
		if err := rejectPathPickerBlocklist(dataDir, cleaned); err != nil {
			return nil, err
		}
		if !seen[cleaned] {
			seen[cleaned] = true
			out = append(out, cleaned)
		}
	}
	slices.Sort(out)
	return out, nil
}

func cleanPathPickerRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", ValidationError("path picker root is required")
	}
	abs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func rejectPathPickerBlocklist(dataDir, path string) error {
	for _, blocked := range pathPickerBlockedRoots(dataDir) {
		if pathWithinRoot(blocked, path) {
			return ValidationError("path is blocklisted")
		}
	}
	return nil
}

func pathPickerBlockedRoots(dataDir string) []string {
	roots := []string{"/etc", "/proc", "/sys", "/dev", "/run", filepath.Join(dataDir, "config"), filepath.Join(dataDir, "secrets")}
	out := roots[:0]
	for _, root := range roots {
		cleaned, err := cleanPathPickerRoot(root)
		if err == nil {
			out = append(out, cleaned)
		}
	}
	return out
}

func (s *Store) AddPathPickerRoot(ctx context.Context, path string) (PathPickerRoot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return PathPickerRoot{}, err
	}
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PathPickerRoot{}, ValidationError("folder does not exist")
		}
		if errors.Is(err, os.ErrPermission) {
			return PathPickerRoot{}, ValidationError("folder is not readable")
		}
		return PathPickerRoot{}, err
	}
	if err := rejectPathPickerBlocklist(s.dataDir, canonical); err != nil {
		return PathPickerRoot{}, err
	}
	before, err := os.Stat(canonical)
	if err != nil {
		return PathPickerRoot{}, err
	}
	root, err := pathPickerOpenRoot(canonical)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PathPickerRoot{}, ValidationError("folder does not exist")
		}
		if errors.Is(err, os.ErrPermission) {
			return PathPickerRoot{}, ValidationError("folder is not readable")
		}
		return PathPickerRoot{}, err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return PathPickerRoot{}, err
	}
	if !os.SameFile(before, info) {
		return PathPickerRoot{}, ValidationError("folder changed while adding root")
	}
	if !info.IsDir() {
		return PathPickerRoot{}, ValidationError("path is not a folder")
	}
	roots, err := normalizeAllowedRoots(s.dataDir, append(s.allowedRoots, canonical))
	if err != nil {
		return PathPickerRoot{}, err
	}
	if err := writePathPickerAllowedRoots(s.dataDir, roots); err != nil {
		return PathPickerRoot{}, err
	}
	s.allowedRoots = roots
	return PathPickerRoot{Name: pathPickerName(canonical), Path: canonical, EntryClass: "ordinary"}, nil
}

func writePathPickerAllowedRoots(dataDir string, roots []string) error {
	path := pathPickerConfigPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(pathPickerConfig{AllowedRoots: roots}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o600)
}

func (s *Store) PathPicker(ctx context.Context, req PathPickerRequest) (PathPickerContract, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PathPickerContract{}, err
	}
	req.Role = strings.TrimSpace(req.Role)
	roles := pathPickerRoles(req.DistributionClass)
	if req.Role == "" {
		req.Role = roles[0]
	}
	if !slices.Contains(roles, req.Role) {
		return PathPickerContract{}, ValidationError("path role is unavailable")
	}
	roots := s.pathPickerRootsLocked()
	if pathPickerAppStoreMode(req.DistributionClass) {
		roots = pathPickerAppStoreRoots(roots)
	}
	depth := clampFolderTreeDepth(req.Depth)
	contract := PathPickerContract{
		Runtime:           detectPathPickerRuntime(),
		Roles:             roles,
		Role:              req.Role,
		PurposeKey:        "pathPicker." + req.Role,
		Roots:             roots,
		PreviewTimeoutMs:  int(pathPickerPreviewTimeoutDuration / time.Millisecond),
		MaxPreviewEntries: pathPickerMaxPreview,
	}
	target := strings.TrimSpace(req.Path)
	if target == "" {
		contract.Entries = make([]PathPickerEntry, 0, len(roots))
		for _, root := range roots {
			entry := pathPickerEntryFromRoot(root)
			s.applyPathPickerEligibility(ctx, &entry, req)
			contract.Entries = append(contract.Entries, entry)
		}
		return contract, nil
	}
	canonical, err := canonicalExistingPath(target)
	if err != nil {
		contract.Entries = []PathPickerEntry{{
			Name:       pathPickerName(target),
			Path:       filepath.Clean(target),
			Kind:       "folder",
			Selectable: false,
			ReasonCode: pathPickerMissingReason(err),
		}}
		return contract, nil
	}
	if err := rejectPathPickerBlocklist(s.dataDir, canonical); err != nil {
		contract.Entries = []PathPickerEntry{blockedPathPickerEntry(canonical)}
		return contract, nil
	}
	if !pathPickerWithinRoots(canonical, roots) {
		contract.Entries = []PathPickerEntry{{
			Name:       pathPickerName(canonical),
			Path:       canonical,
			Kind:       "folder",
			Selectable: false,
			ReasonCode: "outside-roots",
		}}
		return contract, nil
	}
	entry, err := s.pathPickerEntry(ctx, canonical, depth)
	if err != nil {
		return PathPickerContract{}, err
	}
	s.applyPathPickerEligibility(ctx, &entry, req)
	contract.Entries = []PathPickerEntry{entry}
	return contract, nil
}

func pathPickerRoles(distributionClass string) []string {
	roles := []string{PathRoleLibraryRoot, PathRoleIncomingFolder, PathRoleDownloadDestination, PathRoleBackupDestination}
	if pathPickerAppStoreMode(distributionClass) {
		return []string{PathRoleLibraryRoot, PathRoleIncomingFolder, PathRoleBackupDestination}
	}
	return roles
}

func pathPickerAppStoreMode(distributionClass string) bool {
	return strings.EqualFold(strings.TrimSpace(distributionClass), "appStore")
}

func pathPickerAppStoreRoots(roots []PathPickerRoot) []PathPickerRoot {
	filtered := roots[:0]
	for _, root := range roots {
		if pathPickerAppStoreRootAllowed(root) {
			filtered = append(filtered, root)
		}
	}
	return filtered
}

func pathPickerAppStoreRootAllowed(root PathPickerRoot) bool {
	name := strings.ToLower(root.Name)
	base := strings.ToLower(filepath.Base(root.Path))
	return !strings.Contains(name, "download") && !strings.Contains(base, "download")
}

func (s *Store) pathPickerRootsLocked() []PathPickerRoot {
	seen := map[string]bool{}
	roots := []PathPickerRoot{}
	add := func(path, class string) {
		cleaned, err := cleanPathPickerRoot(path)
		if err != nil || rejectPathPickerBlocklist(s.dataDir, cleaned) != nil || seen[cleaned] {
			return
		}
		seen[cleaned] = true
		roots = append(roots, PathPickerRoot{Name: pathPickerName(cleaned), Path: cleaned, EntryClass: class})
	}
	for _, root := range s.allowedRoots {
		add(root, "ordinary")
	}
	if pathPickerGOOS == "darwin" {
		if volumes, err := os.ReadDir(pathPickerVolumesRoot); err == nil {
			for _, volume := range volumes {
				full := filepath.Join(pathPickerVolumesRoot, volume.Name())
				if childIsDir(volume, full) {
					add(full, "mount-point")
				}
			}
		}
	}
	for _, common := range []string{"/media", "/mnt", "/srv", "/share"} {
		if info, err := os.Stat(common); err == nil && info.IsDir() {
			add(common, "mount-point")
		}
	}
	return roots
}

func pathPickerWithinRoots(path string, roots []PathPickerRoot) bool {
	for _, root := range roots {
		if pathWithinRoot(root.Path, path) {
			return true
		}
	}
	return false
}

func pathPickerEntryFromRoot(root PathPickerRoot) PathPickerEntry {
	return PathPickerEntry{
		Name:              root.Name,
		Path:              root.Path,
		Kind:              "folder",
		AccessError:       "none",
		EntryClass:        root.EntryClass,
		Selectable:        true,
		PreviewTimeoutMs:  int(pathPickerPreviewTimeoutDuration / time.Millisecond),
		MaxPreviewEntries: pathPickerMaxPreview,
	}
}

func (s *Store) pathPickerEntry(ctx context.Context, path string, depth int) (PathPickerEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return PathPickerEntry{}, err
	}
	kind := "file"
	if info.IsDir() {
		kind = "folder"
	}
	entry := PathPickerEntry{
		Name:              info.Name(),
		Path:              filepath.Clean(path),
		Kind:              kind,
		AccessError:       "none",
		EntryClass:        "ordinary",
		Selectable:        true,
		PreviewTimeoutMs:  int(pathPickerPreviewTimeoutDuration / time.Millisecond),
		MaxPreviewEntries: pathPickerMaxPreview,
	}
	if entry.Name == "" {
		entry.Name = entry.Path
	}
	if kind == "folder" {
		entry.MediaCount, entry.SampleNames, entry.Truncated = previewPathPickerFolder(ctx, path)
	}
	if kind != "folder" || depth <= 0 {
		return entry, nil
	}
	children, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			entry.AccessError = "permission-denied"
			entry.Selectable = false
			entry.ReasonCode = "permission-denied"
			return entry, nil
		}
		return PathPickerEntry{}, err
	}
	sort.Slice(children, func(i, j int) bool {
		leftFolder := children[i].IsDir()
		rightFolder := children[j].IsDir()
		if leftFolder != rightFolder {
			return leftFolder
		}
		return strings.ToLower(children[i].Name()) < strings.ToLower(children[j].Name())
	})
	entry.Children = make([]PathPickerEntry, 0, len(children))
	for _, child := range children {
		if child.Name() == managedFileTrashDir {
			continue
		}
		childPath := filepath.Join(path, child.Name())
		childEntry, err := s.pathPickerEntry(ctx, childPath, depth-1)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
				continue
			}
			return PathPickerEntry{}, err
		}
		entry.Children = append(entry.Children, childEntry)
	}
	return entry, nil
}

func previewPathPickerFolder(ctx context.Context, path string) (int, []string, bool) {
	type result struct {
		count     int
		samples   []string
		truncated bool
	}
	ch := make(chan result, 1)
	go func() {
		entries, err := pathPickerReadDir(path)
		if err != nil {
			ch <- result{}
			return
		}
		count := 0
		samples := []string{}
		for _, entry := range entries {
			if entry.IsDir() || !pathPickerLooksLikeMedia(entry.Name()) {
				continue
			}
			count++
			if len(samples) < pathPickerMaxPreview {
				samples = append(samples, entry.Name())
			}
		}
		ch <- result{count: count, samples: samples, truncated: count > len(samples)}
	}()
	select {
	case <-ctx.Done():
		return 0, nil, true
	case r := <-ch:
		return r.count, r.samples, r.truncated
	case <-time.After(pathPickerPreviewTimeoutDuration):
		return 0, nil, true
	}
}

func pathPickerLooksLikeMedia(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".m4v", ".mkv", ".mov", ".mp3", ".flac", ".m4a", ".jpg", ".jpeg", ".png", ".webp", ".heic", ".heif", ".cr2", ".nef", ".arw", ".dng":
		return true
	default:
		return false
	}
}

func (s *Store) applyPathPickerEligibility(ctx context.Context, entry *PathPickerEntry, req PathPickerRequest) {
	if entry.AccessError != "" && entry.AccessError != "none" {
		entry.Selectable = false
		if entry.ReasonCode == "" {
			entry.ReasonCode = entry.AccessError
		}
	}
	if entry.ReasonCode == "blocklisted" || entry.ReasonCode == "outside-roots" {
		entry.Selectable = false
	}
	if req.Role == PathRoleIncomingFolder {
		roots, err := libraryRootsSQL(ctx, s.catalogR, req.LibraryID)
		if err != nil || len(roots) == 0 || !cleanPathWithinConfiguredRoots(entry.Path, roots) {
			entry.Selectable = false
			entry.ReasonCode = "not-inside-library-root"
		}
	}
	for i := range entry.Children {
		s.applyPathPickerEligibility(ctx, &entry.Children[i], req)
	}
}

func cleanPathWithinConfiguredRoots(path string, roots []string) bool {
	selected, err := cleanPathPickerRoot(path)
	if err != nil {
		return false
	}
	for _, root := range roots {
		base, err := cleanPathPickerRoot(root)
		if err == nil && pathWithinRoot(base, selected) {
			return true
		}
	}
	return false
}

func pathPickerMissingReason(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "server-cannot-see-path"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission-denied"
	}
	return "server-cannot-see-path"
}

func blockedPathPickerEntry(path string) PathPickerEntry {
	return PathPickerEntry{
		Name:       pathPickerName(path),
		Path:       path,
		Kind:       "folder",
		Selectable: false,
		ReasonCode: "blocklisted",
	}
}

func pathPickerName(path string) string {
	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == string(filepath.Separator) {
		return filepath.Clean(path)
	}
	return name
}

func detectPathPickerRuntime() string {
	if pathPickerFileExists(pathPickerDockerEnv) || pathPickerFileExists(pathPickerPodmanEnv) {
		return PathPickerRuntimeContainer
	}
	raw, err := os.ReadFile(pathPickerCgroupPath)
	if err != nil {
		return PathPickerRuntimeUnknown
	}
	text := strings.ToLower(string(raw))
	if strings.Contains(text, "docker") || strings.Contains(text, "kubepods") ||
		strings.Contains(text, "containerd") || strings.Contains(text, "lxc") {
		return PathPickerRuntimeContainer
	}
	return PathPickerRuntimeBare
}

func pathPickerFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
