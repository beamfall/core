package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/apptest/plugintest"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	sdk "github.com/beamfall/plugin-sdk"
)

const (
	testPathPluginID        = "starlog"
	testEventPluginID       = "chronicle"
	testSerialPluginID      = "dispatch"
	testQueuePluginID       = "fieldnotes"
	testSecondaryPluginID   = "wayfinder"
	testPathImporterID      = "path-records"
	testExternalImporterID  = "external-state"
	testEventImporterID     = "activity"
	testSubscriptionID      = "subscriptions"
	testItemStateImporterID = "item-state"

	testHydrationPluginID      = "ledger"
	testHydrationMarkerField   = "fixture-hydrated"
	testHydrationExternalIDKey = "fixture-record"
)

func testBundle(id string) plugin.Bundle {
	return plugintest.Compatible(plugin.Bundle{
		ID:           plugin.ID(id),
		Version:      "1.0.0",
		DisplayName:  plugin.L("Test Plugin"),
		MediaLabel:   plugin.L("Entry"),
		MediaClasses: []string{"video"},
		Layouts:      []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	})
}

func testBundleWithKind(id, kind string) plugin.Bundle {
	bundle := testBundle(id)
	bundle.EntityKinds = []plugin.EntityKind{{Name: kind, Archetype: "container"}}
	return bundle
}

func testImporter(id string, categories ...string) plugin.ImporterSpec {
	return plugin.ImporterSpec{
		ID:          id,
		DisplayName: plugin.L("Fixture Import"),
		Categories:  append([]string(nil), categories...),
		Input:       "export-file",
		Conflict:    "additive",
	}
}

func testPathBundle() plugin.Bundle {
	bundle := testBundle(testPathPluginID)
	bundle.Importers = []plugin.ImporterSpec{
		testImporter(testPathImporterID, migrate.CategoryWatchState),
		testImporter(testExternalImporterID, migrate.CategoryWatchState),
	}
	return bundle
}

func testEventBundle() plugin.Bundle {
	bundle := testBundle(testEventPluginID)
	bundle.Importers = []plugin.ImporterSpec{testImporter(testEventImporterID, migrate.CategoryWatchState)}
	bundle.ScrobbleMatch = &sdk.ScrobbleMatchSpec{ArtistConfirm: []string{"appears-on"}, AlbumTieBreak: []string{"album-track"}}
	bundle.EntityKinds = []plugin.EntityKind{
		{Name: "artist", Archetype: "person", Relations: []plugin.RelationSpec{{Name: "appears-on", ToKind: plugin.MediaKindName}}},
		{Name: "album", Archetype: "container", Relations: []plugin.RelationSpec{{Name: "album-track", ToKind: plugin.MediaKindName}}},
	}
	return bundle
}

func testSerialBundle() plugin.Bundle {
	bundle := testBundle(testSerialPluginID)
	bundle.Importers = []plugin.ImporterSpec{
		testImporter(testSubscriptionID, migrate.CategorySubscriptions),
		testImporter(testItemStateImporterID, migrate.CategoryItemState),
	}
	return bundle
}

func testQueueBundle() plugin.Bundle {
	bundle := testBundle(testQueuePluginID)
	bundle.IdentificationPolicy = plugin.IdentificationPolicyQueue
	bundle.MediaFields = []plugin.FieldSpec{
		{Name: "class", Type: "text", Label: plugin.L("Class")},
		{Name: "capture-date", Type: "date", Label: plugin.L("Captured")},
		{Name: "source-folder", Type: "text", Label: plugin.L("Source Folder")},
		{Name: "sidecar-title", Type: "text", Label: plugin.L("Sidecar Title")},
		{Name: "sidecar-summary", Type: "text", Label: plugin.L("Sidecar Summary")},
		{Name: "camera-make", Type: "text", Label: plugin.L("Camera Make")},
		{Name: "camera-model", Type: "text", Label: plugin.L("Camera Model")},
	}
	return bundle
}

