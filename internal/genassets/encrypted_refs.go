package genassets

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// encryptedRefsDir holds the per-Library reference ledger for encrypted
// generated assets (CORE-2193). Assets are stored per content fingerprint, so
// the same directory can serve several Libraries, and an Encrypted Library's
// fingerprints live only in its sealed database. Each Encrypted Library that
// publishes (or, at unseal, keeps) ciphertext under a fingerprint directory
// holds an empty marker named after that directory in its own ledger folder,
// so a purge can tell which directories it alone referenced without the
// Library's keys or database. The encrypted subtitle cache attributes its
// objects to a Library folder the same way.
const encryptedRefsDir = ".encrypted-refs"

// encryptedRefsLibraryRel is one Library's ledger folder. The Library ID is
// hashed so a caller-influenced ID can never form a path element of its own.
func encryptedRefsLibraryRel(libraryID store.LibraryID) string {
	sum := sha256.Sum256([]byte(libraryID))
	return filepath.Join(encryptedRefsDir, hex.EncodeToString(sum[:16]))
}

// recordEncryptedReference marks fingerprintDir as referenced by libraryID.
// The caller holds the publish lock.
func recordEncryptedReference(root *os.Root, libraryID store.LibraryID, fingerprintDir string) error {
	rel := encryptedRefsLibraryRel(libraryID)
	if err := ensureRootDir(root, encryptedRefsDir); err != nil {
		return err
	}
	if err := ensureRootDir(root, rel); err != nil {
		return err
	}
	return root.WriteFile(filepath.Join(rel, fingerprintDir), nil, 0o600)
}

// PurgeEncryptedLibraryAssets removes a purged Encrypted Library's generated
// asset ciphertext that no other Encrypted Library's ledger references, then
// collects the generations left unreferenced and drops the Library's ledger.
// In a directory the Library alone referenced only files isObject recognises
// as ciphertext are removed: a public Library's plaintext assets at the same
// fingerprint stay. A directory another Encrypted Library also references is
// kept whole, since without either Library's keys the files cannot be told
// apart; the last of them to be purged removes it. It needs no keys and no
// store access, so it runs after the Library's database is gone. Any failure
// leaves the ledger in place, so a retried purge repeats the whole sweep.
func (s *Service) PurgeEncryptedLibraryAssets(libraryID store.LibraryID, isObject func(io.Reader) (bool, error)) error {
	if libraryID == "" {
		return nil
	}
	if isObject == nil {
		return errors.New("generated assets: purge needs an encrypted-object header check")
	}
	s.publish.Lock()
	defer s.publish.Unlock()
	root, err := os.OpenRoot(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	own := encryptedRefsLibraryRel(libraryID)
	markers, err := fs.ReadDir(root.FS(), own)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	shared, err := otherEncryptedReferences(root, filepath.Base(own))
	if err != nil {
		return err
	}
	var joined error
	for _, marker := range markers {
		name := marker.Name()
		if strings.HasPrefix(name, ".") || shared[name] {
			continue
		}
		joined = errors.Join(joined, removeCiphertextAssets(root, name, isObject))
	}
	if joined != nil {
		return joined
	}
	if _, err := collectUnreferencedGenerations(root); err != nil {
		return err
	}
	return root.RemoveAll(own)
}

// otherEncryptedReferences returns the fingerprint directories any Library's
// ledger other than own references.
func otherEncryptedReferences(root *os.Root, own string) (map[string]bool, error) {
	libraries, err := fs.ReadDir(root.FS(), encryptedRefsDir)
	if err != nil {
		return nil, err
	}
	shared := map[string]bool{}
	for _, library := range libraries {
		if library.Name() == own || !library.IsDir() {
			continue
		}
		markers, err := fs.ReadDir(root.FS(), filepath.Join(encryptedRefsDir, library.Name()))
		if err != nil {
			return nil, err
		}
		for _, marker := range markers {
			shared[marker.Name()] = true
		}
	}
	return shared, nil
}

// removeCiphertextAssets removes every regular file in dir that carries an
// encrypted-object header, and dir itself once it is empty.
func removeCiphertextAssets(root *os.Root, dir string, isObject func(io.Reader) (bool, error)) error {
	info, err := root.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return err
	}
	var joined error
	remaining := 0
	for _, entry := range entries {
		name := filepath.Join(dir, entry.Name())
		ciphertext := false
		if entry.Type().IsRegular() {
			ciphertext, err = rootedFileIsCiphertext(root, name, isObject)
			if err != nil {
				joined = errors.Join(joined, err)
				continue
			}
		}
		if !ciphertext {
			remaining++
			continue
		}
		if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			joined = errors.Join(joined, err)
		}
	}
	if joined == nil && remaining == 0 {
		if err := root.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return joined
}

// dirHasEntries reports whether dir exists under root and holds anything.
func dirHasEntries(root *os.Root, dir string) (bool, error) {
	entries, err := fs.ReadDir(root.FS(), dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return len(entries) > 0, err
}
