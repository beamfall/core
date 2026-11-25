package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/restorestage"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeBackupOps keeps real status-file state in a temp data directory and
// stands in for the drill, recording the key it was handed.
type fakeBackupOps struct {
	dataDir string
	gotKey  string
	report  restorestage.DrillReport
	err     error
}

func (f *fakeBackupOps) Status() (backupcore.Status, error) { return backupcore.LoadStatus(f.dataDir) }

func (f *fakeBackupOps) AcknowledgeBanner(b backupcore.Banner) (backupcore.Status, error) {
	return backupcore.AcknowledgeBanner(f.dataDir, b, time.Now())
}

func (f *fakeBackupOps) SetUpgradeGate(gate string) (backupcore.Status, error) {
	return backupcore.SetUpgradeGate(f.dataDir, gate)
}

func (f *fakeBackupOps) Drill(_ context.Context, _, backupKey string) (restorestage.DrillReport, error) {
	f.gotKey = backupKey
	return f.report, f.err
}

func decodeBackupStatus(t *testing.T, raw json.RawMessage, errs []string) BackupStatusInfo {
	t.Helper()
	var info BackupStatusInfo
	if len(errs) != 0 || json.Unmarshal(raw, &info) != nil {
		t.Fatalf("backup status = %s, errors %q", raw, errs)
	}
	return info
}

// The backup operations fields are Admin-Session gated; the banners follow
// seeded failure fixtures, the update gate defaults to soft, and the drill
// reports its outcome without echoing the key.
func TestBackupOpsFieldsBannersGateAndDrill(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	const (
		status   = `{"query":"query { backupStatus { backupFailedBanner integrityFailedBanner upgradeGate lastFailure { failureClass set } lastIntegrityFailure { failureClass } lastDrill { verified set } } }"}`
		ack      = `{"query":"mutation($b: String!) { acknowledgeBackupBanner(banner: $b) { backupFailedBanner integrityFailedBanner upgradeGate } }","variables":{"b":"%s"}}`
		gate     = `{"query":"mutation($g: String!) { setBackupUpgradeGate(gate: $g) { upgradeGate } }","variables":{"g":"%s"}}`
		drillReq = `{"query":"mutation($e: String!, $k: String!) { verifyBackupNow(envelope: $e, backupKey: $k) { verified set reason databases libraries } }","variables":{"e":"/backups/set-1/backup.bfenc","k":"placeholder-drill-key"}}`
	)
	fill := func(tmpl, v string) string { return strings.Replace(tmpl, "%s", v, 1) }
	for _, body := range []string{status, fill(ack, "backup-failed"), fill(gate, "hard"), drillReq} {
		if _, errs := postLibraryTrash(t, handler, body); len(errs) == 0 || errs[0] != "admin session required" {
			t.Fatalf("%s without an Admin Session: errors %q", body, errs)
		}
	}
	openManagerAdminSession(t, st)

	// Unwired: defaults, with the soft gate.
	data, errs := postLibraryTrash(t, handler, status)
	if info := decodeBackupStatus(t, data["backupStatus"], errs); info.UpgradeGate != backupcore.UpgradeGateSoft || info.BackupFailedBanner || info.IntegrityFailedBanner {
		t.Fatalf("unwired backupStatus = %+v", info)
	}

	dataDir := t.TempDir()
	fake := &fakeBackupOps{dataDir: dataDir}
	handler.SetBackupOps(fake)

	// Seeded failure fixtures: a failed run and a failed integrity check.
	failedAt := time.Now().Add(-time.Hour)
	if _, err := backupcore.RecordRun(dataDir, backupcore.RunRecord{At: failedAt, FailureClass: "destination-full", Set: "/srv/backups/set-1"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := backupcore.RecordRun(dataDir, backupcore.RunRecord{At: failedAt, FailureClass: "integrity"}, true); err != nil {
		t.Fatal(err)
	}
	data, errs = postLibraryTrash(t, handler, status)
	info := decodeBackupStatus(t, data["backupStatus"], errs)
	if !info.BackupFailedBanner || !info.IntegrityFailedBanner || info.UpgradeGate != backupcore.UpgradeGateSoft {
		t.Fatalf("seeded backupStatus = %+v, want both banners and the soft gate", info)
	}
	if info.LastFailure == nil || info.LastFailure.FailureClass != "destination-full" || info.LastFailure.Set != "set-1" {
		t.Fatalf("lastFailure = %+v", info.LastFailure)
	}
	if strings.Contains(string(data["backupStatus"]), "/srv/") {
		t.Fatalf("backupStatus leaked a path: %s", data["backupStatus"])
	}

	// Acknowledging one banner leaves the other.
	data, errs = postLibraryTrash(t, handler, fill(ack, "backup-failed"))
	if info := decodeBackupStatus(t, data["acknowledgeBackupBanner"], errs); info.BackupFailedBanner || !info.IntegrityFailedBanner {
		t.Fatalf("after acknowledging backup-failed = %+v", info)
	}
	if _, errs := postLibraryTrash(t, handler, fill(ack, "everything")); len(errs) == 0 || !strings.Contains(errs[0], "banner must be") {
		t.Fatalf("unknown banner errors = %q", errs)
	}

	// The update gate: hard is opt-in, junk is refused.
	data, errs = postLibraryTrash(t, handler, fill(gate, "hard"))
	if info := decodeBackupStatus(t, data["setBackupUpgradeGate"], errs); info.UpgradeGate != backupcore.UpgradeGateHard {
		t.Fatalf("after hard = %+v", info)
	}
	if _, errs := postLibraryTrash(t, handler, fill(gate, "off")); len(errs) == 0 || !strings.Contains(errs[0], "gate must be") {
		t.Fatalf("unknown gate errors = %q", errs)
	}

	// The drill: a refusal is returned as-is; a verified drill reports its counts.
	fake.err = restorestage.ErrDrillRunning
	if _, errs := postLibraryTrash(t, handler, drillReq); len(errs) == 0 || errs[0] != restorestage.ErrDrillRunning.Error() {
		t.Fatalf("refused drill errors = %q", errs)
	}
	fake.err = nil
	fake.report = restorestage.DrillReport{Verified: true, Set: "set-1", Databases: 2, Libraries: 3, Duration: time.Second}
	data, errs = postLibraryTrash(t, handler, drillReq)
	var drill BackupDrillInfo
	if len(errs) != 0 || json.Unmarshal(data["verifyBackupNow"], &drill) != nil || !drill.Verified || drill.Set != "set-1" || drill.Libraries != 3 {
		t.Fatalf("verifyBackupNow = %s, errors %q", data["verifyBackupNow"], errs)
	}
	if fake.gotKey != "placeholder-drill-key" || strings.Contains(string(data["verifyBackupNow"]), "placeholder-drill-key") {
		t.Fatalf("drill key handling: got %q, response %s", fake.gotKey, data["verifyBackupNow"])
	}
}
