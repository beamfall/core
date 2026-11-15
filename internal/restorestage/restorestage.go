// Package restorestage stages a Backup Set restore from the console so the
// next server start applies it (CORE-1720). A running server cannot replace
// the data directory its open store reads, so staging only decrypts and
// verifies the chosen set into the data directory beside a marker. Startup
// claims the staged set before the store opens and runs the same restore the
// offline CLI runs, keeping the previous data directory as a rollback copy.
package restorestage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/store"
)

const (
	// DirName is the staged set's directory inside the data directory.
	DirName      = "restore-staged"
	markerName   = "staged.json"
	setName      = "set"
	resultName   = "restore-result.json"
	envelopeName = "set.bbk"
	workPrefix   = "restore-work-"
)

// RefusalError is a reason a set cannot be inspected or staged that is safe
// to give the admin: it carries no key material and no stored absolute path.
type RefusalError string

func (e RefusalError) Error() string { return string(e) }

var (
	// ErrAlreadyStaged refuses a second staging while one is pending.
	ErrAlreadyStaged = RefusalError("a restore is already staged; cancel it before staging another")
	// ErrIdentityConflict refuses a set whose secrets and certificates differ
	// from the live ones when the admin did not choose to replace them.
	ErrIdentityConflict = RefusalError("the backup's secrets and certificates differ from this server's; choose to replace them to stage this restore")
)

// Marker records a staged restore.
type Marker struct {
	StagedAt string `json:"stagedAt"`
	Envelope string `json:"envelope"`
	// ReplaceIdentity lets the restore replace this server's secrets and
	// certificates with the backup's, as the CLI's --replace-identity does.
	ReplaceIdentity bool                        `json:"replaceIdentity"`
	Summary         store.BackupManifestSummary `json:"summary"`
}

// Result records the outcome of the last staged restore a server start ran.
type Result struct {
	AppliedAt       string `json:"appliedAt"`
	OK              bool   `json:"ok"`
	Error           string `json:"error,omitempty"`
	RollbackCopy    string `json:"rollbackCopy,omitempty"`
	BackupCreatedAt string `json:"backupCreatedAt,omitempty"`
}

// Status is the staged restore, if any, and the last applied result, if any.
type Status struct {
	Staged     *Marker
	LastResult *Result
}

// SetVerifier confines and integrity-checks a published Backup Set path.
// *store.Store satisfies it through VerifyBackupSet.
type SetVerifier interface {
	VerifyBackupSet(ctx context.Context, manifestPath string) error
}

// Stager inspects and stages restores for one data directory.
type Stager struct {
	dataDir string
	sets    SetVerifier
	now     func() time.Time
	mu      sync.Mutex
}

// New returns a Stager for dataDir. sets confines which envelopes an admin
// may choose to the sanctioned backup roots. It removes scratch copies an
// interrupted inspect or stage left behind, since they hold decrypted data.
func New(dataDir string, sets SetVerifier) *Stager {
	if stale, err := filepath.Glob(filepath.Join(dataDir, workPrefix+"*")); err == nil {
		for _, dir := range stale {
			_ = os.RemoveAll(dir)
		}
	}
	return &Stager{dataDir: dataDir, sets: sets, now: time.Now}
}

// Inspect decrypts the chosen set into a scratch directory inside the data
// directory, reports its contents and versions, and removes the scratch copy.
func (s *Stager) Inspect(ctx context.Context, envelope, backupKey string) (store.BackupManifestSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	work, manifest, err := s.open(ctx, envelope, backupKey)
	if err != nil {
		return store.BackupManifestSummary{}, err
	}
	defer os.RemoveAll(work)
	return store.InspectBackupManifest(ctx, manifest)
}

// Stage decrypts and verifies the chosen set, refuses one this binary cannot
// restore or, unless replaceIdentity is set, one whose secrets and
// certificates differ from the live ones, and leaves it staged for the next
// server start.
func (s *Stager) Stage(ctx context.Context, envelope, backupKey string, replaceIdentity bool) (Marker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	staged := filepath.Join(s.dataDir, DirName)
	if _, err := os.Lstat(staged); err == nil {
		return Marker{}, ErrAlreadyStaged
	} else if !os.IsNotExist(err) {
		return Marker{}, err
	}
	work, manifest, err := s.open(ctx, envelope, backupKey)
	if err != nil {
		return Marker{}, err
	}
	kept := false
	defer func() {
		if !kept {
			_ = os.RemoveAll(work)
		}
	}()
	summary, err := store.InspectBackupManifest(ctx, manifest)
	if err != nil {
		return Marker{}, err
	}
	if !summary.Compatible {
		return Marker{}, RefusalError("this server cannot restore the backup: " + summary.Refusal)
	}
	if err := store.PreflightRestoreIdentity(ctx, manifest, s.dataDir, replaceIdentity); err != nil {
		if errors.Is(err, store.ErrRestoreIdentityConflict) {
			return Marker{}, ErrIdentityConflict
		}
		return Marker{}, err
	}
	marker := Marker{
		StagedAt:        s.now().UTC().Format(time.RFC3339),
		Envelope:        envelope,
		ReplaceIdentity: replaceIdentity,
		Summary:         summary,
	}
	if err := writeJSON(filepath.Join(work, markerName), marker); err != nil {
		return Marker{}, err
	}
	if err := os.Rename(work, staged); err != nil {
		return Marker{}, err
	}
	kept = true
	return marker, syncDir(s.dataDir)
}

