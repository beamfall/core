package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnverifiedSuccessDoesNotClearIntegrityFailedBanner(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 11, 25, 2, 0, 0, 0, time.UTC)
	if _, err := RecordRun(dir, RunRecord{At: t0, FailureClass: "integrity", Set: "beamfall-bad"}, true); err != nil {
		t.Fatal(err)
	}
	s, err := RecordRun(dir, RunRecord{At: t0.Add(time.Hour), Set: "beamfall-unverified"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !s.BannerActive(BannerIntegrityFailed) {
		t.Fatalf("unverified run cleared integrity banner: %+v", s)
	}
	s, err = RecordRun(dir, RunRecord{At: t0.Add(2 * time.Hour), Set: "beamfall-verified", IntegrityVerified: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.BannerActive(BannerIntegrityFailed) || !s.UpdateGateClear() {
		t.Fatalf("verified run did not clear integrity banner/gate: %+v", s)
	}
}

func TestStatusDefaultsAndBannerLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	// ADR-0192 §6: upgrades never hard-block by default.
	if s.EffectiveUpgradeGate() != UpgradeGateSoft {
		t.Fatalf("default upgrade gate = %q, want soft", s.EffectiveUpgradeGate())
	}
	if s.BannerActive(BannerBackupFailed) || s.BannerActive(BannerIntegrityFailed) {
		t.Fatal("fresh status shows a banner")
	}

	t0 := time.Date(2026, 11, 25, 2, 0, 0, 0, time.UTC)
	if _, err := RecordRun(dir, RunRecord{At: t0, FailureClass: "destination-full"}, false); err != nil {
		t.Fatal(err)
	}
	s, _ = LoadStatus(dir)
	if !s.BannerActive(BannerBackupFailed) || s.BannerActive(BannerIntegrityFailed) {
		t.Fatalf("after failure: %+v", s)
	}
	// Acknowledged until the next failure.
	if _, err := AcknowledgeBanner(dir, BannerBackupFailed, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s, _ = LoadStatus(dir)
	if s.BannerActive(BannerBackupFailed) {
		t.Fatal("acknowledged banner still active")
	}
	if _, err := RecordRun(dir, RunRecord{At: t0.Add(24 * time.Hour), FailureClass: "unwritable"}, false); err != nil {
		t.Fatal(err)
	}
	s, _ = LoadStatus(dir)
	if !s.BannerActive(BannerBackupFailed) {
		t.Fatal("a new failure after the acknowledgement did not re-raise the banner")
	}
	// Superseded by a later verified backup.
	if _, err := RecordRun(dir, RunRecord{At: t0.Add(48 * time.Hour), Set: "/x/beamfall-1"}, false); err != nil {
		t.Fatal(err)
	}
	s, _ = LoadStatus(dir)
	if s.BannerActive(BannerBackupFailed) || s.LastSuccess.Set != "beamfall-1" {
		t.Fatalf("after success: %+v", s)
	}

	if _, err := AcknowledgeBanner(dir, "other", t0); err != ErrUnknownBanner {
		t.Fatalf("unknown banner err = %v", err)
	}
	if _, err := SetUpgradeGate(dir, "never"); err != ErrUnknownUpgradeGate {
		t.Fatalf("unknown gate err = %v", err)
	}
	if s, err = SetUpgradeGate(dir, UpgradeGateHard); err != nil || s.EffectiveUpgradeGate() != UpgradeGateHard {
		t.Fatalf("hard gate = %+v, %v", s, err)
	}
	info, err := os.Stat(filepath.Join(dir, statusFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("status file mode = %v, %v", info, err)
	}
}

// The update gate reads a recorded integrity pass, never the banners: an
// acknowledged failure still holds the gate shut (CORE-0778).
func TestUpdateGateClearNeedsRecordedIntegrityPass(t *testing.T) {
	// A data directory that does not exist yet is created owner-only.
	dir := filepath.Join(t.TempDir(), "fresh", "data")
	t0 := time.Date(2026, 11, 25, 2, 0, 0, 0, time.UTC)
	// A success without a recorded integrity pass is not a verified point.
	s, err := RecordRun(dir, RunRecord{At: t0, Set: "beamfall-0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.UpdateGateClear() || s.LastIntegrityPass != nil {
		t.Fatalf("unverified success cleared the gate: %+v", s)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode = %v, %v", info, err)
	}
	s, _ = RecordRun(dir, RunRecord{At: t0.Add(time.Hour), Set: "beamfall-1", IntegrityVerified: true}, false)
	if !s.UpdateGateClear() || s.LastIntegrityPass.Set != "beamfall-1" {
		t.Fatalf("verified success did not clear the gate: %+v", s)
	}
	s, _ = RecordRun(dir, RunRecord{At: t0.Add(25 * time.Hour), FailureClass: "integrity-failure"}, true)
	if s.UpdateGateClear() {
		t.Fatal("a later integrity failure left the gate clear")
	}
	s, _ = AcknowledgeBanner(dir, BannerIntegrityFailed, t0.Add(26*time.Hour))
	if s.BannerActive(BannerIntegrityFailed) || s.UpdateGateClear() {
		t.Fatalf("acknowledging the banner cleared the gate: %+v", s)
	}
	s, _ = RecordRun(dir, RunRecord{At: t0.Add(49 * time.Hour), Set: "beamfall-2", IntegrityVerified: true}, false)
	if !s.UpdateGateClear() {
		t.Fatal("a newer verified backup did not clear the gate")
	}
	s, _ = RecordRun(dir, RunRecord{At: t0.Add(73 * time.Hour), FailureClass: "destination-full"}, false)
	if s.UpdateGateClear() {
		t.Fatal("a later backup failure left the gate clear")
	}
}
