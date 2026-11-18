package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type testHydrationMapper struct{}

func (testHydrationMapper) MarkerField() string { return testHydrationMarkerField }
func (testHydrationMapper) ExternalIDSchemes() []string {
	return []string{testHydrationExternalIDKey}
}
func (testHydrationMapper) MapHydration(tags map[string]string, _ string) HydrationPlan {
	if len(tags) == 0 {
		return HydrationPlan{}
	}
	order := 1002
	artist := HydrationEntity{Kind: "artist", Title: "Artist Quill"}
	album := HydrationEntity{
		Kind:  "album",
		Title: "Lantern Field",
		Fields: []store.EntityField{
			{Name: "release-date", Type: store.EntityFieldDate},
			{Name: "musicbrainz-release-id", Type: store.EntityFieldText, Text: "rel-mbid"},
		},
	}
	return HydrationPlan{
		MarkComplete: true,
		MediaFields: []store.MediaField{
			{Name: "title", Type: store.EntityFieldText, Text: "Copper Weather"},
			{Name: "album", Type: store.EntityFieldText, Text: "Lantern Field"},
			{Name: "track-number", Type: store.EntityFieldNumber, Number: 2},
			{Name: "disc-number", Type: store.EntityFieldNumber, Number: 1},
			{Name: "musicbrainz-recording-id", Type: store.EntityFieldText, Text: "rec-mbid"},
		},
		Relations: []HydrationRelation{
			{From: artist, Name: "appears-on", Attributes: map[string]string{"credited-as": "Artist Quill"}},
			{From: album, Name: "album-track", Cardinality: "one", SortOrder: &order},
			{From: album, Name: "album-artist", ToEntity: &artist},
			{From: HydrationEntity{Kind: "genre", Title: "Alternative Rock"}, Name: "classifies"},
		},
		ExternalIDs: []HydrationExternalID{{Scheme: testHydrationExternalIDKey, Value: "rec-mbid"}},
	}
}

type fixedHydrationMapper struct {
	plan              HydrationPlan
	marker            string
	externalIDSchemes []string
}

func (m fixedHydrationMapper) MarkerField() string { return m.marker }
func (m fixedHydrationMapper) ExternalIDSchemes() []string {
	return append([]string(nil), m.externalIDSchemes...)
}
func (m fixedHydrationMapper) MapHydration(map[string]string, string) HydrationPlan { return m.plan }

const (
	testArchiveHydrationPluginID = "archive"
	testArchiveHydrationMarker   = "archive-hydrated"
)

type testArchiveHydrationMapper struct{}

func (testArchiveHydrationMapper) MarkerField() string { return testArchiveHydrationMarker }
func (testArchiveHydrationMapper) ExternalIDSchemes() []string {
	return nil
}
func (testArchiveHydrationMapper) MapHydration(metadata map[string]string, _ string) HydrationPlan {
	code := metadata["catalog-code"]
	if code == "" {
		return HydrationPlan{}
	}
	return HydrationPlan{
		MarkComplete: true,
		MediaFields: []store.MediaField{{
			Name: "catalog-code",
			Type: store.EntityFieldText,
			Text: code,
		}},
	}
}

func testArchiveHydrationBundle() plugin.Bundle {
	bundle := testBundle(testArchiveHydrationPluginID)
	bundle.MediaFields = []plugin.FieldSpec{
		{Name: "catalog-code", Type: "text", Label: plugin.L("Catalog Code")},
		{Name: testArchiveHydrationMarker, Type: "flag", Label: plugin.L("Hydrated")},
	}
	return bundle
}

// fakeHydrationStore records every write the adapter makes so a test can assert
// the validated, absent-only result without a real Store.
type fakeHydrationStore struct {
	fields    map[store.MediaID][]store.MediaField
	locked    map[string]bool
	lockErr   error
	writes    int
	entities  map[store.EntityID]store.Entity
	relations []store.EntityRelation
	externals []struct {
		media  store.MediaID
		scheme string
		value  string
	}
}

func newFakeHydrationStore() *fakeHydrationStore {
	return &fakeHydrationStore{fields: map[store.MediaID][]store.MediaField{}, entities: map[store.EntityID]store.Entity{}}
}

func (f *fakeHydrationStore) InsertMediaFieldsIfAbsent(_ context.Context, id store.MediaID, fields []store.MediaField) error {
	f.writes++
	// Mimic absent-only: keep the first write per field name.
	have := map[string]bool{}
	for _, existing := range f.fields[id] {
		have[existing.Name] = true
	}
	for _, fld := range fields {
		if !have[fld.Name] {
			f.fields[id] = append(f.fields[id], fld)
			have[fld.Name] = true
		}
	}
	return nil
}

func (f *fakeHydrationStore) UpsertMediaFieldsWithSource(_ context.Context, id store.MediaID, fields []store.MediaField, _ store.MutationSource) error {
	f.writes++
	for _, fld := range fields {
		replaced := false
		for i, existing := range f.fields[id] {
			if existing.Name == fld.Name {
				f.fields[id][i] = fld
				replaced = true
			}
		}
		if !replaced {
			f.fields[id] = append(f.fields[id], fld)
		}
	}
	return nil
}

func (f *fakeHydrationStore) MediaFields(_ context.Context, id store.MediaID, _ store.LibraryFilter) ([]store.MediaField, error) {
	return append([]store.MediaField(nil), f.fields[id]...), nil
}

func (f *fakeHydrationStore) MediaFieldLocks(context.Context, store.MediaID, store.LibraryFilter) ([]store.MediaFieldLock, error) {
	if f.lockErr != nil {
		return nil, f.lockErr
	}
	locks := []store.MediaFieldLock{}
	for field, locked := range f.locked {
		locks = append(locks, store.MediaFieldLock{Field: field, Locked: locked})
	}
	return locks, nil
}

func (f *fakeHydrationStore) UpsertEntity(_ context.Context, entity store.Entity) error {
	f.entities[entity.ID] = entity
	return nil
}

func (f *fakeHydrationStore) UpsertEntityRelation(_ context.Context, relation store.EntityRelation) error {
	f.relations = append(f.relations, relation)
	return nil
}

func (f *fakeHydrationStore) ReplaceMediaRelations(_ context.Context, mediaID store.MediaID, relations []store.EntityRelation) error {
	names := map[string]bool{}
	for _, relation := range relations {
		names[relation.Name] = true
	}
	kept := f.relations[:0]
	for _, relation := range f.relations {
		if relation.ToMediaID != mediaID || !names[relation.Name] {
			kept = append(kept, relation)
		}
	}
	f.relations = append(kept, relations...)
	return nil
}

func (f *fakeHydrationStore) AddMediaExternalID(_ context.Context, mediaID store.MediaID, scheme, value string) error {
	f.externals = append(f.externals, struct {
		media  store.MediaID
		scheme string
		value  string
	}{mediaID, scheme, value})
	return nil
}

func (f *fakeHydrationStore) EntityRelationTargets(_ context.Context, _ store.LibraryID, id store.EntityID, relation string) ([]store.EntityID, bool, error) {
	if _, ok := f.entities[id]; !ok {
		return nil, false, nil
	}
	var targets []store.EntityID
	for _, r := range f.relations {
		if r.FromEntityID == id && r.Name == relation && r.ToEntityID != "" {
			targets = append(targets, r.ToEntityID)
		}
	}
	return targets, true, nil
}

func (f *fakeHydrationStore) field(id store.MediaID, name string) (store.MediaField, bool) {
	for _, fld := range f.fields[id] {
		if fld.Name == name {
			return fld, true
		}
	}
	return store.MediaField{}, false
}

func (f *fakeHydrationStore) relation(name string, to store.MediaID) (store.EntityRelation, bool) {
	for _, r := range f.relations {
		if r.Name == name && r.ToMediaID == to {
			return r, true
		}
	}
	return store.EntityRelation{}, false
}

func newTestAdapter(st hydrationStore) *hydrationAdapter {
	return newTestAdapterWithMapper(st, testHydrationMapper{})
}

func newTestAdapterWithMapper(st hydrationStore, mapper HydrationMapper) *hydrationAdapter {
	registry, err := plugin.NewRegistry([]plugin.Bundle{testHydrationBundle()})
	if err != nil {
		panic(err)
	}
	hydrators := newHydrationAdapters(registry, st, map[string]HydrationMapper{testHydrationPluginID: mapper})
	return hydrators[testHydrationPluginID].(*hydrationAdapter)
}

const (
	testLib   = store.LibraryID("lib-1")
	testMedia = store.MediaID("media-1")
)

