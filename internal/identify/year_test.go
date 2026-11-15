package identify_test

import (
	"context"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
)

// yearEngine answers Search by the year it is given, like a source with a
// year filter (CORE-1128), and records every year searched.
type yearEngine struct {
	fakeEngine
	byYear map[int][]scrape.Result
	years  []int
}

func (y *yearEngine) Search(_ context.Context, spec plugin.ScraperSpec, query string, vars ...scrape.Variables) ([]scrape.Result, error) {
	y.searchedSpec, y.searchedQuery = spec, query
	year := 0
	if len(vars) > 0 {
		year = vars[0].Year
	}
	y.years = append(y.years, year)
	return y.byYear[year], nil
}

func yearScraper(template string) plugin.ScraperSpec {
	spec := autoScraper()
	spec.SearchTemplate = template
	return spec
}

func harborLights(id, year string) scrape.Result {
	return scrape.Result{ID: id, Fields: map[string]any{"title": "Harbor Lights", "year": year}}
}

func runYearIdentify(t *testing.T, engine *yearEngine, spec plugin.ScraperSpec, year int) identify.AutoOutcome {
	t.Helper()
	service := identify.NewService(engine, demoBundles(spec), &fakeStore{})
	service.RegisterScorer("demo", titleScorer{}, 0.75)
	service.SetAutoStores(&fakeQueue{}, fakePins{})
	outcome, err := service.AutoIdentify(context.Background(), "media-harbor", "demo", "",
		identify.ParsedMedia{Title: "Harbor Lights", Class: "movie", Year: year})
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

// Two same-titled candidates tie without a year; the parsed year narrows the
// search to the right one, which then applies.
func TestAutoIdentifyPassesParsedYearToTheSearch(t *testing.T) {
	t.Parallel()
	engine := &yearEngine{byYear: map[int][]scrape.Result{
		0:    {harborLights("1998-id", "1998"), harborLights("2021-id", "2021")},
		2021: {harborLights("2021-id", "2021")},
	}}
	engine.lookupResult = harborLights("2021-id", "2021")
	outcome := runYearIdentify(t, engine, yearScraper("/search?query={query}&year={year}"), 2021)
	if !outcome.Applied || engine.lookedUpID != "2021-id" {
		t.Fatalf("outcome = %#v looked up %q, want 2021-id applied", outcome, engine.lookedUpID)
	}
	if !slices.Equal(engine.years, []int{2021}) {
		t.Fatalf("searched years = %v, want [2021]", engine.years)
	}
}

// A year filter that excludes every candidate (the file's year was an air or
// later release date, say) retries once without the year.
func TestAutoIdentifyRetriesWithoutYearWhenTheFilterFindsNothing(t *testing.T) {
	t.Parallel()
	engine := &yearEngine{byYear: map[int][]scrape.Result{
		0: {harborLights("2021-id", "2021")},
	}}
	engine.lookupResult = harborLights("2021-id", "2021")
	outcome := runYearIdentify(t, engine, yearScraper("/search?query={query}&year={year}"), 2023)
	if !outcome.Applied || engine.lookedUpID != "2021-id" {
		t.Fatalf("outcome = %#v looked up %q, want 2021-id applied after the retry", outcome, engine.lookedUpID)
	}
	if !slices.Equal(engine.years, []int{2023, 0}) {
		t.Fatalf("searched years = %v, want [2023 0]", engine.years)
	}
}

// A scraper whose template has no {year}, or an item with no known year,
// searches once with no year.
func TestAutoIdentifyLeavesYearUnsetWhenUnusedOrUnknown(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		template string
		year     int
	}{
		"template without year": {"/search?query={query}", 2021},
		"unknown year":          {"/search?query={query}&year={year}", 0},
	} {
		engine := &yearEngine{byYear: map[int][]scrape.Result{0: {harborLights("2021-id", "2021")}}}
		engine.lookupResult = harborLights("2021-id", "2021")
		runYearIdentify(t, engine, yearScraper(tc.template), tc.year)
		if !slices.Equal(engine.years, []int{0}) {
			t.Errorf("%s: searched years = %v, want [0]", name, engine.years)
		}
	}
}
