package manifest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/apptest/plugintest"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestNilRegistryTreatsPluginBoundLibraryAsPluginless(t *testing.T) {
	st := storetest.Open(t)
	if _, err := st.CreateLibrary(context.Background(), "Imported", false, "missing-plugin", "profile-default"); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.NewService(st).Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	document, err := NewService(st, nil, nil).Document(context.Background(), scope)
	if err != nil {
		t.Fatalf("nil registry document: %v", err)
	}
	for _, node := range document.Navigation {
		if node.ID == "library-"+string(document.Libraries[0].ID) && node.LayoutID != "layout-home" {
			t.Fatalf("plugin-bound library layout = %q, want pluginless fallback", node.LayoutID)
		}
	}
	if len(document.Layouts) != 1 || document.Layouts[0].ID != "layout-home" {
		t.Fatalf("nil registry layouts = %+v, want only home", document.Layouts)
	}
}

func TestDocumentHidesRestrictedLibraries(t *testing.T) {
	service, authService := newTestService(t)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}

	doc, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := libraryIDs(doc), []string{"library-local"}; !equalStrings(got, want) {
		t.Fatalf("libraries = %v, want %v", got, want)
	}
	if containsTitle(doc, "Archive") {
		t.Fatal("hidden manifest includes restricted Library title")
	}
	if doc.Revealed {
		t.Fatal("hidden manifest is stamped revealed")
	}
}

func TestDocumentIncludesRestrictedLibrariesWhenRevealed(t *testing.T) {
	service, authService := newTestService(t)
	scope, err := authService.Scope(context.Background(), "session-revealed")
	if err != nil {
		t.Fatal(err)
	}

	doc, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := libraryIDs(doc), []string{"library-local", "library-archive"}; !equalStrings(got, want) {
		t.Fatalf("libraries = %v, want %v", got, want)
	}
	if !containsTitle(doc, "Archive") {
		t.Fatal("revealed manifest is missing restricted Library title")
	}
	if !doc.Revealed {
		t.Fatal("revealed manifest is not stamped revealed")
	}
}

func TestDocumentHonorsManagedProfileAccess(t *testing.T) {
	service, authService := newTestService(t)
	scope, err := authService.Scope(context.Background(), "session-managed")
	if err != nil {
		t.Fatal(err)
	}

	doc, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	if doc.ProfileID != "profile-managed" {
		t.Fatalf("ProfileID = %q, want profile-managed", doc.ProfileID)
	}
	if got, want := libraryIDs(doc), []string{"library-local"}; !equalStrings(got, want) {
		t.Fatalf("libraries = %v, want %v", got, want)
	}
}

func TestHomeLayoutIncludesProfileScopedMostPlayedRail(t *testing.T) {
	service, authService := newTestService(t)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}

	doc, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	home := layoutByID(doc, "layout-home")
	section := sectionByID(home, "your-most-played")
	if section.Title.Resolve("") != "Your Most-Played" || section.Source.Kind != "most-played" {
		t.Fatalf("most-played section = %#v", section)
	}
}

func TestDocumentListsDemoSeedLibraries(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir(), store.Options{SeedProfile: store.SeedProfileDemo})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()
	service := NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, nil)
	authService := auth.NewService(st)

	hidden, err := authService.Scope(context.Background(), "session-demo-viewer")
	if err != nil {
		t.Fatal(err)
	}
	hiddenDoc, err := service.Document(context.Background(), hidden)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := libraryIDs(hiddenDoc), []string{"library-demo-open", "library-demo-archive"}; !equalStrings(got, want) {
		t.Fatalf("hidden demo libraries = %v, want %v", got, want)
	}

	revealed, err := authService.Scope(context.Background(), "session-demo-revealed")
	if err != nil {
		t.Fatal(err)
	}
	revealedDoc, err := service.Document(context.Background(), revealed)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := libraryIDs(revealedDoc), []string{"library-demo-open", "library-demo-archive", "library-demo-guarded"}; !equalStrings(got, want) {
		t.Fatalf("revealed demo libraries = %v, want %v", got, want)
	}
}

