package backup

import (
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/beamfall/core/internal/jsonstate"
)

// statusFileName holds the ADR-0192 §6 operator state for scheduled backups:
// the last outcomes behind the two failure banners, their acknowledgements,
// the update-gate policy and the last restore drill. It carries no key
// material and no paths beyond Backup Set base names.
const statusFileName = "backup-status.json"

// UpgradeGateSoft and UpgradeGateHard are the ADR-0192 §6 update policies:
// soft proceeds past a failed backup behind an interstitial (the default);
// hard blocks the update until a fresh verified backup exists.
const (
	UpgradeGateSoft = "soft"
	UpgradeGateHard = "hard"
)

// Banner names the two distinct failure banners of ADR-0192 §6.
type Banner string

const (
	BannerBackupFailed    Banner = "backup-failed"
	BannerIntegrityFailed Banner = "integrity-failed"
)

// RunRecord is one scheduled-backup outcome.
type RunRecord struct {
	At time.Time `json:"at"`
	// FailureClass is empty for a successful, verified run.
	FailureClass string `json:"failureClass,omitempty"`
	// Set is the Backup Set base name, when one was written.
	Set string `json:"set,omitempty"`
}

// DrillRecord is one "Verify my backup now" (Layer-3) drill outcome.
type DrillRecord struct {
	At       time.Time `json:"at"`
	Verified bool      `json:"verified"`
	Set      string    `json:"set,omitempty"`
	// Reason is a fixed, content-free refusal or failure reason.
	Reason string `json:"reason,omitempty"`
}

// Status is the persisted scheduled-backup state.
type Status struct {
	LastSuccess          *RunRecord   `json:"lastSuccess,omitempty"`
	LastFailure          *RunRecord   `json:"lastFailure,omitempty"`
	LastIntegrityFailure *RunRecord   `json:"lastIntegrityFailure,omitempty"`
	BackupFailedAckAt    *time.Time   `json:"backupFailedAckAt,omitempty"`
	IntegrityFailedAckAt *time.Time   `json:"integrityFailedAckAt,omitempty"`
	UpgradeGate          string       `json:"upgradeGate,omitempty"`
	LastDrill            *DrillRecord `json:"lastDrill,omitempty"`
}

// EffectiveUpgradeGate is the update policy, soft unless hard was chosen.
func (s Status) EffectiveUpgradeGate() string {
	if s.UpgradeGate == UpgradeGateHard {
		return UpgradeGateHard
	}
	return UpgradeGateSoft
}

// BannerActive reports whether a failure banner shows: its failure exists and
// is neither superseded by a later verified backup nor acknowledged.
func (s Status) BannerActive(b Banner) bool {
	failure, ack := s.LastFailure, s.BackupFailedAckAt
	if b == BannerIntegrityFailed {
		failure, ack = s.LastIntegrityFailure, s.IntegrityFailedAckAt
	}
	if failure == nil {
		return false
	}
	if s.LastSuccess != nil && s.LastSuccess.At.After(failure.At) {
		return false
	}
	return ack == nil || ack.Before(failure.At)
}

// statusMu serializes read-modify-write of the status file within a process.
var statusMu sync.Mutex

func statusPath(dataDir string) string { return filepath.Join(dataDir, statusFileName) }

// LoadStatus reads the status file; a missing file is a fresh Status.
func LoadStatus(dataDir string) (Status, error) {
	statusMu.Lock()
	defer statusMu.Unlock()
	var s Status
	_, err := jsonstate.Load(statusPath(dataDir), &s)
	return s, err
}

// UpdateStatus applies mutate to the persisted status atomically.
func UpdateStatus(dataDir string, mutate func(*Status) error) (Status, error) {
	statusMu.Lock()
	defer statusMu.Unlock()
	var s Status
	if _, err := jsonstate.Load(statusPath(dataDir), &s); err != nil {
		return Status{}, err
	}
	if err := mutate(&s); err != nil {
		return Status{}, err
	}
	if err := jsonstate.Save(statusPath(dataDir), s); err != nil {
		return Status{}, err
	}
	return s, nil
}

// RecordRun records a scheduled-backup outcome. An integrity failure updates
// only the integrity banner's record; other failures the failed-backup one.
func RecordRun(dataDir string, run RunRecord, integrity bool) (Status, error) {
	run.At = run.At.UTC()
	run.Set = filepath.Base(run.Set)
	if run.Set == "." {
		run.Set = ""
	}
	return UpdateStatus(dataDir, func(s *Status) error {
		r := run
		switch {
		case run.FailureClass == "":
			s.LastSuccess = &r
		case integrity:
			s.LastIntegrityFailure = &r
		default:
			s.LastFailure = &r
		}
		return nil
	})
}

// ErrUnknownBanner and ErrUnknownUpgradeGate reject invalid operator input.
var (
	ErrUnknownBanner      = errors.New("unknown backup banner")
	ErrUnknownUpgradeGate = errors.New("unknown upgrade gate")
)

// AcknowledgeBanner dismisses a failure banner until the next failure.
func AcknowledgeBanner(dataDir string, b Banner, now time.Time) (Status, error) {
	if b != BannerBackupFailed && b != BannerIntegrityFailed {
		return Status{}, ErrUnknownBanner
	}
	now = now.UTC()
	return UpdateStatus(dataDir, func(s *Status) error {
		if b == BannerIntegrityFailed {
			s.IntegrityFailedAckAt = &now
		} else {
			s.BackupFailedAckAt = &now
		}
		return nil
	})
}

// SetUpgradeGate stores the update policy.
func SetUpgradeGate(dataDir, gate string) (Status, error) {
	if gate != UpgradeGateSoft && gate != UpgradeGateHard {
		return Status{}, ErrUnknownUpgradeGate
	}
	return UpdateStatus(dataDir, func(s *Status) error {
		s.UpgradeGate = gate
		return nil
	})
}

// RecordDrill stores the last restore-drill outcome.
func RecordDrill(dataDir string, d DrillRecord) (Status, error) {
	d.At = d.At.UTC()
	return UpdateStatus(dataDir, func(s *Status) error {
		s.LastDrill = &d
		return nil
	})
}
