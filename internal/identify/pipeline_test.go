package identify_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

func TestAutoIdentifySkipsUnkeyedOptionalAuthScraperOnce(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	spec := autoScraper()
	spec.AuthRef = "film-key"
	spec.OptionalAuth = true
	engine := &fakeEngine{}
	queue := &fakeQueue{}
	service := identify.NewService(engine, demoBundles(spec), &fakeStore{})
	service.RegisterScorer("demo", titleScorer{}, 0.75)
	service.SetAutoStores(queue, fakePins{})

	for i := 0; i < 2; i++ {
		outcome, err := service.AutoIdentify(context.Background(), "media-alpha", "demo", "", identify.ParsedMedia{Title: "Blue Harvest", Class: "movie"})
		if err != nil {
			t.Fatal(err)
		}
		if !outcome.Skipped || outcome.Applied || outcome.Queued {
			t.Fatalf("outcome = %#v, want Skipped", outcome)
		}
	}
	if engine.searchedSpec.ID != "" {
		t.Fatalf("optional-auth scraper was searched: %q", engine.searchedSpec.ID)
	}
	if got := strings.Count(logs.String(), "optional-auth scraper unavailable"); got != 1 {
		t.Fatalf("skip log count = %d, want 1; logs=%q", got, logs.String())
	}

	keyedEngine := &fakeEngine{credentials: map[string]bool{"film-key": true}}
	keyedService := identify.NewService(keyedEngine, demoBundles(spec), &fakeStore{})
	keyedService.RegisterScorer("demo", titleScorer{}, 0.75)
	keyedService.SetAutoStores(&fakeQueue{}, fakePins{})
	if _, err := keyedService.AutoIdentify(context.Background(), "media-beta", "demo", "", identify.ParsedMedia{Title: "Blue Harvest", Class: "movie"}); err != nil {
		t.Fatal(err)
	}
	if keyedEngine.searchedSpec.ID != spec.ID {
		t.Fatalf("configured optional-auth scraper searched as %q, want %q", keyedEngine.searchedSpec.ID, spec.ID)
	}
}

// titleScorer scores a candidate 1.0 when its title exactly matches the parsed
// title, else 0.0 — a deterministic stand-in for the compiled MatchScorer so the
// pipeline's above/below-threshold branches are exercised without the Film & TV
// scoring math.
type titleScorer struct{}

func (titleScorer) Score(candidate identify.Candidate, parsed identify.ParsedMedia) float64 {
	if candidate.Title == parsed.Title && parsed.Title != "" {
		return 1.0
	}
	return 0.0
}

type scoreByIDScorer map[string]float64

func (s scoreByIDScorer) Score(candidate identify.Candidate, _ identify.ParsedMedia) float64 {
	return s[candidate.ID]
}

// fakeQueue records Unmatched-queue writes and clears.
type fakeQueue struct {
	queued  []store.UnmatchedItem
	cleared []store.MediaID
}

func (q *fakeQueue) QueueUnmatched(_ context.Context, item store.UnmatchedItem) error {
	q.queued = append(q.queued, item)
	return nil
}

func (q *fakeQueue) ClearUnmatched(_ context.Context, mediaID store.MediaID) (bool, error) {
	q.cleared = append(q.cleared, mediaID)
	return true, nil
}

// fakePins reports a fixed pinned/found pair for any fingerprint.
type fakePins struct {
	pinned bool
	found  bool
}

func (p fakePins) MatchPinned(_ context.Context, _ string) (bool, bool, error) {
	return p.pinned, p.found, nil
}

// autoScraper is a searchable scraper for the "movie" class — SearchTemplate +
// CandidateIDPath make it eligible for the auto-identify pipeline, and the
// implied class binds it to the parsed class.
func autoScraper() plugin.ScraperSpec {
	return plugin.ScraperSpec{
		ID:                 "tmdb-movie",
		Endpoint:           "https://api.example.test",
		SearchTemplate:     "/search?query={query}",
		CandidatesPath:     "results",
		CandidateIDPath:    "id",
		LookupTemplate:     "/movie/{id}",
		FieldMap:           map[string]string{"title": "title", "year": "year"},
		ImpliedMediaFields: map[string]string{"media.class": "movie"},
	}
}

