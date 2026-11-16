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
	PurgeEncryptedLibraryAssets(libraryID store.LibraryID, others []store.LibraryID, isObject func(io.Reader) (bool, error)) (EncryptedAssetPurge, error)
}

// markComplete records that each Library's unseal reconcile finished, so its
// reference ledger lists every directory it uses.
func markComplete(t *testing.T, svc *Service, libraries ...store.LibraryID) {
	t.Helper()
	for _, library := range libraries {
		if err := svc.MarkEncryptedReferencesComplete(library); err != nil {
			t.Fatalf("MarkEncryptedReferencesComplete(%s): %v", library, err)
		}
	}
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

	markComplete(t, f.svc, purged, other)

	purger, ok := any(f.svc).(encryptedLibraryAssetPurger)
	if !ok {
		t.Fatal("genassets.Service cannot purge an Encrypted Library's generated assets")
	}
	if result, err := purger.PurgeEncryptedLibraryAssets(purged, []store.LibraryID{purged, other}, libencrypt.HasObjectHeader); err != nil || result.Unswept != "" {
		t.Fatalf("PurgeEncryptedLibraryAssets = %+v, %v; want a complete sweep", result, err)
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
	if result, err := purger.PurgeEncryptedLibraryAssets(other, []store.LibraryID{other}, libencrypt.HasObjectHeader); err != nil || result.Unswept != "" {
		t.Fatalf("PurgeEncryptedLibraryAssets(other) = %+v, %v; want a complete sweep", result, err)
	}
	if _, err := os.Lstat(f.svc.assetDir(f.audioFP)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("shared ciphertext survived the last referencing Library's purge: %v", err)
	}
}

// TestPurgeEncryptedLibraryAssetsKeepsCiphertextAnIncompleteLedgerMayShare
// pins the CORE-2193 review fix: an Encrypted Library whose ciphertext predates
// the ledger and that has not been unsealed since has no complete ledger, so a
// purge of another Library sharing a fingerprint must keep the directory
// rather than delete the sealed Library's ciphertext. Purging the Library with
// no complete ledger reports that ciphertext may remain.
func TestPurgeEncryptedLibraryAssetsKeepsCiphertextAnIncompleteLedgerMayShare(t *testing.T) {
	f := newEncryptedFixture(t)
	if err := f.generateAll(t); err != nil {
		t.Fatalf("generateAll: %v", err)
	}
	purged := f.audio.LibraryID
	// sealedSince holds the same audio ciphertext from before the ledger and
	// stays Sealed after the upgrade, so it has no ledger at all.
	sealedSince := store.LibraryID("library-sealed-since-upgrade")
	markComplete(t, f.svc, purged)

	result, err := f.svc.PurgeEncryptedLibraryAssets(purged, []store.LibraryID{purged, sealedSince}, libencrypt.HasObjectHeader)
	if err != nil {
		t.Fatalf("PurgeEncryptedLibraryAssets: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.svc.assetDir(f.audioFP), WaveformName)); err != nil {
		t.Fatalf("ciphertext another Library may reference was removed: %v", err)
	}
	if result.Unswept == "" {
		t.Fatal("purge that kept possibly shared ciphertext reported a complete sweep")
	}

	result, err = f.svc.PurgeEncryptedLibraryAssets(sealedSince, nil, libencrypt.HasObjectHeader)
	if err != nil {
		t.Fatalf("PurgeEncryptedLibraryAssets(sealedSince): %v", err)
	}
	if result.Unswept == "" {
		t.Fatal("purge of a Library with no complete ledger reported a complete sweep")
	}
}

// TestEncryptedPublicationRefusedAfterLibraryAssetPurge pins the CORE-2193
// review fix: a generation that selected its encrypted target before the purge
// and commits after it must not republish ciphertext or a ledger for the
// purged Library.
func TestEncryptedPublicationRefusedAfterLibraryAssetPurge(t *testing.T) {
	f := newEncryptedFixture(t)
	library := f.audio.LibraryID
	publication, err := f.svc.beginPublication(string(f.audio.ID), f.audioFP, "waveform:v1", WaveformName)
	if err != nil {
		t.Fatal(err)
	}
	defer publication.close()
	publication.encrypted = &encryptedTarget{libraryID: library, objects: managerObjects{m: f.manager}}
	if err := publication.writeStaged(WaveformName, []byte(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}

	markComplete(t, f.svc, library)
	if _, err := f.svc.PurgeEncryptedLibraryAssets(library, []store.LibraryID{library}, libencrypt.HasObjectHeader); err != nil {
		t.Fatalf("PurgeEncryptedLibraryAssets: %v", err)
	}
	if err := publication.commit(WaveformName, publication.publicName); !errors.Is(err, ErrEncryptedLibraryPurged) {
		t.Fatalf("commit after the purge = %v, want ErrEncryptedLibraryPurged", err)
	}
	if _, err := os.Lstat(filepath.Join(f.svc.assetDir(f.audioFP), WaveformName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("purged Library's ciphertext was republished: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.svc.dir, encryptedRefsLibraryRel(library))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("purged Library's reference ledger was recreated: %v", err)
	}
}
