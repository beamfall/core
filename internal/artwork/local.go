package artwork

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/beamfall/core/internal/store"
)

// SourceLocal is the lock source for artwork imported from a curated image
// file beside the Media (CORE-0359). Local artwork outranks scraped and
// generated artwork; only a manual pick replaces it (ADR-0072).
const SourceLocal = "local"

// maxLocalArtworkBytes caps how much of a sidecar image is read into memory.
const maxLocalArtworkBytes = 32 << 20

// ErrSlotLocked reports that an artwork slot holds locked artwork that a
// lower-precedence writer (a scrape) must not replace.
var ErrSlotLocked = errors.New("artwork slot is locked")

// SlotLockedError names the locked slot and the source that locked it. It
// matches ErrSlotLocked under errors.Is.
type SlotLockedError struct {
	Slot   string
	Source string
}

func (e *SlotLockedError) Error() string {
	return fmt.Sprintf("artwork slot %q is locked by %s artwork", e.Slot, e.Source)
}

// Is makes errors.Is(err, ErrSlotLocked) hold for every SlotLockedError.
func (e *SlotLockedError) Is(target error) bool { return target == ErrSlotLocked }

// slotLock is the file-backed lock marker stored beside a slot file. It records
// where locked artwork came from so a rescan can tell an unchanged sidecar from
// an edited one without a schema column (no migration).
type slotLock struct {
	Source    string `json:"source"`
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size,omitempty"`
	ModTimeNS int64  `json:"mtime_ns,omitempty"`
}

func lockMarkerPath(slotPath string) string { return slotPath + ".lock" }

// readSlotLock reports the lock marker beside slotPath. An unreadable or
// unparseable marker still locks the slot: it fails closed against scrapes,
// and a rescan of a matching sidecar rewrites it.
func readSlotLock(slotPath string) (slotLock, bool) {
	marker := lockMarkerPath(slotPath)
	info, err := os.Lstat(marker)
	if err != nil {
		return slotLock{}, false
	}
	lock := slotLock{Source: SourceLocal}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return lock, true
	}
	// #nosec G304 -- marker is derived from the server-owned artwork root and a validated slot.
	data, err := os.ReadFile(marker)
	if err != nil {
		return lock, true
	}
	var parsed slotLock
	if json.Unmarshal(data, &parsed) != nil || parsed.Source == "" {
		return lock, true
	}
	return parsed, true
}

// IsArtworkLocked reports whether a Media item's slot carries a lock marker.
func (s *Service) IsArtworkLocked(id store.MediaID, slot string) bool {
	if !ValidSlot(slot) {
		return false
	}
	_, locked := readSlotLock(s.ArtworkPath(id, slot))
	return locked
}

// IsEntityArtworkLocked reports whether an Entity's slot carries a lock marker.
func (s *Service) IsEntityArtworkLocked(id store.EntityID, slot string) bool {
	if !ValidSlot(slot) {
		return false
	}
	_, locked := readSlotLock(s.EntityArtworkPath(id, slot))
	return locked
}

// ImportManualArtwork stores operator-picked artwork bytes in a Media slot.
// A manual pick outranks every lock (ADR-0072), so unlike ImportArtwork it
// replaces locked local artwork. The lock marker is kept: an unchanged sidecar
// does not take the slot back, and an edited sidecar still does.
func (s *Service) ImportManualArtwork(ctx context.Context, id store.MediaID, slot string, data []byte) error {
	return s.importMediaArtwork(ctx, id, slot, data, false)
}

// ImportLocalArtwork imports a curated sidecar image file into a Media slot
// and locks the slot with source local. It reports whether the slot changed:
// an unchanged sidecar (same path, size and modification time as the lock
// marker, with the slot file still present) is a no-op. Sealed and Encrypted
// Libraries import nothing and get no marker (ADR-0045). The sidecar must be
// an absolute path inside the absolute library root, it is opened through
// that root so no symlink can lead outside it, the file itself must be a
// regular file (a symlink is refused) within the size cap, and its bytes must
// be a JPEG, PNG or WebP image.
func (s *Service) ImportLocalArtwork(ctx context.Context, id store.MediaID, slot, root, path string) (bool, error) {
	if !ValidSlot(slot) {
		return false, fmt.Errorf("unknown artwork slot %q", slot)
	}
	if s.isSealed(ctx, id) || s.isEncrypted(ctx, id) {
		return false, nil
	}
	return s.importLocal(ctx, s.ArtworkPath(id, slot), string(id), slot, root, path)
}

// ReleaseLocalArtwork removes a Media slot's local lock when no sidecar
// matches the slot any more. observedAt is when the caller looked for
// sidecars: a lock written at or after it is kept, so a stale caller cannot
// unlock artwork a newer import just locked. The artwork itself stays; later
// scrapes may replace it again.
func (s *Service) ReleaseLocalArtwork(ctx context.Context, id store.MediaID, slot string, observedAt time.Time) error {
	if !ValidSlot(slot) {
		return fmt.Errorf("unknown artwork slot %q", slot)
	}
	return s.releaseLocal(ctx, s.ArtworkPath(id, slot), observedAt)
}

