package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// sonicFixture is the station music fixture plus a third track and sonic
// vectors: trackB sits next to trackA, trackC is far from both.
type sonicFixture struct {
	musicFixture
	trackC store.MediaID
}

func seedSonicFixture(t *testing.T, st *store.Store) sonicFixture {
	t.Helper()
	ctx := context.Background()
	fx := seedMusicFixture(t, st)
	c, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: fx.libraryID, Path: "/music/c.flac", Title: "Track C", Container: "flac", Codec: "flac",
		Class: store.MediaClassAudio, ContentFingerprint: "fp-music-c", DurationSecondsHint: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	for fp, vec := range map[string][]float32{
		"fp-music-a": {1, 0},
		"fp-music-b": {0.9, 0.1},
		"fp-music-c": {0.1, 0.9},
	} {
		if err := st.UpsertEmbedding(ctx, fp, store.SonicEmbeddingKind, vec); err != nil {
			t.Fatal(err)
		}
	}
	return sonicFixture{musicFixture: fx, trackC: c.ID}
}

func similarIDs(t *testing.T, h *Handler, id store.MediaID) []string {
	t.Helper()
	items := graphData[[]SourceItem](t, h,
		`query($mediaID:String!) { similarMedia(mediaID:$mediaID) { id } }`,
		map[string]any{"mediaID": string(id)}, "similarMedia")
	return itemIDs(items)
}

func TestSimilarMediaRanksBySonicVector(t *testing.T) {
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedSonicFixture(t, st)

	got := similarIDs(t, h, fx.trackA)
	want := []string{string(fx.trackB), string(fx.trackC)}
	if !equalStringSlices(got, want) {
		t.Fatalf("similarMedia = %v, want %v (nearest first, seed excluded)", got, want)
	}

	// Muting a hit drops it from the row (passive-rail suppression).
	if err := st.SetMute(context.Background(), "profile-default", store.MuteKindMedia, string(fx.trackB), false, true); err != nil {
		t.Fatal(err)
	}
	if got := similarIDs(t, h, fx.trackA); !equalStringSlices(got, []string{string(fx.trackC)}) {
		t.Fatalf("similarMedia with trackB muted = %v, want [%s]", got, fx.trackC)
	}
}

func TestSimilarMediaEmptyWithoutVectors(t *testing.T) {
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	if got := similarIDs(t, h, fx.trackA); len(got) != 0 {
		t.Fatalf("similarMedia without vectors = %v, want empty", got)
	}
}

func TestSimilarMediaUnknownMediaErrors(t *testing.T) {
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	_, errs := post(t, h, `{"query":"query { similarMedia(mediaID:\"media-missing\") { id } }"}`, authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown media") {
		t.Fatalf("errors = %v, want unknown media", errs)
	}
}

func TestSonicAnalysisToggle(t *testing.T) {
	ctx := context.Background()
	const setQuery = `mutation($libraryID:String!, $enabled:Boolean!) { setSonicAnalysisEnabled(libraryID:$libraryID, enabled:$enabled) { libraryID enabled available } }`
	set := func(h *Handler, library string, enabled bool) (map[string]json.RawMessage, []graphError) {
		body, err := json.Marshal(map[string]any{"query": setQuery, "variables": map[string]any{"libraryID": library, "enabled": enabled}})
		if err != nil {
			t.Fatal(err)
		}
		return post(t, h, string(body), authorizeManager)
	}

	t.Run("requires an Admin Session", func(t *testing.T) {
		st := storetest.Open(t)
		h := newTestHandlerWithStore(st)
		if _, errs := set(h, "library-local", false); len(errs) == 0 {
			t.Fatal("toggle without an Admin Session succeeded")
		}
	})

	t.Run("refuses enabling with no analyser", func(t *testing.T) {
		st := storetest.Open(t)
		openManagerAdminSession(t, st)
		h := newTestHandlerWithStore(st)
		_, errs := set(h, "library-local", true)
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "not available") {
			t.Fatalf("errors = %v, want not available", errs)
		}
		if enabled, _ := st.SonicAnalysisEnabled(ctx, "library-local"); enabled {
			t.Fatal("refused toggle was stored")
		}
		// Turning it off is always allowed.
		if _, errs := set(h, "library-local", false); len(errs) != 0 {
			t.Fatalf("disable errors = %v", errs)
		}
	})

	t.Run("enables and arms the sweep when available", func(t *testing.T) {
		st := storetest.Open(t)
		openManagerAdminSession(t, st)
		h := newTestHandlerWithStore(st)
		sweeps := 0
		h.sonicAnalysisAvailable = func() bool { return true }
		h.sonicAnalysisSweep = func(context.Context) { sweeps++ }
		data, errs := set(h, "library-local", true)
		if len(errs) != 0 {
			t.Fatalf("errors = %v", errs)
		}
		var info SonicAnalysisInfo
		mustUnmarshal(t, data["setSonicAnalysisEnabled"], &info)
		if !info.Enabled || !info.Available || info.LibraryID != "library-local" {
			t.Fatalf("info = %+v", info)
		}
		if sweeps != 1 {
			t.Fatalf("sweeps = %d, want 1", sweeps)
		}
		if enabled, _ := st.SonicAnalysisEnabled(ctx, "library-local"); !enabled {
			t.Fatal("toggle not stored")
		}

		readData, errs := post(t, h, `{"query":"query { sonicAnalysisEnabled { libraryID enabled available } }"}`, authorizeManager)
		if len(errs) != 0 {
			t.Fatalf("read errors = %v", errs)
		}
		var infos []SonicAnalysisInfo
		mustUnmarshal(t, readData["sonicAnalysisEnabled"], &infos)
		found := false
		for _, i := range infos {
			if i.LibraryID == "library-local" {
				found = i.Enabled && i.Available
			}
		}
		if !found {
			t.Fatalf("sonicAnalysisEnabled = %+v, want library-local enabled", infos)
		}
	})

	t.Run("unknown Library", func(t *testing.T) {
		st := storetest.Open(t)
		openManagerAdminSession(t, st)
		h := newTestHandlerWithStore(st)
		h.sonicAnalysisAvailable = func() bool { return true }
		_, errs := set(h, "library-missing", true)
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown library") {
			t.Fatalf("errors = %v, want unknown library", errs)
		}
	})
}

