package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/app"
	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/release"
)

func testCLISignedDist(t *testing.T, version string, seed []byte, binaryBytes []byte) string {
	t.Helper()
	dir := t.TempDir()
	binName := "beamfall-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, binName), binaryBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sums := release.SHA256Hex(binaryBytes) + "  " + binName + "\n"
	if err := os.WriteFile(filepath.Join(dir, release.SumsFile), []byte(sums), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := release.Attest(release.AttestOptions{
		Dir:           dir,
		Version:       version,
		Channel:       "stable",
		Commit:        "cafebabe",
		Timestamp:     "2026-11-26T00:00:00Z",
		InvocationID:  "cli-upgrade-test",
		SigningSeed:   seed,
		AllowUnsigned: len(seed) == 0,
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUpgradeCLIStageApplyAndStatus(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed := priv.Seed()
	dataDir := t.TempDir()
	targetBin := filepath.Join(t.TempDir(), "beamfall")
	if err := os.WriteFile(targetBin, []byte("old-cli-bin"), 0o600); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 26, 13, 0, 0, 0, time.UTC)

	dist := testCLISignedDist(t, "v2.0.0", seed, []byte("new-cli-bin-v2.0.0"))

	// Blocked without verified Backup Set or elevated bypass.
	var out bytes.Buffer
	if err := upgradeCommandWithKey(context.Background(), []string{
		"stage", "--data-dir", dataDir, "--dist", dist,
	}, &out, pub); !errors.Is(err, backupcore.ErrUpgradeGateBlocked) {
		t.Fatalf("blocked CLI stage err = %v, want ErrUpgradeGateBlocked", err)
	}

	// Passing integrity verification clears the gate for stage + status + apply.
	if _, err := backupcore.RecordRun(dataDir, backupcore.RunRecord{
		At:                t0,
		Set:               filepath.Join(dataDir, "backups", "beamfall-backup-1"),
		IntegrityVerified: true,
	}, true); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := upgradeCommandWithKey(context.Background(), []string{
		"stage", "--data-dir", dataDir, "--dist", dist,
	}, &out, pub); err != nil {
		t.Fatalf("CLI stage err = %v", err)
	}
	if !strings.Contains(out.String(), `"status": "staged"`) || !strings.Contains(out.String(), `"targetVersion": "v2.0.0"`) {
		t.Fatalf("unexpected CLI stage output: %s", out.String())
	}

	// status reports updateGateClear=true and stagedUpdate.
	out.Reset()
	if err := upgradeCommandWithKey(context.Background(), []string{
		"status", "--data-dir", dataDir,
	}, &out, pub); err != nil {
		t.Fatalf("CLI status err = %v", err)
	}
	var statusPayload struct {
		GateClear    bool   `json:"updateGateClear"`
		GatePolicy   string `json:"upgradeGate"`
		StagedUpdate *struct {
			Status        string `json:"status"`
			TargetVersion string `json:"targetVersion"`
		} `json:"stagedUpdate"`
	}
	if err := json.Unmarshal(out.Bytes(), &statusPayload); err != nil {
		t.Fatalf("decode status output: %v (%s)", err, out.String())
	}
	if !statusPayload.GateClear || statusPayload.GatePolicy != "soft" || statusPayload.StagedUpdate == nil || statusPayload.StagedUpdate.TargetVersion != "v2.0.0" {
		t.Fatalf("unexpected status payload: %+v", statusPayload)
	}

	// Apply refuses while server lock is held.
	releaseLock, err := lockServeDataDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := upgradeCommandWithKey(context.Background(), []string{
		"apply", "--data-dir", dataDir, "--binary", targetBin,
	}, &out, pub); err == nil {
		releaseLock()
		t.Fatal("CLI apply succeeded while serve.lock was held")
	}
	releaseLock()

	// Once stopped, apply swaps the binary atomically and preserves .prev.
	out.Reset()
	if err := upgradeCommandWithKey(context.Background(), []string{
		"apply", "--data-dir", dataDir, "--binary", targetBin,
	}, &out, pub); err != nil {
		t.Fatalf("CLI apply err = %v", err)
	}
	// #nosec G304 -- targetBin is inside t.TempDir().
	gotNew, _ := os.ReadFile(targetBin)
	// #nosec G304 -- targetBin + ".prev" is inside t.TempDir().
	gotPrev, _ := os.ReadFile(targetBin + ".prev")
	if string(gotNew) != "new-cli-bin-v2.0.0" || string(gotPrev) != "old-cli-bin" {
		t.Fatalf("post-apply binaries = (%q, %q)", gotNew, gotPrev)
	}

	// Holding acquireExclusiveServerLock blocks a concurrent CLI stage and lockServeDataDir.
	holdLock, err := acquireExclusiveServerLock(dataDir)
	if err != nil {
		t.Fatalf("acquireExclusiveServerLock err = %v", err)
	}
	if _, err := lockServeDataDir(dataDir); err == nil {
		holdLock()
		t.Fatal("lockServeDataDir succeeded while upgrade lock was held")
	}
	if err := upgradeCommandWithKey(context.Background(), []string{
		"stage", "--data-dir", dataDir, "--dist", dist,
	}, &out, pub); err == nil || !strings.Contains(err.Error(), "server appears to be running") {
		holdLock()
		t.Fatalf("stage while upgrade lock held err = %v", err)
	}
	holdLock()

	// Official release .tar.gz archive dist stages and applies end-to-end via CLI.
	tarDataDir := t.TempDir()
	tarNow := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	if _, err := backupcore.RecordRun(tarDataDir, backupcore.RunRecord{
		At:                tarNow,
		Set:               filepath.Join(tarDataDir, "backups", "set-ok"),
		IntegrityVerified: true,
	}, true); err != nil {
		t.Fatal(err)
	}
	officialDist := testCLIOfficialTarDist(t, "v2.5.0", seed, []byte("official-cli-bin-v2.5.0"))
	tarTarget := filepath.Join(t.TempDir(), "beamfall")
	if err := os.WriteFile(tarTarget, []byte("pre-tar-bin"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := upgradeCommandWithKey(context.Background(), []string{
		"stage", "--data-dir", tarDataDir, "--dist", officialDist,
	}, &out, pub); err != nil {
		t.Fatalf("CLI stage official tar.gz err = %v", err)
	}
	out.Reset()
	if err := upgradeCommandWithKey(context.Background(), []string{
		"apply", "--data-dir", tarDataDir, "--binary", tarTarget,
	}, &out, pub); err != nil {
		t.Fatalf("CLI apply official tar.gz err = %v", err)
	}
	// #nosec G304 -- tarTarget is inside t.TempDir().
	gotTarBin, _ := os.ReadFile(tarTarget)
	// #nosec G304 -- tarTarget + ".prev" is inside t.TempDir().
	gotTarPrev, _ := os.ReadFile(tarTarget + ".prev")
	if string(gotTarBin) != "official-cli-bin-v2.5.0" || string(gotTarPrev) != "pre-tar-bin" {
		t.Fatalf("official tar.gz apply binaries = (%q, %q)", gotTarBin, gotTarPrev)
	}
	_ = app.ErrNoStagedUpdate
}

func testCLIOfficialTarDist(t *testing.T, version string, seed []byte, binaryBytes []byte) string {
	t.Helper()
	dir := t.TempDir()
	stem := "beamfall-core_" + version + "_stable_" + runtime.GOOS + "_" + runtime.GOARCH
	binBase := "beamfall"
	if runtime.GOOS == "windows" {
		binBase = "beamfall.exe"
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     stem + "/" + binBase,
		Mode:     0o755,
		Size:     int64(len(binaryBytes)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binaryBytes); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archiveName := stem + ".tar.gz"
	if err := os.WriteFile(filepath.Join(dir, archiveName), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	sums := release.SHA256Hex(buf.Bytes()) + "  " + archiveName + "\n"
	if err := os.WriteFile(filepath.Join(dir, release.SumsFile), []byte(sums), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := release.Attest(release.AttestOptions{
		Dir:          dir,
		Version:      version,
		Channel:      "stable",
		Commit:       "deadbeef",
		Timestamp:    "2026-11-26T00:00:00Z",
		InvocationID: "cli-official-test",
		SigningSeed:  seed,
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}
