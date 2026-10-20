package plugin

import (
	"math"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
)

// Test bundles use invented vocabulary (worldtest rule): "starlog" is a fake
// domain that exercises every declarative surface.
func validBundle() Bundle {
	return Bundle{
		ID:                 "starlog",
		Version:            "1.0.0",
		SDKFloor:           sdk.Version,
		SDKCeiling:         sdk.Version,
		DisplayName:        L("Starlog"),
		MediaLabel:         L("Entry"),
		MediaClasses:       []string{"video"},
		FeatureEligibility: []string{FeatureAudioStation},
		MediaFields:        []FieldSpec{{Name: "phase", Type: "text", Label: LocaleString{"en": "Phase", "fr": "Phase"}}},
		MediaClassLabels: map[string]LocaleString{
			"flight": {"en": "Flight", "fr": "Vol"},
		},
		EntityKinds: []EntityKind{
			{Name: "navigator", DisplayName: LocaleString{"en": "Navigator", "fr": "Navigateur"},
				Archetype: "person",
				Fields:    []FieldSpec{{Name: "rank", Type: "text", Label: LocaleString{"en": "Rank", "fr": "Grade"}}},
				// Media-targeted relation — the link EntityMaps hydrate.
				Relations: []RelationSpec{{Name: "logged-by", ToKind: MediaKindName, Cardinality: "one"}}},
			{Name: "voyage", Archetype: "container", OrderedChildren: true,
				Relations: []RelationSpec{{Name: "crewed-by", ToKind: "navigator", Attributes: []string{"role"}}}},
		},
		Layouts: []LayoutSpec{
			{ID: "home", Role: "browse", Sections: []SectionSpec{
				{ID: "fresh", Kind: "shelf", Title: L("Fresh Entries"), ArtworkSlot: "poster", Source: SourceSpec{Kind: "recently-added"}},
				{ID: "everything", Kind: "grid", Title: L("All Entries"), ArtworkSlot: "thumb", Source: SourceSpec{Kind: "all-items"}},
			}},
			{ID: "navigator-detail", Role: "detail", EntityKind: "navigator", Sections: []SectionSpec{
				{ID: "entries", Kind: "list", Title: L("Entries"), ArtworkSlot: "thumb", Source: SourceSpec{Kind: "all-items"}},
			}},
		},
		Scrapers: []ScraperSpec{{
			ID: "starbase", DisplayName: L("Starbase"), Kind: "http-json",
			Endpoint: "https://starbase.example",
			AuthRef:  "starbase-key", AuthStyle: "query:api_key",
			SearchTemplate: "/find?q={query}", CandidatesPath: "hits", CandidateIDPath: "id",
			LookupTemplate: "/entries/{id}",
			FieldMap:       map[string]string{"title": "data.name", "year": "data.launched"},
			Transforms:     map[string]string{"year": "year-from-date"},
			ArtworkMap:     map[string]string{"poster": "data.image"},
			// Search elements are flat (no data envelope) — the per-template
			// override maps.
			SearchFieldMap:   map[string]string{"title": "name", "year": "launched"},
			SearchArtworkMap: map[string]string{"poster": "image"},
			ArtworkBaseURL:   "https://img.starbase.example/full",
			// Identifying through this scraper implies the declared "phase"
			// Media field's value (the BF-24 "media." namespace).
			ImpliedMediaFields: map[string]string{"media.phase": "flight"},
			// Lookup documents hydrate navigator Entities from the crew list.
			EntityMaps: []EntityMap{
				{Kind: "navigator", Relation: "logged-by", Path: "data.crew", NamePath: "name", ArtworkPath: "badge"},
			},
		}},
	}
}