func testHydrationBundle() plugin.Bundle {
	bundle := testBundle(testHydrationPluginID)
	bundle.MediaClasses = []string{"audio"}
	bundle.MediaFields = []plugin.FieldSpec{
		{Name: "album", Type: "text", Label: plugin.L("Album")},
		{Name: "track-number", Type: "number", Label: plugin.L("Track Number")},
		{Name: "disc-number", Type: "number", Label: plugin.L("Disc Number")},
		{Name: "release-year", Type: "number", Label: plugin.L("Release Year")},
		{Name: "release-date", Type: "date", Label: plugin.L("Release Date")},
		{Name: "musicbrainz-recording-id", Type: "text", Label: plugin.L("Recording ID")},
		{Name: "musicbrainz-release-id", Type: "text", Label: plugin.L("Release ID")},
		{Name: testHydrationMarkerField, Type: "flag", Label: plugin.L("Hydrated")},
	}
	bundle.EntityKinds = []plugin.EntityKind{
		{
			Name:      "artist",
			Archetype: "person",
			Relations: []plugin.RelationSpec{{
				Name:       "appears-on",
				ToKind:     plugin.MediaKindName,
				Attributes: []string{"credited-as"},
				TypedAttributes: []sdk.RelationAttributeSpec{
					{Name: "weight", Type: "number"},
					{Name: "active", Type: "flag"},
					{Name: "observed-at", Type: "date"},
					{Name: "reference", Type: "url"},
					{Name: "note", Type: "text"},
				},
			}},
		},
		{
			Name:      "album",
			Archetype: "container",
			Fields: []plugin.FieldSpec{
				{Name: "release-date", Type: "date", Label: plugin.L("Release Date")},
				{Name: "musicbrainz-release-id", Type: "text", Label: plugin.L("Release ID")},
			},
			Relations: []plugin.RelationSpec{
				{Name: "album-track", ToKind: plugin.MediaKindName, Cardinality: "one"},
				{Name: "album-artist", ToKind: "artist"},
			},
		},
		{
			Name:      "genre",
			Archetype: "category",
			Relations: []plugin.RelationSpec{{Name: "classifies", ToKind: plugin.MediaKindName}},
		},
	}
	bundle.ScrobbleMatch = &sdk.ScrobbleMatchSpec{ArtistConfirm: []string{"appears-on"}, AlbumTieBreak: []string{"album-track"}}
	return bundle
}

func migrationTestHooks() Hooks {
	factories := map[MigrationSourceKey]MigrationSourceFactory{}
	add := func(pluginID, importerID string, categories ...string) {
		factories[MigrationSourceKey{PluginID: pluginID, ImporterID: importerID}] = fixtureMigrationSourceFactory(categories...)
	}
	add(testPathPluginID, testPathImporterID, migrate.CategoryWatchState)
	add(testPathPluginID, testExternalImporterID, migrate.CategoryWatchState)
	add(testEventPluginID, testEventImporterID, migrate.CategoryWatchState)
	add(testSerialPluginID, testSubscriptionID, migrate.CategorySubscriptions)
	add(testSerialPluginID, testItemStateImporterID, migrate.CategoryItemState)
	return Hooks{MigrationSourceFactories: factories}
}

type fixtureMigrationSource struct {
	path       string
	categories []string
}

func fixtureMigrationSourceFactory(defaultCategories ...string) MigrationSourceFactory {
	return func(categories []string, sourcePath string) migrate.Source {
		if len(categories) == 0 {
			categories = defaultCategories
		}
		return fixtureMigrationSource{path: sourcePath, categories: append([]string(nil), categories...)}
	}
}

func (s fixtureMigrationSource) Categories() []string {
	return append([]string(nil), s.categories...)
}

func (s fixtureMigrationSource) Items(ctx context.Context) ([]migrate.ForeignItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var items []migrate.ForeignItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func writeMigrationFixture(t *testing.T, items ...migrate.ForeignItem) string {
	t.Helper()
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "migration-items.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type fixtureFilenameParser struct {
	parsed scanpkg.ParsedMedia
	fields []scanpkg.ParsedField
}

func (p fixtureFilenameParser) Parse(string, scanpkg.ParseProbe) (scanpkg.ParsedMedia, bool) {
	return p.parsed, true
}

func (p fixtureFilenameParser) ParseTypedMediaFields(string, scanpkg.ParseProbe) []scanpkg.ParsedField {
	return append([]scanpkg.ParsedField(nil), p.fields...)
}