func hydrateTestMetadata(adapter *hydrationAdapter, path string, metadata map[string]string) error {
	return adapter.HydrateMetadata(context.Background(), scanpkg.MetadataHydrationInput{
		MediaID:   testMedia,
		LibraryID: testLib,
		Class:     store.MediaClassAudio,
		Path:      path,
		Metadata:  metadata,
	})
}

func TestHydrateMetadataWritesDeclaredModel(t *testing.T) {
	st := newFakeHydrationStore()
	adapter := newTestAdapter(st)
	tags := map[string]string{
		"TITLE": "Copper Weather", "ALBUM": "Lantern Field",
		"TRACK": "2/12", "DISC": "1", "DATE": "2001-02-03",
		"ARTIST": "Artist Quill", "ALBUMARTIST": "Artist Quill", "GENRE": "Alternative Rock",
		"MUSICBRAINZ_TRACKID": "rec-mbid", "MUSICBRAINZ_ALBUMID": "rel-mbid",
	}
	if err := hydrateTestMetadata(adapter, "/x.flac", tags); err != nil {
		t.Fatalf("HydrateMetadata: %v", err)
	}

	// Declared Media fields written.
	for name, want := range map[string]string{"album": "Lantern Field", "musicbrainz-recording-id": "rec-mbid"} {
		if got, ok := st.field(testMedia, name); !ok || got.Text != want {
			t.Errorf("media field %q = (%q, %v), want %q", name, got.Text, ok, want)
		}
	}
	if got, ok := st.field(testMedia, "track-number"); !ok || got.Number != 2 {
		t.Errorf("track-number = %v", got.Number)
	}
	if got, ok := st.field(testMedia, testHydrationMarkerField); !ok || !got.Flag {
		t.Errorf("hydration marker not set")
	}

	// artist/appears-on → Media, with credited-as attribute.
	artistID := identify.EntityIDForName(testLib, "artist", "Artist Quill")
	if _, ok := st.entities[artistID]; !ok {
		t.Fatal("Artist Quill artist entity not upserted")
	}
	if rel, ok := st.relation("appears-on", testMedia); !ok || rel.FromEntityID != artistID || rel.Attributes["credited-as"] != "Artist Quill" {
		t.Errorf("appears-on relation = %+v", rel)
	}

	// album/album-track → Media, one-cardinality, sort order (disc, track).
	albumID := identify.EntityIDForName(testLib, "album", "Lantern Field")
	rel, ok := st.relation("album-track", testMedia)
	if !ok || rel.FromEntityID != albumID || rel.Cardinality != "one" {
		t.Fatalf("album-track relation = %+v", rel)
	}
	if rel.SortOrder == nil || *rel.SortOrder != 1*1000+2 {
		t.Errorf("album-track sort order = %v, want 1002 (disc 1, track 2)", rel.SortOrder)
	}

	// genre/classifies → Media.
	if _, ok := st.relation("classifies", testMedia); !ok {
		t.Error("classifies relation missing")
	}

	// Recording MBID written under the scrobble-adapter scheme (hyphen).
	if len(st.externals) != 1 || st.externals[0].scheme != testHydrationExternalIDKey || st.externals[0].value != "rec-mbid" {
		t.Errorf("external IDs = %+v, want one %q/rec-mbid", st.externals, testHydrationExternalIDKey)
	}
}

