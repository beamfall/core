package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	filmtv "github.com/beamfall/plugin-filmtv"
)

func mustFilmTVPackResolver(t *testing.T, st filmtvPackStore, kind string) filmtvPackResolver {
	t.Helper()
	inner, ok := filmtv.PackResolvers()[kind]
	if !ok {
		t.Fatalf("filmtv pack kind %q is not registered", kind)
	}
	return filmtvPackResolver{store: st, inner: inner}
}

// seedFilmTVSeries stores a series with one numbered season holding
// media-alpha and an empty specials season, using the kinds the plugin bundle
// declares.
func seedFilmTVSeries(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()
	kinds := newFilmTVHydrationMapper()
	if kinds.groupKind == "" || kinds.parentKind == "" {
		t.Fatal("filmtv bundle declares no season-of relation")
	}
	entities := []store.Entity{
		{ID: "series-1", LibraryID: "library-local", Kind: kinds.parentKind, Title: "Series One"},
		{ID: "season-1", LibraryID: "library-local", Kind: kinds.groupKind, Title: "Season One", Fields: []store.EntityField{
			{Name: "number", Type: store.EntityFieldNumber, Number: 1},
		}},
		{ID: "season-sp", LibraryID: "library-local", Kind: kinds.groupKind, Title: "Specials", Fields: []store.EntityField{
			{Name: "specials", Type: store.EntityFieldFlag, Flag: true},
		}},
	}
	for _, entity := range entities {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("UpsertEntity %s: %v", entity.ID, err)
		}
	}
	relations := []store.EntityRelation{
		{FromEntityID: "season-1", Name: filmtv.SeasonOfRelation, ToEntityID: "series-1"},
		{FromEntityID: "season-sp", Name: filmtv.SeasonOfRelation, ToEntityID: "series-1"},
		{FromEntityID: "season-1", Name: filmtv.SeasonEpisodeRelation, ToMediaID: "media-alpha"},
	}
	for _, relation := range relations {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatalf("UpsertEntityRelation %s: %v", relation.FromEntityID, err)
		}
	}
}

func TestFilmTVPackResolversReadSeasonsAndMembersFromTheStore(t *testing.T) {
	st := storetest.Open(t)
	seedFilmTVSeries(t, st)
	filter := revealedPackFilter()
	for kind, ref := range map[string]string{filmtv.PackKindSeason: "season-1", filmtv.PackKindSeries: "series-1"} {
		got, err := mustFilmTVPackResolver(t, st, kind).Resolve(t.Context(), ref, filter)
		if err != nil {
			t.Fatalf("%s Resolve: %v", kind, err)
		}
		if !reflect.DeepEqual(got.MediaIDs, []store.MediaID{"media-alpha"}) {
			t.Fatalf("%s MediaIDs = %v, want [media-alpha]", kind, got.MediaIDs)
		}
	}
	hidden := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-archive"}, Revealed: true}
	if _, err := mustFilmTVPackResolver(t, st, filmtv.PackKindSeries).Resolve(t.Context(), "series-1", hidden); err == nil {
		t.Fatal("series in a hidden Library: want an error")
	}
}

func TestFilmTVNextUnwatchedPackFollowsTheProfileWatchState(t *testing.T) {
	st := storetest.Open(t)
	seedFilmTVSeries(t, st)
	resolver := mustFilmTVPackResolver(t, st, filmtv.PackKindNextUnwatched)
	filter := revealedPackFilter()
	got, err := resolver.ResolveForProfile(t.Context(), "series-1:3", "profile-default", filter)
	if err != nil {
		t.Fatalf("ResolveForProfile: %v", err)
	}
	if !reflect.DeepEqual(got.MediaIDs, []store.MediaID{"media-alpha"}) {
		t.Fatalf("unwatched MediaIDs = %v, want [media-alpha]", got.MediaIDs)
	}
	progress := store.PlaybackProgress{ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 60, DurationSeconds: 60}
	if err := st.UpsertPlaybackProgress(t.Context(), progress, true, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatalf("UpsertPlaybackProgress: %v", err)
	}
	got, err = resolver.ResolveForProfile(t.Context(), "series-1:3", "profile-default", filter)
	if err != nil {
		t.Fatalf("ResolveForProfile after watching: %v", err)
	}
	if len(got.MediaIDs) != 0 {
		t.Fatalf("MediaIDs after watching = %v, want none", got.MediaIDs)
	}
	if _, err := resolver.Resolve(t.Context(), "series-1:3", filter); err == nil {
		t.Fatal("Resolve without a Profile: want an error")
	}
}

// pagedPackStore serves a fixed list of related Entity IDs in pages.
type pagedPackStore struct {
	related   []store.EntityID
	offsets   []int
	entityErr error
}

func (p *pagedPackStore) MediaForEntity(context.Context, store.EntityID, store.LibraryFilter) ([]store.SourceItem, error) {
	return nil, nil
}

func (p *pagedPackStore) Entity(_ context.Context, id store.EntityID, _ store.LibraryFilter) (store.Entity, error) {
	if p.entityErr != nil && id != "parent" {
		return store.Entity{}, p.entityErr
	}
	return store.Entity{ID: id, Fields: []store.EntityField{
		{Name: "number", Type: store.EntityFieldNumber, Number: 2},
		{Name: "specials", Type: store.EntityFieldFlag, Flag: true},
		{Name: "title", Type: store.EntityFieldText, Text: "ignored"},
	}}, nil
}

func (p *pagedPackStore) EntitiesRelatingTo(_ context.Context, _ store.EntityID, _, _ string, offset, limit int) ([]store.EntityID, error) {
	p.offsets = append(p.offsets, offset)
	end := min(offset+limit, len(p.related))
	if offset >= end {
		return nil, nil
	}
	return p.related[offset:end], nil
}

func (p *pagedPackStore) PlaybackProgressFor(context.Context, store.ProfileID, store.MediaID) (store.PlaybackProgress, bool, error) {
	return store.PlaybackProgress{}, false, nil
}

func TestFilmTVPackReaderPagesRelatedEntitiesAndMapsFields(t *testing.T) {
	fake := &pagedPackStore{}
	for i := range filmtvPackPage + 1 {
		fake.related = append(fake.related, store.EntityID(fmt.Sprintf("e-%03d", i)))
	}
	reader := filmtvPackReader{store: fake}
	got, err := reader.EntitiesOf(t.Context(), "parent", "k", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != filmtvPackPage+1 || !reflect.DeepEqual(fake.offsets, []int{0, filmtvPackPage}) {
		t.Fatalf("read %d entities at offsets %v, want %d at [0 %d]", len(got), fake.offsets, filmtvPackPage+1, filmtvPackPage)
	}
	if got[0].Numbers["number"] != 2 || !got[0].Flags["specials"] || len(got[0].Numbers) != 1 || len(got[0].Flags) != 1 {
		t.Fatalf("entity fields = %+v, want one number and one flag", got[0])
	}
	boom := errors.New("read failed")
	fake.entityErr, fake.offsets = boom, nil
	if _, err := reader.EntitiesOf(t.Context(), "parent", "k", "r"); !errors.Is(err, boom) {
		t.Fatalf("entity read err = %v, want %v", err, boom)
	}
}
