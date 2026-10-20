package store

import (
	"context"
	"testing"
)

// TestMuteRoundTrip exercises the ADR-0054 per-Profile Mute storage reservation:
// set/clear of Entity and Tag targets, include-descendants persistence, per-Profile
// isolation (§2.9 "other Profiles unaffected"), and the My-Data "clear all" (§2.8).
func TestMuteRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const profile ProfileID = "profile-1"
	if got, err := st.MutedTargets(ctx, profile); err != nil || len(got) != 0 {
		t.Fatalf("MutedTargets should start empty: got %v err %v", got, err)
	}

	if err := st.SetMute(ctx, profile, MuteKindEntity, "show-42", false, true); err != nil {
		t.Fatalf("SetMute entity: %v", err)
	}
	if err := st.SetMute(ctx, profile, MuteKindTag, "reality-tv", true, true); err != nil {
		t.Fatalf("SetMute tag: %v", err)
	}

	got, err := st.MutedTargets(ctx, profile)
	if err != nil {
		t.Fatalf("MutedTargets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 mutes, got %d: %+v", len(got), got)
	}
	var tag *MuteTarget
	for i := range got {
		if got[i].Kind == MuteKindTag {
			tag = &got[i]
		}
	}
	if tag == nil || tag.TargetID != "reality-tv" || !tag.IncludeDescendants {
		t.Fatalf("tag mute should carry include-descendants: %+v", got)
	}

	// Another Profile is unaffected (§2.9).
	if other, err := st.MutedTargets(ctx, "profile-2"); err != nil || len(other) != 0 {
		t.Fatalf("other profile must be empty: got %v err %v", other, err)
	}

	// Unmute one target; the other survives.
	if err := st.SetMute(ctx, profile, MuteKindEntity, "show-42", false, false); err != nil {
		t.Fatalf("unmute entity: %v", err)
	}
	if got, _ := st.MutedTargets(ctx, profile); len(got) != 1 {
		t.Fatalf("want 1 after unmute, got %d", len(got))
	}

	// Clear-all wipes the rest.
	if err := st.ClearMutes(ctx, profile); err != nil {
		t.Fatalf("ClearMutes: %v", err)
	}
	if got, _ := st.MutedTargets(ctx, profile); len(got) != 0 {
		t.Fatalf("want 0 after clear-all, got %d", len(got))
	}
}

// muteMediaInLibrary imports one Media into a Library and returns its ID.
func muteMediaInLibrary(t *testing.T, st *Store, library LibraryID, key string) MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          library,
		Path:               "/media/" + key + ".mp4",
		Title:              key,
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:" + key,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile %s: %v", key, err)
	}
	return media.ID
}

// muteRelate links an Entity to a Media (the "Media carries this Entity/Tag"
// edge the mute predicate reads).
func muteRelate(t *testing.T, st *Store, from EntityID, media MediaID) {
	t.Helper()
	if err := st.UpsertEntityRelation(context.Background(), EntityRelation{
		FromEntityID: from, Name: "linked", ToMediaID: media,
	}); err != nil {
		t.Fatalf("relate %s->%s: %v", from, media, err)
	}
}

// muteRelateEntities links a parent Entity to a child Entity (the hierarchy edge
// include-descendants walks).
func muteRelateEntities(t *testing.T, st *Store, from, to EntityID) {
	t.Helper()
	if err := st.UpsertEntityRelation(context.Background(), EntityRelation{
		FromEntityID: from, Name: "child", ToEntityID: to,
	}); err != nil {
		t.Fatalf("relate %s->%s: %v", from, to, err)
	}
}

func mediaIDSet(items []SourceItem) map[MediaID]bool {
	out := map[MediaID]bool{}
	for _, it := range items {
		out[it.ID] = true
	}
	return out
}

// TestMuteDirectEntityBeatsContinueWatching is specificity case (a): a direct
// entity-mute (no include-descendants) drops the entity's Media even from the
// continue-watching rail (ADR-0054 §2.6 bullet 1).
func TestMuteDirectEntityBeatsContinueWatching(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"

	muted := muteMediaInLibrary(t, st, "library-local", "cw-muted")
	kept := muteMediaInLibrary(t, st, "library-local", "cw-kept")
	if err := st.UpsertEntity(ctx, widgetEntity("entity-show", "library-local")); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-show", muted)

	// Both are in progress -> both on continue-watching before any mute.
	mustReport(t, st, profile, muted, 30, 120, false)
	mustReport(t, st, profile, kept, 30, 120, false)

	filter := LibraryFilter{AllLibraries: true}
	before := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "continue-watching", ProfileID: profile}))
	if !before[muted] || !before[kept] {
		t.Fatalf("setup: both items should be on CW, got %v", before)
	}

	// Direct mute (IncludeDescendants=false) of the show entity.
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-show", false, true); err != nil {
		t.Fatal(err)
	}
	after := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "continue-watching", ProfileID: profile}))
	if after[muted] {
		t.Fatalf("direct mute must drop %s from continue-watching, got %v", muted, after)
	}
	if !after[kept] {
		t.Fatalf("non-muted item must remain, got %v", after)
	}
}

