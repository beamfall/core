package integrationhost

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeArtifact(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestVerifyHashPin proves an artifact is accepted only when its bytes match the
// pinned SHA-256, and refused (ErrVerification) on any mismatch.
func TestVerifyHashPin(t *testing.T) {
	data := []byte("worker-binary-bytes")
	path := writeArtifact(t, data)
	sum := sha256.Sum256(data)
	good := Artifact{Path: path, OS: runtime.GOOS, Arch: runtime.GOARCH}

	if err := good.Verify(Pin{Kind: PinHash, Hash: hex.EncodeToString(sum[:])}); err != nil {
		t.Fatalf("matching hash should verify, got %v", err)
	}
	if err := good.Verify(Pin{Kind: PinHash, Hash: "deadbeef"}); !errors.Is(err, ErrVerification) {
		t.Fatalf("hash mismatch must refuse, got %v", err)
	}
	if err := good.Verify(Pin{Kind: PinHash, Hash: ""}); !errors.Is(err, ErrVerification) {
		t.Fatalf("empty hash pin must refuse, got %v", err)
	}
}

// TestVerifySigningKeyPin proves an Ed25519-signed artifact is accepted only
// under the trusted key + valid signature, and refused otherwise.
func TestVerifySigningKeyPin(t *testing.T) {
	data := []byte("signed-worker-bytes")
	path := writeArtifact(t, data)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, data)
	a := Artifact{Path: path, OS: runtime.GOOS, Arch: runtime.GOARCH}

	if err := a.Verify(Pin{Kind: PinSigningKey, SigningKey: pub, Signature: sig}); err != nil {
		t.Fatalf("valid signature should verify, got %v", err)
	}
	// Tampered signature is refused.
	bad := append([]byte(nil), sig...)
	bad[0] ^= 0xff
	if err := a.Verify(Pin{Kind: PinSigningKey, SigningKey: pub, Signature: bad}); !errors.Is(err, ErrVerification) {
		t.Fatalf("tampered signature must refuse, got %v", err)
	}
	// Wrong key is refused.
	otherPub, _, _ := ed25519.GenerateKey(nil)
	if err := a.Verify(Pin{Kind: PinSigningKey, SigningKey: otherPub, Signature: sig}); !errors.Is(err, ErrVerification) {
		t.Fatalf("wrong signing key must refuse, got %v", err)
	}
}

