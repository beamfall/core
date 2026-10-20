package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

// writeFile creates a file (and its parents) with placeholder plaintext so the
// test can assert the reconciler removes it. Fails the test on error.
func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("leaked plaintext derivative"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustBeGone(t *testing.T, label, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s survived reconcile (%s): stat err=%v, want removed", label, path, err)
	}
}

// TestEncryptedDerivativeReconcilerPurgesEveryClass is the ENCPRIV-10d acceptance
// test for the concrete app-layer reconciler: it seeds a real plaintext
// derivative in every class (artwork by MediaID, generated asset + subtitle
// sidecar by content fingerprint, precomputed HLS rendition by MediaID), runs the
// reconcile for that Media's live derivative key, and asserts every orphan is
// removed. Path construction reuses each service's own helper where exported so
// the test binds to the same layout the writers use.
func TestEncryptedDerivativeReconcilerPurgesEveryClass(t *testing.T) {
	dataDir := t.TempDir()
	precomputeRoot := filepath.Join(dataDir, "playback-cache")

	artworkSvc := artwork.NewService(nil, nil, dataDir)
	assetSvc := genassets.NewService(nil, nil, dataDir)
	subtitleHandler := mediahttp.NewHandler(nil, nil, nil, artworkSvc, nil)
	subtitleHandler.SetSubtitles(nil, filepath.Join(dataDir, "subtitles"))

	mediaID := store.MediaID("media-enc-deadbeef")
	fingerprint := "sha256:orphan-fp"

	// One artifact per class, at each class's real on-disk address.
	poster := artworkSvc.ArtworkPath(mediaID, "poster")
	trickplay := assetSvc.ManifestPath(fingerprint)
	preview := assetSvc.PreviewClipPath(fingerprint)
	subtitle := filepath.Join(dataDir, "subtitles", strings.ReplaceAll(fingerprint, ":", "_")+".0.vtt")
	renditionDir := filepath.Join(precomputeRoot, string(playback.PrecomputeRenditionID(mediaID, fingerprint, "720p")))
	rendition := filepath.Join(renditionDir, "index.m3u8")

	for _, p := range []string{poster, trickplay, preview, subtitle, rendition} {
		writeFile(t, p)
	}

	reconciler := newEncryptedDerivativeReconciler(artworkSvc, assetSvc, subtitleHandler, nil, precomputeRoot)
	live := []store.MediaDerivativeKey{{MediaID: mediaID, Fingerprints: []string{fingerprint}}}
	if err := reconciler.ReconcileEncryptedDerivatives(context.Background(), store.LibraryID("library-enc"), live); err != nil {
		t.Fatalf("ReconcileEncryptedDerivatives: %v", err)
	}

	mustBeGone(t, "artwork poster", poster)
	mustBeGone(t, "generated trickplay", trickplay)
	mustBeGone(t, "generated preview", preview)
	mustBeGone(t, "subtitle sidecar", subtitle)
	mustBeGone(t, "precompute rendition dir", renditionDir)
}

// TestEncryptedDerivativeReconcilerFailsClosedOnPurgeError proves the fail-closed
// contract: if any class cannot complete its purge, the whole reconcile returns a
// non-nil, Library-named error so the store unwinds the unseal and the Library
// stays Sealed. The failure is induced by pointing the precompute cache root at a
// regular file, so PurgePrecomputedRenditionsForMedia's os.ReadDir errors.
func TestEncryptedDerivativeReconcilerFailsClosedOnPurgeError(t *testing.T) {
	dataDir := t.TempDir()

	badCacheRoot := filepath.Join(dataDir, "not-a-dir")
	if err := os.WriteFile(badCacheRoot, []byte("file where a dir is expected"), 0o600); err != nil {
		t.Fatalf("seed bad cache root: %v", err)
	}

	artworkSvc := artwork.NewService(nil, nil, dataDir)
	assetSvc := genassets.NewService(nil, nil, dataDir)
	subtitleHandler := mediahttp.NewHandler(nil, nil, nil, artworkSvc, nil)
	subtitleHandler.SetSubtitles(nil, filepath.Join(dataDir, "subtitles"))

	reconciler := newEncryptedDerivativeReconciler(artworkSvc, assetSvc, subtitleHandler, nil, badCacheRoot)
	live := []store.MediaDerivativeKey{{MediaID: store.MediaID("media-enc-x"), Fingerprints: []string{"sha256:x"}}}
	err := reconciler.ReconcileEncryptedDerivatives(context.Background(), store.LibraryID("library-enc"), live)
	if err == nil {
		t.Fatal("reconcile returned nil despite an unpurgeable derivative class, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "library-enc") {
		t.Fatalf("fail-closed error must name the Library for triage, got %v", err)
	}
}

// TestEncryptedDerivativeReconcilerPurgesOrphanedTranscodeDirs proves the 5th
// derivative class: a crash-orphaned live-transcode session dir
// holds PLAINTEXT HLS segments of Encrypted-Library media under
// {dataDir}/transcode/session-<N>/. A restart drops the in-memory session map, so
// on the next unseal every session dir on disk is an orphan and must be purged;
// the four MediaID/fingerprint-keyed classes never touch the transcode root, so
// without this class the plaintext survives at rest (invariant #5 violation). The
// test seeds a plaintext segment in an orphaned session dir on a freshly-created
// Sessions (empty live map, as after a restart), runs the reconcile, and asserts
// the dir is gone. A non-session sibling under the root is left untouched to prove
// the sweep is scoped to session-<N> dirs.
func TestEncryptedDerivativeReconcilerPurgesOrphanedTranscodeDirs(t *testing.T) {
	dataDir := t.TempDir()
	sessions := playback.NewSessions(nil, dataDir, nil)
	transcodeRoot := filepath.Join(dataDir, "transcode")

	orphanSeg := filepath.Join(transcodeRoot, "session-7", "720p", "seg00000.m4s")
	writeFile(t, orphanSeg)
	unrelated := filepath.Join(transcodeRoot, "keepme", "note.txt")
	writeFile(t, unrelated)

	artworkSvc := artwork.NewService(nil, nil, dataDir)
	assetSvc := genassets.NewService(nil, nil, dataDir)
	subtitleHandler := mediahttp.NewHandler(nil, nil, nil, artworkSvc, nil)
	subtitleHandler.SetSubtitles(nil, filepath.Join(dataDir, "subtitles"))
	reconciler := newEncryptedDerivativeReconciler(artworkSvc, assetSvc, subtitleHandler, sessions, filepath.Join(dataDir, "playback-cache"))

	live := []store.MediaDerivativeKey{{MediaID: store.MediaID("media-enc-tc"), Fingerprints: []string{"sha256:tc"}}}
	if err := reconciler.ReconcileEncryptedDerivatives(context.Background(), store.LibraryID("library-enc"), live); err != nil {
		t.Fatalf("ReconcileEncryptedDerivatives: %v", err)
	}

	mustBeGone(t, "orphaned transcode session dir", filepath.Join(transcodeRoot, "session-7"))
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("non-session sibling must survive the transcode sweep, got stat err=%v", err)
	}
}

