// Package offlinepack is the generic, domain-agnostic Offline Pack substrate
// (ADR-0098 area 6). Offline content uses the Offline Pack regime, not
// exportable downloads. This package owns the server spine: pack rows/items,
// distinct Offline Pack grants/manifests/status envelopes, per-target validation
// through a pluggable TargetResolver registry, and encrypted BFKW/BFOP serving
// with the live revoke/relock/seal byte-denial check.
//
// It carries NO domain vocabulary. A target is an opaque (kind, ref): a
// registered TargetResolver translates it into the visible Media that belong in
// the pack. The Music target resolvers (Albums, Playlists/Collections, explicit
// Tracks, frozen Stations) live in the Music plugin and register here via a core adapter — the
// one place music words touch the pack substrate. No provider catalog item can
// become a pack item: a resolver returns store.MediaID values, and pack items
// FK to the media table, so only Beamfall Media ever enters a pack.
package offlinepack

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// ErrUnknownTargetKind is returned when no resolver is registered for a target
// kind.
var ErrUnknownTargetKind = errors.New("offline pack: unknown target kind")

// ErrEmptyTarget is returned when a target resolves to zero visible Media (e.g.
// every member is hidden/sealed for the caller, or a Station generator returned
// nothing). No pack row is created in that case.
var ErrEmptyTarget = errors.New("offline pack: target resolved to no visible media")

// TargetKindMedia is the built-in generic resolver kind: an explicit list of
// Media ids (the domain-agnostic "pack these specific items" target).
const TargetKindMedia = "media"

// BuildJobKind is the ADR-0013 job-substrate kind for Offline Pack packaging.
const BuildJobKind store.JobKind = "offline-pack-build"

// DefaultBuildByteBudget bounds the plaintext bytes committed by one build.
// It is deliberately independent of the number and size of requested items.
const DefaultBuildByteBudget uint64 = 4 << 30

// ExpireJobKind runs the server-side Offline Pack eviction/expiry sweep.
const ExpireJobKind store.JobKind = "offline-pack-expire"

// ResolvedTarget is the output of a TargetResolver: the ordered visible Media
// that belong in the pack plus a manifest-safe display label. The resolver MUST
// have applied Reveal/Sealed filtering already (hidden/sealed Media excluded),
// and Label must not leak tombstone text, hidden Library names, or counts.
type ResolvedTarget struct {
	MediaIDs []store.MediaID
	Label    string
}

// TargetResolver is the plugin surface. A resolver for a target kind translates
// an opaque target ref into the visible Media that belong in the pack, applying
// the caller's Reveal/Sealed filter. Resolvers live in domain packages
// (e.g. the Music plugin, via a core adapter) and register into a Service.
type TargetResolver interface {
	Resolve(ctx context.Context, targetRef string, filter store.LibraryFilter) (ResolvedTarget, error)
}

// Store is the persistence seam the Service needs (satisfied by *store.Store).
// Narrow by intent so the package's store coupling is explicit.
type Store interface {
	CreateOfflinePack(ctx context.Context, pack store.OfflinePack, items []store.OfflinePackItem, now time.Time) (store.OfflinePack, error)
	OfflinePackByID(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID) (store.OfflinePack, error)
	OfflinePacksForProfile(ctx context.Context, profileID store.ProfileID) ([]store.OfflinePack, error)
	OfflinePackItems(ctx context.Context, id store.OfflinePackID) ([]store.OfflinePackItem, error)
	RevokeOfflinePack(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID, now time.Time) (store.OfflinePack, error)
	ExpireOfflinePacks(ctx context.Context, now time.Time) (int, error)
	SetOfflinePackProgress(ctx context.Context, id store.OfflinePackID, bytesReady, bytesTotal int64, now time.Time) (store.OfflinePack, error)
	NextOfflinePackGrantSequence(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID, now time.Time) (store.OfflinePack, error)
	BumpOfflinePackGeneration(ctx context.Context, id store.OfflinePackID, generation store.OfflinePackGeneration, now time.Time) (store.OfflinePack, error)
	VisibleMediaBatch(ctx context.Context, ids []store.MediaID, filter store.LibraryFilter) (map[store.MediaID]store.Media, error)
	PrimaryMediaFile(ctx context.Context, id store.MediaID, filter store.LibraryFilter) (store.MediaFile, error)
	MediaFilesForMedia(ctx context.Context, id store.MediaID, filter store.LibraryFilter) ([]store.MediaFile, error)
	Libraries(ctx context.Context, filter store.LibraryFilter) ([]store.Library, error)
}

