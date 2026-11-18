package artwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/beamfall/core/internal/store"
)

// AdoptEntityArtwork copies the artwork of an Entity being merged into
// another to the survivor, slot by slot, wherever the survivor has none of
// its own: a slot it lacks, or a poster it only inherited while the merged
// Entity's is its own. Each copy carries the slot's lock and inherited marks.
// The merged Entity keeps its files, so undoing the merge restores it whole,
// and the survivor's own artwork is never replaced. A survivor in a Sealed or
// Encrypted Library gets nothing, as with every Entity artwork write; a
// survivor whose Library cannot be read fails the adoption, and with it the
// merge, rather than reading as protected and leaving the artwork behind.
func (s *Service) AdoptEntityArtwork(ctx context.Context, survivor, merged store.EntityID) error {
	if survivor == merged {
		return nil
	}
	if protected, err := s.adoptionProtected(ctx, survivor); err != nil || protected {
		return err
	}
	s.entityPosterMu.Lock()
	defer s.entityPosterMu.Unlock()
	unlock, err := s.lockDerivative(ctx, filepath.Dir(s.EntityPosterPath(survivor)))
	if err != nil {
		return err
	}
	defer unlock()
	for _, slot := range KnownSlots {
		src, err := adoptedSlotPath(s.EntityArtworkPath(merged, slot))
		if err != nil {
			return err
		}
		own, err := adoptedSlotPath(s.EntityArtworkPath(survivor, slot))
		if err != nil {
			return err
		}
		adopt, err := adoptsSlot(src, own, slot)
		if err != nil {
			return err
		}
		if !adopt {
			continue
		}
		if err := s.copyAdoptedSlot(src, filepath.Join(filepath.Dir(own), filepath.Base(src)), slot); err != nil {
			return err
		}
	}
	return nil
}

// adoptionProtected is entityProtected for an adoption: a failed lookup of
// the survivor is an error, not a protected Library.
func (s *Service) adoptionProtected(ctx context.Context, survivor store.EntityID) (bool, error) {
	if s.sealed == nil && s.encrypted == nil {
		return false, nil
	}
	entity, err := s.store.Entity(ctx, survivor, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		return false, fmt.Errorf("read survivor %s: %w", survivor, err)
	}
	return (s.sealed != nil && s.sealed(entity.LibraryID)) || (s.encrypted != nil && s.encrypted(entity.LibraryID)), nil
}

// adoptsSlot reports whether the merged Entity's slot file src goes to the
// survivor, whose own slot file is own: src is a non-empty file and own is
// missing, or own is a poster the survivor only inherited while src is not.
// A file that cannot be inspected fails the adoption, and with it the merge,
// rather than reading as absent and leaving the artwork on the trashed
// Entity.
func adoptsSlot(src, own, slot string) (bool, error) {
	srcInfo, err := statAdopted(src)
	if err != nil || srcInfo == nil || !srcInfo.Mode().IsRegular() || srcInfo.Size() == 0 {
		return false, err
	}
	ownInfo, err := statAdopted(own)
	if err != nil {
		return false, err
	}
	if ownInfo == nil || !ownInfo.Mode().IsRegular() {
		return true, nil
	}
	if slot != SlotPoster {
		return false, nil
	}
	for _, mark := range []struct {
		path string
		want bool
	}{{inheritedMarkerPath(own), true}, {inheritedMarkerPath(src), false}} {
		info, err := statAdopted(mark.path)
		if err != nil {
			return false, err
		}
		if (info != nil && info.Mode().IsRegular()) != mark.want {
			return false, nil
		}
	}
	return true, nil
}

// adoptedSlotPath is storedSlotPath for an adoption: the file that holds a
// slot, path or its legacy overlay JPEG, except that a file which cannot be
// inspected is an error rather than read as absent, so a legacy JPEG behind
// an unreadable path is not passed over for a PNG that does not exist.
func adoptedSlotPath(path string) (string, error) {
	legacy := legacyOverlayPath(path)
	if legacy == "" {
		return path, nil
	}
	for _, candidate := range []string{path, legacy} {
		info, err := statAdopted(candidate)
		if err != nil {
			return "", err
		}
		if info != nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return path, nil
}

// statAdopted stats path; a missing file is a nil FileInfo, not an error.
func statAdopted(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return info, err
}

// copyAdoptedSlot copies one slot file and its marks to dst. A mark src lacks
// is removed from dst, and dst's size buckets of the slot are dropped so they
// are made again from the copy.
//
// The steps run in an order an interrupted copy recovers from, since the
// merge it serves has not committed and is retried, adopting again: the lock
// and generated marks and the buckets settle before the image is published,
// and a survivor's inherited mark is removed last, so until the copy is
// complete the slot still reads as one the survivor lacks or only inherited.
func (s *Service) copyAdoptedSlot(src, dst, slot string) error {
	// #nosec G304 -- src is a slot path this service built under its artwork root.
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	defer s.versions.forget(dst)
	inherited, err := copyAdoptedMark(inheritedMarkerPath(src), inheritedMarkerPath(dst), false)
	if err != nil {
		return err
	}
	for _, mark := range []func(string) string{lockMarkerPath, generatedMarkerPath} {
		if _, err := copyAdoptedMark(mark(src), mark(dst), true); err != nil {
			return err
		}
	}
	for bucket := range bucketWidths {
		_ = os.Remove(filepath.Join(filepath.Dir(dst), slot+"."+bucket+filepath.Ext(dst)))
	}
	if err := writeAtomicFile(dst, data, nil); err != nil {
		return err
	}
	s.recordDominantColor(dst, slot)
	if !inherited {
		if err := os.Remove(inheritedMarkerPath(dst)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// copyAdoptedMark copies the mark at src to dst and reports whether src had
// one. When it has none, dst's is removed if remove is set.
func copyAdoptedMark(src, dst string, remove bool) (bool, error) {
	// #nosec G304 -- the mark sits beside a slot path this service built.
	body, err := os.ReadFile(src)
	switch {
	case err == nil:
		return true, writeAtomicFile(dst, body, nil)
	case !errors.Is(err, os.ErrNotExist):
		return false, err
	case remove:
		if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}
