package backup

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// BannerUnverifiedUpgrade identifies the ADR-0192 §6 / ADR-0243 warning banner
// raised when a release version upgrade proceeded without a verified Backup Set.
const BannerUnverifiedUpgrade Banner = "unverified-upgrade"

var (
	// ErrUpgradeGateBlocked indicates that the ADR-0192 backup update gate
	// refuses staging or applying an update without a verified Backup Set.
	ErrUpgradeGateBlocked = errors.New("backup update gate requires a verified Backup Set newer than any failure")
	// ErrUpgradeGateHardBypassDenied indicates that elevated bypass is forbidden
	// when the update gate policy is set to hard.
	ErrUpgradeGateHardBypassDenied = errors.New("elevated update bypass is disabled while upgrade gate policy is hard")
	// ErrUpgradeConfirmationInvalid indicates that typed bypass confirmation
	// omitted the target version or active failed Backup Set identifier.
	ErrUpgradeConfirmationInvalid = errors.New("typed confirmation does not match target version and failed Backup Set")
	// ErrUpgradeBypassReasonRequired indicates that an elevated bypass audit
	// reason was empty.
	ErrUpgradeBypassReasonRequired = errors.New("audit reason is required for elevated update bypass")
)

// UpgradeBypassRecord captures one step-up Admin authorization to proceed with a
// software upgrade under the soft update gate without a passing Backup Set.
type UpgradeBypassRecord struct {
	At             time.Time  `json:"at"`
	TargetVersion  string     `json:"targetVersion"`
	ActorProfileID string     `json:"actorProfileID,omitempty"`
	Policy         string     `json:"policy"`
	FailedSet      string     `json:"failedSet,omitempty"`
	Reason         string     `json:"reason"`
	AppliedAt      *time.Time `json:"appliedAt,omitempty"`
}

// UpgradeEventRecord captures the latest unverified or bypassed binary version
// jump detected during startup before SQLite migrations run.
type UpgradeEventRecord struct {
	At          time.Time `json:"at"`
	FromVersion string    `json:"fromVersion,omitempty"`
	ToVersion   string    `json:"toVersion"`
	Bypassed    bool      `json:"bypassed"`
	Reason      string    `json:"reason,omitempty"`
}

// BootUpgradeResult describes the outcome of pre-migration upgrade detection.
type BootUpgradeResult struct {
	Changed     bool
	FromVersion string
	ToVersion   string
	Verified    bool
	Bypassed    bool
	Override    bool
	Policy      string
	FailedSet   string
	Reason      string
}

// IsReleaseVersion reports whether v names a stamped release binary rather than
// a local development/test build.
func IsReleaseVersion(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && v != "development"
}

// UnverifiedUpgradeBannerActive reports whether the unverified-upgrade banner
// has not yet been acknowledged after the latest unverified/bypassed upgrade.
func (s Status) UnverifiedUpgradeBannerActive() bool {
	if s.LastUnverifiedUpgrade == nil || s.LastUnverifiedUpgrade.At.IsZero() {
		return false
	}
	if s.UnverifiedUpgradeAckAt == nil {
		return true
	}
	return s.LastUnverifiedUpgrade.At.After(*s.UnverifiedUpgradeAckAt)
}

// FailedSetForUpgradeGate returns the base directory name of the most recent
// failing Backup Set blocking the update gate, or "" if none is recorded.
func (s Status) FailedSetForUpgradeGate() string {
	if s.UpdateGateClear() {
		return ""
	}
	latest := s.latestFailureRecord()
	if latest != nil && strings.TrimSpace(latest.Set) != "" {
		return filepath.Base(strings.TrimSpace(latest.Set))
	}
	for _, candidate := range []*RunRecord{s.LastIntegrityFailure, s.LastFailure} {
		if candidate != nil && strings.TrimSpace(candidate.Set) != "" {
			return filepath.Base(strings.TrimSpace(candidate.Set))
		}
	}
	return ""
}

func (s Status) latestFailureRecord() *RunRecord {
	var latest *RunRecord
	for _, candidate := range []*RunRecord{s.LastFailure, s.LastIntegrityFailure} {
		if candidate == nil {
			continue
		}
		if latest == nil || candidate.At.After(latest.At) {
			latest = candidate
		}
	}
	return latest
}

