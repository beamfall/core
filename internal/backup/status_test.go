package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