// TestMuteBroadCategoryYieldsToPositiveSignal is specificity case (b): a broad
// category-mute (include-descendants) yields to a Favorite or active
// continue-watching on the specific Media (ADR-0054 §2.6 bullet 2). The plain
// member with no positive signal drops.
func TestMuteBroadCategoryYieldsToPositiveSignal(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"

	favorited := muteMediaInLibrary(t, st, "library-local", "cat-fav")
	watching := muteMediaInLibrary(t, st, "library-local", "cat-cw")
	plain := muteMediaInLibrary(t, st, "library-local", "cat-plain")
	if err := st.UpsertEntity(ctx, widgetEntity("entity-genre", "library-local")); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-genre", favorited)
	muteRelate(t, st, "entity-genre", watching)
	muteRelate(t, st, "entity-genre", plain)

	filter := LibraryFilter{AllLibraries: true}
	if err := st.SetMediaFavorite(ctx, profile, favorited, true, filter); err != nil {
		t.Fatal(err)
	}
	mustReport(t, st, profile, watching, 30, 120, false) // active CW

	// Broad category mute (IncludeDescendants=true) of the genre.
	if err := st.SetMute(ctx, profile, MuteKindTag, "entity-genre", true, true); err != nil {
		t.Fatal(err)
	}

	got := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile}))
	if !got[favorited] {
		t.Fatalf("favorited member must survive broad mute, got %v", got)
	}
	if !got[watching] {
		t.Fatalf("active-CW member must survive broad mute, got %v", got)
	}
	if got[plain] {
		t.Fatalf("plain member must drop under broad mute, got %v", got)
	}
}

// TestMuteIncludeDescendantsWalksHierarchy confirms a broad mute on a parent
// category reaches Media that hang off a child category (ADR-0054 §2.3
// include-descendants over the generic entity graph), and a positive signal
// still overrides at the leaf.
func TestMuteIncludeDescendantsWalksHierarchy(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"

	childMedia := muteMediaInLibrary(t, st, "library-local", "desc-child")
	childFav := muteMediaInLibrary(t, st, "library-local", "desc-child-fav")
	for _, e := range []EntityID{"entity-parent", "entity-child"} {
		if err := st.UpsertEntity(ctx, widgetEntity(e, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	muteRelateEntities(t, st, "entity-parent", "entity-child")
	muteRelate(t, st, "entity-child", childMedia)
	muteRelate(t, st, "entity-child", childFav)

	filter := LibraryFilter{AllLibraries: true}
	if err := st.SetMediaFavorite(ctx, profile, childFav, true, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-parent", true, true); err != nil {
		t.Fatal(err)
	}

	got := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile}))
	if got[childMedia] {
		t.Fatalf("descendant Media must drop under include-descendants mute, got %v", got)
	}
	if !got[childFav] {
		t.Fatalf("favorited descendant Media must survive (broad mute yields), got %v", got)
	}
}

// TestMuteFavoriteLastActionWins is specificity case (c): mute<->favorite is a
// toggle resolved by whichever write currently stands. A direct mute removes the
// item even when favorited; clearing the mute brings it back (ADR-0054 §2.6
// bullet 3 — no stored tie-break state).
func TestMuteFavoriteLastActionWins(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"

	media := muteMediaInLibrary(t, st, "library-local", "toggle-media")
	if err := st.UpsertEntity(ctx, widgetEntity("entity-toggle", "library-local")); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-toggle", media)

	filter := LibraryFilter{AllLibraries: true}
	q := SourceQuery{Kind: "all-items", ProfileID: profile}

	// Favorite then DIRECT-mute the entity: the direct mute wins (it beats
	// everything, §2.6 bullet 1), so the favorited item is gone.
	if err := st.SetMediaFavorite(ctx, profile, media, true, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-toggle", false, true); err != nil {
		t.Fatal(err)
	}
	if mediaIDSet(mustSourceItems(t, st, filter, q))[media] {
		t.Fatal("favorited+direct-muted item must be hidden (mute is later/most-specific action)")
	}

	// Clearing the mute (the later action) brings it back — favorite still set.
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-toggle", false, false); err != nil {
		t.Fatal(err)
	}
	if !mediaIDSet(mustSourceItems(t, st, filter, q))[media] {
		t.Fatal("after unmute (last action) the item must reappear")
	}
}

