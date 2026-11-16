package genassets

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/store"
)

// encryptedLibraryAssetPurger is the purge seam CORE-2193 adds.
type encryptedLibraryAssetPurger interface {
	PurgeEncryptedLibraryAssets(libraryID store.LibraryID, isObject func(io.Reader) (bool, error)) error
}

// TestPurgeEncryptedLibraryAssetsRemovesUnsharedCiphertext pins CORE-2193: a
// purged Encrypted Library's generated-asset ciphertext under fingerprints no
// other Library references is removed with its generations, while a
// fingerprint another Encrypted Library references keeps its assets, and a
// public Library's plaintext asset beside the ciphertext stays.
func TestPurgeEncryptedLibraryAssetsRemovesUnsharedCiphertext(t *testing.T) {
	f := newEncryptedFixture(t)
	if err := f.generateAll(t); err != nil {
		t.Fatalf("generateAll: %v", err)
	}
	purged := f.audio.LibraryID
	if f.image.LibraryID != purged {
		t.Fatalf("fixture image Library = %s, want %s", f.image.LibraryID, purged)
	}
	// Another Encrypted Library holds the same audio bytes (attributed at its
	// unseal reconcile).
	other := store.LibraryID("library-other")
	if err := f.svc.ReconcileEncryptedFingerprint(other, f.audioFP, libencrypt.HasObjectHeader); err != nil {
		t.Fatalf("ReconcileEncryptedFingerprint(other): %v", err)
	}
	// A public Library's plaintext asset at the image fingerprint.
	publicAsset := filepath.Join(f.svc.assetDir(f.imageFP), WaveformName)
	if err := os.WriteFile(publicAsset, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	purger, ok := any(f.svc).(encryptedLibraryAssetPurger)
	if !ok {
		t.Fatal("genassets.Service cannot purge an Encrypted Library's generated assets")
	}
	if err := purger.PurgeEncryptedLibraryAssets(purged, libencrypt.HasObjectHeader); err != nil {
		t.Fatalf("PurgeEncryptedLibraryAssets: %v", err)
	}

	enriched := filepath.Join(f.svc.assetDir(f.imageFP), ImageEnrichName)
	if _, err := os.Lstat(enriched); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unshared ciphertext %s survived the purge: %v", ImageEnrichName, err)
	}
	if data, err := os.ReadFile(publicAsset); err != nil || !json.Valid(data) { // #nosec G304 -- test-owned temp path.
		t.Fatalf("public plaintext asset beside the ciphertext was removed: %q, %v", data, err)
	}
	if data := decryptAsset(t, f.svc, purged, f.audioFP, WaveformName); !json.Valid(data) {
		t.Fatalf("shared waveform decrypted to %q", data)
	}
	if removed, err := f.svc.SweepPublicationResidue(); err != nil || removed != 0 {
		t.Fatalf("purge left %d unreferenced generations (err %v)", removed, err)
	}
	if _, err := os.Lstat(filepath.Join(f.svc.dir, encryptedRefsLibraryRel(purged))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("purged Library's reference ledger survived: %v", err)
	}

	// Once the last referencing Library is purged too, the shared assets go.
	if err := purger.PurgeEncryptedLibraryAssets(other, libencrypt.HasObjectHeader); err != nil {
		t.Fatalf("PurgeEncryptedLibraryAssets(other): %v", err)
	}
	if _, err := os.Lstat(f.svc.assetDir(f.audioFP)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("shared ciphertext survived the last referencing Library's purge: %v", err)
	}
}
