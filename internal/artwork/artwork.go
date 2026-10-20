// Package artwork generates and locates Media artwork derivatives. Core knows
// a small fixed artwork slot vocabulary (ADR-0003 artwork slots, ADR-0014
// derivative buckets).
package artwork

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// SweepJobKind is the durable job that fills in missing posters.
const SweepJobKind store.JobKind = "artwork-sweep"

const (
	SlotPoster   = "poster"
	SlotBackdrop = "backdrop"
	SlotThumb    = "thumb"
)

const PreviewUnavailableField = "preview-unavailable"

// KnownSlots is the fixed Core artwork slot vocabulary. Plugins may emit
// newer slot names; Core ignores them until clients and serving paths know
// how to handle the shape.
var KnownSlots = []string{SlotPoster, SlotBackdrop, SlotThumb}

// posterWidth gives frame-grabbed posters enough resolution that the full
// bucket family (w320/w640/w1280) has real headroom.
const posterWidth = 1280

// BucketOriginal names the full-size file; it is always valid and never
// resized.
const BucketOriginal = "original"

// bucketWidths is the server's derivative bucket table: the
// fixed vocabulary clients ask by name — never by pixels. Widths are maxima;
// derivatives never upscale past the source.
//
// w400 and w1920 are the image-Media derivatives: the 400px
// grid thumbnail and the 1920px lightbox/full-screen preview the Photos plugin
// surfaces for still Media. They ride the same content-addressed, lazily
// generated, auth-gated serving path as the poster buckets (mediahttp's
// ValidBucket reads this table) — no image-specific serving route.
var bucketWidths = map[string]int{
	"w320":  320,
	"w400":  400,
	"w640":  640,
	"w1280": 1280,
	"w1920": 1920,
}

// ValidBucket reports whether name is in the derivative bucket vocabulary.
// The empty string means "original" (the bare route stays full-size).
func ValidBucket(name string) bool {
	if name == "" || name == BucketOriginal {
		return true
	}
	_, ok := bucketWidths[name]
	return ok
}

// ValidSlot reports whether name is in Core's artwork slot vocabulary.
func ValidSlot(name string) bool {
	for _, slot := range KnownSlots {
		if name == slot {
			return true
		}
	}
	return false
}

// FrameGrabber is artwork's slice of the FFmpeg seam (interface at the consumer).
type FrameGrabber interface {
	FrameGrab(ctx context.Context, src string, atSeconds float64, width int, dst string) error
}

// ImageScaler is artwork's slice of the FFmpeg seam for derivative resizing.
// The real *ffmpeg.Runner implements both; NewService discovers the scaler on
// the grabber so existing constructor call sites keep working unchanged.
type ImageScaler interface {
	ScaleImage(ctx context.Context, src string, width int, dst string) error
}

// EmbeddedArtworkExtractor is the optional FFmpeg seam for attached pictures.
type EmbeddedArtworkExtractor interface {
	ExtractEmbeddedArtwork(ctx context.Context, src string, width int, dst string) (bool, error)
}

// Service generates and locates artwork files under dataDir/artwork.
type Service struct {
	store     *store.Store
	grabber   FrameGrabber
	scaler    ImageScaler
	embedded  EmbeddedArtworkExtractor
	dir       string
	sealed    SealedPredicate
	encrypted EncryptedPredicate
	deriveMu  [64]derivativeLock
}

type derivativeLock struct {
	once sync.Once
	ch   chan struct{}
}

// SealedPredicate reports whether a Library is currently Sealed (ADR-0098 area
// 2). A Sealed Library gets no plaintext artwork: poster generation and Media
// artwork import fail-closed (skip) for it. Composition wires it off
// libencrypt.Manager.Sealed; nil (the default) treats no Library as sealed.
type SealedPredicate func(libraryID store.LibraryID) bool

// EncryptedPredicate reports whether a Library is Encrypted (provisioned),
// whether it is currently Sealed OR Unsealed. A generated poster
// (a frame grabbed from the Media) and an imported Media artwork slot are
// plaintext derivatives of the user's Media, and no encrypted-plane write path
// exists yet, so both fail closed for every Encrypted Library
// rather than dropping plaintext on the shared plane while the Library is
// Unsealed. Composition wires it off libencrypt.Manager.Provisioned; nil (the
// default) treats no Library as encrypted.
type EncryptedPredicate func(libraryID store.LibraryID) bool