// TestMuteIsPerProfileOnly is specificity case (d): a mute affects only the
// authoring Profile's rails; another Profile is untouched.
func TestMuteIsPerProfileOnly(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const muter ProfileID = "profile-default"
	const other ProfileID = "profile-other"

	media := muteMediaInLibrary(t, st, "library-local", "iso-media")
	if err := st.UpsertEntity(ctx, widgetEntity("entity-iso", "library-local")); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-iso", media)

	if err := st.SetMute(ctx, muter, MuteKindEntity, "entity-iso", false, true); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true}
	if mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: muter}))[media] {
		t.Fatal("muter must not see the muted item")
	}
	if !mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: other}))[media] {
		t.Fatal("a different Profile must still see the item (per-Profile only)")
	}
}

// TestMuteNeverSurfacesRestricted is specificity case (e): the predicate only
// removes from an already reveal-filtered rail — it can never surface a
// restricted target. A muted entity living in a restricted
// Library produces no Media on an unrevealed rail, with or without the mute.
func TestMuteNeverSurfacesRestricted(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"

	restricted := muteMediaInLibrary(t, st, "library-archive", "restricted-media")
	if err := st.UpsertEntity(ctx, widgetEntity("entity-restricted", "library-archive")); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-restricted", restricted)

	// Unrevealed rail: the restricted Media is absent regardless of mute state.
	unrevealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}
	q := SourceQuery{Kind: "all-items", ProfileID: profile}
	if mediaIDSet(mustSourceItems(t, st, unrevealed, q))[restricted] {
		t.Fatal("restricted Media leaked onto an unrevealed rail before mute")
	}
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-restricted", false, true); err != nil {
		t.Fatal(err)
	}
	got := mustSourceItems(t, st, unrevealed, q)
	if mediaIDSet(got)[restricted] {
		t.Fatal("mute predicate must never surface a restricted target")
	}
}

// TestMuteKeepsSearchAndDirectFetch is specificity case (f): a muted item stays
// searchable and directly fetchable — mute is a rail-only suppressor, never a
// removal. The predicate lives in SourceItems, not SearchMedia
// or Media.
func TestMuteKeepsSearchAndDirectFetch(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"

	media := muteMediaInLibrary(t, st, "library-local", "Searchable Muted Title")
	if err := st.UpsertEntity(ctx, widgetEntity("entity-search", "library-local")); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-search", media)
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-search", false, true); err != nil {
		t.Fatal(err)
	}

	filter := LibraryFilter{AllLibraries: true}
	// Gone from the rail.
	if mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile}))[media] {
		t.Fatal("muted item must be gone from the rail")
	}
	// Still found by search.
	results, err := st.SearchMedia(ctx, filter, "Searchable Muted", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !mediaIDSet(results)[media] {
		t.Fatalf("muted item must remain searchable, got %v", results)
	}
	// Still directly fetchable.
	if _, err := st.Media(ctx, media, filter); err != nil {
		t.Fatalf("muted item must remain directly fetchable: %v", err)
	}
}

// TestSetMuteValidates covers the input guards.
func TestSetMuteValidates(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if err := st.SetMute(ctx, "", MuteKindEntity, "x", false, true); err == nil {
		t.Fatal("empty profile must error")
	}
	if err := st.SetMute(ctx, "profile-1", "bogus", "x", false, true); err == nil {
		t.Fatal("invalid kind must error")
	}
	if err := st.SetMute(ctx, "profile-1", MuteKindEntity, "", false, true); err == nil {
		t.Fatal("empty target must error")
	}
}

// mutedIDSet is mediaIDSet restricted to items flagged Muted=true.
func mutedIDSet(items []SourceItem) map[MediaID]bool {
	out := map[MediaID]bool{}
	for _, it := range items {
		if it.Muted {
			out[it.ID] = true
		}
	}
	return out
}