// HasMatchingBypass reports whether s holds an unconsumed, non-stale soft
// elevated bypass authorizing targetVersion.
func (s Status) HasMatchingBypass(targetVersion string) bool {
	if s.EffectiveUpgradeGate() != UpgradeGateSoft {
		return false
	}
	targetVersion = strings.TrimSpace(targetVersion)
	bypass := s.LastUpgradeBypass
	if bypass == nil || bypass.AppliedAt != nil || bypass.Policy != UpgradeGateSoft {
		return false
	}
	if targetVersion == "" || strings.TrimSpace(bypass.TargetVersion) != targetVersion {
		return false
	}
	if latest := s.latestFailureRecord(); latest != nil && latest.At.After(bypass.At) {
		return false
	}
	return true
}

// CanStageUpdate reports whether staging an update to targetVersion is permitted
// under ADR-0192 §6 and ADR-0243: clear backup gate, or a matching soft bypass.
func (s Status) CanStageUpdate(targetVersion string) error {
	if s.UpdateGateClear() {
		return nil
	}
	if s.HasMatchingBypass(targetVersion) {
		return nil
	}
	if s.EffectiveUpgradeGate() == UpgradeGateHard {
		return fmt.Errorf("%w (policy: hard)", ErrUpgradeGateBlocked)
	}
	if failedSet := s.FailedSetForUpgradeGate(); failedSet != "" {
		return fmt.Errorf("%w (policy: soft; latest failure set: %s — verify a backup or authorize an elevated bypass)", ErrUpgradeGateBlocked, failedSet)
	}
	return fmt.Errorf("%w (policy: soft — verify a backup or authorize an elevated bypass)", ErrUpgradeGateBlocked)
}

// ValidateUpgradeConfirmation checks that typed confirmation names both
// targetVersion and any failed Backup Set ID blocking the gate.
func ValidateUpgradeConfirmation(s Status, targetVersion, confirmation string) error {
	targetVersion = strings.TrimSpace(targetVersion)
	confirmation = strings.TrimSpace(confirmation)
	if targetVersion == "" {
		return fmt.Errorf("%w: target version is required", ErrUpgradeConfirmationInvalid)
	}
	if !strings.Contains(confirmation, targetVersion) {
		return fmt.Errorf("%w: confirmation must name target version %s", ErrUpgradeConfirmationInvalid, targetVersion)
	}
	if failedSet := s.FailedSetForUpgradeGate(); failedSet != "" && !strings.Contains(confirmation, failedSet) {
		return fmt.Errorf("%w: confirmation must name failed Backup Set %s", ErrUpgradeConfirmationInvalid, failedSet)
	}
	return nil
}

// RecordUpgradeBypass validates and persists an elevated soft-gate bypass after
// the caller has already re-authenticated the Admin password.
func RecordUpgradeBypass(dataDir, targetVersion, confirmation, reason, actorProfileID string, now time.Time) (Status, error) {
	targetVersion = strings.TrimSpace(targetVersion)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Status{}, ErrUpgradeBypassReasonRequired
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	return UpdateStatus(dataDir, func(s *Status) error {
		if s.EffectiveUpgradeGate() == UpgradeGateHard {
			return ErrUpgradeGateHardBypassDenied
		}
		if err := ValidateUpgradeConfirmation(*s, targetVersion, confirmation); err != nil {
			return err
		}
		s.LastUpgradeBypass = &UpgradeBypassRecord{
			At:             now,
			TargetVersion:  targetVersion,
			ActorProfileID: strings.TrimSpace(actorProfileID),
			Policy:         UpgradeGateSoft,
			FailedSet:      s.FailedSetForUpgradeGate(),
			Reason:         reason,
		}
		return nil
	})
}

// AcknowledgeUpgradeBanner records operator dismissal of BannerUnverifiedUpgrade.
func AcknowledgeUpgradeBanner(dataDir string, now time.Time) (Status, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	return UpdateStatus(dataDir, func(s *Status) error {
		ts := now
		if s.LastUnverifiedUpgrade != nil && ts.Before(s.LastUnverifiedUpgrade.At) {
			ts = s.LastUnverifiedUpgrade.At
		}
		s.UnverifiedUpgradeAckAt = &ts
		return nil
	})
}

