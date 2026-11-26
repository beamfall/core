package store

import (
	"context"
	"testing"
	"time"
)

// TestDeleteProfileErasesItsEncryptedPlaneRows: deleting a Profile removes its
// saved filters, stations and Channels from an Encrypted Library's plane and
// clears it as the ordinal actor there, as the catalog's foreign keys do for
// shared rows. An unsealed plane is erased at once; a sealed plane owes the
// erase and applies it at its next unseal; no erase marker outlives the work.
func TestDeleteProfileErasesItsEncryptedPlaneRows(t *testing.T) {
	ctx := context.Background()
	for _, sealed := range []bool{false, true} {
		st, media := seededEncryptedMedia(t, "profile-delete-plane")
		libraryID := media.LibraryID
		profile, err := st.CreateProfile(ctx, "Departing", ProfileAdult, LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatalf("CreateProfile: %v", err)
		}
		now := time.Now().UTC().Add(-time.Minute)
		if _, err := st.CreateSavedFilter(ctx, SavedFilter{ProfileID: profile.ID, LibraryID: libraryID, Name: "mine", FilterJSON: `{}`}, now); err != nil {
			t.Fatalf("CreateSavedFilter: %v", err)
		}
		if _, err := st.CreateSavedStation(ctx, SavedStation{ProfileID: profile.ID, LibraryID: libraryID, Name: "mine",
			SeedJSON: `{}`, RulesJSON: `{}`, TuningJSON: `{}`}, now); err != nil {
			t.Fatalf("CreateSavedStation: %v", err)
		}
		if _, err := st.CreateChannel(ctx, Channel{LibraryID: libraryID, ProfileID: profile.ID, Name: "mine",
			SourceExpr: `{"kind":"all-items"}`, Ordering: ChannelOrderingChronological, Repeat: ChannelRepeatOff}, now); err != nil {
			t.Fatalf("CreateChannel: %v", err)
		}
		title := "Ordinal holder"
		if _, err := st.CommitMediaIdentification(ctx, media.ID, IdentificationCommit{
			Metadata:   MediaMetadata{Title: &title, OperatorOrdinal: &MediaOrdinal{Sequence: "season", Index: 2}, OrdinalActor: profile.ID},
			Provenance: MediaMetadataProvenance{PluginID: "fixture", ScraperID: "scraper"},
		}); err != nil {
			t.Fatalf("CommitMediaIdentification: %v", err)
		}
		cipher := copyEncryptedLibraryDBCipher{}
		if sealed {
			if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.DeleteProfile(ctx, profile.ID); err != nil {
			t.Fatalf("sealed=%v DeleteProfile: %v", sealed, err)
		}
		markers := func() int {
			return identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM server_config WHERE key = ?`, planeErasurePrefix(libraryID)+string(profile.ID))
		}
		if sealed {
			if n := markers(); n != 1 {
				t.Fatalf("erase markers owed by the sealed plane = %d, want 1", n)
			}
			if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
				t.Fatalf("Unseal: %v", err)
			}
		}
		if n := markers(); n != 0 {
			t.Fatalf("sealed=%v erase markers after the erase = %d", sealed, n)
		}
		plane := st.encryptedDBs[libraryID].reader
		if n := identificationRowCount(t, plane, `
			SELECT (SELECT COUNT(*) FROM saved_filter WHERE profile_id = ?1)
			     + (SELECT COUNT(*) FROM saved_station WHERE profile_id = ?1)
			     + (SELECT COUNT(*) FROM channel WHERE profile_id = ?1)
			     + (SELECT COUNT(*) FROM media_operator_ordinal WHERE actor_profile_id = ?1)`, string(profile.ID)); n != 0 {
			t.Fatalf("sealed=%v plane rows still naming the deleted Profile = %d", sealed, n)
		}
		if n := identificationRowCount(t, plane, `SELECT COUNT(*) FROM media_operator_ordinal WHERE media_id = ? AND ordinal_index = 2`, media.ID); n != 1 {
			t.Fatalf("sealed=%v ordinal rows after the erase = %d, want the ordinal kept", sealed, n)
		}
		_ = st.Close()
	}
}

// CORE-3516 #3: whole-Profile deletion and footprint erasure also remove
// imported private item_state_import rows on unsealed and sealed planes.
func TestDeleteProfileErasesImportedItemStateOnUnsealedAndSealedPlanes(t *testing.T) {
	ctx := context.Background()
	for _, sealed := range []bool{false, true} {
		st, media := seededEncryptedMedia(t, "profile-delete-import-state")
		libraryID := media.LibraryID
		profile, err := st.CreateProfile(ctx, "Departing Import", ProfileAdult, LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatalf("CreateProfile: %v", err)
		}
		plane := st.encryptedDBs[libraryID].writer
		nowMS := time.Now().UTC().Add(-time.Minute).UnixMilli()
		if _, err := plane.ExecContext(ctx, `
			INSERT INTO subscription(id, library_id, feed_url, root, title, added_at_ms) VALUES ('sub-priv', ?, 'https://f.test/rss', '/enc/sub', 'Sub', ?)`,
			libraryID, nowMS); err != nil {
			t.Fatalf("seed subscription: %v", err)
		}
		if _, err := plane.ExecContext(ctx, `
			INSERT INTO item_state_import(profile_id, subscription_id, item_guid, played, progress, starred, imported_at_ms)
			VALUES (?, 'sub-priv', 'guid-secret-1', 1, 0.75, 1, ?)`, profile.ID, nowMS); err != nil {
			t.Fatalf("seed item_state_import: %v", err)
		}
		cipher := copyEncryptedLibraryDBCipher{}
		if sealed {
			if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.DeleteProfile(ctx, profile.ID); err != nil {
			t.Fatalf("sealed=%v DeleteProfile: %v", sealed, err)
		}
		if sealed {
			if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
				t.Fatalf("Unseal: %v", err)
			}
		}
		planeR := st.encryptedDBs[libraryID].reader
		if n := identificationRowCount(t, planeR, `SELECT COUNT(*) FROM item_state_import WHERE profile_id = ?`, string(profile.ID)); n != 0 {
			t.Fatalf("sealed=%v item_state_import rows after DeleteProfile = %d, want 0", sealed, n)
		}
		_ = st.Close()
	}
}

// CORE-3516 #4: recreating a deleted Profile with the same ID and triggering a
// footprint erase while an Encrypted Library is still sealed must not drop
// ProfileDeleted (old private channels are erased while newer channels survive).
func TestRecreatedProfileKeepsOwnChannelsAcrossDeleteAndFootprintErase(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "reuse-profile-channel")
	defer func() { _ = st.Close() }()
	libraryID := media.LibraryID
	clock := time.Unix(1000, 0).UTC()
	st.now = func() time.Time { return clock }

	reusedID := ProfileID("profile-reused-id")
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO profile(id, name, kind) VALUES (?, 'Reuse One', 'adult')`,
		string(reusedID)); err != nil {
		t.Fatalf("insert initial profile: %v", err)
	}
	oldCh, err := st.CreateChannel(ctx, Channel{
		LibraryID: libraryID, ProfileID: reusedID, Name: "Old Private",
		SourceExpr: `{"kind":"all-items"}`, Ordering: ChannelOrderingChronological, Repeat: ChannelRepeatOff,
	}, clock)
	if err != nil {
		t.Fatalf("CreateChannel(old): %v", err)
	}

	cipher := copyEncryptedLibraryDBCipher{}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}

	// Whole-Profile deletion at t=2000 while sealed.
	clock = time.Unix(2000, 0).UTC()
	if err := st.DeleteProfile(ctx, reusedID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}

	// Same Profile ID is recreated at t=3000 and executes a footprint erase at
	// t=4000 while the Library is still sealed.
	clock = time.Unix(3000, 0).UTC()
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO profile(id, name, kind) VALUES (?, 'Reuse Two', 'adult')`,
		string(reusedID)); err != nil {
		t.Fatalf("recreate profile: %v", err)
	}
	clock = time.Unix(4000, 0).UTC()
	if err := st.DeleteProfileFootprintAudited(ctx, reusedID, ActivityEntry{
		OperationID: "op-reuse-footprint", OccurredAt: clock, Action: ActivityOperatorDataDelete,
		ProfileID: reusedID, TargetType: "profile", TargetID: string(reusedID),
	}); err != nil {
		t.Fatalf("DeleteProfileFootprintAudited: %v", err)
	}

	erasures, err := st.pendingPlaneErasuresLocked(ctx, libraryID)
	if err != nil || len(erasures) != 1 || !erasures[0].ProfileDeleted ||
		erasures[0].ProfileDeletedCutoffMS != time.Unix(2000, 0).UTC().UnixMilli() ||
		erasures[0].CutoffMS != time.Unix(4000, 0).UTC().UnixMilli() {
		t.Fatalf("pending erasures = %+v, err=%v", erasures, err)
	}

	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	planeR := st.encryptedDBs[libraryID].reader
	if n := identificationRowCount(t, planeR, `SELECT COUNT(*) FROM channel WHERE id = ?`, string(oldCh.ID)); n != 0 {
		t.Fatalf("old deleted Profile channel survived after subsequent footprint erase: %d", n)
	}
}
