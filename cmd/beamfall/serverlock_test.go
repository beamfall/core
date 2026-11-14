package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/config"
)

// holdServerLockForTest holds dataDir's server lock until the test ends, the
// way a running `serve` does.
func holdServerLockForTest(t *testing.T, dataDir string) {
	t.Helper()
	release, err := acquireServerLock(dataDir)
	if err != nil {
		t.Fatalf("acquire server lock: %v", err)
	}
	t.Cleanup(release)
}

func TestRefuseServerLockRefusesHeldLock(t *testing.T) {
	dataDir := t.TempDir()
	holdServerLockForTest(t, dataDir)
	err := refuseServerLock(dataDir)
	if err == nil || !strings.Contains(err.Error(), "server appears to be running") {
		t.Fatalf("refuseServerLock = %v, want server running refusal", err)
	}
}

func TestRefuseServerLockIgnoresLeftoverUnlockedFile(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, serverLockName), []byte("crashed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseServerLock(dataDir); err != nil {
		t.Fatalf("leftover unlocked server.lock refused: %v", err)
	}
	// The probe must not keep the lock.
	holdServerLockForTest(t, dataDir)
}

func TestAcquireServerLockCreatesPrivateFile(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "fresh")
	holdServerLockForTest(t, dataDir)
	info, err := os.Stat(filepath.Join(dataDir, serverLockName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("server.lock mode = %v, want no group or other access", perm)
	}
}

// TestServeHoldsServerLock boots the real server the way `serve` does and
// checks that offline commands refuse while it runs and work once it stops.
func TestServeHoldsServerLock(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "LockE2E"
	cfg.Discovery.Enabled = false
	cfg.SeedProfile = "conformance"
	cfg.AllowConformanceSeed = true

	beamfall, release, err := startServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("startServer: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- beamfall.Run(runCtx) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run did not stop within 10s")
		}
		release()
	}
	t.Cleanup(stop)

	addrCtx, addrCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer addrCancel()
	if beamfall.Addr(addrCtx) == "" {
		t.Fatal("server did not bind a listener within 5s")
	}

	doc := `{"version":1,"backgroundWorkers":7,"plugins":[]}`
	if _, err := applyOffline(t, cfg.DataDir, doc); err == nil || !strings.Contains(err.Error(), "server appears to be running") {
		t.Fatalf("offline apply-config against a running server = %v, want refusal", err)
	}
	if _, err := lockServeDataDir(cfg.DataDir); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second serve lock = %v, want already running refusal", err)
	}

	stop()
	if _, err := applyOffline(t, cfg.DataDir, doc); err != nil {
		t.Fatalf("offline apply-config after the server stopped: %v", err)
	}
}
