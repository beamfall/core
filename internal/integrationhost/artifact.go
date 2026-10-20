package integrationhost

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/beamfall/core/internal/plugin"
)

const (
	maxWorkerArtifactBytes  = 512 << 20
	MaxPackageManifestBytes = 256 << 10
	manifestApprovalDomain  = "beamfall-integration-manifest-approval-v1\x00"
)

// Artifact is one installed Integration Worker Artifact: a native worker binary
// for a declared OS/architecture target. It is pure data; verification is a
// method, not a side effect of construction.
type Artifact struct {
	// PluginID owns this artifact (one worker per plugin).
	PluginID string
	// Version is the artifact's declared version (operator-facing, surfaced in
	// the Plugin Surface status).
	Version string
	// OS / Arch are the declared target (GOOS/GOARCH). A mismatch with the host
	// is refused at install — a worker for the wrong platform can never launch.
	OS   string
	Arch string
	// Path is the on-disk binary path inside the server's plugin store.
	Path string
	// VerifiedSHA256 binds every supervisor launch/restart to the exact object
	// installed after pin verification. A blank value is never launchable.
	VerifiedSHA256 string
	// Args and WritablePaths are server-owned launch configuration. Production
	// installs leave WritablePaths empty; tests may grant a private scratch root.
	Args          []string
	WritablePaths []string
}

// PinKind names how an Update Source pins an artifact for verification.
const (
	// PinHash pins by the artifact's exact SHA-256 (lowercase hex).
	PinHash = plugin.UpdatePinHash
	// PinSigningKey pins by an Ed25519 signing key id; the artifact ships with a
	// detached signature over its bytes.
	PinSigningKey = plugin.UpdatePinSigningKey
)

// Pin is the verification material for one artifact. Exactly one of Hash or
// (SigningKey + Signature) is used, selected by Kind. First-party plugins use a
// compiled-in trusted key; third-party plugins use an operator-pinned hash or
// signing key from the per-plugin Update Source (ADR-0058 — not a marketplace).
type Pin struct {
	Kind string
	// Hash is the expected lowercase-hex SHA-256 (Kind == PinHash).
	Hash string
	// SigningKey is the trusted Ed25519 public key (Kind == PinSigningKey).
	SigningKey ed25519.PublicKey
	// Signature is the detached Ed25519 signature over the artifact bytes
	// (Kind == PinSigningKey).
	Signature []byte
}

// ErrPlatformMismatch is returned when an artifact's declared target does not
// match the host. A cross-platform worker is never launchable.
var ErrPlatformMismatch = errors.New("integrationhost: artifact platform does not match host")

// ErrVerification is returned when an artifact fails hash or signature
// verification. Install is refused and the failure is audited by the caller.
var ErrVerification = errors.New("integrationhost: artifact verification failed")

// Verify checks the artifact's bytes against the pin BEFORE it is ever
// launchable, and confirms the artifact targets this host. Any mismatch returns
// an error wrapping ErrVerification (or ErrPlatformMismatch) so the install path
// can refuse and audit it. A nil/blank pin is rejected: an unpinned artifact is
// never trusted.
func (a Artifact) Verify(pin Pin) error {
	if err := a.verifyPlatform(); err != nil {
		return err
	}
	data, err := readStableArtifact(a.Path)
	if err != nil {
		return fmt.Errorf("integrationhost: read artifact: %w", err)
	}
	return verifyArtifactBytes(data, pin)
}

func verifyArtifactBytes(data []byte, pin Pin) error {
	switch pin.Kind {
	case PinHash:
		return verifyHash(data, pin.Hash)
	case PinSigningKey:
		return verifySignature(data, pin.SigningKey, pin.Signature)
	default:
		return fmt.Errorf("%w: artifact has no pin (kind %q)", ErrVerification, pin.Kind)
	}
}

