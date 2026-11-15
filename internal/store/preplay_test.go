package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestPrePlayClipsRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	clips, err := st.PrePlayClips(ctx)
	if err != nil || len(clips) != 0 {
		t.Fatalf("unset clips = %v, %v; want empty", clips, err)
	}
	a := importForTest(t, st, "clip-a", "Clip A", false)
	b := importForTest(t, st, "clip-b", "Clip B", false)
	saved, err := st.SetPrePlayClips(ctx, []MediaID{" " + b, a, b, ""}, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := []MediaID{b, a}; !slices.Equal(saved, want) {
		t.Fatalf("saved = %v, want %v", saved, want)
	}
	clips, err = st.PrePlayClips(ctx)
	if err != nil || !slices.Equal(clips, saved) {
		t.Fatalf("clips = %v, %v; want %v", clips, err, saved)
	}
	if _, err := st.SetPrePlayClips(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
	if clips, _ = st.PrePlayClips(ctx); len(clips) != 0 {
		t.Fatalf("cleared clips = %v, want empty", clips)
	}
}

func TestSetPrePlayClipsRejectsUnknownAndTooMany(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	if _, err := st.SetPrePlayClips(ctx, []MediaID{"media-missing"}, now); !isValidation(err) {
		t.Fatalf("unknown clip err = %v, want validation error", err)
	}
	many := make([]MediaID, MaxPrePlayClips+1)
	for i := range many {
		many[i] = MediaID(string(rune('a'+i)) + "-clip")
	}
	if _, err := st.SetPrePlayClips(ctx, many, now); !isValidation(err) {
		t.Fatalf("too many clips err = %v, want validation error", err)
	}
}

func TestPrePlayClipsIgnoresCorruptValue(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.setServerConfigValues(ctx, map[string]string{prePlayClipsKey: "{not json"}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	clips, err := st.PrePlayClips(ctx)
	if err != nil || len(clips) != 0 {
		t.Fatalf("corrupt clips = %v, %v; want empty", clips, err)
	}
}

func TestMediaExtraLinksListsLibraryExtras(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	feature := importForTest(t, st, "feature", "Feature", false)
	other := importForTest(t, st, "other", "Other", false)
	gone := importForTest(t, st, "gone", "Gone", false)
	featureTrailer := importForTest(t, st, "feature-trailer", "Feature Trailer", true)
	otherTrailer := importForTest(t, st, "other-trailer", "Other Trailer", true)
	goneTrailer := importForTest(t, st, "gone-trailer", "Gone Trailer", true)
	for extra, parent := range map[MediaID]MediaID{featureTrailer: feature, otherTrailer: other, goneTrailer: gone} {
		if err := st.LinkMediaRelation(ctx, extra, MediaRelationExtraOf, parent); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.TrashMedia(ctx, gone, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}); err != nil {
		t.Fatal(err)
	}

	links, err := st.MediaExtraLinks(ctx, feature, filter)
	if err != nil {
		t.Fatal(err)
	}
	got := map[MediaID]MediaExtraLink{}
	for _, link := range links {
		got[link.ExtraID] = link
	}
	if len(got) != 2 {
		t.Fatalf("links = %#v, want the feature's and other's trailers", links)
	}
	if link := got[otherTrailer]; link.ParentID != other || link.Path != "/library/other-trailer.mkv" {
		t.Fatalf("other trailer link = %#v", link)
	}
	if _, ok := got[goneTrailer]; ok {
		t.Fatal("trailer of a trashed parent leaked")
	}

	if _, err := st.MediaExtraLinks(ctx, feature, LibraryFilter{}); err == nil {
		t.Fatal("hidden feature: want error")
	}
	if _, err := st.MediaExtraLinks(ctx, "", filter); err == nil {
		t.Fatal("empty feature: want error")
	}
}

func isValidation(err error) bool {
	var validation ValidationError
	return errors.As(err, &validation)
}