type DeviceKeyAttestationVerifier interface {
	VerifyDeviceKey(ctx context.Context, enrollmentID string, publicKey, attestation []byte) error
}

// SealedPredicate reports whether a Library is currently Sealed (ADR-0098 area
// 2). Sealed state is runtime, in-memory, and owned by the encryption seam
// (internal/libencrypt), so the composition root wires this off
// libencrypt.Manager. A Sealed Library serves NOTHING: its Media must never be
// frozen into a pack manifest (build time) and a previously-built pack with a
// member sealed AFTER issue is denied at serve time. nil (the default) treats
// no Library as sealed, so deployments without library encryption pay nothing.
// Domain-agnostic — the seam names no music type.
type SealedPredicate func(libraryID store.LibraryID) bool

// EncryptedContentSource opens libencrypt's shared seekable chunked-AEAD
// plaintext view. Every open carries the object's immutable identity as plain
// strings (object class + persisted object id — TECHDUR-CORE-CRYPTO-1); the
// composition root adapts them onto libencrypt's ObjectContext so this package
// stays decoupled from the crypto layer.
type EncryptedContentSource interface {
	OpenDecryptReader(libraryID, ciphertextPath, objectClass, immutableObjectID string) (io.ReadSeekCloser, error)
}

// ObjectContextResolver derives the immutable object identity (class +
// persisted object id) for one encrypted media file from persisted identifiers
// (the library's media-file inventory); the path is only the lookup key, never
// the identity. The composition root wires it over the store.
type ObjectContextResolver func(libraryID store.LibraryID, path string) (objectClass, immutableObjectID string, err error)

// EncryptedPredicate reports whether a Library's file paths name libencrypt
// ciphertext. Keeping this distinct from SealedPredicate matters: an Unsealed
// encrypted Library may serve decrypted bytes, while a plain Library must still
// be opened directly.
type EncryptedPredicate func(libraryID store.LibraryID) bool

// Service builds and serves Offline Packs.
type Service struct {
	store           Store
	authority       *Authority
	now             func() time.Time
	resolvers       map[string]TargetResolver
	sealed          SealedPredicate
	encrypted       EncryptedPredicate
	content         EncryptedContentSource
	attester        DeviceKeyAttestationVerifier
	packRoot        string
	buildByteBudget uint64
	deviceMu        sync.RWMutex
	devices         map[string]deviceRegistration
	// resolveContext binds each encrypted open to its immutable object identity.
	resolveContext ObjectContextResolver
}

// NewService creates an Offline Pack service with the built-in generic "media"
// resolver registered. Music (and any other domain) resolvers register via
// RegisterResolver from the composition root.
func NewService(st Store, authority *Authority, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	s := &Service{
		store:           st,
		authority:       authority,
		now:             now,
		resolvers:       map[string]TargetResolver{},
		devices:         map[string]deviceRegistration{},
		buildByteBudget: DefaultBuildByteBudget,
	}
	s.resolvers[TargetKindMedia] = explicitMediaResolver{store: st}
	return s
}

// BuildPackRequest names a pack to build.
type BuildPackRequest struct {
	TargetKind        string
	TargetRef         string
	ExpiresAt         *time.Time
	DevicePublicKey   []byte
	DeviceAttestation []byte
	SelectedVersions  map[store.MediaID]string
}

// BuildJobPayload is the durable payload for BuildJobKind. It carries the
// already-authorized caller scope because jobs run after the HTTP request exits.
type BuildJobPayload struct {
	TargetKind        string                          `json:"targetKind"`
	TargetRef         string                          `json:"targetRef"`
	ExpiresAt         *time.Time                      `json:"expiresAt,omitempty"`
	SessionID         store.SessionID                 `json:"sessionID"`
	DeviceID          store.DeviceID                  `json:"deviceID"`
	DeviceRole        store.DeviceRole                `json:"deviceRole"`
	ProfileID         store.ProfileID                 `json:"profileID"`
	Profile           store.ProfileKind               `json:"profile"`
	Revealed          bool                            `json:"revealed"`
	LibraryIDs        []store.LibraryID               `json:"libraryIDs,omitempty"`
	AllLibraries      bool                            `json:"allLibraries,omitempty"`
	Certification     []store.CertificationConstraint `json:"certification,omitempty"`
	DevicePublicKey   []byte                          `json:"devicePublicKey"`
	DeviceAttestation []byte                          `json:"deviceAttestation"`
	SelectedVersions  map[store.MediaID]string        `json:"selectedVersions,omitempty"`
}

