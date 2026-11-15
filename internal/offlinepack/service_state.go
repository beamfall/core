package offlinepack

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Revoke moves a pack to the revoked terminal state. The serving layer then
// denies further bytes/key material for it.
func (s *Service) Revoke(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID) (store.OfflinePack, error) {
	pack, err := s.store.RevokeOfflinePack(ctx, id, profileID, s.now())
	if err == nil && s.packRoot != "" {
		if packDir, pathErr := s.packDir(id); pathErr == nil {
			_ = os.RemoveAll(packDir)
		}
	}
	return pack, err
}

// ExpireDue runs the eviction/expiry sweep, returning the count expired.
func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	return s.store.ExpireOfflinePacks(ctx, s.now())
}

// HandleExpireJob executes the ADR-0013 Offline Pack expiry/eviction sweep.
func (s *Service) HandleExpireJob(ctx context.Context) (int, error) {
	return s.ExpireDue(ctx)
}

// SyncProgress records client reconnect progress against a caller-owned pack.
func (s *Service) SyncProgress(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID, bytesReady, bytesTotal int64) (store.OfflinePack, error) {
	pack, err := s.store.OfflinePackByID(ctx, id, profileID)
	if err != nil {
		return store.OfflinePack{}, err
	}
	if bytesTotal < bytesReady {
		bytesTotal = bytesReady
	}
	return s.store.SetOfflinePackProgress(ctx, pack.ID, bytesReady, bytesTotal, s.now())
}

// Pack returns a caller-owned pack.
func (s *Service) Pack(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID) (store.OfflinePack, error) {
	return s.store.OfflinePackByID(ctx, id, profileID)
}

// Packs lists the caller's packs.
func (s *Service) Packs(ctx context.Context, profileID store.ProfileID) ([]store.OfflinePack, error) {
	return s.store.OfflinePacksForProfile(ctx, profileID)
}

// IssuePackGrant creates or refreshes the distinct Offline Pack credential.
// Sequence allocation is durable and monotonic; clients never mint, extend, or
// re-scope this envelope.
func (s *Service) IssuePackGrant(ctx context.Context, scope auth.Scope, id store.OfflinePackID) (string, error) {
	if s.authority == nil {
		return "", errors.New("offline pack authority is required")
	}
	now := s.now().UTC()
	pack, err := s.store.NextOfflinePackGrantSequence(ctx, id, scope.ProfileID, now)
	if err != nil {
		return "", err
	}
	if !s.servable(pack) || pack.DeviceEnrollmentID != string(scope.DeviceID) || !pack.DeviceAttested {
		return "", ErrPackNotServable
	}
	jti, err := NewJTI()
	if err != nil {
		return "", err
	}
	libraryIDs := make([]string, len(pack.LibraryIDs))
	for i := range pack.LibraryIDs {
		libraryIDs[i] = string(pack.LibraryIDs[i])
	}
	expiry := now.Add(GrantLifetime)
	if pack.ExpiresAt != nil && pack.ExpiresAt.Before(expiry) {
		expiry = *pack.ExpiresAt
	}
	return s.authority.SignGrant(GrantClaims{
		V: 1, Iss: s.authority.Issuer(), Aud: OfflinePackAudience, JTI: jti,
		Seq: pack.GrantSequence, PackID: string(pack.ID), ProfileID: string(pack.ProfileID),
		LibraryIDs: libraryIDs, DeviceEnrollmentID: pack.DeviceEnrollmentID,
		DeviceKeyThumbprint: pack.DeviceKeyThumbprint, ManifestRevision: pack.ManifestRevision,
		ManifestSHA256: pack.ManifestSHA256, Rights: []string{OfflinePlayRight},
		Iat: now.Unix(), Nbf: now.Unix(), Exp: expiry.Unix(), Rev: packRevocations(pack),
	})
}

func (s *Service) IssueStatus(ctx context.Context, scope auth.Scope, id store.OfflinePackID) (string, error) {
	if s.authority == nil {
		return "", errors.New("offline pack authority is required")
	}
	pack, err := s.store.OfflinePackByID(ctx, id, scope.ProfileID)
	if err != nil {
		return "", err
	}
	if pack.DeviceEnrollmentID != string(scope.DeviceID) {
		return "", ErrPackNotServable
	}
	state := protocolState(pack.State)
	destroy := state == "revoked" || state == "expired"
	return s.authority.SignStatus(StatusClaims{
		V: 1, Iss: s.authority.Issuer(), Aud: OfflinePackAudience,
		PackID: string(pack.ID), DeviceEnrollmentID: pack.DeviceEnrollmentID,
		ManifestRevision: pack.ManifestRevision, ServerTime: s.now().UTC().Unix(),
		State: state, Rev: packRevocations(pack), Destroy: destroy,
	})
}

