package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxReleaseBinaryBytes = 512 << 20 // 512 MiB upper bound on extracted server executable

// VerifyDistManifest verifies the signed release manifest and every artifact in
// dir against the binary's pinned ed25519 trust anchor (ADR-0013 §A, ADR-0243).
func VerifyDistManifest(dir string) (Manifest, error) {
	return VerifyDistWithKey(dir, nil)
}

// VerifyDistWithKey verifies dir using pub, defaulting fail-closed to
// PinnedKey when pub is nil. Tests use a non-nil pub for synthetic dists.
func VerifyDistWithKey(dir string, pub ed25519.PublicKey) (Manifest, error) {
	if pub == nil {
		pinned, ok := PinnedKey()
		if !ok {
			return Manifest{}, errNoPinnedKey
		}
		pub = pinned
	}
	if err := verifyDist(dir, func(m Manifest) error {
		return Verify(pub, m)
	}); err != nil {
		return Manifest{}, err
	}
	return LoadManifest(filepath.Join(filepath.Clean(dir), ManifestFile))
}

// SelectBinaryArtifact resolves the server binary entry in m for goos/goarch.
// It checks platform-qualified or bare `beamfall` executables as well as the
// official `script/release.sh` archive `beamfall-core_<version>_<channel>_<os>_<arch>.tar.gz`,
// and rejects non-executable metadata such as SBOMs or source manifests.
func SelectBinaryArtifact(m Manifest, goos, goarch string) (Artifact, error) {
	goos = strings.TrimSpace(goos)
	goarch = strings.TrimSpace(goarch)
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	candidates := []string{
		fmt.Sprintf("beamfall-%s-%s%s", goos, goarch, ext),
		fmt.Sprintf("beamfall_%s_%s%s", goos, goarch, ext),
		"beamfall" + ext,
	}
	if m.Version != "" && m.Channel != "" {
		candidates = append(candidates, fmt.Sprintf("beamfall-core_%s_%s_%s_%s.tar.gz", m.Version, m.Channel, goos, goarch))
	}
	byName := make(map[string]Artifact, len(m.Artifacts))
	for _, art := range m.Artifacts {
		if prev, dup := byName[art.Name]; dup && prev.SHA256 != art.SHA256 {
			return Artifact{}, fmt.Errorf("release: conflicting digests for artifact %q in manifest v%s", art.Name, m.Version)
		}
		byName[art.Name] = art
	}
	for _, cand := range candidates {
		if art, ok := byName[cand]; ok {
			return art, nil
		}
	}
	if m.Version != "" && goos != "" && goarch != "" {
		prefix := fmt.Sprintf("beamfall-core_%s_", m.Version)
		suffix := fmt.Sprintf("_%s_%s.tar.gz", goos, goarch)
		var matched []Artifact
		for _, art := range m.Artifacts {
			if strings.HasPrefix(art.Name, prefix) && strings.HasSuffix(art.Name, suffix) {
				matched = append(matched, art)
			}
		}
		if len(matched) == 1 {
			return matched[0], nil
		}
		if len(matched) > 1 {
			return Artifact{}, fmt.Errorf("release: ambiguous release archive artifacts for %s/%s in manifest v%s", goos, goarch, m.Version)
		}
	}
	return Artifact{}, fmt.Errorf("release: manifest v%s has no binary or release archive artifact for %s/%s", m.Version, goos, goarch)
}