// NewService creates an artwork service rooted at dataDir/artwork.
func NewService(st *store.Store, grabber FrameGrabber, dataDir string) *Service {
	s := &Service{store: st, grabber: grabber, dir: filepath.Join(dataDir, "artwork")}
	if scaler, ok := grabber.(ImageScaler); ok {
		s.scaler = scaler
	}
	if embedded, ok := grabber.(EmbeddedArtworkExtractor); ok {
		s.embedded = embedded
	}
	return s
}

// SetSealed wires the Sealed Library predicate (ADR-0098 area 2). Nil disables it.
func (s *Service) SetSealed(sealed SealedPredicate) {
	s.sealed = sealed
}

// SetEncrypted wires the Encrypted Library predicate. Nil disables it.
func (s *Service) SetEncrypted(encrypted EncryptedPredicate) {
	s.encrypted = encrypted
}

// isSealed reports whether the Media's Library is currently Sealed. A lookup
// failure fails closed (treated as sealed): artwork must never write plaintext
// for a Library it cannot prove is unsealed.
func (s *Service) isSealed(ctx context.Context, id store.MediaID) bool {
	if s.sealed == nil {
		return false
	}
	libraryID, err := s.store.MediaLibraryID(ctx, id)
	if err != nil {
		return true
	}
	return s.sealed(libraryID)
}

// isEncrypted reports whether the Media's Library is Encrypted (provisioned),
// Sealed or Unsealed. A lookup failure fails closed (treated as encrypted):
// artwork must never write a plaintext derivative for a Library it cannot prove
// is unencrypted.
func (s *Service) isEncrypted(ctx context.Context, id store.MediaID) bool {
	if s.encrypted == nil {
		return false
	}
	libraryID, err := s.store.MediaLibraryID(ctx, id)
	if err != nil {
		return true
	}
	return s.encrypted(libraryID)
}

// PosterPath returns where a Media item's poster lives (whether or not it
// exists yet). Media IDs are server-issued (media-sha256-… or seed literals),
// but the path stays inside the artwork dir by construction.
func (s *Service) PosterPath(id store.MediaID) string {
	return s.ArtworkPath(id, SlotPoster)
}

// ArtworkPath returns where a Media item's artwork slot lives (whether or not
// it exists yet). Callers must pass a known slot; unknown slot names are not
// path-addressable.
func (s *Service) ArtworkPath(id store.MediaID, slot string) string {
	return filepath.Join(s.dir, filepath.Base(string(id)), slot+".jpg")
}

// EntityPosterPath returns where an Entity's poster lives (whether or not it
// exists yet). Entities get their own subdirectory so entity IDs can never
// collide with Media IDs in the artwork store.
func (s *Service) EntityPosterPath(id store.EntityID) string {
	return s.EntityArtworkPath(id, SlotPoster)
}

// EntityArtworkPath returns where an Entity's artwork slot lives.
func (s *Service) EntityArtworkPath(id store.EntityID, slot string) string {
	return filepath.Join(s.dir, "entity", filepath.Base(string(id)), slot+".jpg")
}

// HasPoster reports whether a poster file exists for the Media item.
func (s *Service) HasPoster(id store.MediaID) bool {
	_, ok := s.ArtworkVersion(id, SlotPoster)
	return ok
}

// HasArtwork reports whether an artwork slot file exists for the Media item.
func (s *Service) HasArtwork(id store.MediaID, slot string) bool {
	_, ok := s.ArtworkVersion(id, slot)
	return ok
}

// IsGeneratedArtwork reports whether a present slot is a server-generated
// fallback rather than imported or locally supplied artwork.
func (s *Service) IsGeneratedArtwork(id store.MediaID, slot string) bool {
	return s.HasArtwork(id, slot) && regularFileExists(generatedMarkerPath(s.ArtworkPath(id, slot)))
}

func generatedMarkerPath(path string) string { return path + ".generated" }

// HasEntityPoster reports whether a poster file exists for the Entity.
func (s *Service) HasEntityPoster(id store.EntityID) bool {
	_, ok := s.EntityArtworkVersion(id, SlotPoster)
	return ok
}

// HasEntityArtwork reports whether an artwork slot file exists for the Entity.
func (s *Service) HasEntityArtwork(id store.EntityID, slot string) bool {
	_, ok := s.EntityArtworkVersion(id, slot)
	return ok
}

