package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestSubtitleChoiceRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1000, 0).UTC()

	if _, found, err := st.SubtitleChoice(ctx, "profile-default", "entity:series-1"); err != nil || found {
		t.Fatalf("SubtitleChoice on empty store = found %v, err %v; want not found", found, err)
	}

	rec := store.SubtitleChoiceRecord{
		GroupKey: "entity:series-1", Selected: true,
		SubLanguage: "fr", SubKind: "muxed", SubSDH: true,
		AudioLanguage: "ja",
	}
	if err := st.PutSubtitleChoice(ctx, "profile-default", rec, now); err != nil {
		t.Fatalf("PutSubtitleChoice: %v", err)
	}
	got, found, err := st.SubtitleChoice(ctx, "profile-default", "entity:series-1")
	if err != nil || !found {
		t.Fatalf("SubtitleChoice = found %v, err %v; want found", found, err)
	}
	if got.SubLanguage != "fr" || got.SubKind != "muxed" || !got.SubSDH || !got.Selected {
		t.Fatalf("record = %+v, want the pinned French SDH muxed track", got)
	}
	// The audio changed in the same interaction persists as one paired choice.
	if got.AudioLanguage != "ja" {
		t.Fatalf("AudioLanguage = %q, want ja (paired choice)", got.AudioLanguage)
	}

	// A later pick supersedes the prior one for the whole group, including a
	// switch to a deliberate subtitles-off pin.
	off := store.SubtitleChoiceRecord{GroupKey: "entity:series-1", Selected: false}
	if err := st.PutSubtitleChoice(ctx, "profile-default", off, now.Add(time.Minute)); err != nil {
		t.Fatalf("PutSubtitleChoice(off): %v", err)
	}
	got, found, err = st.SubtitleChoice(ctx, "profile-default", "entity:series-1")
	if err != nil || !found {
		t.Fatalf("SubtitleChoice after off-pin = found %v, err %v", found, err)
	}
	if got.Selected || got.SubLanguage != "" || got.AudioLanguage != "" {
		t.Fatalf("record = %+v, want a cleared subtitles-off pin", got)
	}
}

func TestSubtitleChoiceIsPerProfile(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1000, 0).UTC()
	rec := store.SubtitleChoiceRecord{GroupKey: "entity:series-1", Selected: true, SubLanguage: "fr", SubKind: "muxed"}
	if err := st.PutSubtitleChoice(ctx, "profile-default", rec, now); err != nil {
		t.Fatalf("PutSubtitleChoice: %v", err)
	}
	if _, found, err := st.SubtitleChoice(ctx, "profile-managed", "entity:series-1"); err != nil || found {
		t.Fatalf("another profile sees the pin (found=%v, err=%v); the record is per-profile", found, err)
	}
}

func TestPutSubtitleChoiceRejectsUnknownProfileAndEmptyGroup(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1000, 0).UTC()
	if err := st.PutSubtitleChoice(ctx, "profile-nope", store.SubtitleChoiceRecord{GroupKey: "g", Selected: true}, now); err == nil {
		t.Fatal("PutSubtitleChoice accepted an unknown profile")
	}
	if err := st.PutSubtitleChoice(ctx, "profile-default", store.SubtitleChoiceRecord{Selected: true}, now); err == nil {
		t.Fatal("PutSubtitleChoice accepted an empty group key")
	}
}

// MediaSubtitleGroup keys episodic content on its ordered container so a pick
// propagates series-wide, and standalone content on the item itself (the
// original ADR-0177 per-item scope).
func TestMediaSubtitleGroupSeriesVersusStandalone(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)

	standalone, err := st.MediaSubtitleGroup(ctx, "media-demo-1")
	if err != nil {
		t.Fatalf("MediaSubtitleGroup(standalone): %v", err)
	}
	if standalone != "media:media-demo-1" {
		t.Fatalf("standalone group = %q, want media:media-demo-1", standalone)
	}

	// Group two episodes under one ordered container entity: both must resolve to
	// the same series-scoped key, so a pin on one propagates to the other.
	if err := st.UpsertEntity(ctx, store.Entity{ID: "entity-series", LibraryID: "library-local", Kind: "container", Title: "Series"}); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	episodes := make([]store.MediaID, 0, 2)
	for i, fingerprint := range []string{"sha256:episode-one", "sha256:episode-two"} {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID:          "library-local",
			Path:               "/library/episode" + fingerprint + ".mkv",
			Title:              "Episode",
			Container:          "mkv",
			Codec:              "h264",
			ContentFingerprint: fingerprint,
		})
		if err != nil {
			t.Fatalf("ImportMediaFile: %v", err)
		}
		order := i
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: "entity-series",
			Name:         "children",
			ToMediaID:    media.ID,
			SortOrder:    &order,
		}); err != nil {
			t.Fatalf("UpsertEntityRelation(%s): %v", media.ID, err)
		}
		episodes = append(episodes, media.ID)
	}
	first, err := st.MediaSubtitleGroup(ctx, episodes[0])
	if err != nil {
		t.Fatalf("MediaSubtitleGroup(episode 1): %v", err)
	}
	second, err := st.MediaSubtitleGroup(ctx, episodes[1])
	if err != nil {
		t.Fatalf("MediaSubtitleGroup(episode 2): %v", err)
	}
	if first != "entity:entity-series" || first != second {
		t.Fatalf("episode groups = %q / %q, want both entity:entity-series", first, second)
	}
}