func (s *Service) Invalidate(ctx context.Context, id store.OfflinePackID, generation store.OfflinePackGeneration) (store.OfflinePack, error) {
	pack, err := s.store.BumpOfflinePackGeneration(ctx, id, generation, s.now())
	if err == nil && s.packRoot != "" {
		if packDir, pathErr := s.packDir(id); pathErr == nil {
			_ = os.RemoveAll(packDir)
		}
	}
	return pack, err
}

func (s *Service) packDir(id store.OfflinePackID) (string, error) {
	name := string(id)
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || filepath.IsAbs(name) {
		return "", errors.New("offline pack ID is not a safe path component")
	}
	return filepath.Join(s.packRoot, name), nil
}

func (s *Service) selectedVersionFile(ctx context.Context, id store.MediaID, filter store.LibraryFilter, selected string) (store.MediaFile, error) {
	files, err := s.store.MediaFilesForMedia(ctx, id, filter)
	if err != nil {
		return store.MediaFile{}, err
	}
	var matches []store.MediaFile
	for _, file := range files {
		if file.VersionLabel == selected {
			matches = append(matches, file)
		}
	}
	if len(matches) != 1 {
		return store.MediaFile{}, errors.New("offline pack selected Version must resolve to exactly one file")
	}
	return matches[0], nil
}

type packBuildSource struct {
	mediaID         store.MediaID
	libraryID       store.LibraryID
	path            string
	selectedVersion string
	size            uint64
	storageSize     int64
	// transcode marks a video item to transcode before hashing; after the
	// transcode, path names the temporary MP4 in the pack directory.
	transcode  bool
	transcoded bool
	// streams is the source file's stream inventory, read for transcodes.
	streams []store.MediaFileStream
}

func (s *Service) preflightBuildSources(ctx context.Context, ids []store.MediaID, visible map[store.MediaID]store.Media, filter store.LibraryFilter, selectedVersions map[store.MediaID]string, quality string) ([]packBuildSource, error) {
	sources := make([]packBuildSource, 0, len(ids))
	var total uint64
	for _, id := range ids {
		media, ok := visible[id]
		if !ok {
			continue
		}
		if media.Class != store.MediaClassAudio && media.Class != store.MediaClassVideo {
			return nil, errors.New("offline pack supports audio and video Media only")
		}
		transcode := media.Class == store.MediaClassVideo && quality != QualityOriginal
		if transcode && s.encrypted != nil && s.encrypted(media.LibraryID) {
			return nil, errors.New("offline pack cannot transcode Media in an encrypted Library")
		}
		file, err := s.selectedVersionFile(ctx, id, filter, selectedVersions[id])
		if err != nil {
			return nil, err
		}
		source := packBuildSource{
			mediaID: id, libraryID: media.LibraryID, path: file.Path,
			selectedVersion: file.VersionLabel, transcode: transcode,
		}
		if transcode {
			source.streams, err = s.store.MediaFileStreams(ctx, file.ContentFingerprint)
			if err != nil {
				return nil, err
			}
		} else {
			// Fail before any transcode work when original bytes already break
			// a bound; checkBuildBytes re-checks the final set.
			size, err := s.catalogPlaintextSize(media.LibraryID, file)
			if err != nil {
				return nil, err
			}
			if size > MaxItemSize {
				if media.Class == store.MediaClassVideo {
					return nil, ErrOriginalItemTooLarge
				}
				return nil, errors.New("offline pack item exceeds size bound")
			}
			if size > s.buildByteBudget-total {
				return nil, errors.New("offline pack exceeds cumulative byte budget")
			}
			total += size
			source.size = size
		}
		sources = append(sources, source)
	}
	return sources, nil
}

// transcodeSources replaces each marked source with its transcode in packDir
// and records the transcoded size.
func (s *Service) transcodeSources(ctx context.Context, sources []packBuildSource, packDir, quality string) error {
	for i := range sources {
		if !sources[i].transcode {
			continue
		}
		if s.transcode == nil {
			return errors.New("offline pack transcoder is not configured")
		}
		dst := filepath.Join(packDir, fmt.Sprintf("%04d.src.mp4", i))
		if err := s.transcode(ctx, sources[i].path, dst, quality, sources[i].streams); err != nil {
			_ = os.Remove(dst)
			// A transcoder may flatten a cancellation into its own error. Keep
			// shutdown and preemption visible so the job yields and reruns under
			// the same pack ID instead of failing.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("offline pack transcode interrupted: %w", ctxErr)
			}
			return fmt.Errorf("offline pack transcode: %w", err)
		}
		sources[i].path = dst
		sources[i].transcoded = true
		info, err := os.Stat(dst)
		if err != nil {
			return err
		}
		size := info.Size()
		if size <= 0 {
			return errors.New("offline pack transcode produced no bytes")
		}
		sources[i].size = uint64(size)
	}
	return nil
}

