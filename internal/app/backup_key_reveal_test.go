package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/graph"
)

// consoleGraphQL posts one GraphQL request through the app's production
// handler as the seeded manager session and returns data and error messages.
func consoleGraphQL(t *testing.T, beamfall *App, body string) (map[string]json.RawMessage, []string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	var resp struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %d %s: %v", rec.Code, rec.Body.String(), err)
	}
	messages := make([]string, 0, len(resp.Errors))
	for _, e := range resp.Errors {
		messages = append(messages, e.Message)
	}
	return resp.Data, messages
}

func consoleBackup(t *testing.T, beamfall *App, dest string) string {
	t.Helper()
	data, errs := consoleGraphQL(t, beamfall, `{"query":"mutation($d: String!) { backup(dest: $d) { manifest } }","variables":{"d":`+jsonString(dest)+`}}`)
	var info struct{ Manifest string }
	if len(errs) != 0 || json.Unmarshal(data["backup"], &info) != nil || info.Manifest == "" {
		t.Fatalf("backup = %s, errors %q", data["backup"], errs)
	}
	return info.Manifest
}

func consoleInspect(t *testing.T, beamfall *App, envelope, key string) []string {
	t.Helper()
	_, errs := consoleGraphQL(t, beamfall, `{"query":"mutation($e: String!, $k: String!) { inspectBackup(envelope: $e, backupKey: $k) { compatible } }","variables":{"e":`+jsonString(envelope)+`,"k":`+jsonString(key)+`}}`)
	return errs
}

func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

type consoleBackupKey struct {
	EscrowID  string
	BackupKey string
	ExpiresAt string
	DaysLeft  int
}

func consoleBackupKeyStatus(t *testing.T, beamfall *App) (pending bool, daysLeft int) {
	t.Helper()
	data, errs := consoleGraphQL(t, beamfall, `{"query":"query { backupKeyStatus { pending daysLeft } }"}`)
	var status struct {
		Pending  bool
		DaysLeft int
	}
	if len(errs) != 0 || json.Unmarshal(data["backupKeyStatus"], &status) != nil {
		t.Fatalf("backupKeyStatus = %s, errors %q", data["backupKeyStatus"], errs)
	}
	return status.Pending, status.DaysLeft
}

func consoleRevealBackupKey(t *testing.T, beamfall *App) consoleBackupKey {
	t.Helper()
	data, errs := consoleGraphQL(t, beamfall, `{"query":"mutation { revealBackupKey { escrowID backupKey expiresAt daysLeft } }"}`)
	var revealed consoleBackupKey
	if len(errs) != 0 || json.Unmarshal(data["revealBackupKey"], &revealed) != nil || revealed.EscrowID == "" || revealed.BackupKey == "" {
		t.Fatalf("revealBackupKey errors %q", errs)
	}
	return revealed
}

func consoleAcknowledgeBackupKey(t *testing.T, beamfall *App, escrowID string) []string {
	t.Helper()
	_, errs := consoleGraphQL(t, beamfall, `{"query":"mutation($id: String!) { acknowledgeBackupKey(escrowID: $id) { pending } }","variables":{"id":`+jsonString(escrowID)+`}}`)
	return errs
}

func newBackupKeyApp(t *testing.T) (*App, config.Config) {
	t.Helper()
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(ctx, cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = beamfall.Close() })
	return beamfall, cfg
}