func TestDocumentAddsSmartCollectionsToHomeLayout(t *testing.T) {
	st := storetest.Open(t)
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	collection, err := st.CreateSmartCollection(context.Background(), store.SmartCollection{
		LibraryID:  "library-local",
		Name:       "For This Profile",
		SourceExpr: `{"kind":"recently-added"}`,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, nil)
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}

	doc, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	home := doc.Layouts[0]
	section := home.Sections[len(home.Sections)-1]
	if section.ID != "smart-"+string(collection.ID) || section.Title.Resolve("") != "For This Profile" {
		t.Fatalf("smart collection section = %#v", section)
	}
	if section.Source.Kind != "smart-collection" || section.Source.Value != string(collection.ID) {
		t.Fatalf("smart collection source = %#v", section.Source)
	}
}

func newTestService(t *testing.T) (*Service, *auth.Service) {
	t.Helper()
	st := storetest.Open(t)
	return NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, nil), auth.NewService(st)
}

func libraryIDs(doc Document) []string {
	ids := make([]string, len(doc.Libraries))
	for i, library := range doc.Libraries {
		ids[i] = library.ID
	}
	return ids
}

func containsTitle(doc Document, title string) bool {
	for _, library := range doc.Libraries {
		if library.Title == title {
			return true
		}
	}
	for _, node := range doc.Navigation {
		if node.Title.Resolve("") == title {
			return true
		}
	}
	return false
}

func layoutByID(doc Document, id string) Layout {
	for _, layout := range doc.Layouts {
		if layout.ID == id {
			return layout
		}
	}
	return Layout{}
}

