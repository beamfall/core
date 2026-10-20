//go:build !beamfall_appstore

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/app"
	enrichment "github.com/beamfall/plugin-enrichment"
	filmtv "github.com/beamfall/plugin-filmtv"
	music "github.com/beamfall/plugin-music"
	photos "github.com/beamfall/plugin-photos"
)

func TestDefaultFirstPartyBundleIdentities(t *testing.T) {
	got := make([]string, 0, len(firstPartyBundles()))
	for _, bundle := range firstPartyBundles() {
		got = append(got, string(bundle.ID))
	}
	const want = "filmtv,music,podcasts,photos,audiobook,familyarchive,karaoke"
	if joined := strings.Join(got, ","); joined != want {
		t.Fatalf("first-party bundle IDs = %q, want %q", joined, want)
	}
}

func TestFirstPartyHooksComposeProductionPluginBehavior(t *testing.T) {
	hooks := firstPartyHooks()
	if hooks.Parsers[filmtv.PluginID] == nil {
		t.Fatal("Film/TV parser hook missing")
	}
	if hooks.IdentifyScorers[filmtv.PluginID].Scorer == nil {
		t.Fatal("Film/TV scorer hook missing")
	}
	if hooks.HydrationMappers[music.PluginID] == nil {
		t.Fatal("Music hydration hook missing")
	}
	if len(hooks.LocalExtractors[photos.PluginID]) == 0 {
		t.Fatal("Photos local extractor hook missing")
	}
	if hooks.EnrichmentProvider == nil {
		t.Fatal("enrichment provider factory missing")
	}
	if len(hooks.OfflinePackResolvers) != 4 {
		t.Fatalf("offline pack resolver hooks = %d, want 4", len(hooks.OfflinePackResolvers))
	}
	if len(hooks.ScrobbleAdapters) != 3 {
		t.Fatalf("scrobble adapter hooks = %d, want 3", len(hooks.ScrobbleAdapters))
	}
	if hooks.FeedSearcher == nil {
		t.Fatal("feed searcher hook missing")
	}
}

func TestDefaultPluginDependencyClosureHasExactCompiledModules(t *testing.T) {
	assertExactPluginDependencyModules(t, "", []string{
		"plugin-audiobook",
		"plugin-enrichment",
		"plugin-familyarchive",
		"plugin-filmtv",
		"plugin-karaoke",
		"plugin-music",
		"plugin-photos",
		"plugin-podcasts",
		"plugin-sdk",
	})
}

func TestFeedDirectoryScraperUsesPluginTemplate(t *testing.T) {
	endpoint, template, ok := feedDirectoryScraper("itunes-directory")
	if !ok {
		t.Fatal("itunes-directory scraper not found")
	}
	if endpoint != "https://itunes.apple.com" || !strings.Contains(template, "{query}") {
		t.Fatalf("directory scraper = %q %q, want plugin endpoint and query template", endpoint, template)
	}
}

func TestProductionHookAdaptersPreservePluginOutputs(t *testing.T) {
	hooks := firstPartyHooks()
	mapper := hooks.HydrationMappers[music.PluginID]
	hydration := mapper.MapHydration(map[string]string{
		"TITLE": "Track", "ALBUM": "Record", "TRACK": "2/12", "DISC": "1",
		"DATE": "2024-05-06", "ARTIST": "Artist", "ALBUMARTIST": "Album Artist",
		"GENRE": "Genre", "MUSICBRAINZ_TRACKID": "recording-id",
	}, "/music/track.flac")
	wantFields := map[string]string{"title": "Track", "album": "Record", "musicbrainz-recording-id": "recording-id"}
	for _, field := range hydration.MediaFields {
		want, ok := wantFields[field.Name]
		if ok && field.Text == want {
			delete(wantFields, field.Name)
		}
	}
	if len(wantFields) != 0 {
		t.Fatalf("hydration missing media fields: %v", wantFields)
	}
	if len(hydration.ExternalIDs) != 1 || hydration.ExternalIDs[0].Scheme != music.SchemeMusicBrainzRecording || hydration.ExternalIDs[0].Value != "recording-id" {
		t.Fatalf("hydration external IDs = %+v", hydration.ExternalIDs)
	}
	if mapper.MarkerField() != music.HydratedField {
		t.Fatal("music hydration constants drifted")
	}
	schemes := mapper.ExternalIDSchemes()
	if len(schemes) != 1 || schemes[0] != music.SchemeMusicBrainzRecording {
		t.Fatalf("music hydration external-ID schemes = %v", schemes)
	}
	relations := map[string]app.HydrationRelation{}
	for _, relation := range hydration.Relations {
		relations[relation.Name] = relation
	}
	if relation := relations["appears-on"]; relation.From.Kind != "artist" || relation.From.Title != "Artist" {
		t.Fatalf("appears-on relation = %+v", relation)
	}
	if relation := relations["album-track"]; relation.SortOrder == nil || *relation.SortOrder != 1002 {
		t.Fatalf("album-track relation = %+v", relation)
	}
	if relation := relations["album-artist"]; relation.ToEntity == nil || relation.ToEntity.Title != "Album Artist" || relation.RequiresRelation != "album-track" {
		t.Fatalf("album-artist relation = %+v", relation)
	}
	if relation := relations["classifies"]; relation.From.Kind != "genre" || relation.From.Title != "Genre" {
		t.Fatalf("classifies relation = %+v", relation)
	}
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			provider := hooks.EnrichmentProvider(enabled)
			if provider.Available() {
				t.Fatal("enrichment provider unexpectedly available")
			}
			tests := []struct {
				name      string
				input     string
				cancelled bool
				wantErr   error
			}{
				{name: "valid", input: "metadata text", wantErr: enrichment.ErrSemanticProviderUnavailable},
				{name: "blank", input: "!!!", wantErr: enrichment.ErrEmptyInput},
				{name: "cancelled", input: "metadata text", cancelled: true, wantErr: context.Canceled},
			}
			methods := []struct {
				name string
				call func(context.Context, string) ([]float32, error)
			}{
				{name: "query", call: provider.EmbedQuery},
				{name: "document", call: provider.EmbedDocument},
			}
			for _, method := range methods {
				for _, test := range tests {
					t.Run(method.name+"/"+test.name, func(t *testing.T) {
						ctx := t.Context()
						if test.cancelled {
							cancelled, cancel := context.WithCancel(ctx)
							cancel()
							ctx = cancelled
						}
						vector, err := method.call(ctx, test.input)
						if vector != nil {
							t.Fatalf("vector = %v, want nil", vector)
						}
						if !errors.Is(err, test.wantErr) {
							t.Fatalf("error = %v, want errors.Is(_, %v)", err, test.wantErr)
						}
					})
				}
			}
		})
	}
	factory := hooks.MigrationSourceFactories[app.MigrationSourceKey{PluginID: filmtv.PluginID, ImporterID: "nfo"}]
	if factory == nil || factory(nil, t.TempDir()) == nil {
		t.Fatal("Film/TV NFO migration source factory missing")
	}
}
