package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type directoryArchive struct {
	rootPath string
	root     *os.Root
	rootInfo os.FileInfo
	entries  []directoryArchiveEntry
	infos    map[string]os.FileInfo
	expected map[string]archiveExpectation
}

type directoryArchiveEntry struct {
	name   string
	info   os.FileInfo
	file   *os.File
	digest [sha256.Size]byte
}

type archiveStage string

const (
	archiveStageDiscovered archiveStage = "discovered"
	archiveStageStatted    archiveStage = "statted"
	archiveStageOpened     archiveStage = "opened"
	archiveStageHashed     archiveStage = "hashed"
	archiveStageArchive    archiveStage = "archive"
)

type directoryArchiveHooks struct {
	reachStage func(archiveStage, string)
}

func (h *directoryArchiveHooks) reach(stage archiveStage, name string) {
	if h != nil && h.reachStage != nil {
		h.reachStage(stage, name)
	}
}

type archiveManifest struct {
	Type             string                            `json:"type"`
	Databases        map[string]archiveManifestPayload `json:"databases"`
	OperationalFiles map[string]archiveManifestPayload `json:"operationalFiles"`
}

type archiveManifestPayload struct {
	RelativePath string `json:"relativePath"`
	BackupPath   string `json:"backupPath"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
}

type archiveExpectation struct {
	size   int64
	digest [sha256.Size]byte
}

func discoverDirectoryArchive(ctx context.Context, sourceDir string) (*directoryArchive, error) {
	rootPath, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(rootPath)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("backup: source must be a directory, not a symlink")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	snapshot := &directoryArchive{
		rootPath: rootPath,
		root:     root,
		rootInfo: rootInfo,
		infos:    make(map[string]os.FileInfo),
	}
	if err := snapshot.validateRoot(); err != nil {
		_ = root.Close()
		return nil, err
	}
	if err := snapshot.discover(ctx, root, ""); err != nil {
		_ = root.Close()
		return nil, err
	}
	if err := snapshot.validateRoot(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return snapshot, nil
}

func (s *directoryArchive) discover(ctx context.Context, root *os.Root, prefix string) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		path := filepath.Join(prefix, name)
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup: symlink is not allowed in set: %s", path)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("backup: unsupported file type in set: %s", path)
		}
		s.entries = append(s.entries, directoryArchiveEntry{name: path, info: info})
		s.infos[path] = info
		if !info.IsDir() {
			continue
		}
		child, err := openVerifiedArchiveRoot(root, name, info, nil, path)
		if err != nil {
			return err
		}
		discoverErr := s.discover(ctx, child, path)
		closeErr := child.Close()
		if discoverErr != nil {
			return discoverErr
		}
		if closeErr != nil {
			return closeErr
		}
		current, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !sameArchiveObject(info, current) {
			return fmt.Errorf("backup: directory changed during discovery: %s", path)
		}
	}
	return nil
}

func (s *directoryArchive) openEntry(entry *directoryArchiveEntry, hooks *directoryArchiveHooks) (*os.File, error) {
	if err := s.validateRoot(); err != nil {
		return nil, err
	}
	components := strings.Split(entry.name, string(filepath.Separator))
	root := s.root
	opened := make([]*os.Root, 0, len(components)-1)
	defer func() {
		for _, child := range opened {
			_ = child.Close()
		}
	}()
	for index, component := range components {
		path := filepath.Join(components[:index+1]...)
		expected := s.infos[path]
		if index == len(components)-1 {
			if entry.info.IsDir() {
				current, err := root.Lstat(component)
				if err != nil {
					return nil, err
				}
				if !sameArchiveObject(expected, current) {
					return nil, fmt.Errorf("backup: directory changed before archiving: %s", entry.name)
				}
				return nil, nil
			}
			return openVerifiedArchiveFile(root, component, expected, entry.name, hooks)
		}
		child, err := openVerifiedArchiveRoot(root, component, expected, hooks, path)
		if err != nil {
			return nil, fmt.Errorf("backup: path component changed before archiving: %s: %w", path, err)
		}
		opened = append(opened, child)
		root = child
	}
	return nil, errors.New("backup: empty archive entry")
}

func (s *directoryArchive) validateEntry(entry directoryArchiveEntry) error {
	file, err := s.openEntry(&entry, nil)
	if file != nil {
		_ = file.Close()
	}
	return err
}

func (s *directoryArchive) close() {
	for index := range s.entries {
		if s.entries[index].file != nil {
			_ = s.entries[index].file.Close()
			s.entries[index].file = nil
		}
	}
	_ = s.root.Close()
}

func (s *directoryArchive) prepare(ctx context.Context, hooks *directoryArchiveHooks) error {
	expected, constrained, err := s.manifestExpectations(ctx, hooks)
	if err != nil {
		return err
	}
	s.expected = expected
	found := make(map[string]bool, len(expected))
	for index := range s.entries {
		entry := &s.entries[index]
		if entry.info.IsDir() {
			continue
		}
		want, ok := expected[entry.name]
		if constrained && entry.name != "manifest.json" && !ok {
			return fmt.Errorf("backup: archive entry is absent from manifest: %s", entry.name)
		}
		if !ok {
			continue
		}
		found[entry.name] = true
		if entry.info.Size() != want.size {
			return fmt.Errorf("backup: archive entry does not match manifest: %s", entry.name)
		}
	}
	for name := range expected {
		if !found[name] {
			return fmt.Errorf("backup: manifest entry is absent from archive: %s", name)
		}
	}
	return nil
}

func (s *directoryArchive) manifestExpectations(ctx context.Context, hooks *directoryArchiveHooks) (map[string]archiveExpectation, bool, error) {
	var manifestEntry *directoryArchiveEntry
	for index := range s.entries {
		if s.entries[index].name != "manifest.json" {
			continue
		}
		if !s.entries[index].info.Mode().IsRegular() {
			return nil, false, errors.New("backup: archive manifest is not a regular file")
		}
		manifestEntry = &s.entries[index]
		break
	}
	if manifestEntry == nil {
		return nil, false, nil
	}
	raw, err := s.prepareEntry(ctx, manifestEntry, hooks, true)
	if err != nil {
		return nil, false, err
	}
	var manifest archiveManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, false, fmt.Errorf("backup: parse archive manifest: %w", err)
	}
	if manifest.Type != "backup" {
		return nil, false, errors.New("backup: archive manifest type is unsupported")
	}
	if len(manifest.Databases) == 0 {
		return nil, false, errors.New("backup: archive manifest has no database payloads")
	}
	expected := make(map[string]archiveExpectation, len(manifest.Databases)+len(manifest.OperationalFiles))
	for _, payload := range manifest.Databases {
		if err := s.addManifestExpectation(expected, payload); err != nil {
			return nil, false, err
		}
	}
	for _, payload := range manifest.OperationalFiles {
		if err := s.addManifestExpectation(expected, payload); err != nil {
			return nil, false, err
		}
	}
	return expected, true, nil
}

func (s *directoryArchive) addManifestExpectation(expected map[string]archiveExpectation, payload archiveManifestPayload) error {
	if payload.SizeBytes < 0 || strings.TrimSpace(payload.BackupPath) == "" {
		return errors.New("backup: archive manifest contains an invalid payload")
	}
	relative, err := filepath.Rel(s.rootPath, payload.BackupPath)
	if err != nil {
		return err
	}
	relative = filepath.Clean(relative)
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("backup: archive manifest payload escapes source directory")
	}
	rawDigest, err := hex.DecodeString(payload.SHA256)
	if err != nil || len(rawDigest) != sha256.Size {
		return errors.New("backup: archive manifest contains an invalid payload digest")
	}
	var digest [sha256.Size]byte
	copy(digest[:], rawDigest)
	want := archiveExpectation{size: payload.SizeBytes, digest: digest}
	if prior, ok := expected[relative]; ok && prior != want {
		return fmt.Errorf("backup: archive manifest contains conflicting payloads: %s", relative)
	}
	expected[relative] = want
	return nil
}

func (s *directoryArchive) prepareEntry(ctx context.Context, entry *directoryArchiveEntry, hooks *directoryArchiveHooks, capture bool) ([]byte, error) {
	file, err := s.openEntry(entry, hooks)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	hash := sha256.New()
	var contents bytes.Buffer
	destination := io.Writer(hash)
	if capture {
		destination = io.MultiWriter(hash, &contents)
	}
	written, err := copyContext(ctx, destination, io.LimitReader(file, entry.info.Size()+1))
	if err != nil {
		return nil, err
	}
	current, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if written != entry.info.Size() || !sameArchiveVersion(entry.info, current) {
		return nil, fmt.Errorf("backup: file changed while hashing: %s", entry.name)
	}
	copy(entry.digest[:], hash.Sum(nil))
	entry.file = file
	closeOnError = false
	hooks.reach(archiveStageHashed, entry.name)
	return contents.Bytes(), nil
}

func (s *directoryArchive) validateRoot() error {
	current, err := os.Lstat(s.rootPath)
	if err != nil {
		return err
	}
	opened, err := s.root.Stat(".")
	if err != nil {
		return err
	}
	if !sameArchiveObject(s.rootInfo, current) || !sameArchiveObject(s.rootInfo, opened) {
		return errors.New("backup: source directory changed while archiving")
	}
	return nil
}

func openVerifiedArchiveRoot(parent *os.Root, name string, expected os.FileInfo, hooks *directoryArchiveHooks, path string) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !expected.IsDir() || !sameArchiveObject(expected, before) {
		return nil, errors.New("directory was replaced")
	}
	hooks.reach(archiveStageStatted, path)
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, statErr := child.Stat(".")
	after, lstatErr := parent.Lstat(name)
	if statErr != nil || lstatErr != nil || !sameArchiveObject(expected, opened) || !sameArchiveObject(expected, after) {
		_ = child.Close()
		if statErr != nil {
			return nil, statErr
		}
		if lstatErr != nil {
			return nil, lstatErr
		}
		return nil, errors.New("directory was replaced")
	}
	hooks.reach(archiveStageOpened, path)
	return child, nil
}

func openVerifiedArchiveFile(root *os.Root, name string, expected os.FileInfo, path string, hooks *directoryArchiveHooks) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !expected.Mode().IsRegular() || !sameArchiveObject(expected, before) {
		return nil, fmt.Errorf("backup: file changed before archiving: %s", path)
	}
	hooks.reach(archiveStageStatted, path)
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	after, lstatErr := root.Lstat(name)
	if statErr != nil || lstatErr != nil || !sameArchiveObject(expected, opened) || !sameArchiveObject(expected, after) {
		_ = file.Close()
		if statErr != nil {
			return nil, statErr
		}
		if lstatErr != nil {
			return nil, lstatErr
		}
		return nil, fmt.Errorf("backup: file changed before archiving: %s", path)
	}
	hooks.reach(archiveStageOpened, path)
	return file, nil
}

func sameArchiveObject(expected, actual os.FileInfo) bool {
	if expected == nil || actual == nil {
		return false
	}
	if actual.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if expected.IsDir() != actual.IsDir() || expected.Mode().IsRegular() != actual.Mode().IsRegular() {
		return false
	}
	return os.SameFile(expected, actual)
}

func sameArchiveVersion(expected, actual os.FileInfo) bool {
	return sameArchiveObject(expected, actual) &&
		expected.Size() == actual.Size() &&
		expected.ModTime().Equal(actual.ModTime())
}
