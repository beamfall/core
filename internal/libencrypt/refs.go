package libencrypt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// SecretRefStore is the secure-store capability needed to discover and move
// legacy libencrypt entries. MoveSecret must copy, verify, and durably publish
// dst before it deletes src. Repeating a completed move is success.
type SecretRefStore interface {
	SecretRefs(prefix string) ([]string, error)
	MoveSecret(src, dst, expectedSource, destination string) error
}

type secretKind string

const (
	secretKindKEK              secretKind = "kek"
	secretKindBundle           secretKind = "bundle"
	secretKindRewrapJournal    secretKind = "rewrap-journal"
	secretKindProvisionJournal secretKind = "provision-journal"
)

const ownerSecretVersion = 2

var ErrSecretOwnership = errors.New("libencrypt: owner-bound secret metadata mismatch")

type ownerSecret struct {
	Version int        `json:"version"`
	Owner   string     `json:"owner"`
	Kind    secretKind `json:"kind"`
	Payload string     `json:"payload"`
}

func legacyKEKRef(libraryID string) string    { return "libenc-kek-" + sanitizeID(libraryID) }
func legacyBundleRef(libraryID string) string { return "libenc-bundle-" + sanitizeID(libraryID) }
func legacyRewrapJournalRef(libraryID string) string {
	return "libenc-rewrap-journal-" + sanitizeID(libraryID)
}

func ownerRef(libraryID string, kind secretKind) string {
	sum := sha256.Sum256([]byte(libraryID))
	return "libenc-v2-" + hex.EncodeToString(sum[:]) + "-" + string(kind)
}

func kekRef(libraryID string) string    { return ownerRef(libraryID, secretKindKEK) }
func bundleRef(libraryID string) string { return ownerRef(libraryID, secretKindBundle) }
func rewrapJournalRef(libraryID string) string {
	return ownerRef(libraryID, secretKindRewrapJournal)
}
func provisionJournalRef(libraryID string) string {
	return ownerRef(libraryID, secretKindProvisionJournal)
}

func encodeOwnerSecret(owner string, kind secretKind, payload string) (string, error) {
	if owner == "" {
		return "", errors.New("libencrypt: secret owner is required")
	}
	b, err := json.Marshal(ownerSecret{Version: ownerSecretVersion, Owner: owner, Kind: kind, Payload: payload})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeOwnerSecret(raw, owner string, kind secretKind) (string, error) {
	var record ownerSecret
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return "", fmt.Errorf("%w: corrupt %s record: %v", ErrSecretOwnership, kind, err)
	}
	if record.Version != ownerSecretVersion || record.Owner != owner || record.Kind != kind {
		return "", fmt.Errorf("%w: %s", ErrSecretOwnership, kind)
	}
	return record.Payload, nil
}

func inspectOwnerSecretRef(ref, raw string) (ownerSecret, error) {
	var record ownerSecret
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return ownerSecret{}, fmt.Errorf("%w: corrupt v2 record %s: %v", ErrSecretOwnership, ref, err)
	}
	if record.Version != ownerSecretVersion || record.Owner == "" || !validSecretKind(record.Kind) || ownerRef(record.Owner, record.Kind) != ref {
		return ownerSecret{}, fmt.Errorf("%w: v2 ref %s", ErrSecretOwnership, ref)
	}
	return record, nil
}

func validSecretKind(kind secretKind) bool {
	switch kind {
	case secretKindKEK, secretKindBundle, secretKindRewrapJournal, secretKindProvisionJournal:
		return true
	default:
		return false
	}
}

func (m *Manager) readOwnerSecret(owner string, kind secretKind) (string, error) {
	raw, err := m.secrets.Secret(ownerRef(owner, kind))
	if err != nil {
		return "", err
	}
	return decodeOwnerSecret(raw, owner, kind)
}

func (m *Manager) writeOwnerSecret(owner string, kind secretKind, payload string) error {
	ref := ownerRef(owner, kind)
	if payload == "" {
		return m.secrets.SetSecret(ref, "")
	}
	raw, err := encodeOwnerSecret(owner, kind, payload)
	if err != nil {
		return err
	}
	return m.secrets.SetSecret(ref, raw)
}

