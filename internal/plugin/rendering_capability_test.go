package plugin

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

var expectedRenderingCapabilities = []string{
	"accent-style",
	"action-execute",
	"certification-filter",
	"config-date",
	"config-flag",
	"config-number",
	"config-text",
	"config-url",
	"entity-category",
	"entity-container",
	"entity-person",
	"familyscope-pick",
	"field-date",
	"field-flag",
	"field-number",
	"field-text",
	"field-url",
	"importer-configure",
	"layout-browse",
	"layout-detail",
	"layout-shape-collection",
	"layout-shape-container",
	"layout-shape-item",
	"layout-shape-ordered-children",
	"layout-shape-person",
	"mediaclass-subdivide",
	"permission-review",
	"playback-airplay",
	"playback-direct-play",
	"playback-resume",
	"section-filter-bar",
	"section-grid",
	"section-hero",
	"section-info",
	"section-list",
	"section-playback-affordances",
	"section-shelf",
	"sidecar-interactive",
}

func TestRenderingCapabilityVocabularyAndUsedDerivation(t *testing.T) {
	gotVocabulary := RenderingCapabilities()
	if !reflect.DeepEqual(gotVocabulary, expectedRenderingCapabilities) {
		t.Fatalf("RenderingCapabilities() = %v, want %v", gotVocabulary, expectedRenderingCapabilities)
	}
	gotVocabulary[0] = "fabricated"
	if !KnownRenderingCapability(RenderingActionExecute) || KnownRenderingCapability("fabricated") {
		t.Fatal("rendering-capability membership is not closed or defensive")
	}

	bundle := bundleUsingEveryRenderingCapability()
	gotUsed := UsedRenderingCapabilities(bundle)
	if !sort.StringsAreSorted(gotUsed) || !reflect.DeepEqual(gotUsed, expectedRenderingCapabilities) {
		t.Fatalf("UsedRenderingCapabilities() = %v, want %v", gotUsed, expectedRenderingCapabilities)
	}
}

func TestRenderingCapabilityVocabularyCoversClosedDiscriminators(t *testing.T) {
	want := []string{
		"accent-style",
		"action-execute",
		"certification-filter",
		"familyscope-pick",
		"importer-configure",
		"mediaclass-subdivide",
		"permission-review",
		"sidecar-interactive",
	}
	for _, value := range FieldTypes() {
		want = append(want, "config-"+value, "field-"+value)
	}
	for _, value := range Archetypes() {
		want = append(want, "entity-"+value)
	}
	for _, value := range LayoutRoles() {
		want = append(want, "layout-"+value)
	}
	for _, value := range LayoutArchetypes() {
		want = append(want, "layout-shape-"+value)
	}
	for _, value := range PlaybackAffordances() {
		want = append(want, "playback-"+value)
	}
	for _, value := range SectionKinds() {
		want = append(want, "section-"+value)
	}
	sort.Strings(want)
	if got := RenderingCapabilities(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RenderingCapabilities() = %v, mechanically derived closed vocabulary = %v", got, want)
	}
}

func TestUsedRenderingCapabilitiesDerivesNestedDiscriminators(t *testing.T) {
	bundle := Bundle{
		Actions:     []ActionSpec{{InputFields: []ConfigFieldSpec{{Type: "date"}}}},
		EntityKinds: []EntityKind{{Fields: []FieldSpec{{Type: "url"}}}},
		Layouts: []LayoutSpec{{Sections: []SectionSpec{{
			Kind: "grid", PlaybackAffordances: []string{"resume"},
		}}}},
	}
	want := []string{"action-execute", "config-date", "field-url", "playback-resume", "section-grid"}
	if got := UsedRenderingCapabilities(bundle); !reflect.DeepEqual(got, want) {
		t.Fatalf("UsedRenderingCapabilities() = %v, want nested discriminator set %v", got, want)
	}
}

func TestRenderingCapabilityTier1ArtifactPinsCore(t *testing.T) {
	type entry struct {
		ID string `json:"id"`
	}
	type artifact struct {
		SchemaVersion     int     `json:"schemaVersion"`
		VocabularyVersion string  `json:"vocabularyVersion"`
		Capabilities      []entry `json:"capabilities"`
	}
	raw, err := os.ReadFile("../../contracts/rendering-capabilities/v1/vocabulary.json")
	if err != nil {
		t.Fatal(err)
	}
	var got artifact
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 {
		t.Fatalf("artifact schemaVersion = %d, want 1", got.SchemaVersion)
	}
	if got.VocabularyVersion != RenderingCapabilityVocabularyVersion {
		t.Fatalf("artifact vocabularyVersion = %q, core = %q", got.VocabularyVersion, RenderingCapabilityVocabularyVersion)
	}
	ids := make([]string, 0, len(got.Capabilities))
	for _, capability := range got.Capabilities {
		ids = append(ids, capability.ID)
	}
	if !reflect.DeepEqual(ids, RenderingCapabilities()) {
		t.Fatalf("artifact capabilities = %v, core = %v", ids, RenderingCapabilities())
	}
}

func bundleUsingEveryRenderingCapability() Bundle {
	types := []string{"date", "flag", "number", "text", "url"}
	configFields := make([]ConfigFieldSpec, 0, len(types))
	mediaFields := make([]FieldSpec, 0, len(types))
	for _, kind := range types {
		configFields = append(configFields, ConfigFieldSpec{Type: kind})
		mediaFields = append(mediaFields, FieldSpec{Type: kind})
	}
	sections := []SectionSpec{
		{Kind: "filter-bar"},
		{Kind: "grid"},
		{Kind: "hero"},
		{Kind: "info"},
		{Kind: "list"},
		{Kind: "playback-affordances", PlaybackAffordances: []string{"airplay", "direct-play", "resume"}},
		{Kind: "shelf"},
	}
	return Bundle{
		Accent:              "#123456",
		Actions:             []ActionSpec{{}},
		CertificationScale:  []string{"all"},
		ConfigFields:        configFields,
		EntityKinds:         []EntityKind{{Archetype: "category", Fields: mediaFields}, {Archetype: "container"}, {Archetype: "person"}},
		FamilyScopes:        []FamilyScopeSpec{{}},
		Importers:           []ImporterSpec{{}},
		InteractiveSidecars: []string{"ctl"},
		Layouts:             []LayoutSpec{{Role: "browse", Sections: sections}, {Role: "detail", Archetype: "collection"}, {Archetype: "container"}, {Archetype: "item"}, {Archetype: "ordered-children"}, {Archetype: "person"}},
		MediaClassLabels:    map[string]LocaleString{"class": L("Class")},
		MediaFields:         mediaFields,
		Permissions:         []PermissionSpec{{}},
	}
}
