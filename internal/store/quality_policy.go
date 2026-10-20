package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"
)

// Per-Library quality policy + the Radarr-style upgrade loop.
//
// A Library's quality policy is a preference ordering over generic quality
// tiers plus a cutoff: at or above the cutoff a Media is "good enough" and the
// upgrade loop stops promoting better encodes for it. The tiers are GENERIC
// pixel buckets derived from a candidate's frame height (invariant #1 — they
// describe resolution, never domain vocabulary), so the policy reasons over the
// file plane without any plugin knowledge.
//
// The policy is operator config, so it is stored in the existing server_config
// KV table keyed by Library id (the same JSON-blob-under-one-key shape as the
// qBittorrent routing map), NOT a new schema object — the schema is frozen and
// this config fits the existing mechanism.
//
// When scan/import produces a candidate encode that ranks ABOVE a Media's
// current active Version under its Library's policy AND the current Version is
// BELOW cutoff, the upgrade loop folds the candidate into the SAME Media as an
// additional Version and promotes it to the active (default-label) slot, keeping
// the prior active encode as a non-default Version — the "keep-as-Version" merge
// verb. Nothing is destroyed (invariant #6): the old encode stays a playable
// Version, the Media row and all its engagement/metadata persist untouched.

// Quality tiers — the closed, generic vocabulary the policy orders over. They
// are resolution buckets (a candidate's frame height maps to exactly one), not
// domain words, so they live in Core (invariant #1). Best-to-worst pixel size.
const (
	QualityTierUHD    = "uhd"    // ≥ 2160 lines (4K and up).
	QualityTierHD1080 = "hd1080" // 1080–2159 lines.
	QualityTierHD720  = "hd720"  // 720–1079 lines.
	QualityTierSD     = "sd"     // < 720 lines (the floor; an unknown height too).
)

// qualityTiers is the closed tier vocabulary the policy validates against.
var qualityTiers = map[string]bool{
	QualityTierUHD:    true,
	QualityTierHD1080: true,
	QualityTierHD720:  true,
	QualityTierSD:     true,
}

// QualityTierForHeight maps a probed video frame height (lines) to its generic
// quality tier. An unknown height (0 — a probe-less scan) is the SD floor, so an
// un-probed candidate never out-ranks a probed one.
func QualityTierForHeight(height int) string {
	switch {
	case height >= 2160:
		return QualityTierUHD
	case height >= 1080:
		return QualityTierHD1080
	case height >= 720:
		return QualityTierHD720
	default:
		return QualityTierSD
	}
}

// QualityPolicy is a Library's encode-preference policy. Order is
// the preference ranking, best tier first; a tier's index in Order is its rank
// (lower = preferred). Cutoff names the tier at or above which a Media is good
// enough — once a Media's active Version reaches a tier ranked at-or-above
// Cutoff, the upgrade loop stops promoting better candidates for it.
type QualityPolicy struct {
	// Order lists the quality tiers best-first. Tiers absent from Order are
	// unranked: a candidate whose tier is unranked never out-ranks a ranked one.
	Order []string `json:"order"`
	// Cutoff is the satisfied threshold tier (must appear in Order). "" means no
	// cutoff — the loop always upgrades to a strictly better candidate.
	Cutoff string `json:"cutoff"`
}

// DefaultQualityPolicy is the policy applied to a Library with no override: the
// natural best-first resolution ranking with a 1080p cutoff (the common "good
// enough at 1080p, upgrade SD/720p toward it" default). A Library inherits this
// until an operator sets its own.
func DefaultQualityPolicy() QualityPolicy {
	return QualityPolicy{
		Order:  []string{QualityTierUHD, QualityTierHD1080, QualityTierHD720, QualityTierSD},
		Cutoff: QualityTierHD1080,
	}
}

// rank returns the preference index of a tier (0 = best). A tier not in Order is
// unranked: its rank is len(Order), so it sorts behind every ranked tier and two
// unranked tiers compare equal.
func (p QualityPolicy) rank(tier string) int {
	for i, t := range p.Order {
		if t == tier {
			return i
		}
	}
	return len(p.Order)
}

// prefers reports whether candidate ranks strictly above current under the
// policy's preference order (candidate is the better encode).
func (p QualityPolicy) prefers(candidate, current string) bool {
	return p.rank(candidate) < p.rank(current)
}

// satisfied reports whether a tier is at or above the policy's cutoff — i.e. the
// Media is good enough and the upgrade loop should stop. With no cutoff ("")
// nothing is ever satisfied, so the loop always considers a better candidate.
func (p QualityPolicy) satisfied(tier string) bool {
	if p.Cutoff == "" {
		return false
	}
	return p.rank(tier) <= p.rank(p.Cutoff)
}

