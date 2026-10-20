package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func mustKey(t *testing.T) (ed25519.PublicKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	return pub, priv.Seed()
}

func sampleManifest() Manifest {
	return Manifest{
		Version: "v1.2.3",
		Channel: "stable",
		Artifacts: []Artifact{
			{Name: "b_arm64.tar.gz", SHA256: "bbbb"},
			{Name: "a_amd64.tar.gz", SHA256: "aaaa"},
		},
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, seed := mustKey(t)
	m := sampleManifest()
	sig, err := Sign(seed, m)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	m.Signature = sig
	if err := Verify(pub, m); err != nil {
		t.Fatalf("Verify of a freshly signed manifest failed: %v", err)
	}
}

func TestSigningIsArtifactOrderIndependent(t *testing.T) {
	pub, seed := mustKey(t)
	m := sampleManifest()
	sig, err := Sign(seed, m)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Reverse the artifact order; the canonical sort must make the signature
	// still verify.
	m.Artifacts[0], m.Artifacts[1] = m.Artifacts[1], m.Artifacts[0]
	m.Signature = sig
	if err := Verify(pub, m); err != nil {
		t.Fatalf("Verify failed after reordering artifacts (signing not canonical): %v", err)
	}
}

func TestVerifyRejectsTamper(t *testing.T) {
	pub, seed := mustKey(t)
	m := sampleManifest()
	sig, _ := Sign(seed, m)
	m.Signature = sig

	t.Run("bumped version", func(t *testing.T) {
		bad := m
		bad.Version = "v9.9.9"
		if err := Verify(pub, bad); err == nil {
			t.Fatal("Verify accepted a tampered version")
		}
	})
	t.Run("added artifact", func(t *testing.T) {
		bad := m
		bad.Artifacts = append([]Artifact{{Name: "evil", SHA256: "ffff"}}, m.Artifacts...)
		if err := Verify(pub, bad); err == nil {
			t.Fatal("Verify accepted an injected artifact")
		}
	})
	t.Run("wrong key", func(t *testing.T) {
		other, _ := mustKey(t)
		if err := Verify(other, m); err == nil {
			t.Fatal("Verify accepted a non-matching key")
		}
	})
	t.Run("unsigned", func(t *testing.T) {
		bad := m
		bad.Signature = ""
		if err := Verify(pub, bad); err == nil {
			t.Fatal("Verify accepted an unsigned manifest")
		}
	})
	t.Run("empty key fail-closed", func(t *testing.T) {
		if err := Verify(nil, m); err == nil {
			t.Fatal("Verify accepted a nil key (must fail closed)")
		}
	})
}

func TestVerifyArtifact(t *testing.T) {
	m := Manifest{Artifacts: []Artifact{{Name: "x.tar.gz", SHA256: "ABCDEF"}}}
	if err := VerifyArtifact(m, "x.tar.gz", "abcdef"); err != nil {
		t.Fatalf("case-insensitive digest match failed: %v", err)
	}
	if err := VerifyArtifact(m, "x.tar.gz", "deadbeef"); err == nil {
		t.Fatal("digest mismatch accepted (tampered)")
	}
	if err := VerifyArtifact(m, "absent.tar.gz", "abcdef"); err == nil {
		t.Fatal("unknown artifact accepted")
	}
}

func TestPinnedKeyIsUsable(t *testing.T) {
	pub, ok := PinnedKey()
	if !ok {
		t.Fatal("PinnedKey() not usable — the committed trust anchor must decode")
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("pinned key size = %d, want %d", len(pub), ed25519.PublicKeySize)
	}
}

func TestPinnedKeyRejectsUnusablePins(t *testing.T) {
	for _, pin := range []string{
		"",
		"not base64",
		"dG9vIHNob3J0",
	} {
		if _, ok := pinnedKey(pin); ok {
			t.Errorf("pinnedKey(%q) ok = true, want false", pin)
		}
	}
}

func TestParseSumsAndVerifyDistTamper(t *testing.T) {
	dir := t.TempDir()
	// Two artifacts + a real shasum-format SHA256SUMS.
	a := []byte("artifact A contents\n")
	b := []byte("artifact B contents\n")
	if err := os.WriteFile(filepath.Join(dir, "a.tar.gz"), a, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.tar.gz"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	sums := "" +
		SHA256Hex(a) + "  ./a.tar.gz\n" +
		SHA256Hex(b) + "  ./b.tar.gz\n"
	if err := os.WriteFile(filepath.Join(dir, SumsFile), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}

	arts, err := ParseSums(filepath.Join(dir, SumsFile))
	if err != nil {
		t.Fatalf("ParseSums: %v", err)
	}
	if len(arts) != 2 || arts[0].Name != "a.tar.gz" {
		t.Fatalf("ParseSums gave %+v, want 2 sorted artifacts with ./ stripped", arts)
	}

	// Sign with a test key and verify the dist by injecting the same key into a
	// signed manifest written to disk (VerifyDist uses the pinned key, so drive
	// the lower-level checks directly here).
	pub, seed := mustKey(t)
	m := Manifest{Version: "v1", Channel: "stable", Artifacts: arts}
	sig, _ := Sign(seed, m)
	m.Signature = sig
	if err := Verify(pub, m); err != nil {
		t.Fatalf("signed dist manifest failed verify: %v", err)
	}
	// Recompute-and-bind passes for the untouched files...
	if err := VerifyArtifact(m, "a.tar.gz", SHA256Hex(a)); err != nil {
		t.Fatalf("clean artifact rejected: %v", err)
	}
	// ...and rejects a tampered file.
	if err := VerifyArtifact(m, "a.tar.gz", SHA256Hex([]byte("EVIL"))); err == nil {
		t.Fatal("tampered artifact accepted by VerifyArtifact")
	}
}

func TestParseSumsRejectsUnsafeEntries(t *testing.T) {
	digest := SHA256Hex([]byte("artifact"))
	for _, sums := range []string{
		digest + "  ../outside.tar.gz\n",
		"not-a-sha256  artifact.tar.gz\n",
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, SumsFile), []byte(sums), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseSums(filepath.Join(dir, SumsFile)); err == nil {
			t.Fatalf("ParseSums accepted unsafe entry %q", strings.TrimSpace(sums))
		}
	}
}

func TestVerifyDistRejectsArtifactOutsideDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dist")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("outside artifact")
	if err := os.WriteFile(filepath.Join(root, "outside.tar.gz"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	pub, seed := mustKey(t)
	m := Manifest{
		Version:   "v1.0.0",
		Channel:   "stable",
		Artifacts: []Artifact{{Name: "../outside.tar.gz", SHA256: SHA256Hex(content)}},
	}
	m.Signature, _ = Sign(seed, m)
	manifest, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyDist(dir, func(m Manifest) error { return Verify(pub, m) }); err == nil {
		t.Fatal("verifyDist accepted a signed artifact outside the dist directory")
	}
}

func TestAttestAndVerifyDistEndToEnd(t *testing.T) {
	dir := t.TempDir()
	content := []byte("the beamfall server binary tarball\n")
	if err := os.WriteFile(filepath.Join(dir, "beamfall_linux_amd64.tar.gz"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SumsFile), []byte(SHA256Hex(content)+"  beamfall_linux_amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Attest signed against the PINNED key would need the real secret; instead
	// drive Attest with a test seed and verify with the matching key directly.
	_, seed := mustKey(t)
	written, err := Attest(AttestOptions{
		Dir:          dir,
		Version:      "v1.0.0",
		Channel:      "stable",
		Commit:       "abc123",
		Timestamp:    "2026-06-20T00:00:00Z",
		InvocationID: "https://github.com/beamfall/core/actions/runs/1/attempts/1",
		SigningSeed:  seed,
		Modules:      []*debug.Module{{Path: "github.com/beamfall/core", Version: "(devel)"}},
	})
	if err != nil {
		t.Fatalf("Attest: %v", err)
	}
	for _, want := range []string{ManifestFile, SBOMFile, ProvenanceFile} {
		if !contains(written, want) {
			t.Fatalf("Attest did not report writing %s (wrote %v)", want, written)
		}
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Fatalf("Attest did not create %s: %v", want, err)
		}
	}
}

func TestVerifyDistRejectsTamperedArtifactOnDisk(t *testing.T) {
	dir := t.TempDir()
	content := []byte("the beamfall server binary\n")
	if err := os.WriteFile(filepath.Join(dir, "beamfall_linux_amd64.tar.gz"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SumsFile), []byte(SHA256Hex(content)+"  beamfall_linux_amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pub, seed := mustKey(t)
	if _, err := Attest(AttestOptions{
		Dir: dir, Version: "v1.0.0", Channel: "stable", Commit: "abc123", Timestamp: "t",
		InvocationID: "run-1", SigningSeed: seed,
		Modules: []*debug.Module{{Path: "m", Version: "(devel)"}},
	}); err != nil {
		t.Fatalf("Attest: %v", err)
	}
	verify := func(m Manifest) error { return Verify(pub, m) }

	// Clean dist verifies.
	if err := verifyDist(dir, verify); err != nil {
		t.Fatalf("clean dist rejected: %v", err)
	}
	// Tamper the artifact on disk after signing — the recompute-from-disk step
	// must reject it (the "upgrade path rejects a tampered artifact" assertion).
	if err := os.WriteFile(filepath.Join(dir, "beamfall_linux_amd64.tar.gz"), append(content, []byte("EVIL")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDist(dir, verify); err == nil {
		t.Fatal("verifyDist accepted a tampered on-disk artifact, want refusal")
	}
}

func TestVerifyDistRejectsUnlistedArtifactOnDisk(t *testing.T) {
	dir := t.TempDir()
	content := []byte("signed artifact\n")
	if err := os.WriteFile(filepath.Join(dir, "artifact.bin"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SumsFile), []byte(SHA256Hex(content)+"  artifact.bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, seed := mustKey(t)
	if _, err := Attest(AttestOptions{
		Dir: dir, Version: "v1", Channel: "stable", Commit: "abc", Timestamp: "t", InvocationID: "run",
		SigningSeed: seed, Modules: []*debug.Module{{Path: "m", Version: "(devel)"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unsigned-extra.txt"), []byte("not signed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyDist(dir, func(m Manifest) error { return Verify(pub, m) }); err == nil || !strings.Contains(err.Error(), "unlisted artifact") {
		t.Fatalf("verifyDist error = %v, want unlisted artifact refusal", err)
	}
}

func TestAttestRefusesUnsignedWithoutOptIn(t *testing.T) {
	dir := t.TempDir()
	content := []byte("x")
	os.WriteFile(filepath.Join(dir, "x.tar.gz"), content, 0o644)
	os.WriteFile(filepath.Join(dir, SumsFile), []byte(SHA256Hex(content)+"  x.tar.gz\n"), 0o644)

	if _, err := Attest(AttestOptions{Dir: dir, Version: "v1", Channel: "stable", Timestamp: "t"}); err == nil {
		t.Fatal("Attest produced a release with no key and no --allow-unsigned, want refusal")
	}
	// With AllowUnsigned the manifest is written but carries no signature.
	if _, err := Attest(AttestOptions{Dir: dir, Version: "v1", Channel: "stable", Timestamp: "t", AllowUnsigned: true, Modules: []*debug.Module{{Path: "m", Version: "(devel)"}}}); err != nil {
		t.Fatalf("Attest --allow-unsigned: %v", err)
	}
	m, err := LoadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(m.Signature) != "" {
		t.Fatal("--allow-unsigned manifest carries a signature, want none")
	}
}

func TestSBOMAndProvenanceShape(t *testing.T) {
	mods := []*debug.Module{
		{Path: "github.com/beamfall/core", Version: "(devel)"},
		{Path: "modernc.org/sqlite", Version: "v1.0.0"},
	}
	sbom := BuildSBOM("v1.0.0", "2026-06-20T00:00:00Z", mods)
	if err := sbom.Validate(); err != nil {
		t.Fatalf("SBOM invalid: %v", err)
	}
	if sbom.SPDXVersion != "SPDX-2.3" || len(sbom.Packages) != 2 {
		t.Fatalf("SBOM shape wrong: version=%q packages=%d", sbom.SPDXVersion, len(sbom.Packages))
	}

	prov := BuildProvenance("v1.0.0", "stable", "abc123", "run-1", "2026-06-20T00:00:00Z",
		[]Artifact{{Name: "a.tar.gz", SHA256: "aaaa"}})
	if err := prov.Validate(); err != nil {
		t.Fatalf("provenance invalid: %v", err)
	}
	if prov.PredicateType != slsaPredicateType || len(prov.Subject) != 1 {
		t.Fatalf("provenance shape wrong: predicate=%q subjects=%d", prov.PredicateType, len(prov.Subject))
	}
	if prov.Subject[0].Digest["sha256"] != "aaaa" {
		t.Fatalf("provenance subject digest = %q, want aaaa", prov.Subject[0].Digest["sha256"])
	}
	if prov.Predicate.RunDetails.Metadata.InvocationID != "run-1" {
		t.Fatalf("provenance invocation ID = %q, want run-1", prov.Predicate.RunDetails.Metadata.InvocationID)
	}
}

func TestAttestBindsGeneratedAndAdditionalArtifacts(t *testing.T) {
	dir := t.TempDir()
	primary := []byte("archive")
	if err := os.WriteFile(filepath.Join(dir, "archive.tar.gz"), primary, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SumsFile), []byte(SHA256Hex(primary)+"  archive.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"wire-contract.json":           []byte("{}\n"),
		"release-gate-evidence.tar.gz": []byte("evidence"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pub, seed := mustKey(t)
	if _, err := Attest(AttestOptions{
		Dir: dir, Version: "v1", Channel: "stable", Commit: "abc", Timestamp: "2026-01-01T00:00:00Z",
		InvocationID: "run-1", SigningSeed: seed,
		AdditionalArtifacts: []string{"wire-contract.json", "release-gate-evidence.tar.gz"},
		Modules:             []*debug.Module{{Path: "m", Version: "v1"}},
	}); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(pub, m); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"archive.tar.gz", SumsFile, SBOMFile, ProvenanceFile, "wire-contract.json", "release-gate-evidence.tar.gz"} {
		b := readReleaseTestArtifact(t, dir, name)
		if err := VerifyArtifact(m, name, SHA256Hex(b)); err != nil {
			t.Errorf("artifact %s is not bound: %v", name, err)
		}
	}
	for _, name := range []string{SBOMFile, ProvenanceFile, "wire-contract.json", "release-gate-evidence.tar.gz"} {
		t.Run("tamper-"+name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			original := readReleaseTestArtifact(t, dir, name)
			if err := os.WriteFile(path, append(original, 'x'), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := verifyDist(dir, func(m Manifest) error { return Verify(pub, m) }); err == nil {
				t.Fatal("tampered artifact verified")
			}
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func readReleaseTestArtifact(t *testing.T, dir, name string) []byte {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("close release test root: %v", err)
		}
	}()
	contents, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestSignedAttestRequiresReleaseIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SumsFile), []byte(SHA256Hex([]byte("x"))+"  x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, seed := mustKey(t)
	if _, err := Attest(AttestOptions{
		Dir: dir, Version: "v1", Channel: "stable", Commit: "abc", Timestamp: "t", SigningSeed: seed,
		Modules: []*debug.Module{{Path: "m", Version: "v1"}},
	}); err == nil {
		t.Fatal("signed attest accepted missing invocation ID")
	}
}

func TestDecodeSeed(t *testing.T) {
	if seed, err := DecodeSeed(""); err != nil || seed != nil {
		t.Fatalf("DecodeSeed(empty) = %v,%v want nil,nil", seed, err)
	}
	if _, err := DecodeSeed("not base64!!!"); err == nil {
		t.Fatal("DecodeSeed accepted non-base64")
	}
	if _, err := DecodeSeed("YWJj"); err == nil { // "abc" — 3 bytes, not 32
		t.Fatal("DecodeSeed accepted a wrong-length seed")
	}
}

// contains is a tiny local helper (the conformance arch test has its own).
func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
