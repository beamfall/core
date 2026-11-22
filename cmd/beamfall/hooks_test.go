//go:build !beamfall_appstore

package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/app"
	books "github.com/beamfall/plugin-audiobook"
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
	filmtvResolvers := 0
	for _, resolver := range hooks.OfflinePackResolvers {
		if resolver.PluginID == filmtv.PluginID {
			filmtvResolvers++
		}
	}
	if want := len(filmtv.PackResolvers()); want == 0 || filmtvResolvers != want {
		t.Fatalf("Film/TV offline pack resolver hooks = %d, want %d", filmtvResolvers, want)
	}
	if others := len(hooks.OfflinePackResolvers) - filmtvResolvers; others != 4 {
		t.Fatalf("other offline pack resolver hooks = %d, want 4", others)
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
		"GENRE": "Ambient", "MUSICBRAINZ_TRACKID": "recording-id",
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
	rehydrator, ok := mapper.(app.HydrationRehydrator)
	if !ok {
		t.Fatal("music hydration mapper does not opt into fingerprint-keyed re-hydration")
	}
	if rehydrator.FingerprintField() != music.HydratedFingerprintField {
		t.Fatal("music hydration fingerprint field drifted")
	}
	for _, tc := range []struct {
		recorded, current string
		want              app.HydrationWrite
	}{
		{"", "fp-1", app.HydrationWriteFresh},
		{"fp-1", "fp-1", app.HydrationWriteSkip},
		{"fp-1", "", app.HydrationWriteSkip},
		{"fp-1", "fp-2", app.HydrationWriteReplace},
	} {
		if got := rehydrator.DecideHydrationWrite(tc.recorded, tc.current); got != tc.want {
			t.Errorf("DecideHydrationWrite(%q, %q) = %v, want %v", tc.recorded, tc.current, got, tc.want)
		}
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
	if relation := relations["classifies"]; relation.From.Kind != "genre" || relation.From.Title != "Ambient" {
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

// musicAlbumIdentity renders everything the hydration adapter derives an album
// Entity's identity from, for one tagged file.
func musicAlbumIdentity(t *testing.T, tags map[string]string, path string) string {
	t.Helper()
	plan := firstPartyHooks().HydrationMappers[music.PluginID].MapHydration(tags, path)
	for _, relation := range plan.Relations {
		if relation.Name == "album-track" {
			album := relation.From
			album.Fields = nil
			return fmt.Sprintf("%#v", album)
		}
	}
	t.Fatalf("no album-track relation for %v", tags)
	return ""
}

func TestMusicAlbumIdentityIsAlbumArtistAndTitle(t *testing.T) {
	lantern := musicAlbumIdentity(t, map[string]string{"TITLE": "Copper Tide", "ALBUM": "Tidewater Anthology", "ARTIST": "Lantern Choir", "ALBUMARTIST": "Lantern Choir"}, "/flat/a.flac")
	northbound := musicAlbumIdentity(t, map[string]string{"TITLE": "Salt Road", "ALBUM": "Tidewater Anthology", "ARTIST": "Northbound Signal", "ALBUMARTIST": "Northbound Signal"}, "/flat/b.flac")
	if lantern == northbound {
		t.Fatalf("two albums titled Tidewater Anthology by different album artists share one identity: %s", lantern)
	}

	// No album-artist tag and no artist folder: the track artist stands in.
	untaggedLantern := musicAlbumIdentity(t, map[string]string{"TITLE": "Ember Steps", "ALBUM": "Tidewater Anthology", "ARTIST": "Lantern Choir"}, "/flat/c.flac")
	if untaggedLantern != lantern {
		t.Fatalf("album without an album-artist tag = %s, want the artist's album %s", untaggedLantern, lantern)
	}

	// A compilation with one album artist stays one album whatever each track's artist is.
	first := musicAlbumIdentity(t, map[string]string{"TITLE": "One", "ALBUM": "Now 1", "ARTIST": "Artist A", "ALBUMARTIST": "Various Artists"}, "/flat/d.flac")
	second := musicAlbumIdentity(t, map[string]string{"TITLE": "Two", "ALBUM": "Now 1", "ARTIST": "Artist B", "ALBUMARTIST": "Various Artists"}, "/flat/e.flac")
	if first != second {
		t.Fatalf("compilation split by track artist: %s vs %s", first, second)
	}
	flagged := musicAlbumIdentity(t, map[string]string{"TITLE": "Three", "ALBUM": "Now 1", "ARTIST": "Artist C", "COMPILATION": "1"}, "/flat/f.flac")
	if flagged != first {
		t.Fatalf("compilation-flagged track = %s, want %s", flagged, first)
	}
}

// With no provider artwork a local album must still show a cover, and an
// artist one of its albums': the music wiring declares both inheritances,
// album first, over relations the music bundle really declares.
func TestMusicDeclaresEntityPosterInheritance(t *testing.T) {
	var rules []app.EntityPosterRule
	for _, rule := range firstPartyHooks().EntityPosterRules {
		if rule.PluginID == music.PluginID {
			rules = append(rules, rule)
		}
	}
	want := []app.EntityPosterRule{
		{PluginID: music.PluginID, Kind: "album", Sources: []app.EntityPosterSource{{Relation: "album-track"}}},
		{PluginID: music.PluginID, Kind: "artist", Sources: []app.EntityPosterSource{
			{Relation: "album-artist", FromKind: "album"},
			{Relation: "appears-on"},
		}},
	}
	if fmt.Sprint(rules) != fmt.Sprint(want) {
		t.Fatalf("music entity poster rules = %+v, want %+v", rules, want)
	}

	declared := map[string]string{}
	for _, kind := range music.Bundle().EntityKinds {
		for _, relation := range kind.Relations {
			declared[kind.Name+" "+relation.Name] = relation.ToKind
		}
	}
	for from, to := range map[string]string{"album album-track": "media", "album album-artist": "artist", "artist appears-on": "media"} {
		if got, ok := declared[from]; !ok || got != to {
			t.Errorf("music bundle relation %q targets %q (declared %v), want %q", from, got, ok, to)
		}
	}
}

func TestFirstPartyEntityBrowseRules(t *testing.T) {
	want := []app.EntityBrowseRule{bookAuthorBrowseRule(), {
		PluginID: music.PluginID, Kind: "artist", Sources: []app.EntityBrowseSource{
			{Relation: "album-artist", FromKind: "album"},
			{Relation: "appears-on", ExcludeMediaRelation: "album-track"},
		},
	}}
	if got := firstPartyHooks().EntityBrowseRules; !reflect.DeepEqual(got, want) {
		t.Fatalf("browse rules = %+v, want %+v", got, want)
	}
	// Core drops a rule whose attribute the relation does not declare.
	declared := false
	for _, kind := range books.Bundle().EntityKinds {
		for _, relation := range kind.Relations {
			if kind.Name == books.KindPerson && relation.Name == books.RelationCreditedOn {
				declared = slices.Contains(relation.Attributes, books.AttributeRole)
			}
		}
	}
	if !declared {
		t.Fatalf("audiobook person %s does not declare attribute %q", books.RelationCreditedOn, books.AttributeRole)
	}
}

func TestMusicAlbumReleaseTypeGroups(t *testing.T) {
	mapper := musicHydrationMapper{}
	plan := mapper.MapHydration(map[string]string{
		"TITLE": "Copper Tide", "ALBUM": "Tidewater Live", "ALBUMARTIST": "Lantern Choir", "RELEASETYPE": "album; live",
	}, "/flat/a.flac")
	for _, relation := range plan.Relations {
		for _, field := range relation.From.Fields {
			if field.Name == music.ReleaseTypeField {
				t.Fatalf("Entity hydration still writes %q", field.Name)
			}
		}
	}
	tag := mapper.HydrationTag()
	if got := tag.Read(map[string]string{"releasetype": "album; live"}); tag.Field != music.ReleaseTypeField || got != music.ReleaseTypeLive {
		t.Fatalf("Media tag %q = %q, want %q", tag.Field, got, music.ReleaseTypeLive)
	}
	if got := tag.Read(nil); got != "" {
		t.Fatalf("missing tag normalized to %q", got)
	}

	rules := firstPartyHooks().EntityGroupRules
	if len(rules) != 1 || rules[0].PluginID != music.PluginID || rules[0].Kind != "album" || rules[0].Field != tag.Field {
		t.Fatalf("music entity group rules = %+v", rules)
	}
	if got, want := len(rules[0].Groups), len(music.ReleaseTypeGroups()); got != want || rules[0].Groups[0].Value != music.ReleaseTypeAlbum {
		t.Fatalf("release type groups = %+v, want the plugin's %d in order", rules[0].Groups, want)
	}
}