// ImportEntityLocalArtwork is ImportLocalArtwork for an Entity slot, used for
// ordered container artwork. The Entity's Library gates it the same way and a
// lookup failure fails closed. A local poster is the Entity's own, so like
// ImportEntityPoster it clears the inherited mark and the sweep leaves it
// alone.
func (s *Service) ImportEntityLocalArtwork(ctx context.Context, id store.EntityID, slot, root, path string) (bool, error) {
	if !ValidSlot(slot) {
		return false, fmt.Errorf("unknown artwork slot %q", slot)
	}
	if s.entityProtected(ctx, id) {
		return false, nil
	}
	dst := s.EntityArtworkPath(id, slot)
	if slot != SlotPoster {
		return s.importLocal(ctx, dst, string(id), slot, root, path)
	}
	s.entityPosterMu.Lock()
	defer s.entityPosterMu.Unlock()
	changed, err := s.importLocal(ctx, dst, string(id), slot, root, path)
	if err != nil {
		return changed, err
	}
	if err := os.Remove(inheritedMarkerPath(dst)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return changed, err
	}
	return changed, nil
}

// ReleaseEntityLocalArtwork is ReleaseLocalArtwork for an Entity slot.
func (s *Service) ReleaseEntityLocalArtwork(ctx context.Context, id store.EntityID, slot string, observedAt time.Time) error {
	if !ValidSlot(slot) {
		return fmt.Errorf("unknown artwork slot %q", slot)
	}
	return s.releaseLocal(ctx, s.EntityArtworkPath(id, slot), observedAt)
}

// entityProtected reports whether the Entity's Library is Sealed or Encrypted,
// failing closed when the Library cannot be resolved.
func (s *Service) entityProtected(ctx context.Context, id store.EntityID) bool {
	if s.sealed == nil && s.encrypted == nil {
		return false
	}
	entity, err := s.store.Entity(ctx, id, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		return true
	}
	return (s.sealed != nil && s.sealed(entity.LibraryID)) || (s.encrypted != nil && s.encrypted(entity.LibraryID))
}

func (s *Service) importLocal(ctx context.Context, dst, owner, slot, root, src string) (bool, error) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(src) {
		return false, fmt.Errorf("local artwork path %q or root %q is not absolute", src, root)
	}
	rel, err := filepath.Rel(root, src)
	if err != nil || !filepath.IsLocal(rel) {
		return false, fmt.Errorf("local artwork %q is outside the library root", src)
	}
	libraryRoot, err := os.OpenRoot(root)
	if err != nil {
		return false, err
	}
	defer libraryRoot.Close()
	info, err := libraryRoot.Lstat(rel)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("local artwork %q is not a regular file", src)
	}
	if info.Size() == 0 || info.Size() > maxLocalArtworkBytes {
		return false, fmt.Errorf("local artwork %q has unsupported size %d", src, info.Size())
	}
	unlock, err := s.lockDerivative(ctx, filepath.Dir(dst))
	if err != nil {
		return false, err
	}
	defer unlock()
	want := slotLock{Source: SourceLocal, Path: src, Size: info.Size(), ModTimeNS: info.ModTime().UnixNano()}
	current, hadLock := readSlotLock(dst)
	if hadLock && current == want && nonEmptyRegularFile(dst) && !regularFileExists(generatedMarkerPath(dst)) {
		return false, nil
	}
	data, err := readLocalImage(libraryRoot, rel, src, info)
	if err != nil {
		return false, err
	}
	// Lock before publishing: a pending marker (no size or time, so it never
	// matches a sidecar) protects the slot if the import is interrupted, and
	// the next rescan retries it.
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	if err := writeSlotLock(dst, slotLock{Source: SourceLocal, Path: src}); err != nil {
		return false, err
	}
	if err := s.importArtwork(ctx, dst, owner, slot, data); err != nil {
		if !hadLock {
			_ = os.Remove(lockMarkerPath(dst))
		}
		return false, err
	}
	if err := os.Remove(generatedMarkerPath(dst)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := writeSlotLock(dst, want); err != nil {
		return false, err
	}
	return true, nil
}

func writeSlotLock(dst string, lock slotLock) error {
	marker, err := json.Marshal(lock)
	if err != nil {
		return err
	}
	return writeAtomicFile(lockMarkerPath(dst), marker, nil)
}

func (s *Service) releaseLocal(ctx context.Context, dst string, observedAt time.Time) error {
	unlock, err := s.lockDerivative(ctx, filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer unlock()
	current, ok := readSlotLock(dst)
	if !ok || current.Source != SourceLocal {
		return nil
	}
	// A lock written after the caller looked for sidecars belongs to a newer
	// import the caller never saw.
	if info, err := os.Stat(lockMarkerPath(dst)); err == nil && !info.ModTime().Before(observedAt) {
		return nil
	}
	if err := os.Remove(lockMarkerPath(dst)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// readLocalImage reads a sidecar through the library root, so no path
// component can resolve outside it, and checks that the opened file is the
// regular file the Lstat saw rather than one swapped in afterwards.
func readLocalImage(libraryRoot *os.Root, rel, src string, lstat os.FileInfo) ([]byte, error) {
	file, err := libraryRoot.Open(rel)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(lstat, opened) {
		return nil, fmt.Errorf("local artwork %q changed while it was read", src)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLocalArtworkBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxLocalArtworkBytes {
		return nil, fmt.Errorf("local artwork %q has unsupported size %d", src, len(data))
	}
	if !isLocalImageFormat(data) {
		return nil, fmt.Errorf("local artwork %q is not a JPEG, PNG or WebP image", src)
	}
	return data, nil
}

// isLocalImageFormat accepts only the JPEG, PNG and WebP signatures.
func isLocalImageFormat(data []byte) bool {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return true
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return true
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return true
	}
	return false
}