// checkBuildBytes applies the per-item bound and the build byte budget to the
// bytes that will be sealed, and fills each source's signed storage size.
func (s *Service) checkBuildBytes(sources []packBuildSource) (uint64, error) {
	var total uint64
	for i := range sources {
		size := sources[i].size
		if size > MaxItemSize {
			return 0, errors.New("offline pack item exceeds size bound")
		}
		if size > s.buildByteBudget-total {
			return 0, errors.New("offline pack exceeds cumulative byte budget")
		}
		storageSize, err := uint64ToInt64(size)
		if err != nil {
			return 0, err
		}
		sources[i].storageSize = storageSize
		total += size
	}
	return total, nil
}

// removeTranscodes deletes the temporary transcodes once they are sealed.
func removeTranscodes(sources []packBuildSource) {
	for _, source := range sources {
		if source.transcoded {
			_ = os.Remove(source.path)
		}
	}
}

func (s *Service) catalogPlaintextSize(libraryID store.LibraryID, file store.MediaFile) (uint64, error) {
	if file.SizeBytes > 0 {
		return uint64(file.SizeBytes), nil
	}
	if s.encrypted != nil && s.encrypted(libraryID) {
		return 0, errors.New("offline pack encrypted item is missing catalog size")
	}
	info, err := os.Stat(file.Path)
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size < 0 {
		return 0, errors.New("offline pack item has invalid catalog size")
	}
	return uint64(size), nil
}

func uint64ToInt64(value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, errors.New("offline pack byte count exceeds signed storage range")
	}
	return int64(value), nil
}

func (s *Service) hashPackSource(libraryID store.LibraryID, path string, size int64) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	reader, err := s.openContent(libraryID, path)
	if err != nil {
		return digest, err
	}
	hash := sha256.New()
	written, readErr := io.Copy(hash, io.LimitReader(reader, size+1))
	closeErr := reader.Close()
	if readErr != nil {
		return digest, readErr
	}
	if closeErr != nil {
		return digest, closeErr
	}
	if written != size {
		return digest, errors.New("offline pack item size differs from catalog")
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func packExpiry(now time.Time, requested *time.Time) (time.Time, error) {
	expiry := now.Add(GrantLifetime)
	if requested != nil {
		expiry = requested.UTC()
	}
	if !expiry.After(now) || expiry.After(now.Add(GrantLifetime)) {
		return time.Time{}, errors.New("offline pack expiry must be within 30 days")
	}
	return expiry, nil
}

// NewPackID returns a fresh random Offline Pack ID.
func NewPackID() (store.OfflinePackID, error) {
	var value [16]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", err
	}
	return store.OfflinePackID("offline-pack-" + fmt.Sprintf("%x", value[:])), nil
}

// ValidPackID reports whether id has the shape NewPackID produces.
func ValidPackID(id store.OfflinePackID) bool {
	hex, ok := strings.CutPrefix(string(id), "offline-pack-")
	if !ok || len(hex) != 32 {
		return false
	}
	for _, c := range hex {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func packRevocations(pack store.OfflinePack) RevocationGenerations {
	return RevocationGenerations{
		Grant: pack.RevGrant, Enrollment: pack.RevEnrollment, Profile: pack.RevProfile,
		LibraryPolicy: pack.RevLibraryPolicy, Backup: pack.RevBackup,
	}
}

func protocolState(state store.OfflinePackState) string {
	switch state {
	case store.OfflinePackPending:
		return "queued"
	case store.OfflinePackBuilding:
		return "downloading"
	case store.OfflinePackReady:
		return "ready"
	case store.OfflinePackRevoked:
		return "revoked"
	case store.OfflinePackExpired:
		return "expired"
	default:
		return "failed"
	}
}

// servable reports whether a pack's stored state permits byte/material serving.
// Revoked and expired packs are never servable; the eviction sweep may not have
// run yet, so a ready pack whose expiry has passed is also treated as not
// servable here.
func (s *Service) servable(pack store.OfflinePack) bool {
	if pack.State != store.OfflinePackReady {
		return false
	}
	if pack.ExpiresAt != nil && !pack.ExpiresAt.After(s.now()) {
		return false
	}
	return true
}

// packRestricted reports whether any frozen member is in a restricted Library
// for the caller. store.Media carries no restricted flag, so it is derived by
// comparing visibility: a member visible under a REVEALED filter but NOT under
// the caller's actual filter is hidden behind the Reveal contract — i.e.
// restricted. If the caller is already revealed both sets match, so a member
// genuinely in a restricted Library is still flagged via the unrevealed probe.
func (s *Service) packRestricted(ctx context.Context, pack store.OfflinePack, filter store.LibraryFilter) (bool, error) {
	items, err := s.store.OfflinePackItems(ctx, pack.ID)
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, nil
	}
	ids := make([]store.MediaID, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.MediaID)
	}
	revealed := filter
	revealed.Revealed = true
	unrevealed := filter
	unrevealed.Revealed = false
	revealedSet, err := s.store.VisibleMediaBatch(ctx, ids, revealed)
	if err != nil {
		return false, err
	}
	unrevealedSet, err := s.store.VisibleMediaBatch(ctx, ids, unrevealed)
	if err != nil {
		return false, err
	}
	for id := range revealedSet {
		if _, ok := unrevealedSet[id]; !ok {
			return true, nil
		}
	}
	return false, nil
}

