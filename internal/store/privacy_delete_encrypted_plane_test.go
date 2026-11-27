package store

import (
	"context"
	"testing"
	"time"
)

// CORE-0768 / CORE-0769: a Profile footprint erase reaches the saved filters,
// saved Stations and smart Collection authorship an Encrypted Library keeps on
// its own plane, whether that plane is unsealed now or sealed until later.

func planeProfileFootprint(t *testing.T, st *Store, libraryID LibraryID, profileID ProfileID) map[string]int {
	t.Helper()
	reader := st.encryptedDBs[libraryID].reader
	out := map[string]int{}
	for table, query := range map[string]string{
		"saved_filter":     `SELECT COUNT(*) FROM saved_filter WHERE profile_id = ?`,
		"saved_station":    `SELECT COUNT(*) FROM saved_station WHERE profile_id = ?`,
		"smart_collection": `SELECT COUNT(*) FROM smart_collection WHERE created_by_profile_id = ?`,
		"channel":          `SELECT COUNT(*) FROM channel WHERE profile_id = ?`,
	} {
		var n int
		if err := reader.QueryRowContext(context.Background(), query, profileID).Scan(&n); err != nil {
			t.Fatalf("count plane %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

func planeErasureMarkerCount(t *testing.T, st *Store) int {
	t.Helper()
	var n int
	if err := st.catalogR.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM server_config WHERE key LIKE 'privacy.plane-erase:%'`).Scan(&n); err != nil {
		t.Fatalf("count markers: %v", err)
	}
	return n
}

func footprintDeleteActivity(id string) ActivityEntry {
	return ActivityEntry{
		OperationID: id, OccurredAt: time.Now().UTC(), Action: ActivityOperatorDataDelete,
		ProfileID: "profile-default", TargetType: "profile", TargetID: "profile-default",
	}
}

func TestProfileFootprintDeleteErasesUnsealedPlaneRows(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "plane-erase-unsealed")
	defer st.Close()
	libraryID := media.LibraryID
	createExpressionFixture(t, st, libraryID, "plane-erase-unsealed", time.Now().Add(-time.Hour).UTC())

	if err := st.DeleteProfileFootprintAudited(ctx, "profile-default", footprintDeleteActivity("plane-erase-unsealed")); err != nil {
		t.Fatalf("DeleteProfileFootprintAudited: %v", err)
	}
	got := planeProfileFootprint(t, st, libraryID, "profile-default")
	want := map[string]int{"saved_filter": 0, "saved_station": 0, "smart_collection": 0, "channel": 1}
	for table, n := range want {
		if got[table] != n {
			t.Fatalf("plane %s for erased Profile = %d, want %d", table, got[table], n)
		}
	}
	if n := planeErasureMarkerCount(t, st); n != 0 {
		t.Fatalf("plane erasure markers after unsealed erase = %d, want 0", n)
	}
}

func TestProfileFootprintDeleteErasesSealedPlaneAtNextUnseal(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "plane-erase-sealed")
	defer st.Close()
	libraryID := media.LibraryID
	createExpressionFixture(t, st, libraryID, "plane-erase-sealed", time.Now().Add(-time.Hour).UTC())
	// A row stamped after the erase stands for one the Profile creates later;
	// the cutoff must keep it.
	if _, err := st.CreateSavedFilter(ctx, SavedFilter{
		ProfileID: "profile-default", LibraryID: libraryID, Name: "later", FilterJSON: `{}`,
	}, time.Now().Add(24*time.Hour).UTC()); err != nil {
		t.Fatalf("CreateSavedFilter(later): %v", err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := st.DeleteProfileFootprintAudited(ctx, "profile-default", footprintDeleteActivity("plane-erase-sealed")); err != nil {
		t.Fatalf("DeleteProfileFootprintAudited while sealed: %v", err)
	}
	if n := planeErasureMarkerCount(t, st); n != 1 {
		t.Fatalf("plane erasure markers while sealed = %d, want 1", n)
	}

	// An unseal whose shared evacuation transaction fails discards the
	// working plane, so the marker must survive for the retry.
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER test_block_plane_erase_marker BEFORE DELETE ON server_config
		WHEN OLD.key LIKE 'privacy.plane-erase:%'
		BEGIN SELECT RAISE(ABORT, 'blocked by test'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err == nil {
		t.Fatal("unseal with blocked marker delete succeeded")
	}
	if n := planeErasureMarkerCount(t, st); n != 1 {
		t.Fatalf("plane erasure markers after failed unseal = %d, want 1", n)
	}
	if _, err := st.catalogW.ExecContext(ctx, `DROP TRIGGER test_block_plane_erase_marker`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}

	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("retry Unseal: %v", err)
	}
	got := planeProfileFootprint(t, st, libraryID, "profile-default")
	want := map[string]int{"saved_filter": 1, "saved_station": 0, "smart_collection": 0, "channel": 1}
	for table, n := range want {
		if got[table] != n {
			t.Fatalf("plane %s for erased Profile after unseal = %d, want %d", table, got[table], n)
		}
	}
	if n := planeErasureMarkerCount(t, st); n != 0 {
		t.Fatalf("plane erasure markers after unseal = %d, want 0", n)
	}
}
