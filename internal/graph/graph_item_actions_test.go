package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type recordingWatchedObserver struct{ seen []store.MediaID }

func (r *recordingWatchedObserver) Watched(t WatchedTransition) { r.seen = append(r.seen, t.MediaID) }

// itemActionCall posts one item-action field as the default viewer Profile and
// returns its decoded state and any GraphQL errors.
func itemActionCall(t *testing.T, handler http.Handler, authorize func(*http.Request), kind, field string, variables map[string]any) (ItemStateInfo, []graphError) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"query":     kind + " { " + field + " { id kind watched watchlisted rating } }",
		"variables": variables,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d, body %s", field, rec.Code, rec.Body.String())
	}
	var resp struct {
		Data   map[string]ItemStateInfo `json:"data"`
		Errors []graphError             `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v (%s)", field, err, rec.Body.String())
	}
	return resp.Data[field], resp.Errors
}

func engagementInvalidations(t *testing.T, st *store.Store, profile string) int {
	t.Helper()
	events, err := st.ReadEventOutbox(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range events {
		if e.Kind != store.InvalidationKind || e.AggregateID != profile {
			continue
		}
		var payload store.InvalidationPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Reason == store.InvalidationReasonEngagementChanged {
			count++
		}
	}
	return count
}

func TestSetWatchedMarksAndUnmarksForTheCallingProfile(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	observer := &recordingWatchedObserver{}
	handler.SetWatchedObserver(observer)
	target := map[string]any{"mediaID": "media-alpha", "watched": true}

	state, errs := itemActionCall(t, handler, authorizeViewer, "mutation", "setWatched", target)
	if len(errs) != 0 || !state.Watched || state.ID != "media-alpha" || state.Kind != "media" {
		t.Fatalf("setWatched = %+v errors %v", state, errs)
	}
	if len(observer.seen) != 1 || observer.seen[0] != "media-alpha" {
		t.Fatalf("watched observer saw %v, want one media-alpha transition", observer.seen)
	}
	// A repeat is not a second transition.
	if _, errs := itemActionCall(t, handler, authorizeViewer, "mutation", "setWatched", target); len(errs) != 0 {
		t.Fatalf("repeat errors %v", errs)
	}
	if len(observer.seen) != 1 {
		t.Fatalf("repeat mark notified the observer again: %v", observer.seen)
	}
	if other, errs := itemActionCall(t, handler, authorizeManaged, "query", "itemState", map[string]any{"mediaID": "media-alpha"}); len(errs) != 0 || other.Watched {
		t.Fatalf("another Profile's itemState = %+v errors %v", other, errs)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 2 {
		t.Fatalf("engagement invalidations for the caller = %d, want 2", got)
	}
	if got := engagementInvalidations(t, st, "profile-managed"); got != 0 {
		t.Fatalf("another Profile was invalidated %d times", got)
	}

	state, errs = itemActionCall(t, handler, authorizeViewer, "mutation", "setWatched", map[string]any{"mediaID": "media-alpha", "watched": false})
	if len(errs) != 0 || state.Watched {
		t.Fatalf("unmark = %+v errors %v", state, errs)
	}
}

func TestSetWatchedEntityMarksItsMediaWithoutScrobbling(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)
	observer := &recordingWatchedObserver{}
	handler.SetWatchedObserver(observer)
	if err := st.UpsertEntity(ctx, store.Entity{ID: "entity-item", LibraryID: "library-local", Kind: "container", Title: "Container"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: "entity-item", Name: "contains", ToMediaID: "media-alpha"}); err != nil {
		t.Fatal(err)
	}
	state, errs := itemActionCall(t, handler, authorizeViewer, "mutation", "setWatched", map[string]any{"entityID": "entity-item", "watched": true})
	if len(errs) != 0 || !state.Watched || state.Kind != "entity" {
		t.Fatalf("setWatched(entity) = %+v errors %v", state, errs)
	}
	media, errs := itemActionCall(t, handler, authorizeViewer, "query", "itemState", map[string]any{"mediaID": "media-alpha"})
	if len(errs) != 0 || !media.Watched {
		t.Fatalf("Media under the marked Entity = %+v errors %v", media, errs)
	}
	if len(observer.seen) != 0 {
		t.Fatalf("a tree mark reached the watched observer: %v", observer.seen)
	}
}

// An Entity mark walks down only along a parent relation a plugin declares:
// with none declared, a parent covers its own Media alone.
func TestSetWatchedEntityFollowsDeclaredParentRelations(t *testing.T) {
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:           "tree-test",
		Version:      "0.0.1",
		DisplayName:  plugin.LocaleString{"en": "Tree Test"},
		MediaLabel:   plugin.LocaleString{"en": "Item"},
		MediaClasses: []string{"video"},
		EntityKinds: []plugin.EntityKind{{
			Name: "container", Archetype: "container", Hierarchical: true,
			Relations: []plugin.RelationSpec{
				{Name: "contains", ToKind: plugin.MediaKindName},
				{Name: "child-of", ToKind: "container"},
			},
		}},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "all", Kind: "grid", Title: plugin.LocaleString{"en": "All"},
				ArtworkSlot: "thumb", Source: plugin.SourceSpec{Kind: "all-items"}},
		}}},
		Scrapers: []plugin.ScraperSpec{{
			ID: "tree", DisplayName: plugin.LocaleString{"en": "Tree"}, Kind: "http-json",
			Endpoint: "https://tree.example",
			AuthRef:  "tree-key", AuthStyle: "query:api_key",
			SearchTemplate: "/find?q={query}", CandidatesPath: "hits", CandidateIDPath: "id",
			LookupTemplate: "/entries/{id}",
			FieldMap:       map[string]string{"title": "data.name"},
			EntityMaps: []plugin.EntityMap{{Kind: "container", Relation: "contains", Path: "data.label",
				NamePath: "name", ParentNamePath: "parent", ParentRelation: "child-of"}},
		}},
	}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	for name, tc := range map[string]struct {
		handler func(*store.Store) *Handler
		reached bool
	}{
		"declared":   {func(st *store.Store) *Handler { return newTestHandlerWithPlugins(st, registry) }, true},
		"undeclared": {newTestHandlerWithStore, false},
	} {
		t.Run(name, func(t *testing.T) {
			st := storetest.Open(t)
			ctx := context.Background()
			for _, id := range []store.EntityID{"entity-tree-parent", "entity-tree-child"} {
				if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: "library-local", Kind: "container", Title: string(id)}); err != nil {
					t.Fatal(err)
				}
			}
			for _, relation := range []store.EntityRelation{
				{FromEntityID: "entity-tree-child", Name: "child-of", ToEntityID: "entity-tree-parent"},
				{FromEntityID: "entity-tree-child", Name: "contains", ToMediaID: "media-alpha"},
			} {
				if err := st.UpsertEntityRelation(ctx, relation); err != nil {
					t.Fatal(err)
				}
			}
			handler := tc.handler(st)
			if _, errs := itemActionCall(t, handler, authorizeViewer, "mutation", "setWatched", map[string]any{"entityID": "entity-tree-parent", "watched": true}); len(errs) != 0 {
				t.Fatalf("setWatched(parent) errors %v", errs)
			}
			media, errs := itemActionCall(t, handler, authorizeViewer, "query", "itemState", map[string]any{"mediaID": "media-alpha"})
			if len(errs) != 0 || media.Watched != tc.reached {
				t.Fatalf("child Media watched = %v errors %v, want %v", media.Watched, errs, tc.reached)
			}
		})
	}
}

func TestSetWatchlistedAddsAndRemoves(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	state, errs := itemActionCall(t, handler, authorizeViewer, "mutation", "setWatchlisted", map[string]any{"mediaID": "media-alpha", "watchlisted": true})
	if len(errs) != 0 || !state.Watchlisted {
		t.Fatalf("setWatchlisted = %+v errors %v", state, errs)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	items, err := st.SourceItems(context.Background(), filter, store.SourceQuery{Kind: "watchlist", ProfileID: "profile-default"})
	if err != nil || len(items) != 1 || items[0].ID != "media-alpha" {
		t.Fatalf("watchlist Source = %+v err %v", items, err)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("engagement invalidations = %d, want 1", got)
	}
	state, errs = itemActionCall(t, handler, authorizeViewer, "mutation", "setWatchlisted", map[string]any{"mediaID": "media-alpha", "watchlisted": false})
	if len(errs) != 0 || state.Watchlisted {
		t.Fatalf("remove = %+v errors %v", state, errs)
	}
	if _, errs := itemActionCall(t, handler, authorizeViewer, "mutation", "setWatchlisted", map[string]any{"mediaID": "media-alpha"}); len(errs) == 0 {
		t.Fatal("setWatchlisted accepted a call with no watchlisted value")
	}
}

func TestSetRatingSetsClearsAndValidates(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	state, errs := itemActionCall(t, handler, authorizeViewer, "mutation", "setRating", map[string]any{"mediaID": "media-alpha", "rating": 8})
	if len(errs) != 0 || state.Rating != 8 {
		t.Fatalf("setRating = %+v errors %v", state, errs)
	}
	for _, bad := range []any{11, -1, 7.5} {
		if _, errs := itemActionCall(t, handler, authorizeViewer, "mutation", "setRating", map[string]any{"mediaID": "media-alpha", "rating": bad}); len(errs) == 0 {
			t.Fatalf("rating %v accepted", bad)
		}
	}
	if kept, _ := itemActionCall(t, handler, authorizeViewer, "query", "itemState", map[string]any{"mediaID": "media-alpha"}); kept.Rating != 8 {
		t.Fatalf("refused ratings changed the stored one: %+v", kept)
	}
	state, errs = itemActionCall(t, handler, authorizeViewer, "mutation", "setRating", map[string]any{"mediaID": "media-alpha", "rating": 0})
	if len(errs) != 0 || state.Rating != 0 {
		t.Fatalf("clear = %+v errors %v", state, errs)
	}
}

func TestItemActionsRefuseHiddenAndMalformedTargets(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	hidden := map[string]any{"mediaID": "media-archive", "watched": true, "watchlisted": true, "rating": 5}
	for _, call := range [][2]string{{"mutation", "setWatched"}, {"mutation", "setWatchlisted"}, {"mutation", "setRating"}, {"query", "itemState"}} {
		if _, errs := itemActionCall(t, handler, authorizeViewer, call[0], call[1], hidden); len(errs) == 0 {
			t.Fatalf("%s accepted a Media in a hidden Library", call[1])
		}
		if _, errs := itemActionCall(t, handler, authorizeViewer, call[0], call[1], map[string]any{"watched": true, "watchlisted": true, "rating": 5}); len(errs) == 0 {
			t.Fatalf("%s accepted a call with no target", call[1])
		}
	}
	if _, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", "media-archive"); err != nil || ok {
		t.Fatalf("refused mark wrote progress: ok=%v err=%v", ok, err)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 0 {
		t.Fatalf("refused calls emitted %d invalidations", got)
	}
}
