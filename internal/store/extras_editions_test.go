package store

import (
	"context"
	"slices"
	"testing"
)

// importForTest imports one Media file (optionally as an Extra) into the seeded
// local Library and returns its Media ID.
func importForTest(t *testing.T, st *Store, fingerprint, title string, extra bool) MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/library/" + fingerprint + ".mkv",
		Title:              title,
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:" + fingerprint,
		Extra:              extra,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(%s) error: %v", title, err)
	}
	return media.ID
}

func containsMediaID(items []SourceItem, id MediaID) bool {
	return slices.ContainsFunc(items, func(item SourceItem) bool { return item.ID == id })
}

// TestExtraExcludedFromBrowseButFetchable proves the load-bearing ADR-0009 §3
// contract: an Extra is suppressed from the default browse Sources, yet stays
// directly addressable both as a Media item and through the "extras of parent"
// relation read.
func TestExtraExcludedFromBrowseButFetchable(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	parent := importForTest(t, st, "feature01", "Feature One", false)
	extra := importForTest(t, st, "feature01-trailer", "Feature One Trailer", true)

	// Link the Extra under its parent (the scan-time/identification grouping).
	if err := st.LinkMediaRelation(ctx, extra, MediaRelationExtraOf, parent); err != nil {
		t.Fatalf("LinkMediaRelation: %v", err)
	}

	// The default browse Source ("") and the all-items Source both exclude the
	// Extra while still listing the parent.
	for _, kind := range []string{"", "all-items"} {
		items := mustSourceItems(t, st, filter, SourceQuery{Kind: kind, LibraryID: "library-local"})
		if !containsMediaID(items, parent) {
			t.Fatalf("browse Source %q omitted the parent media %q: %#v", kind, parent, items)
		}
		if containsMediaID(items, extra) {
			t.Fatalf("browse Source %q leaked the Extra %q: %#v", kind, extra, items)
		}
	}

	// The Extra is still directly addressable as a Media item.
	if _, err := st.Media(ctx, extra, filter); err != nil {
		t.Fatalf("Extra not directly fetchable as Media: %v", err)
	}

	// And it is fetchable through the explicit "extras of X" relation read.
	extras, err := st.MediaRelationsOf(ctx, parent, MediaRelationExtraOf, filter)
	if err != nil {
		t.Fatalf("MediaRelationsOf(extra-of): %v", err)
	}
	if len(extras) != 1 || extras[0].ID != extra {
		t.Fatalf("extras of parent = %#v, want only %q", extras, extra)
	}
}

// TestExtraFlagIsStickyAcrossRescan proves a re-import that no longer carries
// the Extra bit (e.g. a renamed parent folder) does not silently un-exclude an
// already-flagged Extra.
func TestExtraFlagIsStickyAcrossRescan(t *testing.T) {
	st := openSeeded(t)
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	extra := importForTest(t, st, "clip01", "Clip One", true)
	// Re-import the same content fingerprint with Extra=false (the convention
	// no longer matched). The bit must remain set.
	importForTest(t, st, "clip01", "Clip One", false)

	items := mustSourceItems(t, st, filter, SourceQuery{Kind: "", LibraryID: "library-local"})
	if containsMediaID(items, extra) {
		t.Fatalf("Extra bit was cleared by a re-scan: %q leaked into browse", extra)
	}
}

// TestEditionOfGroupsCutsWithIndependentEngagement proves two cuts linked by an
// edition-of relation group on the detail read, while each keeps its own
// per-edition watch state (their timelines differ — ADR-0009 §2).
func TestEditionOfGroupsCutsWithIndependentEngagement(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	theatrical := importForTest(t, st, "cut-a", "The Work", false)
	directors := importForTest(t, st, "cut-b", "The Work", false)

	// Group the two cuts: each is an edition-of the other (the detail screen
	// walks the relation from whichever cut the viewer opened).
	if err := st.LinkMediaRelation(ctx, theatrical, MediaRelationEditionOf, directors); err != nil {
		t.Fatalf("LinkMediaRelation a->b: %v", err)
	}
	if err := st.LinkMediaRelation(ctx, directors, MediaRelationEditionOf, theatrical); err != nil {
		t.Fatalf("LinkMediaRelation b->a: %v", err)
	}

	// Opening the director's cut surfaces the theatrical cut as its sibling.
	siblings, err := st.MediaRelationsOf(ctx, directors, MediaRelationEditionOf, filter)
	if err != nil {
		t.Fatalf("MediaRelationsOf(edition-of): %v", err)
	}
	if len(siblings) != 1 || siblings[0].ID != theatrical {
		t.Fatalf("siblings of directors cut = %#v, want only %q", siblings, theatrical)
	}

	// Watch the theatrical cut to completion; leave the director's cut partway.
	video := ResumePolicy{Class: MediaClassVideo}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: theatrical, PositionSeconds: 100, DurationSeconds: 100,
	}, true, video); err != nil {
		t.Fatalf("progress theatrical: %v", err)
	}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: directors, PositionSeconds: 30, DurationSeconds: 120,
	}, false, video); err != nil {
		t.Fatalf("progress directors: %v", err)
	}

	// Engagement is keyed per Media, so the two cuts carry independent state.
	tProg, tOK, err := st.PlaybackProgressFor(ctx, "profile-default", theatrical)
	if err != nil {
		t.Fatalf("progress for theatrical: %v", err)
	}
	if !tOK || !tProg.Watched {
		t.Fatalf("theatrical cut should be watched, got %#v (ok=%v)", tProg, tOK)
	}
	dProg, dOK, err := st.PlaybackProgressFor(ctx, "profile-default", directors)
	if err != nil {
		t.Fatalf("progress for directors: %v", err)
	}
	if !dOK || dProg.Watched {
		t.Fatalf("directors cut should be unwatched, got %#v (ok=%v)", dProg, dOK)
	}
	if dProg.PositionSeconds != 30 {
		t.Fatalf("directors cut resume = %ds, want 30s (independent timeline)", dProg.PositionSeconds)
	}
}

