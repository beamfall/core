package graph

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// relatedFixture is a Library of archetyped credits (person and category
// kinds credited to Media) for the relatedMedia read (CORE-2444).
type relatedFixture struct {
	t       *testing.T
	st      *store.Store
	handler *Handler
	library store.Library
	media   map[string]store.MediaID
}

func newRelatedFixture(t *testing.T, restricted bool) *relatedFixture {
	t.Helper()
	st := storetest.Open(t)
	credited := func(name, archetype string) plugin.EntityKind {
		return plugin.EntityKind{
			Name:        name,
			DisplayName: plugin.L(name),
			Archetype:   archetype,
			Relations:   []plugin.RelationSpec{{Name: "credited-in", ToKind: plugin.MediaKindName}},
		}
	}
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "reels",
		Version:     "1.0.0",
		DisplayName: plugin.L("Reels"),
		MediaLabel:  plugin.L("Reel"),
		EntityKinds: []plugin.EntityKind{credited("person", "person"), credited("genre", "category")},
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	library, err := st.CreateLibrary(context.Background(), "Reels Library", restricted, "reels", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	return &relatedFixture{t: t, st: st, handler: handler, library: library, media: map[string]store.MediaID{}}
}

func (f *relatedFixture) addMedia(names ...string) {
	f.t.Helper()
	for _, name := range names {
		media, _, err := f.st.ImportMediaFile(context.Background(), store.ImportMediaFile{
			LibraryID:          f.library.ID,
			Path:               "/srv/reels/" + name + ".mkv",
			Title:              name,
			Container:          "mkv",
			Codec:              "h264",
			ContentFingerprint: "fp-" + name,
			Class:              store.MediaClassVideo,
		})
		if err != nil {
			f.t.Fatal(err)
		}
		f.media[name] = media.ID
	}
}

// credit adds the Entity (once) and credits it to each named Media, in the
// given billing order position.
func (f *relatedFixture) credit(entityID, kind string, order int, media ...string) {
	f.t.Helper()
	ctx := context.Background()
	if err := f.st.UpsertEntity(ctx, store.Entity{ID: store.EntityID(entityID), LibraryID: f.library.ID, Kind: kind, Title: entityID}); err != nil {
		f.t.Fatal(err)
	}
	for _, name := range media {
		n := order
		if err := f.st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: store.EntityID(entityID), Name: "credited-in", ToMediaID: f.media[name], SortOrder: &n,
		}); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *relatedFixture) related(name string, limit int) []string {
	f.t.Helper()
	vars := map[string]any{"mediaID": string(f.media[name])}
	if limit > 0 {
		vars["limit"] = limit
	}
	items := graphData[[]SourceItem](f.t, f.handler,
		`query($mediaID:String!, $limit:Int) { relatedMedia(mediaID:$mediaID, limit:$limit) { id title } }`, vars, "relatedMedia")
	titles := make([]string, len(items))
	for i, item := range items {
		titles[i] = item.Title
	}
	return titles
}

func TestRelatedMediaRanksBySharedCredits(t *testing.T) {
	f := newRelatedFixture(t, false)
	f.addMedia("self", "both-genres", "one-genre", "genre-and-person", "person-only", "unrelated")
	f.credit("genre-a", "genre", 0, "self", "both-genres", "one-genre", "genre-and-person")
	f.credit("genre-b", "genre", 1, "self", "both-genres")
	f.credit("lead", "person", 2, "self", "genre-and-person", "person-only")
	f.credit("other", "genre", 0, "unrelated")

	got := f.related("self", 0)
	// person-only shares a person but no category, so it is left out; the
	// item itself never appears.
	want := []string{"both-genres", "genre-and-person", "one-genre"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relatedMedia = %v, want %v", got, want)
	}
}