// ValidateStagedArtifact checks that artifactPath is a non-empty executable
// or, for `.tar.gz` official release archives, contains a unique valid
// `beamfall` executable entry for goos without path traversal.
func ValidateStagedArtifact(artifactPath, goos string) error {
	info, err := os.Lstat(artifactPath)
	if err != nil {
		return fmt.Errorf("release: inspect staged artifact %s: %w", artifactPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("release: staged artifact %s must be a non-empty regular file", artifactPath)
	}
	if !strings.HasSuffix(artifactPath, ".tar.gz") {
		return nil
	}
	_, err = extractServerBinaryFromTarGz(artifactPath, goos, "")
	return err
}

// MaterializeStagedBinary prepares an executable regular file from artifactPath
// ready to be passed to ApplyBinarySwap. For official `.tar.gz` release
// archives it extracts `<dir>/beamfall(.exe)` into a private temporary file in
// workDir and returns a cleanup callback; for bare executables it returns
// artifactPath directly with a no-op cleanup.
func MaterializeStagedBinary(artifactPath, goos, workDir string) (string, func(), error) {
	noop := func() {}
	if err := ValidateStagedArtifact(artifactPath, goos); err != nil {
		return "", noop, err
	}
	if !strings.HasSuffix(artifactPath, ".tar.gz") {
		return artifactPath, noop, nil
	}
	if strings.TrimSpace(workDir) == "" {
		return "", noop, errors.New("release: extraction work directory is required")
	}
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		return "", noop, fmt.Errorf("release: create extraction directory: %w", err)
	}
	outPath, err := extractServerBinaryFromTarGz(artifactPath, goos, workDir)
	if err != nil {
		return "", noop, err
	}
	return outPath, func() { _ = os.Remove(outPath) }, nil
}

func extractServerBinaryFromTarGz(archivePath, goos, destDir string) (string, error) {
	// #nosec G304 -- archivePath is inside the verified <dataDir>/update-staged/payload tree.
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("release: open staged archive %s: %w", archivePath, err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("release: open gzip archive %s: %w", archivePath, err)
	}
	defer func() { _ = gz.Close() }()

	wantBase := "beamfall"
	if strings.TrimSpace(goos) == "windows" {
		wantBase = "beamfall.exe"
	}

	tr := tar.NewReader(gz)
	found := false
	var extractedPath string
	cleanupExtracted := false
	defer func() {
		if cleanupExtracted && extractedPath != "" {
			_ = os.Remove(extractedPath)
		}
	}()

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("release: read tar archive %s: %w", archivePath, err)
		}
		clean := path.Clean(strings.TrimPrefix(strings.TrimSpace(hdr.Name), "./"))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") {
			return "", fmt.Errorf("release: unsafe tar entry path %q", hdr.Name)
		}
		parts := strings.Split(clean, "/")
		if len(parts) > 2 || parts[len(parts)-1] != wantBase {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("release: archive entry %q must be a regular executable file", hdr.Name)
		}
		if hdr.Size <= 0 || hdr.Size > maxReleaseBinaryBytes {
			return "", fmt.Errorf("release: archive binary %q has invalid size %d", hdr.Name, hdr.Size)
		}
		if found {
			return "", fmt.Errorf("release: archive contains multiple %s binary entries", wantBase)
		}
		found = true

		if destDir == "" {
			// Verify size/readability without writing a file.
			n, copyErr := io.Copy(io.Discard, io.LimitReader(tr, maxReleaseBinaryBytes+1))
			if copyErr != nil {
				return "", fmt.Errorf("release: read archived binary %q: %w", hdr.Name, copyErr)
			}
			if n != hdr.Size {
				return "", fmt.Errorf("release: archived binary %q size mismatch (%d != %d)", hdr.Name, n, hdr.Size)
			}
			continue
		}

		tmp, err := os.CreateTemp(destDir, ".beamfall-extracted-*")
		if err != nil {
			return "", fmt.Errorf("release: create extracted binary temp file: %w", err)
		}
		extractedPath = tmp.Name()
		cleanupExtracted = true
		n, copyErr := io.Copy(tmp, io.LimitReader(tr, maxReleaseBinaryBytes+1))
		chmodErr := tmp.Chmod(0o755)
		syncErr := tmp.Sync()
		closeErr := tmp.Close()
		if copyErr != nil || chmodErr != nil || syncErr != nil || closeErr != nil {
			return "", fmt.Errorf("release: extract archived binary %q: %w", hdr.Name, errors.Join(copyErr, chmodErr, syncErr, closeErr))
		}
		if n != hdr.Size {
			return "", fmt.Errorf("release: extracted binary %q size mismatch (%d != %d)", hdr.Name, n, hdr.Size)
		}
	}
	if !found {
		return "", fmt.Errorf("release: archive %s does not contain %s executable", filepath.Base(archivePath), wantBase)
	}
	cleanupExtracted = false
	return extractedPath, nil
}