// ArtworkVersion returns a cache-busting version token for a Media item's
// artwork slot, present only when the slot file really exists. The token is a
// content hash of the stored artwork bytes (ADR-0014 §2: artwork URLs must be
// content-addressed so BeamfallKit's image cache, Snapshots, and browsers
// "cache hard"). A content hash — not mtime and not a DB column — is what makes
// the URL stable across a re-scrape of identical bytes: a re-identify or
// re-scrape that rewrites the file with the same bytes leaves the token
// unchanged (mtime would churn it and force a needless cache miss), while a
// genuinely new poster yields a new token. It stays file-only because applied
// artwork is file-only (there is no artwork DB row, and a new artwork table
// near the guarded ADR-0011 schema freeze isn't justified by a cache token).
func (s *Service) ArtworkVersion(id store.MediaID, slot string) (string, bool) {
	if !ValidSlot(slot) {
		return "", false
	}
	return versionAt(s.ArtworkPath(id, slot))
}

// EntityArtworkVersion is ArtworkVersion for an Entity artwork slot.
func (s *Service) EntityArtworkVersion(id store.EntityID, slot string) (string, bool) {
	if !ValidSlot(slot) {
		return "", false
	}
	return versionAt(s.EntityArtworkPath(id, slot))
}

// slotFallback names the slot served in place of a requested slot whose own
// file is absent (ADR-0014 §2: per-slot fallback resolves at the endpoint —
// "poster → thumb → frame grab server-side" — so clients ask for a slot and
// never re-implement the chain). The poster is the always-generated base; the
// frame-grab tail of the chain is the sweep that fills the poster in the first
// place, so a present poster is the resolved end of every fallback here.
var slotFallback = map[string]string{
	SlotBackdrop: SlotPoster,
	SlotThumb:    SlotPoster,
}

// ResolveSlot returns the slot to actually serve for a requested Media slot and
// whether any artwork resolves: the requested slot when its file exists, else
// its per-slot fallback. ok=false means neither the slot nor its
// fallback has stored bytes — the caller serves nothing (reveal-safe: an absent
// slot is indistinguishable from a hidden one upstream).
func (s *Service) ResolveSlot(id store.MediaID, slot string) (string, bool) {
	if !ValidSlot(slot) {
		return "", false
	}
	if regularFileExists(s.ArtworkPath(id, slot)) {
		return slot, true
	}
	if fb, ok := slotFallback[slot]; ok && regularFileExists(s.ArtworkPath(id, fb)) {
		return fb, true
	}
	return "", false
}

// ResolveEntitySlot is ResolveSlot for an Entity artwork slot.
func (s *Service) ResolveEntitySlot(id store.EntityID, slot string) (string, bool) {
	if !ValidSlot(slot) {
		return "", false
	}
	if regularFileExists(s.EntityArtworkPath(id, slot)) {
		return slot, true
	}
	if fb, ok := slotFallback[slot]; ok && regularFileExists(s.EntityArtworkPath(id, fb)) {
		return fb, true
	}
	return "", false
}

// versionAt returns the content-hash version token for the artwork file at
// path, or ("", false) when the file is missing or not a regular file. The
// token keys the canonical content-addressed URL, so it must depend only on the
// bytes — never on mtime — to stay stable across a re-scrape of identical bytes.
func versionAt(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	token, err := contentVersion(path)
	if err != nil {
		return "", false
	}
	return token, true
}

// contentVersion hashes the file's bytes into a compact, URL-safe version
// token (base-36 of the leading sha256 bytes). Identical bytes always hash to
// the same token; any change to the bytes changes it — the cache-busting
// content-address contract.
func contentVersion(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum := h.Sum(nil)
	// The leading 64 bits are ample for a cache key (collision risk is
	// negligible for artwork volumes); base-36 keeps the query string short.
	return strconv.FormatUint(binary.BigEndian.Uint64(sum[:8]), 36), nil
}

// RemoveEntityArtwork deletes an Entity's artwork directory — poster plus any
// cached derivatives — after the store purged the Entity (orphan GC). Missing
// directories are fine: most Entities never had artwork.
func (s *Service) RemoveEntityArtwork(id store.EntityID) error {
	return os.RemoveAll(filepath.Dir(s.EntityPosterPath(id)))
}

