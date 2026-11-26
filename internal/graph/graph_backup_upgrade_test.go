package graph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/restorestage"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type stubBackupOps struct {
	dataDir string
}

func (s *stubBackupOps) Status() (backupcore.Status, error) {
	return backupcore.LoadStatus(s.dataDir)
}

func (s *stubBackupOps) AcknowledgeBanner(banner backupcore.Banner) (backupcore.Status, error) {
	return backupcore.AcknowledgeBanner(s.dataDir, banner, time.Now().UTC())
}

func (s *stubBackupOps) SetUpgradeGate(gate string) (backupcore.Status, error) {
	return backupcore.SetUpgradeGate(s.dataDir, gate)
}

func (s *stubBackupOps) Drill(_ context.Context, _, _ string) (restorestage.DrillReport, error) {
	return restorestage.DrillReport{}, errors.New("not used")
}

func (s *stubBackupOps) CurrentKeyFingerprint() (string, error) { return "fp-1", nil }

type stubUpdateGateOps struct {
	dataDir   string
	staged    StagedUpdateInfo
	stageErr  error
	stageDist string
}

func (u *stubUpdateGateOps) AuthorizeUpgradeBypass(_ context.Context, actorProfileID, targetVersion, confirmation, reason string) (backupcore.Status, error) {
	return backupcore.RecordUpgradeBypass(u.dataDir, targetVersion, confirmation, reason, actorProfileID, time.Now().UTC())
}

func (u *stubUpdateGateOps) StageUpdate(_ context.Context, distDir string) (StagedUpdateInfo, error) {
	if u.stageErr != nil {
		return StagedUpdateInfo{}, u.stageErr
	}
	u.stageDist = distDir
	st, err := backupcore.LoadStatus(u.dataDir)
	if err != nil {
		return StagedUpdateInfo{}, err
	}
	if err := st.CanStageUpdate("v2.1.0"); err != nil {
		return StagedUpdateInfo{}, err
	}
	u.staged = StagedUpdateInfo{
		Staged:           true,
		Status:           "staged",
		StagedAt:         time.Now().UTC().Format(time.RFC3339),
		TargetVersion:    "v2.1.0",
		Channel:          "stable",
		ArtifactName:     "beamfall-linux-amd64",
		ArtifactSHA256:   "abcd",
		SourceDistDir:    distDir,
		BypassAuthorized: !st.UpdateGateClear() && st.HasMatchingBypass("v2.1.0"),
	}
	return u.staged, nil
}

func (u *stubUpdateGateOps) CancelStagedUpdate(_ context.Context) (bool, error) {
	if !u.staged.Staged {
		return false, nil
	}
	u.staged = StagedUpdateInfo{}
	return true, nil
}

func (u *stubUpdateGateOps) StagedUpdate(_ context.Context) (StagedUpdateInfo, error) {
	return u.staged, nil
}

func (u *stubUpdateGateOps) AcknowledgeUnverifiedUpgradeBanner(_ context.Context) (backupcore.Status, error) {
	return backupcore.AcknowledgeUpgradeBanner(u.dataDir, time.Now().UTC())
}

