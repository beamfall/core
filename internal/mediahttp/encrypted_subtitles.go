package mediahttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// encryptedSubtitleDirName is the subtitle-cache subdirectory that holds the
// ciphertext WebVTT objects of Encrypted Libraries (ADR-0235 §2). Plaintext
// sidecars of public libraries stay at the top level of the cache, so the two
// classes never share a directory and the plaintext purge, which only walks the
// top level, cannot reach a ciphertext object.
const encryptedSubtitleDirName = "encrypted"

// encryptedSubtitleLibraryRel is the cache-relative directory for one Encrypted
// Library's subtitle objects. The Library ID is hashed so a caller-influenced ID
// can never form a path element of its own.
func encryptedSubtitleLibraryRel(libraryID store.LibraryID) string {
	sum := sha256.Sum256([]byte(libraryID))
	return filepath.Join(encryptedSubtitleDirName, hex.EncodeToString(sum[:16]))
}

// validEncryptedSubtitleName reports whether name has the "<fp>.<index>.vtt"
// shape the encrypted cache writes. Staging temps (leading dot) and anything
// else fail it.
func validEncryptedSubtitleName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	stem, ok := strings.CutSuffix(name, ".vtt")
	if !ok {
		return false
	}
	dot := strings.LastIndexByte(stem, '.')
	if dot <= 0 {
		return false
	}
	index, err := strconv.Atoi(stem[dot+1:])
	return err == nil && index >= 0 && strconv.Itoa(index) == stem[dot+1:]
}

// openSubtitleRoot opens the subtitle cache through one descriptor and checks it
// is still the directory the handler was configured with. A missing cache
// returns (nil, nil).
func (h *Handler) openSubtitleRoot() (*os.Root, error) {
	if h.subtitleDir == "" {
		return nil, nil
	}
	root, err := os.OpenRoot(h.subtitleDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if err := verifyRootUnreplaced(root, h.subtitleDir); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

// ReconcileEncryptedSubtitles is the unseal-time sweep of one Encrypted
// Library's encrypted subtitle cache. It keeps every regular file with the cache
// naming whose bytes start with a libencrypt object header (isCiphertext) and
// removes everything else: staging temps, plaintext, symlinks and directories. A
// missing cache is a no-op. It never calls into the key manager, so it is safe
// to run while the store holds its lock during unseal.
func (h *Handler) ReconcileEncryptedSubtitles(libraryID store.LibraryID, isCiphertext func(io.Reader) (bool, error)) error {
	if libraryID == "" || isCiphertext == nil {
		return errors.New("mediahttp: reconcile needs a library and a ciphertext check")
	}
	root, err := h.openSubtitleRoot()
	if err != nil || root == nil {
		return err
	}
	defer root.Close()
	dir, err := root.OpenRoot(encryptedSubtitleLibraryRel(libraryID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer dir.Close()
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return err
	}
	var joined error
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && validEncryptedSubtitleName(name) {
			keep, err := rootedFileIsCiphertext(dir, name, isCiphertext)
			if err != nil {
				joined = errors.Join(joined, err)
				continue
			}
			if keep {
				continue
			}
		}
		if err := dir.RemoveAll(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func rootedFileIsCiphertext(dir *os.Root, name string, isCiphertext func(io.Reader) (bool, error)) (bool, error) {
	file, err := dir.Open(name)
	if err != nil {
		return false, err
	}
	defer file.Close()
	return isCiphertext(file)
}

// PurgeEncryptedDerivatives removes one Library's whole encrypted subtitle
// cache. The DEK rewrap calls it before re-encrypting anything else: the cache
// is regenerable from the source, so it is purged rather than rewrapped
// (ADR-0235). A missing cache is a no-op.
func (h *Handler) PurgeEncryptedDerivatives(ctx context.Context, libraryID store.LibraryID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if libraryID == "" {
		return errors.New("mediahttp: purge needs a library")
	}
	root, err := h.openSubtitleRoot()
	if err != nil || root == nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(encryptedSubtitleLibraryRel(libraryID))
}
