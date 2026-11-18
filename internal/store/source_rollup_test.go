package store

import (
	"context"
	"testing"
)

// A genre shelf with a ContainerRollUp lists an episode as its show: two
// genre-linked episodes of one show come back as that show once, a film with
// no season stays itself, and both sort together by title (CORE-2424). The
// same query without the roll-up still lists the episodes.
func TestSourceItemsContainerRollUpListsShowsNotEpisodes(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	importFile := func(path, title string) MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: "library-local", Path: path, Title: title,
			Container: "mkv", Codec: "h264", Class: MediaClassVideo,
			ContentFingerprint: HashMediaFilePath(path),
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	jam := importFile("/mnt/user/tv/The IT Crowd/1e01-Yesterday's Jam.mkv", "Yesterday's Jam")
	hell := importFile("/mnt/user/tv/The IT Crowd/The IT Crowd  Series 3/3 01 From Hell.mkv", "From Hell")
	film := importFile("/media/Movies/Airplane (1980).mkv", "Airplane")
	orphan := importFile("/mnt/user/tv/Loose/Pilot.mkv", "Pilot")

	upsert := func(id EntityID, kind, title string) {
		t.Helper()
		if err := st.UpsertEntity(ctx, Entity{ID: id, LibraryID: "library-local", Kind: kind, Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	relate := func(relation EntityRelation) {
		t.Helper()
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	upsert("entity-comedy", "genre", "Comedy")
	upsert("entity-itcrowd", "show", "The IT Crowd")
	upsert("entity-s1", "season", "Season 1")
	upsert("entity-s3", "season", "Season 3")
	upsert("entity-orphan-season", "season", "Season 1")
	for _, media := range []MediaID{jam, hell, film, orphan} {
		relate(EntityRelation{FromEntityID: "entity-comedy", Name: "genre-of", ToMediaID: media})
	}
	relate(EntityRelation{FromEntityID: "entity-s1", Name: "season-episode", ToMediaID: jam})
	relate(EntityRelation{FromEntityID: "entity-s3", Name: "season-episode", ToMediaID: hell})
	relate(EntityRelation{FromEntityID: "entity-s1", Name: "season-of", ToEntityID: "entity-itcrowd"})
	relate(EntityRelation{FromEntityID: "entity-s3", Name: "season-of", ToEntityID: "entity-itcrowd"})
	// A season with no show keeps its episode listed as itself.
	relate(EntityRelation{FromEntityID: "entity-orphan-season", Name: "season-episode", ToMediaID: orphan})

	local := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	comedy := &EntityFilter{Kind: "genre", Name: "Comedy"}
	rollUp := &ContainerRollUp{MemberRelations: []string{"season-episode"}, ParentRelations: []string{"season-of"}}

	items := mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy, RollUp: rollUp})
	type card struct {
		id   MediaID
		kind string
	}
	var got []card
	for _, item := range items {
		got = append(got, card{item.ID, item.Kind})
	}
	want := []card{{film, "media"}, {orphan, "media"}, {"entity-itcrowd", "entity"}}
	if len(got) != len(want) {
		t.Fatalf("rolled-up Comedy = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rolled-up Comedy = %v, want %v", got, want)
		}
	}
	if items[2].Title != "The IT Crowd" || items[2].Summary != "" {
		t.Fatalf("show card = %+v, want title The IT Crowd and no duration summary", items[2])
	}

	// Paging walks the merged order.
	page := mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy, RollUp: rollUp, Limit: 1, Offset: 2})
	if len(page) != 1 || page[0].ID != "entity-itcrowd" {
		t.Fatalf("page 3 = %v, want [entity-itcrowd]", sourceItemIDs(page))
	}

	// Muting the genre hides the show card just as it hides the episodes, and
	// a direct mute of one episode keeps the show while another survives.
	const profile ProfileID = "profile-default"
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-comedy", true, true); err != nil {
		t.Fatal(err)
	}
	if muted := mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy, RollUp: rollUp, ProfileID: profile}); len(muted) != 0 {
		t.Fatalf("rolled-up Comedy with Comedy muted = %v, want none", sourceItemIDs(muted))
	}
	// A Favorite episode keeps its show card on the muted rail, and the card
	// still carries the Muted badge (§2.7: badge by reach, not by drop set).
	if err := st.SetMediaFavorite(ctx, profile, hell, true, local); err != nil {
		t.Fatal(err)
	}
	favored := mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy, RollUp: rollUp, ProfileID: profile})
	if len(favored) != 1 || favored[0].ID != "entity-itcrowd" {
		t.Fatalf("rolled-up Comedy muted with From Hell favorited = %v, want the show", sourceItemIDs(favored))
	}
	if err := st.AnnotateMuted(ctx, profile, local, favored); err != nil {
		t.Fatal(err)
	}
	if !favored[0].Muted {
		t.Fatal("Favorite-kept show card under a Comedy mute is not badged Muted")
	}
	if err := st.SetMediaFavorite(ctx, profile, hell, false, local); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-comedy", true, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-s1", false, true); err != nil {
		t.Fatal(err)
	}
	if kept := mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy, RollUp: rollUp, ProfileID: profile}); len(kept) != 3 || kept[2].ID != "entity-itcrowd" {
		t.Fatalf("rolled-up Comedy with Season 1 muted = %v, want the show kept for Season 3", sourceItemIDs(kept))
	}
	unmuted := mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy, RollUp: rollUp})
	if err := st.AnnotateMuted(ctx, profile, local, unmuted); err != nil {
		t.Fatal(err)
	}
	for _, item := range unmuted {
		if want := item.ID == "entity-itcrowd"; item.Muted != want {
			t.Fatalf("Muted on %s = %v with Season 1 muted, want %v", item.ID, item.Muted, want)
		}
	}
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-s3", false, true); err != nil {
		t.Fatal(err)
	}
	if kept := mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy, RollUp: rollUp, ProfileID: profile}); len(kept) != 2 {
		t.Fatalf("rolled-up Comedy with both seasons muted = %v, want the film and the orphan", sourceItemIDs(kept))
	}

	// A trashed season no longer absorbs its episode: From Hell is listed as
	// itself while Season 1 still rolls up to the show.
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE entity SET trashed_at_ms = 1 WHERE id = 'entity-s3'`); err != nil {
		t.Fatal(err)
	}
	items = mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy, RollUp: rollUp})
	if ids := sourceItemIDs(items); len(ids) != 4 || ids[1] != hell || ids[3] != "entity-itcrowd" {
		t.Fatalf("Comedy with Season 3 trashed = %v, want [film, From Hell, orphan, show]", ids)
	}

	// A trashed show no longer absorbs its episodes.
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE entity SET trashed_at_ms = 1 WHERE id = 'entity-itcrowd'`); err != nil {
		t.Fatal(err)
	}
	items = mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy, RollUp: rollUp})
	if len(items) != 4 {
		t.Fatalf("Comedy with the show trashed = %v, want the four Media", sourceItemIDs(items))
	}

	// Without the roll-up the feed lists every Media.
	items = mustSourceItems(t, st, local, SourceQuery{Kind: "all-items", EntityFilter: comedy})
	if len(items) != 4 {
		t.Fatalf("Comedy without roll-up = %v, want the four Media", sourceItemIDs(items))
	}
}
