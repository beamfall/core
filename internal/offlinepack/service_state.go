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
	"slices"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/encderiv"
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
	// transcode, path names the temporary MP4 in the pack directory, or, for
	// an Encrypted Library, staged holds the encrypted derivative instead.
	transcode  bool
	transcoded bool
	staged     *encderiv.Result
	// duration is the selected file's playback length, which an encrypted
	// transcode must cover. Only an encrypted transcode sets it.
	duration time.Duration
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
		encryptedTranscode := transcode && s.isEncrypted(media.LibraryID)
		if encryptedTranscode && s.pipeTranscode == nil {
			return nil, fmt.Errorf("%w: encrypted-library transcoder is not configured", ErrTranscode)
		}
		file, err := s.selectedVersionFile(ctx, id, filter, selectedVersions[id])
		if err != nil {
			return nil, err
		}
		source := packBuildSource{
			mediaID: id, libraryID: media.LibraryID, path: file.Path,
			selectedVersion: file.VersionLabel, transcode: transcode,
		}
		if encryptedTranscode {
			if source.duration, err = s.selectedFileDuration(ctx, media, file); err != nil {
				return nil, err
			}
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
				return nil, ErrItemTooLarge
			}
			if size > s.buildByteBudget-total {
				return nil, ErrBuildBudget
			}
			total += size
			source.size = size
		}
		sources = append(sources, source)
	}
	return sources, nil
}

// selectedFileDuration is the playback length of the file an encrypted
// transcode reads. Without it a transcode that stopped early could not be told
// from a complete one, so a file of unknown duration is refused. The catalog
// keeps one duration per Media, probed from its first Version's file (the one
// MediaFactsFingerprint names), so any other Version has none.
func (s *Service) selectedFileDuration(ctx context.Context, media store.Media, file store.MediaFile) (time.Duration, error) {
	if media.DurationSeconds <= 0 {
		return 0, fmt.Errorf("%w: the source's duration is unknown", ErrTranscode)
	}
	factsFingerprint, _, err := s.store.MediaFactsFingerprint(ctx, media.ID)
	if err != nil {
		return 0, err
	}
	if factsFingerprint == "" || file.ContentFingerprint != factsFingerprint {
		return 0, fmt.Errorf("%w: the selected Version's duration is unknown", ErrTranscode)
	}
	return time.Duration(media.DurationSeconds) * time.Second, nil
}

// transcodeSources replaces each marked source with its transcode in packDir
// and records the transcoded size. An item in an Encrypted Library is staged
// encrypted instead, under the scope that asked for the pack.
func (s *Service) transcodeSources(ctx context.Context, scope auth.Scope, sources []packBuildSource, packDir, quality string, step func()) error {
	for i := range sources {
		if !sources[i].transcode {
			continue
		}
		if s.isEncrypted(sources[i].libraryID) {
			if err := s.transcodeEncrypted(ctx, scope, &sources[i], packDir, i, quality); err != nil {
				return err
			}
			step()
			continue
		}
		if s.transcode == nil {
			return fmt.Errorf("%w: transcoder is not configured", ErrTranscode)
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
			return fmt.Errorf("%w: %w", ErrTranscode, err)
		}
		sources[i].path = dst
		sources[i].transcoded = true
		info, err := os.Stat(dst)
		if err != nil {
			return err
		}
		size := info.Size()
		if size <= 0 {
			return fmt.Errorf("%w produced no bytes", ErrTranscode)
		}
		sources[i].size = uint64(size)
		step()
	}
	return nil
}

func (s *Service) isEncrypted(libraryID store.LibraryID) bool {
	return s.encrypted != nil && s.encrypted(libraryID)
}

// transcodeEncrypted transcodes one item of an Encrypted Library without
// plaintext leaving the protected plane (ADR-0235): the transcoder reads the
// decrypted source from a token-gated loopback endpoint and its output is
// staged encrypted under a per-item key. The build re-checks the scope's
// authorization throughout, and a Seal or a lost grant ends the transcode.
func (s *Service) transcodeEncrypted(ctx context.Context, scope auth.Scope, source *packBuildSource, packDir string, index int, quality string) error {
	if s.pipeTranscode == nil {
		return fmt.Errorf("%w: encrypted-library transcoder is not configured", ErrTranscode)
	}
	stageDir, err := filepath.Abs(packDir)
	if err != nil {
		return err
	}
	plaintextSize, err := s.encryptedPlaintextSize(source.libraryID, source.path)
	if err != nil {
		return err
	}
	size, err := uint64ToInt64(plaintextSize)
	if err != nil {
		return err
	}
	mediaID, libraryID, path, streams := source.mediaID, source.libraryID, source.path, source.streams
	result, err := encderiv.Run(ctx, encderiv.Job{
		Open:           func() (io.ReadSeekCloser, error) { return s.openContent(libraryID, path) },
		Size:           size,
		StageDir:       stageDir,
		StageName:      fmt.Sprintf("%04d", index),
		MaxBytes:       int64(MaxItemSize),
		SourceDuration: source.duration,
		PollInterval:   s.pipePoll,
		Revoked: func(ctx context.Context) error {
			return s.buildRevoked(ctx, scope, mediaID, libraryID)
		},
		Transcode: func(ctx context.Context, in encderiv.Input, stdout io.Writer) error {
			return s.pipeTranscode(ctx, in, stdout, quality, streams)
		},
	})
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return fmt.Errorf("offline pack transcode interrupted: %w", ctx.Err())
		case errors.Is(err, encderiv.ErrTooLarge):
			return ErrItemTooLarge
		case errors.Is(err, encderiv.ErrRevoked):
			return fmt.Errorf("offline pack transcode stopped: %w", err)
		default:
			return fmt.Errorf("%w: %w", ErrTranscode, err)
		}
	}
	source.staged = result
	source.transcoded = true
	source.size = uint64(result.Size()) //nolint:gosec // Result sizes are non-negative
	return nil
}

