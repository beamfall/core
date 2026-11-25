package restorestage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/store"
)

// DefaultDrillTimeout is the hard timeout of a restore drill (ADR-0192 §5 L3).
const DefaultDrillTimeout = 15 * time.Minute

// drillSpaceFactor is the free space a drill needs, as a multiple of the
// encrypted set's size: the decrypted set, the restored data directory and the
// restore's own staging copy all live in scratch at once.
const drillSpaceFactor = 3

// Drill refusals. Each is safe to display to the admin.
var (
	ErrDrillRunning = RefusalError("a restore drill is already running")
	ErrDrillBusy    = RefusalError("the server is busy; run the restore drill when it is idle")
	ErrDrillNoSpace = RefusalError("there is not enough free space in the data directory for a restore drill")
	ErrDrillTimeout = RefusalError("the restore drill did not finish within its time limit")
)

// DrillGuards are the ADR-0192 §5 L3 guardrails. A zero value applies the
// default timeout and skips the space and idle checks.
type DrillGuards struct {
	// Timeout bounds the whole drill; zero means DefaultDrillTimeout.
	Timeout time.Duration
	// FreeBytes reports free bytes on the filesystem holding dir; ok false
	// means unknown, and the space precheck is skipped.
	FreeBytes func(dir string) (free uint64, ok bool)
	// Idle reports whether the server is idle enough to run a drill.
	Idle func() bool
}

// DrillReport is the outcome of a restore drill that ran.
type DrillReport struct {
	Verified        bool
	Set             string
	BackupCreatedAt string
	Databases       int
	Libraries       int
	Duration        time.Duration
	// Reason is a fixed, content-free failure reason when Verified is false.
	Reason string
}

// drillRunning enforces one concurrent drill per process.
var drillRunning atomic.Bool

// Drill is "Verify my backup now" (ADR-0192 §5 L3): it decrypts the chosen
// set into scratch inside the data directory, restores it into an ephemeral
// data directory there, opens that as a store (migrations and the SQLite
// integrity check), runs a read assertion suite, and removes every scratch
// file. It never touches the live store, the live data files or a staged
// restore. A refusal (busy, running, space, key, set) writes nothing; a drill
// that ran records its outcome in the backup status file.
func (s *Stager) Drill(ctx context.Context, envelope, backupKey string, guards DrillGuards) (DrillReport, error) {
	if !drillRunning.CompareAndSwap(false, true) {
		return DrillReport{}, ErrDrillRunning
	}
	defer drillRunning.Store(false)
	if guards.Idle != nil && !guards.Idle() {
		return DrillReport{}, ErrDrillBusy
	}
	timeout := guards.Timeout
	if timeout <= 0 {
		timeout = DefaultDrillTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := s.drillSpacePrecheck(ctx, envelope, guards.FreeBytes); err != nil {
		return DrillReport{}, drillTimeoutOr(ctx, err)
	}
	start := s.now()
	work, manifest, err := s.open(ctx, envelope, backupKey)
	if err != nil {
		return DrillReport{}, drillTimeoutOr(ctx, err)
	}
	defer os.RemoveAll(work)

	report := DrillReport{Set: filepath.Base(filepath.Dir(envelope))}
	err = runDrillAssertions(ctx, manifest, filepath.Join(work, "drill-data"), &report)
	report.Duration = s.now().Sub(start)
	if err != nil {
		if ctx.Err() != nil {
			report.Reason = "timeout"
		} else {
			report.Reason = "restore-assertions-failed"
		}
	} else {
		report.Verified = true
	}
	if _, recErr := backupcore.RecordDrill(s.dataDir, backupcore.DrillRecord{
		At: s.now(), Verified: report.Verified, Set: report.Set, Reason: report.Reason,
	}); recErr != nil && err == nil {
		err = fmt.Errorf("record restore drill: %w", recErr)
	}
	if report.Reason == "timeout" {
		return report, ErrDrillTimeout
	}
	return report, err
}

// drillTimeoutOr reports the hard timeout in place of whatever error a step
// returned once the drill's deadline has passed.
func drillTimeoutOr(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrDrillTimeout
	}
	return err
}

func (s *Stager) drillSpacePrecheck(ctx context.Context, envelope string, freeBytes func(string) (uint64, bool)) error {
	if strings.TrimSpace(envelope) == "" || filepath.Base(envelope) != envelopeName || s.sets == nil {
		return nil // open refuses these with its own reasons
	}
	if freeBytes == nil {
		return nil
	}
	// Confine the path before reading its size, as open does.
	if err := s.sets.VerifyBackupSet(ctx, envelope); err != nil {
		return RefusalError(err.Error())
	}
	info, err := os.Stat(envelope)
	if err != nil {
		return RefusalError("the backup set could not be read")
	}
	free, ok := freeBytes(s.dataDir)
	if !ok {
		return nil
	}
	need := info.Size() * drillSpaceFactor
	if need > 0 && free < uint64(need) {
		return ErrDrillNoSpace
	}
	return nil
}

// runDrillAssertions restores the decrypted set into scratchData, boots it as
// an ephemeral store and checks it reads back. Every path is inside scratch.
func runDrillAssertions(ctx context.Context, manifest, scratchData string, report *DrillReport) error {
	summary, err := store.InspectBackupManifest(ctx, manifest)
	if err != nil {
		return err
	}
	if !summary.Compatible {
		return errors.New("backup is not restorable by this server")
	}
	report.BackupCreatedAt = summary.CreatedAt
	report.Databases = len(summary.Databases)
	// The scratch directory is new and empty, so it has no identity to keep.
	if _, err := store.RestoreBackupWithOptions(ctx, manifest, scratchData, store.RestoreOptions{ReplaceIdentity: true}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	restored, err := store.Open(ctx, scratchData, store.Options{})
	if err != nil {
		return err
	}
	defer restored.Close()
	libraries, err := restored.Libraries(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return err
	}
	report.Libraries = len(libraries)
	if summary.LibraryRootCount > 0 && len(libraries) == 0 {
		return errors.New("restored catalog has no Libraries but the backup recorded library roots")
	}
	return ctx.Err()
}