// Review finding P3-6: people come from the deduplicated credits in server
// order, so a second-billed person in another department still counts.
func TestRelatedMediaCountsSecondBilledPerson(t *testing.T) {
	f := newRelatedFixture(t, false)
	f.addMedia("self", "with-b", "plain")
	f.credit("genre-a", "genre", 0, "self", "with-b", "plain")
	f.credit("person-a", "person", 1, "self")
	f.credit("person-b", "person", 2, "self", "with-b")
	f.credit("person-c", "person", 3, "self")
	f.credit("person-d", "person", 4, "self")

	got := f.related("self", 0)
	if want := []string{"with-b", "plain"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("relatedMedia = %v, want %v (second-billed person counted)", got, want)
	}
}

func TestRelatedMediaWithoutCategoryCreditsIsEmpty(t *testing.T) {
	f := newRelatedFixture(t, false)
	f.addMedia("self", "shares-person")
	f.credit("lead", "person", 0, "self", "shares-person")
	if got := f.related("self", 0); len(got) != 0 {
		t.Fatalf("relatedMedia = %v, want empty without category credits", got)
	}
}

// Review finding P2-1: a Media the Profile muted ("Not interested", through
// an Entity it is credited to) stays out of the discovery row.
func TestRelatedMediaLeavesOutMutedMedia(t *testing.T) {
	f := newRelatedFixture(t, false)
	f.addMedia("self", "kept", "muted")
	f.credit("genre-a", "genre", 0, "self", "kept", "muted")
	f.credit("disliked", "person", 1, "muted")
	if err := f.st.SetMute(context.Background(), "profile-default", store.MuteKindEntity, "disliked", false, true); err != nil {
		t.Fatal(err)
	}
	if got, want := f.related("self", 0), []string{"kept"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("relatedMedia = %v, want %v (muted Media left out)", got, want)
	}
}

// Review finding P2-4: the read is bounded on the server.
func TestRelatedMediaHonoursLimit(t *testing.T) {
	f := newRelatedFixture(t, false)
	names := []string{"self"}
	for i := range 30 {
		names = append(names, fmt.Sprintf("item-%02d", i))
	}
	f.addMedia(names...)
	f.credit("genre-a", "genre", 0, names...)
	if got := f.related("self", 5); len(got) != 5 {
		t.Fatalf("relatedMedia(limit 5) = %d items, want 5", len(got))
	}
	if got := f.related("self", 0); len(got) != relatedDefaultLimit {
		t.Fatalf("relatedMedia default = %d items, want %d", len(got), relatedDefaultLimit)
	}
	if got := f.related("self", 500); len(got) != 30 {
		t.Fatalf("relatedMedia(limit 500) = %d items, want all 30 under the cap", len(got))
	}
}

func TestRelatedMediaHiddenOrUnknownMedia(t *testing.T) {
	f := newRelatedFixture(t, true)
	f.addMedia("private")
	f.credit("genre-a", "genre", 0, "private")
	for _, id := range []string{string(f.media["private"]), "media-missing"} {
		resp := postGraphJSON(t, f.handler, authorizeViewer,
			`query($mediaID:String!) { relatedMedia(mediaID:$mediaID) { id } }`, map[string]any{"mediaID": id})
		if len(resp.Errors) != 1 || resp.Errors[0].Message != "unknown media" {
			t.Fatalf("relatedMedia(%s) errors = %#v, want unknown media", id, resp.Errors)
		}
	}
}

