package graph

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

var (
	sourceAttribution = &plugin.Attribution{
		Text: plugin.LocaleString{"en": "Data courtesy of Source A.", "fr": "Données fournies par Source A."},
		URL:  "https://source-a.example.test",
	}
	otherAttribution = &plugin.Attribution{
		Text: plugin.L("Uses the Source B API."),
		URL:  "https://source-b.example.test",
	}
)

func attributionScraper(id, name string, attribution *plugin.Attribution) plugin.ScraperSpec {
	return plugin.ScraperSpec{
		ID: id, DisplayName: plugin.L(name), Kind: "http-json", Endpoint: "https://metadata.example.test",
		SearchTemplate: "/search?q={query}", CandidatesPath: "items", CandidateIDPath: "id",
		Attribution: attribution,
	}
}

func attributionBundle(id string, scrapers ...plugin.ScraperSpec) plugin.Bundle {
	return plugin.Bundle{
		ID: plugin.ID(id), Version: "1.0.0", DisplayName: plugin.L(id), MediaLabel: plugin.L("Item"),
		Layouts:  []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		Scrapers: scrapers,
	}
}

func attributionHandler(t *testing.T, st *store.Store, bundles ...plugin.Bundle) *Handler {
	t.Helper()
	registry, err := newTestPluginRegistry(bundles)
	if err != nil {
		t.Fatal(err)
	}
	return newTestHandlerWithPlugins(st, registry)
}

func readMediaAttribution(t *testing.T, handler *Handler, id string) *MetadataAttributionInfo {
	t.Helper()
	data, errs := post(t, handler,
		`{"query":"query($id:String!) { media(id: $id) { id metadataAttribution { providerName text url } } }","variables":{"id":"`+id+`"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("media errors = %#v", errs)
	}
	var media MediaInfo
	if err := json.Unmarshal(data["media"], &media); err != nil {
		t.Fatal(err)
	}
	if media.ID != id {
		t.Fatalf("media id = %q, want %q", media.ID, id)
	}
	return media.MetadataAttribution
}

// TestMediaMetadataAttributionFromActiveScraper pins CORE-1126: the media(id)
// read carries the attribution the active bundle declares for the scraper that
// supplied the item's metadata, as plugin data.
func TestMediaMetadataAttributionFromActiveScraper(t *testing.T) {
	st := storetest.Open(t)
	handler := attributionHandler(t, st, attributionBundle("credit",
		attributionScraper("lookup", "Source A (Items)", sourceAttribution),
		attributionScraper("bare", "Bare", nil),
	))
	ctx := context.Background()

	if got := readMediaAttribution(t, handler, "media-alpha"); got != nil {
		t.Fatalf("attribution before identification = %#v, want none", got)
	}
	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", "credit", "lookup"); err != nil {
		t.Fatal(err)
	}
	got := readMediaAttribution(t, handler, "media-alpha")
	want := &MetadataAttributionInfo{
		ProviderName: plugin.L("Source A (Items)"),
		Text:         sourceAttribution.Text,
		URL:          sourceAttribution.URL,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attribution = %#v, want %#v", got, want)
	}
}

func TestMediaMetadataAttributionAbsentWhenPluginInactiveOrUndeclared(t *testing.T) {
	st := storetest.Open(t)
	handler := attributionHandler(t, st, attributionBundle("credit",
		attributionScraper("lookup", "Source A (Items)", sourceAttribution),
		attributionScraper("bare", "Bare", nil),
	))
	ctx := context.Background()

	for _, tc := range []struct {
		name, pluginID, scraperID string
	}{
		{"scraper declares no attribution", "credit", "bare"},
		{"plugin not active", "inactive", "lookup"},
		{"scraper no longer declared", "credit", "retired"},
	} {
		if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", tc.pluginID, tc.scraperID); err != nil {
			t.Fatal(err)
		}
		if got := readMediaAttribution(t, handler, "media-alpha"); got != nil {
			t.Fatalf("%s: attribution = %#v, want none", tc.name, got)
		}
	}
}

func TestMetadataCreditsDedupesIdenticalAttribution(t *testing.T) {
	st := storetest.Open(t)
	handler := attributionHandler(t, st,
		attributionBundle("credit",
			attributionScraper("lookup", "Source A (Items)", sourceAttribution),
			attributionScraper("lookup-groups", "Source A (Groups)", sourceAttribution),
			attributionScraper("bare", "Bare", nil),
		),
		attributionBundle("more",
			attributionScraper("other", "Source B", otherAttribution),
			attributionScraper("again", "Source A again", sourceAttribution),
		),
	)
	data, errs := post(t, handler, `{"query":"query { metadataCredits { text url } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("metadataCredits errors = %#v", errs)
	}
	var got []MetadataAttributionInfo
	if err := json.Unmarshal(data["metadataCredits"], &got); err != nil {
		t.Fatal(err)
	}
	want := []MetadataAttributionInfo{
		{Text: sourceAttribution.Text, URL: sourceAttribution.URL},
		{Text: otherAttribution.Text, URL: otherAttribution.URL},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadataCredits = %#v, want %#v", got, want)
	}

	empty := attributionHandler(t, st, attributionBundle("bare-only", attributionScraper("bare", "Bare", nil)))
	data, errs = post(t, empty, `{"query":"query { metadataCredits { text url } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("empty metadataCredits errors = %#v", errs)
	}
	if string(data["metadataCredits"]) != "[]" {
		t.Fatalf("empty metadataCredits = %s, want []", data["metadataCredits"])
	}
}

func TestPluginScraperInfoCarriesAttribution(t *testing.T) {
	st := storetest.Open(t)
	bundle := attributionBundle("credit",
		attributionScraper("lookup", "Source A (Items)", sourceAttribution),
		attributionScraper("bare", "Bare", nil),
	)
	handler := attributionHandler(t, st, bundle)
	items := handler.pluginScraperInfo("credit", bundle)
	if len(items) != 2 {
		t.Fatalf("scrapers = %#v, want 2", items)
	}
	want := &MetadataAttributionInfo{Text: sourceAttribution.Text, URL: sourceAttribution.URL}
	if !reflect.DeepEqual(items[0].Attribution, want) {
		t.Fatalf("lookup attribution = %#v, want %#v", items[0].Attribution, want)
	}
	if items[1].Attribution != nil {
		t.Fatalf("bare attribution = %#v, want none", items[1].Attribution)
	}
}
