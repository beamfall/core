package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	filmtv "github.com/beamfall/plugin-filmtv"
	music "github.com/beamfall/plugin-music"
	sdk "github.com/beamfall/plugin-sdk"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const fixMatchSourcesTestQuery = `query($mediaID: String!) {
	fixMatchSources(mediaID: $mediaID) { pluginID scraperID label supportsChildren acceptsPastedID isDefault }
}`

func TestFixMatchSourcesUsesOwningBundleAndProvenance(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{filmtv.Bundle(), music.Bundle()})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(manifest.NewService(st, time.Now, registry), auth.NewService(st), st, registry)
	ctx := t.Context()
	for _, tc := range []struct {
		name       string
		pluginID   string
		container  string
		codec      string
		provenance store.MediaMetadataProvenance
		want       []string
		firstLabel string
	}{
		{"video", "filmtv", "mkv", "h264", store.MediaMetadataProvenance{PluginID: "filmtv", ScraperID: "tvdb-series", CandidateID: "7"},
			[]string{"tvdb-series", "tmdb-movie", "tmdb-tv", "anilist-series", "anilist-movie"}, "TheTVDB (Series)"},
		{"audio", "music", "flac", "flac", store.MediaMetadataProvenance{PluginID: "filmtv", ScraperID: "tmdb-tv", CandidateID: "8"},
			[]string{"musicbrainz"}, "MusicBrainz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			library, err := st.CreateLibrary(ctx, tc.name, false, tc.pluginID, "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: library.ID, Path: "/fixtures/" + tc.name + "." + tc.container, Title: tc.name,
				Container: tc.container, Codec: tc.codec,
				ContentFingerprint: "sha256:" + tc.name,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetMediaMetadataProvenance(ctx, media.ID, tc.provenance.PluginID, tc.provenance.ScraperID, tc.provenance.CandidateID); err != nil {
				t.Fatal(err)
			}
			var sources []FixMatchSourceInfo
			execGraph(t, handler, authorizeManager, fixMatchSourcesTestQuery, map[string]any{"mediaID": string(media.ID)}, "fixMatchSources", &sources)
			var ids []string
			for i, source := range sources {
				ids = append(ids, source.ScraperID)
				if source.PluginID != tc.pluginID || source.IsDefault != (i == 0) || source.SupportsChildren {
					t.Fatalf("source %d = %+v", i, source)
				}
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("scraper IDs = %v, want %v", ids, tc.want)
			}
			if sources[0].Label != tc.firstLabel || !sources[0].AcceptsPastedID {
				t.Fatalf("default source = %+v, want label %q and pasted-ID lookup", sources[0], tc.firstLabel)
			}
		})
	}
}

func TestFixMatchReadsRequireManagerAndVisibleMedia(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)
	for _, query := range []string{
		fixMatchSourcesTestQuery,
		`query($mediaID: String!) { fixMatchCurrent(mediaID: $mediaID) { candidateID } }`,
		`query($mediaID: String!) { fixMatchLockedFields(mediaID: $mediaID) { field value } }`,
	} {
		for _, tc := range []struct {
			authorize func(*http.Request)
			mediaID   string
			message   string
		}{
			{authorizeManager, "media-archive", "unknown media"},
			{authorizeViewer, "media-alpha", "manager role required"},
		} {
			resp := postGraphJSON(t, handler, tc.authorize, query, map[string]any{"mediaID": tc.mediaID})
			if len(resp.Errors) != 1 || resp.Errors[0].Message != tc.message {
				t.Fatalf("%s with %s = %+v, want %q", query, tc.mediaID, resp.Errors, tc.message)
			}
		}
	}
}