// PurgeMediaArtwork removes a Media item's entire artwork directory — every slot
// plus every scaled bucket derivative — through the same path scheme ArtworkPath
// writes, so the purge can never drift from the layout. It is the reconcile hook
// for the Encrypted-Library crash-recovery sweep (ADR-0045 §reconcile,
// ENCPRIV-10d): an Encrypted Library must have no plaintext artwork on the shared
// plane, so any directory found here is a crash-stranded leak. A missing
// directory is fine (the common case — no leak). It routes a hard delete rather
// than the recoverable trash: leaked plaintext artwork of an encrypted photo is
// exactly what invariant #5 forbids at rest, and it is losslessly regenerable
// from the sealed original, so recoverability (invariant #6) does not apply to it.
func (s *Service) PurgeMediaArtwork(id store.MediaID) error {
	return os.RemoveAll(filepath.Join(s.dir, filepath.Base(string(id))))
}

// candidatePrefix namespaces fetched-candidate-preview cache directories under
// the artwork store so the reconciliation sweep can recognize and own them.
const candidatePrefix = "candidate-"

// CandidateCacheID is the synthetic Media ID a fetched artwork candidate's
// preview is cached under. Caching keys previews by candidate ID, which keeps
// the cache directory addressable for the SweepCandidatePreviewCache reconciler.
func CandidateCacheID(candidateID string) store.MediaID {
	return store.MediaID(candidatePrefix + candidateID)
}

// SweepCandidatePreviewCache reconciles the candidate-preview cache on disk
// against the live candidate set, removing the directory for every cached
// candidate whose row no longer exists (orphan GC for fetched artwork-candidate
// previews). It runs after the retention purge commits, so a crash mid-purge
// self-heals on the next sweep: the only files removed are ones whose owning
// row is already gone. liveIDs is the set of candidate IDs still in the table.
// Returns the number of orphan directories removed.
func (s *Service) SweepCandidatePreviewCache(liveIDs []string) (int, error) {
	live := make(map[string]struct{}, len(liveIDs))
	for _, id := range liveIDs {
		live[id] = struct{}{}
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, candidatePrefix) {
			continue
		}
		id := strings.TrimPrefix(name, candidatePrefix)
		if _, ok := live[id]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.dir, name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func nonEmptyRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// derivativeFresh reports whether a cached derivative at path exists and is no
// older than its original (origModTime). A missing derivative or one predating
// the original is stale and must be regenerated.
func derivativeFresh(path string, origModTime time.Time) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return !info.ModTime().Before(origModTime)
}

// PosterDerivative returns the on-disk path to serve for a bucket of the
// Media item's poster, lazily generating and caching the derivative next to
// the full-size file on first request. The empty bucket and BucketOriginal
// return the full-size path; unknown buckets are an error. Generation
// failures (no scaler, no source, ffmpeg error) honestly fall back to the
// full-size path — never a broken image.
func (s *Service) PosterDerivative(ctx context.Context, id store.MediaID, bucket string) (string, error) {
	return s.ArtworkDerivative(ctx, id, SlotPoster, bucket)
}

// ArtworkDerivative returns the on-disk path to serve for a Media item's
// artwork slot and derivative bucket.
func (s *Service) ArtworkDerivative(ctx context.Context, id store.MediaID, slot, bucket string) (string, error) {
	if !ValidSlot(slot) {
		return "", fmt.Errorf("unknown artwork slot %q", slot)
	}
	return s.derivative(ctx, s.ArtworkPath(id, slot), string(id), slot, bucket)
}

// EntityPosterDerivative is PosterDerivative for an Entity's poster: the same
// bucket vocabulary, lazy generation, and honest full-size fallback.
func (s *Service) EntityPosterDerivative(ctx context.Context, id store.EntityID, bucket string) (string, error) {
	return s.EntityArtworkDerivative(ctx, id, SlotPoster, bucket)
}

// EntityArtworkDerivative is ArtworkDerivative for an Entity artwork slot.
func (s *Service) EntityArtworkDerivative(ctx context.Context, id store.EntityID, slot, bucket string) (string, error) {
	if !ValidSlot(slot) {
		return "", fmt.Errorf("unknown artwork slot %q", slot)
	}
	return s.derivative(ctx, s.EntityArtworkPath(id, slot), string(id), slot, bucket)
}