func TestHydrationAdaptersSupportNonMusicMediaShape(t *testing.T) {
	registry, err := plugin.NewRegistry([]plugin.Bundle{testHydrationBundle(), testArchiveHydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	st := newFakeHydrationStore()
	adapters := newHydrationAdapters(registry, st, map[string]HydrationMapper{
		testHydrationPluginID:        testHydrationMapper{},
		testArchiveHydrationPluginID: testArchiveHydrationMapper{},
	})
	if len(adapters) != 2 {
		t.Fatalf("hydration adapters = %d, want 2", len(adapters))
	}
	archive := adapters[testArchiveHydrationPluginID]
	capability := archive.Capability()
	if !capability.Supports(store.MediaClassVideo) || capability.Supports(store.MediaClassAudio) {
		t.Fatalf("archive capability = %+v, want video only", capability)
	}
	if err := archive.HydrateMetadata(context.Background(), scanpkg.MetadataHydrationInput{
		MediaID:   testMedia,
		LibraryID: testLib,
		Class:     store.MediaClassAudio,
		Path:      "/archive/entry.flac",
		Metadata:  map[string]string{"catalog-code": "WRONG-SHAPE"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.field(testMedia, "catalog-code"); ok {
		t.Fatal("archive adapter hydrated a Media class outside its capability")
	}
	if err := archive.HydrateMetadata(context.Background(), scanpkg.MetadataHydrationInput{
		MediaID:   testMedia,
		LibraryID: testLib,
		Class:     store.MediaClassVideo,
		Path:      "/archive/entry.mkv",
		Metadata:  map[string]string{"catalog-code": "ARC-42"},
	}); err != nil {
		t.Fatal(err)
	}
	if got, ok := st.field(testMedia, "catalog-code"); !ok || got.Text != "ARC-42" {
		t.Fatalf("catalog-code = (%q, %v), want ARC-42", got.Text, ok)
	}
	if got, ok := st.field(testMedia, testArchiveHydrationMarker); !ok || !got.Flag {
		t.Fatal("archive hydration marker not set")
	}
	if musicCapability := adapters[testHydrationPluginID].Capability(); !musicCapability.Supports(store.MediaClassAudio) || musicCapability.Supports(store.MediaClassVideo) {
		t.Fatalf("music fixture capability = %+v, want audio only", musicCapability)
	}
}

func TestHydrateMetadataWritesDeclaredRelationAttributeTypes(t *testing.T) {
	attributes := map[string]string{
		"credited-as": "Fixture Credit",
		"weight":      "4.5",
		"active":      "true",
		"observed-at": "2026-07-13T12:30:00Z",
		"reference":   "https://example.test/reference",
		"note":        "Fixture note",
	}
	mapper := fixedHydrationMapper{plan: HydrationPlan{Relations: []HydrationRelation{{
		From:       HydrationEntity{Kind: "artist", Title: "Fixture Entity"},
		Name:       "appears-on",
		Attributes: attributes,
	}}}}
	st := newFakeHydrationStore()
	adapter := newTestAdapterWithMapper(st, mapper)
	if err := hydrateTestMetadata(adapter, "/x.bin", nil); err != nil {
		t.Fatalf("HydrateMetadata: %v", err)
	}
	relation, ok := st.relation("appears-on", testMedia)
	if !ok {
		t.Fatal("declared relation was not persisted")
	}
	if len(relation.Attributes) != len(attributes) {
		t.Fatalf("relation attributes = %#v, want %#v", relation.Attributes, attributes)
	}
	for name, want := range attributes {
		if got := relation.Attributes[name]; got != want {
			t.Errorf("relation attribute %q = %q, want %q", name, got, want)
		}
	}
}

func TestHydrateMetadataAbsentOnly(t *testing.T) {
	st := newFakeHydrationStore()
	// A prior user edit on the album field must survive re-hydration.
	st.fields[testMedia] = []store.MediaField{{Name: "album", Type: store.EntityFieldText, Text: "User Edit"}}
	adapter := newTestAdapter(st)
	if err := hydrateTestMetadata(adapter, "/x.flac", map[string]string{"ALBUM": "Tag Album", "TITLE": "t"}); err != nil {
		t.Fatalf("HydrateMetadata: %v", err)
	}
	if got, _ := st.field(testMedia, "album"); got.Text != "User Edit" {
		t.Errorf("album = %q, want the prior user edit preserved (absent-only)", got.Text)
	}
}

func TestHydrateMetadataEmptyInputNoWrites(t *testing.T) {
	st := newFakeHydrationStore()
	adapter := newTestAdapter(st)
	if err := hydrateTestMetadata(adapter, "/x.flac", nil); err != nil {
		t.Fatalf("HydrateMetadata: %v", err)
	}
	if len(st.fields) != 0 || len(st.entities) != 0 || len(st.relations) != 0 || len(st.externals) != 0 {
		t.Fatal("empty metadata must produce no writes (so an untagged file is not marked hydrated)")
	}
}

func TestHydrationPlanMarkCompleteControlsMarker(t *testing.T) {
	dropped := HydrationPlan{
		MediaFields: []store.MediaField{{}},
		Relations:   []HydrationRelation{{}},
		ExternalIDs: []HydrationExternalID{{}},
	}
	for _, tt := range []struct {
		name       string
		plan       HydrationPlan
		wantTitle  bool
		wantMarker bool
	}{
		{name: "false with contribution", plan: HydrationPlan{MediaFields: []store.MediaField{{Name: "title", Type: store.EntityFieldText, Text: "Kept"}}}, wantTitle: true},
		{name: "true with contribution", plan: HydrationPlan{MarkComplete: true, MediaFields: []store.MediaField{{Name: "title", Type: store.EntityFieldText, Text: "Kept"}}}, wantTitle: true, wantMarker: true},
		{name: "false with all contributions dropped", plan: dropped},
		{name: "true with all contributions dropped", plan: HydrationPlan{MarkComplete: true, MediaFields: dropped.MediaFields, Relations: dropped.Relations, ExternalIDs: dropped.ExternalIDs}, wantMarker: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeHydrationStore()
			mapper := fixedHydrationMapper{
				plan:              tt.plan,
				marker:            testHydrationMarkerField,
				externalIDSchemes: []string{testHydrationExternalIDKey},
			}
			adapter := newTestAdapterWithMapper(st, mapper)
			if err := hydrateTestMetadata(adapter, "/x.bin", nil); err != nil {
				t.Fatal(err)
			}
			_, gotTitle := st.field(testMedia, "title")
			if gotTitle != tt.wantTitle {
				t.Errorf("title present = %v, want %v", gotTitle, tt.wantTitle)
			}
			marker, gotMarker := st.field(testMedia, testHydrationMarkerField)
			if gotMarker != tt.wantMarker || gotMarker && !marker.Flag {
				t.Errorf("completion marker = (%v, %+v), want present %v", gotMarker, marker, tt.wantMarker)
			}
			if len(st.entities) != 0 || len(st.relations) != 0 || len(st.externals) != 0 {
				t.Fatal("dropped contributions produced non-field writes")
			}
		})
	}
}

func TestHydrateMetadataRejectsInvalidPlanBeforeAnyWrite(t *testing.T) {
	validPrefix := []store.MediaField{{Name: "title", Type: store.EntityFieldText, Text: "must stay atomic"}}
	attributePlan := func(attributes map[string]string) HydrationPlan {
		return HydrationPlan{MarkComplete: true, MediaFields: validPrefix, Relations: []HydrationRelation{{
			From:       HydrationEntity{Kind: "artist", Title: "Source"},
			Name:       "appears-on",
			Attributes: attributes,
		}}}
	}
	for _, tt := range []struct {
		name string
		plan HydrationPlan
	}{
		{
			name: "undeclared media field",
			plan: HydrationPlan{MarkComplete: true, MediaFields: append(validPrefix,
				store.MediaField{Name: "not-declared", Type: store.EntityFieldText, Text: "bad"})},
		},
		{
			name: "media field type mismatch",
			plan: HydrationPlan{MarkComplete: true, MediaFields: append(validPrefix,
				store.MediaField{Name: "album", Type: store.EntityFieldNumber, Number: 1})},
		},
		{
			name: "undeclared entity field",
			plan: HydrationPlan{MarkComplete: true, MediaFields: validPrefix, Relations: []HydrationRelation{{
				From:        HydrationEntity{Kind: "album", Title: "Container", Fields: []store.EntityField{{Name: "not-declared", Type: store.EntityFieldText}}},
				Name:        "album-track",
				Cardinality: "one",
			}}},
		},
		{
			name: "entity field type mismatch",
			plan: HydrationPlan{MarkComplete: true, MediaFields: validPrefix, Relations: []HydrationRelation{{
				From:        HydrationEntity{Kind: "album", Title: "Container", Fields: []store.EntityField{{Name: "release-date", Type: store.EntityFieldText}}},
				Name:        "album-track",
				Cardinality: "one",
			}}},
		},
		{
			name: "media relation supplied entity target",
			plan: HydrationPlan{MarkComplete: true, MediaFields: validPrefix, Relations: []HydrationRelation{{
				From:     HydrationEntity{Kind: "artist", Title: "Source"},
				Name:     "appears-on",
				ToEntity: &HydrationEntity{Kind: "artist", Title: "Target"},
			}}},
		},
		{
			name: "entity relation supplied media target",
			plan: HydrationPlan{MarkComplete: true, MediaFields: validPrefix, Relations: []HydrationRelation{{
				From: HydrationEntity{Kind: "album", Title: "Container"},
				Name: "album-artist",
			}}},
		},
		{
			name: "entity relation target kind mismatch",
			plan: HydrationPlan{MarkComplete: true, MediaFields: validPrefix, Relations: []HydrationRelation{{
				From:     HydrationEntity{Kind: "album", Title: "Container"},
				Name:     "album-artist",
				ToEntity: &HydrationEntity{Kind: "genre", Title: "Wrong Kind"},
			}}},
		},
		{
			name: "relation cardinality mismatch",
			plan: HydrationPlan{MarkComplete: true, MediaFields: validPrefix, Relations: []HydrationRelation{{
				From:        HydrationEntity{Kind: "album", Title: "Container"},
				Name:        "album-track",
				Cardinality: "many",
			}}},
		},
		{
			name: "declared one cardinality omitted",
			plan: HydrationPlan{MarkComplete: true, MediaFields: validPrefix, Relations: []HydrationRelation{{
				From: HydrationEntity{Kind: "album", Title: "Container"},
				Name: "album-track",
			}}},
		},
		{name: "undeclared relation attribute", plan: attributePlan(map[string]string{"not-declared": "value"})},
		{name: "relation number attribute is not finite", plan: attributePlan(map[string]string{"weight": "NaN"})},
		{name: "relation flag attribute is malformed", plan: attributePlan(map[string]string{"active": "sometimes"})},
		{name: "relation date attribute is malformed", plan: attributePlan(map[string]string{"observed-at": "2026-99-99"})},
		{name: "relation URL attribute is malformed", plan: attributePlan(map[string]string{"reference": "://not-a-url"})},
		{name: "relation URL attribute has no authority", plan: attributePlan(map[string]string{"reference": "https://"})},
		{name: "relation URL attribute is relative", plan: attributePlan(map[string]string{"reference": "/not-absolute"})},
		{name: "relation typed text attribute is blank", plan: attributePlan(map[string]string{"note": "  "})},
		{
			name: "undeclared external ID scheme",
			plan: HydrationPlan{MarkComplete: true, MediaFields: validPrefix,
				ExternalIDs: []HydrationExternalID{{Scheme: "not-declared", Value: "bad"}}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeHydrationStore()
			mapper := fixedHydrationMapper{
				plan:              tt.plan,
				marker:            testHydrationMarkerField,
				externalIDSchemes: []string{testHydrationExternalIDKey},
			}
			adapter := newTestAdapterWithMapper(st, mapper)
			if err := hydrateTestMetadata(adapter, "/x.bin", nil); err == nil {
				t.Fatal("HydrateMetadata accepted invalid plan")
			}
			assertNoHydrationWrites(t, st)
		})
	}
}

func assertNoHydrationWrites(t *testing.T, st *fakeHydrationStore) {
	t.Helper()
	if len(st.fields) != 0 || len(st.entities) != 0 || len(st.relations) != 0 || len(st.externals) != 0 {
		t.Fatalf("invalid plan wrote state: fields=%+v entities=%+v relations=%+v externalIDs=%+v", st.fields, st.entities, st.relations, st.externals)
	}
}

func TestScrobbleSelectorsFromBundle(t *testing.T) {
	registry, err := plugin.NewRegistry([]plugin.Bundle{testHydrationBundle()})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	sel := scrobbleRelationSelectors(registry)
	if len(sel.ArtistConfirm) != 1 || sel.ArtistConfirm[0] != "appears-on" {
		t.Errorf("ArtistConfirm = %v, want [appears-on]", sel.ArtistConfirm)
	}
	if len(sel.AlbumTieBreak) != 1 || sel.AlbumTieBreak[0] != "album-track" {
		t.Errorf("AlbumTieBreak = %v, want [album-track]", sel.AlbumTieBreak)
	}
	// Selectors must be the bare, unqualified slugs EntitiesForMedia matches.
	for _, name := range append(sel.ArtistConfirm, sel.AlbumTieBreak...) {
		for _, r := range name {
			if r == '/' {
				t.Errorf("selector %q must be unqualified (no kind/ prefix)", name)
			}
		}
	}
}

// TestHydrateMetadataRejectsUndeclaredMarkerAndRelationNames covers the plan
// checks that depend on the completion marker or on relation names rather than
// on a field's shape: the marker stays under MarkComplete's control, relations
// and their prerequisites must be declared, and MarkComplete needs a declared
// flag marker.
func TestHydrateMetadataRejectsUndeclaredMarkerAndRelationNames(t *testing.T) {
	album := HydrationEntity{Kind: "album", Title: "Container"}
	for _, tt := range []struct {
		name   string
		marker string
		plan   HydrationPlan
		want   string
	}{
		{
			name:   "media field writes the completion marker",
			marker: testHydrationMarkerField,
			plan: HydrationPlan{MediaFields: []store.MediaField{
				{Name: testHydrationMarkerField, Type: store.EntityFieldFlag, Flag: true},
			}},
			want: "must be controlled by MarkComplete",
		},
		{
			name:   "undeclared relation name",
			marker: testHydrationMarkerField,
			plan:   HydrationPlan{Relations: []HydrationRelation{{From: album, Name: "not-declared"}}},
			want:   "relation \"not-declared\" from entity kind \"album\" is not declared",
		},
		{
			name:   "undeclared prerequisite relation",
			marker: testHydrationMarkerField,
			plan: HydrationPlan{Relations: []HydrationRelation{{
				From: album, Name: "album-track", Cardinality: "one", RequiresRelation: "not-declared",
			}}},
			want: "prerequisite relation \"not-declared\"",
		},
		{
			name:   "scoped entity without a declared scope relation",
			marker: testHydrationMarkerField,
			plan: HydrationPlan{Relations: []HydrationRelation{{
				From: HydrationEntity{Kind: "album", Title: "Record", Scope: "Somebody"}, Name: "album-track", Cardinality: "one",
			}}},
			want: "declares no scope relation",
		},
		{
			name:   "completion marker not declared",
			marker: "not-declared",
			plan:   HydrationPlan{MarkComplete: true, MediaFields: []store.MediaField{{Name: "title", Type: store.EntityFieldText, Text: "x"}}},
			want:   "completion marker \"not-declared\" is not declared",
		},
		{
			name:   "completion marker is not a flag",
			marker: "album",
			plan:   HydrationPlan{MarkComplete: true, MediaFields: []store.MediaField{{Name: "title", Type: store.EntityFieldText, Text: "x"}}},
			want:   "want \"flag\"",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeHydrationStore()
			adapter := newTestAdapterWithMapper(st, fixedHydrationMapper{plan: tt.plan, marker: tt.marker})
			err := hydrateTestMetadata(adapter, "/x.bin", nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("HydrateMetadata = %v, want error containing %q", err, tt.want)
			}
			assertNoHydrationWrites(t, st)
		})
	}
}

// scopedAlbumMapper maps a track to an album scoped by its album artist, the
// way a plugin that scopes a container kind does: the scope rides the Entity
// and the declared scope relation names the Entity it is scoped by.
type scopedAlbumMapper struct{}

func (scopedAlbumMapper) MarkerField() string         { return testHydrationMarkerField }
func (scopedAlbumMapper) ExternalIDSchemes() []string { return nil }
func (scopedAlbumMapper) EntityScopes() []HydrationEntityScope {
	return []HydrationEntityScope{{Kind: "album", Relation: "album-artist"}}
}
func (scopedAlbumMapper) MapHydration(metadata map[string]string, _ string) HydrationPlan {
	album := HydrationEntity{Kind: "album", Title: metadata["album"], Scope: metadata["album-artist"]}
	return HydrationPlan{Relations: []HydrationRelation{
		{From: album, Name: "album-track", Cardinality: "one"},
		{From: album, Name: "album-artist", RequiresRelation: "album-track",
			ToEntity: &HydrationEntity{Kind: "artist", Title: metadata["album-artist"]}},
	}}
}

func hydrateScopedTrack(t *testing.T, st *fakeHydrationStore, media store.MediaID, album, albumArtist string) store.EntityID {
	t.Helper()
	adapter := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	err := adapter.HydrateMetadata(context.Background(), scanpkg.MetadataHydrationInput{
		MediaID: media, LibraryID: testLib, Class: store.MediaClassAudio, Path: "/x.bin",
		Metadata: map[string]string{"album": album, "album-artist": albumArtist},
	})
	if err != nil {
		t.Fatalf("HydrateMetadata(%s): %v", media, err)
	}
	var owner store.EntityID
	for _, r := range st.relations {
		if r.Name == "album-track" && r.ToMediaID == media {
			owner = r.FromEntityID
		}
	}
	if owner == "" {
		t.Fatalf("no album-track relation for %s", media)
	}
	return owner
}

// TestHydrationSeparatesSameTitledEntitiesByScope pins that the scope, not the
// title alone, is the identity: the same title under two scopes is two
// Entities, each pointing at its own scope Entity, and the same title under one
// scope is one Entity however many Media carry it.
func TestHydrationSeparatesSameTitledEntitiesByScope(t *testing.T) {
	st := newFakeHydrationStore()
	first := hydrateScopedTrack(t, st, "media-1", "Shared Collection", "First Band")
	second := hydrateScopedTrack(t, st, "media-2", "Shared Collection", "Second Band")
	again := hydrateScopedTrack(t, st, "media-3", "Shared Collection", "First Band")
	if first == second {
		t.Fatalf("two scopes share one entity %s", first)
	}
	if again != first {
		t.Fatalf("same scope hydrated to %s then %s", first, again)
	}
	for id, artist := range map[store.EntityID]string{first: "First Band", second: "Second Band"} {
		if got := st.entities[id].Title; got != "Shared Collection" {
			t.Errorf("entity %s title = %q, want the plain title", id, got)
		}
		targets, _, _ := st.EntityRelationTargets(context.Background(), testLib, id, "album-artist")
		want := identify.EntityIDForName(testLib, "artist", artist)
		if len(targets) == 0 {
			t.Fatalf("entity %s records no scope", id)
		}
		for _, target := range targets {
			if target != want {
				t.Errorf("entity %s scope target = %s, want %s (%s)", id, target, want, artist)
			}
		}
	}
}

// TestHydrationAdoptsUnscopedEntityForItsOnlyScope pins the upgrade path: an
// Entity written under the title-only id keeps that id (so nothing is orphaned
// or duplicated on rescan) when it records no scope yet or only this one.
func TestHydrationAdoptsUnscopedEntityForItsOnlyScope(t *testing.T) {
	legacy := identify.EntityIDForName(testLib, "album", "Shared Collection")
	artist := identify.EntityIDForName(testLib, "artist", "First Band")
	for name, relations := range map[string][]store.EntityRelation{
		"no scope recorded":   nil,
		"this scope recorded": {{FromEntityID: legacy, Name: "album-artist", ToEntityID: artist}},
	} {
		t.Run(name, func(t *testing.T) {
			st := newFakeHydrationStore()
			st.entities[legacy] = store.Entity{ID: legacy, LibraryID: testLib, Kind: "album", Title: "Shared Collection"}
			st.relations = relations
			if got := hydrateScopedTrack(t, st, "media-1", "Shared Collection", "First Band"); got != legacy {
				t.Fatalf("hydrated to %s, want the existing entity %s", got, legacy)
			}
			if got := hydrateScopedTrack(t, st, "media-2", "Shared Collection", "First Band"); got != legacy {
				t.Fatalf("second track hydrated to %s, want the existing entity %s", got, legacy)
			}
			if len(st.entities) != 2 {
				t.Fatalf("entities = %d, want the adopted album and its artist: %+v", len(st.entities), st.entities)
			}
		})
	}
}

// TestHydrationGivesATitleOnlyEntityToOneScopeOfAPlan pins a plan that carries
// two same-titled Entities of different scopes: the plan's relations are
// written only after every id resolves, so the title-only Entity, which
// records no scope yet, goes to the first scope and the second gets its own.
func TestHydrationGivesATitleOnlyEntityToOneScopeOfAPlan(t *testing.T) {
	st := newFakeHydrationStore()
	legacy := identify.EntityIDForName(testLib, "album", "Shared Collection")
	st.entities[legacy] = store.Entity{ID: legacy, LibraryID: testLib, Kind: "album", Title: "Shared Collection"}
	adapter := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	var relations []HydrationRelation
	for _, artist := range []string{"First Band", "Second Band"} {
		album := HydrationEntity{Kind: "album", Title: "Shared Collection", Scope: artist}
		relations = append(relations,
			HydrationRelation{From: album, Name: "album-track"},
			HydrationRelation{From: album, Name: "album-artist", RequiresRelation: "album-track",
				ToEntity: &HydrationEntity{Kind: "artist", Title: artist}})
	}
	if err := adapter.writeRelations(context.Background(), "media-1", testLib, relations, true); err != nil {
		t.Fatal(err)
	}
	targets, _, _ := st.EntityRelationTargets(context.Background(), testLib, legacy, "album-artist")
	if first := identify.EntityIDForName(testLib, "artist", "First Band"); len(targets) != 1 || targets[0] != first {
		t.Fatalf("title-only entity's scopes = %v, want %s alone", targets, first)
	}
	scoped := identify.EntityIDForScopedName(testLib, "album", "Second Band", "Shared Collection")
	if _, ok := st.entities[scoped]; !ok {
		t.Fatalf("second scope's entity %s missing: %+v", scoped, st.entities)
	}
}

// TestHydrationKeepsUnscopedEntityOfAnotherScope pins the other half: a
// title-only Entity that records a different scope is somebody else's, so the
// scope being hydrated gets its own Entity and the existing one is untouched.
func TestHydrationKeepsUnscopedEntityOfAnotherScope(t *testing.T) {
	st := newFakeHydrationStore()
	legacy := identify.EntityIDForName(testLib, "album", "Shared Collection")
	other := identify.EntityIDForName(testLib, "artist", "First Band")
	st.entities[legacy] = store.Entity{ID: legacy, LibraryID: testLib, Kind: "album", Title: "Shared Collection"}
	st.relations = []store.EntityRelation{{FromEntityID: legacy, Name: "album-artist", ToEntityID: other}}

	got := hydrateScopedTrack(t, st, "media-1", "Shared Collection", "Second Band")
	if got == legacy {
		t.Fatal("a second scope adopted another scope's entity")
	}
	if want := identify.EntityIDForScopedName(testLib, "album", "Second Band", "Shared Collection"); got != want {
		t.Fatalf("hydrated to %s, want the scoped id %s", got, want)
	}
	targets, _, _ := st.EntityRelationTargets(context.Background(), testLib, legacy, "album-artist")
	if len(targets) != 1 || targets[0] != other {
		t.Fatalf("existing entity's scope changed: %v", targets)
	}
}

// TestHydrationSplitsMergedEntityAfterScopeRepair runs the whole upgrade on a
// real Store: two scopes' Media sit under one title-only Entity (how hydration
// wrote them before the kind was scoped), the one-time repair reopens it, and
// the next hydration leaves the first scope on the existing Entity and moves
// the second scope's Media to its own, with no third Entity and no Media left
// behind.
func TestHydrationSplitsMergedEntityAfterScopeRepair(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib, err := st.CreateLibrary(ctx, "Shelf", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	track := func(name string) store.MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	tracks := map[store.MediaID]string{track("one"): "First Band", track("two"): "Second Band"}

	// Before scoping: both tracks land on the title-only Entity.
	legacy := identify.EntityIDForName(lib.ID, "album", "Shared Collection")
	if err := st.UpsertEntity(ctx, store.Entity{ID: legacy, LibraryID: lib.ID, Kind: "album", Title: "Shared Collection"}); err != nil {
		t.Fatal(err)
	}
	for media, artist := range tracks {
		artistID := identify.EntityIDForName(lib.ID, "artist", artist)
		if err := st.UpsertEntity(ctx, store.Entity{ID: artistID, LibraryID: lib.ID, Kind: "artist", Title: artist}); err != nil {
			t.Fatal(err)
		}
		for _, relation := range []store.EntityRelation{
			{FromEntityID: legacy, Name: "album-track", ToMediaID: media, Cardinality: "one"},
			{FromEntityID: legacy, Name: "album-artist", ToEntityID: artistID},
		} {
			if err := st.UpsertEntityRelation(ctx, relation); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.SetMediaFields(ctx, media, []store.MediaField{{Name: testHydrationMarkerField, Type: store.EntityFieldFlag, Flag: true}}); err != nil {
			t.Fatal(err)
		}
	}

	adapter := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	hydrators := map[string]scanpkg.MetadataHydrator{testHydrationPluginID: adapter}
	mappers := map[string]HydrationMapper{testHydrationPluginID: scopedAlbumMapper{}}
	splitAmbiguousHydrationScopes(ctx, st, hydrators, mappers, slog.New(slog.DiscardHandler))
	stale, err := st.MediaLackingFieldInLibrary(ctx, lib.ID, store.MediaClassAudio, testHydrationMarkerField)
	if err != nil || len(stale) != len(tracks) {
		t.Fatalf("stale after repair = %v, %v; want both tracks requeued", stale, err)
	}
	for media := range stale {
		err := adapter.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: media, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "album-artist": tracks[media]},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	albums, err := st.EntitiesByKind(ctx, lib.ID, "album", store.LibraryFilter{AllLibraries: true}, store.EntityKindOptions{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 2 {
		t.Fatalf("albums = %d, want one per scope: %+v", len(albums), albums)
	}
	kept := false
	seen := map[store.EntityID]bool{}
	for _, album := range albums {
		kept = kept || album.ID == legacy
		members, err := st.MediaForEntity(ctx, album.ID, store.LibraryFilter{AllLibraries: true})
		if err != nil || len(members) != 1 {
			t.Fatalf("album %s members = %d, %v; want its one track", album.ID, len(members), err)
		}
		targets, _, err := st.EntityRelationTargets(ctx, lib.ID, album.ID, "album-artist")
		want := identify.EntityIDForName(lib.ID, "artist", tracks[members[0].ID])
		if err != nil || len(targets) != 1 || targets[0] != want {
			t.Fatalf("album %s scope = %v, %v; want only its track's artist %s", album.ID, targets, err, want)
		}
		seen[targets[0]] = true
	}
	if !kept {
		t.Fatalf("the existing entity %s was replaced: %+v", legacy, albums)
	}
	if len(seen) != 2 {
		t.Fatalf("both albums ended under one scope: %v", seen)
	}
}

// refusingEnqueueStore fails every job enqueue, the way a full or unavailable
// jobs database would.
type refusingEnqueueStore struct{ *store.Store }

func (refusingEnqueueStore) EnqueueJob(context.Context, store.Job) (store.Job, error) {
	return store.Job{}, errors.New("jobs unavailable")
}

// TestScopeRepairQueuesBackfillScan pins that the repair's reopened Media are
// actually hydrated again: an ordinary scan skips unchanged files, so the
// repair queues a backfill scan of every root of the Library, and the pending
// mark survives a failed enqueue so the next start queues it instead.
func TestScopeRepairQueuesBackfillScan(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	lib, err := st.CreateLibraryWithRoots(ctx, "Shelf", false, testHydrationPluginID, "profile-default", []string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: lib.ID, Path: root + "/one.flac", Title: "one", Container: "flac", Codec: "flac",
		Class: store.MediaClassAudio, ContentFingerprint: "fp-one",
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := identify.EntityIDForName(lib.ID, "album", "Shared Collection")
	if err := st.UpsertEntity(ctx, store.Entity{ID: legacy, LibraryID: lib.ID, Kind: "album", Title: "Shared Collection"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: legacy, Name: "album-track", ToMediaID: media.ID, Cardinality: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, media.ID, []store.MediaField{{Name: testHydrationMarkerField, Type: store.EntityFieldFlag, Flag: true}}); err != nil {
		t.Fatal(err)
	}
	hydrators := map[string]scanpkg.MetadataHydrator{testHydrationPluginID: newTestAdapterWithMapper(st, scopedAlbumMapper{})}
	mappers := map[string]HydrationMapper{testHydrationPluginID: scopedAlbumMapper{}}
	scanJobs := func() []store.Job {
		t.Helper()
		jobs, err := st.ListJobs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []store.Job
		for _, job := range jobs {
			if job.Kind == scanpkg.JobKind {
				out = append(out, job)
			}
		}
		return out
	}

	splitAmbiguousHydrationScopes(ctx, refusingEnqueueStore{st}, hydrators, mappers, slog.New(slog.DiscardHandler))
	if got := scanJobs(); len(got) != 0 {
		t.Fatalf("scan jobs after a refused enqueue = %+v, want none", got)
	}
	if pending, err := st.PendingEntityScopeBackfills(ctx); err != nil || len(pending) != 1 || pending[0] != lib.ID {
		t.Fatalf("pending after a refused enqueue = %v, %v; want %s kept", pending, err, lib.ID)
	}

	splitAmbiguousHydrationScopes(ctx, st, hydrators, mappers, slog.New(slog.DiscardHandler))
	got := scanJobs()
	if len(got) != 1 || !strings.HasSuffix(got[0].DedupeKey, ":backfill") || !strings.Contains(got[0].DedupeKey, string(lib.ID)) {
		t.Fatalf("scan jobs = %+v, want one backfill scan of %s", got, lib.ID)
	}
	if pending, err := st.PendingEntityScopeBackfills(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending after queueing = %v, %v; want none", pending, err)
	}
}

// rendezvousHydrationStore serialises a fakeHydrationStore for concurrent use
// and holds the first two reads of one Entity's scope until both have been
// read (or a short wait passes), so two hydrations that are not serialised
// read the same state before either writes.
type rendezvousHydrationStore struct {
	mu      sync.Mutex
	inner   *fakeHydrationStore
	watched store.EntityID
	arrived int
	both    chan struct{}
}

func (r *rendezvousHydrationStore) InsertMediaFieldsIfAbsent(ctx context.Context, id store.MediaID, fields []store.MediaField) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.InsertMediaFieldsIfAbsent(ctx, id, fields)
}

func (r *rendezvousHydrationStore) UpsertMediaFieldsWithSource(ctx context.Context, id store.MediaID, fields []store.MediaField, source store.MutationSource) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.UpsertMediaFieldsWithSource(ctx, id, fields, source)
}

func (r *rendezvousHydrationStore) MediaFields(ctx context.Context, id store.MediaID, filter store.LibraryFilter) ([]store.MediaField, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.MediaFields(ctx, id, filter)
}

func (r *rendezvousHydrationStore) MediaFieldLocks(ctx context.Context, id store.MediaID, filter store.LibraryFilter) ([]store.MediaFieldLock, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.MediaFieldLocks(ctx, id, filter)
}

func (r *rendezvousHydrationStore) UpsertEntity(ctx context.Context, entity store.Entity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.UpsertEntity(ctx, entity)
}

func (r *rendezvousHydrationStore) UpsertEntityRelation(ctx context.Context, relation store.EntityRelation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.UpsertEntityRelation(ctx, relation)
}

func (r *rendezvousHydrationStore) ReplaceMediaRelations(ctx context.Context, mediaID store.MediaID, relations []store.EntityRelation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.ReplaceMediaRelations(ctx, mediaID, relations)
}

func (r *rendezvousHydrationStore) AddMediaExternalID(ctx context.Context, mediaID store.MediaID, scheme, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.AddMediaExternalID(ctx, mediaID, scheme, value)
}

func (r *rendezvousHydrationStore) EntityRelationTargets(ctx context.Context, libraryID store.LibraryID, id store.EntityID, relation string) ([]store.EntityID, bool, error) {
	r.mu.Lock()
	targets, exists, err := r.inner.EntityRelationTargets(ctx, libraryID, id, relation)
	watched := id == r.watched
	if watched {
		r.arrived++
		if r.arrived == 2 {
			close(r.both)
		}
	}
	r.mu.Unlock()
	if watched {
		select {
		case <-r.both:
		case <-time.After(200 * time.Millisecond):
		}
	}
	return targets, exists, err
}

// TestConcurrentHydrationAdoptsTitleOnlyEntityOnce pins that two scans
// hydrating one Library at once cannot both adopt one title-only Entity for
// different scopes: one adopts it, the other gets its own scoped Entity, and
// every track's album records only that track's album artist.
func TestConcurrentHydrationAdoptsTitleOnlyEntityOnce(t *testing.T) {
	inner := newFakeHydrationStore()
	legacy := identify.EntityIDForName(testLib, "album", "Shared Collection")
	inner.entities[legacy] = store.Entity{ID: legacy, LibraryID: testLib, Kind: "album", Title: "Shared Collection"}
	st := &rendezvousHydrationStore{inner: inner, watched: legacy, both: make(chan struct{})}
	adapter := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	tracks := map[store.MediaID]string{"media-1": "First Band", "media-2": "Second Band"}

	var wg sync.WaitGroup
	errs := make(chan error, len(tracks))
	for media, artist := range tracks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- adapter.HydrateMetadata(context.Background(), scanpkg.MetadataHydrationInput{
				MediaID: media, LibraryID: testLib, Class: store.MediaClassAudio, Path: "/x.bin",
				Metadata: map[string]string{"album": "Shared Collection", "album-artist": artist},
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	albums := map[store.EntityID]bool{}
	for media, artist := range tracks {
		owner, ok := inner.relation("album-track", media)
		if !ok {
			t.Fatalf("no album-track relation for %s", media)
		}
		albums[owner.FromEntityID] = true
		targets, _, _ := inner.EntityRelationTargets(context.Background(), testLib, owner.FromEntityID, "album-artist")
		want := identify.EntityIDForName(testLib, "artist", artist)
		if len(targets) != 1 || targets[0] != want {
			t.Fatalf("%s album %s scope = %v, want only %s (%s)", media, owner.FromEntityID, targets, want, artist)
		}
	}
	if len(albums) != 2 || !albums[legacy] {
		t.Fatalf("albums = %v, want the adopted %s and one scoped album", albums, legacy)
	}
}

const testHydrationFingerprintField = "fixture-hydrated-fingerprint"

// rehydratingTestMapper maps the title and album tags and opts in to
// fingerprint-keyed hydration with the same rule the plugin side ships.
type rehydratingTestMapper struct{}

func (rehydratingTestMapper) MarkerField() string         { return testHydrationMarkerField }
func (rehydratingTestMapper) ExternalIDSchemes() []string { return nil }
func (rehydratingTestMapper) FingerprintField() string    { return testHydrationFingerprintField }
func (rehydratingTestMapper) MapHydration(tags map[string]string, _ string) HydrationPlan {
	plan := HydrationPlan{}
	if tags["title"] != "" {
		plan.MarkComplete = true
		plan.MediaFields = append(plan.MediaFields, store.MediaField{Name: "title", Type: store.EntityFieldText, Text: tags["title"]})
	}
	if tags["album"] != "" {
		plan.MarkComplete = true
		plan.MediaFields = append(plan.MediaFields, store.MediaField{Name: "album", Type: store.EntityFieldText, Text: tags["album"]})
	}
	// "contributors" maps to the many-cardinality credit relation, one row per
	// comma-separated name; "category" maps to a second, independent kind.
	if tags["contributors"] != "" {
		for _, name := range strings.Split(tags["contributors"], ",") {
			plan.Relations = append(plan.Relations, HydrationRelation{From: HydrationEntity{Kind: "artist", Title: name}, Name: "appears-on"})
		}
	}
	if tags["category"] != "" {
		plan.Relations = append(plan.Relations, HydrationRelation{From: HydrationEntity{Kind: "genre", Title: tags["category"]}, Name: "classifies"})
	}
	return plan
}
func (rehydratingTestMapper) DecideHydrationWrite(recorded, current string) HydrationWrite {
	switch {
	case recorded == "":
		return HydrationWriteFresh
	case current == "" || recorded == current:
		return HydrationWriteSkip
	default:
		return HydrationWriteReplace
	}
}

func rehydrationBundle() plugin.Bundle {
	bundle := testHydrationBundle()
	bundle.MediaFields = append(bundle.MediaFields, plugin.FieldSpec{Name: testHydrationFingerprintField, Type: "text", Label: plugin.L("Hydrated From")})
	return bundle
}

func newRehydrationAdapter(t *testing.T, st hydrationStore) *hydrationAdapter {
	t.Helper()
	registry, err := plugin.NewRegistry([]plugin.Bundle{rehydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	hydrators := newHydrationAdapters(registry, st, map[string]HydrationMapper{testHydrationPluginID: rehydratingTestMapper{}})
	return hydrators[testHydrationPluginID].(*hydrationAdapter)
}

func hydrateWithFingerprint(t *testing.T, adapter *hydrationAdapter, fingerprint string, tags map[string]string) {
	t.Helper()
	if err := adapter.HydrateMetadata(context.Background(), scanpkg.MetadataHydrationInput{
		MediaID: testMedia, LibraryID: testLib, Class: store.MediaClassAudio,
		Path: "/x.flac", Metadata: tags, ContentFingerprint: fingerprint,
	}); err != nil {
		t.Fatalf("HydrateMetadata(%q): %v", fingerprint, err)
	}
}

// recordedFingerprint strips the plan digest hydrationKey appends, leaving
// the content fingerprint the recorded key was built from.
func recordedFingerprint(key string) string {
	fingerprint, _, _ := strings.Cut(key, ";plan=")
	return fingerprint
}

func TestHydrateMetadataKeyedOnContentFingerprint(t *testing.T) {
	st := newFakeHydrationStore()
	adapter := newRehydrationAdapter(t, st)
	text := func(name string) string {
		field, _ := st.field(testMedia, name)
		return field.Text
	}

	hydrateWithFingerprint(t, adapter, "fp-1", map[string]string{"title": "One", "album": "First"})
	if text("title") != "One" || text("album") != "First" || recordedFingerprint(text(testHydrationFingerprintField)) != "fp-1" {
		t.Fatalf("fresh hydration fields = %+v", st.fields[testMedia])
	}

	writes := st.writes
	hydrateWithFingerprint(t, adapter, "fp-1", map[string]string{"title": "One", "album": "First"})
	if st.writes != writes || text("title") != "One" {
		t.Fatalf("same fingerprint and plan wrote %d times, title %q; want no write", st.writes-writes, text("title"))
	}

	hydrateWithFingerprint(t, adapter, "fp-2", map[string]string{"title": "Two", "album": "Second"})
	if text("title") != "Two" || text("album") != "Second" || recordedFingerprint(text(testHydrationFingerprintField)) != "fp-2" {
		t.Fatalf("replace hydration fields = %+v", st.fields[testMedia])
	}

	st.locked = map[string]bool{"title": true}
	hydrateWithFingerprint(t, adapter, "fp-3", map[string]string{"title": "Three", "album": "Third"})
	if text("title") != "Two" || text("album") != "Third" {
		t.Fatalf("locked replace: title %q album %q, want locked title kept and album replaced", text("title"), text("album"))
	}

	// Unreadable locks fail closed: no field is replaced and the recorded
	// fingerprint stays, so a later scan retries the replace.
	st.locked, st.lockErr = nil, errors.New("locks unreadable")
	hydrateWithFingerprint(t, adapter, "fp-4", map[string]string{"title": "Four", "album": "Fourth"})
	if text("title") != "Two" || text("album") != "Third" || recordedFingerprint(text(testHydrationFingerprintField)) != "fp-3" {
		t.Fatalf("lock-read failure: title %q album %q fingerprint %q, want nothing replaced", text("title"), text("album"), text(testHydrationFingerprintField))
	}
}

func TestHydrateMetadataRejectsUndeclaredFingerprintField(t *testing.T) {
	registry, err := plugin.NewRegistry([]plugin.Bundle{testHydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	st := newFakeHydrationStore()
	adapter := newHydrationAdapters(registry, st, map[string]HydrationMapper{testHydrationPluginID: rehydratingTestMapper{}})[testHydrationPluginID].(*hydrationAdapter)
	err = adapter.HydrateMetadata(context.Background(), scanpkg.MetadataHydrationInput{
		MediaID: testMedia, LibraryID: testLib, Class: store.MediaClassAudio,
		Metadata: map[string]string{"title": "One"}, ContentFingerprint: "fp-1",
	})
	if err == nil || !strings.Contains(err.Error(), testHydrationFingerprintField) {
		t.Fatalf("HydrateMetadata error = %v, want undeclared fingerprint field rejected", err)
	}
	assertNoHydrationWrites(t, st)
}

// countingHydrationStore counts the hydration seam's Media field writes
// against a real Store.
type countingHydrationStore struct {
	*store.Store
	writes int
}

func (c *countingHydrationStore) InsertMediaFieldsIfAbsent(ctx context.Context, id store.MediaID, fields []store.MediaField) error {
	c.writes++
	return c.Store.InsertMediaFieldsIfAbsent(ctx, id, fields)
}

func (c *countingHydrationStore) UpsertMediaFieldsWithSource(ctx context.Context, id store.MediaID, fields []store.MediaField, source store.MutationSource) error {
	c.writes++
	return c.Store.UpsertMediaFieldsWithSource(ctx, id, fields, source)
}

type tagProber struct{ tags *map[string]string }

func (p tagProber) Probe(context.Context, string) (ffmpeg.Probe, error) {
	return ffmpeg.Probe{Container: "flac", AudioCodec: "flac", DurationSeconds: 60, Tags: *p.tags}, nil
}

// rehydrationScan drives the real scan and hydration seam over one audio
// file whose bytes, mtime and probe tags a test can rewrite between scans.
type rehydrationScan struct {
	t       *testing.T
	st      *store.Store
	counted *countingHydrationStore
	adapter *hydrationAdapter
	library store.LibraryID
	root    string
	path    string
	tags    map[string]string
	mediaID store.MediaID
}

func newRehydrationScan(t *testing.T) *rehydrationScan {
	t.Helper()
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Library", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	counted := &countingHydrationStore{Store: st}
	root := t.TempDir()
	return &rehydrationScan{t: t, st: st, counted: counted, adapter: newRehydrationAdapter(t, counted),
		library: library.ID, root: root, path: filepath.Join(root, "01 track.flac")}
}

// write sets the file's bytes and an mtime age past the stability window.
func (r *rehydrationScan) write(data string, age time.Duration, tags map[string]string) {
	r.t.Helper()
	if err := os.WriteFile(r.path, []byte(data), 0o600); err != nil {
		r.t.Fatal(err)
	}
	r.touch(age)
	r.tags = tags
}

func (r *rehydrationScan) touch(age time.Duration) {
	r.t.Helper()
	mtime := time.Now().Add(-age)
	if err := os.Chtimes(r.path, mtime, mtime); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rehydrationScan) scan() {
	r.t.Helper()
	result, err := scanpkg.ScanWithMetadataHydration(context.Background(), r.st, tagProber{tags: &r.tags}, nil, nil, nil, nil, nil,
		map[string]scanpkg.MetadataHydrator{testHydrationPluginID: r.adapter}, scanpkg.Request{LibraryID: r.library, Root: r.root})
	if err != nil {
		r.t.Fatalf("scan: %v", err)
	}
	if len(result.Files) != 1 {
		r.t.Fatalf("scan files = %d, want 1", len(result.Files))
	}
	r.mediaID = result.Files[0].MediaID
}

func (r *rehydrationScan) field(name string) string {
	r.t.Helper()
	fields, err := r.st.MediaFields(context.Background(), r.mediaID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, field := range fields {
		if field.Name == name {
			return field.Text
		}
	}
	return ""
}

func TestScanRehydratesRetaggedFile(t *testing.T) {
	r := newRehydrationScan(t)
	r.write("original audio bytes padding padding", 3*time.Hour, map[string]string{"title": "One", "album": "First"})
	r.scan()
	if r.field("title") != "One" || r.field("album") != "First" || r.field(testHydrationFingerprintField) == "" {
		t.Fatalf("first scan: title %q album %q fingerprint %q", r.field("title"), r.field("album"), r.field(testHydrationFingerprintField))
	}
	recorded := r.field(testHydrationFingerprintField)

	// Same bytes, later mtime: the scan re-hashes the file, and the seam skips it.
	writes := r.counted.writes
	r.touch(2 * time.Hour)
	r.scan()
	if r.counted.writes != writes {
		t.Fatalf("unchanged file re-hydrated: %d hydration writes, want 0", r.counted.writes-writes)
	}

	// New bytes derive a new content-fingerprint Media ID, so the import starts
	// a fresh Media and the seam's Fresh write records the new tags.
	r.write("retagged audio bytes padding padding padding", time.Hour, map[string]string{"title": "Two", "album": "Second"})
	r.scan()
	if r.field("title") != "Two" || r.field("album") != "Second" {
		t.Fatalf("re-tagged scan: title %q album %q, want the new tags", r.field("title"), r.field("album"))
	}
	if got := r.field(testHydrationFingerprintField); got == "" || got == recorded {
		t.Fatalf("re-tagged fingerprint = %q, want a new fingerprint (was %q)", got, recorded)
	}
}

// rehydrate drives the hydration seam directly for the scanned Media with a
// new content fingerprint. A full re-scan cannot reach a Replace for the same
// Media: new bytes derive a new content-fingerprint Media ID, so the import
// starts a fresh Media.
func (r *rehydrationScan) rehydrate(fingerprint string, tags map[string]string) {
	r.t.Helper()
	if err := r.adapter.HydrateMetadata(context.Background(), scanpkg.MetadataHydrationInput{
		MediaID: r.mediaID, LibraryID: r.library, Class: store.MediaClassAudio,
		Path: r.path, Metadata: tags, ContentFingerprint: fingerprint,
	}); err != nil {
		r.t.Fatalf("HydrateMetadata: %v", err)
	}
}

func TestRehydrationReplacesScanTierFields(t *testing.T) {
	r := newRehydrationScan(t)
	r.write("original audio bytes padding padding", 2*time.Hour, map[string]string{"title": "One", "album": "First"})
	r.scan()
	r.rehydrate("sha256:retagged", map[string]string{"title": "Two", "album": "Second"})
	if r.field("title") != "Two" || r.field("album") != "Second" || recordedFingerprint(r.field(testHydrationFingerprintField)) != "sha256:retagged" {
		t.Fatalf("title %q album %q fingerprint %q, want the re-tagged values", r.field("title"), r.field("album"), r.field(testHydrationFingerprintField))
	}
}

// relationSources returns the titles of the Entities related to the scanned
// Media by one relation name, sorted.
func (r *rehydrationScan) relationSources(name string) []string {
	r.t.Helper()
	related, err := r.st.EntitiesForMedia(context.Background(), r.mediaID, name, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		r.t.Fatal(err)
	}
	titles := make([]string, 0, len(related))
	for _, entity := range related {
		titles = append(titles, entity.Entity.Title)
	}
	sort.Strings(titles)
	return titles
}

// CORE-1654: a replace rewrites every relation kind the plan carries,
// many-cardinality credits included, and keeps the kinds it does not carry.
func TestRehydrationReplacesManyCardinalityRelations(t *testing.T) {
	r := newRehydrationScan(t)
	r.write("original audio bytes padding padding", 2*time.Hour, map[string]string{
		"title": "One", "contributors": "Contributor Alpha,Contributor Beta", "category": "Category One",
	})
	r.scan()
	if got := r.relationSources("appears-on"); !slices.Equal(got, []string{"Contributor Alpha", "Contributor Beta"}) {
		t.Fatalf("first scan credits = %v", got)
	}

	r.rehydrate("sha256:retagged", map[string]string{"title": "Two", "contributors": "Contributor Beta,Contributor Gamma"})
	if got := r.relationSources("appears-on"); !slices.Equal(got, []string{"Contributor Beta", "Contributor Gamma"}) {
		t.Fatalf("re-hydrated credits = %v, want only the new credits", got)
	}
	if got := r.relationSources("classifies"); !slices.Equal(got, []string{"Category One"}) {
		t.Fatalf("untouched relation kind = %v, want it kept", got)
	}

	r.rehydrate("sha256:retagged-again", map[string]string{"title": "Three", "category": "Category Two"})
	if got := r.relationSources("classifies"); !slices.Equal(got, []string{"Category Two"}) {
		t.Fatalf("re-hydrated category = %v, want only the new category", got)
	}
	if got := r.relationSources("appears-on"); !slices.Equal(got, []string{"Contributor Beta", "Contributor Gamma"}) {
		t.Fatalf("credits after a category-only replace = %v, want them kept", got)
	}
}

func TestRehydrationKeepsLockedTitle(t *testing.T) {
	r := newRehydrationScan(t)
	r.write("original audio bytes padding padding", 2*time.Hour, map[string]string{"title": "One", "album": "First"})
	r.scan()
	if _, err := r.st.SetMediaFieldLock(context.Background(), r.mediaID, "title", true, store.LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatal(err)
	}
	r.rehydrate("sha256:retagged", map[string]string{"title": "Two", "album": "Second"})
	if r.field("title") != "One" || r.field("album") != "Second" {
		t.Fatalf("title %q album %q, want the locked title kept and the album re-hydrated", r.field("title"), r.field("album"))
	}
}

func TestRehydrationKeepsManualTitle(t *testing.T) {
	r := newRehydrationScan(t)
	r.write("original audio bytes padding padding", 2*time.Hour, map[string]string{"title": "One", "album": "First"})
	r.scan()
	title := "Edited"
	if err := r.st.UpdateMediaMetadata(context.Background(), r.mediaID, store.MediaMetadata{Title: &title, Source: store.MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	r.rehydrate("sha256:retagged", map[string]string{"title": "Two", "album": "Second"})
	if r.field("title") != "One" || r.field("album") != "Second" {
		t.Fatalf("title %q album %q, want the manually owned title kept and the album re-hydrated", r.field("title"), r.field("album"))
	}
}

// discFolderTestMapper is rehydratingTestMapper plus a disc number derived
// from a "CDn" parent folder, the path-derived fact CORE-1733 is about.
type discFolderTestMapper struct{ rehydratingTestMapper }

func (discFolderTestMapper) MapHydration(tags map[string]string, path string) HydrationPlan {
	plan := rehydratingTestMapper{}.MapHydration(tags, path)
	if disc, ok := strings.CutPrefix(filepath.Base(filepath.Dir(path)), "CD"); ok {
		if n, err := strconv.Atoi(disc); err == nil && n > 0 {
			plan.MarkComplete = true
			plan.MediaFields = append(plan.MediaFields, store.MediaField{Name: "disc-number", Type: store.EntityFieldNumber, Number: float64(n)})
		}
	}
	return plan
}

func (r *rehydrationScan) number(name string) float64 {
	r.t.Helper()
	fields, err := r.st.MediaFields(context.Background(), r.mediaID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, field := range fields {
		if field.Name == name {
			return field.Number
		}
	}
	return 0
}

// move relocates the scanned file under root, keeping its bytes and mtime.
func (r *rehydrationScan) move(rel string) {
	r.t.Helper()
	next := filepath.Join(r.root, rel)
	if err := os.MkdirAll(filepath.Dir(next), 0o750); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Rename(r.path, next); err != nil {
		r.t.Fatal(err)
	}
	r.path = next
}

// CORE-1733: moving an untagged item into a CDn folder keeps its bytes, so
// the content fingerprint alone would skip it; the path-derived disc number
// must still be re-derived, while an unmoved, unchanged file is not reworked.
func TestScanRehydratesMovedFileIntoDiscFolder(t *testing.T) {
	r := newRehydrationScan(t)
	registry, err := plugin.NewRegistry([]plugin.Bundle{rehydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	r.adapter = newHydrationAdapters(registry, r.counted, map[string]HydrationMapper{testHydrationPluginID: discFolderTestMapper{}})[testHydrationPluginID].(*hydrationAdapter)
	r.path = filepath.Join(r.root, "Album", "CD1", "01 track.flac")
	if err := os.MkdirAll(filepath.Dir(r.path), 0o750); err != nil {
		t.Fatal(err)
	}
	r.write("untagged audio bytes padding padding", 3*time.Hour, map[string]string{"title": "One"})
	r.scan()
	if got := r.number("disc-number"); got != 1 {
		t.Fatalf("first scan disc-number = %v, want 1", got)
	}
	first := r.mediaID

	writes := r.counted.writes
	r.touch(2 * time.Hour)
	r.scan()
	if r.counted.writes != writes {
		t.Fatalf("unchanged, unmoved file re-hydrated: %d writes, want 0", r.counted.writes-writes)
	}

	r.move(filepath.Join("Album", "CD2", "01 track.flac"))
	r.scan()
	if r.mediaID != first {
		t.Fatalf("moved file Media = %q, want the same Media %q", r.mediaID, first)
	}
	if got := r.number("disc-number"); got != 2 {
		t.Fatalf("moved file disc-number = %v, want 2 from the CD2 folder", got)
	}

	writes = r.counted.writes
	r.touch(time.Hour)
	r.scan()
	if r.counted.writes != writes {
		t.Fatalf("moved file re-hydrated again with nothing changed: %d writes, want 0", r.counted.writes-writes)
	}

	// A move that changes no derived fact is not reworked either.
	r.move(filepath.Join("Renamed", "CD2", "01 track.flac"))
	writes = r.counted.writes
	r.scan()
	if r.counted.writes != writes || r.number("disc-number") != 2 {
		t.Fatalf("fact-preserving move: %d writes, disc-number %v; want no write and disc 2", r.counted.writes-writes, r.number("disc-number"))
	}
}

// CORE-1838: a managed move keeps the file's bytes, size and mtime and
// repoints the stored path, so without invalidation the next scan took the
// unchanged-file fast path and never re-derived the disc number.
func TestScanRehydratesManagedMoveBetweenDiscFolders(t *testing.T) {
	ctx := context.Background()
	r := newRehydrationScan(t)
	registry, err := plugin.NewRegistry([]plugin.Bundle{rehydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	r.adapter = newHydrationAdapters(registry, r.counted, map[string]HydrationMapper{testHydrationPluginID: discFolderTestMapper{}})[testHydrationPluginID].(*hydrationAdapter)
	if r.root, err = filepath.EvalSymlinks(r.root); err != nil {
		t.Fatal(err)
	}
	if err := r.st.SetLibraryRoots(ctx, r.library, []string{r.root}); err != nil {
		t.Fatal(err)
	}
	r.path = filepath.Join(r.root, "Set", "CD1", "01 track.flac")
	discTwo := filepath.Join(r.root, "Set", "CD2")
	for _, dir := range []string{filepath.Dir(r.path), discTwo} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	r.write("untagged audio bytes padding padding", 3*time.Hour, map[string]string{"title": "One"})
	r.scan()
	if got := r.number("disc-number"); got != 1 {
		t.Fatalf("first scan disc-number = %v, want 1", got)
	}
	first := r.mediaID

	moved, err := r.st.MoveManagedPath(ctx, r.library, r.path, discTwo, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("MoveManagedPath: %v", err)
	}
	r.path = moved.Path
	r.scan()
	if r.mediaID != first {
		t.Fatalf("moved file Media = %q, want the same Media %q", r.mediaID, first)
	}
	if got := r.number("disc-number"); got != 2 {
		t.Fatalf("managed-moved file disc-number = %v, want 2 from the CD2 folder", got)
	}

	writes := r.counted.writes
	r.scan()
	if r.counted.writes != writes {
		t.Fatalf("managed-moved file re-hydrated again with nothing changed: %d writes, want 0", r.counted.writes-writes)
	}
}
