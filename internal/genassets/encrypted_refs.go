package genassets

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
//
// Ciphertext written before the ledger existed is attributed only when its
// Library's unseal reconcile backfills the ledger, so a ledger is trusted to
// be complete only once encryptedRefsComplete is in it: the reconcile writes
// it after the backfill, and every new Encrypted Library is unsealed (so
// reconciled) when it is provisioned. A purge deletes nothing while any other
// Encrypted Library's ledger is incomplete, since that Library may share any
// directory.
const encryptedRefsDir = ".encrypted-refs"

// encryptedRefsComplete marks a ledger as listing every directory its Library
// references. Fingerprint directories never begin with a dot.
const encryptedRefsComplete = ".complete"

// ErrEncryptedLibraryPurged refuses to publish an asset for an Encrypted
// Library whose generated assets have been purged: a generation that chose
// its target before the purge must not recreate ciphertext the purge removed.
var ErrEncryptedLibraryPurged = errors.New("generated assets: the Encrypted Library has been purged")

// EncryptedAssetPurge reports what an Encrypted Library's asset purge could
// not remove. The leftovers are ciphertext that is unreadable once the
// Library's keys are erased.
type EncryptedAssetPurge struct {
	// Unswept explains why some of the Library's ciphertext may remain on
	// disk; it is empty after a complete sweep.
	Unswept string
}

// encryptedRefsLibraryRel is one Library's ledger folder. The Library ID is
// hashed so a caller-influenced ID can never form a path element of its own.
func encryptedRefsLibraryRel(libraryID store.LibraryID) string {
	sum := sha256.Sum256([]byte(libraryID))
	return filepath.Join(encryptedRefsDir, hex.EncodeToString(sum[:16]))
}

// recordEncryptedReference durably marks fingerprintDir as referenced by
// libraryID, so the marker is on disk before any ciphertext it covers. The
// caller holds the publish lock.
func recordEncryptedReference(root *os.Root, libraryID store.LibraryID, fingerprintDir string) error {
	return writeLedgerMarker(root, libraryID, fingerprintDir)
}

// MarkEncryptedReferencesComplete records that libraryID's ledger lists every
// generated-asset directory it references. The unseal reconcile calls it once
// every live fingerprint has been reconciled and attributed.
func (s *Service) MarkEncryptedReferencesComplete(libraryID store.LibraryID) error {
	if libraryID == "" {
		return nil
	}
	s.publish.Lock()
	defer s.publish.Unlock()
	if s.purgedEncrypted[libraryID] {
		return ErrEncryptedLibraryPurged
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return writeLedgerMarker(root, libraryID, encryptedRefsComplete)
}

// writeLedgerMarker writes an empty marker in libraryID's ledger and syncs it
// and every directory above it. An existing marker is synced again: an
// earlier write may have created it and then failed to sync, so its presence
// alone does not prove it is durable.
func writeLedgerMarker(root *os.Root, libraryID store.LibraryID, name string) error {
	rel := encryptedRefsLibraryRel(libraryID)
	marker := filepath.Join(rel, name)
	if err := ensureRootDir(root, encryptedRefsDir); err != nil {
		return err
	}
	if err := ensureRootDir(root, rel); err != nil {
		return err
	}
	file, err := root.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return err
	}
	return syncRootDirs(root, rel, encryptedRefsDir, ".")
}

// syncRootDirs fsyncs each directory under root, so entries created in or
// removed from it are durable.
func syncRootDirs(root *os.Root, dirs ...string) error {
	for _, dir := range dirs {
		f, err := root.Open(dir)
		if err != nil {
			return err
		}
		if err := errors.Join(f.Sync(), f.Close()); err != nil {
			return err
		}
	}
	return nil
}

