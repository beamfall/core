package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeSignedDistForTest(t *testing.T, version string, seed []byte, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	var sums string
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
		sums += SHA256Hex(content) + "  " + name + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, SumsFile), []byte(sums), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Attest(AttestOptions{
		Dir:           dir,
		Version:       version,
		Channel:       "stable",
		Commit:        "abc123",
		Timestamp:     "2026-11-26T00:00:00Z",
		InvocationID:  "test-run",
		SigningSeed:   seed,
		AllowUnsigned: len(seed) == 0,
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerifyDistWithKeyAndBinarySwap(t *testing.T) {
	pub, seed := mustKey(t)
	newBin := []byte("#!/bin/sh\necho beamfall v1.2.0\n")
	dir := writeSignedDistForTest(t, "v1.2.0", seed, map[string][]byte{
		"beamfall-linux-amd64": newBin,
	})

	manifest, err := VerifyDistWithKey(dir, pub)
	if err != nil {
		t.Fatalf("VerifyDistWithKey valid dist err = %v", err)
	}
	art, err := SelectBinaryArtifact(manifest, "linux", "amd64")
	if err != nil || art.Name != "beamfall-linux-amd64" {
		t.Fatalf("SelectBinaryArtifact = (%+v, %v)", art, err)
	}

	// Wrong key or unsigned dist fails closed.
	otherPub, _ := mustKey(t)
	if _, err := VerifyDistWithKey(dir, otherPub); err == nil {
		t.Fatal("VerifyDistWithKey accepted wrong public key")
	}
	unsignedDir := writeSignedDistForTest(t, "v1.2.0", nil, map[string][]byte{"beamfall": newBin})
	if _, err := VerifyDistWithKey(unsignedDir, pub); err == nil {
		t.Fatal("VerifyDistWithKey accepted unsigned dist")
	}

	// Binary swap replaces existing executable atomically and preserves .prev.
	target := filepath.Join(t.TempDir(), "beamfall")
	oldBin := []byte("#!/bin/sh\necho beamfall v1.1.0\n")
	if err := os.WriteFile(target, oldBin, 0o600); err != nil {
		t.Fatal(err)
	}
	rollback, err := ApplyBinarySwap(filepath.Join(dir, art.Name), target)
	if err != nil {
		t.Fatal(err)
	}
	if rollback != target+".prev" {
		t.Fatalf("rollback = %q, want %q", rollback, target+".prev")
	}
	// #nosec G304 -- target is in this test's t.TempDir().
	gotNew, err := os.ReadFile(target)
	if err != nil || string(gotNew) != string(newBin) {
		t.Fatalf("target binary = %q (%v), want %q", gotNew, err, newBin)
	}
	// #nosec G304 -- rollback is in this test's t.TempDir().
	gotPrev, err := os.ReadFile(rollback)
	if err != nil || string(gotPrev) != string(oldBin) {
		t.Fatalf("rollback binary = %q (%v), want %q", gotPrev, err, oldBin)
	}
}

func makeTestReleaseTarGz(t *testing.T, archiveStem string, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		mode := int64(0o644)
		if filepath.Base(name) == "beamfall" || filepath.Base(name) == "beamfall.exe" {
			mode = 0o755
		}
		fullName := archiveStem + "/" + name
		if err := tw.WriteHeader(&tar.Header{
			Name:     fullName,
			Mode:     mode,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSelectAndMaterializeOfficialReleaseTarGz(t *testing.T) {
	pub, seed := mustKey(t)
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	binBase := "beamfall"
	if goos == "windows" {
		binBase = "beamfall.exe"
	}
	execBytes := []byte("#!/bin/sh\necho beamfall v1.4.0\n")
	stem := "beamfall-core_v1.4.0_stable_" + goos + "_" + goarch
	archiveName := stem + ".tar.gz"
	archiveBytes := makeTestReleaseTarGz(t, stem, map[string][]byte{
		"LICENSE":     []byte("MIT"),
		"README.txt":  []byte("Beamfall Core v1.4.0"),
		"source.json": []byte(`{"version":"v1.4.0"}`),
		binBase:       execBytes,
	})
	dir := writeSignedDistForTest(t, "v1.4.0", seed, map[string][]byte{
		archiveName:                 archiveBytes,
		stem + ".source.json":       []byte(`{"version":"v1.4.0"}`),
		"beamfall-v1.4.0.sbom.json": []byte(`{"spdxVersion":"SPDX-2.3"}`),
		"wire-contract.json":        []byte(`{}`),
	})

	manifest, err := VerifyDistWithKey(dir, pub)
	if err != nil {
		t.Fatalf("VerifyDistWithKey official archive dist err = %v", err)
	}
	art, err := SelectBinaryArtifact(manifest, goos, goarch)
	if err != nil || art.Name != archiveName {
		t.Fatalf("SelectBinaryArtifact = (%+v, %v), want %q", art, err, archiveName)
	}

	matPath, cleanup, err := MaterializeStagedBinary(filepath.Join(dir, art.Name), goos, t.TempDir())
	if err != nil {
		t.Fatalf("MaterializeStagedBinary err = %v", err)
	}
	defer cleanup()

	// #nosec G304 -- matPath is inside t.TempDir().
	got, err := os.ReadFile(matPath)
	if err != nil || !bytes.Equal(got, execBytes) {
		t.Fatalf("extracted executable = %q (%v), want %q", got, err, execBytes)
	}

	// Non-matching platform fails instead of returning SBOM or another OS archive.
	if _, err := SelectBinaryArtifact(manifest, "plan9", "riscv64"); err == nil || !strings.Contains(err.Error(), "no binary or release archive") {
		t.Fatalf("SelectBinaryArtifact unsupported OS err = %v", err)
	}
}