func TestFixMatchRejectsVisibleEncryptedMedia(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	handler := newTestHandlerWithStore(st)
	handler.SetIdentify(identify.NewService(nil, nil, st))
	mgr := libencrypt.NewManagerWithParams(newMemSecretStore(), libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	libraryID := newEncryptedLibrary(t, st)
	if _, err := mgr.Provision(libraryID, libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), handler.libEncryptDBCipher()); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: store.LibraryID(libraryID), Path: "/fixtures/protected.mkv", Title: "Protected Title",
		Container: "mkv", Codec: "h264", ContentFingerprint: "sha256:protected",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionRevealed(ctx, "session-manager", true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, query string }{
		{"sources", fixMatchSourcesTestQuery},
		{"current", `query($mediaID: String!) { fixMatchCurrent(mediaID: $mediaID) { candidateID } }`},
		{"locked fields", `query($mediaID: String!) { fixMatchLockedFields(mediaID: $mediaID) { field } }`},
		{"candidates", `query($mediaID: String!) { fixMatchCandidates(mediaID: $mediaID, pluginID: "demo", scraperID: "lookup") { id } }`},
		{"preview", `query($mediaID: String!) { fixMatchPreview(mediaID: $mediaID, pluginID: "demo", scraperID: "lookup", candidateID: "42") { field } }`},
		{"replace", `mutation($mediaID: String!) { fixMatch(mediaID: $mediaID, pluginID: "demo", scraperID: "lookup", candidateID: "42") { id } }`},
		{"associate", `mutation($mediaID: String!) { fixMatch(mediaID: $mediaID, recordID: "record", importRunID: "run") { id } }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := postGraphJSON(t, handler, authorizeManager, tc.query, map[string]any{"mediaID": string(media.ID)})
			if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != ErrorCodeInvalid ||
				resp.Errors[0].Message != "fix match is unavailable in encrypted libraries" {
				t.Fatalf("errors = %#v, want encrypted-library refusal", resp.Errors)
			}
		})
	}
	for locale, want := range map[string]string{
		"fr": "La correction de la correspondance est indisponible dans les bibliothèques chiffrées.",
		"de": "Die Zuordnung kann in verschlüsselten Bibliotheken nicht korrigiert werden.",
		"es": "La corrección de coincidencias no está disponible en bibliotecas cifradas.",
		"pt": "A correção de correspondências não está disponível em bibliotecas criptografadas.",
	} {
		t.Run(locale, func(t *testing.T) {
			resp := postGraphJSON(t, handler, func(req *http.Request) {
				authorizeManager(req)
				*req = *req.WithContext(l10n.WithLocale(req.Context(), locale))
			}, fixMatchSourcesTestQuery, map[string]any{"mediaID": string(media.ID)})
			if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != ErrorCodeInvalid || resp.Errors[0].Message != want {
				t.Fatalf("errors = %#v, want localized encrypted-library refusal %q", resp.Errors, want)
			}
		})
	}
}

type rematchTestEngine struct {
	identifyTestEngine
	searchCalls int
}

func (e *rematchTestEngine) Search(ctx context.Context, spec plugin.ScraperSpec, query string, vars ...scrape.Variables) ([]scrape.Result, error) {
	e.searchCalls++
	return e.identifyTestEngine.Search(ctx, spec, query, vars...)
}

func TestFixMatchReidentifiesMatchedItemAndRespectsLocks(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	engine := &rematchTestEngine{}
	service := identify.NewService(engine, handler.plugins, st)
	service.SetAutoStores(st, st)
	service.RegisterScorer("demo", fixedScorer{scores: map[string]float64{"42": 1}}, 0.75)
	handler.SetIdentify(service)
	ctx := t.Context()
	media, first, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/fixtures/matched.mkv", Title: "Previous Title",
		Container: "mkv", Codec: "h264", ContentFingerprint: "sha256:matched",
	})
	if err != nil {
		t.Fatal(err)
	}
	part := store.MediaFile{Path: "/fixtures/matched-part2.mkv", ContentFingerprint: "sha256:matched-part2", PartOrdinal: 1}
	alternate := store.MediaFile{Path: "/fixtures/matched-alt.mkv", ContentFingerprint: "sha256:matched-alt", VersionLabel: "alternate"}
	for _, file := range []store.MediaFile{part, alternate} {
		if err := st.AddMediaFileVersion(ctx, media.ID, file, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.MarkMediaFileMissing(ctx, media.LibraryID, alternate.Path); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, media.ID, "demo", "lookup", "previous"); err != nil {
		t.Fatal(err)
	}
	title, summary := "Operator Title", "Operator Summary"
	if err := st.UpdateMediaMetadata(ctx, media.ID, store.MediaMetadata{Title: &title, Summary: &summary, Source: store.MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
		t.Fatalf("initial unmatched state = %v, %v", queued, err)
	}

	const mutation = `mutation($mediaID: String!, $candidateID: String!, $clearLocks: [String!]) {
		fixMatch(mediaID: $mediaID, pluginID: "demo", scraperID: "lookup", candidateID: $candidateID, clearLocks: $clearLocks) { title summary year }
	}`
	const lockedFieldsQuery = `query($mediaID: String!) {
		fixMatchLockedFields(mediaID: $mediaID) { field value }
	}`
	vars := map[string]any{"mediaID": string(media.ID), "candidateID": "42"}
	var matched MediaInfo
	execGraph(t, handler, authorizeManager, mutation, vars, "fixMatch", &matched)
	if matched.Title != title || matched.Summary != summary || matched.Year != 1977 {
		t.Fatalf("re-match with locks = %+v", matched)
	}
	for _, file := range []store.MediaFile{first, part, alternate} {
		if pinned, found, err := st.MatchPinned(ctx, file.ContentFingerprint); err != nil || !found || !pinned {
			t.Fatalf("match pin for %s = %v, %v, %v", file.ContentFingerprint, pinned, found, err)
		}
		outcome, err := service.AutoIdentify(ctx, media.ID, "demo", file.ContentFingerprint, identify.ParsedMedia{Title: "blue harvest"})
		if err != nil || !outcome.Skipped || outcome.SkipReason != identify.SkipPinned || outcome.Applied || outcome.Queued {
			t.Fatalf("auto-identify for %s = %+v, %v", file.ContentFingerprint, outcome, err)
		}
	}
	if engine.searchCalls != 0 {
		t.Fatalf("pinned re-match triggered %d searches", engine.searchCalls)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
		t.Fatalf("unmatched state after re-match and rescan = %v, %v", queued, err)
	}

	const currentQuery = `query($mediaID: String!) {
		fixMatchCurrent(mediaID: $mediaID) { pluginID scraperID candidateID title year }
	}`
	var current *FixMatchCurrentInfo
	execGraph(t, handler, authorizeManager, currentQuery, vars, "fixMatchCurrent", &current)
	if want := (FixMatchCurrentInfo{PluginID: "demo", ScraperID: "lookup", CandidateID: "42", Title: title, Year: 1977}); current == nil || *current != want {
		t.Fatalf("current match = %+v, want %+v", current, want)
	}
	execGraph(t, handler, authorizeManager, currentQuery, map[string]any{"mediaID": "media-alpha"}, "fixMatchCurrent", &current)
	if current != nil {
		t.Fatalf("unidentified current match = %+v, want null", current)
	}

	vars["clearLocks"] = []string{"title", "bogus"}
	rejected := postGraphJSON(t, handler, authorizeManager, mutation, vars)
	if len(rejected.Errors) != 1 || rejected.Errors[0].Extensions.Code != ErrorCodeInvalid || rejected.Errors[0].Message != "unknown lockable field" {
		t.Fatalf("invalid clearLocks = %+v", rejected.Errors)
	}
	vars["candidateID"] = "missing"
	vars["clearLocks"] = []string{"title"}
	if rejected := postGraphJSON(t, handler, authorizeManager, mutation, vars); len(rejected.Errors) != 1 {
		t.Fatalf("failed candidate lookup = %+v", rejected)
	}
	var locks []FixMatchLockedFieldInfo
	execGraph(t, handler, authorizeManager, lockedFieldsQuery, vars, "fixMatchLockedFields", &locks)
	if want := []FixMatchLockedFieldInfo{{Field: "title", Value: title}, {Field: "summary", Value: summary}}; !slices.Equal(locks, want) {
		t.Fatalf("locks after failed fixes = %+v, want %+v", locks, want)
	}

	vars["candidateID"] = "42"
	execGraph(t, handler, authorizeManager, mutation, vars, "fixMatch", &matched)
	if matched.Title != "Blue Harvest" || matched.Summary != summary {
		t.Fatalf("re-match clearing title lock = %+v", matched)
	}
	execGraph(t, handler, authorizeManager, lockedFieldsQuery, vars, "fixMatchLockedFields", &locks)
	if want := []FixMatchLockedFieldInfo{{Field: "summary", Value: summary}}; !slices.Equal(locks, want) {
		t.Fatalf("locks after clear = %+v, want %+v", locks, want)
	}

	sortTitle := "Earlier Provider Sort Key"
	if err := st.UpdateMediaMetadata(ctx, media.ID, store.MediaMetadata{SortTitle: &sortTitle}); err != nil {
		t.Fatal(err)
	}
	vars["clearLocks"] = []string{"sortTitle"}
	execGraph(t, handler, authorizeManager, mutation, vars, "fixMatch", &matched)
	if got, err := st.MediaSortTitle(ctx, media.ID, managerScope().LibraryFilter()); err != nil || got != "" {
		t.Fatalf("sort title = %q, %v; replacement with no sort key must retract the earlier provider's key", got, err)
	}
}

// METAPARITY-4 (ADR-0068 §A) — the Fix-Match full contract: the failure-reason
// taxonomy on the queue read, per-candidate confidence + source attribution,
// the paste-an-id direct fetch, and the preview-before-apply diff.

// TestUnmatchedQueueReportsFailureReasonTaxonomy pins the three exhaustive
// shapes of ADR-0068 §A's taxonomy onto real queue rows: no candidate at all,
// a best candidate under the plugin threshold, and a candidate that cleared the
// threshold yet was still parked (the fail-closed ambiguous tie).
func TestUnmatchedQueueReportsFailureReasonTaxonomy(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	for _, row := range []struct {
		path       string
		candidate  string
		confidence float64
		want       string
	}{
		{"/fixtures/none.mkv", "", 0, store.UnmatchedReasonNoCandidates},
		{"/fixtures/weak.mkv", "42", 0.5, store.UnmatchedReasonBelowThreshold},
		{"/fixtures/tied.mkv", "42", 0.9, store.UnmatchedReasonAmbiguous},
	} {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID:          "library-local",
			Path:               row.path,
			Title:              row.path,
			Container:          "mkv",
			Codec:              "h264",
			ContentFingerprint: "sha256:" + row.path,
		})
		if err != nil {
			t.Fatalf("ImportMediaFile(%s): %v", row.path, err)
		}
		if err := st.QueueUnmatched(ctx, store.UnmatchedItem{
			MediaID: media.ID, PluginID: "demo", ScraperID: "lookup",
			CandidateID: row.candidate, Confidence: row.confidence, Threshold: 0.75,
		}); err != nil {
			t.Fatalf("QueueUnmatched(%s): %v", row.path, err)
		}
		items, err := handler.unmatchedQueue(ctx, managerScope(), request{})
		if err != nil {
			t.Fatalf("unmatchedQueue: %v", err)
		}
		got := ""
		for _, item := range items {
			if item.MediaID == string(media.ID) {
				got = item.FailureReason
			}
		}
		if got != row.want {
			t.Fatalf("failureReason for %s = %q, want %q", row.path, got, row.want)
		}
	}
}

// TestFixMatchCandidatesScoresAndAttributesSource covers the "confidence +
// why-this-candidate" half of the contract: every candidate carries the
// plugin scorer's confidence against its threshold plus the plugin/scraper that
// produced it, ordered best-first, and a tie at the top is reported as the
// fail-closed "ambiguous" rather than presented as a confident answer.
func TestFixMatchCandidatesScoresAndAttributesSource(t *testing.T) {
	handler := newScoredFixMatchHandler(t, map[string]float64{"strong": 0.9, "weak": 0.4})

	got, err := handler.fixMatchCandidates(context.Background(), managerScope(), request{
		Variables: map[string]any{"pluginID": "demo", "scraperID": "lookup", "query": "blue harvest"},
	})
	if err != nil {
		t.Fatalf("fixMatchCandidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2", len(got))
	}
	if got[0].ID != "strong" || got[1].ID != "weak" {
		t.Fatalf("candidate order = %q,%q, want strong,weak", got[0].ID, got[1].ID)
	}
	if got[0].Confidence != 0.9 || got[0].Threshold != 0.75 || !got[0].Scored {
		t.Fatalf("top candidate scoring = %#v, want 0.9 over a 0.75 threshold", got[0])
	}
	if got[0].PluginID != "demo" || got[0].ScraperID != "lookup" {
		t.Fatalf("top candidate source = %q/%q, want demo/lookup", got[0].PluginID, got[0].ScraperID)
	}
	if got[0].Reason != "" {
		t.Fatalf("sole above-threshold candidate reason = %q, want no reason", got[0].Reason)
	}
	if got[1].Reason != store.UnmatchedReasonBelowThreshold {
		t.Fatalf("runner-up reason = %q, want below-threshold", got[1].Reason)
	}
}

// TestFixMatchCandidatesReportsAmbiguousTie is the fail-closed tie rule:
// two candidates sharing the top score are ambiguous even though
// both clear the threshold, so the Console never renders a coin flip as certain.
func TestFixMatchCandidatesReportsAmbiguousTie(t *testing.T) {
	handler := newScoredFixMatchHandler(t, map[string]float64{"strong": 0.9, "weak": 0.9})

	got, err := handler.fixMatchCandidates(context.Background(), managerScope(), request{
		Variables: map[string]any{"pluginID": "demo", "scraperID": "lookup", "query": "blue harvest"},
	})
	if err != nil {
		t.Fatalf("fixMatchCandidates: %v", err)
	}
	if got[0].Reason != store.UnmatchedReasonAmbiguous {
		t.Fatalf("tied top reason = %q, want ambiguous", got[0].Reason)
	}
}

// TestFixMatchCandidatesUnscoredWithoutScorer keeps a plugin with no registered
// MatchScorer honest: the Console is told the candidates are unscored rather
// than being handed a meaningless 0% confidence.
func TestFixMatchCandidatesUnscoredWithoutScorer(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)

	got, err := handler.fixMatchCandidates(context.Background(), managerScope(), request{
		Variables: map[string]any{"pluginID": "demo", "scraperID": "lookup", "query": "blue harvest"},
	})
	if err != nil {
		t.Fatalf("fixMatchCandidates: %v", err)
	}
	if len(got) != 1 || got[0].Scored || got[0].Reason != candidateReasonUnscored {
		t.Fatalf("unscored candidates = %#v, want one unscored row", got)
	}
}

// TestIdentifyLookupFetchesPastedID covers the paste-a-provider-id path: the
// operator who already knows the id skips search entirely, and a bad id is a
// clean error rather than a silent empty candidate.
func TestIdentifyLookupFetchesPastedID(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)
	body := `{"query":"query IdentifyLookup($pluginID: String!, $scraperID: String!, $candidateID: String!) { identifyLookup(pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id title summary year } }","variables":{"pluginID":"demo","scraperID":"lookup","candidateID":"42"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			IdentifyLookup IdentifyCandidate `json:"identifyLookup"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if resp.Data.IdentifyLookup.Title != "Blue Harvest" || resp.Data.IdentifyLookup.Year != 1977 {
		t.Fatalf("identifyLookup = %#v, want the pasted candidate hydrated", resp.Data.IdentifyLookup)
	}

	if _, err := handler.identifyLookup(context.Background(), managerScope(), request{
		Variables: map[string]any{"pluginID": "demo", "scraperID": "lookup", "candidateID": "nope"},
	}); err == nil {
		t.Fatal("identifyLookup with an unknown id succeeded, want an error")
	}
}

// TestFixMatchPreviewDiffsWithoutWriting is the preview-before-apply guarantee:
// the diff names current vs proposed for every core descriptive field AND the
// Media row is byte-for-byte untouched afterwards.
func TestFixMatchPreviewDiffsWithoutWriting(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/fixtures/preview.mkv",
		Title:              "preview",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:preview",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	diff, err := handler.fixMatchPreview(ctx, managerScope(), request{
		Variables: map[string]any{
			"mediaID": string(media.ID), "pluginID": "demo",
			"scraperID": "lookup", "candidateID": "42",
		},
	})
	if err != nil {
		t.Fatalf("fixMatchPreview: %v", err)
	}
	want := map[string]FixMatchFieldDiff{
		"title":   {Field: "title", Current: "preview", Proposed: "Blue Harvest", Changed: true},
		"summary": {Field: "summary", Current: "", Proposed: "A classic.", Changed: true},
		"year":    {Field: "year", Current: "", Proposed: "1977", Changed: true},
	}
	if len(diff) != len(want) {
		t.Fatalf("diff = %#v, want %d fields", diff, len(want))
	}
	for _, row := range diff {
		if row != want[row.Field] {
			t.Fatalf("diff[%s] = %#v, want %#v", row.Field, row, want[row.Field])
		}
	}

	after, err := st.Media(ctx, media.ID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "preview" || after.Summary != "" || after.Year != 0 {
		t.Fatalf("preview wrote to the Media row: %#v", after)
	}
}

// stubMediaTitles reports one fixed parser-owned title for every Media.
type stubMediaTitles string

func (s stubMediaTitles) ParsedMediaTitle(context.Context, store.MediaID, string) (string, error) {
	return string(s), nil
}

// TestFixMatchPreviewProposesTheAppliedTitle (CORE-1713): when the plugin's
// parser owns the Media's display title, Apply keeps it, so the preview
// proposes it too rather than the scraper's title.
func TestFixMatchPreviewProposesTheAppliedTitle(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	handler.identify.SetMediaTitles(stubMediaTitles("S01E03"))
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/fixtures/episode.mkv", Title: "S01E03",
		Container: "mkv", Codec: "h264", ContentFingerprint: "sha256:episode",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	vars := map[string]any{"mediaID": string(media.ID), "pluginID": "demo", "scraperID": "lookup", "candidateID": "42"}
	diff, err := handler.fixMatchPreview(ctx, managerScope(), request{Variables: vars})
	if err != nil {
		t.Fatalf("fixMatchPreview: %v", err)
	}
	for _, row := range diff {
		if row.Field == "title" && (row.Proposed != "S01E03" || row.Changed) {
			t.Fatalf("title diff = %#v, want the parser title kept unchanged", row)
		}
	}
	if err := handler.identify.Apply(ctx, media.ID, "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	after, err := st.Media(ctx, media.ID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "S01E03" || after.Summary != "A classic." {
		t.Fatalf("applied media = title %q summary %q, want the previewed title and the lookup summary", after.Title, after.Summary)
	}
}

// stubIdentifiedTitles is a stubMediaTitles whose parser also names the file
// from the identification's "phase" field value.
type stubIdentifiedTitles struct{ stubMediaTitles }

func (s stubIdentifiedTitles) IdentifiedMediaTitle(_ context.Context, _ store.MediaID, _ string, fields map[string]string) (string, error) {
	return string(s.stubMediaTitles) + " · " + fields["phase"], nil
}

// TestFixMatchPreviewProposesTheIdentifiedTitle (CORE-2470): when the parser
// builds the title from the identification's field values, the preview
// proposes that title and Apply writes it.
func TestFixMatchPreviewProposesTheIdentifiedTitle(t *testing.T) {
	handler, st := newIdentifyTestHandler(t, func(bundle *plugin.Bundle) {
		bundle.MediaFields = []plugin.FieldSpec{{Name: "phase", Type: "text"}}
		bundle.Scrapers[0].ImpliedMediaFields = map[string]string{"media.phase": "Smells Like Mean Spirit"}
	})
	handler.identify.SetMediaTitles(stubIdentifiedTitles{stubMediaTitles("S03E01")})
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/fixtures/episode.mkv", Title: "S03E01",
		Container: "mkv", Codec: "h264", ContentFingerprint: "sha256:episode",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	const want = "S03E01 · Smells Like Mean Spirit"
	vars := map[string]any{"mediaID": string(media.ID), "pluginID": "demo", "scraperID": "lookup", "candidateID": "42"}
	diff, err := handler.fixMatchPreview(ctx, managerScope(), request{Variables: vars})
	if err != nil {
		t.Fatalf("fixMatchPreview: %v", err)
	}
	for _, row := range diff {
		if row.Field == "title" && (row.Proposed != want || !row.Changed) {
			t.Fatalf("title diff = %#v, want %q proposed", row, want)
		}
	}
	if err := handler.identify.Apply(ctx, media.ID, "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	after, err := st.Media(ctx, media.ID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != want {
		t.Fatalf("applied title = %q, want the previewed %q", after.Title, want)
	}
}

// TestFixMatchPreviewRejectsHiddenMedia keeps the preview on the same
// reveal-and-Library posture as fixMatch — a preview must not leak a hidden
// item's current metadata.
func TestFixMatchPreviewRejectsHiddenMedia(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)
	_, err := handler.fixMatchPreview(context.Background(), auth.Scope{}, request{
		Variables: map[string]any{
			"mediaID": "media-archive", "pluginID": "demo",
			"scraperID": "lookup", "candidateID": "42",
		},
	})
	if err == nil || err.Error() != "unknown media" {
		t.Fatalf("fixMatchPreview on a hidden media = %v, want unknown media", err)
	}
}

// managerScope is the all-Libraries revealed scope the resolver-level tests use;
// the HTTP-level tests exercise the role gate itself.
func managerScope() auth.Scope {
	return auth.Scope{ProfileID: "profile-default", Revealed: true, AllLibraries: true}
}

// twoCandidateEngine returns two candidates so ordering, the threshold split,
// and the fail-closed tie rule are all observable.
type twoCandidateEngine struct{}

func (twoCandidateEngine) Search(_ context.Context, _ plugin.ScraperSpec, _ string, _ ...scrape.Variables) ([]scrape.Result, error) {
	return []scrape.Result{
		{ID: "weak", Fields: map[string]any{"title": "Blue Harvest (TV)", "year": "1980"}},
		{ID: "strong", Fields: map[string]any{"title": "Blue Harvest", "year": "1977"}},
	}, nil
}

func (twoCandidateEngine) Lookup(_ context.Context, _ plugin.ScraperSpec, id string, _ ...scrape.Variables) (scrape.Result, error) {
	return scrape.Result{}, errors.New("not found: " + id)
}

// fixedScorer scores by candidate id so a test states the exact confidences it
// wants without depending on any real scoring heuristic.
type fixedScorer struct{ scores map[string]float64 }

func (s fixedScorer) Score(candidate sdk.Candidate, _ sdk.ParsedMedia) float64 {
	return s.scores[candidate.ID]
}

// newScoredFixMatchHandler wires the two-candidate engine with a scorer whose
// per-id confidences the caller dictates, against a 0.75 auto-apply threshold.
func newScoredFixMatchHandler(t *testing.T, scores map[string]float64) *Handler {
	t.Helper()
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "demo",
		Version:     "0.1.0",
		DisplayName: plugin.L("Demo"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		Scrapers: []plugin.ScraperSpec{{
			ID:              "lookup",
			DisplayName:     plugin.L("Demo Lookup"),
			Kind:            "http-json",
			Endpoint:        "https://demo.example.test",
			SearchTemplate:  "/search?q={query}",
			CandidatesPath:  "results",
			CandidateIDPath: "id",
			LookupTemplate:  "/title/{id}",
			FieldMap:        map[string]string{"title": "name", "year": "year"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	service := identify.NewService(twoCandidateEngine{}, registry, st)
	service.RegisterScorer("demo", fixedScorer{scores: scores}, 0.75)
	handler.SetIdentify(service)
	return handler
}

// yearScorer gives every candidate the same title score and adds a little when
// the candidate's year equals the parsed year, so only a year can break a tie.
type yearScorer struct{}

func (yearScorer) Score(candidate sdk.Candidate, parsed sdk.ParsedMedia) float64 {
	if parsed.Year != 0 && candidate.Year == parsed.Year {
		return 0.95
	}
	return 0.9
}

// TestFixMatchCandidatesYearBreaksTie is CORE-2910: equal titles tie without a
// year, and the operator's year makes the matching candidate the clear answer.
func TestFixMatchCandidatesYearBreaksTie(t *testing.T) {
	handler := newScoredFixMatchHandler(t, nil)
	handler.identify.RegisterScorer("demo", yearScorer{}, 0.75)
	vars := func(extra map[string]any) request {
		v := map[string]any{"pluginID": "demo", "scraperID": "lookup", "query": "blue harvest"}
		for k, val := range extra {
			v[k] = val
		}
		return request{Variables: v}
	}

	got, err := handler.fixMatchCandidates(context.Background(), managerScope(), vars(nil))
	if err != nil {
		t.Fatalf("fixMatchCandidates without year: %v", err)
	}
	if got[0].Reason != store.UnmatchedReasonAmbiguous {
		t.Fatalf("no-year top reason = %q, want ambiguous", got[0].Reason)
	}

	got, err = handler.fixMatchCandidates(context.Background(), managerScope(), vars(map[string]any{"year": float64(1980)}))
	if err != nil {
		t.Fatalf("fixMatchCandidates with year: %v", err)
	}
	if got[0].ID != "weak" || got[0].Year != 1980 || got[0].Reason != "" {
		t.Fatalf("year 1980 top = %#v, want the 1980 candidate with no reason", got[0])
	}

	for _, bad := range []float64{-1, 0} {
		if _, err := handler.fixMatchCandidates(context.Background(), managerScope(), vars(map[string]any{"year": bad})); err == nil {
			t.Fatalf("year %v accepted, want a client error", bad)
		}
	}
	if _, err := handler.fixMatchCandidates(context.Background(), managerScope(), vars(map[string]any{"year": 2020.5})); err == nil {
		t.Fatal("fractional year accepted, want a client error")
	}
}
