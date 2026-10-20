package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type managedMoveStrategy uint8

const (
	managedMoveInvalid managedMoveStrategy = iota
	managedMoveNoop
	managedMoveAtomicRename
	managedMoveHardlink
	managedMoveVerifiedCopy
)

type managedMove struct {
	strategy managedMoveStrategy
	caseOnly bool
}

func (s *Store) prepareManagedMove(source, target string) (managedMove, error) {
	if source == target {
		return managedMove{strategy: managedMoveNoop}, nil
	}
	info, err := os.Lstat(source)
	if err != nil {
		return managedMove{}, err
	}
	sourceCaps, err := s.capabilitiesForManagedMutation(source)
	if err != nil {
		return managedMove{}, err
	}
	targetCaps, err := s.capabilitiesForManagedMutation(target)
	if err != nil {
		return managedMove{}, err
	}
	return prepareManagedMoveForCapabilities(source, target, info, sourceCaps, targetCaps)
}

func prepareManagedMoveForCapabilities(
	source, target string,
	info os.FileInfo,
	sourceCaps, targetCaps MountCapabilities,
) (managedMove, error) {
	if sourceCaps.Mount != targetCaps.Mount {
		if info.Mode().IsRegular() &&
			targetCaps.AtomicRename == CapabilitySupported &&
			targetCaps.PermissionPreservation == CapabilitySupported {
			return managedMove{strategy: managedMoveVerifiedCopy}, nil
		}
		return managedMove{}, ValidationError(fmt.Sprintf(
			"mount %q cannot perform managed mutation to mount %q: missing capability %q",
			sourceCaps.Mount, targetCaps.Mount, "cross-mount atomic rename",
		))
	}

	caseOnly := sourceCaps.CaseSensitive == CapabilityUnsupported &&
		strings.EqualFold(filepath.Clean(source), filepath.Clean(target))
	if info.Mode().IsRegular() && !caseOnly && sourceCaps.Hardlinks == CapabilitySupported {
		return managedMove{strategy: managedMoveHardlink}, nil
	}
	if sourceCaps.AtomicRename != CapabilitySupported {
		return managedMove{}, missingMountCapability(sourceCaps, "atomic rename", sourceCaps.AtomicRename)
	}
	if sourceCaps.PermissionPreservation != CapabilitySupported {
		return managedMove{}, missingMountCapability(sourceCaps, "permission preservation", sourceCaps.PermissionPreservation)
	}
	return managedMove{strategy: managedMoveAtomicRename, caseOnly: caseOnly}, nil
}

func (s *Store) capabilitiesForManagedMutation(path string) (MountCapabilities, error) {
	probePath, err := existingMutationDirectory(path)
	if err != nil {
		return MountCapabilities{}, err
	}
	return s.mountCapabilityProbe.probe(probePath, false)
}

func existingMutationDirectory(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Stat(current)
		if err == nil && info.IsDir() {
			return current, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("managed mutation has no existing parent for %q", path)
		}
		current = parent
	}
}

func missingMountCapability(capabilities MountCapabilities, name string, status CapabilityStatus) error {
	return ValidationError(fmt.Sprintf(
		"mount %q cannot perform managed mutation: missing capability %q (probe reported %s)",
		capabilities.Mount, name, status,
	))
}

func executeManagedMove(move managedMove, source, target string) error {
	if move.strategy == managedMoveNoop {
		return nil
	}
	if move.strategy != managedMoveAtomicRename &&
		move.strategy != managedMoveHardlink &&
		move.strategy != managedMoveVerifiedCopy {
		return errors.New("managed mutation strategy was not prepared")
	}
	return executeManagedMoveWithRename(move, source, target, os.Rename)
}

func executeManagedMoveWithRename(
	move managedMove,
	source, target string,
	rename func(string, string) error,
) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		sourceInfo, sourceErr := os.Lstat(source)
		if !move.caseOnly || sourceErr != nil || !os.SameFile(sourceInfo, info) {
			return errors.New("managed mutation target already exists: " + target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if move.strategy == managedMoveAtomicRename {
		if move.caseOnly {
			return executeCaseOnlyRename(source, target, rename)
		}
		return rename(source, target)
	}
	if move.strategy == managedMoveVerifiedCopy {
		return executeVerifiedCopyMove(source, target, rename)
	}
	if err := os.Link(source, target); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

func executeCaseOnlyRename(source, target string, rename func(string, string) error) error {
	token, err := newManagedTrashID()
	if err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(source), ".beamfall-case-rename-"+token)
	if err := rename(source, temporary); err != nil {
		return err
	}
	if err := rename(temporary, target); err != nil {
		return errors.Join(err, rename(temporary, source))
	}
	return nil
}

func executeVerifiedCopyMove(source, target string, rename func(string, string) error) error {
	//nolint:gosec // source comes from a prepared managed move whose production callers validate or derive it within managed Library/trash roots.
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	sourceInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".move-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := io.Copy(temporary, sourceFile); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(sourceInfo.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	sourceSize, sourceDigest, err := managedMoveDigest(sourceFile)
	if err != nil {
		return err
	}
	if err := sourceFile.Close(); err != nil {
		return err
	}
	targetSize, targetDigest, err := managedMoveDigestPath(temporaryPath)
	if err != nil {
		return err
	}
	if sourceSize != targetSize || sourceDigest != targetDigest {
		return errors.New("managed mutation copy verification failed")
	}
	if _, err := os.Lstat(target); err == nil {
		return errors.New("managed mutation target already exists: " + target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := rename(temporaryPath, target); err != nil {
		return err
	}
	published = true
	if err := os.Remove(source); err != nil {
		return errors.Join(err, os.Remove(target))
	}
	return nil
}

func managedMoveDigest(file *os.File) (int64, [sha256.Size]byte, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, [sha256.Size]byte{}, err
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, [sha256.Size]byte{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return size, digest, nil
}

func managedMoveDigestPath(path string) (int64, [sha256.Size]byte, error) {
	//nolint:gosec // path is the server-created temporaryPath returned by os.CreateTemp in executeVerifiedCopyMove.
	file, err := os.Open(path)
	if err != nil {
		return 0, [sha256.Size]byte{}, err
	}
	defer file.Close()
	return managedMoveDigest(file)
}