// packHasSealedMember reports whether any frozen member of the pack is currently
// in a Sealed Library for the caller (ADR-0098 area 2). It re-derives Sealed
// state live from the encryption seam at serve time, so a member whose Library
// was sealed AFTER the pack was built/granted is detected. The serving layer
// denies such a pack unconditionally (a Sealed Library serves NOTHING — unlike
// restricted, Reveal does not lift it). When no Sealed predicate is wired this
// is always false (nothing sealed). A member resolved away by the filter (e.g.
// already deleted) is NOT treated as sealed here — revoke/expiry/restricted
// guards cover those; this guard is specifically the Sealed boundary.
func (s *Service) packHasSealedMember(ctx context.Context, pack store.OfflinePack) (bool, error) {
	if s.sealed == nil {
		return false, nil
	}
	// Cheap short-circuit: if NO Library is currently sealed, no member can be
	// (one Library enumeration, no per-item resolve). This is the common case for
	// an encryption-enabled deployment whose Libraries are all unsealed.
	sealedAny := false
	// Reveal+sentinel so the enumeration is COMPLETE (restricted Libraries
	// included): this probe only asks "is anything sealed at all", it is not a
	// visibility decision — the per-member resolve below makes the real call.
	libraries, err := s.store.Libraries(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return false, err
	}
	for _, lib := range libraries {
		if s.sealed(lib.ID) {
			sealedAny = true
			break
		}
	}
	if !sealedAny {
		return false, nil
	}
	items, err := s.store.OfflinePackItems(ctx, pack.ID)
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, nil
	}
	ids := make([]store.MediaID, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.MediaID)
	}
	// Resolve member Libraries under a Reveal+sentinel filter so we learn every
	// member's Library (a sealed member's Library would be dropped by a
	// sealed-stamped filter — here we want the raw mapping to test it ourselves).
	base := store.LibraryFilter{AllLibraries: true, Revealed: true}
	rows, err := s.store.VisibleMediaBatch(ctx, ids, base)
	if err != nil {
		return false, err
	}
	for _, media := range rows {
		if s.sealed(media.LibraryID) {
			return true, nil
		}
	}
	return false, nil
}

// PackHasSealedMember is the exported serve-time Sealed re-check used by the
// HTTP authorize path.
func (s *Service) PackHasSealedMember(ctx context.Context, pack store.OfflinePack) (bool, error) {
	return s.packHasSealedMember(ctx, pack)
}

// explicitMediaResolver is the built-in generic resolver: the target ref is a
// comma-separated list of Media ids; it returns the subset that is visible
// under the filter, preserving the requested order. Domain-agnostic.
type explicitMediaResolver struct {
	store Store
}

func (r explicitMediaResolver) Resolve(ctx context.Context, targetRef string, filter store.LibraryFilter) (ResolvedTarget, error) {
	ids := ParseMediaRefs(targetRef)
	if len(ids) == 0 {
		return ResolvedTarget{}, ErrEmptyTarget
	}
	visible, err := r.store.VisibleMediaBatch(ctx, ids, filter)
	if err != nil {
		return ResolvedTarget{}, err
	}
	ordered := make([]store.MediaID, 0, len(ids))
	for _, id := range ids {
		if _, ok := visible[id]; ok {
			ordered = append(ordered, id)
		}
	}
	return ResolvedTarget{MediaIDs: ordered, Label: "Offline Pack"}, nil
}
