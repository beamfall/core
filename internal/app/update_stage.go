package app

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/release"
)

const (
	stagedUpdateDirName  = "update-staged"
	stagedUpdateFileName = "staged.json"
	stagedUpdatePayload  = "payload"
	stagedUpdateLockName = "update-staged.lock"
)

var (
	// ErrUpdateAlreadyStaged reports that an unapplied update is already staged.
	ErrUpdateAlreadyStaged = errors.New("an update is already staged; cancel or apply it before staging another")
	// ErrNoStagedUpdate reports that no active staged update exists.
	ErrNoStagedUpdate = errors.New("no staged update is present")

	stageLocksMu sync.Mutex
	stageLocks   = map[string]*sync.Mutex{}
)

// StagedUpdateMarker is the durable marker under <dataDir>/update-staged/staged.json
// describing a signature-verified, gate-checked staged server update (ADR-0243 D1).
type StagedUpdateMarker struct {
	Status              string     `json:"status"` // "staged", "applying", or "applied"
	StagedAt            time.Time  `json:"stagedAt"`
	TargetVersion       string     `json:"targetVersion"`
	Channel             string     `json:"channel"`
	Commit              string     `json:"commit,omitempty"`
	ArtifactName        string     `json:"artifactName"`
	ArtifactSHA256      string     `json:"artifactSHA256"`
	AppliedBinarySHA256 string     `json:"appliedBinarySHA256,omitempty"`
	SourceDistDir       string     `json:"sourceDistDir"`
	PayloadDir          string     `json:"payloadDir"`
	BypassAuthorized    bool       `json:"bypassAuthorized,omitempty"`
	AppliedAt           *time.Time `json:"appliedAt,omitempty"`
	TargetBinaryPath    string     `json:"targetBinaryPath,omitempty"`
	RollbackBinary      string     `json:"rollbackBinary,omitempty"`
}

func lockUpdateStage(dataDir string) (func(), error) {
	cleanDataDir := filepath.Clean(strings.TrimSpace(dataDir))
	if cleanDataDir == "" || cleanDataDir == "." {
		return nil, errors.New("data directory is required")
	}
	if err := os.MkdirAll(cleanDataDir, 0o700); err != nil {
		return nil, err
	}
	key := cleanDataDir
	if resolved, err := filepath.EvalSymlinks(cleanDataDir); err == nil {
		key = resolved
	}
	stageLocksMu.Lock()
	mu := stageLocks[key]
	if mu == nil {
		mu = &sync.Mutex{}
		stageLocks[key] = mu
	}
	stageLocksMu.Unlock()

	mu.Lock()
	lockPath := filepath.Join(cleanDataDir, stagedUpdateLockName)
	// #nosec G304 -- lockPath is confined inside the operator-chosen data directory.
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	if err := lockUpdateStageFile(f); err != nil {
		_ = f.Close()
		mu.Unlock()
		return nil, err
	}
	return func() {
		_ = unlockUpdateStageFile(f)
		_ = f.Close()
		mu.Unlock()
	}, nil
}