// TestEncryptedDerivativeReconcilerFailsClosedOnTranscodeSweepError proves the
// transcode class honors the fail-closed contract: if the sweep cannot complete
// the whole reconcile returns a non-nil, Library-named error so the store unwinds
// the unseal and the Library stays Sealed. The failure is induced by pointing the
// transcode root at a regular file, so PurgeOrphanedTranscodeDirs' os.ReadDir
// errors.
func TestEncryptedDerivativeReconcilerFailsClosedOnTranscodeSweepError(t *testing.T) {
	dataDir := t.TempDir()
	sessions := playback.NewSessions(nil, dataDir, nil)
	if err := os.WriteFile(filepath.Join(dataDir, "transcode"), []byte("file where the transcode dir is expected"), 0o600); err != nil {
		t.Fatalf("seed bad transcode root: %v", err)
	}

	artworkSvc := artwork.NewService(nil, nil, dataDir)
	assetSvc := genassets.NewService(nil, nil, dataDir)
	subtitleHandler := mediahttp.NewHandler(nil, nil, nil, artworkSvc, nil)
	subtitleHandler.SetSubtitles(nil, filepath.Join(dataDir, "subtitles"))
	reconciler := newEncryptedDerivativeReconciler(artworkSvc, assetSvc, subtitleHandler, sessions, filepath.Join(dataDir, "playback-cache"))

	live := []store.MediaDerivativeKey{{MediaID: store.MediaID("media-enc-tc"), Fingerprints: []string{"sha256:tc"}}}
	err := reconciler.ReconcileEncryptedDerivatives(context.Background(), store.LibraryID("library-enc"), live)
	if err == nil {
		t.Fatal("reconcile returned nil despite an unsweepable transcode root, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "library-enc") {
		t.Fatalf("fail-closed error must name the Library for triage, got %v", err)
	}
}

// The purge helpers must be safe no-ops when the derivative roots are empty (the
// common case: a healthy Encrypted Library has no plaintext derivative to purge).
func TestEncryptedDerivativeReconcilerNoOpWhenNothingPresent(t *testing.T) {
	dataDir := t.TempDir()
	artworkSvc := artwork.NewService(nil, nil, dataDir)
	assetSvc := genassets.NewService(nil, nil, dataDir)
	subtitleHandler := mediahttp.NewHandler(nil, nil, nil, artworkSvc, nil)
	subtitleHandler.SetSubtitles(nil, filepath.Join(dataDir, "subtitles"))
	reconciler := newEncryptedDerivativeReconciler(artworkSvc, assetSvc, subtitleHandler, nil, filepath.Join(dataDir, "playback-cache"))

	live := []store.MediaDerivativeKey{{MediaID: store.MediaID("media-enc-empty"), Fingerprints: []string{"sha256:none"}}}
	if err := reconciler.ReconcileEncryptedDerivatives(context.Background(), store.LibraryID("library-enc"), live); err != nil {
		t.Fatalf("reconcile over empty roots must succeed, got %v", err)
	}
}