func (m *Manager) ownerSecretExists(owner string, kind secretKind) (bool, error) {
	ref := ownerRef(owner, kind)
	if store, ok := m.secrets.(SecretRefStore); ok {
		refs, err := store.SecretRefs(ref)
		if err != nil {
			return false, err
		}
		for _, candidate := range refs {
			if candidate == ref {
				return true, nil
			}
		}
		return false, nil
	}
	// Only a genuinely missing ref is absence. Any other read error (permissions,
	// corruption, I/O) must propagate: reporting it as absent lets a fresh
	// provision silently overwrite an unreadable journal or key generation.
	_, err := m.secrets.Secret(ref)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RefMigrationReport is non-secret startup evidence. Orphan refs stay in the
// secure store so an operator can restore the missing Library row or recover
// them manually; no owner is guessed.
type RefMigrationReport struct {
	Moved   []string
	Orphans []string
}

var ErrLegacyRefCollision = errors.New("libencrypt: ambiguous legacy secret ref collision")

type legacyRef struct {
	ref    string
	suffix string
	kind   secretKind
}

func parseLegacyRef(ref string) (legacyRef, bool) {
	for _, candidate := range []struct {
		prefix string
		kind   secretKind
	}{
		{"libenc-kek-", secretKindKEK},
		{"libenc-bundle-", secretKindBundle},
		{"libenc-rewrap-journal-", secretKindRewrapJournal},
	} {
		if strings.HasPrefix(ref, candidate.prefix) {
			return legacyRef{ref: ref, suffix: strings.TrimPrefix(ref, candidate.prefix), kind: candidate.kind}, true
		}
	}
	return legacyRef{}, false
}

// MigrateLegacyRefs moves legacy sanitize-derived refs to exact-owner v2 refs.
// owners must include active and trashed Libraries. A suffix with more than one
// possible owner is never opened or moved; all colliding owners fail closed.
func (m *Manager) MigrateLegacyRefs(owners []string) (RefMigrationReport, error) {
	// Legacy refs are keyed by a lossy suffix rather than an exact owner. Keep
	// their entire inventory/move pass mutually exclusive, then also take the
	// destination owner's normal lifecycle lease around each unique move.
	m.migration.Lock()
	defer m.migration.Unlock()

	store, ok := m.secrets.(SecretRefStore)
	if !ok {
		return RefMigrationReport{}, errors.New("libencrypt: secure store cannot inventory/move legacy refs")
	}
	bySuffix := make(map[string][]string)
	ownerSet := make(map[string]struct{})
	for _, owner := range owners {
		if owner == "" {
			continue
		}
		if _, exists := ownerSet[owner]; exists {
			continue
		}
		ownerSet[owner] = struct{}{}
		bySuffix[sanitizeID(owner)] = append(bySuffix[sanitizeID(owner)], owner)
	}
	refs, err := store.SecretRefs("libenc-")
	if err != nil {
		return RefMigrationReport{}, fmt.Errorf("libencrypt: inventory legacy refs: %w", err)
	}
	sort.Strings(refs)
	var report RefMigrationReport
	// Validate every owner-bound entry before moving any legacy source. A bad v2
	// record therefore fails startup without partially advancing the migration.
	for _, ref := range refs {
		if !strings.HasPrefix(ref, "libenc-v2-") {
			continue
		}
		raw, err := m.secrets.Secret(ref)
		if err != nil {
			return report, fmt.Errorf("libencrypt: read v2 ref %s: %w", ref, err)
		}
		record, err := inspectOwnerSecretRef(ref, raw)
		if err != nil {
			return report, err
		}
		if _, exists := ownerSet[record.Owner]; !exists {
			report.Orphans = append(report.Orphans, ref)
		}
	}
	var collisions []string
	for _, ref := range refs {
		if strings.HasPrefix(ref, "libenc-v2-") {
			continue
		}
		legacy, ok := parseLegacyRef(ref)
		if !ok {
			continue
		}
		candidates := bySuffix[legacy.suffix]
		switch len(candidates) {
		case 0:
			report.Orphans = append(report.Orphans, ref)
			continue
		case 1:
		default:
			collisions = append(collisions, fmt.Sprintf("%s owners=%s", ref, strings.Join(candidates, ",")))
			continue
		}
		owner := candidates[0]
		releaseOwner := m.acquireLibrary(owner)
		payload, err := m.secrets.Secret(ref)
		if err != nil {
			releaseOwner()
			return report, fmt.Errorf("libencrypt: read legacy ref %s: %w", ref, err)
		}
		destination, err := encodeOwnerSecret(owner, legacy.kind, payload)
		if err != nil {
			releaseOwner()
			return report, err
		}
		dst := ownerRef(owner, legacy.kind)
		if err := store.MoveSecret(ref, dst, payload, destination); err != nil {
			releaseOwner()
			return report, fmt.Errorf("libencrypt: move legacy ref %s: %w", ref, err)
		}
		written, err := m.secrets.Secret(dst)
		if err != nil {
			releaseOwner()
			return report, fmt.Errorf("libencrypt: verify moved ref %s: %w", dst, err)
		}
		if _, err := decodeOwnerSecret(written, owner, legacy.kind); err != nil {
			releaseOwner()
			return report, err
		}
		releaseOwner()
		report.Moved = append(report.Moved, ref)
	}
	if len(collisions) != 0 {
		return report, fmt.Errorf("%w: %s", ErrLegacyRefCollision, strings.Join(collisions, "; "))
	}
	return report, nil
}