// InstallVerifiedObject verifies one stable snapshot of source and installs
// exactly those bytes into a server-owned content-addressed object store. The
// returned Artifact never names the mutable update-source path. Existing
// objects are re-hashed before reuse, so every restart executes the verified
// object or fails closed.
func InstallVerifiedObject(root string, source Artifact, pin Pin) (Artifact, error) {
	if err := source.verifyPlatform(); err != nil {
		return Artifact{}, err
	}
	data, err := readStableArtifact(source.Path)
	if err != nil {
		return Artifact{}, fmt.Errorf("integrationhost: read artifact: %w", err)
	}
	if err := verifyArtifactBytes(data, pin); err != nil {
		return Artifact{}, err
	}
	return installVerifiedArtifactBytes(root, source, data)
}

// InstallVerifiedUpdateCandidate verifies the worker bytes under the configured
// Update Source pin and installs the exact package-manifest bytes beside it.
func InstallVerifiedUpdateCandidate(root string, source Artifact, manifestBytes []byte, pin Pin) (Artifact, error) {
	if len(manifestBytes) > MaxPackageManifestBytes {
		return Artifact{}, fmt.Errorf("integrationhost: package manifest exceeds %d-byte limit", MaxPackageManifestBytes)
	}
	if err := source.verifyPlatform(); err != nil {
		return Artifact{}, err
	}
	data, err := readStableArtifact(source.Path)
	if err != nil {
		return Artifact{}, fmt.Errorf("integrationhost: read artifact: %w", err)
	}
	if err := verifyArtifactBytes(data, pin); err != nil {
		return Artifact{}, err
	}
	installed, err := installVerifiedArtifactBytes(root, source, data)
	if err != nil {
		return Artifact{}, err
	}
	manifestPath := VerifiedUpdateManifestPath(installed.Path)
	if err := installImmutableObject(filepath.Dir(manifestPath), manifestPath, manifestBytes, 0o400); err != nil {
		return Artifact{}, err
	}
	return installed, nil
}

// VerifiedUpdateManifestPath returns the immutable package-manifest sidecar for
// an installed, pin-verified update candidate.
func VerifiedUpdateManifestPath(artifactPath string) string {
	return artifactPath + ".manifest.json"
}

func installVerifiedArtifactBytes(root string, source Artifact, data []byte) (Artifact, error) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	objectDir := filepath.Join(root, "sha256")
	if err := os.MkdirAll(objectDir, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("integrationhost: create artifact store: %w", err)
	}
	objectPath := filepath.Join(objectDir, digest)
	if err := installImmutableObject(objectDir, objectPath, data, 0o500); err != nil {
		return Artifact{}, err
	}
	installed := source
	installed.Path = objectPath
	installed.VerifiedSHA256 = digest
	return installed, nil
}

