package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

// encryptedDerivativeReconciler is the app-layer implementation of the store's
// unseal→register derivative reconcile seam (ADR-0045 §reconcile, ENCPRIV-10d).
//
// It closes the crash-recovery plaintext-derivative leak: generation for an
// Encrypted Library is fail-closed on every plane, so once its
// metadata DB is unsealed and its media inventory is enumerable, NO plaintext
// derivative should exist on the shared plane. Any that does is a crash-stranded
// orphan (a derivative half-written before a crash sealed the Library, or a
// pre-10b leak), and every such artifact is purged here.
//
// The store hands it the full live derivative-key set (each Media's ID plus its
// files' content fingerprints) under the store lock; the store is the sole owner
// of that confidential Media↔fingerprint join, materialized once so this
// reconciler never calls back into the store (a re-entrant read would deadlock on
// the held write lock). Each derivative service owns its own path scheme and
// exposes a purge keyed the same way it writes, so the sweep cannot drift from
// the layout:
//
//   - artwork     — per-Media dir {dataDir}/artwork/<mediaID>/ (all slots+buckets)
//   - generated   — per-fingerprint dir {dataDir}/generated/<fp>/ (trickplay,
//     preview, marker thumbs, waveform/loudness/silence,
//     image-enrich)
//   - subtitles   — per-fingerprint sidecars {dataDir}/subtitles/<fp>.<idx>.vtt
//   - playback    — per-Media precomputed HLS renditions
//     {playback-cache}/precompute:<mediaID>:…/
//   - transcode   — orphaned live-transcode session dirs {dataDir}/transcode/
//     session-<bootNonce>-<N>/ (HLS segments written during on-demand transcode).
//     Unlike the four classes above, session dirs carry no on-disk media/library
//     attribution (they are keyed by an in-memory counter), so this class is swept
//     as a set once per reconcile. The sweep purges prior boots' directories,
//     and the active manager reports failures from its own teardowns.
//
// Ciphertext derivatives are the exception (ADR-0235): the Library's encrypted
// subtitle cache {dataDir}/subtitles/encrypted/<library hash>/ and each live
// fingerprint's generated-asset dir keep every validly named object that
// carries a libencrypt header, and anything else in them (plaintext, staging
// temps, stray entries) is removed. The Library's encrypted precompute tree
// {playback-cache}/.precompute-encrypted/<library hex>/ keeps each generation
// whose members are all ciphertext and removes any other generation whole.
//
// FAIL CLOSED: if any purge errors, the whole reconcile returns non-nil and the
// store call site unwinds the registration, keeping the Library Sealed — the
// server never serves a plane whose derivatives are unaccounted for.
type encryptedDerivativeReconciler struct {
	artwork         *artwork.Service
	assets          *genassets.Service
	subtitles       *mediahttp.Handler
	sessions        *playback.Sessions
	precomputeCache string
}

// newEncryptedDerivativeReconciler builds the reconciler from the live derivative
// services, the transcode session manager, and the precompute cache root.
// precomputeCacheRoot may be empty (no precompute cache configured), in which case
// the precompute sweep is a no-op.
func newEncryptedDerivativeReconciler(art *artwork.Service, assets *genassets.Service, subtitles *mediahttp.Handler, sessions *playback.Sessions, precomputeCacheRoot string) *encryptedDerivativeReconciler {
	return &encryptedDerivativeReconciler{
		artwork:         art,
		assets:          assets,
		subtitles:       subtitles,
		sessions:        sessions,
		precomputeCache: precomputeCacheRoot,
	}
}

// ReconcileEncryptedDerivatives purges every plaintext derivative belonging to
// the just-unsealed Encrypted Library's live media. It never partially succeeds
// silently: every purge error for every class is joined and returned, so the
// store fails the unseal closed rather than register a plane with a surviving
// leak. Each class is swept independently (a failure in one does not skip the
// others) so a single unwritable directory still surfaces alongside the rest.
func (r *encryptedDerivativeReconciler) ReconcileEncryptedDerivatives(ctx context.Context, libraryID store.LibraryID, live []store.MediaDerivativeKey) error {
	var joined error
	for _, key := range live {
		joined = errors.Join(joined, r.purgeMedia(libraryID, key))
	}
	if r.subtitles != nil {
		joined = errors.Join(joined, r.subtitles.ReconcileEncryptedSubtitles(libraryID, libencrypt.HasObjectHeader))
	}
	joined = errors.Join(joined, playback.ReconcileEncryptedPrecomputedRenditions(r.precomputeCache, libraryID, libencrypt.HasObjectHeader))
	if r.sessions != nil {
		joined = errors.Join(joined, r.sessions.PurgeOrphanedTranscodeDirs(), r.sessions.CheckTeardowns(libraryID))
	}
	// Every live fingerprint is now attributed, so the Library's generated-asset
	// ledger is complete and a purge may trust it (CORE-2193).
	if joined == nil && r.assets != nil {
		joined = r.assets.MarkEncryptedReferencesComplete(libraryID)
	}
	if joined != nil {
		return fmt.Errorf("encrypted-library %s derivative reconcile: %w", libraryID, joined)
	}
	return nil
}

// purgeMedia removes every derivative class for one live Media: the MediaID-keyed
// classes (artwork, precompute) once, and the fingerprint-keyed classes
// (generated assets, subtitle sidecars) once per content fingerprint. A
// generated-asset directory that keeps ciphertext is attributed to libraryID,
// so the Library's purge can remove it (CORE-2193).
func (r *encryptedDerivativeReconciler) purgeMedia(libraryID store.LibraryID, key store.MediaDerivativeKey) error {
	var joined error
	if r.artwork != nil {
		joined = errors.Join(joined, r.artwork.PurgeMediaArtwork(key.MediaID))
	}
	joined = errors.Join(joined, playback.PurgePrecomputedRenditionsForMedia(r.precomputeCache, key.MediaID))
	for _, fingerprint := range key.Fingerprints {
		if r.assets != nil {
			joined = errors.Join(joined, r.assets.ReconcileEncryptedFingerprint(libraryID, fingerprint, libencrypt.HasObjectHeader))
		}
		if r.subtitles != nil {
			joined = errors.Join(joined, r.subtitles.PurgeSubtitlesForFingerprint(fingerprint))
		}
	}
	return joined
}
