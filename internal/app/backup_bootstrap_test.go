package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
)

// TestBackupBootstrapFirstBootAndConsoleRequestLifecycle pins the bootstrap
// escrow lifecycle to the PRODUCTION entry points instead of the backupcore
// functions directly: App.New must mint the first-boot escrow (the
// ensureBackupBootstrap call in app.go's New), and App.Handler must re-mint an
// expired one on a console request (app.go's backupBootstrapHTTPHandler
// wrapping). This test never calls ensureBackupBootstrap or constructs
// backupBootstrapHTTPHandler itself — deleting either production invocation
// breaks this test instead of leaving it green.
func TestBackupBootstrapFirstBootAndConsoleRequestLifecycle(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	dataDir := cfg.DataDir

	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	first, err := backupcore.ReadBootstrapEscrow(dataDir, time.Now())
	if err != nil {
		t.Fatalf("first-boot escrow unavailable: %v", err)
	}
	info, err := os.Stat(filepath.Join(dataDir, "secrets", "backup-key-bootstrap-escrow.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("bootstrap escrow mode = %o, want 600", info.Mode().Perm())
	}

	backdateBootstrapEscrow(t, dataDir)

	recorder := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/graphql", nil))
	if recorder.Code == http.StatusInternalServerError {
		t.Fatalf("console request failed the bootstrap re-mint: %d %s", recorder.Code, recorder.Body.String())
	}
	fresh, err := backupcore.ReadBootstrapEscrow(dataDir, time.Now())
	if err != nil {
		t.Fatalf("re-minted escrow unavailable: %v", err)
	}
	if fresh.ID == first.ID {
		t.Fatal("expired bootstrap escrow was not re-minted at console request")
	}

	if err := backupcore.AcknowledgeBootstrapEscrow(dataDir, fresh.ID, func(backupcore.BackupKey) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := beamfall.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatalf("restart New returned error: %v", err)
	}
	defer restarted.Close()
	if _, err := backupcore.ReadBootstrapEscrow(dataDir, time.Now()); !errors.Is(err, backupcore.ErrBootstrapEscrowNotFound) {
		t.Fatalf("acknowledged lifecycle re-created escrow across restart: %v", err)
	}
	if _, err := backupcore.LoadPublicWrapKey(dataDir); err != nil {
		t.Fatalf("acknowledged public wrap key unavailable: %v", err)
	}
}

// backdateBootstrapEscrow rewrites the on-disk bootstrap escrow file (the
// format backupcore.CreateBootstrapEscrow writes) so it is already expired,
// without waiting out the real 30-day backupcore.BootstrapEscrowLifetime. It
// round-trips every field except the two timestamps, so the escrow ID and key
// are unchanged — only its recorded age moves.
func backdateBootstrapEscrow(t *testing.T, dataDir string) {
	t.Helper()
	path := filepath.Join(dataDir, "secrets", "backup-key-bootstrap-escrow.json")
	raw, err := os.ReadFile(path) //nolint:gosec // test-owned path inside t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	var disk struct {
		Version   int       `json:"version"`
		ID        string    `json:"id"`
		Key       string    `json:"backup_key"`
		CreatedAt time.Time `json:"created_at"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	disk.CreatedAt = time.Now().UTC().Add(-(backupcore.BootstrapEscrowLifetime + time.Hour))
	disk.ExpiresAt = disk.CreatedAt.Add(backupcore.BootstrapEscrowLifetime)
	out, err := json.Marshal(disk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}
