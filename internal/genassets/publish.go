package genassets

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/beamfall/core/internal/store"
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
	// encrypted, when set, routes the publication into an Encrypted Library's
	// protected plane: staged files are written as ciphertext and content
	// identity is the hash of the decrypted bytes.
	encrypted *encryptedTarget
}

// encryptedTarget names the Encrypted Library a publication belongs to and the
// cipher that writes and reads its objects.
type encryptedTarget struct {
	libraryID store.LibraryID
	objects   EncryptedObjects
}

func (s *Service) publishBytes(owner, source, contract, name string, data []byte) error {
	return s.publishData(nil, owner, source, contract, name, data)
}

// publishData publishes in-memory asset bytes. With a target the bytes are
// encrypted as they are staged, so no plaintext copy reaches the disk.
func (s *Service) publishData(target *encryptedTarget, owner, source, contract, name string, data []byte) error {
	publication, err := s.beginPublication(owner, source, contract, name)
	if err != nil {
		return err
	}
	defer publication.close()
	publication.encrypted = target
	if err := publication.writeStaged(name, data); err != nil {
		return err
	}
	return publication.commit(name, publication.publicName)
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

// readAsset reads a published asset's bytes, decrypting an Encrypted Library's
// ciphertext object in memory.
func (s *Service) readAsset(libraryID store.LibraryID, fingerprint, name string) ([]byte, error) {
	if !s.isEncrypted(libraryID) {
		root, err := os.OpenRoot(s.dir)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		return root.ReadFile(filepath.Join(assetFingerprintDir(fingerprint), name))
	}
	path := filepath.Join(s.assetDir(fingerprint), name)
	if s.objects == nil {
		return nil, errors.New("generated assets: encrypted plane is not wired")
	}
	reader, err := s.objects.OpenDecryptReader(libraryID, path, encryptedClassGeneratedAsset, generatedAssetObjectID(fingerprint, name))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

// writeStaged writes one asset into the stage directory: as a plaintext file
// for a public Library, or as a ciphertext object bound to its generated-asset
// context for an Encrypted Library.
func (p *assetPublication) writeStaged(name string, data []byte) error {
	stagePath := filepath.Join(p.stageDir, name)
	if p.encrypted == nil {
		return p.root.WriteFile(stagePath, data, 0o600)
	}
	return p.encrypted.objects.EncryptObject(p.encrypted.libraryID, bytes.NewReader(data),
		filepath.Join(p.root.Name(), stagePath), encryptedClassGeneratedAsset, generatedAssetObjectID(p.source, name))
}

// Encrypted-object contexts for the objects genassets reads and writes. They
// mirror libencrypt's ObjectClassMediaBytes / GeneratedAssetObjectContext;
// genassets does not import libencrypt (ARCHITECTURE.md §4), and the encrypted
// tests round-trip through the real libencrypt.Manager to keep them in step.
const (
	encryptedClassMediaBytes     = "media-bytes"
	encryptedClassGeneratedAsset = "generated-asset"
)

// generatedAssetObjectID is the immutable object id a generated asset's
// ciphertext is bound to: the source content fingerprint plus the asset name.
func generatedAssetObjectID(fingerprint, name string) string {
	return fingerprint + "/" + name
}

// contentHash is the content identity of a rooted asset file. An encrypted
// asset is identified by its decrypted bytes, since each encryption is
// randomized; a file that does not decrypt under the asset's context fails.
func (p *assetPublication) contentHash(name, publicName string) ([sha256.Size]byte, error) {
	if p.encrypted == nil {
		return rootedFileHash(p.root, name)
	}
	reader, err := p.encrypted.objects.OpenDecryptReader(p.encrypted.libraryID,
		filepath.Join(p.root.Name(), name), encryptedClassGeneratedAsset,
		generatedAssetObjectID(p.source, filepath.Base(publicName)))
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer reader.Close()
	return readerHash(reader)
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
	if p.encrypted != nil {
		if p.service.purgedEncrypted[p.encrypted.libraryID] {
			return ErrEncryptedLibraryPurged
		}
		// Attribute the fingerprint to the Library before its ciphertext is
		// public, so a purge never meets an asset it cannot attribute.
		if err := recordEncryptedReference(p.root, p.encrypted.libraryID, assetFingerprintDir(p.source)); err != nil {
			return err
		}
	}
	contentHash, err := p.contentHash(stagePath, publicName)
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
		matches, matchErr := p.pathHasHash(generationPath, publicName, contentHash)
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
		matches, matchErr := p.pathHasHash(publicName, publicName, contentHash)
		if matchErr != nil {
			return matchErr
		}
		if !matches {
			return fmt.Errorf("%w: public identity already contains different content", ErrPublicationConflict)
		}
	}
	return nil
}

func (p *assetPublication) pathHasHash(name, publicName string, want [sha256.Size]byte) (bool, error) {
	info, err := p.root.Lstat(name)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%w: publication target is not a regular file", ErrPublicationConflict)
	}
	got, err := p.contentHash(name, publicName)
	return got == want, err
}

func rootedFileHash(root *os.Root, name string) ([sha256.Size]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	return readerHash(file)
}

func readerHash(r io.Reader) ([sha256.Size]byte, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, r); err != nil {
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
		if path == assetStagingDir || path == assetGenerationDir || path == encryptedRefsDir {
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