// derivative backs the Media and Entity artwork derivative paths; owner is
// the ID logged when generation falls back.
func (s *Service) derivative(ctx context.Context, original, owner, slot, bucket string) (string, error) {
	if bucket == "" || bucket == BucketOriginal {
		return original, nil
	}
	width, ok := bucketWidths[bucket]
	if !ok {
		return "", fmt.Errorf("unknown artwork bucket %q", bucket)
	}
	dst := filepath.Join(filepath.Dir(original), slot+"."+bucket+".jpg")
	origInfo, err := os.Stat(original)
	if err != nil || !origInfo.Mode().IsRegular() {
		return original, nil
	}
	// A cached derivative is fresh only when it is at least as new as its
	// original; a rewritten original (replaced artwork — the URL's ?v changes
	// but the cached bytes don't) must regenerate the bucket.
	if derivativeFresh(dst, origInfo.ModTime()) {
		return dst, nil
	}
	if s.scaler == nil {
		return original, nil
	}
	unlock, err := s.lockDerivative(ctx, filepath.Dir(original))
	if err != nil {
		return original, err
	}
	defer unlock()
	if derivativeFresh(dst, origInfo.ModTime()) {
		return dst, nil
	}
	if err := s.scaleImageAtomic(ctx, original, width, dst, nil); err != nil {
		slog.Debug("artwork derivative generation failed; serving original",
			"owner", owner, "bucket", bucket, "error", err.Error())
		return original, nil
	}
	return dst, nil
}

// DeriveImage publishes the still-image poster and the two ADR-0043 buckets.
// The fingerprint marker is written last, so a scan checkpoint can only pass
// work whose complete derivative set is durable and current.
func (s *Service) DeriveImage(ctx context.Context, id store.MediaID, path, fingerprint string) error {
	if id == "" || path == "" || fingerprint == "" {
		return errors.New("image derivative requires media ID, path, and fingerprint")
	}
	if s.isSealed(ctx, id) || s.isEncrypted(ctx, id) {
		return nil
	}
	if s.scaler == nil {
		return errors.New("no image scaler configured")
	}
	poster := s.PosterPath(id)
	unlock, err := s.lockDerivative(ctx, filepath.Dir(poster))
	if err != nil {
		return err
	}
	defer unlock()
	marker := poster + ".source-fingerprint"
	buckets := []string{"w400", "w1920"}
	// #nosec G304 -- marker is derived from the server-owned artwork root and validated Media ID.
	if data, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(data)) == fingerprint && nonEmptyRegularFile(poster) {
		fresh := true
		for _, bucket := range buckets {
			if !nonEmptyRegularFile(filepath.Join(filepath.Dir(poster), SlotPoster+"."+bucket+".jpg")) {
				fresh = false
				break
			}
		}
		if fresh {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(poster), 0o700); err != nil {
		return err
	}
	guard := func() error {
		// Check both sides of every atomic rename below. The encrypted-library
		// lifecycle reconciler remains the backstop for a transition after the
		// post-rename check has completed.
		if s.isSealed(ctx, id) || s.isEncrypted(ctx, id) {
			return errProtectedArtwork
		}
		return nil
	}
	if err := s.scaleImageAtomic(ctx, path, posterWidth, poster, guard); err != nil {
		if errors.Is(err, errProtectedArtwork) {
			return s.PurgeMediaArtwork(id)
		}
		_ = s.store.UpsertMediaFields(ctx, id, []store.MediaField{{Name: PreviewUnavailableField, Type: store.EntityFieldFlag, Flag: true}})
		return fmt.Errorf("decoding image preview for %s: %w", id, err)
	}
	if err := writeAtomicFile(generatedMarkerPath(poster), nil, guard); err != nil {
		if errors.Is(err, errProtectedArtwork) {
			return s.PurgeMediaArtwork(id)
		}
		return err
	}
	for _, bucket := range buckets {
		dst := filepath.Join(filepath.Dir(poster), SlotPoster+"."+bucket+".jpg")
		if err := s.scaleImageAtomic(ctx, path, bucketWidths[bucket], dst, guard); err != nil {
			if errors.Is(err, errProtectedArtwork) {
				return s.PurgeMediaArtwork(id)
			}
			_ = s.store.UpsertMediaFields(ctx, id, []store.MediaField{{Name: PreviewUnavailableField, Type: store.EntityFieldFlag, Flag: true}})
			return fmt.Errorf("generating image derivative %s for %s: %w", bucket, id, err)
		}
	}
	if err := writeAtomicFile(marker, []byte(fingerprint+"\n"), guard); err != nil {
		if errors.Is(err, errProtectedArtwork) {
			return s.PurgeMediaArtwork(id)
		}
		return err
	}
	return s.store.UpsertMediaFields(ctx, id, []store.MediaField{{Name: PreviewUnavailableField, Type: store.EntityFieldFlag, Flag: false}})
}