// ShouldUpgrade is the upgrade-loop decision: a candidate encode
// at candidateTier should replace the current active Version at currentTier iff
// the current is BELOW cutoff (not yet good enough) AND the candidate ranks
// strictly above it. When the current is already at-or-above cutoff the loop
// stops even if the candidate is technically better, so a Library does not churn
// files past its "good enough" bar.
func (p QualityPolicy) ShouldUpgrade(currentTier, candidateTier string) bool {
	if p.satisfied(currentTier) {
		return false
	}
	return p.prefers(candidateTier, currentTier)
}

// configQualityPolicyPrefix keys a Library's quality-policy JSON in
// server_config: "library.quality_policy:<libraryID>". One row per Library that
// has an override; absent rows fall back to DefaultQualityPolicy. The closed
// key vocabulary stays intact — this is the one prefixed family, validated by
// QualityPolicy's tier checks on write, not an open key namespace.
const configQualityPolicyPrefix = "library.quality_policy:"

func qualityPolicyKey(libraryID LibraryID) string {
	return configQualityPolicyPrefix + string(libraryID)
}

// QualityPolicyForLibrary reads a Library's quality policy, falling back to
// DefaultQualityPolicy when the Library has no override row. A stored row that
// fails to decode also falls back to the default rather than erroring — the
// policy is advisory operator config, never a correctness gate.
func (s *Store) QualityPolicyForLibrary(ctx context.Context, libraryID LibraryID) (QualityPolicy, error) {
	if libraryID == "" {
		return QualityPolicy{}, ValidationError("library ID is required")
	}
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return QualityPolicy{}, err
	}
	raw, ok := values[qualityPolicyKey(libraryID)]
	if !ok || raw == "" {
		return DefaultQualityPolicy(), nil
	}
	var policy QualityPolicy
	if jsonErr := json.Unmarshal([]byte(raw), &policy); jsonErr != nil || len(policy.Order) == 0 {
		return DefaultQualityPolicy(), nil
	}
	return policy, nil
}

// SetQualityPolicyForLibrary persists a Library's quality policy.
// Every tier in Order, and the Cutoff (when set), must be in the closed tier
// vocabulary; Order must be non-empty and the Cutoff (when set) must appear in
// it. An invalid policy is a client-safe ValidationError. The write lands in the
// existing server_config KV, no migration.
func (s *Store) SetQualityPolicyForLibrary(ctx context.Context, libraryID LibraryID, policy QualityPolicy, now time.Time) error {
	if libraryID == "" {
		return ValidationError("library ID is required")
	}
	if len(policy.Order) == 0 {
		return ValidationError("quality policy order must list at least one tier")
	}
	seen := map[string]bool{}
	for _, tier := range policy.Order {
		if !qualityTiers[tier] {
			return ValidationError("unknown quality tier " + tier)
		}
		if seen[tier] {
			return ValidationError("quality tier " + tier + " listed more than once")
		}
		seen[tier] = true
	}
	if policy.Cutoff != "" {
		if !qualityTiers[policy.Cutoff] {
			return ValidationError("unknown cutoff tier " + policy.Cutoff)
		}
		if !seen[policy.Cutoff] {
			return ValidationError("cutoff tier " + policy.Cutoff + " must appear in the preference order")
		}
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return s.setServerConfigValues(ctx, map[string]string{qualityPolicyKey(libraryID): string(encoded)}, now)
}

// QualityCandidate is a probed encode the upgrade loop is asked to fold into an
// existing Media. It carries its own content identity, path, and
// the probe data the policy ranks on (frame height → quality tier) — the loop
// never reads per-file resolution from the store because resolution is not a
// media_file column; the caller (scan/import) passes the freshly probed value.
type QualityCandidate struct {
	ContentFingerprint string
	Path               string
	VideoHeight        int
	OSHash             string
	PHash              string
	SizeBytes          int64
	ModTime            time.Time
	// Streams is the optional per-stream inventory for the candidate's bytes,
	// recorded alongside the new Version like AddMediaFileVersion's streams arg.
	Streams []MediaFileStream
}

// keepAsVersionLabel is the version_label the prior active encode is demoted to
// when a candidate is promoted (ADR-0009 §4 keep-as-Version). The default ("")
// Version is the active one PrimaryMediaFile/negotiation pick first; the kept
// encode moves to this non-default label so it stays a playable Version without
// being the active pick.
const keepAsVersionLabel = "kept"

// MergeKeepAsVersion folds a candidate encode into an existing Media as an
// additional Version, applying the Library's quality policy (ADR-0009 §4
// keep-as-Version merge verb + upgrade loop). It is non-destructive (invariant
// #6): the prior active encode is always preserved as a Version, never trashed.
//
//   - If the policy says the candidate should UPGRADE the current active Version
//     (current below cutoff, candidate ranks higher), the candidate becomes the
//     active (default-label "") Version and the prior active encode is demoted to
//     the keep-as-Version slot — both files remain, the policy-preferred one is
//     active.
//   - Otherwise the candidate is folded in as a kept (non-active) Version: the
//     current active encode stays active, the candidate joins as a sibling
//     Version (so a worse-or-equal duplicate becomes a Version rather than a
//     separate Media, and a better candidate a Library has already deemed good
//     enough is retained without churn).
//
// Returns whether the candidate was promoted to active. currentHeight is the
// probed frame height of the Media's current active encode (the caller reads it
// from the Media row — VideoWidth/VideoHeight — since per-file resolution is not
// stored).
func (s *Store) MergeKeepAsVersion(ctx context.Context, libraryID LibraryID, mediaID MediaID, currentHeight int, candidate QualityCandidate, now time.Time) (promoted bool, err error) {
	if mediaID == "" {
		return false, ValidationError("media ID is required")
	}
	if candidate.ContentFingerprint == "" {
		return false, ValidationError("candidate content fingerprint is required")
	}
	if candidate.Path == "" {
		return false, ValidationError("candidate path is required")
	}
	policy, err := s.QualityPolicyForLibrary(ctx, libraryID)
	if err != nil {
		return false, err
	}
	currentTier := QualityTierForHeight(currentHeight)
	candidateTier := QualityTierForHeight(candidate.VideoHeight)
	upgrade := policy.ShouldUpgrade(currentTier, candidateTier)

	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	if err := s.mergeKeepAsVersionSQL(ctx, mediaID, candidate, upgrade, now); err != nil {
		return false, err
	}
	// The active Version changed identity-wise (it absorbed an encode, possibly
	// swapping which file is active), so announce an update. A failed outbox
	// append never fails the merge — the catalog write already committed (the
	// same posture as ImportMediaFile / MergeApply).
	if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, mediaID, nil, now); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", mediaID, "error", eventErr)
	}
	return upgrade, nil
}

