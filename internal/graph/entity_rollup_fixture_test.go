package graph

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// rollUpFixture is a Library whose plugin declares a two-level container
// chain (a part holds Media and nests into a volume), a person credited on
// Media one by one and on volumes as a whole, and a category tagging Media.
type rollUpFixture struct {
	handler  *Handler
	library  store.LibraryID
	person   store.EntityID
	topic    store.EntityID
	volumes  []store.EntityID
	parts    []store.EntityID
	held     []store.MediaID
	loose    []store.MediaID
	volumeOf map[store.MediaID]store.EntityID
}

func rollUpBundle() plugin.Bundle {
	return plugin.Bundle{
		ID:          "rollupkit",
		Version:     "0.1.0",
		DisplayName: plugin.L("Rollupkit"),
		MediaLabel:  plugin.L("Piece"),
		EntityKinds: []plugin.EntityKind{
			{Name: "volume", Archetype: "container", OrderedChildren: true, Relations: []plugin.RelationSpec{
				{Name: "credit", ToKind: "author"},
			}},
			{Name: "part", Archetype: "container", OrderedChildren: true, Relations: []plugin.RelationSpec{
				{Name: "holds", ToKind: plugin.MediaKindName, Cardinality: "one"},
				{Name: "part-of", ToKind: "volume"},
			}},
			{Name: "author", Archetype: "person", Relations: []plugin.RelationSpec{
				{Name: "credited-in", ToKind: plugin.MediaKindName},
			}},
			{Name: "topic", Archetype: "category", Relations: []plugin.RelationSpec{
				{Name: "tags", ToKind: plugin.MediaKindName},
			}},
		},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}
}

// newRollUpFixture builds volumes × partsPer × itemsPer held Media and loose
// standalone Media. The person is credited on every Media item and on every
// volume; the topic tags every Media item.
func newRollUpFixture(tb testing.TB, volumes, partsPer, itemsPer, loose int) rollUpFixture {
	tb.Helper()
	st := storetest.Open(tb)
	registry, err := newTestPluginRegistry([]plugin.Bundle{rollUpBundle()})
	if err != nil {
		tb.Fatal(err)
	}
	f := rollUpFixture{
		handler: NewHandler(
			manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
			auth.NewService(st),
			st,
			registry,
		),
		person:   "entity-author",
		topic:    "entity-topic",
		volumeOf: map[store.MediaID]store.EntityID{},
	}
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Rollup", false, "rollupkit", "profile-default")
	if err != nil {
		tb.Fatal(err)
	}
	f.library = library.ID
	upsert := func(e store.Entity) {
		if err := st.UpsertEntity(ctx, e); err != nil {
			tb.Fatal(err)
		}
	}
	relate := func(r store.EntityRelation) {
		if err := st.UpsertEntityRelation(ctx, r); err != nil {
			tb.Fatal(err)
		}
	}
	importMedia := func(name string, year int) store.MediaID {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/rollup/" + name + ".mkv", Title: name, Year: year,
			Container: "mkv", Codec: "h264", ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			tb.Fatal(err)
		}
		return item.ID
	}
	upsert(store.Entity{ID: f.person, LibraryID: library.ID, Kind: "author", Title: "Author"})
	upsert(store.Entity{ID: f.topic, LibraryID: library.ID, Kind: "topic", Title: "Topic"})
	for v := range volumes {
		volume := store.EntityID(fmt.Sprintf("entity-volume-%03d", v))
		f.volumes = append(f.volumes, volume)
		upsert(store.Entity{ID: volume, LibraryID: library.ID, Kind: "volume", Title: fmt.Sprintf("Volume %03d", v)})
		relate(store.EntityRelation{FromEntityID: volume, Name: "credit", ToEntityID: f.person})
		for p := range partsPer {
			part := store.EntityID(fmt.Sprintf("entity-part-%03d-%02d", v, p))
			f.parts = append(f.parts, part)
			upsert(store.Entity{ID: part, LibraryID: library.ID, Kind: "part", Title: fmt.Sprintf("Part %d", p+1)})
			relate(store.EntityRelation{FromEntityID: part, Name: "part-of", ToEntityID: volume})
			for i := range itemsPer {
				// Later items are older, so a volume's year is not its first item's.
				id := importMedia(fmt.Sprintf("v%03d-p%02d-i%02d", v, p, i), 2030-p*itemsPer-i)
				f.held = append(f.held, id)
				f.volumeOf[id] = volume
				position := i
				relate(store.EntityRelation{FromEntityID: part, Name: "holds", ToMediaID: id, Cardinality: "one", SortOrder: &position})
				relate(store.EntityRelation{FromEntityID: f.person, Name: "credited-in", ToMediaID: id})
				relate(store.EntityRelation{FromEntityID: f.topic, Name: "tags", ToMediaID: id})
			}
		}
	}
	for i := range loose {
		id := importMedia(fmt.Sprintf("loose-%03d", i), 0)
		f.loose = append(f.loose, id)
		relate(store.EntityRelation{FromEntityID: f.person, Name: "credited-in", ToMediaID: id})
		relate(store.EntityRelation{FromEntityID: f.topic, Name: "tags", ToMediaID: id})
	}
	return f
}
