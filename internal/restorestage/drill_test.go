package restorestage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
)

// treeDigest hashes every regular file under dir (path and content), so a
// test can prove a refused drill left the live data directory byte-identical.
func treeDigest(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 G122 -- test-owned temp dir
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func diffDigest(before, after map[string]string) []string {
	var diff []string
	for k, v := range before {
		if after[k] != v {
			diff = append(diff, k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			diff = append(diff, k)
		}
	}
	sort.Strings(diff)
	return diff
}

// A drill on a seeded box restores the latest set into scratch, boots it and
// reports restore-verified, leaving no scratch behind and recording the
// outcome for the console.
func TestDrillOnSeededBoxReportsRestoreVerified(t *testing.T) {
	f := newStageFixture(t)
	stager := New(f.dataDir, f.store)
	report, err := stager.Drill(context.Background(), f.envelope, f.key, DrillGuards{
		FreeBytes: func(string) (uint64, bool) { return 1 << 40, true },
		Idle:      func() bool { return true },
	})
	if err != nil {
		t.Fatalf("drill: %v", err)
	}
	if !report.Verified || report.Reason != "" {
		t.Fatalf("report = %+v, want restore-verified", report)
	}
	// Layer-1: every catalog and events table matched the snapshot by row
	// count and row checksum.
	if report.Databases < 2 || report.Libraries == 0 || report.BackupCreatedAt == "" || report.Tables == 0 {
		t.Fatalf("report = %+v, want the seeded catalog read back", report)
	}
	if report.Set != filepath.Base(filepath.Dir(f.envelope)) {
		t.Fatalf("report set = %q", report.Set)
	}
	assertNoScratch(t, f.dataDir)
	if _, staged, err := Pending(f.dataDir); err != nil || staged {
		t.Fatalf("drill left a staged restore: %v %v", staged, err)
	}
	status, err := backupcore.LoadStatus(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastDrill == nil || !status.LastDrill.Verified || status.LastDrill.Set != report.Set {
		t.Fatalf("recorded drill = %+v", status.LastDrill)
	}
}

// A wrong key fails closed before anything is written: the live data
// directory is byte-identical, there is no scratch, no staged restore and no
// recorded drill, and the live store's history is unchanged.
func TestDrillWrongKeyFailsClosedWithoutMutation(t *testing.T) {
	f := newStageFixture(t)
	stager := New(f.dataDir, f.store)
	historyBefore, err := f.store.BackupHistory(context.Background(), f.backupDir)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := backupcore.MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	before := treeDigest(t, f.dataDir)
	backupsBefore := treeDigest(t, f.backupDir)

	for _, key := range []string{wrong.Display(), "not-a-key", ""} {
		report, err := stager.Drill(context.Background(), f.envelope, key, DrillGuards{})
		var refusal RefusalError
		if !errors.As(err, &refusal) || report.Verified {
			t.Fatalf("key %q: report=%+v err=%v, want a refusal", key, report, err)
		}
		if strings.Contains(err.Error(), f.key) || (key != "" && strings.Contains(err.Error(), key)) {
			t.Fatalf("refusal leaks key material: %v", err)
		}
	}
	if diff := diffDigest(before, treeDigest(t, f.dataDir)); len(diff) != 0 {
		t.Fatalf("wrong-key drill mutated the live data directory: %v", diff)
	}
	if diff := diffDigest(backupsBefore, treeDigest(t, f.backupDir)); len(diff) != 0 {
		t.Fatalf("wrong-key drill mutated the backup folder: %v", diff)
	}
	historyAfter, err := f.store.BackupHistory(context.Background(), f.backupDir)
	if err != nil || len(historyAfter) != len(historyBefore) {
		t.Fatalf("history changed: %v -> %v (%v)", historyBefore, historyAfter, err)
	}
	if status, err := backupcore.LoadStatus(f.dataDir); err != nil || status.LastDrill != nil {
		t.Fatalf("refused drill was recorded: %+v %v", status.LastDrill, err)
	}
}

// Guardrails: the free-space precheck and the idle gate refuse before any
// scratch is created.
func TestDrillGuardrailsSpaceAndIdle(t *testing.T) {
	f := newStageFixture(t)
	stager := New(f.dataDir, f.store)
	if _, err := stager.Drill(context.Background(), f.envelope, f.key, DrillGuards{
		FreeBytes: func(string) (uint64, bool) { return 1024, true },
	}); !errors.Is(err, ErrDrillNoSpace) {
		t.Fatalf("low space err = %v, want ErrDrillNoSpace", err)
	}
	if _, err := stager.Drill(context.Background(), f.envelope, f.key, DrillGuards{
		Idle: func() bool { return false },
	}); !errors.Is(err, ErrDrillBusy) {
		t.Fatalf("busy err = %v, want ErrDrillBusy", err)
	}
	assertNoScratch(t, f.dataDir)
	if status, err := backupcore.LoadStatus(f.dataDir); err != nil || status.LastDrill != nil {
		t.Fatalf("refused drill was recorded: %+v %v", status.LastDrill, err)
	}
}

// blockingVerifier holds VerifyBackupSet until the context ends, standing in
// for a drill step that hangs.
type blockingVerifier struct {
	entered chan struct{}
	once    sync.Once
}

func (b *blockingVerifier) VerifyBackupSet(ctx context.Context, _ string) error {
	b.once.Do(func() { close(b.entered) })
	<-ctx.Done()
	return ctx.Err()
}

// Guardrail: the hard timeout bounds a hung step and leaves no scratch.
func TestDrillGuardrailTimeout(t *testing.T) {
	f := newStageFixture(t)
	stager := New(f.dataDir, &blockingVerifier{entered: make(chan struct{})})
	start := time.Now()
	_, err := stager.Drill(context.Background(), f.envelope, f.key, DrillGuards{Timeout: 50 * time.Millisecond})
	if !errors.Is(err, ErrDrillTimeout) {
		t.Fatalf("err = %v, want ErrDrillTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timed-out drill took %s", elapsed)
	}
	assertNoScratch(t, f.dataDir)
}

// Guardrail: one drill at a time — a second drill while one runs is refused,
// not queued.
func TestDrillGuardrailOneAtATime(t *testing.T) {
	f := newStageFixture(t)
	blocker := &blockingVerifier{entered: make(chan struct{})}
	first := New(f.dataDir, blocker)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := first.Drill(ctx, f.envelope, f.key, DrillGuards{})
		done <- err
	}()
	<-blocker.entered
	second := New(f.dataDir, f.store)
	if _, err := second.Drill(context.Background(), f.envelope, f.key, DrillGuards{}); !errors.Is(err, ErrDrillRunning) {
		t.Fatalf("concurrent drill err = %v, want ErrDrillRunning", err)
	}
	// The slot is per box: a drill over another data directory still runs.
	other := newStageFixture(t)
	if report, err := New(other.dataDir, other.store).Drill(context.Background(), other.envelope, other.key, DrillGuards{}); err != nil || !report.Verified {
		t.Fatalf("drill on another box while one runs: %+v %v", report, err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled drill reported success")
	}
	// Once the first drill ends, a drill may run again.
	if report, err := second.Drill(context.Background(), f.envelope, f.key, DrillGuards{}); err != nil || !report.Verified {
		t.Fatalf("drill after the first ended: %+v %v", report, err)
	}
}

// A pending staged restore refuses the drill before anything is written: the
// staged set, the live data and the status file are untouched.
func TestDrillRefusedWhileStagedRestorePending(t *testing.T) {
	f := newStageFixture(t)
	stager := New(f.dataDir, f.store)
	if _, err := stager.Stage(context.Background(), f.envelope, f.key, false); err != nil {
		t.Fatalf("stage: %v", err)
	}
	before := treeDigest(t, f.dataDir)
	_, err := stager.Drill(context.Background(), f.envelope, f.key, DrillGuards{})
	if !errors.Is(err, ErrDrillStaged) {
		t.Fatalf("drill err = %v, want ErrDrillStaged", err)
	}
	if diff := diffDigest(before, treeDigest(t, f.dataDir)); len(diff) != 0 {
		t.Fatalf("refused drill changed %v", diff)
	}
	if _, staged, err := Pending(f.dataDir); err != nil || !staged {
		t.Fatalf("staged restore after refused drill: %v %v", staged, err)
	}
}

// CORE-3516 #18: when the drill runs its assertions and detects corruption/
// incompatibility, it records LastDrill{Verified:false} and returns the completed
// report with nil error so callers display the failed drill instead of keeping
// a stale green result.
func TestDrillFailedAssertionsReturnsCompletedReport(t *testing.T) {
	ctx := context.Background()
	f := newStageFixture(t)
	if okRep, err := New(f.dataDir, f.store).Drill(ctx, f.envelope, f.key, DrillGuards{}); err != nil || !okRep.Verified {
		t.Fatalf("baseline drill: %+v, %v", okRep, err)
	}
	incompatible := resealWithNewerSchema(t, f)
	report, err := New(f.dataDir, f.store).Drill(ctx, incompatible, f.key, DrillGuards{})
	if err != nil {
		t.Fatalf("failed drill returned err=%v, want completed report with nil err", err)
	}
	if report.Verified || report.Reason != "restore-assertions-failed" || report.Set != "newer-set" {
		t.Fatalf("failed drill report = %+v, want Verified=false Reason=restore-assertions-failed Set=newer-set", report)
	}
	status, err := backupcore.LoadStatus(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastDrill == nil || status.LastDrill.Verified || status.LastDrill.Reason != "restore-assertions-failed" || status.LastDrill.Set != "newer-set" {
		t.Fatalf("persisted LastDrill = %+v", status.LastDrill)
	}
}

// CORE-3516 follow-up: when RecordDrill fails after a failed assertion, the
// persistence error must propagate rather than returning nil over stale green state.
func TestDrillFailedAssertionsDoesNotMaskRecordDrillError(t *testing.T) {
	ctx := context.Background()
	f := newStageFixture(t)
	stager := New(f.dataDir, f.store)
	if okRep, err := stager.Drill(ctx, f.envelope, f.key, DrillGuards{}); err != nil || !okRep.Verified {
		t.Fatalf("baseline drill: %+v, %v", okRep, err)
	}
	incompatible := resealWithNewerSchema(t, f)
	statusFile := filepath.Join(f.dataDir, "backup-status.json")
	if err := os.Chmod(statusFile, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(statusFile, 0o600) }()

	report, err := stager.Drill(ctx, incompatible, f.key, DrillGuards{})
	if err == nil || !strings.Contains(err.Error(), "record restore drill") {
		t.Fatalf("failed drill with unwritable status: report=%+v err=%v, want record restore drill error", report, err)
	}
	if err := os.Chmod(statusFile, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := backupcore.LoadStatus(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastDrill == nil || !status.LastDrill.Verified {
		t.Fatalf("persisted baseline LastDrill = %+v, want prior green state untouched", status.LastDrill)
	}
}