func TestGraphAuthorizeBypassRequiresReauthAndStageEnforcesGate(t *testing.T) {
	st := storetest.Open(t)
	now := time.Now().UTC()
	if err := st.SetAdminPassword(t.Context(), "admin-secret", now); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if _, err := backupcore.RecordRun(dataDir, backupcore.RunRecord{
		At:           now.Add(-time.Hour),
		FailureClass: "integrity-corrupt",
		Set:          "/backups/beamfall-backup-77",
	}, false); err != nil {
		t.Fatal(err)
	}

	handler := newTestHandlerWithStore(st)
	handler.SetBackupOps(&stubBackupOps{dataDir: dataDir})
	updateOps := &stubUpdateGateOps{dataDir: dataDir}
	handler.SetUpdateGateOps(updateOps)

	// Open an Admin Session on the test manager session.
	_, errs := post(t, handler,
		`{"query":"mutation V($password: String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"admin-secret"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("verifyAdminSession errs = %#v", errs)
	}

	// Staging before clearing or bypassing is refused cleanly with CONFLICT.
	_, errs = post(t, handler,
		`{"query":"mutation S($d: String!){ stageCoreUpdate(distDir: $d){ staged } }","variables":{"d":"/tmp/dist"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "backup update gate") || errs[0].Extensions.Code != ErrorCodeConflict {
		t.Fatalf("blocked soft stageCoreUpdate errs = %#v, want gate CONFLICT", errs)
	}

	// Wrong password fails step-up re-auth even inside an active Admin Session.
	_, errs = post(t, handler,
		`{"query":"mutation B($v:String!,$c:String!,$r:String!,$p:String!){ authorizeBackupUpgradeBypass(targetVersion:$v,confirmation:$c,reason:$r,password:$p){ upgradeGate } }","variables":{"v":"v2.1.0","c":"v2.1.0 beamfall-backup-77","r":"hotfix","p":"wrong-pass"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin password rejected") {
		t.Fatalf("wrong step-up password errs = %#v", errs)
	}

	// Hard gate refuses both stageCoreUpdate (CONFLICT) and elevated bypass (CONFLICT).
	_, errs = post(t, handler, `{"query":"mutation{ setBackupUpgradeGate(gate:\"hard\"){ upgradeGate } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setBackupUpgradeGate(hard) errs = %#v", errs)
	}
	_, errs = post(t, handler,
		`{"query":"mutation S($d: String!){ stageCoreUpdate(distDir: $d){ staged } }","variables":{"d":"/tmp/dist"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "backup update gate") || errs[0].Extensions.Code != ErrorCodeConflict {
		t.Fatalf("blocked hard stageCoreUpdate errs = %#v, want gate CONFLICT", errs)
	}
	_, errs = post(t, handler,
		`{"query":"mutation B($v:String!,$c:String!,$r:String!,$p:String!){ authorizeBackupUpgradeBypass(targetVersion:$v,confirmation:$c,reason:$r,password:$p){ upgradeGate } }","variables":{"v":"v2.1.0","c":"v2.1.0 beamfall-backup-77","r":"hotfix","p":"admin-secret"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "disabled while upgrade gate policy is hard") || errs[0].Extensions.Code != ErrorCodeConflict {
		t.Fatalf("hard-policy bypass errs = %#v", errs)
	}

	// Switch back to soft: typed confirmation omitting failed Set is refused.
	_, errs = post(t, handler, `{"query":"mutation{ setBackupUpgradeGate(gate:\"soft\"){ upgradeGate } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setBackupUpgradeGate(soft) errs = %#v", errs)
	}
	_, errs = post(t, handler,
		`{"query":"mutation B($v:String!,$c:String!,$r:String!,$p:String!){ authorizeBackupUpgradeBypass(targetVersion:$v,confirmation:$c,reason:$r,password:$p){ upgradeGate } }","variables":{"v":"v2.1.0","c":"v2.1.0 only","r":"hotfix","p":"admin-secret"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "beamfall-backup-77") {
		t.Fatalf("bad confirmation errs = %#v, want failed Backup Set requirement", errs)
	}

	// Valid step-up bypass succeeds, records activity, and unblocks staging for v2.1.0.
	data, errs := post(t, handler,
		`{"query":"mutation B($v:String!,$c:String!,$r:String!,$p:String!){ authorizeBackupUpgradeBypass(targetVersion:$v,confirmation:$c,reason:$r,password:$p){ failedSet lastUpgradeBypass{ targetVersion failedSet reason policy } } }","variables":{"v":"v2.1.0","c":"v2.1.0 beamfall-backup-77","r":"hotfix","p":"admin-secret"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("valid authorizeBackupUpgradeBypass errs = %#v", errs)
	}
	var statusResp BackupStatusInfo
	if err := json.Unmarshal(data["authorizeBackupUpgradeBypass"], &statusResp); err != nil {
		t.Fatal(err)
	}
	if statusResp.LastUpgradeBypass == nil || statusResp.LastUpgradeBypass.TargetVersion != "v2.1.0" || statusResp.LastUpgradeBypass.FailedSet != "beamfall-backup-77" {
		t.Fatalf("bypass status = %+v", statusResp)
	}

	data, errs = post(t, handler,
		`{"query":"mutation S($d: String!){ stageCoreUpdate(distDir: $d){ staged status targetVersion bypassAuthorized } }","variables":{"d":"/tmp/signed-dist"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("authorized stageCoreUpdate errs = %#v", errs)
	}
	var staged StagedUpdateInfo
	if err := json.Unmarshal(data["stageCoreUpdate"], &staged); err != nil {
		t.Fatal(err)
	}
	if !staged.Staged || staged.TargetVersion != "v2.1.0" || !staged.BypassAuthorized {
		t.Fatalf("staged = %+v", staged)
	}

	// stagedCoreUpdate query reflects staged state; cancelStagedCoreUpdate clears it.
	data, errs = post(t, handler, `{"query":"query{ stagedCoreUpdate{ staged targetVersion } }"}`, authorizeManager)
	if len(errs) != 0 || !strings.Contains(string(data["stagedCoreUpdate"]), "v2.1.0") {
		t.Fatalf("stagedCoreUpdate = %s, errs=%#v", data["stagedCoreUpdate"], errs)
	}
	data, errs = post(t, handler, `{"query":"mutation{ cancelStagedCoreUpdate }"}`, authorizeManager)
	if len(errs) != 0 || string(data["cancelStagedCoreUpdate"]) != "true" {
		t.Fatalf("cancelStagedCoreUpdate = %s, errs=%#v", data["cancelStagedCoreUpdate"], errs)
	}

	// Unverified upgrade banner surfaces in backupStatus and dismisses via acknowledgeBackupBanner.
	if err := backupcore.SeedBootBinaryVersion(dataDir, "v2.0.0"); err != nil {
		t.Fatal(err)
	}
	bootRes, err := backupcore.EvaluateBootUpgrade(dataDir, "v2.1.0", false, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := backupcore.CommitBootUpgrade(dataDir, bootRes, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	data, errs = post(t, handler, `{"query":"query{ backupStatus{ unverifiedUpgradeBanner lastBinaryVersion lastUnverifiedUpgrade{ fromVersion toVersion bypassed } } }"}`, authorizeManager)
	if len(errs) != 0 || !strings.Contains(string(data["backupStatus"]), `"unverifiedUpgradeBanner":true`) {
		t.Fatalf("backupStatus after boot upgrade = %s, errs=%#v", data["backupStatus"], errs)
	}
	data, errs = post(t, handler, `{"query":"mutation{ acknowledgeBackupBanner(banner:\"unverified-upgrade\"){ unverifiedUpgradeBanner } }"}`, authorizeManager)
	if len(errs) != 0 || !strings.Contains(string(data["acknowledgeBackupBanner"]), `"unverifiedUpgradeBanner":false`) {
		t.Fatalf("acknowledgeBackupBanner = %s, errs=%#v", data["acknowledgeBackupBanner"], errs)
	}

	// Activity log captured the bypass, stage, cancel, and banner ack.
	entries, err := st.ActivityEntriesAfter(t.Context(), 0, []string{store.ActivityConfigUpdate}, 0)
	if err != nil {
		t.Fatal(err)
	}
	seenBypass := false
	for _, entry := range entries {
		if entry.TargetType == "backup" && entry.TargetID == "upgrade-bypass:v2.1.0" {
			seenBypass = true
		}
	}
	if !seenBypass {
		t.Fatalf("expected upgrade-bypass activity entry in %+v", entries)
	}

	// Agent and Diagnostics API tokens are denied stagedCoreUpdate and bypass/staging mutations.
	for _, access := range []store.APITokenAccess{store.APITokenAccessAgent, store.APITokenAccessDiagnostics} {
		_, token, err := st.CreateAPITokenWithAccess(t.Context(), string(access), access, now)
		if err != nil {
			t.Fatal(err)
		}
		_, errs = post(t, handler, `{"query":"query{ stagedCoreUpdate{ staged sourceDistDir rollbackBinary } }"}`, func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+token)
		})
		if len(errs) == 0 || errs[0].Message != "admin session required" {
			t.Fatalf("%s token stagedCoreUpdate errs = %#v, want admin session required", access, errs)
		}
	}
	// Same-second newer failure marks server-computed lastUpgradeBypass.valid = false.
	sameSecDir := t.TempDir()
	baseSec := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	if _, err := backupcore.RecordRun(sameSecDir, backupcore.RunRecord{
		At:           baseSec.Add(-time.Minute),
		FailureClass: "destination-full",
		Set:          "beamfall-set-1",
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := backupcore.RecordUpgradeBypass(sameSecDir, "v2.1.0", "v2.1.0 beamfall-set-1", "urgent", "profile-1", baseSec.Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := backupcore.RecordRun(sameSecDir, backupcore.RunRecord{
		At:           baseSec.Add(900 * time.Millisecond),
		FailureClass: "destination-full",
		Set:          "beamfall-set-2",
	}, false); err != nil {
		t.Fatal(err)
	}
	sameSecHandler := newTestHandlerWithStore(st)
	sameSecHandler.SetBackupOps(&stubBackupOps{dataDir: sameSecDir})
	sameSecHandler.SetUpdateGateOps(&stubUpdateGateOps{dataDir: sameSecDir})
	data, errs = post(t, sameSecHandler, `{"query":"query{ backupStatus{ lastUpgradeBypass{ at valid } } }"}`, authorizeManager)
	if len(errs) != 0 || !strings.Contains(string(data["backupStatus"]), `"valid":false`) {
		t.Fatalf("same-second superseded bypass backupStatus = %s, errs=%#v, want valid:false", data["backupStatus"], errs)
	}
	_, token, err := st.CreateAPITokenWithAccess(t.Context(), "agent-mut", store.APITokenAccessAgent, now)
	if err != nil {
		t.Fatal(err)
	}
	_, errs = post(t, handler, `{"query":"mutation{ cancelStagedCoreUpdate }"}`, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	})
	if len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("agent token cancelStagedCoreUpdate errs = %#v, want admin session required", errs)
	}
}