func sectionByID(layout Layout, id string) Section {
	for _, section := range layout.Sections {
		if section.ID == id {
			return section
		}
	}
	return Section{}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPluginBoundLibraryGetsPluginLayout(t *testing.T) {
	st := storetest.Open(t)
	registry, err := plugintest.Registry([]plugin.Bundle{{
		ID:           "starlog",
		DisplayName:  plugin.L("Starlog"),
		MediaLabel:   plugin.L("Entry"),
		MediaClasses: []string{"video"},
		EntityKinds: []plugin.EntityKind{
			{Name: "crew-member", Archetype: "person"},
		},
		Layouts: []plugin.LayoutSpec{
			{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "fresh", Kind: "shelf", Title: plugin.L("Fresh Entries"), ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "recently-added"}},
				{ID: "everything", Kind: "grid", Title: plugin.L("All Entries"), Source: plugin.SourceSpec{Kind: "all-items"}},
				{ID: "discover", Kind: "shelf", Title: plugin.L("Directory Discover"), Source: plugin.SourceSpec{Kind: "directory", Value: "trending"}},
			}},
			{ID: "crew", Role: "detail", EntityKind: "crew-member", Sections: []plugin.SectionSpec{
				{ID: "roster", Kind: "list", Title: plugin.L("Roster"), Source: plugin.SourceSpec{Kind: "entity-media"}},
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLibrary(context.Background(), "Starlogs", false, "starlog", "profile-default"); err != nil {
		t.Fatal(err)
	}
	service := NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry)
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}

	document, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	var node NavigationNode
	for _, n := range document.Navigation {
		if n.ID == "library-library-starlogs" {
			node = n
		}
	}
	if node.LayoutID != "layout-starlog" {
		t.Fatalf("plugin library layoutID = %q, want layout-starlog (nav: %#v)", node.LayoutID, document.Navigation)
	}
	var layout Layout
	for _, l := range document.Layouts {
		if l.ID == "layout-starlog" {
			layout = l
		}
	}
	if len(layout.Sections) != 3 || layout.Sections[0].Title.Resolve("") != "Fresh Entries" || layout.Sections[1].Source.Kind != "all-items" {
		t.Fatalf("plugin layout = %#v", layout)
	}
	if source := layout.Sections[2].Source; source.Kind != "directory" || source.Value != "trending" {
		t.Fatalf("directory discover source = %#v, want directory/trending", source)
	}
	// The generic home layout still exists and stays first.
	if document.Layouts[0].ID != "layout-home" {
		t.Fatalf("layout order = %#v, want layout-home first", document.Layouts)
	}
	// Additional (non-browse) Layouts are emitted under their stable
	// "layout-<plugin>-<layoutID>" wire IDs, unreferenced by navigation.
	var detail Layout
	for _, l := range document.Layouts {
		if l.ID == "layout-starlog-crew" {
			detail = l
		}
	}
	if len(detail.Sections) != 1 || detail.Sections[0].ID != "roster" {
		t.Fatalf("detail layout = %#v, want the crew roster section (layouts: %#v)", detail, document.Layouts)
	}
	// Detail Layouts carry their role + entity-kind binding so clients can
	// route an opened entity to the right Layout; the browse Layout keeps
	// its historical shape (both omitted).
	if detail.Role != "detail" || detail.EntityKind != "crew-member" {
		t.Fatalf("detail layout routing metadata = role %q, entityKind %q, want detail/crew-member", detail.Role, detail.EntityKind)
	}
	if layout.Role != "" || layout.EntityKind != "" {
		t.Fatalf("browse layout carries routing metadata: role %q, entityKind %q", layout.Role, layout.EntityKind)
	}
	// The entity-scoped Source travels as the bare kind — the context
	// entity comes from the request, never the manifest.
	if source := detail.Sections[0].Source; source.Kind != "entity-media" || source.EntityKind != "" || len(source.FieldFilters) != 0 {
		t.Fatalf("detail source = %#v, want bare entity-media", source)
	}
	for _, n := range document.Navigation {
		if n.LayoutID == "layout-starlog-crew" {
			t.Fatalf("navigation references the detail layout: %#v", n)
		}
	}
}

// TestManifestTitleWireShape pins the ADR-0034 locale-map wire rule: an
// English-only title serializes as the plain-string shorthand (existing
// English clients keep their shape), while a title carrying translations
// serializes as the full locale map for client-side BCP-47 resolution.
func TestManifestTitleWireShape(t *testing.T) {
	st := storetest.Open(t)
	registry, err := plugintest.Registry([]plugin.Bundle{{
		ID:           "starlog",
		DisplayName:  plugin.L("Starlog"),
		MediaLabel:   plugin.L("Entry"),
		MediaClasses: []string{"video"},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "fresh", Kind: "shelf",
				Title:       plugin.LocaleString{"en": "Fresh Entries", "fr": "Entrées fraîches"},
				ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "recently-added"}},
		}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLibrary(context.Background(), "Starlogs", false, "starlog", "profile-default"); err != nil {
		t.Fatal(err)
	}
	service := NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry)
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	document, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(data)
	// English-only entries keep the plain-string shorthand (Library titles
	// are user data and never gain translations; core-served titles like
	// "Home" are translated and ride the map form below).
	if !strings.Contains(wire, `"title":"Local Library"`) {
		t.Fatalf("English-only title lost its plain-string shorthand: %s", wire)
	}
	// Translated entries ship the full locale map.
	if !strings.Contains(wire, `"title":{"en":"Fresh Entries","fr":"Entrées fraîches"}`) {
		t.Fatalf("translated title did not serialize as a locale map: %s", wire)
	}
}

// TestManifestHasAmbientEntry pins the ADR-0035 §1 contribution: one top-level
// "Ambient" Navigation entry, present and well-formed, on every manifest
// (independent of plugins or libraries). It is a fixed-vocabulary posture named
// by shape (View "ambient") — generic, no domain vocab (invariant #1/#3) — and
// carries no LayoutID, since the ambient session assembles its Ambient Queue
// live from a Source rather than browsing a Layout.
func TestManifestHasAmbientEntry(t *testing.T) {
	service, authService := newTestService(t)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	var ambient *NavigationNode
	for i := range doc.Navigation {
		if doc.Navigation[i].ID == "ambient" {
			ambient = &doc.Navigation[i]
			break
		}
	}
	if ambient == nil {
		t.Fatalf("manifest has no top-level Ambient navigation entry: %+v", doc.Navigation)
	}
	if ambient.View != "ambient" {
		t.Fatalf("Ambient entry View = %q, want %q", ambient.View, "ambient")
	}
	if ambient.LayoutID != "" {
		t.Fatalf("Ambient entry carries LayoutID %q, want none (queue is live-assembled, not a Layout)", ambient.LayoutID)
	}
	if ambient.LibraryID != "" {
		t.Fatalf("Ambient entry carries LibraryID %q, want none (top-level posture)", ambient.LibraryID)
	}
	if got := ambient.Title.Resolve("en"); got != "Ambient" {
		t.Fatalf("Ambient entry English title = %q, want %q", got, "Ambient")
	}
}

// TestSourceFieldFiltersWireShape pins the filter serialization rule: a
// Section's declared FieldFilters travel on its wire SourceSpec (clients echo
// them back on sourceItems), and filterless Sources omit the key entirely so
// existing payload shapes stay byte-identical.
func TestSourceFieldFiltersWireShape(t *testing.T) {
	st := storetest.Open(t)
	registry, err := plugintest.Registry([]plugin.Bundle{{
		ID:           "starlog",
		DisplayName:  plugin.L("Starlog"),
		MediaLabel:   plugin.L("Entry"),
		MediaClasses: []string{"video"},
		MediaFields: []plugin.FieldSpec{
			{Name: "phase", Type: "text"},
			{Name: "score", Type: "number"},
			{Name: "verified", Type: "flag"},
			{Name: "released", Type: "date"},
			{Name: "runtime", Type: "number"},
		},
		Layouts: []plugin.LayoutSpec{
			{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "everything", Kind: "grid", Title: plugin.L("All Entries"),
					Source: plugin.SourceSpec{Kind: "all-items"}},
				{ID: "flights", Kind: "grid", Title: plugin.L("Flights"),
					Source: plugin.SourceSpec{Kind: "all-items",
						FieldFilters: map[string]any{
							"phase":    "flight",
							"score":    8.5,
							"verified": true,
							"released": "2020-07-04T12:00:00Z",
							"runtime":  map[string]any{"gte": 90, "lte": 120},
						}}},
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLibrary(context.Background(), "Starlogs", false, "starlog", "profile-default"); err != nil {
		t.Fatal(err)
	}
	service := NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry)
	scope, err := auth.NewService(st).Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}

	document, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	var layout Layout
	for _, l := range document.Layouts {
		if l.ID == "layout-starlog" {
			layout = l
		}
	}
	if len(layout.Sections) != 2 {
		t.Fatalf("layout = %#v, want both grids", layout)
	}
	if got := layout.Sections[1].Source.FieldFilters; got["phase"] != "flight" {
		t.Fatalf("filtered source = %#v, want phase=flight", layout.Sections[1].Source)
	}

	plain, err := json.Marshal(layout.Sections[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "fieldFilters") {
		t.Fatalf("filterless source serializes fieldFilters: %s", plain)
	}
	filtered, err := json.Marshal(layout.Sections[1].Source)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"fieldFilters":{"phase":"flight","released":"2020-07-04T12:00:00Z","runtime":{"gte":90,"lte":120},"score":8.5,"verified":true}`; !strings.Contains(string(filtered), want) {
		t.Fatalf("filtered source wire = %s, want %s", filtered, want)
	}

	var round plugin.SourceSpec
	if err := json.Unmarshal(filtered, &round); err != nil {
		t.Fatal(err)
	}
	if got := round.FieldFilters["phase"]; got != "flight" {
		t.Fatalf("phase filter = %#v, want flight", got)
	}
	if got := round.FieldFilters["score"]; got != float64(8.5) {
		t.Fatalf("score filter = %#v, want 8.5", got)
	}
	if got := round.FieldFilters["verified"]; got != true {
		t.Fatalf("verified filter = %#v, want true", got)
	}
	if got := round.FieldFilters["released"]; got != "2020-07-04T12:00:00Z" {
		t.Fatalf("released filter = %#v, want RFC3339 string", got)
	}
	rangeFilter, ok := round.FieldFilters["runtime"].(map[string]any)
	if !ok {
		t.Fatalf("runtime filter = %#v, want range object", round.FieldFilters["runtime"])
	}
	if rangeFilter["gte"] != float64(90) || rangeFilter["lte"] != float64(120) {
		t.Fatalf("runtime range = %#v, want gte/lte numbers", rangeFilter)
	}
}

// TestFilterBarAndAccentWireShape pins the BF-20 wire additions: a
// filter-bar Section's declared options travel as the `filters` array (and
// every other Section omits the key), and a bound plugin's accent rides the
// LibrarySummary as `accent` (omitted for plugin-less Libraries) — both
// additive, so existing payload shapes stay byte-identical.
func TestFilterBarAndAccentWireShape(t *testing.T) {
	st := storetest.Open(t)
	registry, err := plugintest.Registry([]plugin.Bundle{{
		ID:           "starlog",
		DisplayName:  plugin.L("Starlog"),
		MediaLabel:   plugin.L("Entry"),
		MediaClasses: []string{"video"},
		Accent:       "#3FA6C9",
		MediaFields:  []plugin.FieldSpec{{Name: "phase", Type: "text"}},
		Layouts: []plugin.LayoutSpec{
			{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "refine", Kind: "filter-bar", Title: plugin.L("Refine"),
					Source: plugin.SourceSpec{Kind: "all-items"},
					FilterOptions: []plugin.FilterOption{
						{Field: "phase", Value: "flight",
							Label: plugin.LocaleString{"en": "Flights", "fr": "Vols"}},
					}},
				{ID: "everything", Kind: "grid", Title: plugin.L("All Entries"),
					Source: plugin.SourceSpec{Kind: "all-items"}},
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLibrary(context.Background(), "Starlogs", false, "starlog", "profile-default"); err != nil {
		t.Fatal(err)
	}
	service := NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry)
	scope, err := auth.NewService(st).Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}

	document, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	var layout Layout
	for _, l := range document.Layouts {
		if l.ID == "layout-starlog" {
			layout = l
		}
	}
	if len(layout.Sections) != 2 {
		t.Fatalf("layout = %#v, want filter-bar plus grid", layout)
	}
	bar, err := json.Marshal(layout.Sections[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := `"filters":[{"field":"phase","value":"flight","label":{"en":"Flights","fr":"Vols"}}]`; !strings.Contains(string(bar), want) {
		t.Fatalf("filter-bar wire = %s, want %s", bar, want)
	}
	grid, err := json.Marshal(layout.Sections[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(grid), "filters") {
		t.Fatalf("optionless section serializes filters: %s", grid)
	}

	plain, plugged := LibrarySummary{}, LibrarySummary{}
	for _, library := range document.Libraries {
		if library.PluginID != nil && *library.PluginID == "starlog" {
			plugged = library
		} else {
			plain = library
		}
	}
	if plugged.Accent != "#3FA6C9" {
		t.Fatalf("plugin-bound library accent = %q, want #3FA6C9", plugged.Accent)
	}
	plainWire, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plainWire), "accent") {
		t.Fatalf("plugin-less library serializes accent: %s", plainWire)
	}
}

// TestSourceEntityFilterWireShape pins the entity-relation filter's wire rule,
// mirroring FieldFilters: a Section's declared EntityFilter travels on its
// wire SourceSpec as the entityFilter object (clients echo it back on
// sourceItems), and filterless Sources omit the key so existing payload
// shapes stay byte-identical.
func TestSourceEntityFilterWireShape(t *testing.T) {
	st := storetest.Open(t)
	registry, err := plugintest.Registry([]plugin.Bundle{{
		ID:           "starlog",
		DisplayName:  plugin.L("Starlog"),
		MediaLabel:   plugin.L("Entry"),
		MediaClasses: []string{"video"},
		EntityKinds: []plugin.EntityKind{
			{Name: "maker", DisplayName: plugin.L("Maker"), Archetype: "category",
				Relations: []plugin.RelationSpec{{Name: "made", ToKind: plugin.MediaKindName}}},
		},
		Layouts: []plugin.LayoutSpec{
			{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "everything", Kind: "grid", Title: plugin.L("All Entries"),
					Source: plugin.SourceSpec{Kind: "all-items"}},
				{ID: "acme", Kind: "shelf", Title: plugin.L("From Acme"),
					Source: plugin.SourceSpec{Kind: "all-items",
						EntityFilter: &plugin.EntityFilter{Kind: "maker", Name: "Acme"}}},
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLibrary(context.Background(), "Starlogs", false, "starlog", "profile-default"); err != nil {
		t.Fatal(err)
	}
	service := NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry)
	scope, err := auth.NewService(st).Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}

	document, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	var layout Layout
	for _, l := range document.Layouts {
		if l.ID == "layout-starlog" {
			layout = l
		}
	}
	if len(layout.Sections) != 2 {
		t.Fatalf("layout = %#v, want both sections", layout)
	}
	filter := layout.Sections[1].Source.EntityFilter
	if filter == nil || filter.Kind != "maker" || filter.Name != "Acme" {
		t.Fatalf("filtered source = %#v, want maker/Acme entity filter", layout.Sections[1].Source)
	}

	plain, err := json.Marshal(layout.Sections[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "entityFilter") {
		t.Fatalf("filterless source serializes entityFilter: %s", plain)
	}
	filtered, err := json.Marshal(layout.Sections[1].Source)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"entityFilter":{"kind":"maker","name":"Acme"}`; !strings.Contains(string(filtered), want) {
		t.Fatalf("filtered source wire = %s, want %s", filtered, want)
	}
}

// TestPlaybackAffordancesSectionReachesDetailLayout proves the manifest
// mechanically passes a playback-affordances Section — the Resume CTA plus
// direct-play/AirPlay chips of the approved iOS detail mockup — through to the
// compiled detail Layout (RECON-IOS-DETAIL). The wire kind and its affordances
// come from the plugin's ADR-0014 Section declaration; core only forwards them.
func TestPlaybackAffordancesSectionReachesDetailLayout(t *testing.T) {
	st := storetest.Open(t)
	registry, err := plugintest.Registry([]plugin.Bundle{{
		ID:           "starlog",
		DisplayName:  plugin.L("Starlog"),
		MediaLabel:   plugin.L("Entry"),
		MediaClasses: []string{"video"},
		Layouts: []plugin.LayoutSpec{
			{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "everything", Kind: "grid", Title: plugin.L("All Entries"), Source: plugin.SourceSpec{Kind: "all-items"}},
			}},
			{ID: "entry", Role: "detail", Sections: []plugin.SectionSpec{
				{
					ID:                  "playback",
					Kind:                "playback-affordances",
					Title:               plugin.L("Play"),
					Source:              plugin.SourceSpec{Kind: "entity-media"},
					PlaybackAffordances: []string{"resume", "direct-play", "airplay"},
				},
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLibrary(context.Background(), "Starlogs", false, "starlog", "profile-default"); err != nil {
		t.Fatal(err)
	}
	service := NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry)
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	document, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	var detail Layout
	for _, l := range document.Layouts {
		if l.ID == "layout-starlog-entry" {
			detail = l
		}
	}
	if len(detail.Sections) != 1 || detail.Sections[0].Kind != "playback-affordances" {
		t.Fatalf("detail layout = %#v, want one playback-affordances section", detail)
	}
	if got := detail.Sections[0].PlaybackAffordances; len(got) != 3 ||
		got[0] != "resume" || got[1] != "direct-play" || got[2] != "airplay" {
		t.Fatalf("playback affordances = %#v, want [resume direct-play airplay]", got)
	}

	// The affordances travel on the wire under a stable "playbackAffordances"
	// key so every renderer (starting with the iOS detail screen) inherits the
	// Resume CTA and direct-play/AirPlay chips.
	wire, err := json.Marshal(detail.Sections[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := `"playbackAffordances":["resume","direct-play","airplay"]`; !strings.Contains(string(wire), want) {
		t.Fatalf("playback-affordances section wire = %s, want %s", wire, want)
	}

	// A Section without playback affordances keeps its historical wire shape —
	// the new key is omitted rather than emitted empty.
	browse := document.Layouts[0]
	plain, err := json.Marshal(browse.Sections[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "playbackAffordances") {
		t.Fatalf("non-playback section wire carries the key: %s", plain)
	}
}
