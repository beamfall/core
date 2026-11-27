package graph

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func newLensMetadataHandler(t *testing.T) (*Handler, *store.Store, map[string]store.LibraryID) {
	t.Helper()
	st := storetest.Open(t)
	ctx := context.Background()

	resumeFalse := false
	resumeTrue := true
	reg, err := newTestPluginRegistry([]plugin.Bundle{
		{
			ID:           "bundle-video",
			Version:      "0.1.0",
			DisplayName:  plugin.L("Cinema"),
			MediaLabel:   plugin.L("Feature"),
			MediaClasses: []string{"video"},
			Accent:       "#C9803F",
			Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "recent", Kind: "shelf", Title: plugin.L("Recent"), Source: plugin.SourceSpec{Kind: "recently-added"}},
			}}},
		},
		{
			ID:                  "bundle-stage",
			Version:             "0.1.0",
			DisplayName:         plugin.L("Sing"),
			MediaLabel:          plugin.L("Track"),
			MediaClasses:        []string{"audio", "video"},
			Engagement:          plugin.Engagement{ResumeMedia: &resumeFalse, SelectPlays: true},
			InteractiveSidecars: []string{"cdg", "lrc"},
			LibraryDefaults:     plugin.LibraryDefaults{Icon: "mic"},
			Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "recent", Kind: "shelf", Title: plugin.L("Recent"), Source: plugin.SourceSpec{Kind: "recently-added"}},
			}}},
		},
		{
			ID:                 "bundle-audio",
			Version:            "0.1.0",
			DisplayName:        plugin.L("Tracks"),
			MediaLabel:         plugin.L("Track"),
			MediaClasses:       []string{"audio"},
			FeatureEligibility: []string{"audio-station"},
			Engagement:         plugin.Engagement{ResumeMedia: &resumeTrue, PlaysAlone: true},
			LibraryDefaults:    plugin.LibraryDefaults{Icon: "headphones"},
			Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "recent", Kind: "shelf", Title: plugin.L("Recent"), Source: plugin.SourceSpec{Kind: "recently-added"}},
			}}},
		},
	})
	if err != nil {
		t.Fatalf("newTestPluginRegistry: %v", err)
	}
	const profileID = "profile-default"
	libVideo, err := st.CreateLibrary(ctx, "Cinema", false, "bundle-video", profileID)
	if err != nil {
		t.Fatalf("CreateLibrary video: %v", err)
	}
	libStage, err := st.CreateLibrary(ctx, "Stage", false, "bundle-stage", profileID)
	if err != nil {
		t.Fatalf("CreateLibrary stage: %v", err)
	}
	libAudio, err := st.CreateLibrary(ctx, "Listening", false, "bundle-audio", profileID)
	if err != nil {
		t.Fatalf("CreateLibrary audio: %v", err)
	}

	seedOne := func(libID store.LibraryID, path, title, fp string, class store.MediaClass) {
		if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID:           libID,
			Path:                path,
			Title:               title,
			Container:           "mkv",
			Codec:               "h264",
			ContentFingerprint:  fp,
			DurationSecondsHint: 120,
			Class:               class,
		}); err != nil {
			t.Fatalf("ImportMediaFile(%s): %v", title, err)
		}
	}
	seedOne(libVideo.ID, "/video/alpha.mkv", "Alpha Signal", "fp-alpha", store.MediaClassVideo)
	seedOne(libStage.ID, "/stage/beta.mp4", "Beta Signal", "fp-beta", store.MediaClassVideo)
	seedOne(libAudio.ID, "/audio/gamma.flac", "Gamma Signal", "fp-gamma", store.MediaClassAudio)

	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	h := NewHandler(manifest.NewService(st, clock, reg), auth.NewService(st), st, reg)
	return h, st, map[string]store.LibraryID{
		"video": libVideo.ID,
		"stage": libStage.ID,
		"audio": libAudio.ID,
	}
}

func TestPluginsAndLibrariesExposeModalityMetadata(t *testing.T) {
	h, _, _ := newLensMetadataHandler(t)
	reply := postHomeStudioJSON(t, h, `query {
		plugins {
			id
			mediaClasses
			presentation { resumeMedia playsAlone selectPlays interactiveSidecars defaultIcon }
			capabilities
		}
		homeFilterSchema {
			libraries {
				id
				title
				pluginID
				accent
				mediaClasses
				presentation { resumeMedia playsAlone selectPlays interactiveSidecars defaultIcon }
				capabilities
			}
		}
	}`, nil)
	if errs, _ := reply["errors"].([]any); len(errs) != 0 {
		t.Fatalf("errors = %#v", errs)
	}
	data := reply["data"].(map[string]any)
	plugins := data["plugins"].([]any)
	if len(plugins) != 3 {
		t.Fatalf("plugins len = %d, want 3", len(plugins))
	}
	audioPlugin := plugins[2].(map[string]any)
	if audioPlugin["id"] != "bundle-audio" {
		t.Fatalf("unexpected plugin id: %#v", audioPlugin["id"])
	}
	caps := audioPlugin["capabilities"].([]any)
	if len(caps) != 1 || caps[0] != "audio-station" {
		t.Fatalf("audio capabilities = %#v", caps)
	}
	pres := plugins[1].(map[string]any)["presentation"].(map[string]any)
	if pres["selectPlays"] != true || pres["resumeMedia"] != false || pres["defaultIcon"] != "mic" {
		t.Fatalf("stage presentation = %#v", pres)
	}
	schemaLibs := data["homeFilterSchema"].(map[string]any)["libraries"].([]any)
	var foundVideo map[string]any
	for _, raw := range schemaLibs {
		entry := raw.(map[string]any)
		if entry["pluginID"] == "bundle-video" {
			foundVideo = entry
			break
		}
	}
	if foundVideo == nil {
		t.Fatalf("bundle-video missing from homeFilterSchema.libraries: %#v", schemaLibs)
	}
	if foundVideo["accent"] != "#C9803F" {
		t.Fatalf("video accent = %#v, want #C9803F", foundVideo["accent"])
	}
}