func openConsoleAdminSession(t *testing.T, beamfall *App) {
	t.Helper()
	if err := beamfall.store.SetSessionAdminUntil(context.Background(), "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
}

// CORE-2032: on a running server the owner reveals the backup key, confirms
// it, and the escrow file is removed. A backup taken after confirming opens
// with the revealed key. Without an Admin Session nothing is revealed.
func TestConsoleBackupKeyRevealAndAcknowledge(t *testing.T) {
	beamfall, cfg := newBackupKeyApp(t)
	escrowPath := filepath.Join(cfg.DataDir, "secrets", "backup-key-bootstrap-escrow.json")

	if _, errs := consoleGraphQL(t, beamfall, `{"query":"mutation { revealBackupKey { backupKey } }"}`); len(errs) == 0 || errs[0] != "admin session required" {
		t.Fatalf("revealBackupKey without an Admin Session: errors %q", errs)
	}
	openConsoleAdminSession(t, beamfall)

	if pending, days := consoleBackupKeyStatus(t, beamfall); !pending || days != 30 {
		t.Fatalf("status before confirming: pending=%v daysLeft=%d, want pending with 30 days", pending, days)
	}
	before := consoleBackup(t, beamfall, filepath.Join(cfg.DataDir, "backups-before"))

	revealed := consoleRevealBackupKey(t, beamfall)
	if again := consoleRevealBackupKey(t, beamfall); again != revealed {
		t.Fatal("revealing twice before confirming returned a different key")
	}
	if errs := consoleAcknowledgeBackupKey(t, beamfall, "not-the-escrow"); len(errs) == 0 || errs[0] != "the backup key changed; reveal it again" {
		t.Fatalf("acknowledge with a stale escrow ID: errors %q", errs)
	}
	if errs := consoleAcknowledgeBackupKey(t, beamfall, revealed.EscrowID); len(errs) != 0 {
		t.Fatalf("acknowledgeBackupKey errors %q", errs)
	}
	if _, err := os.Stat(escrowPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escrow file after acknowledgement: %v", err)
	}
	if pending, _ := consoleBackupKeyStatus(t, beamfall); pending {
		t.Fatal("status still pending after acknowledgement")
	}
	if _, errs := consoleGraphQL(t, beamfall, `{"query":"mutation { revealBackupKey { backupKey } }"}`); len(errs) == 0 || errs[0] != "no backup key is waiting to be saved" {
		t.Fatalf("revealBackupKey after acknowledgement: errors %q", errs)
	}

	after := consoleBackup(t, beamfall, filepath.Join(cfg.DataDir, "backups-after"))
	if errs := consoleInspect(t, beamfall, after, revealed.BackupKey); len(errs) != 0 {
		t.Fatalf("backup taken after confirming does not open with the revealed key: %q", errs)
	}
	// A set taken before confirming was sealed to the temporary bootstrap key;
	// the console says so next to the key.
	if errs := consoleInspect(t, beamfall, before, revealed.BackupKey); len(errs) == 0 {
		t.Fatal("a set taken before confirming opened with the confirmed key; update the console note")
	}
}

// CORE-2032 criterion 1: without a way to see the key, the lapsed bootstrap
// window replaced it and orphaned earlier sets. The server still replaces it,
// so the console now shows the new window and refuses to confirm the old key.
func TestConsoleBackupKeyExpiryReplacesThePendingKey(t *testing.T) {
	beamfall, cfg := newBackupKeyApp(t)
	openConsoleAdminSession(t, beamfall)
	first := consoleRevealBackupKey(t, beamfall)

	backdateBootstrapEscrow(t, cfg.DataDir)
	if pending, days := consoleBackupKeyStatus(t, beamfall); !pending || days != 30 {
		t.Fatalf("status after the window lapsed: pending=%v daysLeft=%d, want a fresh 30-day window", pending, days)
	}
	if errs := consoleAcknowledgeBackupKey(t, beamfall, first.EscrowID); len(errs) == 0 || errs[0] != "the backup key changed; reveal it again" {
		t.Fatalf("acknowledging the lapsed key: errors %q", errs)
	}
	fresh := consoleRevealBackupKey(t, beamfall)
	if fresh.EscrowID == first.EscrowID || fresh.BackupKey == first.BackupKey {
		t.Fatal("the lapsed window did not replace the pending key")
	}
}

func TestBackupKeyEscrowRefusesExpiredAcknowledgement(t *testing.T) {
	dataDir := t.TempDir()
	start := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	escrow, err := backupcore.CreateBootstrapEscrow(dataDir, start)
	if err != nil {
		t.Fatal(err)
	}
	adapter := backupKeyEscrow{dataDir: dataDir, now: func() time.Time { return escrow.ExpiresAt }}
	if err := adapter.Acknowledge(escrow.ID); !errors.Is(err, graph.ErrBackupKeyNotPending) {
		t.Fatalf("acknowledge at expiry = %v, want ErrBackupKeyNotPending", err)
	}
	if pending, _, err := adapter.Pending(); err != nil || pending {
		t.Fatalf("pending after expiry = %v, %v", pending, err)
	}
}
