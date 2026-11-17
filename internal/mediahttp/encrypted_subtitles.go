package mediahttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/playback/subtitleingest"
	"github.com/beamfall/core/internal/store"
)

// EncryptedSubtitleRequest names one muxed subtitle stream of an Encrypted
// Library's media file to extract in memory.
type EncryptedSubtitleRequest struct {
	LibraryID store.LibraryID
	// SourcePath and Fingerprint address the ciphertext media object and its
	// immutable identity.
	SourcePath  string
	Fingerprint string
	// StreamIndex is the absolute ffprobe stream index.
	StreamIndex int
	// MaxBytes bounds the extracted WebVTT.
	MaxBytes int64
	// Revoked reports a non-nil error once the request may no longer read the
	// source: the Library is Sealed or the request's grant no longer verifies.
	Revoked func(context.Context) error
}

// EncryptedSubtitleExtractor extracts one subtitle stream of an Encrypted
// Library as WebVTT through the shared encrypted-derivative helper
// (encderiv.RunCapture): FFmpeg reads the decrypted source from a per-item
// loopback endpoint and the output is captured in memory, never on disk.
type EncryptedSubtitleExtractor interface {
	ExtractEncryptedSubtitle(ctx context.Context, req EncryptedSubtitleRequest) ([]byte, error)
}

// EncryptedObjectWriter encrypts src into a libencrypt object at ciphertextPath
// under the Library's current key, bound to the given object identity. The
// plaintext never reaches disk.
type EncryptedObjectWriter interface {
	EncryptObject(libraryID store.LibraryID, src io.Reader, ciphertextPath, objectClass, immutableObjectID string) error
}

// SetEncryptedSubtitles wires subtitle extraction for Unsealed Encrypted
// Libraries. Serving the cached object also needs SetEncryptedBytes. While any
// of the three is missing, an Encrypted Library's subtitle is refused.
func (h *Handler) SetEncryptedSubtitles(extractor EncryptedSubtitleExtractor, writer EncryptedObjectWriter) {
	h.encryptedSubtitles = extractor
	h.encryptedObjects = writer
}

// encryptedSubtitleObjectID is the immutable identity a cached subtitle object
// is bound to. Generated assets use "<fingerprint>/<name>", and a fingerprint
// never starts with "subtitle/", so the two cannot collide.
func encryptedSubtitleObjectID(fingerprint string, streamIndex int) string {
	return fmt.Sprintf("subtitle/%s/%d", fingerprint, streamIndex)
}

// serveEncryptedSubtitle serves an Unsealed Encrypted Library's subtitle from
// its ciphertext cache, extracting it first when the cache has no valid object.
// It refuses with the same 404 an Encrypted Library got before this path
// existed when the pipeline is not wired, and with 404 when the request is
// revoked while it runs.
func (h *Handler) serveEncryptedSubtitle(w http.ResponseWriter, r *http.Request, libraryID store.LibraryID, file store.MediaFile, streamIndex int, name string, revoked func(context.Context) error) {
	if h.encryptedSubtitles == nil || h.encryptedObjects == nil || h.encryptedBytes == nil {
		http.Error(w, "subtitle unavailable", http.StatusNotFound)
		return
	}
	vtt, err := h.encryptedSubtitle(r.Context(), libraryID, file, streamIndex, revoked)
	if errors.Is(err, errSubtitlePending) {
		writeSubtitlePending(w)
		return
	}
	if err != nil {
		if revoked(r.Context()) != nil {
			http.Error(w, "media unavailable", http.StatusNotFound)
			return
		}
		slog.Error("encrypted subtitle extract failed", "error", err.Error())
		http.Error(w, "subtitle unavailable", http.StatusServiceUnavailable)
		return
	}
	// Authorization is checked again right before the bytes leave: a request
	// that waited on another's extraction, or hit the cache, would otherwise
	// serve plaintext the requester lost access to in the meantime.
	if revoked(r.Context()) != nil {
		clear(vtt)
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(vtt))
}

