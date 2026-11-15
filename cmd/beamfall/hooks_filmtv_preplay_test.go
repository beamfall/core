//go:build !beamfall_appstore

package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	filmtv "github.com/beamfall/plugin-filmtv"
)

func importPrePlayFixture(t *testing.T, st *store.Store, name, class string, extra bool) store.MediaID {
	t.Helper()
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/library/" + name + ".mkv", Title: name,
		Container: "mkv", Codec: "h264", ContentFingerprint: "sha256:" + name, Extra: extra,
	})
	if err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
	if class != "" {
		if err := st.SetMediaFields(ctx, media.ID, []store.MediaField{{Name: filmtvClassField, Type: store.EntityFieldText, Text: class}}); err != nil {
			t.Fatalf("class %s: %v", name, err)
		}
	}
	return media.ID
}

func TestFilmTVPrePlayPolicyPlansTrailersFromTheStore(t *testing.T) {
	if hook := firstPartyHooks().PrePlayPolicy; hook == nil || hook.PluginID != filmtv.PluginID || hook.New == nil {
		t.Fatalf("Film/TV pre-play hook = %+v", hook)
	}
	st := storetest.Open(t)
	ctx := context.Background()
	feature := importPrePlayFixture(t, st, "feature", "movie", false)
	seen := importPrePlayFixture(t, st, "seen", "movie", false)
	fresh := importPrePlayFixture(t, st, "fresh", "movie", false)
	part := importPrePlayFixture(t, st, "part", "episode", false)
	links := map[store.MediaID]store.MediaID{
		importPrePlayFixture(t, st, "seen-trailer", "", true):  seen,
		importPrePlayFixture(t, st, "fresh-trailer", "", true): fresh,
		importPrePlayFixture(t, st, "part-trailer", "", true):  part,
	}
	for extra, parent := range links {
		if err := st.LinkMediaRelation(ctx, extra, store.MediaRelationExtraOf, parent); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID: "profile-default", MediaID: seen, PositionSeconds: 100, DurationSeconds: 100, Watched: true, UpdatedAt: time.Now().UTC(),
	}, true, store.ResumePolicy{}); err != nil {
		t.Fatal(err)
	}
	policy := firstPartyHooks().PrePlayPolicy.New(st)
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}, PlaybackGate: true}
	request := graph.PrePlayRequest{ProfileID: "profile-default", Filter: filter, FeatureID: feature, Count: 1, Seed: 3}

	plan, err := policy.PlanPrePlay(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Eligible || plan.Label != filmtv.PrePlayLabel || !slices.Equal(plan.MediaIDs, []store.MediaID{findExtra(links, fresh)}) {
		t.Fatalf("count 1 plan = %+v, want the unwatched film's trailer", plan)
	}

	request.Count = 5
	if plan, err = policy.PlanPrePlay(ctx, request); err != nil {
		t.Fatal(err)
	}
	got := slices.Clone(plan.MediaIDs)
	slices.Sort(got)
	want := []store.MediaID{findExtra(links, seen), findExtra(links, fresh)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("count 5 trailers = %v, want %v", got, want)
	}

	request.FeatureID = part
	if plan, err = policy.PlanPrePlay(ctx, request); err != nil || plan.Eligible {
		t.Fatalf("non-film feature plan = %+v, %v; want ineligible", plan, err)
	}

	request.FeatureID, request.Filter = feature, store.LibraryFilter{}
	if plan, err = policy.PlanPrePlay(ctx, request); err != nil || plan.Eligible {
		t.Fatalf("hidden feature plan = %+v, %v; want ineligible", plan, err)
	}
}

func findExtra(links map[store.MediaID]store.MediaID, parent store.MediaID) store.MediaID {
	for extra, p := range links {
		if p == parent {
			return extra
		}
	}
	return ""
}