func (s *Service) lockDerivative(ctx context.Context, path string) (func(), error) {
	sum := sha256.Sum256([]byte(path))
	lock := &s.deriveMu[binary.LittleEndian.Uint64(sum[:8])%uint64(len(s.deriveMu))]
	lock.once.Do(func() {
		lock.ch = make(chan struct{}, 1)
		lock.ch <- struct{}{}
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-lock.ch:
		return func() { lock.ch <- struct{}{} }, nil
	}
}

var errProtectedArtwork = errors.New("library became protected during artwork generation")

func (s *Service) scaleImageAtomic(ctx context.Context, src string, width int, dst string, beforePublish func() error) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".image-derive-*.jpg")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	defer os.Remove(tmpName)
	if err := s.scaler.ScaleImage(ctx, src, width, tmpName); err != nil {
		return err
	}
	if err := syncFile(tmpName); err != nil {
		return err
	}
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			_ = os.Remove(dst)
			_ = syncDir(filepath.Dir(dst))
			return err
		}
	}
	return syncDir(filepath.Dir(dst))
}

func writeAtomicFile(path string, data []byte, beforePublish func() error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".image-fingerprint-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			_ = os.Remove(path)
			_ = syncDir(filepath.Dir(path))
			return err
		}
	}
	return syncDir(filepath.Dir(path))
}

