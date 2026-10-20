//go:build beamfall_appstore

package main

import (
	"strings"
	"testing"
)

func TestAppStoreFirstPartyBundles(t *testing.T) {
	bundles := firstPartyBundles()
	ids := make([]string, 0, len(bundles))
	for _, bundle := range bundles {
		ids = append(ids, string(bundle.ID))
	}
	const wantIDs = "filmtv,music,podcasts,photos,audiobook,familyarchive,karaoke"
	if joined := strings.Join(ids, ","); joined != wantIDs {
		t.Fatalf("App Store bundle IDs = %q, want exactly %q", joined, wantIDs)
	}
}

func TestAppStoreHooksHaveBundledOwners(t *testing.T) {
	bundled := map[string]bool{}
	for _, bundle := range firstPartyBundles() {
		bundled[string(bundle.ID)] = true
	}
	hooks := firstPartyHooks()
	owners := map[string]bool{}
	for owner := range hooks.Parsers {
		owners[owner] = true
	}
	for owner := range hooks.IdentifyScorers {
		owners[owner] = true
	}
	for key := range hooks.MigrationSourceFactories {
		owners[key.PluginID] = true
	}
	for owner := range hooks.LocalExtractors {
		owners[owner] = true
	}
	for owner := range hooks.HydrationMappers {
		owners[owner] = true
	}
	for _, hook := range hooks.OfflinePackResolvers {
		owners[hook.PluginID] = true
	}
	for _, hook := range hooks.ScrobbleAdapters {
		owners[hook.PluginID] = true
	}
	if hooks.FeedSearcher != nil {
		owners[hooks.FeedSearcher.PluginID] = true
	}
	for owner := range owners {
		if !bundled[owner] {
			t.Errorf("App Store hook owner %q has no compiled bundle", owner)
		}
	}
}

func TestAppStoreDependencyClosureHasExactCompiledModules(t *testing.T) {
	assertExactPluginDependencyModules(t, "beamfall_appstore", []string{
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