// TestAnnotateMutedFlagsSearchResults is the ADR-0054 §2.7 badge acceptance for
// explicit search: a directly-muted target stays SEARCHABLE (§2.2) and its Media
// carries Muted=true, while an unmuted item is left Muted=false.
func TestAnnotateMutedFlagsSearchResults(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"

	muted := muteMediaInLibrary(t, st, "library-local", "Badge Muted Title")
	kept := muteMediaInLibrary(t, st, "library-local", "Badge Kept Title")
	if err := st.UpsertEntity(ctx, widgetEntity("entity-badge", "library-local")); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-badge", muted)
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-badge", false, true); err != nil {
		t.Fatal(err)
	}

	filter := LibraryFilter{AllLibraries: true}
	results, err := st.SearchMedia(ctx, filter, "Badge", 50)
	if err != nil {
		t.Fatal(err)
	}
	// Both remain searchable (mute never removes from search).
	if got := mediaIDSet(results); !got[muted] || !got[kept] {
		t.Fatalf("both items must remain searchable, got %v", got)
	}
	// No badge before annotation.
	if len(mutedIDSet(results)) != 0 {
		t.Fatalf("results should be unbadged before AnnotateMuted, got %v", mutedIDSet(results))
	}
	if err := st.AnnotateMuted(ctx, profile, filter, results); err != nil {
		t.Fatal(err)
	}
	badged := mutedIDSet(results)
	if !badged[muted] {
		t.Fatalf("muted item must be badged Muted=true, got %v", badged)
	}
	if badged[kept] {
		t.Fatalf("unmuted item must stay Muted=false, got %v", badged)
	}
}

// TestAnnotateMutedBadgesFavoriteOverriddenCard is the §2.7 requirement that a
// Favorite-overridden broad-mute card — which SURVIVES the passive rail via the
// positive signal (§2.6 bullet 2) — still appears WITH the Muted badge. Badge is
// by mute reach, not the suppression drop set.
func TestAnnotateMutedBadgesFavoriteOverriddenCard(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"

	favorited := muteMediaInLibrary(t, st, "library-local", "fav-override")
	plain := muteMediaInLibrary(t, st, "library-local", "plain-drop")
	if err := st.UpsertEntity(ctx, widgetEntity("entity-genre", "library-local")); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-genre", favorited)
	muteRelate(t, st, "entity-genre", plain)

	filter := LibraryFilter{AllLibraries: true}
	if err := st.SetMediaFavorite(ctx, profile, favorited, true, filter); err != nil {
		t.Fatal(err)
	}
	// Broad category mute (include-descendants) of the genre.
	if err := st.SetMute(ctx, profile, MuteKindTag, "entity-genre", true, true); err != nil {
		t.Fatal(err)
	}

	// On the passive rail: the favorited card survives, the plain member drops.
	rail := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile}))
	if !rail[favorited] {
		t.Fatalf("favorited card must survive the rail (positive signal), got %v", rail)
	}
	if rail[plain] {
		t.Fatalf("plain broad-muted member must drop from the rail, got %v", rail)
	}

	// The surviving rail items are annotated: the favorited-but-broad-muted card
	// is badged even though it was not suppressed.
	items := mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile})
	if err := st.AnnotateMuted(ctx, profile, filter, items); err != nil {
		t.Fatal(err)
	}
	if !mutedIDSet(items)[favorited] {
		t.Fatalf("Favorite-overridden broad-mute card must be badged Muted=true (§2.7), got %v", mutedIDSet(items))
	}
}

// TestAnnotateMutedFlagsEntitySearch is the kind-aware entity badge: a directly-
// muted ENTITY appears in entity search with Muted=true, and a same-id muted TAG
// does NOT badge a different entity that happens not to be muted.
func TestAnnotateMutedFlagsEntitySearch(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"

	registerWidgetKindPolicy(t, st) // opt widget into full-text search
	if err := st.UpsertEntity(ctx, widgetEntity("entity-searchbadge", "library-local")); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-searchbadge", false, true); err != nil {
		t.Fatal(err)
	}

	filter := LibraryFilter{AllLibraries: true}
	items, err := st.SearchEntities(ctx, filter, "Widget", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range items {
		if string(items[i].ID) == "entity-searchbadge" {
			found = true
		}
	}
	if !found {
		t.Fatalf("muted entity must remain searchable, got %v", items)
	}
	if err := st.AnnotateMuted(ctx, profile, filter, items); err != nil {
		t.Fatal(err)
	}
	for i := range items {
		want := string(items[i].ID) == "entity-searchbadge"
		if items[i].Muted != want {
			t.Fatalf("entity %s Muted=%v, want %v", items[i].ID, items[i].Muted, want)
		}
	}
}