// importExtraForTest imports one Media file with an explicit class and oshash,
// optionally as an Extra, into the seeded local Library. It complements
// importForTest for the surfaces (dedupe/stats) that need a shared oshash and a
// resumable class.
func importExtraForTest(t *testing.T, st *Store, fingerprint, title, oshash string, class MediaClass, extra bool) MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/library/" + fingerprint + ".mkv",
		Title:               title,
		Container:           "mkv",
		Codec:               "h264",
		Class:               class,
		ContentFingerprint:  "sha256:" + fingerprint,
		DurationSecondsHint: 100,
		OSHash:              oshash,
		Extra:               extra,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(%s) error: %v", title, err)
	}
	return media.ID
}

// TestExtraExcludedFromEngagementShelves proves the FREEZE-2 residual: an Extra
// with a resume point and play count is excluded BY DEFAULT from the engagement
// shelves — continue-watching and recently/most-played — while a
// normal feature with the same engagement state stays present.
func TestExtraExcludedFromEngagementShelves(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}

	video := ResumePolicy{Class: MediaClassVideo}

	// Continue-watching pair: a mid-playback (in-progress, unwatched) resume
	// point. Both would qualify if Extras were not excluded.
	cwFeature := importExtraForTest(t, st, "feat-cw", "Feature CW", "", MediaClassVideo, false)
	cwExtra := importExtraForTest(t, st, "feat-cw-trailer", "Feature CW Trailer", "", MediaClassVideo, true)
	for _, id := range []MediaID{cwFeature, cwExtra} {
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
			ProfileID: "profile-default", MediaID: id, PositionSeconds: 30, DurationSeconds: 100,
		}, false, video); err != nil {
			t.Fatalf("continue-watching progress %q: %v", id, err)
		}
	}

	// Played pair: a finished play (bumps play_count) so both qualify for
	// recently/most-played.
	playFeature := importExtraForTest(t, st, "feat-play", "Feature Play", "", MediaClassVideo, false)
	playExtra := importExtraForTest(t, st, "feat-play-trailer", "Feature Play Trailer", "", MediaClassVideo, true)
	for _, id := range []MediaID{playFeature, playExtra} {
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
			ProfileID: "profile-default", MediaID: id, PositionSeconds: 100, DurationSeconds: 100,
		}, true, video); err != nil {
			t.Fatalf("played progress %q: %v", id, err)
		}
	}

	cases := []struct {
		kind            string
		wantFeature     MediaID
		wantAbsentExtra MediaID
	}{
		{"continue-watching", cwFeature, cwExtra},
		{"recently-played", playFeature, playExtra},
		{"most-played", playFeature, playExtra},
	}
	for _, tc := range cases {
		items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: tc.kind, ProfileID: "profile-default"})
		if err != nil {
			t.Fatalf("SourceItems(%s): %v", tc.kind, err)
		}
		if !containsMediaID(items, tc.wantFeature) {
			t.Fatalf("%s omitted the normal feature %q: %#v", tc.kind, tc.wantFeature, items)
		}
		if containsMediaID(items, tc.wantAbsentExtra) {
			t.Fatalf("%s leaked the Extra %q: %#v", tc.kind, tc.wantAbsentExtra, items)
		}
	}
}