// ManifestApprovalBinding binds one permission approval to the exact verified
// artifact/manifest pair. The artifact digest identifies the immutable binary;
// manifestBytes are the bounded immutable sidecar installed with it.
func ManifestApprovalBinding(artifactSHA256 string, manifestBytes []byte) (string, error) {
	if !validArtifactDigest(artifactSHA256) {
		return "", fmt.Errorf("integrationhost: invalid artifact digest")
	}
	if len(manifestBytes) > MaxPackageManifestBytes {
		return "", fmt.Errorf("integrationhost: package manifest exceeds %d-byte limit", MaxPackageManifestBytes)
	}
	h := sha256.New()
	_, _ = io.WriteString(h, manifestApprovalDomain)
	_, _ = io.WriteString(h, artifactSHA256)
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(manifestBytes)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(manifestBytes)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadPackageManifest reads one stable regular package-manifest file under the
// explicit package-manifest byte ceiling.
func ReadPackageManifest(path string) ([]byte, error) {
	return readStableLimitedFile(path, MaxPackageManifestBytes, "package manifest")
}

// GrantApprovalBinding reads the artifact/manifest binding for one high-risk
// permission. Blank or missing binding files are treated as unapproved.
func GrantApprovalBinding(root, pluginID, kind string) (string, bool, error) {
	path, err := grantApprovalBindingPath(root, pluginID, kind)
	if err != nil {
		return "", false, err
	}
	// #nosec G304 -- path is constructed from hex-encoded identifiers under the server-owned artifact root.
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	binding := strings.TrimSpace(string(raw))
	if binding == "" {
		return "", false, nil
	}
	if !validArtifactDigest(binding) {
		return "", false, fmt.Errorf("integrationhost: invalid grant approval binding")
	}
	return binding, true, nil
}

// SetGrantApprovalBinding atomically replaces a binding or writes a blank
// revocation marker.
func SetGrantApprovalBinding(root, pluginID, kind, binding string) error {
	if binding != "" && !validArtifactDigest(binding) {
		return fmt.Errorf("integrationhost: invalid grant approval binding")
	}
	path, err := grantApprovalBindingPath(root, pluginID, kind)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".approval-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(binding); err != nil {
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
	return os.Rename(tmpPath, path)
}

func grantApprovalBindingPath(root, pluginID, kind string) (string, error) {
	if pluginID == "" || !IsKnownPermission(kind) {
		return "", fmt.Errorf("integrationhost: invalid grant approval identity")
	}
	return filepath.Join(root, "approvals", hex.EncodeToString([]byte(pluginID)), hex.EncodeToString([]byte(kind))), nil
}

func validArtifactDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func (a Artifact) verifyPlatform() error {
	if a.OS != "" && a.OS != runtime.GOOS {
		return fmt.Errorf("%w: artifact OS %q, host %q", ErrPlatformMismatch, a.OS, runtime.GOOS)
	}
	if a.Arch != "" && a.Arch != runtime.GOARCH {
		return fmt.Errorf("%w: artifact arch %q, host %q", ErrPlatformMismatch, a.Arch, runtime.GOARCH)
	}
	return nil
}

func readStableArtifact(path string) ([]byte, error) {
	return readStableLimitedFile(path, maxWorkerArtifactBytes, "artifact")
}

func readStableLimitedFile(path string, limit int, label string) ([]byte, error) {
	// #nosec G304 -- path is the caller-selected artifact constrained by this function's regular-file and stability checks.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", label)
	}
	if before.Size() > int64(limit) {
		return nil, fmt.Errorf("%s exceeds %d-byte limit", label, limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%s exceeds %d-byte limit", label, limit)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("%s changed while being read", label)
	}
	return data, nil
}

func installImmutableObject(dir, path string, data []byte, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%w: content-addressed object is not a regular file", ErrVerification)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("integrationhost: inspect artifact object: %w", err)
	}
	// #nosec G304 -- path is contained by InstallVerifiedObject's content-addressed object construction.
	if existing, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(existing, data) {
			return fmt.Errorf("%w: content-addressed object mismatch", ErrVerification)
		}
		return os.Chmod(path, mode)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("integrationhost: inspect artifact object: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".install-*")
	if err != nil {
		return fmt.Errorf("integrationhost: stage artifact object: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Link(tmpPath, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("integrationhost: publish artifact object: %w", err)
		}
		// #nosec G304 -- path is contained by InstallVerifiedObject's content-addressed object construction.
		existing, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(existing, data) {
			return fmt.Errorf("%w: raced content-addressed object mismatch", ErrVerification)
		}
	}
	return nil
}

// HashFile returns the lowercase-hex SHA-256 of a file. The install path uses it
// to record the verified artifact hash on the worker row.
func HashFile(path string) (string, error) {
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

func verifyHash(data []byte, want string) error {
	if want == "" {
		return fmt.Errorf("%w: empty hash pin", ErrVerification)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	// constantTimeHexEqual avoids leaking the mismatch position; hashes are not
	// secret, but the discipline matches the rest of the codebase's HMAC paths.
	if !constantTimeStringEqual(got, want) {
		return fmt.Errorf("%w: sha256 mismatch", ErrVerification)
	}
	return nil
}

func verifySignature(data []byte, key ed25519.PublicKey, sig []byte) error {
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: invalid signing key", ErrVerification)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: invalid signature length", ErrVerification)
	}
	if !ed25519.Verify(key, data, sig) {
		return fmt.Errorf("%w: signature does not verify", ErrVerification)
	}
	return nil
}
