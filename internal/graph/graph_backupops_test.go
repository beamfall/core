package graph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/restorestage"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeBackupOps keeps real status-file state in a temp data directory and
// stands in for the drill, recording the key it was handed.
type fakeBackupOps struct {
	dataDir string
	gotKey  string
	report  restorestage.DrillReport
	err     error
	key     string
}

func (f *fakeBackupOps) Status() (backupcore.Status, error) { return backupcore.LoadStatus(f.dataDir) }

func (f *fakeBackupOps) AcknowledgeBanner(b backupcore.Banner) (backupcore.Status, error) {
	return backupcore.AcknowledgeBanner(f.dataDir, b, time.Now())
}

func (f *fakeBackupOps) SetUpgradeGate(gate string) (backupcore.Status, error) {
	return backupcore.SetUpgradeGate(f.dataDir, gate)
}

func (f *fakeBackupOps) RecordRun(run backupcore.RunRecord, integrity bool) (backupcore.Status, error) {
	return backupcore.RecordRun(f.dataDir, run, integrity)
}

func (f *fakeBackupOps) Drill(_ context.Context, _, backupKey string) (restorestage.DrillReport, error) {
	f.gotKey = backupKey
	return f.report, f.err
}

func (f *fakeBackupOps) CurrentKeyFingerprint() (string, error) { return f.key, nil }

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
		gateReq  = `{"query":"query { backupStatus { updateGateClear lastIntegrityPass { set } } }"}`
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

	// The update gate reads a recorded integrity pass, never the banners.
	data, errs = postLibraryTrash(t, handler, gateReq)
	if info := decodeBackupStatus(t, data["backupStatus"], errs); info.UpdateGateClear || info.LastIntegrityPass != nil {
		t.Fatalf("gate clear with only failures on record: %+v", info)
	}
	if _, err := backupcore.RecordRun(dataDir, backupcore.RunRecord{At: time.Now(), Set: "/srv/backups/set-2", IntegrityVerified: true}, false); err != nil {
		t.Fatal(err)
	}
	data, errs = postLibraryTrash(t, handler, gateReq)
	if info := decodeBackupStatus(t, data["backupStatus"], errs); !info.UpdateGateClear || info.LastIntegrityPass == nil || info.LastIntegrityPass.Set != "set-2" {
		t.Fatalf("gate after a verified backup: %+v", info)
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

	// CORE-3516 #18: completed failed drills return verified=false and reason
	// without being swallowed into a GraphQL error.
	fake.report = restorestage.DrillReport{Verified: false, Set: "set-1", Reason: "restore-assertions-failed", Duration: time.Second}
	data, errs = postLibraryTrash(t, handler, drillReq)
	var failedDrill BackupDrillInfo
	if len(errs) != 0 || json.Unmarshal(data["verifyBackupNow"], &failedDrill) != nil || failedDrill.Verified || failedDrill.Reason != "restore-assertions-failed" {
		t.Fatalf("failed verifyBackupNow = %s, errors %q", data["verifyBackupNow"], errs)
	}
}