// mergeKeepAsVersionSQL performs the keep-as-Version write in one transaction so
// the (media_id, version_label, part_ordinal) available-slot uniqueness is never
// transiently violated: when promoting, the prior default Version is demoted to
// the keep label BEFORE the candidate claims the default slot.
func (s *Store) mergeKeepAsVersionSQL(ctx context.Context, mediaID MediaID, candidate QualityCandidate, upgrade bool, now time.Time) error {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var one int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM media WHERE id = ?", mediaID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("media not found")
	}
	if err != nil {
		return err
	}

	label := keepAsVersionLabel
	if upgrade {
		// Demote the current active (default-label, default-Part) encode to the
		// keep slot FIRST, freeing ('' , 0) for the candidate. Only the active
		// primary Part is relabelled; any existing kept Versions are untouched.
		// Non-destructive: the row persists, it just stops being the active pick.
		if _, err := tx.ExecContext(ctx, `
			UPDATE media_file SET version_label = ?
			WHERE media_id = ? AND version_label = '' AND part_ordinal = 0 AND missing_at_ms IS NULL
		`, keepAsVersionLabel, mediaID); err != nil {
			return err
		}
		label = "" // the candidate takes the active default slot.
	}

	// Insert (or re-scan-upsert) the candidate at its slot. ON CONFLICT keys on
	// content_fingerprint like the other file-plane writes, so re-running the
	// same candidate is idempotent.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, oshash, phash, size_bytes, mtime_ms, missing_at_ms, last_seen_at_ms)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, NULL, ?)
		ON CONFLICT(content_fingerprint) DO UPDATE SET
		  media_id = excluded.media_id,
		  path = excluded.path,
		  version_label = excluded.version_label,
		  part_ordinal = excluded.part_ordinal,
		  oshash = CASE WHEN excluded.oshash = '' THEN media_file.oshash ELSE excluded.oshash END,
		  phash = CASE WHEN excluded.phash = '' THEN media_file.phash ELSE excluded.phash END,
		  size_bytes = CASE WHEN excluded.size_bytes = 0 THEN media_file.size_bytes ELSE excluded.size_bytes END,
		  mtime_ms = CASE WHEN excluded.mtime_ms = 0 THEN media_file.mtime_ms ELSE excluded.mtime_ms END,
		  missing_at_ms = NULL,
		  last_seen_at_ms = excluded.last_seen_at_ms
	`, candidate.ContentFingerprint, candidate.ContentFingerprint, mediaID, candidate.Path, label,
		candidate.OSHash, candidate.PHash, candidate.SizeBytes, timeToMillis(candidate.ModTime), timeToMillis(now)); err != nil {
		return err
	}
	if candidate.Streams != nil {
		if err := replaceMediaFileInventoryTx(ctx, tx, candidate.ContentFingerprint, candidate.Streams, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}