// BuildJobPayloadFor snapshots the caller's pack authority into a job payload.
func BuildJobPayloadFor(scope auth.Scope, req BuildPackRequest) ([]byte, error) {
	return json.Marshal(BuildJobPayload{
		TargetKind:        req.TargetKind,
		TargetRef:         req.TargetRef,
		ExpiresAt:         req.ExpiresAt,
		SessionID:         scope.SessionID,
		DeviceID:          scope.DeviceID,
		DeviceRole:        scope.DeviceRole,
		ProfileID:         scope.ProfileID,
		Profile:           scope.Profile,
		Revealed:          scope.Revealed,
		LibraryIDs:        append([]store.LibraryID(nil), scope.LibraryIDs...),
		AllLibraries:      scope.AllLibraries,
		Certification:     append([]store.CertificationConstraint(nil), scope.Certification...),
		DevicePublicKey:   append([]byte(nil), req.DevicePublicKey...),
		DeviceAttestation: append([]byte(nil), req.DeviceAttestation...),
		SelectedVersions:  req.SelectedVersions,
	})
}

// HandleBuildJob executes one ADR-0013 Offline Pack packaging job.
func (s *Service) HandleBuildJob(ctx context.Context, payload []byte) (store.OfflinePack, error) {
	var job BuildJobPayload
	if err := json.Unmarshal(payload, &job); err != nil {
		return store.OfflinePack{}, err
	}
	return s.BuildPack(ctx, auth.Scope{
		SessionID:     job.SessionID,
		DeviceID:      job.DeviceID,
		DeviceRole:    job.DeviceRole,
		ProfileID:     job.ProfileID,
		Profile:       job.Profile,
		Revealed:      job.Revealed,
		LibraryIDs:    append([]store.LibraryID(nil), job.LibraryIDs...),
		AllLibraries:  job.AllLibraries,
		Certification: append([]store.CertificationConstraint(nil), job.Certification...),
	}, BuildPackRequest{
		TargetKind:        job.TargetKind,
		TargetRef:         job.TargetRef,
		ExpiresAt:         job.ExpiresAt,
		DevicePublicKey:   append([]byte(nil), job.DevicePublicKey...),
		DeviceAttestation: append([]byte(nil), job.DeviceAttestation...),
		SelectedVersions:  job.SelectedVersions,
	})
}