// newAutoService wires a Service with the titleScorer registered for "demo" at
// the given threshold, plus the fake queue and pin stores.
func newAutoService(t *testing.T, engine *fakeEngine, threshold float64, queue *fakeQueue, pins fakePins) *identify.Service {
	t.Helper()
	service := identify.NewService(engine, demoBundles(autoScraper()), &fakeStore{})
	service.RegisterScorer("demo", titleScorer{}, threshold)
	service.SetAutoStores(queue, pins)
	return service
}

// (a) A candidate above the plugin's threshold auto-applies on scan and does NOT
// queue — the autonomous path (invariant #6, Trash ON).
func TestAutoIdentifyAppliesAboveThreshold(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{
		searchResults: []scrape.Result{
			{ID: "10", Fields: map[string]any{"title": "Blue Harvest", "year": "1977"}},
			{ID: "11", Fields: map[string]any{"title": "Red Harvest", "year": "1980"}},
		},
		lookupResult: scrape.Result{Fields: map[string]any{"title": "Blue Harvest", "year": "1977"}},
	}
	queue := &fakeQueue{}
	service := newAutoService(t, engine, 0.75, queue, fakePins{})

	outcome, err := service.AutoIdentify(context.Background(), "media-alpha", "demo", "sha256:alpha",
		identify.ParsedMedia{Title: "Blue Harvest", Class: "movie", Year: 1977})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Applied || outcome.Queued || outcome.Skipped {
		t.Fatalf("outcome = %#v, want Applied", outcome)
	}
	if engine.lookedUpID != "10" {
		t.Fatalf("auto-applied candidate %q, want the matching 10", engine.lookedUpID)
	}
	if len(queue.queued) != 0 {
		t.Fatalf("auto-applied item also queued: %#v", queue.queued)
	}
	// A successful auto-apply clears any stale queue row.
	if len(queue.cleared) != 1 || queue.cleared[0] != "media-alpha" {
		t.Fatalf("auto-apply did not clear the queue row: %#v", queue.cleared)
	}
}