// TestExtraExcludedFromNextUp proves an Extra child of an ordered container is
// never picked as the next-up frontier: with the first unwatched
// child being an Extra and the second a normal episode, next-up surfaces the
// normal episode, skipping the Extra.
func TestExtraExcludedFromNextUp(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}

	// An ordered container (e.g. a season) with three ordered children: a
	// watched opener, an Extra (trailer) at the frontier, and a normal episode
	// after it. Engagement in the opener engages the container.
	container := EntityID("container-nextup")
	if err := st.UpsertEntity(ctx, widgetEntity(container, "library-local")); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	opener := importExtraForTest(t, st, "nu-ep1", "Ep One", "", MediaClassVideo, false)
	extra := importExtraForTest(t, st, "nu-extra", "Trailer", "", MediaClassVideo, true)
	episode := importExtraForTest(t, st, "nu-ep2", "Ep Two", "", MediaClassVideo, false)
	for i, id := range []MediaID{opener, extra, episode} {
		order := i + 1
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: container, Name: "linked", ToMediaID: id, SortOrder: &order,
		}); err != nil {
			t.Fatalf("UpsertEntityRelation %q: %v", id, err)
		}
	}
	// Finish the opener so the frontier advances past it.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: opener, PositionSeconds: 100, DurationSeconds: 100,
	}, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatalf("progress opener: %v", err)
	}

	items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "next-up", ProfileID: "profile-default"})
	if err != nil {
		t.Fatalf("SourceItems(next-up): %v", err)
	}
	if containsMediaID(items, extra) {
		t.Fatalf("next-up leaked the Extra %q: %#v", extra, items)
	}
	if !containsMediaID(items, episode) {
		t.Fatalf("next-up omitted the normal episode %q (Extra should be skipped, not the whole container): %#v", episode, items)
	}
}

// TestExtraExcludedFromDedupeAndStats proves an Extra sharing a normal Media's
// oshash is neither a dedupe candidate nor counted in the duplicate stat.
// A byte-identical trailer must never make a real feature look
// like a duplicate, nor appear in HealthDuplicateGroups.
func TestExtraExcludedFromDedupeAndStats(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}

	const sharedHash = "deadbeefdeadbeef"
	feature := importExtraForTest(t, st, "dup-feature", "Dup Feature", sharedHash, MediaClassVideo, false)
	extra := importExtraForTest(t, st, "dup-extra", "Dup Extra", sharedHash, MediaClassVideo, true)

	// Dedupe: the only other holder of feature's oshash is an Extra, so the
	// feature must NOT be a duplicate candidate and HealthDuplicateGroups must
	// be empty (a real duplicate would need a second non-Extra holder).
	groups, err := st.HealthDuplicateGroups(ctx, "library-local", all)
	if err != nil {
		t.Fatalf("HealthDuplicateGroups: %v", err)
	}
	for _, g := range groups {
		for _, item := range g {
			if item.MediaID == feature || item.MediaID == extra {
				t.Fatalf("dedupe group leaked Extra/feature pair: %#v", g)
			}
		}
	}

	// Stats: the duplicate dimension must count neither the feature (its only
	// hash-partner is an Extra) nor the Extra itself.
	counts, err := st.LibraryHealthCounts(ctx, all)
	if err != nil {
		t.Fatalf("LibraryHealthCounts: %v", err)
	}
	var dupCount int
	for _, lh := range counts {
		if lh.LibraryID == "library-local" {
			dupCount = lh.Counts[HealthDuplicate]
		}
	}
	if dupCount != 0 {
		t.Fatalf("duplicate stat = %d, want 0 (Extra partner must not count): groups=%#v", dupCount, groups)
	}

	// Control: add a second NON-Extra holder of the same hash — now the two
	// features ARE a real duplicate pair and DO count, proving the exclusion is
	// the Extra bit and not a blanket suppression of the hash.
	feature2 := importExtraForTest(t, st, "dup-feature2", "Dup Feature Two", sharedHash, MediaClassVideo, false)
	counts, err = st.LibraryHealthCounts(ctx, all)
	if err != nil {
		t.Fatalf("LibraryHealthCounts (control): %v", err)
	}
	for _, lh := range counts {
		if lh.LibraryID == "library-local" {
			dupCount = lh.Counts[HealthDuplicate]
		}
	}
	if dupCount != 2 {
		t.Fatalf("duplicate stat after second feature = %d, want 2 (feature+feature2)", dupCount)
	}
	groups, err = st.HealthDuplicateGroups(ctx, "library-local", all)
	if err != nil {
		t.Fatalf("HealthDuplicateGroups (control): %v", err)
	}
	if len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("duplicate groups = %#v, want one group of two (feature,feature2)", groups)
	}
	if containsHealthItem(groups[0], extra) {
		t.Fatalf("dedupe group leaked the Extra %q even with a real duplicate present: %#v", extra, groups[0])
	}
	if !containsHealthItem(groups[0], feature) || !containsHealthItem(groups[0], feature2) {
		t.Fatalf("dedupe group %#v missing one of the real features %q/%q", groups[0], feature, feature2)
	}
}

func containsHealthItem(items []HealthItem, id MediaID) bool {
	for _, item := range items {
		if item.MediaID == id {
			return true
		}
	}
	return false
}

// TestLinkMediaRelationRejectsUnknownShapeAndSelfLink guards the fixed-vocabulary
// and no-self-link invariants on the Media->Media relation.
func TestLinkMediaRelationRejectsUnknownShapeAndSelfLink(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	a := importForTest(t, st, "rel-a", "A", false)
	b := importForTest(t, st, "rel-b", "B", false)

	if err := st.LinkMediaRelation(ctx, a, MediaRelationName("director-cut"), b); err == nil {
		t.Fatal("expected unknown media relation shape to be rejected")
	}
	if err := st.LinkMediaRelation(ctx, a, MediaRelationEditionOf, a); err == nil {
		t.Fatal("expected a self-link to be rejected")
	}
}
