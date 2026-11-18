package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestLibraryHealthFollowsPluginScraper covers CORE-2340 and CORE-2383 through
// the graph: a Library whose plugin has a searchable scraper reports the
// identification and artwork dimensions and accepts their remediation, while a
// Library whose plugin has no scraper reports no-scraper instead, omits the
// dimensions that cannot apply and refuses rescans as not applicable.
func TestLibraryHealthFollowsPluginScraper(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	registry, err := newTestPluginRegistry([]plugin.Bundle{
		{
			ID:          "demo",
			Version:     "0.1.0",
			DisplayName: plugin.L("Demo"),
			MediaLabel:  plugin.L("Item"),
			Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
			MediaFields: []plugin.FieldSpec{{Name: "class", Type: "text", Label: plugin.L("Class")}},
			Scrapers: []plugin.ScraperSpec{{
				ID:                 "lookup",
				DisplayName:        plugin.L("Demo Lookup"),
				Kind:               "http-json",
				Endpoint:           "https://demo.example.test",
				SearchTemplate:     "/search?q={query}",
				CandidatesPath:     "results",
				CandidateIDPath:    "id",
				LookupTemplate:     "/title/{id}",
				FieldMap:           map[string]string{"title": "name", "summary": "overview", "year": "year"},
				ArtworkMap:         map[string]string{"poster": "poster_path"},
				ImpliedMediaFields: map[string]string{"media.class": "video"},
			}},
		},
		{
			ID:          "plain",
			Version:     "0.1.0",
			DisplayName: plugin.L("Plain"),
			MediaLabel:  plugin.L("Item"),
			Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	service := identify.NewService(identifyTestEngine{}, registry, st)
	service.RegisterScorer("demo", fixedScorer{}, 0.75)
	handler.SetIdentify(service)
	st.SetLibraryHealthProfiler(service.HealthProfile)
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	scraped, err := st.CreateLibraryWithRoots(ctx, "Scraped", false, "demo", "profile-default", []string{"/media/scraped"}, "")
	if err != nil {
		t.Fatalf("CreateLibraryWithRoots(scraped): %v", err)
	}
	plain, err := st.CreateLibraryWithRoots(ctx, "Plain", false, "plain", "profile-default", []string{"/media/plain"}, "")
	if err != nil {
		t.Fatalf("CreateLibraryWithRoots(plain): %v", err)
	}
	bulkOpsImport(t, st, string(scraped.ID), "scraped one", "/media/scraped/one.mp4", "sha256:scraped-one", "aaaa0000000000c1")
	bulkOpsImport(t, st, string(plain.ID), "plain one", "/media/plain/one.mp4", "sha256:plain-one", "aaaa0000000000c2")
	openAdmin(t, handler)

	data, errs := post(t, handler, `{"query":"query { libraryHealth { libraryID counts } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("libraryHealth errors = %#v", errs)
	}
	var rows []LibraryHealthInfo
	mustUnmarshal(t, data["libraryHealth"], &rows)
	counts := map[string]map[string]int{}
	for _, row := range rows {
		counts[row.LibraryID] = row.Counts
	}

	withScraper := counts[string(scraped.ID)]
	if withScraper == nil {
		t.Fatalf("scraped library missing from %#v", rows)
	}
	for dim, want := range map[string]int{"unmatched": 1, "missing-artwork": 1, "broken": 0, "duplicate": 0} {
		if got, ok := withScraper[dim]; !ok || got != want {
			t.Errorf("scraped %s = %d (present %v), want %d", dim, got, ok, want)
		}
	}
	if _, ok := withScraper["scrape-failure"]; !ok {
		t.Error("scraped library omits scrape-failure, want it reported")
	}
	if _, ok := withScraper["no-scraper"]; ok {
		t.Error("scraped library reports no-scraper")
	}

	withoutScraper := counts[string(plain.ID)]
	if withoutScraper == nil {
		t.Fatalf("plain library missing from %#v", rows)
	}
	if got := withoutScraper["no-scraper"]; got != 1 {
		t.Errorf("plain no-scraper = %d, want 1", got)
	}
	for _, dim := range []string{"unmatched", "scrape-failure", "missing-artwork"} {
		if _, ok := withoutScraper[dim]; ok {
			t.Errorf("plain library reports %s, want it absent as not applicable", dim)
		}
	}

	items := `{"query":"query Q($libraryID:String!,$dimension:String!){ libraryHealthItems(libraryID:$libraryID,dimension:$dimension){ mediaID } }","variables":{"libraryID":"` + string(plain.ID) + `","dimension":"unmatched"}}`
	data, errs = post(t, handler, items, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("libraryHealthItems errors = %#v", errs)
	}
	var drill []HealthItemInfo
	mustUnmarshal(t, data["libraryHealthItems"], &drill)
	if len(drill) != 0 {
		t.Errorf("plain unmatched drill-down = %#v, want empty", drill)
	}

	rescan := func(libraryID store.LibraryID, field string) []graphError {
		_, errs := post(t, handler, `{"query":"mutation M($libraryID:String!){ `+field+`(libraryID:$libraryID){ affected } }","variables":{"libraryID":"`+string(libraryID)+`"}}`, authorizeManager)
		return errs
	}
	for _, field := range []string{"bulkRescanUnmatched", "bulkRescrapeScrapeFailures", "bulkFetchMissingArtwork"} {
		errs := rescan(plain.ID, field)
		if len(errs) != 1 || !strings.Contains(errs[0].Message, "not applicable") {
			t.Errorf("%s on plain library errors = %#v, want not applicable", field, errs)
		}
		if errs := rescan(scraped.ID, field); len(errs) != 0 {
			t.Errorf("%s on scraped library errors = %#v", field, errs)
		}
	}
}