// PurgeEncryptedLibraryAssets removes a purged Encrypted Library's generated
// asset ciphertext that no other Encrypted Library references, then collects
// the generations left unreferenced and drops the Library's ledger. others
// lists every Encrypted Library still in the catalog. In a directory the
// Library alone referenced only files isObject recognises as ciphertext are
// removed: a public Library's plaintext assets at the same fingerprint stay. A
// directory another Encrypted Library also references is kept whole, since
// without either Library's keys the files cannot be told apart; the last of
// them to be purged removes it. While any other Encrypted Library's ledger is
// incomplete nothing is removed, and a Library whose own ledger is incomplete
// can leave directories it never recorded; both are reported in Unswept. It
// needs no keys and no store access, so it runs after the Library's database
// is gone. From the first call on, publication for the Library is refused. The
// deletions are synced before the ledger is dropped, and any failure leaves
// the ledger in place, so a retried purge repeats the whole sweep.
func (s *Service) PurgeEncryptedLibraryAssets(libraryID store.LibraryID, others []store.LibraryID, isObject func(io.Reader) (bool, error)) (EncryptedAssetPurge, error) {
	var result EncryptedAssetPurge
	if libraryID == "" {
		return result, nil
	}
	if isObject == nil {
		return result, errors.New("generated assets: purge needs an encrypted-object header check")
	}
	s.publish.Lock()
	defer s.publish.Unlock()
	if s.purgedEncrypted == nil {
		s.purgedEncrypted = map[store.LibraryID]bool{}
	}
	s.purgedEncrypted[libraryID] = true
	root, err := os.OpenRoot(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer root.Close()
	own := encryptedRefsLibraryRel(libraryID)
	markers, err := fs.ReadDir(root.FS(), own)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	ownComplete, err := ledgerComplete(root, libraryID)
	if err != nil {
		return result, err
	}
	incomplete := 0
	for _, other := range others {
		if other == libraryID {
			continue
		}
		complete, err := ledgerComplete(root, other)
		if err != nil {
			return result, err
		}
		if !complete {
			incomplete++
		}
	}
	switch {
	case incomplete > 0:
		result.Unswept = fmt.Sprintf("%d other Encrypted Libraries have not recorded the generated assets they share; ciphertext kept", incomplete)
	case !ownComplete:
		result.Unswept = "the Library never recorded every generated asset it references; unrecorded ciphertext kept"
	}
	if incomplete == 0 && len(markers) > 0 {
		if err := s.removeUnsharedCiphertext(root, filepath.Base(own), markers, isObject); err != nil {
			return result, err
		}
	}
	if err := root.RemoveAll(own); err != nil {
		return result, err
	}
	if err := syncRootDirs(root, encryptedRefsDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	return result, nil
}

// removeUnsharedCiphertext removes the ciphertext in every directory the own
// ledger lists and no other ledger does, collects the generations left
// unreferenced, and syncs both before the caller drops the ledger.
func (s *Service) removeUnsharedCiphertext(root *os.Root, own string, markers []fs.DirEntry, isObject func(io.Reader) (bool, error)) error {
	shared, err := otherEncryptedReferences(root, own)
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
	return syncRootDirs(root, assetGenerationDir, ".")
}

// ledgerComplete reports whether libraryID's ledger is marked complete.
func ledgerComplete(root *os.Root, libraryID store.LibraryID) (bool, error) {
	_, err := root.Lstat(filepath.Join(encryptedRefsLibraryRel(libraryID), encryptedRefsComplete))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// otherEncryptedReferences returns the fingerprint directories any Library's
// ledger other than own references.
func otherEncryptedReferences(root *os.Root, own string) (map[string]bool, error) {
	shared := map[string]bool{}
	libraries, err := fs.ReadDir(root.FS(), encryptedRefsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return shared, nil
	}
	if err != nil {
		return nil, err
	}
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
// encrypted-object header, and dir itself once it is empty. A directory that
// keeps entries is synced so the removals are durable.
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
	if joined != nil {
		return joined
	}
	if remaining > 0 {
		return syncRootDirs(root, dir)
	}
	if err := root.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// dirHasEntries reports whether dir exists under root and holds anything.
func dirHasEntries(root *os.Root, dir string) (bool, error) {
	entries, err := fs.ReadDir(root.FS(), dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return len(entries) > 0, err
}