// Cancel removes a staged restore. It reports false when none was staged.
func (s *Stager) Cancel() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	staged := filepath.Join(s.dataDir, DirName)
	if _, err := os.Lstat(staged); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := os.RemoveAll(staged); err != nil {
		return false, err
	}
	return true, syncDir(s.dataDir)
}

// Status reports the staged restore and the last applied result.
func (s *Stager) Status() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var status Status
	marker, ok, err := Pending(s.dataDir)
	if err != nil {
		return Status{}, err
	}
	if ok {
		status.Staged = &marker
	}
	var result Result
	switch err := readJSON(filepath.Join(s.dataDir, resultName), &result); {
	case err == nil:
		status.LastResult = &result
	case !errors.Is(err, os.ErrNotExist):
		return Status{}, err
	}
	return status, nil
}

// open validates the chosen envelope and decrypts it into a fresh scratch
// directory inside the data directory. It returns that directory and the
// decrypted manifest path.
func (s *Stager) open(ctx context.Context, envelope, backupKey string) (string, string, error) {
	if strings.TrimSpace(envelope) == "" {
		return "", "", RefusalError("choose a backup set")
	}
	if filepath.Base(envelope) != envelopeName {
		return "", "", RefusalError("choose an encrypted backup set (" + envelopeName + ")")
	}
	if s.sets == nil {
		return "", "", RefusalError("backup sets are unavailable")
	}
	if err := s.sets.VerifyBackupSet(ctx, envelope); err != nil {
		return "", "", RefusalError(err.Error())
	}
	if strings.TrimSpace(backupKey) == "" {
		return "", "", RefusalError("the backup key is required")
	}
	key, err := backupcore.ParseBackupKey(strings.TrimSpace(backupKey))
	if err != nil {
		return "", "", RefusalError("the backup key is not valid")
	}
	work, err := os.MkdirTemp(s.dataDir, workPrefix)
	if err != nil {
		return "", "", err
	}
	if _, err := backupcore.OpenDirectory(ctx, key, envelope, filepath.Join(work, setName)); err != nil {
		_ = os.RemoveAll(work)
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		return "", "", RefusalError("the backup could not be opened with this key")
	}
	return work, filepath.Join(work, setName, "manifest.json"), nil
}

// Pending reports the staged restore in dataDir, if any.
func Pending(dataDir string) (Marker, bool, error) {
	var marker Marker
	err := readJSON(filepath.Join(dataDir, DirName, markerName), &marker)
	if errors.Is(err, os.ErrNotExist) {
		return Marker{}, false, nil
	}
	if err != nil {
		return Marker{}, false, fmt.Errorf("read staged restore: %w", err)
	}
	return marker, true, nil
}

// Claim moves the staged set out of dataDir to a sibling directory so the
// data directory can be snapshotted and replaced, and returns that directory
// and the manifest path inside it. Once claimed, a restart does not retry it.
func Claim(dataDir string, now time.Time) (string, string, error) {
	claimed := filepath.Clean(dataDir) + ".restore-applying-" + now.UTC().Format("20060102T150405.000000000Z")
	if err := os.Rename(filepath.Join(dataDir, DirName), claimed); err != nil {
		return "", "", err
	}
	if err := syncDir(dataDir); err != nil {
		return "", "", err
	}
	return claimed, filepath.Join(claimed, setName, "manifest.json"), nil
}

// Journal records, beside the data directory, a staged restore that has
// started replacing the data directory. It is written before the live data
// directory is renamed to Snapshot and removed only once the outcome is safe
// (restored, or rolled back), so a crash or a failed rollback in between
// leaves a record that stops the next start from booting an empty server.
type Journal struct {
	StartedAt string `json:"startedAt"`
	Snapshot  string `json:"snapshot"`
}

// JournalPath is where dataDir's restore journal lives. It sits outside the
// data directory, which the restore renames away.
func JournalPath(dataDir string) string {
	return filepath.Clean(dataDir) + ".restore-journal.json"
}

// WriteJournal durably records that a restore is about to rename dataDir.
func WriteJournal(dataDir string, journal Journal) error {
	if err := writeJSON(JournalPath(dataDir), journal); err != nil {
		return fmt.Errorf("write restore journal: %w", err)
	}
	return syncDir(filepath.Dir(filepath.Clean(dataDir)))
}

// ReadJournal reports dataDir's restore journal, if any.
func ReadJournal(dataDir string) (Journal, bool, error) {
	var journal Journal
	err := readJSON(JournalPath(dataDir), &journal)
	if errors.Is(err, os.ErrNotExist) {
		return Journal{}, false, nil
	}
	if err != nil {
		return Journal{}, true, fmt.Errorf("read restore journal: %w", err)
	}
	return journal, true, nil
}

// ClearJournal removes dataDir's restore journal once the outcome is safe.
func ClearJournal(dataDir string) error {
	if err := os.Remove(JournalPath(dataDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(filepath.Dir(filepath.Clean(dataDir)))
}

// RemoveOrphanClaims removes claimed sets a start that stopped before
// renaming the data directory left beside it: they hold decrypted data. The
// caller must hold the server lock and have found no journal.
func RemoveOrphanClaims(dataDir string) {
	if orphans, err := filepath.Glob(filepath.Clean(dataDir) + ".restore-applying-*"); err == nil {
		for _, dir := range orphans {
			_ = os.RemoveAll(dir)
		}
	}
}

// WriteResult records the outcome of an applied restore in dataDir.
func WriteResult(dataDir string, result Result) error {
	return writeJSON(filepath.Join(dataDir, resultName), result)
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path) //nolint:gosec // fixed file name inside the server's own data directory
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func syncDir(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // the server's own data directory, opened only to fsync
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