func TestEditionRailsExcludeGroup(t *testing.T) {
	f := newRelatedFixture(t, false)
	f.addMedia("parent", "edition", "sibling", "other-parent", "other-edition")
	f.credit("category", "genre", 0, "parent", "edition", "sibling", "other-parent", "other-edition")
	f.credit("person", "person", 1, "parent", "edition", "sibling", "other-parent", "other-edition")
	for _, pair := range [][2]string{{"edition", "parent"}, {"sibling", "parent"}, {"other-edition", "other-parent"}} {
		if err := f.st.LinkMediaRelation(context.Background(), f.media[pair[0]], store.MediaRelationEditionOf, f.media[pair[1]]); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"parent", "edition", "sibling"} {
		t.Run(name, func(t *testing.T) {
			if got, want := f.related(name, 0), []string{"other-parent"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("related = %v, want %v", got, want)
			}
			items := graphData[[]SourceItem](t, f.handler,
				`query($context:String!) { sourceItems(kind:"entity-media", entityID:"person", excludeEditionGroupOf:$context, limit:1) { id title } }`,
				map[string]any{"context": string(f.media[name])}, "sourceItems")
			if len(items) != 1 || items[0].ID != string(f.media["other-parent"]) {
				t.Fatalf("More-with = %v, want other-parent", items)
			}
		})
	}
	items := graphData[[]SourceItem](t, f.handler,
		`query { sourceItems(kind:"entity-media", entityID:"person") { id title } }`, nil, "sourceItems")
	var titles []string
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	if want := []string{"edition", "other-edition", "other-parent", "parent", "sibling"}; !reflect.DeepEqual(titles, want) {
		t.Fatalf("person Source = %v, want %v", titles, want)
	}
}

func TestRelatedMediaHiddenParentHasStableRepresentative(t *testing.T) {
	f := newRelatedFixture(t, false)
	f.addMedia("self", "edition", "sibling")
	visibleLibrary := f.library
	var err error
	f.library, err = f.st.CreateLibrary(context.Background(), "Hidden", true, "reels", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	f.addMedia("parent")
	f.library = visibleLibrary
	f.credit("first", "genre", 0, "self", "edition")
	f.credit("second", "genre", 1, "self", "sibling")
	for _, name := range []string{"edition", "sibling"} {
		if err := f.st.LinkMediaRelation(context.Background(), f.media[name], store.MediaRelationEditionOf, f.media["parent"]); err != nil {
			t.Fatal(err)
		}
	}
	want := "edition"
	if f.media["sibling"] < f.media["edition"] {
		want = "sibling"
	}
	if got := f.related("self", 0); !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("related = %v, want one %s", got, want)
	}
	if got := f.related("edition", 0); !reflect.DeepEqual(got, []string{"self"}) {
		t.Fatalf("edition related = %v", got)
	}
}

func TestEntityMediaEditionExclusionHiddenOrUnknown(t *testing.T) {
	f := newRelatedFixture(t, false)
	f.credit("person", "person", 0)
	var err error
	f.library, err = f.st.CreateLibrary(context.Background(), "Hidden", true, "reels", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	f.addMedia("hidden")
	var message string
	for _, id := range []string{string(f.media["hidden"]), "missing"} {
		resp := postGraphJSON(t, f.handler, authorizeViewer,
			`query($context:String!) { sourceItems(kind:"entity-media", entityID:"person", excludeEditionGroupOf:$context) { id } }`, map[string]any{"context": id})
		if len(resp.Errors) != 1 {
			t.Fatalf("errors = %v", resp.Errors)
		}
		if message != "" && resp.Errors[0].Message != message {
			t.Fatalf("hidden/missing errors differ: %q and %q", message, resp.Errors[0].Message)
		}
		message = resp.Errors[0].Message
	}
}

func TestEntityMediaSourcePreservesEditionChildren(t *testing.T) {
	f := newRelatedFixture(t, false)
	f.addMedia("parent", "edition")
	f.credit("person", "person", 0, "edition")
	ctx := context.Background()
	if err := f.st.LinkMediaRelation(ctx, f.media["edition"], store.MediaRelationEditionOf, f.media["parent"]); err != nil {
		t.Fatal(err)
	}
	children, err := f.st.MediaForEntity(ctx, "person", store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].ID != f.media["edition"] {
		t.Fatalf("MediaForEntity = %v, want only the edition", children)
	}
	items := graphData[[]SourceItem](t, f.handler,
		`query { sourceItems(kind:"entity-media", entityID:"person") { id title } }`, nil, "sourceItems")
	if len(items) != 1 || items[0].ID != string(children[0].ID) {
		t.Fatalf("plain Source = %v, want MediaForEntity children %v", items, children)
	}
}
