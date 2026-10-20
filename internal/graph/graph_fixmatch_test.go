package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/beamfall/plugin-sdk"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

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