func TestSourceItemsAndSearchResultFilterByLibraryIDs(t *testing.T) {
	h, _, libs := newLensMetadataHandler(t)

	// Narrow sourceItems to video + stage libraries only.
	srcReply := postHomeStudioJSON(t, h, `query Q($ids: [String!]) {
		sourceItems(kind: "recently-added", libraryIDs: $ids, limit: 20) { id title libraryID mediaClass }
	}`, map[string]any{"ids": []string{string(libs["video"]), string(libs["stage"])}})
	if errs, _ := srcReply["errors"].([]any); len(errs) != 0 {
		t.Fatalf("sourceItems errors: %#v", errs)
	}
	srcItems := srcReply["data"].(map[string]any)["sourceItems"].([]any)
	if len(srcItems) != 2 {
		t.Fatalf("filtered sourceItems count = %d, want 2: %#v", len(srcItems), srcItems)
	}
	for _, raw := range srcItems {
		libID := raw.(map[string]any)["libraryID"].(string)
		if libID == string(libs["audio"]) {
			t.Fatalf("unexpected audio library item in watch sourceItems: %#v", raw)
		}
	}

	// Explicit empty libraryIDs yields empty slice rather than widening to all.
	emptySrc := postHomeStudioJSON(t, h, `query Q($ids: [String!]) {
		sourceItems(kind: "recently-added", libraryIDs: $ids, limit: 20) { id }
	}`, map[string]any{"ids": []string{}})
	if errs, _ := emptySrc["errors"].([]any); len(errs) != 0 {
		t.Fatalf("empty sourceItems errors: %#v", errs)
	}
	if got := emptySrc["data"].(map[string]any)["sourceItems"].([]any); len(got) != 0 {
		t.Fatalf("expected 0 items for empty libraryIDs, got %#v", got)
	}

	// home-shelf recent intersects requested libraryIDs with session scope.
	shelfReply := postHomeStudioJSON(t, h, `query Q($ids: [String!]) {
		sourceItems(kind: "home-shelf", value: "recent", libraryIDs: $ids, limit: 20) { id libraryID }
	}`, map[string]any{"ids": []string{string(libs["audio"])}})
	if errs, _ := shelfReply["errors"].([]any); len(errs) != 0 {
		t.Fatalf("home-shelf sourceItems errors: %#v", errs)
	}
	shelfItems := shelfReply["data"].(map[string]any)["sourceItems"].([]any)
	if len(shelfItems) != 1 || shelfItems[0].(map[string]any)["libraryID"] != string(libs["audio"]) {
		t.Fatalf("filtered home-shelf items = %#v, want 1 audio item", shelfItems)
	}

	// Narrow searchResult to audio library only.
	searchReply := postHomeStudioJSON(t, h, `query S($q: String!, $ids: [String!]) {
		searchResult(query: $q, mode: "keyword", libraryIDs: $ids) {
			mode
			items { id title libraryID mediaClass }
		}
	}`, map[string]any{"q": "Signal", "ids": []string{string(libs["audio"])}})
	if errs, _ := searchReply["errors"].([]any); len(errs) != 0 {
		t.Fatalf("searchResult errors: %#v", errs)
	}
	items := searchReply["data"].(map[string]any)["searchResult"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("filtered searchResult count = %d, want 1: %#v", len(items), items)
	}
	if items[0].(map[string]any)["libraryID"] != string(libs["audio"]) {
		t.Fatalf("searchResult libraryID = %#v, want %s", items[0].(map[string]any)["libraryID"], libs["audio"])
	}

	// Non-granted libraryID never widens scope.
	deniedSearch := postHomeStudioJSON(t, h, `query S($q: String!, $ids: [String!]) {
		searchResult(query: $q, mode: "keyword", libraryIDs: $ids) { items { id } }
	}`, map[string]any{"q": "Signal", "ids": []string{"nonexistent-library"}})
	if errs, _ := deniedSearch["errors"].([]any); len(errs) != 0 {
		t.Fatalf("denied searchResult errors: %#v", errs)
	}
	if got := deniedSearch["data"].(map[string]any)["searchResult"].(map[string]any)["items"].([]any); len(got) != 0 {
		t.Fatalf("expected 0 items for unknown libraryID, got %#v", got)
	}
}