// buildRevoked reports why a build may no longer read an item of an Encrypted
// Library: the Library is Sealed, the requesting Profile is disabled or gone
// or has lost its grant to the Library, or the Media has left the scope.
func (s *Service) buildRevoked(ctx context.Context, scope auth.Scope, mediaID store.MediaID, libraryID store.LibraryID) error {
	if s.sealed != nil && s.sealed(libraryID) {
		return errors.New("library is sealed")
	}
	profile, err := s.store.Profile(ctx, scope.ProfileID)
	if err != nil {
		return errors.New("profile is unavailable")
	}
	if profile.Disabled {
		return errors.New("profile is disabled")
	}
	if !scope.AllLibraries && !slices.Contains(profile.Library, libraryID) {
		return errors.New("profile no longer has the library")
	}
	filter, err := s.filter(ctx, scope)
	if err != nil {
		return errors.New("library scope is unavailable")
	}
	visible, err := s.store.VisibleMediaBatch(ctx, []store.MediaID{mediaID}, filter)
	if err != nil {
		return errors.New("media visibility is unavailable")
	}
	if _, ok := visible[mediaID]; !ok {
		return errors.New("media is no longer visible")
	}
	return nil
}

// packSourceDigest is the SHA-256 of the bytes an item seals. A staged
// derivative's digest was taken over its final layout as it was staged.
func (s *Service) packSourceDigest(source packBuildSource) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if source.staged == nil {
		return s.hashPackSource(source.libraryID, source.path, source.storageSize)
	}
	if source.staged.Size() != source.storageSize {
		return digest, errors.New("offline pack staged item size differs from its transcode")
	}
	copy(digest[:], source.staged.SHA256())
	return digest, nil
}

// openPackSource opens the bytes an item seals. A staged derivative is
// re-authorized first, so a Seal that lands after its transcode still keeps
// it out of the pack.
func (s *Service) openPackSource(ctx context.Context, scope auth.Scope, source packBuildSource) (io.ReadCloser, error) {
	if source.staged == nil {
		return s.openContent(source.libraryID, source.path)
	}
	if err := s.reauthorizeStaged(ctx, scope, source); err != nil {
		return nil, err
	}
	return source.staged.Open()
}

// reauthorizeStaged re-runs the build's authorization for each staged
// derivative among sources. The build calls it before and after each one is
// encrypted into the pack and over all of them before the pack is signed.
func (s *Service) reauthorizeStaged(ctx context.Context, scope auth.Scope, sources ...packBuildSource) error {
	for _, source := range sources {
		if source.staged == nil {
			continue
		}
		if err := s.buildRevoked(ctx, scope, source.mediaID, source.libraryID); err != nil {
			return fmt.Errorf("offline pack seal stopped: %w: %w", encderiv.ErrRevoked, err)
		}
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
			return 0, ErrItemTooLarge
		}
		if size > s.buildByteBudget-total {
			return 0, ErrBuildBudget
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

// removeTranscodes deletes the temporary transcodes once they are sealed or
// the build ends without sealing. A staged derivative's Close deletes its
// staging file and zeroes its key.
func removeTranscodes(sources []packBuildSource) {
	for _, source := range sources {
		switch {
		case source.staged != nil:
			_ = source.staged.Close()
		case source.transcoded:
			_ = os.Remove(source.path)
		}
	}
}

func (s *Service) catalogPlaintextSize(libraryID store.LibraryID, file store.MediaFile) (uint64, error) {
	if file.SizeBytes > 0 {
		return uint64(file.SizeBytes), nil
	}
	if s.encrypted != nil && s.encrypted(libraryID) {
		return s.encryptedPlaintextSize(libraryID, file.Path)
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

// encryptedPlaintextSize reads an encrypted item's plaintext length from its
// decrypt reader when the catalog carries no size (a file scanned before
// size_bytes was recorded); stat would report the ciphertext length.
// hashPackSource then decrypts the whole stream, authenticating every chunk,
// and refuses a stream that does not end at this length.
func (s *Service) encryptedPlaintextSize(libraryID store.LibraryID, path string) (uint64, error) {
	reader, err := s.openContent(libraryID, path)
	if err != nil {
		return 0, err
	}
	size, seekErr := reader.Seek(0, io.SeekEnd)
	closeErr := reader.Close()
	if seekErr != nil {
		return 0, seekErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if size < 0 {
		return 0, errors.New("offline pack item has invalid plaintext size")
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
// is always false (nothing sealed). A sealed encrypted Library's rows live on
// its locked plane, so its members resolve to nothing at all; the store cannot
// tell them apart from deleted rows. While any Library the pack may draw from is
// sealed, a member that does not resolve is therefore treated as sealed (fail
// closed): the pack serves again once the Library is unsealed.
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
	// A pack scoped to named Libraries can only hold members from them.
	inScope := func(store.LibraryID) bool { return true }
	if len(pack.LibraryIDs) > 0 {
		scoped := make(map[store.LibraryID]bool, len(pack.LibraryIDs))
		for _, id := range pack.LibraryIDs {
			scoped[id] = true
		}
		inScope = func(id store.LibraryID) bool { return scoped[id] }
	}
	libraries, err := s.store.Libraries(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return false, err
	}
	for _, lib := range libraries {
		if inScope(lib.ID) && s.sealed(lib.ID) {
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
	for _, id := range ids {
		media, ok := rows[id]
		if !ok || s.sealed(media.LibraryID) {
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
