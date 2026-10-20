package genassets

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	assetStagingDir    = ".staging"
	assetGenerationDir = ".generations"
)

// ErrPublicationConflict means an immutable or public identity already names
// different bytes, or a publication path is not the expected regular shape.
var ErrPublicationConflict = errors.New("generated asset publication conflict")

type assetPublication struct {
	service    *Service
	root       *os.Root
	owner      string
	source     string
	contract   string
	publicName string
	stageDir   string
}

func (s *Service) publishBytes(owner, source, contract, name string, data []byte) error {
	return s.publishGeneratedFile(owner, source, contract, name, func(path string) error {
		return os.WriteFile(path, data, 0o600)
	})
}

func (s *Service) publishGeneratedFile(owner, source, contract, name string, generate func(string) error) error {
	publication, err := s.beginPublication(owner, source, contract, name)
	if err != nil {
		return err
	}
	defer publication.close()
	output := filepath.Join(publication.root.Name(), publication.stageDir, name)
	if err := generate(output); err != nil {
		return err
	}
	return publication.commit(name, publication.publicName)
}

func (s *Service) beginPublication(owner, source, contract, name string) (*assetPublication, error) {
	if s.publishErr != nil {
		return nil, fmt.Errorf("recovering generated asset publication state: %w", s.publishErr)
	}
	if owner == "" || source == "" || contract == "" || !validAssetName(name) {
		return nil, errors.New("generated asset publication identity is incomplete")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = root.Close()
		}
	}()
	if err := ensureRootDir(root, assetStagingDir); err != nil {
		return nil, err
	}
	if err := ensureRootDir(root, assetGenerationDir); err != nil {
		return nil, err
	}
	if err := ensureRootDir(root, assetFingerprintDir(source)); err != nil {
		return nil, err
	}
	stageDir, err := exclusiveStageDir(root, owner, source, contract, name)
	if err != nil {
		return nil, err
	}
	closeOnError = false
	return &assetPublication{
		service:    s,
		root:       root,
		owner:      owner,
		source:     source,
		contract:   contract,
		publicName: filepath.Join(assetFingerprintDir(source), name),
		stageDir:   stageDir,
	}, nil
}

func ensureRootDir(root *os.Root, name string) error {
	if err := root.MkdirAll(name, 0o700); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrPublicationConflict, name)
	}
	return nil
}

func exclusiveStageDir(root *os.Root, owner, source, contract, name string) (string, error) {
	for range 8 {
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return "", err
		}
		identity := publicationDigest(owner, source, contract, name, fmt.Sprintf("%x", nonce))
		stageDir := filepath.Join(assetStagingDir, identity+".stage")
		if err := root.Mkdir(stageDir, 0o700); err == nil {
			return stageDir, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("cannot allocate exclusive generated asset staging directory")
}

func (p *assetPublication) commit(stageName, publicName string) error {
	p.service.publish.Lock()
	defer p.service.publish.Unlock()
	stagePath := filepath.Join(p.stageDir, stageName)
	info, err := p.root.Lstat(stagePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: staged output is not a regular file", ErrPublicationConflict)
	}
	contentHash, err := rootedFileHash(p.root, stagePath)
	if err != nil {
		return err
	}
	contentID := fmt.Sprintf("%x", contentHash[:])
	generationName := publicationDigest(p.owner, p.source, p.contract, publicName, contentID)
	contentStagePath := filepath.Join(p.stageDir, generationName+".asset")
	if err := p.root.Link(stagePath, contentStagePath); err != nil {
		return err
	}
	generationPath := filepath.Join(assetGenerationDir, generationName)
	if err := p.root.Link(contentStagePath, generationPath); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		matches, matchErr := p.pathHasHash(generationPath, contentHash)
		if matchErr != nil {
			return matchErr
		}
		if !matches {
			return fmt.Errorf("%w: generation identity collision", ErrPublicationConflict)
		}
	}
	if err := p.root.Link(generationPath, publicName); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		matches, matchErr := p.pathHasHash(publicName, contentHash)
		if matchErr != nil {
			return matchErr
		}
		if !matches {
			return fmt.Errorf("%w: public identity already contains different content", ErrPublicationConflict)
		}
	}
	return nil
}

func (p *assetPublication) pathHasHash(name string, want [sha256.Size]byte) (bool, error) {
	info, err := p.root.Lstat(name)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%w: publication target is not a regular file", ErrPublicationConflict)
	}
	got, err := rootedFileHash(p.root, name)
	return got == want, err
}

func rootedFileHash(root *os.Root, name string) ([sha256.Size]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}

func (s *Service) removeOrphanAssets(fingerprint string, keep map[string]bool, accepts func(string) bool) error {
	s.publish.Lock()
	defer s.publish.Unlock()
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	dir := assetFingerprintDir(fingerprint)
	entries, err := fs.ReadDir(root.FS(), dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var joined error
	for _, entry := range entries {
		name := entry.Name()
		if !accepts(name) || keep[name] {
			continue
		}
		if err := root.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			joined = errors.Join(joined, err)
		}
	}
	if joined != nil {
		return joined
	}
	_, err = collectUnreferencedGenerations(root)
	return err
}

func (p *assetPublication) close() {
	_ = p.root.RemoveAll(p.stageDir)
	_ = p.root.Close()
}

func publicationDigest(owner, source, contract, name, contentHash string) string {
	digest := sha256.Sum256([]byte(owner + "\x00" + source + "\x00" + contract + "\x00" + name + "\x00" + contentHash))
	return fmt.Sprintf("%x", digest[:])
}

func assetFingerprintDir(fingerprint string) string {
	safe := stringsLowerSanitize(fingerprint)
	return filepath.Base(safe)
}

func stringsLowerSanitize(value string) string {
	value = strings.ToLower(value)
	return strings.NewReplacer(":", "-", "/", "-", "\\", "-", " ", "-").Replace(value)
}

// SweepPublicationResidue removes abandoned staging directories and immutable
// generation files that have no public hard-link reference. Existing public
// assets are never inferred stale from age or naming and are never removed.
func (s *Service) SweepPublicationResidue() (int, error) {
	s.publish.Lock()
	defer s.publish.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	if err := ensureRootDir(root, assetStagingDir); err != nil {
		return 0, err
	}
	if err := ensureRootDir(root, assetGenerationDir); err != nil {
		return 0, err
	}
	removed := 0
	staging, err := fs.ReadDir(root.FS(), assetStagingDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	for _, entry := range staging {
		if err := root.RemoveAll(filepath.Join(assetStagingDir, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	collected, err := collectUnreferencedGenerations(root)
	return removed + collected, err
}

func collectUnreferencedGenerations(root *os.Root) (int, error) {
	publicInfos := []fs.FileInfo{}
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == assetStagingDir || path == assetGenerationDir {
			return fs.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			publicInfos = append(publicInfos, info)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	generations, err := fs.ReadDir(root.FS(), assetGenerationDir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range generations {
		path := filepath.Join(assetGenerationDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return removed, err
		}
		referenced := false
		for _, publicInfo := range publicInfos {
			if os.SameFile(info, publicInfo) {
				referenced = true
				break
			}
		}
		if referenced {
			continue
		}
		if err := root.Remove(path); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