func validateStagePathsSafe(dataDir string) error {
	root := stagedUpdateRoot(dataDir)
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("staged update directory %s must be a real directory, not a symlink", stagedUpdateDirName)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	payload := filepath.Join(root, stagedUpdatePayload)
	if info, err := os.Lstat(payload); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("staged payload directory %s must be a real directory, not a symlink", stagedUpdatePayload)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	markerPath := stagedUpdateMarkerPath(dataDir)
	if info, err := os.Lstat(markerPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("staged update marker %s must be a regular file", stagedUpdateFileName)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func stagedUpdateRoot(dataDir string) string {
	return filepath.Join(filepath.Clean(dataDir), stagedUpdateDirName)
}

func stagedUpdateMarkerPath(dataDir string) string {
	return filepath.Join(stagedUpdateRoot(dataDir), stagedUpdateFileName)
}

// LoadStagedUpdate reads the staged update marker from dataDir, reporting
// exists=false cleanly when none has been written.
func LoadStagedUpdate(dataDir string) (StagedUpdateMarker, bool, error) {
	if strings.TrimSpace(dataDir) == "" {
		return StagedUpdateMarker{}, false, errors.New("data directory is required")
	}
	if err := validateStagePathsSafe(dataDir); err != nil {
		return StagedUpdateMarker{}, false, err
	}
	return loadStagedUpdateUnlocked(dataDir)
}

func loadStagedUpdateUnlocked(dataDir string) (StagedUpdateMarker, bool, error) {
	raw, err := os.ReadFile(stagedUpdateMarkerPath(dataDir))
	if os.IsNotExist(err) {
		return StagedUpdateMarker{}, false, nil
	}
	if err != nil {
		return StagedUpdateMarker{}, false, err
	}
	var marker StagedUpdateMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return StagedUpdateMarker{}, false, fmt.Errorf("decode staged update marker: %w", err)
	}
	return marker, true, nil
}

// StageUpdate verifies a local signed release distribution directory with pub
// (defaulting fail-closed to release.PinnedKey when pub is nil), enforces the
// ADR-0192 backup update gate, and copies the verified payload into dataDir.
func StageUpdate(dataDir, distDir string, pub ed25519.PublicKey, now time.Time) (StagedUpdateMarker, error) {
	if strings.TrimSpace(dataDir) == "" {
		return StagedUpdateMarker{}, errors.New("data directory is required")
	}
	distDir = filepath.Clean(strings.TrimSpace(distDir))
	if distDir == "" || distDir == "." {
		return StagedUpdateMarker{}, errors.New("distribution directory is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	unlock, err := lockUpdateStage(dataDir)
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	defer unlock()

	if err := validateStagePathsSafe(dataDir); err != nil {
		return StagedUpdateMarker{}, err
	}
	existing, ok, err := loadStagedUpdateUnlocked(dataDir)
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	if ok && (existing.Status == "staged" || existing.Status == "applying") {
		return StagedUpdateMarker{}, ErrUpdateAlreadyStaged
	}

	manifest, err := release.VerifyDistWithKey(distDir, pub)
	if err != nil {
		return StagedUpdateMarker{}, fmt.Errorf("verify distribution: %w", err)
	}
	artifact, err := release.SelectBinaryArtifact(manifest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return StagedUpdateMarker{}, err
	}

	backupStatus, err := backupcore.LoadStatus(dataDir)
	if err != nil {
		return StagedUpdateMarker{}, fmt.Errorf("load backup status: %w", err)
	}
	if err := backupStatus.CanStageUpdate(manifest.Version); err != nil {
		return StagedUpdateMarker{}, err
	}
	bypassed := !backupStatus.UpdateGateClear() && backupStatus.HasMatchingBypass(manifest.Version)

	root := stagedUpdateRoot(dataDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return StagedUpdateMarker{}, fmt.Errorf("create staged update directory: %w", err)
	}
	if err := validateStagePathsSafe(dataDir); err != nil {
		return StagedUpdateMarker{}, err
	}
	payloadDir := filepath.Join(root, stagedUpdatePayload)
	if err := os.RemoveAll(payloadDir); err != nil {
		return StagedUpdateMarker{}, fmt.Errorf("reset staged payload: %w", err)
	}
	if err := os.MkdirAll(payloadDir, 0o700); err != nil {
		return StagedUpdateMarker{}, fmt.Errorf("create staged payload directory: %w", err)
	}
	cleanupPayload := true
	defer func() {
		if cleanupPayload && validateStagePathsSafe(dataDir) == nil {
			_ = os.RemoveAll(payloadDir)
		}
	}()

	copied := map[string]bool{}
	for _, entry := range []struct {
		name string
		mode os.FileMode
	}{
		{release.ManifestFile, 0o600},
		{release.SumsFile, 0o600},
	} {
		if err := copyVerifiedFile(filepath.Join(distDir, entry.name), filepath.Join(payloadDir, entry.name), entry.mode); err != nil {
			return StagedUpdateMarker{}, fmt.Errorf("stage %s: %w", entry.name, err)
		}
		copied[entry.name] = true
	}
	for _, mArt := range manifest.Artifacts {
		base := filepath.Base(mArt.Name)
		if mArt.Name == "" || base != mArt.Name || base == "." || base == ".." || strings.ContainsAny(mArt.Name, `/\`) {
			return StagedUpdateMarker{}, fmt.Errorf("stage artifact %q: unsafe relative path", mArt.Name)
		}
		if copied[base] {
			continue
		}
		mode := os.FileMode(0o600)
		if base == artifact.Name && !strings.HasSuffix(base, ".tar.gz") {
			mode = 0o755
		}
		if err := copyVerifiedFile(filepath.Join(distDir, base), filepath.Join(payloadDir, base), mode); err != nil {
			return StagedUpdateMarker{}, fmt.Errorf("stage %s: %w", base, err)
		}
		copied[base] = true
	}
	if _, err := release.VerifyDistWithKey(payloadDir, pub); err != nil {
		return StagedUpdateMarker{}, fmt.Errorf("re-verify staged payload: %w", err)
	}
	stagedArtPath := filepath.Join(payloadDir, artifact.Name)
	gotSHA, err := fileSHA256Hex(stagedArtPath)
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	if gotSHA != artifact.SHA256 {
		return StagedUpdateMarker{}, fmt.Errorf("staged artifact %s digest mismatch", artifact.Name)
	}
	if err := release.ValidateStagedArtifact(stagedArtPath, runtime.GOOS); err != nil {
		return StagedUpdateMarker{}, err
	}

	marker := StagedUpdateMarker{
		Status:           "staged",
		StagedAt:         now,
		TargetVersion:    manifest.Version,
		Channel:          manifest.Channel,
		Commit:           manifest.Commit,
		ArtifactName:     artifact.Name,
		ArtifactSHA256:   artifact.SHA256,
		SourceDistDir:    distDir,
		PayloadDir:       payloadDir,
		BypassAuthorized: bypassed,
	}
	if err := writeStagedUpdateMarker(dataDir, marker); err != nil {
		return StagedUpdateMarker{}, err
	}
	cleanupPayload = false
	return marker, nil
}

// CancelStagedUpdate removes an active "staged" update marker and payload.
func CancelStagedUpdate(dataDir string) (bool, error) {
	if strings.TrimSpace(dataDir) == "" {
		return false, errors.New("data directory is required")
	}
	unlock, err := lockUpdateStage(dataDir)
	if err != nil {
		return false, err
	}
	defer unlock()

	if err := validateStagePathsSafe(dataDir); err != nil {
		return false, err
	}
	marker, ok, err := loadStagedUpdateUnlocked(dataDir)
	if err != nil {
		return false, err
	}
	if !ok || marker.Status != "staged" {
		return false, nil
	}
	if err := os.Remove(stagedUpdateMarkerPath(dataDir)); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err := os.RemoveAll(filepath.Join(stagedUpdateRoot(dataDir), stagedUpdatePayload)); err != nil {
		return false, err
	}
	return true, nil
}

// ApplyStagedUpdate re-verifies the staged payload and backup update gate, swaps
// targetBinary atomically with a .prev rollback copy, and marks staged.json applied.
// It serializes mutations per dataDir, rejects symlinked staging paths, and reconciles
// interrupted post-swap marker retries without overwriting the original .prev backup.
func ApplyStagedUpdate(_ context.Context, dataDir, targetBinary string, allowUnverified bool, pub ed25519.PublicKey, now time.Time) (StagedUpdateMarker, error) {
	if strings.TrimSpace(dataDir) == "" {
		return StagedUpdateMarker{}, errors.New("data directory is required")
	}
	targetBinary = filepath.Clean(strings.TrimSpace(targetBinary))
	if targetBinary == "" || targetBinary == "." {
		return StagedUpdateMarker{}, errors.New("target binary path is required")
	}
	absTarget, err := filepath.Abs(targetBinary)
	if err != nil {
		return StagedUpdateMarker{}, fmt.Errorf("resolve target binary path: %w", err)
	}
	canonTarget, err := canonicalTargetBinaryPath(absTarget)
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	targetBinary = absTarget
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	unlock, err := lockUpdateStage(dataDir)
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	defer unlock()

	if err := validateStagePathsSafe(dataDir); err != nil {
		return StagedUpdateMarker{}, err
	}
	marker, ok, err := loadStagedUpdateUnlocked(dataDir)
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	if !ok || (marker.Status != "staged" && marker.Status != "applying") {
		return StagedUpdateMarker{}, ErrNoStagedUpdate
	}

	payloadDir := filepath.Join(stagedUpdateRoot(dataDir), stagedUpdatePayload)
	manifest, err := release.VerifyDistWithKey(payloadDir, pub)
	if err != nil {
		return StagedUpdateMarker{}, fmt.Errorf("verify staged payload: %w", err)
	}
	if manifest.Version != marker.TargetVersion {
		return StagedUpdateMarker{}, fmt.Errorf("staged manifest version %q differs from marker version %q", manifest.Version, marker.TargetVersion)
	}
	selectedArt, err := release.SelectBinaryArtifact(manifest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return StagedUpdateMarker{}, fmt.Errorf("select staged server binary: %w", err)
	}
	if marker.ArtifactName != selectedArt.Name {
		return StagedUpdateMarker{}, fmt.Errorf("staged marker artifact %q does not match platform binary %q", marker.ArtifactName, selectedArt.Name)
	}
	if marker.ArtifactSHA256 != selectedArt.SHA256 {
		return StagedUpdateMarker{}, fmt.Errorf("staged marker digest does not match platform binary %s", selectedArt.Name)
	}
	if err := release.VerifyArtifact(manifest, selectedArt.Name, marker.ArtifactSHA256); err != nil {
		return StagedUpdateMarker{}, err
	}
	artBase := filepath.Base(marker.ArtifactName)
	if marker.ArtifactName == "" || artBase != marker.ArtifactName || strings.ContainsAny(marker.ArtifactName, `/\`) {
		return StagedUpdateMarker{}, fmt.Errorf("invalid staged artifact name %q", marker.ArtifactName)
	}
	stagedArt := filepath.Join(payloadDir, artBase)
	artInfo, err := os.Lstat(stagedArt)
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	if artInfo.Mode()&os.ModeSymlink != 0 || !artInfo.Mode().IsRegular() {
		return StagedUpdateMarker{}, fmt.Errorf("staged artifact %s must be a regular non-symlink file", artBase)
	}
	gotSHA, err := fileSHA256Hex(stagedArt)
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	if gotSHA != marker.ArtifactSHA256 {
		return StagedUpdateMarker{}, fmt.Errorf("staged binary %s digest mismatch", marker.ArtifactName)
	}

	execPath, cleanupExec, err := release.MaterializeStagedBinary(stagedArt, runtime.GOOS, filepath.Dir(targetBinary))
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	defer cleanupExec()

	execSHA, err := fileSHA256Hex(execPath)
	if err != nil {
		return StagedUpdateMarker{}, err
	}

	// Reconcile an interrupted apply whose binary swap already committed before
	// final applied-marker persistence succeeded, preserving original .prev.
	if marker.Status == "applying" && sameTargetBinaryPath(marker.TargetBinaryPath, canonTarget) && marker.AppliedBinarySHA256 == execSHA {
		if curSHA, shaErr := fileSHA256Hex(targetBinary); shaErr == nil && curSHA == execSHA {
			if marker.RollbackBinary == "" {
				if _, prevErr := os.Stat(targetBinary + ".prev"); prevErr == nil {
					marker.RollbackBinary = targetBinary + ".prev"
				}
			}
			applied := now
			marker.Status = "applied"
			marker.AppliedAt = &applied
			if err := writeStagedUpdateMarker(dataDir, marker); err != nil {
				return StagedUpdateMarker{}, err
			}
			return marker, nil
		}
	}

	if !allowUnverified {
		st, err := backupcore.LoadStatus(dataDir)
		if err != nil {
			return StagedUpdateMarker{}, fmt.Errorf("load backup status: %w", err)
		}
		if err := st.CanStageUpdate(marker.TargetVersion); err != nil {
			return StagedUpdateMarker{}, err
		}
	}

	// Record the applying intent and expected target digest before modifying
	// targetBinary so both marker writability failures and post-swap retry keep
	// the original .prev rollback executable intact.
	marker.Status = "applying"
	marker.TargetBinaryPath = targetBinary
	marker.AppliedBinarySHA256 = execSHA
	if _, statErr := os.Stat(targetBinary); statErr == nil {
		marker.RollbackBinary = targetBinary + ".prev"
	} else {
		marker.RollbackBinary = ""
	}
	if err := writeStagedUpdateMarker(dataDir, marker); err != nil {
		return StagedUpdateMarker{}, err
	}

	rollbackPath, err := release.ApplyBinarySwap(execPath, targetBinary)
	if err != nil {
		return StagedUpdateMarker{}, err
	}
	applied := now
	marker.Status = "applied"
	marker.AppliedAt = &applied
	marker.TargetBinaryPath = targetBinary
	marker.RollbackBinary = rollbackPath
	if err := writeStagedUpdateMarker(dataDir, marker); err != nil {
		return StagedUpdateMarker{}, err
	}
	return marker, nil
}

func writeStagedUpdateMarker(dataDir string, marker StagedUpdateMarker) error {
	if err := validateStagePathsSafe(dataDir); err != nil {
		return err
	}
	root := stagedUpdateRoot(dataDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := validateStagePathsSafe(dataDir); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(root, ".staged-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, stagedUpdateMarkerPath(dataDir)); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func canonicalTargetBinaryPath(targetBinary string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(targetBinary))
	if cleaned == "" || cleaned == "." {
		return "", errors.New("target binary path is required")
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve target binary path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return resolved, nil
	}
	dir := filepath.Dir(absPath)
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolvedDir, filepath.Base(absPath)), nil
	}
	return absPath, nil
}

func sameTargetBinaryPath(storedPath, canonTarget string) bool {
	if strings.TrimSpace(storedPath) == "" {
		return false
	}
	if storedCanon, err := canonicalTargetBinaryPath(storedPath); err == nil && storedCanon == canonTarget {
		return true
	}
	storedInfo, errA := os.Stat(storedPath)
	targetInfo, errB := os.Stat(canonTarget)
	return errA == nil && errB == nil && os.SameFile(storedInfo, targetInfo)
}

func copyVerifiedFile(src, dst string, mode os.FileMode) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 || !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("source %s must be a regular non-symlink file", filepath.Base(src))
	}
	if dstInfo, err := os.Lstat(dst); err == nil {
		if dstInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination %s must not be a symlink", filepath.Base(dst))
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// #nosec G304 -- src and dst are non-symlink regular paths confined to distDir and <dataDir>/update-staged/payload.
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	// #nosec G304 -- dst sits under <dataDir>/update-staged/payload with a base-validated filename.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func fileSHA256Hex(path string) (string, error) {
	// #nosec G304 -- path is the verified staged payload artifact inside <dataDir>/update-staged/payload.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