// TestVerifyNoPinRefused proves an unpinned artifact is never trusted.
func TestVerifyNoPinRefused(t *testing.T) {
	a := Artifact{Path: writeArtifact(t, []byte("x")), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if err := a.Verify(Pin{}); !errors.Is(err, ErrVerification) {
		t.Fatalf("unpinned artifact must refuse, got %v", err)
	}
}

func TestInstallVerifiedUpdateCandidateVerifiesArtifactBytesAndPreservesManifest(t *testing.T) {
	artifactBytes := []byte("worker-v2")
	manifestBytes := []byte(`{"id":"worker","version":"2.0.0"}`)
	source := Artifact{PluginID: "worker", Path: writeArtifact(t, artifactBytes), OS: runtime.GOOS, Arch: runtime.GOARCH}
	digest := sha256.Sum256(artifactBytes)
	pin := Pin{Kind: PinHash, Hash: hex.EncodeToString(digest[:])}
	installed, err := InstallVerifiedUpdateCandidate(t.TempDir(), source, manifestBytes, pin)
	if err != nil || installed.VerifiedSHA256 != pin.Hash {
		t.Fatalf("verified candidate install = %+v, %v", installed, err)
	}
	installedManifest, err := os.ReadFile(VerifiedUpdateManifestPath(installed.Path)) // #nosec G304 -- installed is contained by this test's temporary artifact root.
	if err != nil || !bytes.Equal(installedManifest, manifestBytes) {
		t.Fatalf("installed verified manifest = %q, %v", installedManifest, err)
	}
	if err := os.WriteFile(source.Path, []byte("tampered-worker-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallVerifiedUpdateCandidate(t.TempDir(), source, manifestBytes, pin); !errors.Is(err, ErrVerification) {
		t.Fatalf("artifact substitution = %v, want verification failure", err)
	}
}

func TestInstallVerifiedUpdateCandidateVerifiesArtifactSignature(t *testing.T) {
	artifactBytes := []byte("signed-worker-v2")
	manifestBytes := []byte(`{"id":"worker","version":"2.0.0"}`)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	source := Artifact{PluginID: "worker", Path: writeArtifact(t, artifactBytes), OS: runtime.GOOS, Arch: runtime.GOARCH}
	pin := Pin{Kind: PinSigningKey, SigningKey: publicKey, Signature: ed25519.Sign(privateKey, artifactBytes)}

	installed, err := InstallVerifiedUpdateCandidate(t.TempDir(), source, manifestBytes, pin)
	if err != nil || installed.VerifiedSHA256 == "" {
		t.Fatalf("signature-verified candidate install = %+v, %v", installed, err)
	}
	installedManifest, err := os.ReadFile(VerifiedUpdateManifestPath(installed.Path)) // #nosec G304 -- installed is contained by this test's temporary artifact root.
	if err != nil || !bytes.Equal(installedManifest, manifestBytes) {
		t.Fatalf("installed signature-verified manifest = %q, %v", installedManifest, err)
	}
	if err := os.WriteFile(source.Path, []byte("tampered-signed-worker-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallVerifiedUpdateCandidate(t.TempDir(), source, manifestBytes, pin); !errors.Is(err, ErrVerification) {
		t.Fatalf("signed artifact substitution = %v, want verification failure", err)
	}
}

func TestPackageManifestByteCeiling(t *testing.T) {
	manifestBytes := bytes.Repeat([]byte("x"), MaxPackageManifestBytes+1)
	artifactBytes := []byte("worker-v2")
	source := Artifact{PluginID: "worker", Path: writeArtifact(t, artifactBytes), OS: runtime.GOOS, Arch: runtime.GOARCH}
	digest := sha256.Sum256(artifactBytes)
	pin := Pin{Kind: PinHash, Hash: hex.EncodeToString(digest[:])}

	if _, err := InstallVerifiedUpdateCandidate(t.TempDir(), source, manifestBytes, pin); err == nil {
		t.Fatal("oversize in-memory package manifest was accepted")
	}
	path := filepath.Join(t.TempDir(), "package.json")
	if err := os.WriteFile(path, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPackageManifest(path); err == nil {
		t.Fatal("oversize package manifest file was read")
	}
}

func TestGrantApprovalBindingTracksExactVerifiedManifest(t *testing.T) {
	digest := strings.Repeat("a", 64)
	one, err := ManifestApprovalBinding(digest, []byte(`{"id":"worker","version":"1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	two, err := ManifestApprovalBinding(digest, []byte(`{"id":"worker","version":"2.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("different verified manifest bytes produced the same approval binding")
	}

	root := t.TempDir()
	if err := SetGrantApprovalBinding(root, "worker", PermReadCatalog, one); err != nil {
		t.Fatal(err)
	}
	got, ok, err := GrantApprovalBinding(root, "worker", PermReadCatalog)
	if err != nil || !ok || got != one {
		t.Fatalf("stored approval binding = %q ok=%v err=%v", got, ok, err)
	}
	if err := SetGrantApprovalBinding(root, "worker", PermReadCatalog, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := GrantApprovalBinding(root, "worker", PermReadCatalog); err != nil || ok {
		t.Fatalf("revoked approval binding remained effective: ok=%v err=%v", ok, err)
	}
}

// TestVerifyPlatformMismatch proves a cross-platform artifact is refused before
// any hash check — it could never launch on this host.
func TestVerifyPlatformMismatch(t *testing.T) {
	a := Artifact{Path: writeArtifact(t, []byte("x")), OS: "plan9", Arch: "sparc"}
	if err := a.Verify(Pin{Kind: PinHash, Hash: "irrelevant"}); !errors.Is(err, ErrPlatformMismatch) {
		t.Fatalf("platform mismatch must refuse, got %v", err)
	}
}

func TestInstallVerifiedObjectPinsExactBytes(t *testing.T) {
	sourcePath := writeArtifact(t, []byte("verified-worker"))
	wantHash, err := HashFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	source := Artifact{PluginID: "fixture", Version: "1", Path: sourcePath}
	installed, err := InstallVerifiedObject(t.TempDir(), source, Pin{Kind: PinHash, Hash: wantHash})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Path == sourcePath {
		t.Fatal("installed artifact still names mutable source path")
	}
	want, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("source replacement changed installed verified object")
	}
	if err := installed.Verify(Pin{Kind: PinHash, Hash: wantHash}); err != nil {
		t.Fatalf("installed object no longer verifies: %v", err)
	}
}

func TestInstallImmutableObjectReuseKeepsExecutableReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker")
	want := []byte("verified-worker")
	if err := installImmutableObject(dir, path, want, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := installImmutableObject(dir, path, want, 0o500); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o500 {
		t.Fatalf("reused object mode = %o, want 500", got)
	}
	// #nosec G304 -- path is test-owned and contained by t.TempDir.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("reused object bytes = %q, want %q", got, want)
	}
}
