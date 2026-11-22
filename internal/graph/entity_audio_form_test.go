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

// TestEntityQueryAudioForm pins CORE-2288: a container Entity whose first
// member is audio reads audioForm "feed" (with its Subscription) when that
// member is a catalogued Subscription entry, "long-form" when the Library's
// plugin resumes Media, and no hint otherwise.
func TestEntityQueryAudioForm(t *testing.T) {
	st := storetest.Open(t)
	resumes := true
	kinds := func(prefix string) []plugin.EntityKind {
		return []plugin.EntityKind{{
			Name:              prefix + "-set",
			DisplayName:       plugin.L("Set"),
			PluralDisplayName: plugin.L("Sets"),
			Archetype:         "container",
			OrderedChildren:   true,
			Relations:         []plugin.RelationSpec{{Name: prefix + "-member", ToKind: plugin.MediaKindName}},
		}, {
			Name:        prefix + "-tag",
			DisplayName: plugin.L("Tag"),
			Archetype:   "category",
			Relations:   []plugin.RelationSpec{{Name: prefix + "-tagged", ToKind: plugin.MediaKindName}},
		}}
	}
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID: "spoken", Version: "1.0.0", DisplayName: plugin.L("Spoken"), MediaLabel: plugin.L("Part"),
		Engagement:  plugin.Engagement{ResumeMedia: &resumes},
		EntityKinds: kinds("spoken"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}, {
		ID: "tunes", Version: "1.0.0", DisplayName: plugin.L("Tunes"), MediaLabel: plugin.L("Cut"),
		EntityKinds: kinds("tunes"),
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
	ctx := context.Background()
	now := time.Now().UTC()
	spoken, err := st.CreateLibrary(ctx, "Spoken", false, "spoken", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	tunes, err := st.CreateLibrary(ctx, "Tunes", false, "tunes", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	importMedia := func(library store.LibraryID, name string, class store.MediaClass) store.MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library, Path: "/srv/" + name, Title: name, Container: "m4a", Codec: "aac",
			Class: class, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: spoken.ID, FeedURL: "https://feeds.example.com/talk", Root: "/media/talk", Title: "Talk",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	entry := func(n int) store.FeedCatalogEntry {
		return store.FeedCatalogEntry{
			GUID: fmt.Sprintf("talk-%d", n), Title: fmt.Sprintf("Talk %d", n), PublishedAt: now.Add(time.Duration(n) * time.Hour),
			EnclosureURL: fmt.Sprintf("https://cdn.example.com/talk-%d.m4a", n), EnclosureType: "audio/x-m4a", EnclosureLength: 1000,
		}
	}
	catalogued, err := st.UpsertSubscriptionCatalog(ctx, sub.ID, []store.FeedCatalogEntry{entry(1), entry(2)}, now)
	if err != nil || len(catalogued) != 2 {
		t.Fatalf("catalogue = %+v, %v", catalogued, err)
	}
	members := map[store.EntityID]struct {
		library store.LibraryID
		kind    string
		media   store.MediaID
	}{
		"entity-feed":      {spoken.ID, "spoken-set", catalogued[0].MediaID},
		"entity-long-form": {spoken.ID, "spoken-set", importMedia(spoken.ID, "part.m4a", store.MediaClassAudio)},
		"entity-album":     {tunes.ID, "tunes-set", importMedia(tunes.ID, "cut.m4a", store.MediaClassAudio)},
		"entity-video":     {spoken.ID, "spoken-set", importMedia(spoken.ID, "clip.mkv", store.MediaClassVideo)},
		"entity-category":  {spoken.ID, "spoken-tag", importMedia(spoken.ID, "tagged.m4a", store.MediaClassAudio)},
		"entity-empty":     {spoken.ID, "spoken-set", ""},
	}
	for id, m := range members {
		if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: m.library, Kind: m.kind, Title: string(id)}); err != nil {
			t.Fatal(err)
		}
		if m.media == "" {
			continue
		}
		relation := m.kind[:len(m.kind)-4] + "-member"
		if m.kind == "spoken-tag" {
			relation = "spoken-tagged"
		}
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: id, Name: relation, ToMediaID: m.media}); err != nil {
			t.Fatal(err)
		}
	}
	// Only the first member decides: a standalone first part keeps the
	// long-form hint though a later member is a feed entry, and an unordered
	// set's first member is the title-first one mediaForEntity lists, not the
	// lowest ID.
	relate := func(id store.EntityID, media store.MediaID, order *int) {
		t.Helper()
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: id, Name: "spoken-member", ToMediaID: media, SortOrder: order}); err != nil {
			t.Fatal(err)
		}
	}
	first, second := 0, 1
	for _, id := range []store.EntityID{"entity-mixed", "entity-unordered"} {
		if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: spoken.ID, Kind: "spoken-set", Title: string(id)}); err != nil {
			t.Fatal(err)
		}
	}
	relate("entity-mixed", importMedia(spoken.ID, "standalone.m4a", store.MediaClassAudio), &first)
	relate("entity-mixed", catalogued[1].MediaID, &second)
	relate("entity-unordered", importMedia(spoken.ID, "Zulu.m4a", store.MediaClassAudio), nil)
	relate("entity-unordered", importMedia(spoken.ID, "Alpha.mkv", store.MediaClassVideo), nil)
	want := map[store.EntityID][2]string{
		"entity-mixed":     {"long-form", ""},
		"entity-unordered": {"", ""},
		"entity-feed":      {"feed", string(sub.ID)},
		"entity-long-form": {"long-form", ""},
		"entity-album":     {"", ""},
		"entity-video":     {"", ""},
		"entity-category":  {"", ""},
		"entity-empty":     {"", ""},
	}
	for id, w := range want {
		var got EntityDetailInfo
		query := fmt.Sprintf(`query { entity(id: %q) { id audioForm subscriptionID } }`, id)
		execGraph(t, handler, authorizeManager, query, nil, "entity", &got)
		if got.AudioForm != w[0] || got.SubscriptionID != w[1] {
			t.Errorf("%s audioForm/subscriptionID = %q/%q, want %q/%q", id, got.AudioForm, got.SubscriptionID, w[0], w[1])
		}
	}
}