func syncFile(path string) error {
	// #nosec G304 -- path is a server-created temporary file under the artwork root.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDir(path string) error {
	// #nosec G304 -- path is the server-owned artwork directory being published.
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// GenerateDerivatives pre-generates the full bucket set for a Media item's
// poster (ADR-0014: derivatives come from generation time, not per-request;
// the lazy route path stays as the backfill). Failures fall back per bucket.
func (s *Service) GenerateDerivatives(ctx context.Context, id store.MediaID) {
	for bucket := range bucketWidths {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.PosterDerivative(ctx, id, bucket)
	}
}

// GeneratePoster grabs one frame from the Media item's primary file. The
// server-internal filter sees every Library: posters for restricted media are
// generated at rest and reveal-filtered at the serving boundary.
func (s *Service) GeneratePoster(ctx context.Context, id store.MediaID) error {
	if s.grabber == nil {
		return errors.New("no frame grabber configured")
	}
	serverFilter := store.LibraryFilter{Revealed: true, AllLibraries: true}
	media, err := s.store.Media(ctx, id, serverFilter)
	if err != nil {
		return fmt.Errorf("loading media %s: %w", id, err)
	}
	// Sealed Libraries get no plaintext poster (ADR-0098 area 2): skip generation
	// entirely — no frame is grabbed and no file is written.
	if s.sealed != nil && s.sealed(media.LibraryID) {
		return nil
	}
	// Encrypted Libraries fail closed the same way even while Unsealed (ADR-0045
	// §2.3): a grabbed poster is a plaintext derivative with no encrypted-plane
	// write path yet, so refuse rather than write it to the shared plane.
	if s.encrypted != nil && s.encrypted(media.LibraryID) {
		return nil
	}
	file, err := s.store.PrimaryMediaFile(ctx, id, serverFilter)
	if err != nil {
		return fmt.Errorf("loading primary file for %s: %w", id, err)
	}
	found, err := s.importEmbeddedPoster(ctx, id, file.Path)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	dst := s.PosterPath(id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	marker := generatedMarkerPath(dst)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		return err
	}
	generated := false
	defer func() {
		if !generated {
			_ = os.Remove(marker)
		}
	}()
	if media.Class == store.MediaClassImage {
		if s.scaler == nil {
			return errors.New("no image scaler configured")
		}
		err := s.scaler.ScaleImage(ctx, file.Path, posterWidth, dst)
		_ = s.store.UpsertMediaFields(ctx, id, []store.MediaField{{
			Name: PreviewUnavailableField,
			Type: store.EntityFieldFlag,
			Flag: err != nil,
		}})
		if err != nil {
			return fmt.Errorf("decoding image preview for %s: %w", id, err)
		}
		generated = true
		return nil
	}
	// A tenth of the way in skips title cards without seeking deep into long
	// files; very short or unprobed media grabs near the start.
	atSeconds := float64(media.DurationSeconds) / 10
	if atSeconds < 1 {
		atSeconds = 0
	}
	if err := s.grabber.FrameGrab(ctx, file.Path, atSeconds, posterWidth, dst); err != nil {
		return err
	}
	generated = true
	return nil
}

// ImportEmbeddedPoster extracts an attached picture from the primary media
// file. It is imported provenance, so fetched artwork must not replace it.
func (s *Service) ImportEmbeddedPoster(ctx context.Context, id store.MediaID) (bool, error) {
	if s.embedded == nil || s.isSealed(ctx, id) || s.isEncrypted(ctx, id) {
		return false, nil
	}
	file, err := s.store.PrimaryMediaFile(ctx, id, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		return false, err
	}
	return s.importEmbeddedPoster(ctx, id, file.Path)
}

func (s *Service) importEmbeddedPoster(ctx context.Context, id store.MediaID, src string) (bool, error) {
	if s.embedded == nil {
		return false, nil
	}
	dst := s.PosterPath(id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	found, err := s.embedded.ExtractEmbeddedArtwork(ctx, src, posterWidth, dst)
	if err != nil || !found {
		return false, err
	}
	if err := os.Remove(generatedMarkerPath(dst)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for bucket := range bucketWidths {
		_ = os.Remove(filepath.Join(filepath.Dir(dst), SlotPoster+"."+bucket+".jpg"))
	}
	return true, nil
}

type sweepPayload struct {
	Version  int             `json:"version"`
	MediaIDs []store.MediaID `json:"media_ids,omitempty"`
}

type sweepCheckpoint struct {
	Version   int    `json:"version"`
	LastID    string `json:"last_id,omitempty"`
	Generated int    `json:"generated"`
	Failed    int    `json:"failed"`
}

const sweepJobBatchSize = 10

// NewSweepJob builds the deduped background job that generates missing posters.
func NewSweepJob(createdAt time.Time) (store.Job, error) {
	payload, err := json.Marshal(sweepPayload{Version: 1})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind:      SweepJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(SweepJobKind),
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// NewSelectedSweepJob builds a background job that generates posters only for
// the supplied Media IDs. The dedupe key is scoped to that exact sorted set, so
// selected UI remediation cannot collapse into the global sweep.
func NewSelectedSweepJob(mediaIDs []store.MediaID, createdAt time.Time) (store.Job, error) {
	ids := make([]string, 0, len(mediaIDs))
	seen := map[store.MediaID]bool{}
	for _, id := range mediaIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	selected := make([]store.MediaID, len(ids))
	for i, id := range ids {
		selected[i] = store.MediaID(id)
	}
	payload, err := json.Marshal(sweepPayload{Version: 1, MediaIDs: selected})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind:      SweepJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(SweepJobKind) + ":selected:" + strings.Join(ids, ","),
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// SweepHandler returns the job handler that fills in missing posters,
// checkpointing by Media ID so a preempted sweep resumes where it stopped.
func SweepHandler(service *Service) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var payload sweepPayload
		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return jobs.Result{}, err
			}
		}
		var checkpoint sweepCheckpoint
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
		}
		ids := payload.MediaIDs
		pagedAll := len(ids) == 0
		if len(ids) == 0 {
			var err error
			ids, err = service.store.MediaIDsAfter(ctx, store.MediaID(checkpoint.LastID), sweepJobBatchSize+1)
			if err != nil {
				return jobs.Result{}, err
			}
		}
		processed := 0
		for _, id := range ids {
			if !pagedAll && checkpoint.LastID != "" && string(id) <= checkpoint.LastID {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			if !service.HasPoster(id) {
				if err := service.GeneratePoster(ctx, id); err != nil {
					checkpoint.Failed++
				} else {
					checkpoint.Generated++
				}
			}
			// Steady state matches ADR-0014: the sweep owns derivative
			// generation; cache hits make this a stat on later passes.
			if service.HasPoster(id) {
				service.GenerateDerivatives(ctx, id)
			}
			checkpoint.LastID = string(id)
			processed++
			if processed >= sweepJobBatchSize {
				break
			}
		}
		checkpoint.Version = 1
		data, err := json.Marshal(checkpoint)
		if err != nil {
			return jobs.Result{}, err
		}
		progress := store.JobProgress{
			Current: int64(checkpoint.Generated),
			Unit:    "posters",
			Message: "Generating artwork",
		}
		if err := service.store.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return jobs.Result{}, err
		}
		if (pagedAll && len(ids) > processed) || (!pagedAll && processed >= sweepJobBatchSize) || ctx.Err() != nil {
			return jobs.Result{Checkpoint: data}, jobs.ErrYielded
		}
		return jobs.Result{}, nil
	}
}