// encryptedSubtitle returns the sanitised WebVTT for one stream, decrypted from
// the Library's subtitle cache or, on a miss, extracted in memory, hardened in
// memory and written to the cache as a ciphertext object. A cache object that
// no longer decrypts (a stale key, a torn or tampered file) is removed and
// regenerated once.
func (h *Handler) encryptedSubtitle(ctx context.Context, libraryID store.LibraryID, file store.MediaFile, streamIndex int, revoked func(context.Context) error) ([]byte, error) {
	if file.ContentFingerprint == "" {
		return nil, errors.New("media file has no content fingerprint")
	}
	dir, err := h.ensureEncryptedSubtitleDir(libraryID)
	if err != nil {
		return nil, err
	}
	member, err := newRootedPath(dir, fmt.Sprintf("%s.%d.vtt", safeSubtitleFingerprint(file.ContentFingerprint), streamIndex))
	if err != nil {
		return nil, err
	}
	objectID := encryptedSubtitleObjectID(file.ContentFingerprint, streamIndex)
	if vtt, ok := h.decryptCachedSubtitle(libraryID, member, objectID); ok {
		return vtt, nil
	}
	// One extraction per (file, stream); other subtitles never queue behind
	// it, and a request that waits too long gets errSubtitlePending.
	unlock, err := h.subtitleJobs.lockKey(ctx, objectID, h.subtitleWaitLimit())
	if err != nil {
		return nil, err
	}
	defer unlock()
	if vtt, ok := h.decryptCachedSubtitle(libraryID, member, objectID); ok {
		return vtt, nil
	}
	if err := removeRootedMember(member); err != nil {
		return nil, err
	}
	raw, err := h.encryptedSubtitles.ExtractEncryptedSubtitle(ctx, EncryptedSubtitleRequest{
		LibraryID:   libraryID,
		SourcePath:  file.Path,
		Fingerprint: file.ContentFingerprint,
		StreamIndex: streamIndex,
		MaxBytes:    subtitleingest.MaxTextBytes,
		Revoked:     revoked,
	})
	if err != nil {
		return nil, err
	}
	vtt, err := hardenWebVTTInMemory(raw)
	clear(raw)
	if err != nil {
		return nil, err
	}
	if err := revoked(ctx); err != nil {
		return nil, err
	}
	if err := h.encryptedObjects.EncryptObject(libraryID, bytes.NewReader(vtt), member.String(), encryptedClassGeneratedAsset, objectID); err != nil {
		return nil, err
	}
	return vtt, nil
}

// decryptCachedSubtitle decrypts a cached subtitle object. A missing entry, a
// non-regular entry or an object that fails to decrypt is a miss.
func (h *Handler) decryptCachedSubtitle(libraryID store.LibraryID, member rootedPath, objectID string) ([]byte, bool) {
	if !rootedRegularFileExists(member) {
		return nil, false
	}
	vtt, err := h.encryptedBytes.DecryptBytes(libraryID, member.String(), encryptedClassGeneratedAsset, objectID)
	if err != nil {
		return nil, false
	}
	return vtt, true
}

// removeRootedMember removes a cache member through a descriptor on its root.
// A missing member is fine.
func removeRootedMember(p rootedPath) error {
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := verifyRootUnreplaced(root, p.root); err != nil {
		return err
	}
	if err := root.RemoveAll(p.rel); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// hardenWebVTTInMemory applies the subtitle ingest boundary that
// hardenExtractedWebVTT applies to a staged file (size cap, content guard,
// sanitisation) to bytes already in memory.
func hardenWebVTTInMemory(raw []byte) ([]byte, error) {
	format := string(subtitleingest.FormatVTT)
	if err := subtitleingest.CheckSize(format, int64(len(raw))); err != nil {
		return nil, err
	}
	if err := subtitleingest.Guard(format, raw); err != nil {
		return nil, err
	}
	return []byte(subtitleingest.SanitizeWebVTT(string(raw))), nil
}

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

// ensureEncryptedSubtitleDir creates one Library's encrypted subtitle directory
// through a descriptor on the subtitle cache and refuses it unless every
// component is a real directory, so a planted symlink can never redirect the
// cache's writes and removals outside the subtitle cache.
func (h *Handler) ensureEncryptedSubtitleDir(libraryID store.LibraryID) (string, error) {
	if h.subtitleDir == "" {
		return "", errors.New("mediahttp: subtitle cache is not configured")
	}
	if err := os.MkdirAll(h.subtitleDir, 0o700); err != nil {
		return "", err
	}
	root, err := h.openSubtitleRoot()
	if err != nil {
		return "", err
	}
	if root == nil {
		return "", os.ErrNotExist
	}
	defer root.Close()
	rel := encryptedSubtitleLibraryRel(libraryID)
	if err := root.MkdirAll(rel, 0o700); err != nil {
		return "", err
	}
	walked := ""
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		walked = filepath.Join(walked, part)
		info, err := root.Lstat(walked)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%w: %s is not a directory", errRootScope, filepath.ToSlash(walked))
		}
	}
	return filepath.Join(h.subtitleDir, rel), nil
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