// CORE-3516 #15 + follow-up: unverified manual backup leaves trusted recovery
// point, integrity banner, and update gate untouched; confined verifyBackup
// pass clears them and confined verifyBackup failure records an integrity incident.
func TestManualBackupAndVerifyClearUpdateGate(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	dataDir := t.TempDir()
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(dataDir))
	if _, err := backupcore.CreateBootstrapEscrow(dataDir, time.Now().UTC()); err != nil {
		t.Fatalf("CreateBootstrapEscrow: %v", err)
	}
	handler.SetBackupOps(&fakeBackupOps{dataDir: dataDir})
	if _, err := backupcore.RecordRun(dataDir, backupcore.RunRecord{At: time.Now().UTC().Add(-time.Minute), FailureClass: "integrity", Set: "prior-bad"}, true); err != nil {
		t.Fatal(err)
	}
	root, err := st.AddPathPickerRoot(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("AddPathPickerRoot: %v", err)
	}
	destJSON, _ := json.Marshal(root.Path)
	createBody := `{"query":"mutation($d: String!) { backup(dest: $d) { manifest } }","variables":{"d":` + string(destJSON) + `}}`
	data, errs := postLibraryTrash(t, handler, createBody)
	if len(errs) != 0 {
		t.Fatalf("backup errors = %q", errs)
	}
	var created struct {
		Manifest string `json:"manifest"`
	}
	if err := json.Unmarshal(data["backup"], &created); err != nil || created.Manifest == "" {
		t.Fatalf("backup payload = %s, %v", data["backup"], err)
	}
	if filepath.Base(created.Manifest) != "set.bbk" {
		t.Fatalf("encrypted manual backup manifest = %q, want set.bbk", created.Manifest)
	}
	statusBody := `{"query":"query { backupStatus { updateGateClear integrityFailedBanner lastSuccess { set } lastIntegrityPass { set } lastIntegrityFailure { failureClass set } } }"}`
	statusData, errs := postLibraryTrash(t, handler, statusBody)
	if info := decodeBackupStatus(t, statusData["backupStatus"], errs); info.UpdateGateClear || !info.IntegrityFailedBanner || info.LastSuccess != nil || info.LastIntegrityPass != nil {
		t.Fatalf("after unverified manual backup: %+v", info)
	}
	manifestJSON, _ := json.Marshal(created.Manifest)
	verifyBody := `{"query":"mutation($m: String!) { verifyBackup(manifest: $m) { ok error } }","variables":{"m":` + string(manifestJSON) + `}}`
	if _, errs := postLibraryTrash(t, handler, verifyBody); len(errs) != 0 {
		t.Fatalf("verifyBackup errors = %q", errs)
	}
	statusData, errs = postLibraryTrash(t, handler, statusBody)
	if info := decodeBackupStatus(t, statusData["backupStatus"], errs); !info.UpdateGateClear || info.IntegrityFailedBanner || info.LastSuccess == nil || info.LastIntegrityPass == nil || info.LastIntegrityPass.Set == "" {
		t.Fatalf("after verified manual backup: %+v", info)
	}
	// Wait past the prior pass timestamp, tamper with set.bbk, and verify again:
	// confined failure must record LastIntegrityFailure and close the gate.
	time.Sleep(2 * time.Millisecond)
	if err := os.WriteFile(created.Manifest, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	vData, errs := postLibraryTrash(t, handler, verifyBody)
	var vRes VerifyBackupInfo
	if len(errs) != 0 || json.Unmarshal(vData["verifyBackup"], &vRes) != nil || vRes.OK {
		t.Fatalf("tampered verifyBackup = %s, errs=%q", vData["verifyBackup"], errs)
	}
	statusData, errs = postLibraryTrash(t, handler, statusBody)
	if info := decodeBackupStatus(t, statusData["backupStatus"], errs); info.UpdateGateClear || !info.IntegrityFailedBanner || info.LastIntegrityFailure == nil || info.LastIntegrityFailure.FailureClass != "integrity" {
		t.Fatalf("after failed manual verifyBackup: %+v", info)
	}
	_ = store.IsConfinedBackupIntegrityError // compile-time check of store helper
}

// BKUP-14 key-to-archive mapping: a set sealed to the current wrap key is
// current, one sealed to an earlier key is archived, unknown stays blank.
func TestBackupKeyState(t *testing.T) {
	for _, tc := range []struct{ set, current, want string }{
		{"fp-a", "fp-a", "current"},
		{"fp-z", "fp-a", "archived"},
		{"", "fp-a", ""},
		{"fp-a", "", ""},
	} {
		if got := backupKeyState(tc.set, tc.current); got != tc.want {
			t.Errorf("backupKeyState(%q, %q) = %q, want %q", tc.set, tc.current, got, tc.want)
		}
	}
}