// A top-score tie among auto-apply-worthy candidates is a genuine ambiguity: the
// scorer cannot tell them apart, so the pipeline must NOT silently attach the
// first (the old first-wins behaviour). Two identical-title candidates both score
// 1.0 with titleScorer; the fail-closed tie rule demotes the decision
// to non-auto-apply and the item routes to the Unmatched queue for a human. This
// test would fail under the previous first-wins tie handling.
func TestAutoIdentifyTiedTopDefersToManual(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{
		searchResults: []scrape.Result{
			{ID: "10", Fields: map[string]any{"title": "Blue Harvest", "year": "1977"}},
			{ID: "11", Fields: map[string]any{"title": "Blue Harvest", "year": "1980"}},
		},
	}
	queue := &fakeQueue{}
	service := newAutoService(t, engine, 0.75, queue, fakePins{})

	outcome, err := service.AutoIdentify(context.Background(), "media-tie", "demo", "sha256:tie",
		identify.ParsedMedia{Title: "Blue Harvest", Class: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Applied || outcome.Skipped || !outcome.Queued {
		t.Fatalf("outcome = %#v, want Queued (ambiguous tie must not auto-apply)", outcome)
	}
	if engine.lookedUpID != "" {
		t.Fatalf("tied candidates auto-applied %q — a top-score tie must defer to manual", engine.lookedUpID)
	}
	if len(queue.queued) != 1 {
		t.Fatalf("len(queued) = %d, want 1 (the ambiguous item is parked)", len(queue.queued))
	}
}

func TestAutoIdentifyPopularityBreaksExactTopScoreTie(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{
		searchResults: []scrape.Result{
			{ID: "10", Fields: map[string]any{"title": "Blue Harvest", "popularity": 10}},
			{ID: "11", Fields: map[string]any{"title": "Blue Harvest", "popularity": 100}},
		},
		lookupResult: scrape.Result{Fields: map[string]any{"title": "Blue Harvest"}},
	}
	queue := &fakeQueue{}
	service := newAutoService(t, engine, 0.75, queue, fakePins{})

	outcome, err := service.AutoIdentify(context.Background(), "media-popular", "demo", "sha256:popular",
		identify.ParsedMedia{Title: "Blue Harvest", Class: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Applied || outcome.Queued || outcome.Skipped {
		t.Fatalf("outcome = %#v, want Applied", outcome)
	}
	if engine.lookedUpID != "11" {
		t.Fatalf("popularity tiebreak applied %q, want the more popular 11", engine.lookedUpID)
	}
	if len(queue.queued) != 0 {
		t.Fatalf("popularity-resolved item also queued: %#v", queue.queued)
	}
}

func TestAutoIdentifyPopularityDoesNotBreakNearScoreAmbiguity(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{
		searchResults: []scrape.Result{
			{ID: "10", Fields: map[string]any{"title": "Blue Harvest", "popularity": 10}},
			{ID: "11", Fields: map[string]any{"title": "Blue Harvest", "popularity": 100}},
		},
		lookupResult: scrape.Result{Fields: map[string]any{"title": "Blue Harvest"}},
	}
	queue := &fakeQueue{}
	service := identify.NewService(engine, demoBundles(autoScraper()), &fakeStore{})
	service.RegisterScorer("demo", scoreByIDScorer{"10": 1, "11": 0.99}, 0.75)
	service.SetAutoStores(queue, fakePins{})

	outcome, err := service.AutoIdentify(context.Background(), "media-near-popular", "demo", "sha256:near-popular",
		identify.ParsedMedia{Title: "Blue Harvest", Class: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Applied || outcome.Queued || outcome.Skipped {
		t.Fatalf("outcome = %#v, want Applied", outcome)
	}
	if engine.lookedUpID != "10" {
		t.Fatalf("near-score popularity tiebreak applied %q, want the stronger score 10", engine.lookedUpID)
	}
}

// (b) Below the threshold, AutoIdentify writes an Unmatched-queue row and applies
// nothing — never guess.
func TestAutoIdentifyQueuesBelowThreshold(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{
		searchResults: []scrape.Result{
			{ID: "10", Fields: map[string]any{"title": "Totally Different", "year": "1990"}},
		},
	}
	queue := &fakeQueue{}
	service := newAutoService(t, engine, 0.75, queue, fakePins{})

	outcome, err := service.AutoIdentify(context.Background(), "media-beta", "demo", "sha256:beta",
		identify.ParsedMedia{Title: "Blue Harvest", Class: "movie", Year: 1977})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Applied || outcome.Skipped || !outcome.Queued {
		t.Fatalf("outcome = %#v, want Queued", outcome)
	}
	if engine.lookedUpID != "" {
		t.Fatalf("below-threshold item was applied (looked up %q)", engine.lookedUpID)
	}
	if len(queue.queued) != 1 {
		t.Fatalf("len(queued) = %d, want 1", len(queue.queued))
	}
	row := queue.queued[0]
	if row.MediaID != "media-beta" || row.PluginID != "demo" || row.ScraperID != "tmdb-movie" {
		t.Fatalf("queued row = %#v", row)
	}
	if row.Confidence >= row.Threshold {
		t.Fatalf("queued row confidence %.2f >= threshold %.2f, should be below", row.Confidence, row.Threshold)
	}
	if row.CandidateID != "10" || row.CandidateTitle != "Totally Different" {
		t.Fatalf("queued row did not carry the best guess: %#v", row)
	}
}

// A pinned match (an operator's prior Fix-match) is left alone on rescan — the
// pipeline Skips without searching or applying. The unit half of test (c); the
// scan-level rescan-survives-pin assertion lives in internal/scan.
func TestAutoIdentifySkipsPinnedMatch(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{searchResults: []scrape.Result{{ID: "10", Fields: map[string]any{"title": "Blue Harvest"}}}}
	queue := &fakeQueue{}
	service := newAutoService(t, engine, 0.75, queue, fakePins{pinned: true, found: true})

	outcome, err := service.AutoIdentify(context.Background(), "media-pinned", "demo", "sha256:pinned",
		identify.ParsedMedia{Title: "Blue Harvest", Class: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Skipped || outcome.Applied || outcome.Queued {
		t.Fatalf("outcome = %#v, want Skipped (pinned)", outcome)
	}
	if engine.searchedSpec.ID != "" {
		t.Fatalf("pinned match was searched (spec %q) — rescan must leave it alone", engine.searchedSpec.ID)
	}
	if len(queue.queued) != 0 {
		t.Fatalf("pinned match was queued: %#v", queue.queued)
	}
}

// A plugin with no registered scorer keeps the manual-only behaviour (ADR-0044
// §2): AutoIdentify Skips without touching the queue.
func TestAutoIdentifySkipsWithoutScorer(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{}
	queue := &fakeQueue{}
	service := identify.NewService(engine, demoBundles(autoScraper()), &fakeStore{})
	service.SetAutoStores(queue, fakePins{})

	outcome, err := service.AutoIdentify(context.Background(), "media-x", "demo", "sha256:x",
		identify.ParsedMedia{Title: "Blue Harvest", Class: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Skipped {
		t.Fatalf("outcome = %#v, want Skipped (no scorer)", outcome)
	}
	if len(queue.queued) != 0 {
		t.Fatalf("no-scorer item queued: %#v", queue.queued)
	}
}

func TestQueueManualParksScraperlessPlugin(t *testing.T) {
	t.Parallel()
	queue := &fakeQueue{}
	service := identify.NewService(&fakeEngine{}, demoBundles(plugin.ScraperSpec{}), &fakeStore{})
	service.SetAutoStores(queue, fakePins{})

	outcome, err := service.QueueManual(context.Background(), "media-family", "demo", "sha256:family",
		identify.ParsedMedia{Title: "Camping Weekend", Class: "home-video"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Applied || outcome.Skipped || !outcome.Queued {
		t.Fatalf("outcome = %#v, want Queued", outcome)
	}
	if len(queue.queued) != 1 {
		t.Fatalf("len(queued) = %d, want 1", len(queue.queued))
	}
	row := queue.queued[0]
	if row.MediaID != "media-family" || row.PluginID != "demo" {
		t.Fatalf("queued row = %#v", row)
	}
	if row.ScraperID != "manual" || row.CandidateID != "" || row.Confidence != 0 || row.Threshold != 0 {
		t.Fatalf("manual queue row should carry only the manual source marker and title hint: %#v", row)
	}
	if row.CandidateTitle != "Camping Weekend" {
		t.Fatalf("CandidateTitle = %q, want parsed title hint", row.CandidateTitle)
	}
}

func TestQueueManualSkipsPinnedMatch(t *testing.T) {
	t.Parallel()
	queue := &fakeQueue{}
	service := identify.NewService(&fakeEngine{}, demoBundles(plugin.ScraperSpec{}), &fakeStore{})
	service.SetAutoStores(queue, fakePins{pinned: true, found: true})

	outcome, err := service.QueueManual(context.Background(), "media-family", "demo", "sha256:family",
		identify.ParsedMedia{Title: "Camping Weekend", Class: "home-video"})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Skipped || outcome.Applied || outcome.Queued {
		t.Fatalf("outcome = %#v, want Skipped (pinned)", outcome)
	}
	if len(queue.queued) != 0 {
		t.Fatalf("pinned manual item queued: %#v", queue.queued)
	}
}