// ApplyBinarySwap atomically replaces targetBinaryPath with stagedBinaryPath,
// preserving any prior target binary at <targetBinaryPath>.prev before swap.
func ApplyBinarySwap(stagedBinaryPath, targetBinaryPath string) (string, error) {
	stagedBinaryPath = filepath.Clean(strings.TrimSpace(stagedBinaryPath))
	targetBinaryPath = filepath.Clean(strings.TrimSpace(targetBinaryPath))
	if stagedBinaryPath == "" || stagedBinaryPath == "." {
		return "", errors.New("release: staged binary path is required")
	}
	if targetBinaryPath == "" || targetBinaryPath == "." {
		return "", errors.New("release: target binary path is required")
	}
	info, err := os.Stat(stagedBinaryPath)
	if err != nil {
		return "", fmt.Errorf("release: staged binary %s: %w", stagedBinaryPath, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("release: staged binary %s must be a non-empty regular file", stagedBinaryPath)
	}
	targetDir := filepath.Dir(targetBinaryPath)
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		return "", fmt.Errorf("release: create binary directory: %w", err)
	}

	tmp, err := os.CreateTemp(targetDir, ".beamfall-upgrade-*")
	if err != nil {
		return "", fmt.Errorf("release: stage temp binary: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	src, err := os.Open(stagedBinaryPath)
	if err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("release: open staged binary: %w", err)
	}
	_, copyErr := io.Copy(tmp, src)
	closeSrcErr := src.Close()
	chmodErr := tmp.Chmod(0o755)
	syncErr := tmp.Sync()
	closeTmpErr := tmp.Close()
	if copyErr != nil || closeSrcErr != nil || chmodErr != nil || syncErr != nil || closeTmpErr != nil {
		return "", fmt.Errorf("release: write temp binary: %w", errors.Join(copyErr, closeSrcErr, chmodErr, syncErr, closeTmpErr))
	}

	rollbackPath, err := preserveRollbackBinary(targetBinaryPath)
	if err != nil {
		return "", err
	}

	// Atomically replace targetBinaryPath while targetBinaryPath stays valid on disk.
	if err := os.Rename(tmpPath, targetBinaryPath); err != nil {
		return "", fmt.Errorf("release: replace binary: %w", err)
	}
	cleanupTmp = false
	// #nosec G304 -- targetDir is filepath.Dir(targetBinaryPath) of the operator-supplied or executable swap target.
	if dirHandle, err := os.Open(targetDir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return rollbackPath, nil
}

func preserveRollbackBinary(targetBinaryPath string) (string, error) {
	if _, err := os.Stat(targetBinaryPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("release: inspect target binary %s: %w", targetBinaryPath, err)
	}
	targetDir := filepath.Dir(targetBinaryPath)
	rollbackPath := targetBinaryPath + ".prev"

	// #nosec G304 -- targetBinaryPath is the operator-supplied or executable swap target.
	cur, err := os.Open(targetBinaryPath)
	if err != nil {
		return "", fmt.Errorf("release: open previous binary: %w", err)
	}
	prevTmp, err := os.CreateTemp(targetDir, ".beamfall-prev-*")
	if err != nil {
		_ = cur.Close()
		return "", fmt.Errorf("release: create previous binary temp file: %w", err)
	}
	prevTmpPath := prevTmp.Name()
	cleanupPrev := true
	defer func() {
		if cleanupPrev {
			_ = os.Remove(prevTmpPath)
		}
	}()

	_, copyErr := io.Copy(prevTmp, cur)
	closeCurErr := cur.Close()
	chmodErr := prevTmp.Chmod(0o755)
	syncErr := prevTmp.Sync()
	closePrevErr := prevTmp.Close()
	if copyErr != nil || closeCurErr != nil || chmodErr != nil || syncErr != nil || closePrevErr != nil {
		return "", fmt.Errorf("release: copy previous binary: %w", errors.Join(copyErr, closeCurErr, chmodErr, syncErr, closePrevErr))
	}
	if err := os.Rename(prevTmpPath, rollbackPath); err != nil {
		return "", fmt.Errorf("release: preserve previous binary: %w", err)
	}
	cleanupPrev = false
	return rollbackPath, nil
}
