package store

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

// TestOfflinePackSealRacingCatalogPurgeLeavesNoPack pins CORE-1851 for a
// catalog Library: a purge that commits after the pack's items were found
// but before the pack is written refuses the pack instead of sealing items
// that name removed Media.
func TestOfflinePackSealRacingCatalogPurgeLeavesNoPack(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	var library LibraryID
	if err := st.catalogR.QueryRowContext(ctx, `SELECT library_id FROM media WHERE id = 'media-alpha'`).Scan(&library); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLibrary(ctx, library); err != nil {
		t.Fatalf("DeleteLibrary: %v", err)
	}
	var purgeErr error
	st.offlinePackSealHook = func() {
		// CreateOfflinePack holds the read lock here, as purgeLibrary does
		// around this call.
		_, _, purgeErr = st.purgeLibraryLocked(context.Background(), library, math.MaxInt64, false)
	}

	_, err := st.CreateOfflinePack(ctx, OfflinePack{ProfileID: "profile-default", TargetKind: "media"},
		[]OfflinePackItem{{MediaID: "media-alpha"}}, time.Now().UTC())
	if purgeErr != nil {
		t.Fatalf("purge in the seal window: %v", purgeErr)
	}
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("CreateOfflinePack after a racing purge = %v, want a validation error", err)
	}
	assertNoOfflinePackRows(t, st)
}

// TestOfflinePackSealRacingEncryptedPurgeLeavesNoPack pins CORE-1851 for an
// Encrypted Library, whose Media stays readable in its private database until
// the purge's teardown: the purge's catalog commit alone refuses the pack.
func TestOfflinePackSealRacingEncryptedPurgeLeavesNoPack(t *testing.T) {
	ctx := context.Background()
	st, library, media := trashedEncryptedLibrary(t)
	st.SetEncryptedLibraryKeyEraser(&fakeKeyEraser{})
	var purgeErr error
	st.offlinePackSealHook = func() {
		_, _, purgeErr = st.purgeLibraryLocked(ctx, library, math.MaxInt64, true)
	}

	_, err := st.CreateOfflinePack(ctx, OfflinePack{ProfileID: "profile-default", TargetKind: "media"},
		[]OfflinePackItem{{MediaID: media.ID}}, time.Now().UTC())
	if purgeErr != nil {
		t.Fatalf("purge in the seal window: %v", purgeErr)
	}
	if err := st.completeLibraryPurge(ctx, library); err != nil {
		t.Fatalf("completeLibraryPurge: %v", err)
	}
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("CreateOfflinePack after a racing purge = %v, want a validation error", err)
	}
	assertNoOfflinePackRows(t, st)
}

func assertNoOfflinePackRows(t *testing.T, st *Store) {
	t.Helper()
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM offline_pack`); got != 0 {
		t.Fatalf("offline packs = %d, want 0", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM offline_pack_item`); got != 0 {
		t.Fatalf("offline pack items = %d, want 0", got)
	}
}

// TestPurgeUnsealedEncryptedLibraryRemovesItsPackItems pins CORE-1852: the
// catalog media delete trigger never sees an Encrypted Library's Media, so
// the purge itself removes the pack items naming it, and keeps the items of
// another Library in the same pack.
func TestPurgeUnsealedEncryptedLibraryRemovesItsPackItems(t *testing.T) {
	ctx := context.Background()
	st, library, media := trashedEncryptedLibrary(t)
	st.SetEncryptedLibraryKeyEraser(&fakeKeyEraser{})
	pack := createPackOverEncryptedMedia(t, st, library, media.ID)

	if _, err := st.PurgeLibrary(ctx, library); err != nil {
		t.Fatalf("PurgeLibrary: %v", err)
	}
	assertOnlyArchivePackItem(t, st, pack)
}

// TestPurgeSealedEncryptedLibraryRemovesItsPackItems pins CORE-1852 for a
// sealed Library, whose Media ids cannot be listed: the pack items no
// readable plane holds go, and the other Library's item stays.
func TestPurgeSealedEncryptedLibraryRemovesItsPackItems(t *testing.T) {
	ctx := context.Background()
	st, library, media := trashedEncryptedLibrary(t)
	st.SetEncryptedLibraryKeyEraser(&fakeKeyEraser{})
	pack := createPackOverEncryptedMedia(t, st, library, media.ID)
	if err := st.SealEncryptedLibraryDB(ctx, library, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}

	if _, err := st.PurgeLibrary(ctx, library); err != nil {
		t.Fatalf("PurgeLibrary: %v", err)
	}
	assertOnlyArchivePackItem(t, st, pack)
}

// createPackOverEncryptedMedia seals a pack holding the encrypted Media and
// the seeded catalog Media of another Library.
func createPackOverEncryptedMedia(t *testing.T, st *Store, library LibraryID, media MediaID) OfflinePackID {
	t.Helper()
	pack, err := st.CreateOfflinePack(t.Context(), OfflinePack{
		ProfileID: "profile-default", TargetKind: "media", State: OfflinePackReady,
		LibraryIDs: []LibraryID{library, "library-archive"},
	}, []OfflinePackItem{{MediaID: media}, {MediaID: "media-archive"}}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateOfflinePack: %v", err)
	}
	return pack.ID
}

func assertOnlyArchivePackItem(t *testing.T, st *Store, pack OfflinePackID) {
	t.Helper()
	items, err := st.OfflinePackItems(t.Context(), pack)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].MediaID != "media-archive" {
		t.Fatalf("pack items after purge = %+v, want only media-archive", items)
	}
}
