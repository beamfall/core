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
