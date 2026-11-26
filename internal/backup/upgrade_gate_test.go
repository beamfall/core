package backup

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestUpgradeConfirmationAndBypassLifecycle(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

	if _, err := RecordRun(dir, RunRecord{
		At:           t0,
		FailureClass: "integrity-corrupt",
		Set:          filepath.Join(dir, "backups", "beamfall-backup-42"),
	}, false); err != nil {
		t.Fatal(err)
	}

	// Hard gate refuses bypass cleanly.
	if _, err := SetUpgradeGate(dir, UpgradeGateHard); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordUpgradeBypass(dir, "v1.4.0", "v1.4.0 beamfall-backup-42", "emergency patch", "profile-owner", t0.Add(time.Minute)); !errors.Is(err, ErrUpgradeGateHardBypassDenied) {
		t.Fatalf("hard RecordUpgradeBypass err = %v, want ErrUpgradeGateHardBypassDenied", err)
	}
	if st, err := LoadStatus(dir); err != nil {
		t.Fatal(err)
	} else if err := st.CanStageUpdate("v1.4.0"); !errors.Is(err, ErrUpgradeGateBlocked) {
		t.Fatalf("hard CanStageUpdate err = %v, want ErrUpgradeGateBlocked", err)
	}

	// Switch to soft policy: wrong confirmations & empty reasons fail.
	if _, err := SetUpgradeGate(dir, UpgradeGateSoft); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordUpgradeBypass(dir, "v1.4.0", "v1.4.0", "reason", "profile-owner", t0.Add(time.Minute)); !errors.Is(err, ErrUpgradeConfirmationInvalid) {
		t.Fatalf("missing failed set confirmation err = %v, want ErrUpgradeConfirmationInvalid", err)
	}
	if _, err := RecordUpgradeBypass(dir, "v1.4.0", "beamfall-backup-42", "reason", "profile-owner", t0.Add(time.Minute)); !errors.Is(err, ErrUpgradeConfirmationInvalid) {
		t.Fatalf("missing target version confirmation err = %v, want ErrUpgradeConfirmationInvalid", err)
	}
	if _, err := RecordUpgradeBypass(dir, "v1.4.0", "v1.4.0 beamfall-backup-42", "   ", "profile-owner", t0.Add(time.Minute)); !errors.Is(err, ErrUpgradeBypassReasonRequired) {
		t.Fatalf("empty reason err = %v, want ErrUpgradeBypassReasonRequired", err)
	}

	// Valid bypass authorizes matching version only.
	st, err := RecordUpgradeBypass(dir, "v1.4.0", "upgrade v1.4.0 despite beamfall-backup-42", "hardware swap", "profile-owner", t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CanStageUpdate("v1.4.0"); err != nil {
		t.Fatalf("soft matching bypass CanStageUpdate err = %v", err)
	}
	if err := st.CanStageUpdate("v1.5.0"); !errors.Is(err, ErrUpgradeGateBlocked) {
		t.Fatalf("mismatched version CanStageUpdate err = %v, want ErrUpgradeGateBlocked", err)
	}

	// A newer failure invalidates the prior bypass.
	if _, err := RecordRun(dir, RunRecord{
		At:           t0.Add(3 * time.Minute),
		FailureClass: "destination-full",
		Set:          filepath.Join(dir, "backups", "beamfall-backup-43"),
	}, false); err != nil {
		t.Fatal(err)
	}
	if st, err = LoadStatus(dir); err != nil {
		t.Fatal(err)
	} else if err := st.CanStageUpdate("v1.4.0"); !errors.Is(err, ErrUpgradeGateBlocked) {
		t.Fatalf("stale bypass after newer failure err = %v, want ErrUpgradeGateBlocked", err)
	}
}

func TestEvaluateBootUpgradeTransitions(t *testing.T) {
	t0 := time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC)

	t.Run("hard blocks before version advance unless overridden or verified", func(t *testing.T) {
		dir := t.TempDir()
		if err := SeedBootBinaryVersion(dir, "v1.0.0"); err != nil {
			t.Fatal(err)
		}
		if _, err := SetUpgradeGate(dir, UpgradeGateHard); err != nil {
			t.Fatal(err)
		}
		if _, err := EvaluateBootUpgrade(dir, "v1.1.0", false, t0); !errors.Is(err, ErrUpgradeGateBlocked) {
			t.Fatalf("hard EvaluateBootUpgrade err = %v, want ErrUpgradeGateBlocked", err)
		}
		st, err := LoadStatus(dir)
		if err != nil {
			t.Fatal(err)
		}
		if st.LastBinaryVersion != "v1.0.0" {
			t.Fatalf("LastBinaryVersion = %q after blocked upgrade, want v1.0.0", st.LastBinaryVersion)
		}

		// Explicit recovery override stays non-mutating on EvaluateBootUpgrade until CommitBootUpgrade.
		res, err := EvaluateBootUpgrade(dir, "v1.1.0", true, t0.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if !res.Changed || !res.Override || res.Verified {
			t.Fatalf("override result = %+v", res)
		}
		st, err = LoadStatus(dir)
		if err != nil {
			t.Fatal(err)
		}
		if st.LastBinaryVersion != "v1.0.0" {
			t.Fatalf("LastBinaryVersion = %q before CommitBootUpgrade, want v1.0.0", st.LastBinaryVersion)
		}
		if err := CommitBootUpgrade(dir, res, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		st, err = LoadStatus(dir)
		if err != nil {
			t.Fatal(err)
		}
		if st.LastBinaryVersion != "v1.1.0" || !st.UnverifiedUpgradeBannerActive() {
			t.Fatalf("unexpected post-override status: %+v", st)
		}
	})

	t.Run("soft consumes matching bypass once and allows banner ack", func(t *testing.T) {
		dir := t.TempDir()
		if err := SeedBootBinaryVersion(dir, "v1.0.0"); err != nil {
			t.Fatal(err)
		}
		if _, err := RecordUpgradeBypass(dir, "v1.2.0", "v1.2.0", "emergency fix", "profile-1", t0); err != nil {
			t.Fatal(err)
		}
		res, err := EvaluateBootUpgrade(dir, "v1.2.0", false, t0.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if !res.Changed || !res.Bypassed || res.FromVersion != "v1.0.0" || res.ToVersion != "v1.2.0" {
			t.Fatalf("bypassed result = %+v", res)
		}
		if err := CommitBootUpgrade(dir, res, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		st, err := LoadStatus(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !st.UnverifiedUpgradeBannerActive() || st.LastUpgradeBypass.AppliedAt == nil {
			t.Fatalf("expected active banner and AppliedAt on %+v", st)
		}
		if st.HasMatchingBypass("v1.2.0") {
			t.Fatal("consumed bypass still matched after boot")
		}
		st, err = AcknowledgeUpgradeBanner(dir, t0.Add(2*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if st.UnverifiedUpgradeBannerActive() {
			t.Fatal("unverified upgrade banner remained active after ack")
		}
	})

	t.Run("verified backup clears upgrade without unverified banner", func(t *testing.T) {
		dir := t.TempDir()
		if err := SeedBootBinaryVersion(dir, "v1.0.0"); err != nil {
			t.Fatal(err)
		}
		if _, err := SetUpgradeGate(dir, UpgradeGateHard); err != nil {
			t.Fatal(err)
		}
		if _, err := RecordRun(dir, RunRecord{At: t0, Set: filepath.Join(dir, "backups", "beamfall-backup-1"), IntegrityVerified: true}, true); err != nil {
			t.Fatal(err)
		}
		res, err := EvaluateBootUpgrade(dir, "v1.3.0", false, t0.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if !res.Changed || !res.Verified || res.Bypassed {
			t.Fatalf("verified result = %+v", res)
		}
		if err := CommitBootUpgrade(dir, res, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		st, err := LoadStatus(dir)
		if err != nil {
			t.Fatal(err)
		}
		if st.UnverifiedUpgradeBannerActive() || st.LastBinaryVersion != "v1.3.0" {
			t.Fatalf("unexpected verified post-boot status: %+v", st)
		}
	})
}
