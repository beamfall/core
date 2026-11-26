package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/release"
	"github.com/beamfall/core/internal/store"
)

func testSignedUpdateDist(t *testing.T, version string, seed []byte, payload []byte) string {
	t.Helper()
	dir := t.TempDir()
	binName := "beamfall-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, binName), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sums := release.SHA256Hex(payload) + "  " + binName + "\n"
	if err := os.WriteFile(filepath.Join(dir, release.SumsFile), []byte(sums), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := release.Attest(release.AttestOptions{
		Dir:           dir,
		Version:       version,
		Channel:       "stable",
		Commit:        "deadbeef",
		Timestamp:     "2026-11-26T00:00:00Z",
		InvocationID:  "update-stage-test",
		SigningSeed:   seed,
		AllowUnsigned: len(seed) == 0,
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStageAndApplyEnforcesGateAndSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed := priv.Seed()
	dataDir := t.TempDir()
	targetBin := filepath.Join(t.TempDir(), "beamfall")
	if err := os.WriteFile(targetBin, []byte("old-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	dist := testSignedUpdateDist(t, "v1.4.0", seed, []byte("new-binary-v1.4.0"))

	// Without a verified backup pass or soft bypass, StageUpdate fails closed.
	if _, err := StageUpdate(dataDir, dist, pub, t0); !errors.Is(err, backupcore.ErrUpgradeGateBlocked) {
		t.Fatalf("StageUpdate without backup err = %v, want ErrUpgradeGateBlocked", err)
	}

	// Hard gate blocks even if a soft bypass was attempted earlier.
	if _, err := backupcore.SetUpgradeGate(dataDir, backupcore.UpgradeGateHard); err != nil {
		t.Fatal(err)
	}
	if _, err := StageUpdate(dataDir, dist, pub, t0); !errors.Is(err, backupcore.ErrUpgradeGateBlocked) {
		t.Fatalf("hard StageUpdate err = %v, want ErrUpgradeGateBlocked", err)
	}

	// Switch to soft and authorize elevated bypass for v1.4.0.
	if _, err := backupcore.SetUpgradeGate(dataDir, backupcore.UpgradeGateSoft); err != nil {
		t.Fatal(err)
	}
	if _, err := backupcore.RecordUpgradeBypass(dataDir, "v1.4.0", "v1.4.0", "verified manually", "profile-1", t0); err != nil {
		t.Fatal(err)
	}

	// Unsigned/tampered dist is rejected even when the backup gate is clear/bypassed.
	unsignedDist := testSignedUpdateDist(t, "v1.4.0", nil, []byte("unsigned-bin"))
	if _, err := StageUpdate(dataDir, unsignedDist, pub, t0); err == nil {
		t.Fatal("StageUpdate accepted unsigned dist")
	}

	marker, err := StageUpdate(dataDir, dist, pub, t0)
	if err != nil {
		t.Fatalf("StageUpdate with matching soft bypass err = %v", err)
	}
	if !marker.BypassAuthorized || marker.TargetVersion != "v1.4.0" {
		t.Fatalf("marker = %+v", marker)
	}

	// Double-stage fails while one is active.
	if _, err := StageUpdate(dataDir, dist, pub, t0); !errors.Is(err, ErrUpdateAlreadyStaged) {
		t.Fatalf("duplicate StageUpdate err = %v, want ErrUpdateAlreadyStaged", err)
	}

	// If a backup failure arrives AFTER staging, apply refuses stale bypass unless overridden.
	if _, err := backupcore.RecordRun(dataDir, storeBackupRunRecord(t0.Add(time.Minute), "beamfall-backup-99", false), false); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyStagedUpdate(context.Background(), dataDir, targetBin, false, pub, t0.Add(2*time.Minute)); !errors.Is(err, backupcore.ErrUpgradeGateBlocked) {
		t.Fatalf("ApplyStagedUpdate after new failure err = %v, want ErrUpgradeGateBlocked", err)
	}

	// Re-authorizing bypass after the failure lets apply complete and swap cleanly.
	if _, err := backupcore.RecordUpgradeBypass(dataDir, "v1.4.0", "v1.4.0 beamfall-backup-99", "re-authorized", "profile-1", t0.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyStagedUpdate(context.Background(), dataDir, targetBin, false, pub, t0.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("ApplyStagedUpdate err = %v", err)
	}
	if applied.Status != "applied" || applied.RollbackBinary != targetBin+".prev" {
		t.Fatalf("applied marker = %+v", applied)
	}
	// #nosec G304 -- targetBin is inside t.TempDir().
	gotBin, _ := os.ReadFile(targetBin)
	// #nosec G304 -- targetBin + ".prev" is inside t.TempDir().
	gotPrev, _ := os.ReadFile(targetBin + ".prev")
	if string(gotBin) != "new-binary-v1.4.0" || string(gotPrev) != "old-binary" {
		t.Fatalf("binaries after swap = (%q, %q)", gotBin, gotPrev)
	}

	// Tampered staged.json PayloadDir pointing outside dataDir cannot bypass local payload confinement.
	evilDir := testSignedUpdateDist(t, "v1.4.0", seed, []byte("evil-binary"))
	marker, err = StageUpdate(dataDir, dist, pub, t0.Add(5*time.Minute))
	if err == nil {
		// Force a tampered marker with PayloadDir pointing to evilDir.
		marker.PayloadDir = evilDir
		if err := writeStagedUpdateMarker(dataDir, marker); err != nil {
			t.Fatal(err)
		}
		applied2, err := ApplyStagedUpdate(context.Background(), dataDir, targetBin, true, pub, t0.Add(6*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		// #nosec G304 -- targetBin is inside t.TempDir().
		gotAfter, _ := os.ReadFile(targetBin)
		if string(gotAfter) != "new-binary-v1.4.0" || applied2.ArtifactSHA256 != marker.ArtifactSHA256 {
			t.Fatalf("tampered PayloadDir influenced binary (%q)", gotAfter)
		}
	}
}

func storeBackupRunRecord(at time.Time, set string, integrity bool) backupcore.RunRecord {
	if integrity {
		return backupcore.RunRecord{At: at, Set: set, IntegrityVerified: true}
	}
	return backupcore.RunRecord{At: at, FailureClass: "destination-full", Set: set}
}

func TestAppNewEnforcesBootUpgradeGateBeforeStoreOpen(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)

	oldVersion := capabilities.ReleaseVersion
	capabilities.ReleaseVersion = "v1.0.0"
	t.Cleanup(func() { capabilities.ReleaseVersion = oldVersion })

	first, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("initial New v1.0.0 err = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate operator setting hard update gate without a passing Backup Set.
	if _, err := backupcore.SetUpgradeGate(cfg.DataDir, backupcore.UpgradeGateHard); err != nil {
		t.Fatal(err)
	}

	// Upgrading to v1.1.0 under hard gate refuses before migrations run.
	capabilities.ReleaseVersion = "v1.1.0"
	if _, err := New(ctx, cfg, nil, nil); !errors.Is(err, backupcore.ErrUpgradeGateBlocked) {
		t.Fatalf("hard upgrade New v1.1.0 err = %v, want ErrUpgradeGateBlocked", err)
	}

	// Switching to soft allows boot with banner + backup.upgrade_without_backup outbox webhook.
	if _, err := backupcore.SetUpgradeGate(cfg.DataDir, backupcore.UpgradeGateSoft); err != nil {
		t.Fatal(err)
	}
	upgraded, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("soft upgrade New v1.1.0 err = %v", err)
	}
	events := backupOutboxEvents(t, upgraded.store, store.BackupEventUpgradeWithoutBackup)
	if len(events) != 1 {
		t.Fatalf("backup.upgrade_without_backup outbox events = %d, want 1", len(events))
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := backupcore.LoadStatus(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastBinaryVersion != "v1.1.0" || !st.UnverifiedUpgradeBannerActive() {
		t.Fatalf("post-upgrade status = %+v", st)
	}
}

func TestApplyStagedUpdateRejectsNonExecutableSignedArtifact(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed := priv.Seed()
	dataDir := t.TempDir()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	distDir := t.TempDir()
	binName := "beamfall-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	sbomName := "beamfall-v2.0.0.sbom.spdx.json"
	binBytes := []byte("real-binary-v2")
	sbomBytes := []byte(`{"spdxVersion":"SPDX-2.3"}`)
	if err := os.WriteFile(filepath.Join(distDir, binName), binBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, sbomName), sbomBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sums := release.SHA256Hex(binBytes) + "  " + binName + "\n" +
		release.SHA256Hex(sbomBytes) + "  " + sbomName + "\n"
	if err := os.WriteFile(filepath.Join(distDir, release.SumsFile), []byte(sums), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := release.Attest(release.AttestOptions{
		Dir:          distDir,
		Version:      "v2.0.0",
		Channel:      "stable",
		Commit:       "deadbeef",
		Timestamp:    "2026-11-26T00:00:00Z",
		InvocationID: "sbom-test",
		SigningSeed:  seed,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := backupcore.RecordRun(dataDir, storeBackupRunRecord(now, filepath.Join(dataDir, "backups", "set-ok"), true), true); err != nil {
		t.Fatal(err)
	}
	if _, err := StageUpdate(dataDir, distDir, pub, now); err != nil {
		t.Fatalf("StageUpdate err = %v", err)
	}
	marker, ok, err := LoadStagedUpdate(dataDir)
	if err != nil || !ok {
		t.Fatalf("LoadStagedUpdate = (%+v, %v, %v)", marker, ok, err)
	}
	marker.ArtifactName = sbomName
	marker.ArtifactSHA256 = release.SHA256Hex(sbomBytes)
	if err := writeStagedUpdateMarker(dataDir, marker); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "beamfall")
	orig := []byte("existing-binary-v1")
	if err := os.WriteFile(target, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyStagedUpdate(ctx, dataDir, target, true, pub, now.Add(time.Minute)); err == nil {
		t.Fatal("ApplyStagedUpdate accepted signed non-executable SBOM artifact")
	}
	// #nosec G304 -- target is in t.TempDir().
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(orig) {
		t.Fatalf("target binary changed to %q (%v), want %q", got, err, orig)
	}
}

func TestInterruptedBootRetryStillEmitsUpgradeWebhook(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)

	oldVersion := capabilities.ReleaseVersion
	capabilities.ReleaseVersion = "v1.0.0"
	t.Cleanup(func() { capabilities.ReleaseVersion = oldVersion })

	first, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate interrupted boot (canceled context) during upgrade to v1.1.0.
	capabilities.ReleaseVersion = "v1.1.0"
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := New(canceledCtx, cfg, nil, nil); err == nil {
		t.Fatal("expected canceled New v1.1.0 boot to fail")
	}

	// Retry boot must still detect v1.0.0 -> v1.1.0 and emit the upgrade webhook.
	retried, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("retry New v1.1.0 err = %v", err)
	}
	defer func() { _ = retried.Close() }()
	events := backupOutboxEvents(t, retried.store, store.BackupEventUpgradeWithoutBackup)
	if len(events) != 1 {
		t.Fatalf("retry backup.upgrade_without_backup events = %d, want 1", len(events))
	}
}

func TestAuthorizeUpgradeBypassRollsBackOnAuditFailure(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	instance, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = instance.Close() }()

	if _, err := backupcore.RecordRun(cfg.DataDir, storeBackupRunRecord(now, filepath.Join(cfg.DataDir, "backups", "set-1"), false), false); err != nil {
		t.Fatal(err)
	}
	ops := &updateGateOps{
		dataDir: cfg.DataDir,
		store:   instance.store,
		now:     func() time.Time { return now.Add(time.Minute) },
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ops.AuthorizeUpgradeBypass(canceledCtx, "profile-1", "v2.0.0", "v2.0.0 set-1", "audit fail"); err == nil {
		t.Fatal("expected canceled AuthorizeUpgradeBypass to fail")
	}
	st, err := backupcore.LoadStatus(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CanStageUpdate("v2.0.0"); !errors.Is(err, backupcore.ErrUpgradeGateBlocked) {
		t.Fatalf("CanStageUpdate after rolled-back bypass err = %v, want ErrUpgradeGateBlocked", err)
	}
}

func makeOfficialReleaseDist(t *testing.T, version, channel string, seed []byte, execBytes []byte) string {
	t.Helper()
	dir := t.TempDir()
	stem := "beamfall-core_" + version + "_" + channel + "_" + runtime.GOOS + "_" + runtime.GOARCH
	binBase := "beamfall"
	if runtime.GOOS == "windows" {
		binBase = "beamfall.exe"
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{
		"LICENSE":     []byte("License"),
		"README.txt":  []byte("Beamfall Core"),
		"source.json": []byte(`{"version":"` + version + `"}`),
		binBase:       execBytes,
	} {
		mode := int64(0o644)
		if name == binBase {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     stem + "/" + name,
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
	archiveName := stem + ".tar.gz"
	archiveBytes := buf.Bytes()
	sourceBytes := []byte(`{"version":"` + version + `"}`)
	if err := os.WriteFile(filepath.Join(dir, archiveName), archiveBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stem+".source.json"), sourceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sums := release.SHA256Hex(archiveBytes) + "  " + archiveName + "\n" +
		release.SHA256Hex(sourceBytes) + "  " + stem + ".source.json\n"
	if err := os.WriteFile(filepath.Join(dir, release.SumsFile), []byte(sums), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := release.Attest(release.AttestOptions{
		Dir:          dir,
		Version:      version,
		Channel:      channel,
		Commit:       "deadbeef",
		Timestamp:    "2026-11-26T00:00:00Z",
		InvocationID: "official-dist",
		SigningSeed:  seed,
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStageAndApplyOfficialReleaseTarGz(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed := priv.Seed()
	dataDir := t.TempDir()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	if _, err := backupcore.RecordRun(dataDir, storeBackupRunRecord(now, filepath.Join(dataDir, "backups", "set-ok"), true), true); err != nil {
		t.Fatal(err)
	}
	wantBin := []byte("#!/bin/sh\necho official v2.2.0\n")
	distDir := makeOfficialReleaseDist(t, "v2.2.0", "stable", seed, wantBin)

	staged, err := StageUpdate(dataDir, distDir, pub, now)
	if err != nil {
		t.Fatalf("StageUpdate official archive err = %v", err)
	}
	if !strings.HasSuffix(staged.ArtifactName, ".tar.gz") {
		t.Fatalf("staged.ArtifactName = %q, want .tar.gz", staged.ArtifactName)
	}
	target := filepath.Join(t.TempDir(), "beamfall")
	oldBin := []byte("#!/bin/sh\necho old\n")
	if err := os.WriteFile(target, oldBin, 0o600); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyStagedUpdate(context.Background(), dataDir, target, false, pub, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ApplyStagedUpdate official archive err = %v", err)
	}
	if applied.Status != "applied" {
		t.Fatalf("applied = %+v", applied)
	}
	// #nosec G304 -- target is inside t.TempDir().
	gotNew, _ := os.ReadFile(target)
	// #nosec G304 -- RollbackBinary is inside t.TempDir().
	gotPrev, _ := os.ReadFile(applied.RollbackBinary)
	if !bytes.Equal(gotNew, wantBin) || !bytes.Equal(gotPrev, oldBin) {
		t.Fatalf("post-apply binaries = (%q, %q)", gotNew, gotPrev)
	}
}

func TestStageApplyCancelRejectsSymlinkEscape(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed := priv.Seed()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	externalDir := t.TempDir()
	canaryPayload := filepath.Join(externalDir, "payload")
	if err := os.MkdirAll(canaryPayload, 0o700); err != nil {
		t.Fatal(err)
	}
	canaryFile := filepath.Join(canaryPayload, "keep.txt")
	if err := os.WriteFile(canaryFile, []byte("do-not-delete"), 0o600); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	if _, err := backupcore.RecordRun(dataDir, storeBackupRunRecord(now, filepath.Join(dataDir, "backups", "set-ok"), true), true); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalDir, filepath.Join(dataDir, "update-staged")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	distDir := testSignedUpdateDist(t, "v2.0.0", seed, []byte("bin-v2"))
	if _, err := StageUpdate(dataDir, distDir, pub, now); err == nil {
		t.Fatal("StageUpdate succeeded when update-staged is a symlink")
	}
	if _, _, err := LoadStagedUpdate(dataDir); err == nil {
		t.Fatal("LoadStagedUpdate succeeded when update-staged is a symlink")
	}
	if _, err := CancelStagedUpdate(dataDir); err == nil {
		t.Fatal("CancelStagedUpdate succeeded when update-staged is a symlink")
	}
	// #nosec G304 -- canaryFile is inside t.TempDir().
	if got, err := os.ReadFile(canaryFile); err != nil || string(got) != "do-not-delete" {
		t.Fatalf("external canary modified or deleted: %q (%v)", got, err)
	}
}

func TestStageUpdateSerializesConcurrentCalls(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed := priv.Seed()
	dataDir := t.TempDir()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	if _, err := backupcore.RecordRun(dataDir, storeBackupRunRecord(now, filepath.Join(dataDir, "backups", "set-ok"), true), true); err != nil {
		t.Fatal(err)
	}
	distA := testSignedUpdateDist(t, "v2.0.0", seed, []byte("bin-v2.0.0"))
	distB := testSignedUpdateDist(t, "v2.1.0", seed, []byte("bin-v2.1.0"))

	const workers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	wins := make([]StagedUpdateMarker, 0, workers)
	var mu sync.Mutex
	errs := make([]error, 0, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		dist := distA
		if i%2 == 1 {
			dist = distB
		}
		go func(d string) {
			defer wg.Done()
			<-start
			m, err := StageUpdate(dataDir, d, pub, now)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				wins = append(wins, m)
			}
		}(dist)
	}
	close(start)
	wg.Wait()

	if len(wins) != 1 || len(errs) != workers-1 {
		t.Fatalf("concurrent StageUpdate wins=%d errs=%v, want 1 winner", len(wins), errs)
	}
	for _, err := range errs {
		if !errors.Is(err, ErrUpdateAlreadyStaged) {
			t.Fatalf("unexpected loser err = %v, want ErrUpdateAlreadyStaged", err)
		}
	}
	loaded, ok, err := LoadStagedUpdate(dataDir)
	if err != nil || !ok || loaded.TargetVersion != wins[0].TargetVersion || loaded.ArtifactSHA256 != wins[0].ArtifactSHA256 {
		t.Fatalf("loaded marker = (%+v, %v, %v), want winner %+v", loaded, ok, err, wins[0])
	}
}

func TestConcurrentAuthorizeUpgradeBypassAuditRollback(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	instance, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = instance.Close() }()

	if _, err := backupcore.RecordRun(cfg.DataDir, storeBackupRunRecord(now, filepath.Join(cfg.DataDir, "backups", "set-1"), false), false); err != nil {
		t.Fatal(err)
	}
	ops := &updateGateOps{
		dataDir: cfg.DataDir,
		store:   instance.store,
		now:     func() time.Time { return now.Add(time.Minute) },
	}

	// Overlapping failed authorizations must leave zero usable unaudited bypass.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cctx, cancel := context.WithCancel(ctx)
			cancel()
			_, _ = ops.AuthorizeUpgradeBypass(cctx, "profile-1", "v2.0.0", "v2.0.0 set-1", "concurrent canceled")
		}()
	}
	close(start)
	wg.Wait()

	st, err := backupcore.LoadStatus(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CanStageUpdate("v2.0.0"); !errors.Is(err, backupcore.ErrUpgradeGateBlocked) {
		t.Fatalf("CanStageUpdate after concurrent canceled calls err = %v, want ErrUpgradeGateBlocked", err)
	}

	// An earlier committed bypass must survive a subsequent failed authorization attempt.
	ops2 := &updateGateOps{
		dataDir: cfg.DataDir,
		store:   instance.store,
		now:     func() time.Time { return now.Add(2 * time.Minute) },
	}
	if _, err := ops2.AuthorizeUpgradeBypass(ctx, "profile-1", "v2.0.0", "v2.0.0 set-1", "committed grant"); err != nil {
		t.Fatalf("committed AuthorizeUpgradeBypass err = %v", err)
	}
	ops3 := &updateGateOps{
		dataDir: cfg.DataDir,
		store:   instance.store,
		now:     func() time.Time { return now.Add(3 * time.Minute) },
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ops3.AuthorizeUpgradeBypass(cctx, "profile-1", "v3.0.0", "v3.0.0 set-1", "failed follow-up"); err == nil {
		t.Fatal("expected canceled follow-up AuthorizeUpgradeBypass to fail")
	}
	st, err = backupcore.LoadStatus(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.HasMatchingBypass("v2.0.0") || st.HasMatchingBypass("v3.0.0") {
		t.Fatalf("expected committed v2.0.0 bypass preserved and failed v3.0.0 rolled back: %+v", st.LastUpgradeBypass)
	}
}

func TestApplyStagedUpdatePreservesRollbackOnMarkerFailureAndRetry(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed := priv.Seed()
	dataDir := t.TempDir()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	if _, err := backupcore.RecordRun(dataDir, storeBackupRunRecord(now, filepath.Join(dataDir, "backups", "set-ok"), true), true); err != nil {
		t.Fatal(err)
	}
	newBin := []byte("new-binary-v2")
	oldBin := []byte("old-binary-v1")
	distDir := testSignedUpdateDist(t, "v2.0.0", seed, newBin)
	if _, err := StageUpdate(dataDir, distDir, pub, now); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "beamfall")
	if err := os.WriteFile(target, oldBin, 0o600); err != nil {
		t.Fatal(err)
	}
	binName := "beamfall-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	if _, err := release.ApplyBinarySwap(filepath.Join(distDir, binName), target); err != nil {
		t.Fatal(err)
	}

	// Simulate crash right after writing "applying" and swapping target (leaving .prev = oldBin).
	// Record a relative path to verify canonical path reconciliation on retry with absolute target.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relTarget, err := filepath.Rel(cwd, target)
	if err != nil {
		relTarget = target
	}
	if err := writeStagedUpdateMarker(dataDir, StagedUpdateMarker{
		Status:              "applying",
		StagedAt:            now,
		TargetVersion:       "v2.0.0",
		Channel:             "stable",
		Commit:              "deadbeef",
		ArtifactName:        binName,
		ArtifactSHA256:      release.SHA256Hex(newBin),
		AppliedBinarySHA256: release.SHA256Hex(newBin),
		SourceDistDir:       distDir,
		PayloadDir:          filepath.Join(dataDir, stagedUpdateDirName, stagedUpdatePayload),
		TargetBinaryPath:    relTarget,
		RollbackBinary:      target + ".prev",
	}); err != nil {
		t.Fatal(err)
	}

	// Retry via absolute target path must reconcile to "applied" without overwriting .prev with newBin.
	applied, err := ApplyStagedUpdate(context.Background(), dataDir, target, false, pub, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("retry ApplyStagedUpdate err = %v", err)
	}
	if applied.Status != "applied" {
		t.Fatalf("reconciled status = %+v", applied)
	}
	// #nosec G304 -- target + ".prev" is inside t.TempDir().
	gotPrev, err := os.ReadFile(target + ".prev")
	if err != nil || !bytes.Equal(gotPrev, oldBin) {
		t.Fatalf("retry overwrote original rollback binary: .prev = %q (%v), want %q", gotPrev, err, oldBin)
	}
}