// BuildPack is the server-only ADR-0157/0208 producer. It resolves an audio-only
// target, verifies the enrolled device key attestation, selects one immutable
// Version per Media, seals BFOP bytes, wraps the per-revision CEK, signs the
// manifest, and persists the authority snapshot atomically.
func (s *Service) BuildPack(ctx context.Context, scope auth.Scope, req BuildPackRequest) (store.OfflinePack, error) {
	if s.authority == nil || s.attester == nil || s.packRoot == "" {
		return store.OfflinePack{}, errors.New("offline pack authority, attestation verifier, and pack root are required")
	}
	if scope.ProfileID == "" || scope.DeviceID == "" {
		return store.OfflinePack{}, errors.New("offline pack requires an enrolled Profile device")
	}
	if len(req.DevicePublicKey) == 0 {
		s.deviceMu.RLock()
		registered := s.devices[string(scope.DeviceID)]
		s.deviceMu.RUnlock()
		req.DevicePublicKey = registered.publicKey
		req.DeviceAttestation = registered.attestation
	}
	deviceKID, err := DeviceKID(req.DevicePublicKey)
	if err != nil {
		return store.OfflinePack{}, err
	}
	if len(req.DeviceAttestation) == 0 {
		return store.OfflinePack{}, errors.New("offline pack device-key attestation is required")
	}
	if err := s.attester.VerifyDeviceKey(ctx, string(scope.DeviceID), req.DevicePublicKey, req.DeviceAttestation); err != nil {
		return store.OfflinePack{}, fmt.Errorf("offline pack device-key attestation: %w", err)
	}
	now := s.now().UTC()
	expiry, err := packExpiry(now, req.ExpiresAt)
	if err != nil {
		return store.OfflinePack{}, err
	}
	resolver, ok := s.resolvers[req.TargetKind]
	if !ok {
		return store.OfflinePack{}, fmt.Errorf("%w: %q", ErrUnknownTargetKind, req.TargetKind)
	}
	filter, err := s.filter(ctx, scope)
	if err != nil {
		return store.OfflinePack{}, err
	}
	resolved, err := resolver.Resolve(ctx, req.TargetRef, filter)
	if err != nil {
		return store.OfflinePack{}, err
	}
	if len(resolved.MediaIDs) == 0 {
		return store.OfflinePack{}, ErrEmptyTarget
	}
	if len(resolved.MediaIDs) > MaxManifestItems {
		return store.OfflinePack{}, errors.New("offline pack exceeds item bound")
	}
	// Defense in depth: re-confirm every resolved id is visible Media before it
	// is frozen, and build the manifest from those rows so titles are the
	// catalog's (never resolver-supplied free text). A resolver that returned a
	// hidden id (a bug) is dropped here, not written into the pack.
	visible, err := s.store.VisibleMediaBatch(ctx, resolved.MediaIDs, filter)
	if err != nil {
		return store.OfflinePack{}, err
	}
	sources, bytesTotal, err := s.preflightBuildSources(ctx, resolved.MediaIDs, visible, filter, req.SelectedVersions)
	if err != nil {
		return store.OfflinePack{}, err
	}
	storageBytesTotal, err := uint64ToInt64(bytesTotal)
	if err != nil {
		return store.OfflinePack{}, err
	}
	if len(sources) == 0 {
		return store.OfflinePack{}, ErrEmptyTarget
	}
	packID, err := newOfflinePackID()
	if err != nil {
		return store.OfflinePack{}, err
	}
	packDir, err := s.packDir(packID)
	if err != nil {
		return store.OfflinePack{}, err
	}
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		return store.OfflinePack{}, err
	}
	keepPackDir := false
	defer func() {
		if !keepPackDir {
			_ = os.RemoveAll(packDir)
		}
	}()

	items := make([]store.OfflinePackItem, 0, len(sources))
	manifestItems := make([]ManifestItem, 0, len(sources))
	librarySet := map[string]struct{}{}
	for _, source := range sources {
		sum, err := s.hashPackSource(source.libraryID, source.path, source.storageSize)
		if err != nil {
			return store.OfflinePack{}, err
		}
		digest := base64.RawURLEncoding.EncodeToString(sum[:])
		sortOrder := len(items) + 1
		items = append(items, store.OfflinePackItem{
			MediaID: source.mediaID, SortOrder: sortOrder, SelectedVersion: source.selectedVersion,
			PlaintextSize: source.storageSize, SHA256: digest,
		})
		manifestItems = append(manifestItems, ManifestItem{
			MediaID: string(source.mediaID), SelectedVersion: source.selectedVersion,
			Size: source.size, SHA256: digest,
		})
		librarySet[string(source.libraryID)] = struct{}{}
	}
	libraryIDs := make([]string, 0, len(librarySet))
	for id := range librarySet {
		libraryIDs = append(libraryIDs, id)
	}
	slices.Sort(libraryIDs)
	const revision = uint64(1)
	binding := ManifestBinding{
		V: 1, Iss: s.authority.Issuer(), Aud: OfflinePackAudience,
		PackID: string(packID), ProfileID: string(scope.ProfileID), LibraryIDs: libraryIDs,
		DeviceEnrollmentID: string(scope.DeviceID), DeviceKeyThumbprint: deviceKID,
		ManifestRevision: revision, PackExpiresAt: expiry.Unix(), Items: manifestItems,
		WrapSuite: WrapSuite,
	}
	manifestDigest, err := ManifestSHA256(binding)
	if err != nil {
		return store.OfflinePack{}, err
	}
	manifestDigestRaw, _ := base64.RawURLEncoding.DecodeString(manifestDigest)
	cek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, cek); err != nil {
		return store.OfflinePack{}, err
	}
	aad, err := BuildWrapAAD(WrapAADFields{
		Issuer: s.authority.Issuer(), PackID: string(packID), ProfileID: string(scope.ProfileID),
		DeviceEnrollmentID: string(scope.DeviceID), DeviceKeyThumbprint: deviceKID,
		ManifestSHA256: manifestDigest, ManifestRevision: "1",
	})
	if err != nil {
		return store.OfflinePack{}, err
	}
	envelope, err := WrapCEK(req.DevicePublicKey, aad, cek)
	if err != nil {
		return store.OfflinePack{}, err
	}
	envelopeDigest, err := WrappedEnvelopeSHA256(envelope)
	if err != nil {
		return store.OfflinePack{}, err
	}
	for i, source := range sources {
		reader, err := s.openContent(source.libraryID, source.path)
		if err != nil {
			return store.OfflinePack{}, err
		}
		path := filepath.Join(packDir, fmt.Sprintf("%04d.bfop", i))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // validated server-owned pack directory and generated filename
		if err != nil {
			_ = reader.Close()
			return store.OfflinePack{}, err
		}
		digest, err := base64.RawURLEncoding.DecodeString(items[i].SHA256)
		if err != nil {
			_ = file.Close()
			_ = reader.Close()
			return store.OfflinePack{}, err
		}
		encodeErr := EncryptPackItemTo(file, cek, manifestDigestRaw, uint32(i), source.size, digest, reader)
		closeFileErr := file.Close()
		closeReaderErr := reader.Close()
		if encodeErr != nil {
			return store.OfflinePack{}, encodeErr
		}
		if closeFileErr != nil {
			return store.OfflinePack{}, closeFileErr
		}
		if closeReaderErr != nil {
			return store.OfflinePack{}, closeReaderErr
		}
		items[i].CiphertextPath = path
	}
	scopeTemplate := GrantClaims{
		V: 1, Iss: s.authority.Issuer(), Aud: OfflinePackAudience,
		PackID: string(packID), ProfileID: string(scope.ProfileID), LibraryIDs: libraryIDs,
		DeviceEnrollmentID: string(scope.DeviceID), DeviceKeyThumbprint: deviceKID,
		ManifestRevision: revision, ManifestSHA256: manifestDigest,
		Rights: []string{OfflinePlayRight},
	}
	manifestClaims := ManifestClaims{
		V: binding.V, Iss: binding.Iss, Aud: binding.Aud, PackID: binding.PackID,
		ProfileID: binding.ProfileID, LibraryIDs: binding.LibraryIDs,
		DeviceEnrollmentID: binding.DeviceEnrollmentID, DeviceKeyThumbprint: binding.DeviceKeyThumbprint,
		ManifestRevision: binding.ManifestRevision, ManifestSHA256: manifestDigest,
		GrantScopeSHA256: GrantScopeSHA256(scopeTemplate), PackExpiresAt: binding.PackExpiresAt,
		Items: manifestItems, Wrap: ManifestWrap{
			Suite: WrapSuite, DeviceKID: deviceKID,
			Envelope: base64.RawURLEncoding.EncodeToString(envelope), EnvelopeSHA256: envelopeDigest,
		},
	}
	manifestJWS, err := s.authority.SignManifest(manifestClaims)
	if err != nil {
		return store.OfflinePack{}, err
	}
	storeLibraryIDs := make([]store.LibraryID, len(libraryIDs))
	for i := range libraryIDs {
		storeLibraryIDs[i] = store.LibraryID(libraryIDs[i])
	}
	pack := store.OfflinePack{
		ID:                  packID,
		ProfileID:           scope.ProfileID,
		DeviceID:            scope.DeviceID,
		TargetKind:          req.TargetKind,
		TargetRef:           req.TargetRef,
		State:               store.OfflinePackReady,
		Manifest:            manifestJWS,
		BytesTotal:          storageBytesTotal,
		BytesReady:          storageBytesTotal,
		ExpiresAt:           &expiry,
		DeviceEnrollmentID:  string(scope.DeviceID),
		DeviceKeyThumbprint: deviceKID,
		DevicePublicKey:     append([]byte(nil), req.DevicePublicKey...),
		DeviceAttested:      true,
		LibraryIDs:          storeLibraryIDs,
		ManifestRevision:    revision,
		ManifestSHA256:      manifestDigest,
		RevGrant:            1, RevEnrollment: 1, RevProfile: 1, RevLibraryPolicy: 1, RevBackup: 1,
		WrappedCEK: append([]byte(nil), envelope...),
	}
	created, err := s.store.CreateOfflinePack(ctx, pack, items, now)
	if err != nil {
		return store.OfflinePack{}, err
	}
	keepPackDir = true
	return created, nil
}