func TestStationSonicSeedKeepsSimilarityOrder(t *testing.T) {
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedSonicFixture(t, st)
	got, _, err := h.generateStation(context.Background(), defaultScope(), stationSeed{kind: "sonic", id: string(fx.trackA)}, stationRules{}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ids := candidateIDs(got)
	want := []string{string(fx.trackA), string(fx.trackB), string(fx.trackC)}
	if !equalStringSlices(ids, want) {
		t.Fatalf("sonic station = %v, want %v", ids, want)
	}
	for _, c := range got {
		if c.Explanation != "Sounds like your seed" {
			t.Fatalf("explanation = %q", c.Explanation)
		}
	}

	// A seed with no vector yields just itself, never an error.
	bare := storetest.Open(t)
	bareHandler := newStationTestHandler(t, bare)
	bareFx := seedMusicFixture(t, bare)
	got, _, err = bareHandler.generateStation(context.Background(), defaultScope(), stationSeed{kind: "sonic", id: string(bareFx.trackA)}, stationRules{}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ids := candidateIDs(got); !equalStringSlices(ids, []string{string(bareFx.trackA)}) {
		t.Fatalf("sonic station without vectors = %v, want the seed alone", ids)
	}
}

func TestSimilarMediaLimitAppliesAfterMuteSuppression(t *testing.T) {
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedSonicFixture(t, st)
	if err := st.SetMute(context.Background(), "profile-default", store.MuteKindMedia, string(fx.trackB), false, true); err != nil {
		t.Fatal(err)
	}
	items := graphData[[]SourceItem](t, h,
		`query($mediaID:String!) { similarMedia(mediaID:$mediaID, limit:1) { id locked } }`,
		map[string]any{"mediaID": string(fx.trackA)}, "similarMedia")
	if got := itemIDs(items); !equalStringSlices(got, []string{string(fx.trackC)}) {
		t.Fatalf("similarMedia(limit:1) with the nearest hit muted = %v, want [%s]", got, fx.trackC)
	}
}

func TestSonicAnalysisToggleRejectsNonBoolean(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	h := newTestHandlerWithStore(st)
	h.sonicAnalysisAvailable = func() bool { return true }
	h.sonicAnalysisSweep = func(context.Context) {}
	body, err := json.Marshal(map[string]any{
		"query":     `mutation($libraryID:String!, $enabled:Boolean!) { setSonicAnalysisEnabled(libraryID:$libraryID, enabled:$enabled) { enabled } }`,
		"variables": map[string]any{"libraryID": "library-local", "enabled": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, errs := post(t, h, string(body), authorizeManager); len(errs) == 0 {
		t.Fatal("string enabled was accepted")
	}
	if enabled, _ := st.SonicAnalysisEnabled(ctx, "library-local"); enabled {
		t.Fatal("rejected toggle changed the stored state")
	}
}

func TestStationSonicSeedStaysFirstUnderTuning(t *testing.T) {
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedSonicFixture(t, st)
	// preferRecent would rank trackB (2020) above the older seed; the seed
	// still leads and only its neighbours are biased.
	got, _, err := h.generateStation(context.Background(), defaultScope(), stationSeed{kind: "sonic", id: string(fx.trackA)}, stationRules{}, stationTuning{PreferRecent: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ids := candidateIDs(got); len(ids) == 0 || ids[0] != string(fx.trackA) {
		t.Fatalf("sonic station under preferRecent = %v, want the seed %s first", ids, fx.trackA)
	}
}
