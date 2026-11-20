package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/playback/sessiontest"
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
	sessions := sessiontest.New(t, nil, dataDir, nil)
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

	if err := reconciler.ReconcileEncryptedTranscodeScratch(t.Context(), "library-enc"); err != nil {
		t.Fatal(err)
	}

	mustBeGone(t, "orphaned transcode session dir", filepath.Join(transcodeRoot, "session-7"))
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("non-session sibling must survive the transcode sweep, got stat err=%v", err)
	}
}

func TestEncryptedDerivativeReconcilerFailsClosedOnTranscodeSweepError(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(t.Context(), dataDir, store.Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := filepath.Join(dataDir, "transcode")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	readErr := &os.PathError{Op: "readdir", Path: root, Err: os.ErrPermission}
	failRead := false
	sessions, err := playback.NewSessionsInDir(nil, root, nil, playback.WithScratchReadDir(func(root fs.FS, name string) ([]fs.DirEntry, error) {
		if !failRead {
			return fs.ReadDir(root, name)
		}
		close(entered)
		<-release
		return nil, readErr
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sessions.Close(); err != nil {
			t.Error(err)
		}
	}()
	st.SetEncryptedDerivativeReconciler(newEncryptedDerivativeReconciler(nil, nil, nil, sessions, ""))

	failRead = true
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	libraryID := store.LibraryID("library-enc")
	result := make(chan error, 1)
	go func() {
		result <- st.UnsealEncryptedLibraryDB(t.Context(), libraryID, passthroughAppDBCipher{})
	}()
	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("unseal returned before scratch read: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("unseal never reached scratch read")
	}
	if other, err := playback.NewSessionsInDir(nil, root, nil); err == nil {
		_ = other.Close()
		t.Fatal("another manager acquired the root during unseal")
	}
	read := make(chan error, 1)
	go func() {
		_, err := st.FirstRunSetupComplete(t.Context())
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scratch read held the Store lock")
	}
	if st.EncryptedLibraryDBRegistered(libraryID) {
		t.Fatal("plane registered while scratch read was pending")
	}
	close(release)
	err = <-result
	if !errors.Is(err, readErr) || !strings.Contains(err.Error(), string(libraryID)) {
		t.Fatalf("unseal returned %v, want Library-named scratch read failure", err)
	}
	if st.EncryptedLibraryDBRegistered(libraryID) {
		t.Fatal("failed scratch read published the plane")
	}
	failRead = false
	if err := st.UnsealEncryptedLibraryDB(t.Context(), libraryID, passthroughAppDBCipher{}); err != nil {
		t.Fatalf("retry unseal: %v", err)
	}
	if !st.EncryptedLibraryDBRegistered(libraryID) {
		t.Fatal("successful retry did not publish the plane")
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

// TestEncryptedDerivativeReconcilerKeepsCiphertextSubtitles proves the
// ADR-0235 exception: the unseal reconcile keeps a valid ciphertext object in
// the Library's encrypted subtitle cache and removes plaintext and temps beside
// it. The cache directory is computed the way mediahttp lays it out
// (subtitles/encrypted/<hex of the first 16 bytes of sha256(library ID)>).
func TestEncryptedDerivativeReconcilerKeepsCiphertextSubtitles(t *testing.T) {
	dataDir := t.TempDir()
	artworkSvc := artwork.NewService(nil, nil, dataDir)
	assetSvc := genassets.NewService(nil, nil, dataDir)
	subtitleHandler := mediahttp.NewHandler(nil, nil, nil, artworkSvc, nil)
	subtitleHandler.SetSubtitles(nil, filepath.Join(dataDir, "subtitles"))

	libraryID := store.LibraryID("library-enc")
	sum := sha256.Sum256([]byte(libraryID))
	cacheDir := filepath.Join(dataDir, "subtitles", "encrypted", hex.EncodeToString(sum[:16]))
	ciphertext := filepath.Join(cacheDir, "sha256_fp.0.vtt")
	plaintext := filepath.Join(cacheDir, "sha256_fp.1.vtt")
	temp := filepath.Join(cacheDir, ".libenc-1.tmp")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	header := append([]byte("BFE2"), 0x02, 0x00, 0x01, 0x00, 0x00)
	header = append(header, make([]byte, 64)...)
	if err := os.WriteFile(ciphertext, header, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, plaintext)
	writeFile(t, temp)

	reconciler := newEncryptedDerivativeReconciler(artworkSvc, assetSvc, subtitleHandler, nil, filepath.Join(dataDir, "playback-cache"))
	if err := reconciler.ReconcileEncryptedDerivatives(context.Background(), libraryID, nil); err != nil {
		t.Fatalf("ReconcileEncryptedDerivatives: %v", err)
	}
	if _, err := os.Stat(ciphertext); err != nil {
		t.Fatalf("ciphertext subtitle object removed: %v", err)
	}
	mustBeGone(t, "plaintext in the encrypted subtitle cache", plaintext)
	mustBeGone(t, "staging temp in the encrypted subtitle cache", temp)
}

// TestEncryptedDerivativeReconcilerSweepsEncryptedPrecompute binds the unseal
// reconcile to the encrypted precompute tree: a generation of libencrypt
// objects survives and a generation holding a plaintext segment is removed.
func TestEncryptedDerivativeReconcilerSweepsEncryptedPrecompute(t *testing.T) {
	dataDir := t.TempDir()
	cacheRoot := filepath.Join(dataDir, "playback-cache")
	libraryID := store.LibraryID("library-enc")
	mediaDir := filepath.Join(cacheRoot, ".precompute-encrypted", hex.EncodeToString([]byte(libraryID)), hex.EncodeToString([]byte("media-alpha")))
	kept := filepath.Join(mediaDir, strings.Repeat("a", 64), "seg-00000.m4s")
	leaked := filepath.Join(mediaDir, strings.Repeat("b", 64))
	header := append([]byte("BFE2"), 0x02, 0x00, 0x01, 0x00, 0x00)
	header = append(header, make([]byte, 64)...)
	if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, header, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(leaked, "seg-00000.m4s"))

	reconciler := newEncryptedDerivativeReconciler(nil, nil, nil, nil, cacheRoot)
	if err := reconciler.ReconcileEncryptedDerivatives(context.Background(), libraryID, nil); err != nil {
		t.Fatalf("ReconcileEncryptedDerivatives: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("ciphertext precompute generation removed: %v", err)
	}
	mustBeGone(t, "plaintext generation in the encrypted precompute tree", leaked)
}

// encryptedLibraryIDs is a fixed encryptedLibraryLister.
type encryptedLibraryIDs []store.LibraryID

func (ids encryptedLibraryIDs) EncryptedLibraryIDs(context.Context) ([]store.LibraryID, error) {
	return ids, nil
}

// TestEncryptedAssetPurgeTrustsLedgerOnlyAfterUnsealReconcile pins the
// CORE-2193 review fix through the production adapters: the unseal reconcile
// marks a Library's generated-asset ledger complete, so its purge sweeps
// without a warning, while purging a Library never reconciled since the
// ledger existed logs that its ciphertext may remain.
func TestEncryptedAssetPurgeTrustsLedgerOnlyAfterUnsealReconcile(t *testing.T) {
	dataDir := t.TempDir()
	assetSvc := genassets.NewService(nil, nil, dataDir)
	reconciler := newEncryptedDerivativeReconciler(nil, assetSvc, nil, nil, filepath.Join(dataDir, "playback-cache"))
	reconciled, stale := store.LibraryID("library-reconciled"), store.LibraryID("library-stale")
	if err := reconciler.ReconcileEncryptedDerivatives(context.Background(), reconciled, nil); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var logged strings.Builder
	purger := encryptedAssetPurger{
		assets:    assetSvc,
		libraries: encryptedLibraryIDs{reconciled},
		logger:    slog.New(slog.NewTextHandler(&logged, nil)),
	}

	if err := purger.PurgeEncryptedDerivatives(context.Background(), reconciled); err != nil {
		t.Fatalf("purge reconciled Library: %v", err)
	}
	if logged.Len() != 0 {
		t.Fatalf("purge of a reconciled Library warned: %s", logged.String())
	}
	if err := purger.PurgeEncryptedDerivatives(context.Background(), stale); err != nil {
		t.Fatalf("purge stale Library: %v", err)
	}
	if !strings.Contains(logged.String(), "kept generated-asset ciphertext") {
		t.Fatalf("purge of a never-reconciled Library did not report kept ciphertext: %q", logged.String())
	}
}