// SeedBootBinaryVersion records currentVersion when backup-status.json does not
// yet have a LastBinaryVersion and currentVersion is a release version.
func SeedBootBinaryVersion(dataDir, currentVersion string) error {
	if !IsReleaseVersion(currentVersion) {
		return nil
	}
	currentVersion = strings.TrimSpace(currentVersion)
	_, err := UpdateStatus(dataDir, func(s *Status) error {
		if s.LastBinaryVersion == "" {
			s.LastBinaryVersion = currentVersion
		}
		return nil
	})
	return err
}

// EvaluateBootUpgrade runs before SQLite migrations to detect binary version
// changes and evaluate the ADR-0192 §6 / ADR-0243 backup update gate without
// advancing LastBinaryVersion before startup completes and audit events commit.
func EvaluateBootUpgrade(dataDir, currentVersion string, allowUnverified bool, _ time.Time) (BootUpgradeResult, error) {
	currentVersion = strings.TrimSpace(currentVersion)
	if !IsReleaseVersion(currentVersion) {
		return BootUpgradeResult{}, nil
	}
	s, err := LoadStatus(dataDir)
	if err != nil {
		return BootUpgradeResult{}, err
	}
	prev := strings.TrimSpace(s.LastBinaryVersion)
	if prev == "" || prev == currentVersion {
		return BootUpgradeResult{}, nil
	}
	policy := s.EffectiveUpgradeGate()
	failedSet := s.FailedSetForUpgradeGate()
	res := BootUpgradeResult{
		Changed:     true,
		FromVersion: prev,
		ToVersion:   currentVersion,
		Policy:      policy,
		FailedSet:   failedSet,
	}
	if s.UpdateGateClear() {
		res.Verified = true
		return res, nil
	}
	if s.HasMatchingBypass(currentVersion) {
		res.Bypassed = true
		res.Reason = strings.TrimSpace(s.LastUpgradeBypass.Reason)
		return res, nil
	}
	if allowUnverified {
		res.Override = true
		res.Reason = "allow-unverified-upgrade"
		return res, nil
	}
	if policy == UpgradeGateHard {
		return BootUpgradeResult{}, fmt.Errorf(
			"boot upgrade from %s to %s blocked by hard backup update gate (run a verified backup, restore from Backup Set, or pass --allow-unverified-upgrade): %w",
			prev,
			currentVersion,
			ErrUpgradeGateBlocked,
		)
	}
	res.Reason = "soft-gate-unverified"
	return res, nil
}

// CommitBootUpgrade persists the version transition, bypass consumption, and
// unverified-upgrade banner after store startup and required audit persistence
// have succeeded.
func CommitBootUpgrade(dataDir string, res BootUpgradeResult, now time.Time) error {
	toVersion := strings.TrimSpace(res.ToVersion)
	if !res.Changed || !IsReleaseVersion(toVersion) {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	_, err := UpdateStatus(dataDir, func(s *Status) error {
		s.LastBinaryVersion = toVersion
		if res.Bypassed && s.LastUpgradeBypass != nil && strings.TrimSpace(s.LastUpgradeBypass.TargetVersion) == toVersion {
			appliedAt := now
			s.LastUpgradeBypass.AppliedAt = &appliedAt
		}
		if !res.Verified {
			s.LastUnverifiedUpgrade = &UpgradeEventRecord{
				At:          now,
				FromVersion: res.FromVersion,
				ToVersion:   toVersion,
				Bypassed:    res.Bypassed,
				Reason:      res.Reason,
			}
		}
		return nil
	})
	return err
}

// RestoreUpgradeBypass rolls back a freshly written bypass when durable audit
// delivery fails, leaving any newer concurrent bypass record untouched.
func RestoreUpgradeBypass(dataDir, targetVersion string, at time.Time, prev *UpgradeBypassRecord) error {
	targetVersion = strings.TrimSpace(targetVersion)
	at = at.UTC()
	_, err := UpdateStatus(dataDir, func(s *Status) error {
		if s.LastUpgradeBypass == nil {
			return nil
		}
		if strings.TrimSpace(s.LastUpgradeBypass.TargetVersion) != targetVersion || !s.LastUpgradeBypass.At.Equal(at) {
			return nil
		}
		if prev == nil {
			s.LastUpgradeBypass = nil
			return nil
		}
		copied := *prev
		if prev.AppliedAt != nil {
			applied := *prev.AppliedAt
			copied.AppliedAt = &applied
		}
		s.LastUpgradeBypass = &copied
		return nil
	})
	return err
}