// ImportPoster stores fetched artwork bytes as the Media item's poster
// (scraped artwork application, ADR-0009). The image is normalized to JPEG
// at posterWidth through the scaler when one is configured (raw bytes are
// stored verbatim otherwise), and stale bucket derivatives are invalidated
// so the next request regenerates from the new poster.
func (s *Service) ImportPoster(ctx context.Context, id store.MediaID, data []byte) error {
	return s.ImportArtwork(ctx, id, SlotPoster, data)
}

// ImportArtwork stores fetched artwork bytes as the Media item's requested
// slot (scraped artwork application, ADR-0009).
func (s *Service) ImportArtwork(ctx context.Context, id store.MediaID, slot string, data []byte) error {
	if !ValidSlot(slot) {
		return fmt.Errorf("unknown artwork slot %q", slot)
	}
	// Sealed Libraries get no plaintext poster on disk (ADR-0098 area 2): skip the
	// import fail-closed so no plaintext artwork file is written.
	if s.isSealed(ctx, id) {
		return nil
	}
	// Encrypted Libraries fail closed even while Unsealed: the
	// imported slot is a plaintext artwork derivative of the user's Media with no
	// encrypted-plane write path yet, so refuse rather than write it to disk.
	if s.isEncrypted(ctx, id) {
		return nil
	}
	dst := s.ArtworkPath(id, slot)
	if err := s.importArtwork(ctx, dst, string(id), slot, data); err != nil {
		return err
	}
	if err := os.Remove(generatedMarkerPath(dst)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ImportCandidateArtwork stores a fetched artwork *candidate* preview under its
// synthetic candidate-cache ID (CandidateCacheID). Unlike ImportArtwork it does
// NOT apply the Sealed-Library gate: a candidate preview is server-fetched
// REMOTE art (from the candidate's SourceURL shown during identify/fix-match),
// not a derivative of the user's encrypted Media, so it is not the plaintext
// leak vector the Sealed gate closes — and the synthetic ID has no owning Media
// to resolve a Library from, so isSealed would spuriously fail closed and skip
// every preview. The owning Media's Sealed state is enforced at the serving
// call site (mediahttp) before this is reached, so a candidate for a genuinely
// Sealed Library is still handled per policy there.
func (s *Service) ImportCandidateArtwork(ctx context.Context, id store.MediaID, data []byte) error {
	return s.importArtwork(ctx, s.ArtworkPath(id, SlotPoster), string(id), SlotPoster, data)
}

// ImportEntityPoster stores fetched artwork bytes as the Entity's poster —
// the write path identify's entity hydration lands EntityMap ArtworkPath
// images through. Same normalization and derivative invalidation as
// ImportPoster.
func (s *Service) ImportEntityPoster(ctx context.Context, id store.EntityID, data []byte) error {
	return s.importArtwork(ctx, s.EntityPosterPath(id), string(id), SlotPoster, data)
}

// importArtwork backs the Media and Entity artwork import paths; owner is the
// ID reported when normalization fails.
func (s *Service) importArtwork(ctx context.Context, dst, owner, slot string, data []byte) error {
	if len(data) == 0 {
		return errors.New("artwork data is empty")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".import.tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if s.scaler != nil {
		if err := s.scaler.ScaleImage(ctx, tmp, posterWidth, dst); err != nil {
			return fmt.Errorf("normalizing imported artwork for %s: %w", owner, err)
		}
	} else if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	for bucket := range bucketWidths {
		_ = os.Remove(filepath.Join(filepath.Dir(dst), slot+"."+bucket+".jpg"))
	}
	return nil
}