func TestValidateAcceptsCompleteBundle(t *testing.T) {
	if err := Validate(validBundle()); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateAcceptsPlaybackAffordancesSection(t *testing.T) {
	bundle := validBundle()
	bundle.Layouts[1].Sections = append(bundle.Layouts[1].Sections, SectionSpec{
		ID:                  "playback",
		Kind:                "playback-affordances",
		Source:              SourceSpec{Kind: "entity-media"},
		PlaybackAffordances: PlaybackAffordances(),
	})
	if err := Validate(bundle); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	bundle.Layouts[1].Sections[1].PlaybackAffordances = []string{"cast"}
	if err := Validate(bundle); err == nil || !strings.Contains(err.Error(), "playback affordance") {
		t.Fatalf("Validate returned %v, want playback affordance error", err)
	}
}

func TestValidateAcceptsLegacyBlankVersion(t *testing.T) {
	bundle := validBundle()
	bundle.Version = ""
	if err := Validate(bundle); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateRejectsInvalidIdentificationPolicy(t *testing.T) {
	bundle := validBundle()
	bundle.IdentificationPolicy = "manual"
	if err := Validate(bundle); err == nil || !strings.Contains(err.Error(), "identification policy") {
		t.Fatalf("Validate returned %v, want identification policy error", err)
	}
	if _, err := NewRegistry([]Bundle{bundle}); err == nil || !strings.Contains(err.Error(), "identification policy") {
		t.Fatalf("NewRegistry returned %v, want identification policy error", err)
	}
}

func TestRegistryFeatureEligiblePluginIDs(t *testing.T) {
	reg, err := NewRegistry([]Bundle{validBundle(), {
		ID:           "quiet",
		Version:      "1.0.0",
		SDKFloor:     sdk.Version,
		SDKCeiling:   sdk.Version,
		DisplayName:  L("Quiet"),
		MediaLabel:   L("Item"),
		MediaClasses: []string{"audio"},
		Layouts:      []LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := reg.FeatureEligiblePluginIDs(FeatureAudioStation)
	if len(got) != 1 || got[0] != "starlog" {
		t.Fatalf("FeatureEligiblePluginIDs = %v, want [starlog]", got)
	}
}

// sdkRangeAboveEmbedded derives an SDK floor/ceiling pair that is strictly
// newer than the embedded sdk.Version by construction, while keeping one shared
// major as validateSDKCompatibility requires. Deriving it instead of pinning a
// literal is deliberate: on 2026-08-23 the hardcoded "0.3.0"/"0.4.0" range in
// TestValidateRejectsTooNewSDKFloor silently began *including* the embedded SDK
// once it reached 0.3.1, so Validate returned nil, the rejection assertion went
// vacuous, and the failure closed core main for the whole fleet. Any literal
// one notch higher would rot the same way at the next SDK bump.
func sdkRangeAboveEmbedded(t *testing.T) (floor, ceiling string) {
	t.Helper()
	parts := strings.Split(sdk.Version, ".")
	if len(parts) != 3 {
		t.Fatalf("sdk.Version = %q, want major.minor.patch", sdk.Version)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("sdk.Version = %q has a non-numeric minor: %v", sdk.Version, err)
	}
	floor = parts[0] + "." + strconv.Itoa(minor+1) + ".0"
	ceiling = parts[0] + "." + strconv.Itoa(minor+2) + ".0"
	return floor, ceiling
}

func TestValidateRejectsTooNewSDKFloor(t *testing.T) {
	bundle := validBundle()
	bundle.SDKFloor, bundle.SDKCeiling = sdkRangeAboveEmbedded(t)
	if err := Validate(bundle); err == nil || !strings.Contains(err.Error(), "excludes embedded SDK") {
		t.Fatalf("Validate error = %v, want SDK floor rejection", err)
	}
	if _, err := NewRegistry([]Bundle{bundle}); err == nil {
		t.Fatal("NewRegistry loaded a plugin requiring a newer SDK")
	}
}

func TestValidateRejectsTooOldSDKCeiling(t *testing.T) {
	bundle := validBundle()
	bundle.SDKFloor = "0.1.0"
	bundle.SDKCeiling = "0.1.10"
	if err := Validate(bundle); err == nil || !strings.Contains(err.Error(), "excludes embedded SDK") {
		t.Fatalf("Validate error = %v, want SDK ceiling rejection", err)
	}
	if _, err := NewRegistry([]Bundle{bundle}); err == nil {
		t.Fatal("NewRegistry loaded a plugin excluding the embedded SDK")
	}
}

func TestValidateRequiresSDKBounds(t *testing.T) {
	missingFloor := validBundle()
	missingFloor.SDKFloor = ""
	if err := Validate(missingFloor); err == nil || !strings.Contains(err.Error(), "SDK floor is required") {
		t.Fatalf("Validate error = %v, want required SDK floor", err)
	}

	missingCeiling := validBundle()
	missingCeiling.SDKCeiling = ""
	if err := Validate(missingCeiling); err == nil || !strings.Contains(err.Error(), "SDK ceiling is required") {
		t.Fatalf("Validate error = %v, want required SDK ceiling", err)
	}
}

func TestValidateRejectsCrossMajorSDKRange(t *testing.T) {
	bundle := validBundle()
	bundle.SDKFloor = "0.2.0"
	bundle.SDKCeiling = "1.0.0"
	if err := Validate(bundle); err == nil || !strings.Contains(err.Error(), "one supported major") {
		t.Fatalf("Validate error = %v, want supported-major rejection", err)
	}
}

// An EntityMap may populate the hydrated Entity's declared fields and link a
// same-kind parent on a Hierarchical kind (the entity field + hierarchy
// hydration layer).
func TestValidateAcceptsEntityMapFieldsAndHierarchy(t *testing.T) {
	bundle := validBundle()
	bundle.EntityKinds = append(bundle.EntityKinds, EntityKind{
		Name: "house", Archetype: "container", Hierarchical: true,
		Relations: []RelationSpec{
			{Name: "from-house", ToKind: MediaKindName},
			{Name: "child-of", ToKind: "house"},
		},
	})
	bundle.Scrapers[0].EntityMaps = append(bundle.Scrapers[0].EntityMaps,
		EntityMap{Kind: "navigator", Relation: "logged-by", Path: "data.crew", NamePath: "name",
			FieldMap: map[string]string{"rank": "rank"}, OrdinalField: "rank"},
		EntityMap{Kind: "house", Relation: "from-house", Path: "data.label", NamePath: "name",
			ParentNamePath: "parent", ParentRelation: "child-of"},
	)
	if err := Validate(bundle); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateRejectsInvalidLabelMapsAtLoad(t *testing.T) {
	bundle := validBundle()
	bundle.LabelMaps = []LabelMapSpec{{Label: "tone", Tag: "tone", Kind: "navigator", Relation: "logged-by"}}
	if err := Validate(bundle); err == nil {
		t.Fatal("Validate accepted a dual-target LabelMap")
	}
	if _, err := NewRegistry([]Bundle{bundle}); err == nil {
		t.Fatal("NewRegistry loaded a plugin with an invalid LabelMap")
	}
}

func TestResolveLabelMapsUsesFixedEqualityNormalization(t *testing.T) {
	bundle := validBundle()
	bundle.LabelMaps = []LabelMapSpec{
		{Label: " Space   Walk ", Tag: "spacewalk", MinConfidence: 0.7},
		{Label: "space walk", Kind: "navigator", Relation: "logged-by", EntityName: "EVA", MinConfidence: 0.7},
	}

	got := ResolveLabelMaps(bundle, []Label{
		{Name: "space walk", Confidence: 0.9},
		{Name: "space", Confidence: 0.99},
		{Name: "SPACE   WALK", Confidence: 0.6},
	}, 0.5)

	if len(got.Tags) != 1 || got.Tags[0].Tag != "spacewalk" {
		t.Fatalf("tag targets = %#v, want one spacewalk target", got.Tags)
	}
	if len(got.Entities) != 1 || got.Entities[0].Kind != "navigator" || got.Entities[0].Relation != "logged-by" {
		t.Fatalf("entity targets = %#v, want one navigator/logged-by target", got.Entities)
	}
	if got.Unmatched != 2 {
		t.Fatalf("unmatched = %d, want 2 (literal miss + below floor)", got.Unmatched)
	}
}

func TestResolveLabelMapsUsesFallbackFloor(t *testing.T) {
	bundle := validBundle()
	bundle.LabelMaps = []LabelMapSpec{{Label: "tone", Tag: "tone"}}
	got := ResolveLabelMaps(bundle, []Label{{Name: "tone", Confidence: 0.4}}, 0.5)
	if len(got.Tags) != 0 || got.Unmatched != 1 {
		t.Fatalf("result = %#v, want below fallback floor dropped", got)
	}
}

func TestResolveLabelMapsRejectsInvalidConfidence(t *testing.T) {
	bundle := validBundle()
	bundle.LabelMaps = []LabelMapSpec{{Label: "tone", Tag: "tone", MinConfidence: 0.5}}
	got := ResolveLabelMaps(bundle, []Label{
		{Name: "tone", Confidence: math.NaN()},
		{Name: "tone", Confidence: -0.1},
		{Name: "tone", Confidence: 1.1},
	}, 0.5)
	if len(got.Tags) != 0 || len(got.Entities) != 0 || got.Unmatched != 3 {
		t.Fatalf("result = %#v, want all invalid confidences unmatched", got)
	}
}

// filterBarSection is one valid filter-bar Section carrying the given
// options; rejection cases mutate the options.
func filterBarSection(options ...FilterOption) SectionSpec {
	return SectionSpec{ID: "refine", Kind: "filter-bar", Title: L("Refine"),
		Source: SourceSpec{Kind: "all-items"}, FilterOptions: options}
}

func TestValidateAcceptsAccentAndFilterBarOptions(t *testing.T) {
	bundle := validBundle()
	bundle.Accent = "#C9803F"
	bundle.Layouts[0].Sections = append(bundle.Layouts[0].Sections,
		filterBarSection(FilterOption{Field: "phase", Value: "flight",
			Label: LocaleString{"en": "Flights", "fr": "Vols"}}))
	if err := Validate(bundle); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

// The "exif" local-file scraper kind is accepted with no
// endpoint, no templates, and no auth — it reads the Media's own file — and may
// still carry an ImpliedMediaFields constant classification.
func TestValidateAcceptsLocalFileExifScraper(t *testing.T) {
	bundle := validBundle()
	bundle.Scrapers = append(bundle.Scrapers, ScraperSpec{
		ID: "exif", DisplayName: L("EXIF"), Kind: "exif",
		ImpliedMediaFields: map[string]string{"media.phase": "flight"},
	})
	if err := Validate(bundle); err != nil {
		t.Fatalf("Validate rejected a well-formed exif scraper: %v", err)
	}
}

// A "credentials" importer reads from a live source behind a secret, so it must
// declare the secret-store ref the wizard fills.
func TestValidateAcceptsCredentialsImporterWithRef(t *testing.T) {
	bundle := validBundle()
	bundle.Importers = []ImporterSpec{{
		ID:            "emby",
		DisplayName:   LocaleString{"en": "Emby", "fr": "Emby"},
		Categories:    []string{"watch-state"},
		Input:         "credentials",
		Conflict:      "additive",
		CredentialRef: "emby-token",
	}}
	if err := Validate(bundle); err != nil {
		t.Fatalf("Validate rejected a well-formed credentials importer: %v", err)
	}
}

// A "credentials" importer with no credentialRef cannot collect its secret —
// an authoring mistake, refused rather than enqueuing an unauthenticatable run.
func TestValidateRejectsCredentialsImporterWithoutRef(t *testing.T) {
	bundle := validBundle()
	bundle.Importers = []ImporterSpec{{
		ID:          "emby",
		DisplayName: LocaleString{"en": "Emby", "fr": "Emby"},
		Categories:  []string{"watch-state"},
		Input:       "credentials",
		Conflict:    "additive",
	}}
	if err := Validate(bundle); err == nil {
		t.Fatal("Validate accepted a credentials importer with no credentialRef")
	}
}

// An "export-file" importer reads a path, not a secret, so a credentialRef on it
// is a declaration mistake — surfaced rather than silently ignored.
func TestValidateRejectsExportFileImporterWithRef(t *testing.T) {
	bundle := validBundle()
	bundle.Importers = []ImporterSpec{{
		ID:            "plex",
		DisplayName:   LocaleString{"en": "Plex", "fr": "Plex"},
		Categories:    []string{"watch-state"},
		Input:         "export-file",
		Conflict:      "additive",
		CredentialRef: "plex-token",
	}}
	if err := Validate(bundle); err == nil {
		t.Fatal("Validate accepted an export-file importer that declares a credentialRef")
	}
}

// Network-only fields on an "exif" scraper are authoring mistakes, surfaced
// rather than silently ignored.
func TestValidateRejectsExifScraperWithNetworkFields(t *testing.T) {
	cases := map[string]func(*ScraperSpec){
		"endpoint":    func(s *ScraperSpec) { s.Endpoint = "https://x.example" },
		"lookup":      func(s *ScraperSpec) { s.LookupTemplate = "/x/{id}" },
		"search":      func(s *ScraperSpec) { s.SearchTemplate = "/q?={query}" },
		"auth":        func(s *ScraperSpec) { s.AuthRef = "k"; s.AuthStyle = "bearer" },
		"candidates":  func(s *ScraperSpec) { s.CandidatesPath = "hits" },
		"field-map":   func(s *ScraperSpec) { s.FieldMap = map[string]string{"title": "x"} },
		"artwork-map": func(s *ScraperSpec) { s.ArtworkMap = map[string]string{"poster": "x"} },
		"entity-maps": func(s *ScraperSpec) {
			s.EntityMaps = []EntityMap{{Kind: "navigator", Relation: "logged-by", Path: "p"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			bundle := validBundle()
			spec := ScraperSpec{ID: "exif", DisplayName: L("EXIF"), Kind: "exif"}
			mutate(&spec)
			bundle.Scrapers = append(bundle.Scrapers, spec)
			if err := Validate(bundle); err == nil {
				t.Fatalf("Validate accepted an exif scraper with a %s field, want rejection", name)
			}
		})
	}
}

func TestValidateAcceptsFieldIndexPostures(t *testing.T) {
	bundle := validBundle()
	bundle.EntityKinds[0].Fields = []FieldSpec{
		{Name: "rank", Type: "text", Filterable: true, Sortable: true, Searchable: true},
		{Name: "commissioned", Type: "date", Filterable: true, Sortable: true},
		{Name: "missions", Type: "number", Sortable: true},
		{Name: "active", Type: "flag", Filterable: true},
	}
	if err := Validate(bundle); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateRejectsVocabularyViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Bundle)
		want   string
	}{
		{"bad archetype", func(b *Bundle) { b.EntityKinds[0].Archetype = "hero-figure" }, "unknown archetype"},
		{"bad section kind", func(b *Bundle) { b.Layouts[0].Sections[0].Kind = "carousel" }, "unknown kind"},
		{"bad artwork slot", func(b *Bundle) { b.Layouts[0].Sections[0].ArtworkSlot = "mural" }, "unknown artwork slot"},
		{"bad source kind", func(b *Bundle) { b.Layouts[0].Sections[0].Source.Kind = "trending" }, "unknown source kind"},
		{"bad accent", func(b *Bundle) { b.Accent = "amber" }, "hex color"},
		{"filter options on non-filter-bar", func(b *Bundle) {
			b.Layouts[0].Sections[0].FilterOptions = []FilterOption{{Field: "phase", Value: "flight"}}
		}, "only filter-bar sections take them"},
		{"filter option undeclared field", func(b *Bundle) {
			b.Layouts[0].Sections = append(b.Layouts[0].Sections,
				filterBarSection(FilterOption{Field: "ghost", Value: "flight"}))
		}, "undeclared media field"},
		{"filter option non-text field", func(b *Bundle) {
			b.MediaFields = append(b.MediaFields, FieldSpec{Name: "stardate", Type: "number"})
			b.Layouts[0].Sections = append(b.Layouts[0].Sections,
				filterBarSection(FilterOption{Field: "stardate", Value: "9"}))
		}, "only text fields can be filtered"},
		{"filter option blank value", func(b *Bundle) {
			b.Layouts[0].Sections = append(b.Layouts[0].Sections,
				filterBarSection(FilterOption{Field: "phase", Value: " "}))
		}, "requires a value"},
		{"filter option label without English anchor", func(b *Bundle) {
			b.Layouts[0].Sections = append(b.Layouts[0].Sections,
				filterBarSection(FilterOption{Field: "phase", Value: "flight", Label: LocaleString{"fr": "Vol"}}))
		}, `"en" entry`},
		{"section title without English anchor", func(b *Bundle) {
			b.Layouts[0].Sections[0].Title = LocaleString{"fr": "Entrées fraîches"}
		}, `"en" entry`},
		{"section title with blank translation", func(b *Bundle) {
			b.Layouts[0].Sections[0].Title = LocaleString{"en": "Fresh Entries", "fr": ""}
		}, "blank"},
		{"bad layout ID", func(b *Bundle) { b.Layouts[0].ID = "Home" }, "lowercase slug"},
		{"duplicate layout ID", func(b *Bundle) { b.Layouts[1].ID = "home" }, "duplicate layout ID"},
		{"bad layout role", func(b *Bundle) { b.Layouts[1].Role = "sidebar" }, "unknown role"},
		{"zero browse layouts", func(b *Bundle) { b.Layouts[0].Role = "detail" }, "exactly one browse layout, got 0"},
		{"two browse layouts", func(b *Bundle) {
			b.Layouts[1].Role = "browse"
			b.Layouts[1].EntityKind = ""
		}, "exactly one browse layout, got 2"},
		{"entity kind on browse layout", func(b *Bundle) { b.Layouts[0].EntityKind = "navigator" }, "only detail layouts take an entity kind"},
		{"dangling layout entity kind", func(b *Bundle) { b.Layouts[1].EntityKind = "ghost" }, "binds undeclared entity kind"},
		{"duplicate section ID within layout", func(b *Bundle) { b.Layouts[0].Sections[1].ID = "fresh" }, "duplicate section ID"},
		{"bad field type", func(b *Bundle) { b.EntityKinds[0].Fields[0].Type = "blob" }, "unknown type"},
		{"kind display name without English anchor", func(b *Bundle) {
			b.EntityKinds[0].DisplayName = LocaleString{"fr": "Navigateur"}
		}, `"en" entry`},
		{"kind field label without English anchor", func(b *Bundle) {
			b.EntityKinds[0].Fields[0].Label = LocaleString{"fr": "Grade"}
		}, `"en" entry`},
		{"media field label with blank translation", func(b *Bundle) {
			b.MediaFields[0].Label = LocaleString{"en": "Phase", "fr": ""}
		}, "blank"},
		{"bad scraper kind", func(b *Bundle) { b.Scrapers[0].Kind = "ftp" }, "unknown kind"},
		{"http endpoint", func(b *Bundle) { b.Scrapers[0].Endpoint = "http://starbase.example" }, "must be https"},
		{"dangling relation", func(b *Bundle) { b.EntityKinds[1].Relations[0].ToKind = "ghost" }, "undeclared kind"},
		{"bad relation cardinality", func(b *Bundle) { b.EntityKinds[0].Relations[0].Cardinality = "several" }, "unknown cardinality"},
		{"blank relation attribute", func(b *Bundle) { b.EntityKinds[1].Relations[0].Attributes = []string{"role", " "} }, "blank attribute"},
		{"duplicate relation attribute", func(b *Bundle) { b.EntityKinds[1].Relations[0].Attributes = []string{"role", "role"} }, "duplicate attribute"},
		{"searchable non-text kind field", func(b *Bundle) {
			b.EntityKinds[0].Fields = append(b.EntityKinds[0].Fields,
				FieldSpec{Name: "commissioned", Type: "date", Searchable: true})
		}, "only text fields can be searchable"},
		{"searchable non-text media field", func(b *Bundle) {
			b.MediaFields = append(b.MediaFields,
				FieldSpec{Name: "stardate", Type: "number", Searchable: true})
		}, "only text fields can be searchable"},
		{"indexed kind field with non-slug name", func(b *Bundle) {
			b.EntityKinds[0].Fields = append(b.EntityKinds[0].Fields,
				FieldSpec{Name: "Rank Code", Type: "text", Filterable: true})
		}, "must be a lowercase slug"},
		{"bad media class", func(b *Bundle) { b.MediaClasses = []string{"hologram"} }, "unknown media class"},
		{"bad media field type", func(b *Bundle) { b.MediaFields[0].Type = "blob" }, "unknown type"},
		{"bad media field name", func(b *Bundle) { b.MediaFields[0].Name = "Phase" }, "must be a lowercase slug"},
		{"duplicate media field", func(b *Bundle) {
			b.MediaFields = append(b.MediaFields, FieldSpec{Name: "phase", Type: "number"})
		}, "duplicate media field"},
		{"bad class label key", func(b *Bundle) {
			b.MediaClassLabels = map[string]LocaleString{"Flight": {"en": "Flight"}}
		}, "must be a lowercase slug"},
		{"class label without english", func(b *Bundle) {
			b.MediaClassLabels = map[string]LocaleString{"flight": {"fr": "Vol"}}
		}, "requires an English fallback"},
		{"bad scraper slot", func(b *Bundle) { b.Scrapers[0].ArtworkMap = map[string]string{"mural": "x"} }, "unknown artwork slot"},
		{"no templates", func(b *Bundle) {
			b.Scrapers[0].LookupTemplate = ""
			b.Scrapers[0].SearchTemplate = ""
			b.Scrapers[0].CandidatesPath = ""
			b.Scrapers[0].CandidateIDPath = ""
		}, "neither a lookup nor a search template"},
		{"bad auth style", func(b *Bundle) { b.Scrapers[0].AuthStyle = "cookie" }, "unknown auth style"},
		{"bad auth style param", func(b *Bundle) { b.Scrapers[0].AuthStyle = "header:" }, "unknown auth style"},
		{"auth style without ref", func(b *Bundle) {
			b.Scrapers[0].AuthRef = ""
			b.Scrapers[0].AuthStyle = "query:api_key"
		}, "without an AuthRef"},
		{"login-bearer without login", func(b *Bundle) { b.Scrapers[0].AuthStyle = "login-bearer" }, "require each other"},
		{"login without login-bearer", func(b *Bundle) {
			b.Scrapers[0].Login = &LoginSpec{Path: "/login", SecretField: "apikey", TokenPath: "data.token"}
		}, "require each other"},
		{"incomplete login", func(b *Bundle) {
			b.Scrapers[0].AuthStyle = "login-bearer"
			b.Scrapers[0].Login = &LoginSpec{Path: "login", SecretField: "apikey", TokenPath: "data.token"}
		}, "login declaration"},
		{"search without candidates path", func(b *Bundle) { b.Scrapers[0].CandidatesPath = "" }, "requires a candidates path"},
		{"negative max pages", func(b *Bundle) { b.Scrapers[0].MaxPages = -1 }, "max pages must not be negative"},
		{"search+lookup without candidate id", func(b *Bundle) { b.Scrapers[0].CandidateIDPath = "" }, "no candidate ID path"},
		{"candidates without search", func(b *Bundle) { b.Scrapers[0].SearchTemplate = "" }, "without a search template"},
		{"search maps without search template", func(b *Bundle) {
			b.Scrapers[0].SearchTemplate = ""
			b.Scrapers[0].CandidatesPath = ""
			b.Scrapers[0].CandidateIDPath = ""
			b.Scrapers[0].SearchArtworkMap = nil
		}, "search maps without a search template"},
		{"bad search artwork slot", func(b *Bundle) {
			b.Scrapers[0].SearchArtworkMap = map[string]string{"mural": "image"}
		}, "unknown artwork slot"},
		{"transform unmapped in both maps", func(b *Bundle) {
			b.Scrapers[0].Transforms = map[string]string{"summary": "year-from-date"}
		}, "unmapped field"},
		{"rss search", func(b *Bundle) {
			b.Scrapers[0].Kind = "rss"
			b.Scrapers[0].AuthStyle = ""
		}, "rss scrapers cannot declare a search template"},
		{"bad transform kind", func(b *Bundle) { b.Scrapers[0].Transforms = map[string]string{"year": "uppercase"} }, "unknown transform"},
		{"bad field type", func(b *Bundle) { b.Scrapers[0].FieldTypes = map[string]string{"score": "decimal"} }, "unknown field type"},
		{"transform on unmapped field", func(b *Bundle) { b.Scrapers[0].Transforms = map[string]string{"ghost": "year-from-date"} }, "unmapped field"},
		{"http artwork base", func(b *Bundle) { b.Scrapers[0].ArtworkBaseURL = "http://img.starbase.example" }, "artwork base URL must be https"},
		{"negative request interval", func(b *Bundle) { b.Scrapers[0].MinRequestIntervalMS = -1 }, "must not be negative"},
		{"implied undeclared media field", func(b *Bundle) {
			b.Scrapers[0].ImpliedMediaFields = map[string]string{"media.ghost": "flight"}
		}, "does not declare"},
		{"implied bare built-in field", func(b *Bundle) {
			b.Scrapers[0].ImpliedMediaFields = map[string]string{"title": "flight"}
		}, "implies built-in field"},
		{"implied non-text media field", func(b *Bundle) {
			b.MediaFields[0].Type = "number"
		}, "only text fields can be implied"},
		{"implied media field without value", func(b *Bundle) {
			b.Scrapers[0].ImpliedMediaFields = map[string]string{"media.phase": "  "}
		}, "without a value"},
		{"field map unknown bare key", func(b *Bundle) {
			b.Scrapers[0].FieldMap = map[string]string{"title": "data.name", "rank": "data.rank"}
		}, "is not a built-in"},
		{"field map unmatched media key", func(b *Bundle) {
			b.Scrapers[0].FieldMap = map[string]string{"title": "data.name", "media.ghost": "data.ghost"}
		}, "does not declare"},
		{"field map entity namespace", func(b *Bundle) {
			b.Scrapers[0].FieldMap = map[string]string{"title": "data.name", "entity.navigator": "data.captain"}
		}, "entity routing is EntityMap"},
		{"search field map unknown bare key", func(b *Bundle) {
			b.Scrapers[0].SearchFieldMap = map[string]string{"title": "name", "rank": "rank"}
		}, "is not a built-in"},
		{"entity map undeclared kind", func(b *Bundle) {
			b.Scrapers[0].EntityMaps[0].Kind = "ghost"
		}, "entity map targets undeclared kind"},
		{"entity map relation not declared on kind", func(b *Bundle) {
			b.Scrapers[0].EntityMaps[0].Relation = "ghost-link"
		}, "not a Media-targeted relation"},
		{"entity map relation not media-targeted", func(b *Bundle) {
			// crewed-by is declared on voyage and targets navigator, not Media.
			b.Scrapers[0].EntityMaps[0] = EntityMap{Kind: "voyage", Relation: "crewed-by", Path: "data.crew"}
		}, "not a Media-targeted relation"},
		{"entity map blank path", func(b *Bundle) {
			b.Scrapers[0].EntityMaps[0].Path = "  "
		}, "requires a path"},
		{"entity map blank artwork path", func(b *Bundle) {
			b.Scrapers[0].EntityMaps[0].ArtworkPath = "  "
		}, "blank artwork path"},
		{"entity map field undeclared", func(b *Bundle) {
			b.Scrapers[0].EntityMaps[0].FieldMap = map[string]string{"ghost": "data.ghost"}
		}, "maps undeclared field"},
		{"entity map field blank path", func(b *Bundle) {
			b.Scrapers[0].EntityMaps[0].FieldMap = map[string]string{"rank": "  "}
		}, "has a blank path"},
		{"entity map ordinal field not in field map", func(b *Bundle) {
			b.Scrapers[0].EntityMaps[0].OrdinalField = "rank"
		}, "ordinal field"},
		{"entity map parent without relation", func(b *Bundle) {
			b.Scrapers[0].EntityMaps[0].ParentNamePath = "data.parent"
		}, "without a parent relation"},
		{"entity map parent relation without name path", func(b *Bundle) {
			b.Scrapers[0].EntityMaps[0].ParentRelation = "child-of"
		}, "without a parent name path"},
		{"entity map parent relation not same-kind hierarchical", func(b *Bundle) {
			// navigator is not Hierarchical and has no same-kind relation.
			b.Scrapers[0].EntityMaps[0].ParentNamePath = "data.parent"
			b.Scrapers[0].EntityMaps[0].ParentRelation = "logged-by"
		}, "not a same-kind relation"},
		{"entity maps without lookup template", func(b *Bundle) {
			b.Scrapers[0].LookupTemplate = ""
		}, "entity maps without a lookup template"},
		{"entity maps on rss", func(b *Bundle) {
			b.Scrapers[0].Kind = "rss"
			b.Scrapers[0].AuthStyle = ""
			b.Scrapers[0].AuthRef = ""
			b.Scrapers[0].SearchTemplate = ""
			b.Scrapers[0].CandidatesPath = ""
			b.Scrapers[0].CandidateIDPath = ""
			b.Scrapers[0].SearchFieldMap = nil
			b.Scrapers[0].SearchArtworkMap = nil
		}, "rss scrapers cannot declare entity maps"},
	}
	for _, c := range cases {
		bundle := validBundle()
		c.mutate(&bundle)
		err := Validate(bundle)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// A transform's field name may be mapped by either template's map — one
// transform vocabulary serves both (the documented rule).
func TestTransformMayMapThroughSearchFieldMapOnly(t *testing.T) {
	bundle := validBundle()
	bundle.Scrapers[0].FieldMap = map[string]string{"title": "data.name"}
	bundle.Scrapers[0].SearchFieldMap = map[string]string{"title": "name", "year": "launched"}
	bundle.Scrapers[0].Transforms = map[string]string{"year": "year-from-date"}
	if err := Validate(bundle); err != nil {
		t.Fatalf("transform on a search-only-mapped field rejected: %v", err)
	}
}

func TestBrowseLayoutAccessor(t *testing.T) {
	bundle := validBundle()
	if got, ok := bundle.BrowseLayout(); !ok || got.ID != "home" || got.Role != "browse" {
		t.Fatalf("BrowseLayout = %#v, want the home browse layout", got)
	}
	if got, ok := (Bundle{}).BrowseLayout(); ok || got.ID != "" || len(got.Sections) != 0 {
		t.Fatalf("BrowseLayout on an empty bundle = (%#v, %t), want zero LayoutSpec and false", got, ok)
	}
}

func TestRegistryRejectsDuplicateIDs(t *testing.T) {
	if _, err := NewRegistry([]Bundle{validBundle(), validBundle()}); err == nil {
		t.Fatal("registry accepted duplicate plugin IDs")
	}
	registry, err := NewRegistry([]Bundle{validBundle()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Bundle("starlog"); !ok {
		t.Fatal("activated bundle not found")
	}
	if _, ok := registry.Bundle("ghost"); ok {
		t.Fatal("unknown bundle reported active")
	}
}

func TestRegistryOwnsActivatedBundleState(t *testing.T) {
	bundle := validBundle()
	registry, err := NewRegistry([]Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}

	bundle.FeatureEligibility[0] = "mutated-input"
	if got := registry.FeatureEligiblePluginIDs(FeatureAudioStation); len(got) != 1 || got[0] != bundle.ID {
		t.Fatalf("eligibility after input mutation = %v, want [%s]", got, bundle.ID)
	}

	activated, ok := registry.Bundle(bundle.ID)
	if !ok {
		t.Fatal("activated bundle not found")
	}
	activated.FeatureEligibility[0] = "mutated-result"
	if got := registry.FeatureEligiblePluginIDs(FeatureAudioStation); len(got) != 1 || got[0] != bundle.ID {
		t.Fatalf("eligibility after result mutation = %v, want [%s]", got, bundle.ID)
	}
}

func TestRegistryScopesScraperAndImporterCredentialRefsPerPlugin(t *testing.T) {
	bundle := func(id ID) Bundle {
		b := validBundle()
		b.ID = id
		b.Scrapers[0].AuthRef = "shared-key"
		b.Importers = []ImporterSpec{{
			ID:            "source-import",
			DisplayName:   L("Source import"),
			Categories:    []string{"watch-state"},
			Input:         "credentials",
			Conflict:      "additive",
			CredentialRef: "shared-key",
		}}
		return b
	}
	registry, err := NewRegistry([]Bundle{bundle("alpha"), bundle("beta")})
	if err != nil {
		t.Fatal(err)
	}

	refs := map[ID]string{}
	for _, id := range []ID{"alpha", "beta"} {
		activated, ok := registry.Bundle(id)
		if !ok {
			t.Fatalf("activated bundle %q not found", id)
		}
		want := ScopedCredentialRef(id, "shared-key")
		if got := activated.Scrapers[0].AuthRef; got != want {
			t.Fatalf("plugin %q scraper AuthRef = %q, want %q", id, got, want)
		}
		if got := activated.Importers[0].CredentialRef; got != want {
			t.Fatalf("plugin %q importer CredentialRef = %q, want %q", id, got, want)
		}
		refs[id] = activated.Scrapers[0].AuthRef
	}
	if refs["alpha"] == refs["beta"] {
		t.Fatalf("cross-plugin credential refs collide: %q", refs["alpha"])
	}
}

func TestScopedCredentialRefsArePortableRoundTripAndRejectEscape(t *testing.T) {
	scoped := ScopedCredentialRef("alpha", "api-key.v1")
	if scoped == "" {
		t.Fatal("valid local credential ref was rejected")
	}
	if strings.ContainsAny(scoped, `<>:"/\\|?*`) || strings.HasSuffix(scoped, ".") || strings.HasSuffix(scoped, " ") {
		t.Fatalf("scoped ref %q is not a portable filename", scoped)
	}
	pluginID, local, err := ParseScopedCredentialRef(scoped)
	if err != nil || pluginID != "alpha" || local != "api-key.v1" {
		t.Fatalf("round trip = (%q, %q, %v)", pluginID, local, err)
	}
	for _, malicious := range []string{"../secret", `..\\secret`, "plugin:beta:secret", scoped, "CON", ".hidden"} {
		if got := ScopedCredentialRef("alpha", malicious); got != "" {
			t.Errorf("ScopedCredentialRef(%q) = %q, want refusal", malicious, got)
		}
	}
}

func TestValidateEntityItemsSource(t *testing.T) {
	bundle := Bundle{
		ID:          "demo",
		Version:     "0.1.0",
		SDKFloor:    sdk.Version,
		SDKCeiling:  sdk.Version,
		DisplayName: L("Demo"),
		MediaLabel:  L("Item"),
		EntityKinds: []EntityKind{{Name: "maker", Archetype: "person"}},
		Layouts: []LayoutSpec{{ID: "home", Role: "browse", Sections: []SectionSpec{{
			ID:     "makers",
			Kind:   "grid",
			Title:  L("Makers"),
			Source: SourceSpec{Kind: "entity-items", EntityKind: "maker"},
		}}}},
	}
	if err := Validate(bundle); err != nil {
		t.Fatalf("valid entity-items section rejected: %v", err)
	}

	bundle.Layouts[0].Sections[0].Source.EntityKind = "unknown"
	if err := Validate(bundle); err == nil {
		t.Fatal("undeclared entity kind accepted")
	}

	if err := ValidateSource(SourceSpec{Kind: "entity-items"}); err == nil {
		t.Fatal("entity-items without entity kind accepted")
	}
	if err := ValidateSource(SourceSpec{Kind: "recently-added", EntityKind: "maker"}); err == nil {
		t.Fatal("entity kind on a non-entity source accepted")
	}
}

func TestValidateEntityMediaSource(t *testing.T) {
	// entity-media takes no parameters — the context entity comes from the
	// request, so the bare kind is the whole spec.
	if err := ValidateSource(SourceSpec{Kind: "entity-media"}); err != nil {
		t.Fatalf("bare entity-media source rejected: %v", err)
	}
	if err := ValidateSource(SourceSpec{Kind: "entity-media", EntityKind: "maker"}); err == nil {
		t.Fatal("entity kind on an entity-media source accepted")
	}
}

func TestValidateOnThisDaySource(t *testing.T) {
	// on-this-day is a fixed, parameterless date projection.
	if err := ValidateSource(SourceSpec{Kind: "on-this-day"}); err != nil {
		t.Fatalf("bare on-this-day source rejected: %v", err)
	}
	if err := ValidateSource(SourceSpec{Kind: "on-this-day", EntityKind: "album"}); err == nil {
		t.Fatal("entity kind on on-this-day source accepted")
	}
}

func TestValidateRandomSource(t *testing.T) {
	// random is a recognized kind; a seed is its one parameter,
	// and empty is the valid default seed.
	if err := ValidateSource(SourceSpec{Kind: "random"}); err != nil {
		t.Fatalf("bare random source rejected: %v", err)
	}
	if err := ValidateSource(SourceSpec{Kind: "random", Seed: "shuffle-1"}); err != nil {
		t.Fatalf("random source with seed rejected: %v", err)
	}
	// A seed belongs only to random — it must not ride other kinds.
	if err := ValidateSource(SourceSpec{Kind: "recently-added", Seed: "shuffle-1"}); err == nil {
		t.Fatal("seed on a non-random source accepted")
	}
}

// Valued predicate Sources: each requires a
// Value drawn from its closed vocabulary; no other kind takes a Value, and a
// missing/unknown value on a valued kind is rejected.
func TestValidateValuedSources(t *testing.T) {
	if err := ValidateSource(SourceSpec{Kind: "resume-progress", Value: "in-progress"}); err != nil {
		t.Fatalf("resume-progress with in-progress value rejected: %v", err)
	}
	for _, value := range AvailabilityValues() {
		if err := ValidateSource(SourceSpec{Kind: "availability", Value: value}); err != nil {
			t.Fatalf("availability value %q rejected: %v", value, err)
		}
	}
	for _, value := range WatchedByAnyProfileValues() {
		if err := ValidateSource(SourceSpec{Kind: "watched-by-any-profile", Value: value}); err != nil {
			t.Fatalf("watched-by-any-profile value %q rejected: %v", value, err)
		}
	}
	// A valued kind requires a value, and rejects one outside its vocabulary.
	if err := ValidateSource(SourceSpec{Kind: "resume-progress"}); err == nil {
		t.Fatal("resume-progress without a value accepted")
	}
	if err := ValidateSource(SourceSpec{Kind: "availability", Value: "downloading"}); err == nil {
		t.Fatal("availability with an unknown value accepted")
	}
	if err := ValidateSource(SourceSpec{Kind: "watched-by-any-profile"}); err == nil {
		t.Fatal("watched-by-any-profile without a value accepted")
	}
	if err := ValidateSource(SourceSpec{Kind: "watched-by-any-profile", Value: "profile-default"}); err == nil {
		t.Fatal("watched-by-any-profile with a Profile-like value accepted")
	}
	// resume-progress's vocabulary is not availability's, and vice versa.
	if err := ValidateSource(SourceSpec{Kind: "resume-progress", Value: "archived"}); err == nil {
		t.Fatal("availability value accepted on resume-progress")
	}
	// A value belongs only to the valued kinds — it must not ride others.
	if err := ValidateSource(SourceSpec{Kind: "recently-added", Value: "in-progress"}); err == nil {
		t.Fatal("value on a non-valued source accepted")
	}
}

// FieldFilters: declarative typed filters keyed by declared Media fields,
// accepted only on the catalog feeds (FilterableSourceKinds).
func TestValidateFieldFilters(t *testing.T) {
	withFilter := func(kind string, filters map[string]any, fields ...FieldSpec) Bundle {
		bundle := validBundle()
		bundle.MediaFields = append(bundle.MediaFields, fields...)
		bundle.Layouts[0].Sections[1].Source = SourceSpec{Kind: kind, FieldFilters: filters}
		return bundle
	}

	for _, kind := range FilterableSourceKinds() {
		if err := Validate(withFilter(kind, map[string]any{"phase": "flight"})); err != nil {
			t.Fatalf("declared-field text filter on %q rejected: %v", kind, err)
		}
		if err := Validate(withFilter(kind, map[string]any{"score": 8.5},
			FieldSpec{Name: "score", Type: "number"})); err != nil {
			t.Fatalf("declared-field number filter on %q rejected: %v", kind, err)
		}
		if err := Validate(withFilter(kind, map[string]any{"released": "2026-01-01"},
			FieldSpec{Name: "released", Type: "date"})); err != nil {
			t.Fatalf("declared-field date filter on %q rejected: %v", kind, err)
		}
		if err := Validate(withFilter(kind, map[string]any{"verified": true},
			FieldSpec{Name: "verified", Type: "flag"})); err != nil {
			t.Fatalf("declared-field flag filter on %q rejected: %v", kind, err)
		}
		if err := Validate(withFilter(kind, map[string]any{"score": map[string]any{"gte": 4.5, "lte": 9.5}},
			FieldSpec{Name: "score", Type: "number"})); err != nil {
			t.Fatalf("declared-field number range on %q rejected: %v", kind, err)
		}
		if err := Validate(withFilter(kind, map[string]any{"released": map[string]any{"gte": "2026-01-01", "lte": "2026-12-31"}},
			FieldSpec{Name: "released", Type: "date"})); err != nil {
			t.Fatalf("declared-field date range on %q rejected: %v", kind, err)
		}
	}

	cases := []struct {
		name   string
		bundle Bundle
		want   string
	}{
		{"undeclared field", withFilter("all-items", map[string]any{"orbit": "low"}), "undeclared media field"},
		{"blank value", withFilter("all-items", map[string]any{"phase": " "}), "non-blank string"},
		{"bad key shape", withFilter("all-items", map[string]any{"Phase": "flight"}), "lowercase slug"},
		{"text range", withFilter("all-items", map[string]any{"phase": map[string]any{"gte": "a", "lte": "z"}}), "range"},
		{"engagement feed", withFilter("continue-watching", map[string]any{"phase": "flight"}), "does not take field filters"},
		{"next-up feed", withFilter("next-up", map[string]any{"phase": "flight"}), "does not take field filters"},
		{"entity feed", withFilter("entity-media", map[string]any{"phase": "flight"}), "does not take field filters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.bundle)
			if err == nil {
				t.Fatal("invalid field filter accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}

	// Filters must respect the field type: a text field cannot take a range.
	bundle := withFilter("all-items", map[string]any{"phase": map[string]any{"gte": "a", "lte": "z"}})
	if err := Validate(bundle); err == nil || !strings.Contains(err.Error(), "range") {
		t.Fatalf("text-range field filter error = %v, want range rejection", err)
	}
}

// EntityFilter: the declarative entity-relation filter, accepted only on the
// catalog feeds, keyed by a declared Entity Kind plus a non-blank name.
func TestValidateEntityFilter(t *testing.T) {
	withFilter := func(kind string, filter *EntityFilter) Bundle {
		bundle := validBundle()
		bundle.Layouts[0].Sections[1].Source = SourceSpec{Kind: kind, EntityFilter: filter}
		return bundle
	}

	for _, kind := range FilterableSourceKinds() {
		if err := Validate(withFilter(kind, &EntityFilter{Kind: "navigator", Name: "Ada"})); err != nil {
			t.Fatalf("declared-kind entity filter on %q rejected: %v", kind, err)
		}
	}
	// EntityFilter and FieldFilters compose on the same Source.
	combined := withFilter("all-items", &EntityFilter{Kind: "navigator", Name: "Ada"})
	combined.Layouts[0].Sections[1].Source.FieldFilters = map[string]any{"phase": "flight"}
	if err := Validate(combined); err != nil {
		t.Fatalf("entity filter combined with field filters rejected: %v", err)
	}

	cases := []struct {
		name   string
		bundle Bundle
		want   string
	}{
		{"undeclared kind", withFilter("all-items", &EntityFilter{Kind: "captain", Name: "Ada"}), "undeclared entity kind"},
		{"bad kind shape", withFilter("all-items", &EntityFilter{Kind: "Navigator", Name: "Ada"}), "lowercase slug"},
		{"blank name", withFilter("all-items", &EntityFilter{Kind: "navigator", Name: " "}), "requires a name"},
		{"engagement feed", withFilter("continue-watching", &EntityFilter{Kind: "navigator", Name: "Ada"}), "does not take an entity filter"},
		{"entity feed", withFilter("entity-media", &EntityFilter{Kind: "navigator", Name: "Ada"}), "does not take an entity filter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.bundle)
			if err == nil {
				t.Fatal("invalid entity filter accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateCertificationScale(t *testing.T) {
	withScale := func(scale []string, fields ...FieldSpec) Bundle {
		bundle := validBundle()
		bundle.MediaFields = append(bundle.MediaFields, fields...)
		bundle.CertificationScale = scale
		return bundle
	}
	certField := FieldSpec{Name: "certification", Type: "text"}

	if err := Validate(withScale([]string{"cadet", "officer", "captain"}, certField)); err != nil {
		t.Fatalf("valid certification scale rejected: %v", err)
	}
	// No scale, no certification field: scale is opt-in.
	if err := Validate(validBundle()); err != nil {
		t.Fatalf("bundle without scale rejected: %v", err)
	}

	cases := []struct {
		name   string
		bundle Bundle
		want   string
	}{
		{"missing field", withScale([]string{"cadet", "captain"}), "requires the text media field"},
		{"non-text field", func() Bundle {
			bundle := validBundle()
			bundle.MediaFields = append(bundle.MediaFields, FieldSpec{Name: "certification", Type: "number"})
			bundle.CertificationScale = []string{"cadet", "captain"}
			return bundle
		}(), "requires the text media field"},
		{"blank value", withScale([]string{"cadet", " "}, certField), "non-blank"},
		{"duplicate value", withScale([]string{"cadet", "cadet"}, certField), "duplicate certification scale value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.bundle)
			if err == nil {
				t.Fatal("invalid certification scale accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestInventoryRegistryStandingByOrigin(t *testing.T) {
	builtin := validBundle()
	staged := Bundle{
		ID:           "quiet",
		Version:      "1.0.0",
		SDKFloor:     sdk.Version,
		SDKCeiling:   sdk.Version,
		DisplayName:  L("Quiet"),
		MediaLabel:   L("Item"),
		MediaClasses: []string{"audio"},
		Layouts:      []LayoutSpec{{ID: "home", Role: "browse"}},
	}
	stagedStanding := PluginStanding{Provenance: ProvenanceThirdParty, TrustState: PluginTrusted}
	reg, err := NewInventoryRegistry(
		[]Bundle{builtin, staged},
		map[ID]bool{builtin.ID: true},
		map[ID]PluginStanding{staged.ID: stagedStanding},
	)
	if err != nil {
		t.Fatal(err)
	}
	builtinStanding, ok := reg.Standing(builtin.ID)
	if !ok || builtinStanding.Provenance != ProvenanceFirstParty {
		t.Fatalf("executable bundle standing = %#v, %v; want First-Party", builtinStanding, ok)
	}
	gotStagedStanding, ok := reg.Standing(staged.ID)
	if !ok || gotStagedStanding != stagedStanding {
		t.Fatalf("inventory bundle standing = %#v, %v; want %#v", gotStagedStanding, ok, stagedStanding)
	}
}
