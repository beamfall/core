package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrOfflinePackNotFound is returned when a pack id does not exist (or is not
// the caller's, when scoped by profile).
var ErrOfflinePackNotFound = errors.New("offline pack not found")

// OfflinePackID is a stable Offline Pack identifier.
type OfflinePackID string

// OfflinePackState is the closed lifecycle vocabulary for a pack.
type OfflinePackState string

const (
	// OfflinePackPending is a freshly created pack whose bytes are not yet built.
	OfflinePackPending OfflinePackState = "pending"
	// OfflinePackBuilding marks a pack whose bytes are being assembled.
	OfflinePackBuilding OfflinePackState = "building"
	// OfflinePackReady marks a pack whose bytes are available to serve.
	OfflinePackReady OfflinePackState = "ready"
	// OfflinePackRevoked is a terminal denial state: the server denies further
	// bytes and key material (server-side revoke/relock, ADR-0098).
	OfflinePackRevoked OfflinePackState = "revoked"
	// OfflinePackExpired is a terminal state reached by the eviction/expiry sweep.
	OfflinePackExpired OfflinePackState = "expired"
)

// OfflinePack is one generic, domain-agnostic Offline Pack row (ADR-0098 area
// 6). TargetKind/TargetRef are OPAQUE: the generic substrate never interprets
// them — a TargetResolver registered for the kind translates the ref into a
// visible Media list before any item row is written. Manifest holds only
// visible items (filtered at write time): no secrets, no hidden Library names,
// no tombstone labels, no excluded counts.
type OfflinePack struct {
	ID         OfflinePackID
	ProfileID  ProfileID
	DeviceID   DeviceID
	TargetKind string
	TargetRef  string
	State      OfflinePackState
	Manifest   string
	BytesTotal int64
	BytesReady int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	ItemCount  int

	DeviceEnrollmentID  string
	DeviceKeyThumbprint string
	DevicePublicKey     []byte
	DeviceAttested      bool
	LibraryIDs          []LibraryID
	ManifestRevision    uint64
	ManifestSHA256      string
	GrantSequence       uint64
	RevGrant            uint64
	RevEnrollment       uint64
	RevProfile          uint64
	RevLibraryPolicy    uint64
	RevBackup           uint64
	WrappedCEK          []byte
}

// OfflinePackItem is one frozen Media reference in a pack's resolved list.
type OfflinePackItem struct {
	PackID          OfflinePackID
	MediaID         MediaID
	SortOrder       int
	SelectedVersion string
	PlaintextSize   int64
	SHA256          string
	CiphertextPath  string
}

// CreateOfflinePack inserts a pack row plus its frozen, ordered Media list in a
// single mutation. items must already be the Reveal/Sealed-filtered visible
// Media (the caller resolves the target through a TargetResolver first); this
// method does not re-resolve. A pack with zero items is rejected — an empty
// pack is never created (e.g. a Station build whose generator returned nothing
// errors out before reaching here).
func (s *Store) CreateOfflinePack(ctx context.Context, pack OfflinePack, items []OfflinePackItem, now time.Time) (OfflinePack, error) {
	pack.TargetKind = strings.TrimSpace(pack.TargetKind)
	if pack.ProfileID == "" {
		return OfflinePack{}, ValidationError("profile ID is required")
	}
	if pack.TargetKind == "" {
		return OfflinePack{}, ValidationError("target kind is required")
	}
	if !validOfflinePackState(pack.State) {
		return OfflinePack{}, ValidationError("invalid offline pack state")
	}
	if err := validateOfflinePackProgress(pack.BytesReady, pack.BytesTotal); err != nil {
		return OfflinePack{}, err
	}
	if len(items) == 0 {
		return OfflinePack{}, ValidationError("offline pack must contain at least one item")
	}
	if pack.ID == "" {
		id, err := randomOfflinePackID()
		if err != nil {
			return OfflinePack{}, err
		}
		pack.ID = id
	}
	if pack.State == "" {
		pack.State = OfflinePackPending
	}
	libraryIDsJSON, err := json.Marshal(pack.LibraryIDs)
	if err != nil {
		return OfflinePack{}, err
	}
	pack.CreatedAt = now.UTC()
	pack.UpdatedAt = pack.CreatedAt
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflinePack{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return OfflinePack{}, err
	}
	defer tx.Rollback()
	var profileExists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM profile WHERE id = ?`, pack.ProfileID).Scan(&profileExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OfflinePack{}, ValidationError("profile not found")
		}
		return OfflinePack{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO offline_pack (id, profile_id, device_id, target_kind, target_ref, state,
		    manifest_json, bytes_total, bytes_ready, created_at_ms, updated_at_ms, expires_at_ms,
		    device_enrollment_id, device_key_thumbprint, device_public_key, device_attested,
		    library_ids_json, manifest_revision, manifest_sha256, grant_seq,
		    rev_grant, rev_enrollment, rev_profile, rev_library_policy, rev_backup, wrapped_cek)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, pack.ID, pack.ProfileID, pack.DeviceID, pack.TargetKind, pack.TargetRef, pack.State,
		pack.Manifest, pack.BytesTotal, pack.BytesReady,
		pack.CreatedAt.UnixMilli(), pack.UpdatedAt.UnixMilli(), millisOrNull(pack.ExpiresAt),
		pack.DeviceEnrollmentID, pack.DeviceKeyThumbprint, nonnilBytes(pack.DevicePublicKey), pack.DeviceAttested,
		string(libraryIDsJSON), pack.ManifestRevision, pack.ManifestSHA256, pack.GrantSequence,
		nonzeroGeneration(pack.RevGrant), nonzeroGeneration(pack.RevEnrollment),
		nonzeroGeneration(pack.RevProfile), nonzeroGeneration(pack.RevLibraryPolicy),
		nonzeroGeneration(pack.RevBackup), nonnilBytes(pack.WrappedCEK)); err != nil {
		return OfflinePack{}, err
	}
	for i, item := range items {
		order := item.SortOrder
		if order == 0 {
			order = i + 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO offline_pack_item
			    (pack_id, media_id, sort_order, selected_version, plaintext_size, sha256, ciphertext_path)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, pack.ID, item.MediaID, order, item.SelectedVersion, item.PlaintextSize, item.SHA256, item.CiphertextPath); err != nil {
			return OfflinePack{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return OfflinePack{}, err
	}
	return s.offlinePackByIDLocked(ctx, pack.ID)
}

// OfflinePackByID returns a pack scoped to the owning Profile. A pack belonging
// to another Profile reads as not-found (no cross-Profile leak).
func (s *Store) OfflinePackByID(ctx context.Context, id OfflinePackID, profileID ProfileID) (OfflinePack, error) {
	if id == "" {
		return OfflinePack{}, ValidationError("id is required")
	}
	if profileID == "" {
		return OfflinePack{}, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflinePack{}, err
	}
	pack, err := s.offlinePackByIDLocked(ctx, id)
	if err != nil {
		return OfflinePack{}, err
	}
	if pack.ProfileID != profileID {
		return OfflinePack{}, ErrOfflinePackNotFound
	}
	return pack, nil
}

// OfflinePacksForProfile lists the caller's packs, newest-updated first.
func (s *Store) OfflinePacksForProfile(ctx context.Context, profileID ProfileID) ([]OfflinePack, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT p.id, p.profile_id, p.device_id, p.target_kind, p.target_ref, p.state,
		       p.manifest_json, p.bytes_total, p.bytes_ready, p.created_at_ms, p.updated_at_ms,
		       p.expires_at_ms, p.revoked_at_ms, COUNT(i.media_id),
		       p.device_enrollment_id, p.device_key_thumbprint, p.device_public_key, p.device_attested,
		       p.library_ids_json, p.manifest_revision, p.manifest_sha256, p.grant_seq,
		       p.rev_grant, p.rev_enrollment, p.rev_profile, p.rev_library_policy, p.rev_backup,
		       p.wrapped_cek
		FROM offline_pack p
		LEFT JOIN offline_pack_item i ON i.pack_id = p.id
		WHERE p.profile_id = ?
		GROUP BY p.id
		ORDER BY p.updated_at_ms DESC, p.id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OfflinePack{}
	for rows.Next() {
		pack, err := scanOfflinePack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pack)
	}
	return out, rows.Err()
}

// VisibleMediaBatch returns the subset of ids that are visible Media under the
// filter (Reveal/Sealed applied), keyed by id. It is the public seam the
// Offline Pack substrate (internal/offlinepack) uses to resolve and re-confirm
// pack membership without leaking hidden Media.
func (s *Store) VisibleMediaBatch(ctx context.Context, ids []MediaID, filter LibraryFilter) (map[MediaID]Media, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.visibleMediaBatchSQL(ctx, ids, filter)
}

// OfflinePackItems returns a pack's frozen Media list in order.
func (s *Store) OfflinePackItems(ctx context.Context, id OfflinePackID) ([]OfflinePackItem, error) {
	if id == "" {
		return nil, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT pack_id, media_id, sort_order, selected_version, plaintext_size, sha256, ciphertext_path
		FROM offline_pack_item
		WHERE pack_id = ?
		ORDER BY sort_order, media_id
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OfflinePackItem{}
	for rows.Next() {
		var item OfflinePackItem
		if err := rows.Scan(&item.PackID, &item.MediaID, &item.SortOrder, &item.SelectedVersion,
			&item.PlaintextSize, &item.SHA256, &item.CiphertextPath); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SetOfflinePackState transitions a pack's lifecycle state and refreshes
// updated_at. Used for pending → building → ready.
func (s *Store) SetOfflinePackState(ctx context.Context, id OfflinePackID, state OfflinePackState, now time.Time) (OfflinePack, error) {
	if id == "" {
		return OfflinePack{}, ValidationError("id is required")
	}
	if !validOfflinePackState(state) {
		return OfflinePack{}, ValidationError("invalid offline pack state")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflinePack{}, err
	}
	res, err := s.catalogW.ExecContext(ctx, `
		UPDATE offline_pack SET state = ?, updated_at_ms = ? WHERE id = ?
	`, state, now.UTC().UnixMilli(), id)
	if err != nil {
		return OfflinePack{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return OfflinePack{}, ErrOfflinePackNotFound
	}
	return s.offlinePackByIDLocked(ctx, id)
}

// SetOfflinePackProgress records reconnect progress sync: how many
// bytes of the pack have been fetched. The client resumes against bytes_ready.
func (s *Store) SetOfflinePackProgress(ctx context.Context, id OfflinePackID, bytesReady, bytesTotal int64, now time.Time) (OfflinePack, error) {
	if id == "" {
		return OfflinePack{}, ValidationError("id is required")
	}
	if err := validateOfflinePackProgress(bytesReady, bytesTotal); err != nil {
		return OfflinePack{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflinePack{}, err
	}
	res, err := s.catalogW.ExecContext(ctx, `
		UPDATE offline_pack SET bytes_ready = ?, bytes_total = ?, updated_at_ms = ? WHERE id = ?
	`, bytesReady, bytesTotal, now.UTC().UnixMilli(), id)
	if err != nil {
		return OfflinePack{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return OfflinePack{}, ErrOfflinePackNotFound
	}
	return s.offlinePackByIDLocked(ctx, id)
}

// NextOfflinePackGrantSequence atomically advances the durable grant sequence.
// Only a ready, unexpired caller-owned pack can advance; issuance signs the
// returned snapshot so concurrent refreshes never reuse or roll back a seq.
func (s *Store) NextOfflinePackGrantSequence(ctx context.Context, id OfflinePackID, profileID ProfileID, now time.Time) (OfflinePack, error) {
	if id == "" || profileID == "" {
		return OfflinePack{}, ValidationError("offline pack id and profile ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflinePack{}, err
	}
	nowMS := now.UTC().UnixMilli()
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE offline_pack
		SET grant_seq = grant_seq + 1, updated_at_ms = ?
		WHERE id = ? AND profile_id = ? AND state = ?
		  AND (expires_at_ms IS NULL OR expires_at_ms > ?)
		  AND device_attested = 1 AND manifest_revision > 0
	`, nowMS, id, profileID, OfflinePackReady, nowMS)
	if err != nil {
		return OfflinePack{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return OfflinePack{}, ErrOfflinePackNotFound
	}
	return s.offlinePackByIDLocked(ctx, id)
}

type OfflinePackGeneration string

const (
	OfflinePackGenerationGrant         OfflinePackGeneration = "grant"
	OfflinePackGenerationEnrollment    OfflinePackGeneration = "enrollment"
	OfflinePackGenerationProfile       OfflinePackGeneration = "profile"
	OfflinePackGenerationLibraryPolicy OfflinePackGeneration = "library_policy"
	OfflinePackGenerationBackup        OfflinePackGeneration = "backup"
)

// BumpOfflinePackGeneration invalidates the selected authority generation.
// The closed column lookup prevents callers from constructing SQL identifiers.
func (s *Store) BumpOfflinePackGeneration(ctx context.Context, id OfflinePackID, generation OfflinePackGeneration, now time.Time) (OfflinePack, error) {
	column, ok := map[OfflinePackGeneration]string{
		OfflinePackGenerationGrant:         "rev_grant",
		OfflinePackGenerationEnrollment:    "rev_enrollment",
		OfflinePackGenerationProfile:       "rev_profile",
		OfflinePackGenerationLibraryPolicy: "rev_library_policy",
		OfflinePackGenerationBackup:        "rev_backup",
	}[generation]
	if !ok {
		return OfflinePack{}, ValidationError("unknown offline pack generation")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflinePack{}, err
	}
	result, err := s.catalogW.ExecContext(ctx,
		"UPDATE offline_pack SET "+column+" = "+column+" + 1, updated_at_ms = ? WHERE id = ?",
		now.UTC().UnixMilli(), id)
	if err != nil {
		return OfflinePack{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return OfflinePack{}, ErrOfflinePackNotFound
	}
	return s.offlinePackByIDLocked(ctx, id)
}

// RevokeOfflinePack moves a pack to the revoked terminal state (server-side
// revoke, ADR-0098). The serving layer denies further bytes/key material for a
// revoked pack. Scoped to the owning Profile.
func (s *Store) RevokeOfflinePack(ctx context.Context, id OfflinePackID, profileID ProfileID, now time.Time) (OfflinePack, error) {
	if id == "" {
		return OfflinePack{}, ValidationError("id is required")
	}
	if profileID == "" {
		return OfflinePack{}, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflinePack{}, err
	}
	pack, err := s.offlinePackByIDLocked(ctx, id)
	if err != nil {
		return OfflinePack{}, err
	}
	if pack.ProfileID != profileID {
		return OfflinePack{}, ErrOfflinePackNotFound
	}
	nowMS := now.UTC().UnixMilli()
	if _, err := s.catalogW.ExecContext(ctx, `
		UPDATE offline_pack
		SET state = ?, revoked_at_ms = ?, updated_at_ms = ?, rev_grant = rev_grant + 1
		WHERE id = ?
	`, OfflinePackRevoked, nowMS, nowMS, id); err != nil {
		return OfflinePack{}, err
	}
	return s.offlinePackByIDLocked(ctx, id)
}

// ExpireOfflinePacks is the eviction/expiry sweep: any ready/pending/
// building pack whose expires_at has passed moves to the expired terminal
// state. Returns the count expired. Revoked packs are left untouched (already
// terminal). Idempotent.
func (s *Store) ExpireOfflinePacks(ctx context.Context, now time.Time) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	nowMS := now.UTC().UnixMilli()
	res, err := s.catalogW.ExecContext(ctx, `
		UPDATE offline_pack
		SET state = ?, updated_at_ms = ?
		WHERE expires_at_ms IS NOT NULL AND expires_at_ms <= ?
		  AND state NOT IN (?, ?)
	`, OfflinePackExpired, nowMS, nowMS, OfflinePackRevoked, OfflinePackExpired)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) offlinePackByIDLocked(ctx context.Context, id OfflinePackID) (OfflinePack, error) {
	pack, err := scanOfflinePack(s.catalogR.QueryRowContext(ctx, `
		SELECT p.id, p.profile_id, p.device_id, p.target_kind, p.target_ref, p.state,
		       p.manifest_json, p.bytes_total, p.bytes_ready, p.created_at_ms, p.updated_at_ms,
		       p.expires_at_ms, p.revoked_at_ms, COUNT(i.media_id),
		       p.device_enrollment_id, p.device_key_thumbprint, p.device_public_key, p.device_attested,
		       p.library_ids_json, p.manifest_revision, p.manifest_sha256, p.grant_seq,
		       p.rev_grant, p.rev_enrollment, p.rev_profile, p.rev_library_policy, p.rev_backup,
		       p.wrapped_cek
		FROM offline_pack p
		LEFT JOIN offline_pack_item i ON i.pack_id = p.id
		WHERE p.id = ?
		GROUP BY p.id
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return OfflinePack{}, ErrOfflinePackNotFound
	}
	return pack, err
}

type offlinePackScanner interface {
	Scan(dest ...any) error
}

func scanOfflinePack(scanner offlinePackScanner) (OfflinePack, error) {
	var pack OfflinePack
	var expiresAt, revokedAt sql.NullInt64
	var createdAtMS, updatedAtMS int64
	var deviceAttested int
	var libraryIDsJSON string
	if err := scanner.Scan(&pack.ID, &pack.ProfileID, &pack.DeviceID, &pack.TargetKind, &pack.TargetRef,
		&pack.State, &pack.Manifest, &pack.BytesTotal, &pack.BytesReady, &createdAtMS, &updatedAtMS,
		&expiresAt, &revokedAt, &pack.ItemCount, &pack.DeviceEnrollmentID, &pack.DeviceKeyThumbprint,
		&pack.DevicePublicKey, &deviceAttested, &libraryIDsJSON, &pack.ManifestRevision,
		&pack.ManifestSHA256, &pack.GrantSequence, &pack.RevGrant, &pack.RevEnrollment,
		&pack.RevProfile, &pack.RevLibraryPolicy, &pack.RevBackup, &pack.WrappedCEK); err != nil {
		return OfflinePack{}, err
	}
	pack.DeviceAttested = deviceAttested != 0
	if err := json.Unmarshal([]byte(libraryIDsJSON), &pack.LibraryIDs); err != nil {
		return OfflinePack{}, err
	}
	pack.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	pack.UpdatedAt = time.UnixMilli(updatedAtMS).UTC()
	if expiresAt.Valid {
		t := time.UnixMilli(expiresAt.Int64).UTC()
		pack.ExpiresAt = &t
	}
	if revokedAt.Valid {
		t := time.UnixMilli(revokedAt.Int64).UTC()
		pack.RevokedAt = &t
	}
	return pack, nil
}

func millisOrNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().UnixMilli()
}

func validOfflinePackState(state OfflinePackState) bool {
	switch state {
	case "", OfflinePackPending, OfflinePackBuilding, OfflinePackReady, OfflinePackRevoked, OfflinePackExpired:
		return true
	default:
		return false
	}
}

func validateOfflinePackProgress(bytesReady, bytesTotal int64) error {
	if bytesReady < 0 || bytesTotal < 0 {
		return ValidationError("byte counts must be non-negative")
	}
	if bytesReady > bytesTotal {
		return ValidationError("bytes ready must not exceed bytes total")
	}
	return nil
}

func randomOfflinePackID() (OfflinePackID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return OfflinePackID("offline-pack-" + hex.EncodeToString(raw[:])), nil
}

func nonzeroGeneration(value uint64) uint64 {
	if value == 0 {
		return 1
	}
	return value
}

func nonnilBytes(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}
