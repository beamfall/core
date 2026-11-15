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
