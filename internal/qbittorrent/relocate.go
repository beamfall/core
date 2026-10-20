package qbittorrent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RelocateMode records how a payload landed in the Library (visible in status,
// ADR-0020 §2).
type RelocateMode string

const (
	// RelocateHardlink: same filesystem, instant, zero extra disk, seeding
	// undisturbed.
	RelocateHardlink RelocateMode = "hardlink"
	// RelocateCopy: cross-filesystem fallback; the seed copy is untouched.
	RelocateCopy RelocateMode = "copy"
)

// relocateFile links src into dst, preferring a hardlink and falling back to a
// copy across filesystems. It never moves or deletes src (§2). The destination
// directory is created if absent. If dst already points at src, relocation is a
// no-op so a partial prior import can retry; any other pre-existing destination
// remains an error so a silent clobber is impossible.
func relocateFile(src, dst string) (RelocateMode, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Lstat(dst); err == nil {
		srcInfo, srcErr := os.Stat(src)
		dstInfo, dstErr := os.Stat(dst)
		if srcErr == nil && dstErr == nil && os.SameFile(srcInfo, dstInfo) {
			return RelocateHardlink, nil
		}
		return "", fmt.Errorf("relocate: destination already exists: %s", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	// Try a hardlink first. The most common failure is EXDEV (cross-device),
	// which means a copy is required; any other error is real and surfaced.
	if err := os.Link(src, dst); err == nil {
		return RelocateHardlink, nil
	} else if !isCrossDevice(err) {
		return "", err
	}
	if err := copyFile(src, dst); err != nil {
		return "", err
	}
	return RelocateCopy, nil
}

// copyFile streams src to a temp file beside dst, then renames into place, so
// the scanner never observes a half-written file (same-filesystem rename is
// atomic). The temp file is removed on any failure.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	// Ensure the destination directory exists. relocateFile already creates it,
	// but copyFile must not depend on its single caller having done so.
	if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".partial"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			out.Close()
			os.Remove(tmp)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, dst); err != nil {
		return err
	}
	return nil
}
