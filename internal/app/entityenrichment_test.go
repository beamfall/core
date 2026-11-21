package app

import (
	"testing"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
)

func testEntityEnrichment() EntityEnrichment {
	return EntityEnrichment{
		PluginID: testHydrationPluginID, Key: "classify", Kind: "album", MediaRelation: "album-track",
		ContextRelation: "album-artist", TargetKind: "genre", Relation: "classifies",
		Spec:  plugin.ScraperSpec{ID: "local", Endpoint: "https://provider.test/api"},
		Query: func(identify.EnrichmentSubject) string { return "q" },
		Pick:  func(identify.EnrichmentSubject, []scrape.Result) (string, bool) { return "", false },
		Names: func(scrape.Result) []string { return nil },
	}
}

// Only enrichments an activated bundle can back reach the job: their kinds and
// relations are declared, and they run on a declared scraper's endpoint.
func TestActiveEntityEnrichmentsKeepsOnlyDeclaredEnrichments(t *testing.T) {
	bundle := testHydrationBundle()
	bundle.Scrapers = append(bundle.Scrapers, plugin.ScraperSpec{ID: "local", DisplayName: plugin.L("Local"), Kind: "http-json", Endpoint: "https://provider.test/api", SearchTemplate: "/search?q={query}", CandidatesPath: "hits", CandidateIDPath: "id", LookupTemplate: "/item/{id}"})
	registry, err := plugin.NewRegistry([]plugin.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	valid := testEntityEnrichment()
	broken := func(edit func(*EntityEnrichment)) EntityEnrichment {
		enrichment := testEntityEnrichment()
		edit(&enrichment)
		return enrichment
	}
	enrichments := []EntityEnrichment{
		valid,
		broken(func(e *EntityEnrichment) { e.PluginID = "not-activated" }),
		broken(func(e *EntityEnrichment) { e.Kind = "playlist" }),
		broken(func(e *EntityEnrichment) { e.TargetKind = "mood" }),
		broken(func(e *EntityEnrichment) { e.MediaRelation = "album-artist" }),
		broken(func(e *EntityEnrichment) { e.ContextRelation = "missing" }),
		broken(func(e *EntityEnrichment) { e.Relation = "appears-on" }),
		broken(func(e *EntityEnrichment) { e.Spec.ID = "remote" }),
		broken(func(e *EntityEnrichment) { e.Spec.Endpoint = "https://elsewhere.test" }),
		broken(func(e *EntityEnrichment) { e.Names = nil }),
	}

	got := activeEntityEnrichments(registry, enrichments, nil)

	if len(got) != 1 || got[0].Key != valid.Key || got[0].Spec.Endpoint != valid.Spec.Endpoint {
		t.Fatalf("active enrichments = %+v, want only the valid one", got)
	}
	if got := activeEntityEnrichments(nil, enrichments, nil); got != nil {
		t.Fatalf("enrichments without a registry = %+v, want none", got)
	}
}
