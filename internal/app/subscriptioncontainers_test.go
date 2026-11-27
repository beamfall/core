package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

type fakeShowStore struct {
	entities  []store.Entity
	relations []store.EntityRelation
	entries   []store.SubscriptionEntry
}

func (f *fakeShowStore) LibraryPluginID(context.Context, store.LibraryID) (string, error) {
	return "test.feeds", nil
}
func (f *fakeShowStore) UpsertEntity(_ context.Context, e store.Entity) error {
	f.entities = append(f.entities, e)
	return nil
}
func (f *fakeShowStore) UpsertHydratedEntityRelation(_ context.Context, r store.EntityRelation) error {
	f.relations = append(f.relations, r)
	return nil
}
func (f *fakeShowStore) SubscriptionEntries(context.Context, store.SubscriptionID) ([]store.SubscriptionEntry, error) {
	return f.entries, nil
}

type fakeBundles struct{ bundle plugin.Bundle }

func (f fakeBundles) Bundle(plugin.ID) (plugin.Bundle, bool) { return f.bundle, true }

type fakeShowArtwork struct {
	entityPoster map[store.EntityID]bool
	posters      map[store.MediaID]string
	thumbs       map[store.MediaID]bool
}

func (f *fakeShowArtwork) HasEntityPoster(id store.EntityID) bool { return f.entityPoster[id] }
func (f *fakeShowArtwork) ImportEntityPoster(_ context.Context, id store.EntityID, _ []byte) error {
	f.entityPoster[id] = true
	return nil
}
func (f *fakeShowArtwork) HasPoster(id store.MediaID) bool { _, ok := f.posters[id]; return ok }
func (f *fakeShowArtwork) ImportPoster(_ context.Context, id store.MediaID, data []byte) error {
	f.posters[id] = string(data)
	return nil
}
func (f *fakeShowArtwork) HasArtwork(id store.MediaID, _ string) bool { return f.thumbs[id] }
func (f *fakeShowArtwork) ImportArtwork(_ context.Context, id store.MediaID, _ string, _ []byte) error {
	f.thumbs[id] = true
	return nil
}

type fakeShowFetcher map[string]string

func (f fakeShowFetcher) FetchResource(_ context.Context, u string) ([]byte, error) {
	return []byte(f[u]), nil
}

func feedBundle() plugin.Bundle {
	return plugin.Bundle{EntityKinds: []plugin.EntityKind{{
		Name: "container", Archetype: "container",
		Fields:    []plugin.FieldSpec{{Name: "summary", Type: "text"}, {Name: "website", Type: "url"}},
		Relations: []plugin.RelationSpec{{Name: "item", ToKind: plugin.MediaKindName, Cardinality: "one"}},
	}}}
}

func TestPublishContainerCreatesContainerWithInheritedArtwork(t *testing.T) {
	st := &fakeShowStore{entries: []store.SubscriptionEntry{
		{MediaID: "m-own", GUID: "own", PublishedAt: time.Unix(200, 0)},
		{MediaID: "m-plain", GUID: "plain", PublishedAt: time.Unix(100, 0)},
	}}
	art := &fakeShowArtwork{entityPoster: map[store.EntityID]bool{}, posters: map[store.MediaID]string{}, thumbs: map[store.MediaID]bool{}}
	fetcher := fakeShowFetcher{"https://x/container.jpg": "SHOW", "https://x/own.jpg": "OWN"}
	p := newSubscriptionContainers(st, fakeBundles{feedBundle()}, art, fetcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	doc := scrape.FeedDocument{
		Title: "Tower", Description: "About", Link: "https://x", ArtworkURL: "https://x/container.jpg",
		Entries: []scrape.FeedEntry{{GUID: "own", ArtworkURL: "https://x/own.jpg"}, {GUID: "plain"}},
	}
	sub := store.Subscription{ID: "sub-1", LibraryID: "lib-1"}
	for range 2 { // idempotent across polls
		if err := p.PublishContainer(context.Background(), sub, doc, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(st.entities) != 2 || st.entities[0].ID != st.entities[1].ID || st.entities[0].Title != "Tower" || st.entities[0].Kind != "container" {
		t.Fatalf("entities = %+v", st.entities)
	}
	if len(st.entities[0].Fields) != 2 {
		t.Fatalf("fields = %+v", st.entities[0].Fields)
	}
	if len(st.relations) != 4 || st.relations[0].Name != "item" || st.relations[0].Cardinality != "one" || st.relations[0].ToMediaID != "m-own" {
		t.Fatalf("relations = %+v", st.relations)
	}
	if !art.entityPoster[st.entities[0].ID] {
		t.Fatal("container poster missing")
	}
	if art.posters["m-own"] != "OWN" || !art.thumbs["m-own"] || art.posters["m-plain"] != "SHOW" || art.thumbs["m-plain"] {
		t.Fatalf("posters = %v thumbs = %v", art.posters, art.thumbs)
	}
}

func TestPublishContainerSkipsPluginWithoutHolderKind(t *testing.T) {
	st := &fakeShowStore{}
	p := newSubscriptionContainers(st, fakeBundles{plugin.Bundle{}}, nil, nil, slog.Default())
	if err := p.PublishContainer(context.Background(), store.Subscription{ID: "s", LibraryID: "l"}, scrape.FeedDocument{Title: "T"}, nil); err != nil || len(st.entities) != 0 {
		t.Fatalf("err=%v entities=%v", err, st.entities)
	}
}

type failingShowFetcher map[string]bool

func (f failingShowFetcher) FetchResource(_ context.Context, u string) ([]byte, error) {
	if f[u] {
		return nil, errors.New("fetch failed")
	}
	return []byte("SHOW"), nil
}

func TestPublishContainerRetriesItemArtworkInsteadOfKeepingFallback(t *testing.T) {
	st := &fakeShowStore{entries: []store.SubscriptionEntry{{MediaID: "m-own", GUID: "own", PublishedAt: time.Unix(200, 0)}}}
	art := &fakeShowArtwork{entityPoster: map[store.EntityID]bool{}, posters: map[store.MediaID]string{}, thumbs: map[store.MediaID]bool{}}
	fetcher := failingShowFetcher{"https://x/own.jpg": true}
	p := newSubscriptionContainers(st, fakeBundles{feedBundle()}, art, fetcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	doc := scrape.FeedDocument{Title: "T", ArtworkURL: "https://x/container.jpg", Entries: []scrape.FeedEntry{{GUID: "own", ArtworkURL: "https://x/own.jpg"}}}
	sub := store.Subscription{ID: "sub-1", LibraryID: "lib-1"}
	if err := p.PublishContainer(context.Background(), sub, doc, nil); !errors.Is(err, errArtworkIncomplete) {
		t.Fatalf("err = %v, want errArtworkIncomplete", err)
	}
	if _, ok := art.posters["m-own"]; ok {
		t.Fatalf("item kept the show art as a permanent fallback: %v", art.posters)
	}
	fetcher["https://x/own.jpg"] = false
	if err := p.PublishContainer(context.Background(), sub, doc, nil); err != nil {
		t.Fatal(err)
	}
	if art.posters["m-own"] == "" {
		t.Fatal("item poster not retried")
	}
}
