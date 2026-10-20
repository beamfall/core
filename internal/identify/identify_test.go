package identify_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// fakeEngine returns canned results and records the spec/values it saw.
type fakeEngine struct {
	searchResults []scrape.Result
	lookupResult  scrape.Result
	err           error

	searchedSpec  plugin.ScraperSpec
	searchedQuery string
	lookedUpID    string
	seenVars      scrape.Variables
	credentials   map[string]bool
}

func (f *fakeEngine) CredentialAvailable(ref string) bool {
	return f.credentials[ref]
}

func (f *fakeEngine) Search(_ context.Context, spec plugin.ScraperSpec, query string, vars ...scrape.Variables) ([]scrape.Result, error) {
	f.searchedSpec, f.searchedQuery = spec, query
	if len(vars) > 0 {
		f.seenVars = vars[0]
	}
	return f.searchResults, f.err
}

func (f *fakeEngine) Lookup(_ context.Context, spec plugin.ScraperSpec, id string, vars ...scrape.Variables) (scrape.Result, error) {
	f.searchedSpec, f.lookedUpID = spec, id
	if len(vars) > 0 {
		f.seenVars = vars[0]
	}
	return f.lookupResult, f.err
}

// fakeBundles resolves a fixed bundle set.
type fakeBundles map[plugin.ID]plugin.Bundle

func (f fakeBundles) Bundle(id plugin.ID) (plugin.Bundle, bool) {
	bundle, ok := f[id]
	return bundle, ok
}

// fakeStore records the one metadata write.
type fakeStore struct {
	mediaID  store.MediaID
	metadata store.MediaMetadata
	calls    int
}

func (f *fakeStore) UpdateMediaMetadata(_ context.Context, id store.MediaID, metadata store.MediaMetadata) error {
	f.mediaID, f.metadata = id, metadata
	f.calls++
	return nil
}

type fakeCandidateStore struct {
	upserts []struct {
		mediaID   store.MediaID
		slot      string
		sourceURL string
		pluginID  string
		scraperID string
	}
	applied []string
}

func (f *fakeCandidateStore) UpsertArtworkCandidate(_ context.Context, mediaID store.MediaID, slot, sourceURL, pluginID, scraperID string) error {
	f.upserts = append(f.upserts, struct {
		mediaID   store.MediaID
		slot      string
		sourceURL string
		pluginID  string
		scraperID string
	}{mediaID: mediaID, slot: slot, sourceURL: sourceURL, pluginID: pluginID, scraperID: scraperID})
	return nil
}

func (f *fakeCandidateStore) MarkArtworkCandidateApplied(_ context.Context, candidateID string) error {
	f.applied = append(f.applied, candidateID)
	return nil
}

func demoBundles(spec plugin.ScraperSpec) fakeBundles {
	return fakeBundles{
		"demo": {
			ID: "demo", DisplayName: plugin.L("Demo"), MediaLabel: plugin.L("Item"),
			// Declared Media fields the "media." routing tests target:
			// "media.phase" (text), "media.rating" (number), "media.code"
			// (text) resolve against these FieldSpecs.
			MediaFields: []plugin.FieldSpec{
				{Name: "phase", Type: "text"},
				{Name: "rating", Type: "number"},
				{Name: "code", Type: "text"},
			},
			Scrapers: []plugin.ScraperSpec{spec},
		},
	}
}

func TestSearchMapsCandidates(t *testing.T) {
	engine := &fakeEngine{searchResults: []scrape.Result{
		{
			ID:      "42",
			Fields:  map[string]any{"title": "Blue Harvest", "summary": "A classic.", "year": "1977"},
			Artwork: map[string]string{"poster": "https://img.example.test/42.jpg"},
		},
		{
			ID:     "43",
			Fields: map[string]any{"title": "No Year", "year": "not-a-year"},
		},
	}}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})

	candidates, err := service.Search(context.Background(), "demo", "lookup", "blue harvest", "")
	if err != nil {
		t.Fatal(err)
	}
	if engine.searchedSpec.ID != "lookup" || engine.searchedQuery != "blue harvest" {
		t.Fatalf("engine saw spec %q query %q", engine.searchedSpec.ID, engine.searchedQuery)
	}
	if len(candidates) != 2 {
		t.Fatalf("len(candidates) = %d, want 2", len(candidates))
	}
	first := candidates[0]
	if first.ID != "42" || first.Title != "Blue Harvest" || first.Summary != "A classic." || first.Year != 1977 {
		t.Fatalf("candidates[0] = %#v", first)
	}
	if first.Artwork["poster"] != "https://img.example.test/42.jpg" {
		t.Fatalf("candidates[0].Artwork = %#v", first.Artwork)
	}
	if candidates[1].Year != 0 {
		t.Fatalf("unparseable year = %d, want omitted (0)", candidates[1].Year)
	}
}

func TestSearchValidation(t *testing.T) {
	service := identify.NewService(&fakeEngine{}, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	ctx := context.Background()

	if _, err := service.Search(ctx, "demo", "lookup", "  ", ""); err == nil {
		t.Fatal("accepted blank query")
	}
	if _, err := service.Search(ctx, "nope", "lookup", "q", ""); err == nil {
		t.Fatal("accepted unknown plugin")
	}
	if _, err := service.Search(ctx, "demo", "nope", "q", ""); err == nil {
		t.Fatal("accepted unknown scraper")
	}
}

func TestApplyWritesLookedUpMetadata(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest", "summary": "A classic.", "year": "1977"},
	}}
	st := &fakeStore{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if engine.lookedUpID != "42" {
		t.Fatalf("engine looked up %q, want 42", engine.lookedUpID)
	}
	if st.calls != 1 || st.mediaID != "media-alpha" {
		t.Fatalf("store write = %d calls for %q", st.calls, st.mediaID)
	}
	if st.metadata.Title == nil || *st.metadata.Title != "Blue Harvest" {
		t.Fatalf("metadata.Title = %v", st.metadata.Title)
	}
	if st.metadata.Summary == nil || *st.metadata.Summary != "A classic." {
		t.Fatalf("metadata.Summary = %v", st.metadata.Summary)
	}
	if st.metadata.Year == nil || *st.metadata.Year != 1977 {
		t.Fatalf("metadata.Year = %v", st.metadata.Year)
	}
}

func TestApplyPersistsAndMarksArtworkCandidate(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields:  map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{artwork.SlotPoster: "https://img.example.test/42.jpg"},
	}}
	st := &fakeStore{}
	candidates := &fakeCandidateStore{}
	fetcher := &fakeFetcher{data: []byte("jpeg bytes")}
	art := &fakeArtwork{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)
	service.SetArtwork(fetcher, art)
	service.SetArtworkCandidates(candidates)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if len(candidates.upserts) != 1 {
		t.Fatalf("upserts = %#v, want one poster candidate", candidates.upserts)
	}
	upsert := candidates.upserts[0]
	if upsert.mediaID != "media-alpha" || upsert.slot != artwork.SlotPoster ||
		upsert.sourceURL != "https://img.example.test/42.jpg" ||
		upsert.pluginID != "demo" || upsert.scraperID != "lookup" {
		t.Fatalf("upsert = %#v", upsert)
	}
	wantID := store.ArtworkCandidateID("media-alpha", artwork.SlotPoster, "https://img.example.test/42.jpg")
	if len(candidates.applied) != 1 || candidates.applied[0] != wantID {
		t.Fatalf("applied = %#v, want %q", candidates.applied, wantID)
	}
}

func TestApplyOmitsUnparseableYearAndBlankFields(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Only Title", "summary": "  ", "year": "circa 1977"},
	}}
	st := &fakeStore{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if st.metadata.Title == nil || st.metadata.Summary != nil || st.metadata.Year != nil {
		t.Fatalf("metadata = %#v, want title only", st.metadata)
	}
}

func TestApplyRejectsResultWithNoUsableFields(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{Fields: map[string]any{"year": "soonish"}}}
	st := &fakeStore{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err == nil {
		t.Fatal("accepted a result with no usable fields")
	}
	if st.calls != 0 {
		t.Fatalf("store written %d times, want 0", st.calls)
	}
}

func TestApplyValidation(t *testing.T) {
	service := identify.NewService(&fakeEngine{}, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	ctx := context.Background()

	if err := service.Apply(ctx, "", "demo", "lookup", "42"); err == nil {
		t.Fatal("accepted empty media ID")
	}
	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", " "); err == nil {
		t.Fatal("accepted blank candidate ID")
	}
	if err := service.Apply(ctx, "media-alpha", "nope", "lookup", "42"); err == nil {
		t.Fatal("accepted unknown plugin")
	}
}

func TestApplyPropagatesEngineError(t *testing.T) {
	engine := &fakeEngine{err: errors.New("upstream down")}
	st := &fakeStore{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err == nil {
		t.Fatal("engine error not propagated")
	}
	if st.calls != 0 {
		t.Fatal("store written despite engine error")
	}
}

// TestServiceAgainstRealEngine runs the full ADR-0003 §4 flow — search →
// pick → lookup → apply — through the real scrape engine against a loopback
// test server.
func TestServiceAgainstRealEngine(t *testing.T) {
	// feature:identify-scrape
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "blue harvest" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"results":[
			{"id":42,"name":"Blue Harvest","overview":"A classic.","released":"1977-05-25","poster":"/p/42.jpg"},
			{"id":7,"name":"Blue Harvest II","overview":"The sequel.","released":"1980-05-21"}
		]}`)
	})
	mux.HandleFunc("/title/42", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"name":"Blue Harvest","overview":"A classic.","released":"1977-05-25"}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID:              "lookup",
		DisplayName:     plugin.L("Demo Lookup"),
		Kind:            "http-json",
		Endpoint:        server.URL,
		SearchTemplate:  "/search?q={query}",
		CandidatesPath:  "results",
		CandidateIDPath: "id",
		LookupTemplate:  "/title/{id}",
		FieldMap:        map[string]string{"title": "name", "summary": "overview", "year": "released"},
		Transforms:      map[string]string{"year": "year-from-date"},
		ArtworkMap:      map[string]string{"poster": "poster"},
		ArtworkBaseURL:  "https://img.example.test",
	}
	engine := scrape.New(scrape.WithInsecureLoopback())
	st := &fakeStore{}
	service := identify.NewService(engine, demoBundles(spec), st)
	ctx := context.Background()

	candidates, err := service.Search(ctx, "demo", "lookup", "blue harvest", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("len(candidates) = %d, want 2", len(candidates))
	}
	picked := candidates[0]
	if picked.ID != "42" || picked.Title != "Blue Harvest" || picked.Year != 1977 {
		t.Fatalf("candidates[0] = %#v", picked)
	}
	if picked.Artwork["poster"] != "https://img.example.test/p/42.jpg" {
		t.Fatalf("candidate artwork = %#v, want composed URL", picked.Artwork)
	}

	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", picked.ID); err != nil {
		t.Fatal(err)
	}
	if st.metadata.Title == nil || *st.metadata.Title != "Blue Harvest" {
		t.Fatalf("applied title = %v", st.metadata.Title)
	}
	if st.metadata.Summary == nil || *st.metadata.Summary != "A classic." {
		t.Fatalf("applied summary = %v", st.metadata.Summary)
	}
	if st.metadata.Year == nil || *st.metadata.Year != 1977 {
		t.Fatalf("applied year = %v", st.metadata.Year)
	}
}

// fakeFetcher serves canned bytes for one URL.
type fakeFetcher struct {
	url   string
	data  []byte
	err   error
	calls int

	byURL map[string]fetchedResource
	urls  []string
}

type fetchedResource struct {
	data []byte
	err  error
}

func (f *fakeFetcher) FetchResource(_ context.Context, rawURL string) ([]byte, error) {
	f.url = rawURL
	f.urls = append(f.urls, rawURL)
	f.calls++
	if f.byURL != nil {
		resource, ok := f.byURL[rawURL]
		if !ok {
			return nil, fmt.Errorf("unexpected fetch %q", rawURL)
		}
		return resource.data, resource.err
	}
	return f.data, f.err
}

// fakeArtwork records imported poster bytes.
type fakeArtwork struct {
	mediaID store.MediaID
	data    []byte
	err     error
	calls   int

	entityHas   map[store.EntityID]bool
	entityIDs   []store.EntityID
	entityData  []byte
	entityErr   error
	entityCalls int

	imports       []importedArtwork
	has           map[string]bool
	generated     map[string]bool
	embedded      bool
	embeddedCalls int
}

type importedArtwork struct {
	slot string
	id   store.MediaID
	data string
}

func (f *fakeArtwork) ImportPoster(_ context.Context, id store.MediaID, data []byte) error {
	f.mediaID, f.data = id, data
	f.calls++
	f.imports = append(f.imports, importedArtwork{slot: artwork.SlotPoster, id: id, data: string(data)})
	return f.err
}

func (f *fakeArtwork) ImportArtwork(_ context.Context, id store.MediaID, slot string, data []byte) error {
	f.mediaID, f.data = id, data
	f.calls++
	f.imports = append(f.imports, importedArtwork{slot: slot, id: id, data: string(data)})
	return f.err
}

func (f *fakeArtwork) HasArtwork(_ store.MediaID, slot string) bool {
	return f.has[slot]
}

func (f *fakeArtwork) IsGeneratedArtwork(_ store.MediaID, slot string) bool {
	return f.generated[slot]
}

func (f *fakeArtwork) ImportEmbeddedPoster(_ context.Context, _ store.MediaID) (bool, error) {
	f.embeddedCalls++
	return f.embedded, nil
}

func (f *fakeArtwork) HasEntityPoster(id store.EntityID) bool {
	return f.entityHas[id]
}

func (f *fakeArtwork) ImportEntityPoster(_ context.Context, id store.EntityID, data []byte) error {
	f.entityIDs = append(f.entityIDs, id)
	f.entityData = data
	f.entityCalls++
	return f.entityErr
}

// fakeFields records UpsertMediaFieldsWithSource writes.
type fakeFields struct {
	mediaID store.MediaID
	fields  []store.MediaField
	source  store.MutationSource
	err     error
	calls   int
}

func (f *fakeFields) UpsertMediaFieldsWithSource(_ context.Context, id store.MediaID, fields []store.MediaField, source store.MutationSource) error {
	f.mediaID, f.fields, f.source = id, fields, source
	f.calls++
	return f.err
}

func TestApplyWritesImpliedMediaFields(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
	}}
	st := &fakeStore{}
	fields := &fakeFields{}
	spec := plugin.ScraperSpec{ID: "lookup", ImpliedMediaFields: map[string]string{"media.phase": "flight"}}
	service := identify.NewService(engine, demoBundles(spec), st)
	service.SetFields(fields)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if st.calls != 1 {
		t.Fatalf("metadata write calls = %d, want 1", st.calls)
	}
	if fields.calls != 1 || fields.mediaID != "media-alpha" {
		t.Fatalf("field write = %d calls for %q", fields.calls, fields.mediaID)
	}
	if fields.source != store.MutationSourceOperator {
		t.Fatalf("field write source = %q, want operator (identified tier)", fields.source)
	}
	want := []store.MediaField{{Name: "phase", Type: store.EntityFieldText, Text: "flight"}}
	if len(fields.fields) != 1 || fields.fields[0] != want[0] {
		t.Fatalf("fields = %#v, want %#v", fields.fields, want)
	}
}

func TestApplyWithoutImpliedFieldsSkipsFieldWrite(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
	}}
	fields := &fakeFields{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetFields(fields)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if fields.calls != 0 {
		t.Fatalf("field write calls = %d, want 0", fields.calls)
	}
}

func TestApplyImpliedFieldsRequireWiredFieldStore(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
	}}
	spec := plugin.ScraperSpec{ID: "lookup", ImpliedMediaFields: map[string]string{"media.phase": "flight"}}
	service := identify.NewService(engine, demoBundles(spec), &fakeStore{})

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err == nil {
		t.Fatal("implied fields silently dropped without a field store")
	}
}

func TestApplyPropagatesFieldWriteError(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
	}}
	fields := &fakeFields{err: errors.New("disk full")}
	spec := plugin.ScraperSpec{ID: "lookup", ImpliedMediaFields: map[string]string{"media.phase": "flight"}}
	service := identify.NewService(engine, demoBundles(spec), &fakeStore{})
	service.SetFields(fields)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err == nil {
		t.Fatal("field write error not propagated")
	}
}

// TestApplyRoutesMediaFieldKeys covers the BF-24 "media.<name>" FieldMap route:
// a lookup document carrying a declared number field and a declared text field
// writes both onto the Media row, typed per the FieldSpec — built-in keys
// (title) stay on the metadata path and never reach the field store.
func TestApplyRoutesMediaFieldKeys(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		// The scrape engine stores coerced values keyed by the FieldMap key;
		// "media.rating" arrives as a float64 (number FieldType), the others
		// as strings.
		Fields: map[string]any{"title": "Blue Harvest", "media.rating": 8.5, "media.code": "BH-001"},
	}}
	st := &fakeStore{}
	fields := &fakeFields{}
	spec := plugin.ScraperSpec{ID: "lookup", FieldMap: map[string]string{
		"title": "name", "media.rating": "score", "media.code": "code",
	}}
	service := identify.NewService(engine, demoBundles(spec), st)
	service.SetFields(fields)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if st.metadata.Title == nil || *st.metadata.Title != "Blue Harvest" {
		t.Fatalf("built-in title not on metadata path: %+v", st.metadata)
	}
	if fields.calls != 1 {
		t.Fatalf("field write calls = %d, want 1", fields.calls)
	}
	byName := map[string]store.MediaField{}
	for _, f := range fields.fields {
		byName[f.Name] = f
	}
	if f := byName["rating"]; f.Type != store.EntityFieldNumber || f.Number != 8.5 {
		t.Fatalf("rating = %#v, want number 8.5", f)
	}
	if f := byName["code"]; f.Type != store.EntityFieldText || f.Text != "BH-001" {
		t.Fatalf("code = %#v, want text BH-001", f)
	}
	if _, ok := byName["title"]; ok {
		t.Fatal("built-in title leaked into the field store")
	}
}

// TestApplyReidentifyOverwritesMediaFields confirms plugin Media fields are
// identification-owned (no per-field locks in v1): a second Apply with new
// values writes again, overwriting the first.
func TestApplyReidentifyOverwritesMediaFields(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "First", "media.code": "AAA"},
	}}
	fields := &fakeFields{}
	spec := plugin.ScraperSpec{ID: "lookup", FieldMap: map[string]string{"title": "name", "media.code": "code"}}
	service := identify.NewService(engine, demoBundles(spec), &fakeStore{})
	service.SetFields(fields)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	engine.lookupResult.Fields = map[string]any{"title": "Second", "media.code": "BBB"}
	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if fields.calls != 2 {
		t.Fatalf("field write calls = %d, want 2 (re-identify overwrites)", fields.calls)
	}
	if len(fields.fields) != 1 || fields.fields[0].Text != "BBB" {
		t.Fatalf("after re-identify fields = %#v, want code=BBB", fields.fields)
	}
}

// TestApplySkipsUnparseableMediaField confirms an unparseable typed value is
// skipped (logged) without failing identification — the metadata write and any
// other parseable field still land.
func TestApplySkipsUnparseableMediaField(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		// "media.rating" is declared number but the document carries a
		// non-numeric string; it must skip, while "media.code" still writes.
		Fields: map[string]any{"title": "Blue Harvest", "media.rating": "not-a-number", "media.code": "BH-001"},
	}}
	fields := &fakeFields{}
	spec := plugin.ScraperSpec{ID: "lookup", FieldMap: map[string]string{
		"title": "name", "media.rating": "score", "media.code": "code",
	}}
	service := identify.NewService(engine, demoBundles(spec), &fakeStore{})
	service.SetFields(fields)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatalf("unparseable field failed the lookup: %v", err)
	}
	if len(fields.fields) != 1 || fields.fields[0].Name != "code" {
		t.Fatalf("fields = %#v, want only the parseable code field", fields.fields)
	}
}

func TestApplyDownloadsAndStoresPoster(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields:  map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{"poster": "https://img.example.test/p/42.jpg"},
	}}
	st := &fakeStore{}
	fetcher := &fakeFetcher{data: []byte("jpeg-bytes")}
	art := &fakeArtwork{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)
	service.SetArtwork(fetcher, art)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if fetcher.url != "https://img.example.test/p/42.jpg" {
		t.Fatalf("fetched %q", fetcher.url)
	}
	if art.calls != 1 || art.mediaID != "media-alpha" || string(art.data) != "jpeg-bytes" {
		t.Fatalf("ImportPoster = %d calls, media %q, data %q", art.calls, art.mediaID, art.data)
	}
}

func TestApplyPreservesExistingPoster(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields:  map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{artwork.SlotPoster: "https://img.example.test/p/42.jpg"},
	}}
	fetcher := &fakeFetcher{data: []byte("remote-poster")}
	art := &fakeArtwork{has: map[string]bool{artwork.SlotPoster: true}}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetArtwork(fetcher, art)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 || art.calls != 0 {
		t.Fatalf("existing poster fetched/imported: fetches=%d imports=%d", fetcher.calls, art.calls)
	}
}

func TestApplyReplacesGeneratedPoster(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields:  map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{artwork.SlotPoster: "https://img.example.test/p/42.jpg"},
	}}
	fetcher := &fakeFetcher{data: []byte("official-poster")}
	art := &fakeArtwork{
		has:       map[string]bool{artwork.SlotPoster: true},
		generated: map[string]bool{artwork.SlotPoster: true},
	}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetArtwork(fetcher, art)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 || art.calls != 1 || string(art.data) != "official-poster" {
		t.Fatalf("generated poster replacement: fetches=%d imports=%d data=%q", fetcher.calls, art.calls, art.data)
	}
}

func TestApplyPrefersEmbeddedPosterToFetchedArtwork(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields:  map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{artwork.SlotPoster: "https://img.example.test/p/42.jpg"},
	}}
	fetcher := &fakeFetcher{data: []byte("remote-poster")}
	art := &fakeArtwork{
		has:       map[string]bool{artwork.SlotPoster: true},
		generated: map[string]bool{artwork.SlotPoster: true},
		embedded:  true,
	}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetArtwork(fetcher, art)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if art.embeddedCalls != 1 || fetcher.calls != 0 || art.calls != 0 {
		t.Fatalf("embedded=%d fetches=%d imports=%d", art.embeddedCalls, fetcher.calls, art.calls)
	}
}

func TestApplyDownloadsAndStoresKnownArtworkSlots(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{
			artwork.SlotPoster:   "https://img.example.test/p/42.jpg",
			artwork.SlotBackdrop: "https://img.example.test/b/42.jpg",
			artwork.SlotThumb:    "https://img.example.test/t/42.jpg",
		},
	}}
	fetcher := &fakeFetcher{byURL: map[string]fetchedResource{
		"https://img.example.test/p/42.jpg": {data: []byte("poster-bytes")},
		"https://img.example.test/b/42.jpg": {data: []byte("backdrop-bytes")},
		"https://img.example.test/t/42.jpg": {data: []byte("thumb-bytes")},
	}}
	art := &fakeArtwork{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetArtwork(fetcher, art)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}

	imported := map[string]string{}
	for _, imp := range art.imports {
		if imp.id != "media-alpha" {
			t.Fatalf("imported media = %q, want media-alpha", imp.id)
		}
		imported[imp.slot] = imp.data
	}
	want := map[string]string{
		artwork.SlotPoster:   "poster-bytes",
		artwork.SlotBackdrop: "backdrop-bytes",
		artwork.SlotThumb:    "thumb-bytes",
	}
	if len(imported) != len(want) {
		t.Fatalf("imported slots = %#v, want %#v", imported, want)
	}
	for slot, data := range want {
		if imported[slot] != data {
			t.Fatalf("slot %q data = %q, want %q (all imports %#v)", slot, imported[slot], data, imported)
		}
	}
}

func TestApplySkipsUnknownArtworkSlots(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{
			artwork.SlotPoster: "https://img.example.test/p/42.jpg",
			"banner":           "https://img.example.test/banner/42.jpg",
		},
	}}
	fetcher := &fakeFetcher{byURL: map[string]fetchedResource{
		"https://img.example.test/p/42.jpg": {data: []byte("poster-bytes")},
	}}
	art := &fakeArtwork{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetArtwork(fetcher, art)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if len(art.imports) != 1 || art.imports[0].slot != artwork.SlotPoster {
		t.Fatalf("imports = %#v, want only poster", art.imports)
	}
	for _, url := range fetcher.urls {
		if url == "https://img.example.test/banner/42.jpg" {
			t.Fatal("unknown artwork slot was fetched")
		}
	}
}

func TestApplyArtworkSlotFailuresDoNotAbortOtherSlots(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{
			artwork.SlotPoster:   "https://img.example.test/p/42.jpg",
			artwork.SlotBackdrop: "https://img.example.test/b/42.jpg",
			artwork.SlotThumb:    "https://img.example.test/t/42.jpg",
		},
	}}
	fetcher := &fakeFetcher{byURL: map[string]fetchedResource{
		"https://img.example.test/p/42.jpg": {data: []byte("poster-bytes")},
		"https://img.example.test/b/42.jpg": {err: errors.New("blocked")},
		"https://img.example.test/t/42.jpg": {data: []byte("thumb-bytes")},
	}}
	art := &fakeArtwork{}
	st := &fakeStore{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)
	service.SetArtwork(fetcher, art)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatalf("artwork slot failure must not fail Apply: %v", err)
	}
	if st.calls != 1 {
		t.Fatalf("metadata write calls = %d, want 1", st.calls)
	}
	imported := map[string]string{}
	for _, imp := range art.imports {
		imported[imp.slot] = imp.data
	}
	want := map[string]string{
		artwork.SlotPoster: "poster-bytes",
		artwork.SlotThumb:  "thumb-bytes",
	}
	if len(imported) != len(want) {
		t.Fatalf("imported slots = %#v, want %#v", imported, want)
	}
	for slot, data := range want {
		if imported[slot] != data {
			t.Fatalf("slot %q data = %q, want %q", slot, imported[slot], data)
		}
	}
}

func TestApplyPosterFailuresAreBestEffort(t *testing.T) {
	cases := map[string]struct {
		fetcher *fakeFetcher
		artwork *fakeArtwork
	}{
		"fetch fails":  {&fakeFetcher{err: errors.New("blocked")}, &fakeArtwork{}},
		"import fails": {&fakeFetcher{data: []byte("x")}, &fakeArtwork{err: errors.New("disk full")}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			engine := &fakeEngine{lookupResult: scrape.Result{
				Fields:  map[string]any{"title": "Blue Harvest"},
				Artwork: map[string]string{"poster": "https://img.example.test/p/42.jpg"},
			}}
			st := &fakeStore{}
			service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)
			service.SetArtwork(tc.fetcher, tc.artwork)
			if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
				t.Fatalf("artwork failure must not fail Apply: %v", err)
			}
			if st.calls != 1 {
				t.Fatalf("metadata write calls = %d, want 1", st.calls)
			}
		})
	}
}

func TestApplyWithoutArtworkSeamsSkipsFetch(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields:  map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{"poster": "https://img.example.test/p/42.jpg"},
	}}
	st := &fakeStore{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)
	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if st.calls != 1 {
		t.Fatalf("metadata write calls = %d, want 1", st.calls)
	}
}

// fakeEntityStore records entity hydration writes.
type fakeEntityStore struct {
	library     store.LibraryID
	libraryErr  error
	upsertErr   error
	relationErr error
	mediaAsked  store.MediaID
	entities    []store.Entity
	relations   []store.EntityRelation
}

func (f *fakeEntityStore) MediaLibrary(_ context.Context, id store.MediaID) (store.LibraryID, error) {
	f.mediaAsked = id
	return f.library, f.libraryErr
}

func (f *fakeEntityStore) UpsertEntity(_ context.Context, entity store.Entity) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.entities = append(f.entities, entity)
	return nil
}

func (f *fakeEntityStore) UpsertEntityRelation(_ context.Context, relation store.EntityRelation) error {
	if f.relationErr != nil {
		return f.relationErr
	}
	f.relations = append(f.relations, relation)
	return nil
}

func hydratedLookupResult() scrape.Result {
	return scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
		Entities: []scrape.EntityResult{
			{Kind: "tone", Relation: "tone-of", Name: "Solemn", SortOrder: identifyIntPtr(0)},
			{Kind: "tone", Relation: "tone-of", Name: "Wry", SortOrder: identifyIntPtr(1)},
			{Kind: "maker", Relation: "made", Name: "Lucas & Co"},
		},
	}
}

func identifyIntPtr(value int) *int {
	return &value
}

func TestApplyHydratesEntitiesAndRelations(t *testing.T) {
	engine := &fakeEngine{lookupResult: hydratedLookupResult()}
	entities := &fakeEntityStore{library: "lib-main"}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetEntities(entities)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if entities.mediaAsked != "media-alpha" {
		t.Fatalf("library resolved for %q, want media-alpha", entities.mediaAsked)
	}
	if len(entities.entities) != 3 || len(entities.relations) != 3 {
		t.Fatalf("writes = %d entities, %d relations, want 3 each", len(entities.entities), len(entities.relations))
	}
	for i, entity := range entities.entities {
		if entity.LibraryID != "lib-main" {
			t.Fatalf("entity %d library = %q, want lib-main", i, entity.LibraryID)
		}
		if entity.ID == "" || entity.Kind == "" || entity.Title == "" {
			t.Fatalf("entity %d incomplete: %#v", i, entity)
		}
		relation := entities.relations[i]
		if relation.FromEntityID != entity.ID || relation.ToMediaID != "media-alpha" || relation.ToEntityID != "" {
			t.Fatalf("relation %d = %#v, want %s → media-alpha", i, relation, entity.ID)
		}
	}
	if entities.entities[0].Kind != "tone" || entities.entities[0].Title != "Solemn" ||
		entities.relations[0].Name != "tone-of" {
		t.Fatalf("first hydration = %#v / %#v", entities.entities[0], entities.relations[0])
	}
	if entities.entities[2].Kind != "maker" || entities.relations[2].Name != "made" {
		t.Fatalf("third hydration = %#v / %#v", entities.entities[2], entities.relations[2])
	}
}

// A scraper EntityMap that declares an OrdinalField sources the relation's
// sort_order from the plugin-supplied document value, not the array index —
// the ordering source that lets OrderedChildren and the next-up Source land in
// the intended order. The fixture lists episodes in
// reverse so a passing assertion proves the ordinal, not document position,
// drove sort_order; a non-numeric ordinal falls back to the array index.
func TestApplyOrdinalFieldDrivesRelationSortOrder(t *testing.T) {
	spec := plugin.ScraperSpec{
		ID: "lookup",
		EntityMaps: []plugin.EntityMap{{
			Kind: "season", Relation: "contains", Path: "episodes", NamePath: "name",
			FieldMap:     map[string]string{"position": "episode_number"},
			OrdinalField: "position",
		}},
	}
	// Document order is reversed relative to episode_number: index 0 carries
	// ordinal 2, index 1 carries ordinal 1. SortOrder set by the scrape engine
	// is the array index (0,1); the OrdinalField must override it to (2,1).
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Voyages"},
		Entities: []scrape.EntityResult{
			{Kind: "season", Relation: "contains", Name: "The Return", SortOrder: identifyIntPtr(0), Fields: map[string]string{"position": "2"}},
			{Kind: "season", Relation: "contains", Name: "The Departure", SortOrder: identifyIntPtr(1), Fields: map[string]string{"position": "1"}},
			{Kind: "season", Relation: "contains", Name: "The Pilot", SortOrder: identifyIntPtr(2), Fields: map[string]string{"position": "notanumber"}},
		},
	}}
	entities := &fakeEntityStore{library: "lib-main"}
	service := identify.NewService(engine, demoBundles(spec), &fakeStore{})
	service.SetEntities(entities)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if len(entities.relations) != 3 {
		t.Fatalf("relations = %d, want 3", len(entities.relations))
	}
	got := map[string]int{}
	for i, relation := range entities.relations {
		if relation.SortOrder == nil {
			t.Fatalf("relation %d (%s) has nil sort_order", i, entities.entities[i].Title)
		}
		got[entities.entities[i].Title] = *relation.SortOrder
	}
	// The two numeric ordinals override array index; the non-numeric one falls
	// back to its array index (2).
	if got["The Return"] != 2 || got["The Departure"] != 1 || got["The Pilot"] != 2 {
		t.Fatalf("sort_order by title = %#v, want Return:2 Departure:1 Pilot:2 (ordinal overrides index, non-numeric falls back)", got)
	}
}

// Re-applying the same identification derives the same Entity IDs — the
// (Library, kind, name) keying that makes hydration idempotent.
func TestApplyHydrationIsIdempotent(t *testing.T) {
	engine := &fakeEngine{lookupResult: hydratedLookupResult()}
	entities := &fakeEntityStore{library: "lib-main"}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetEntities(entities)
	ctx := context.Background()

	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if len(entities.entities) != 6 {
		t.Fatalf("entity writes = %d, want 6 (3 per apply)", len(entities.entities))
	}
	for i := 0; i < 3; i++ {
		if entities.entities[i].ID != entities.entities[i+3].ID {
			t.Fatalf("re-apply forked entity %d: %q vs %q", i, entities.entities[i].ID, entities.entities[i+3].ID)
		}
		first, second := entities.relations[i], entities.relations[i+3]
		if first.FromEntityID != second.FromEntityID || first.Name != second.Name || first.ToMediaID != second.ToMediaID {
			t.Fatalf("re-apply forked relation %d: %#v vs %#v", i, first, second)
		}
	}
	// Different (library, kind, name) inputs must not collide.
	ids := map[store.EntityID]bool{}
	for _, entity := range entities.entities[:3] {
		ids[entity.ID] = true
	}
	if len(ids) != 3 {
		t.Fatalf("distinct hydrations share IDs: %v", ids)
	}
}

func TestApplyHydrationRefreshesRelationSortOrder(t *testing.T) {
	engine := &fakeEngine{lookupResult: hydratedLookupResult()}
	entities := &fakeEntityStore{library: "lib-main"}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetEntities(entities)
	ctx := context.Background()

	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	engine.lookupResult = scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
		Entities: []scrape.EntityResult{
			{Kind: "tone", Relation: "tone-of", Name: "Wry", SortOrder: identifyIntPtr(0)},
			{Kind: "tone", Relation: "tone-of", Name: "Solemn", SortOrder: identifyIntPtr(1)},
			{Kind: "maker", Relation: "made", Name: "Lucas & Co"},
		},
	}
	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if len(entities.relations) != 6 {
		t.Fatalf("relations = %d, want 6", len(entities.relations))
	}
	firstByEntity := map[store.EntityID]int{}
	for _, relation := range entities.relations[:3] {
		if relation.SortOrder != nil {
			firstByEntity[relation.FromEntityID] = *relation.SortOrder
		}
	}
	for _, relation := range entities.relations[3:5] {
		firstOrder, ok := firstByEntity[relation.FromEntityID]
		if !ok || relation.SortOrder == nil {
			t.Fatalf("relation did not carry refreshed order: first=%v second=%#v", firstByEntity, relation)
		}
		if firstOrder == *relation.SortOrder {
			t.Fatalf("relation %q order stayed %d across reorder", relation.FromEntityID, firstOrder)
		}
	}
	if entities.relations[5].SortOrder != nil {
		t.Fatalf("scalar relation carries sort order: %#v", entities.relations[5])
	}
}

func TestApplyEntitiesRequireWiredEntityStore(t *testing.T) {
	engine := &fakeEngine{lookupResult: hydratedLookupResult()}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err == nil {
		t.Fatal("hydrated entities silently dropped without an entity store")
	}
}

func TestApplyWithoutEntitiesSkipsEntityWrites(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{Fields: map[string]any{"title": "Blue Harvest"}}}
	entities := &fakeEntityStore{library: "lib-main"}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetEntities(entities)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if entities.mediaAsked != "" || len(entities.entities) != 0 {
		t.Fatalf("entity store touched with no hydrations: %#v", entities)
	}
}

func hydratedLookupResultWithArtwork() scrape.Result {
	result := hydratedLookupResult()
	result.Entities[2].ArtworkURL = "https://img.example.test/makers/lucas.png"
	return result
}

func TestApplyImportsEntityPoster(t *testing.T) {
	engine := &fakeEngine{lookupResult: hydratedLookupResultWithArtwork()}
	entities := &fakeEntityStore{library: "lib-main"}
	fetcher := &fakeFetcher{data: []byte("logo-bytes")}
	art := &fakeArtwork{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetEntities(entities)
	service.SetArtwork(fetcher, art)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 || fetcher.url != "https://img.example.test/makers/lucas.png" {
		t.Fatalf("fetch = %d calls for %q", fetcher.calls, fetcher.url)
	}
	if art.entityCalls != 1 || string(art.entityData) != "logo-bytes" {
		t.Fatalf("ImportEntityPoster = %d calls, data %q", art.entityCalls, art.entityData)
	}
	// The poster lands on the Entity the hydration upserted (the maker).
	if len(art.entityIDs) != 1 || art.entityIDs[0] != entities.entities[2].ID {
		t.Fatalf("poster imported for %v, want %q", art.entityIDs, entities.entities[2].ID)
	}
}

// Re-applying an identification must not re-download entity posters: once
// HasEntityPoster reports true, the fetch is skipped entirely.
func TestApplySkipsEntityPosterWhenPresent(t *testing.T) {
	engine := &fakeEngine{lookupResult: hydratedLookupResultWithArtwork()}
	entities := &fakeEntityStore{library: "lib-main"}
	fetcher := &fakeFetcher{data: []byte("logo-bytes")}
	art := &fakeArtwork{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetEntities(entities)
	service.SetArtwork(fetcher, art)
	ctx := context.Background()

	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	art.entityHas = map[store.EntityID]bool{art.entityIDs[0]: true}
	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 || art.entityCalls != 1 {
		t.Fatalf("re-apply re-fetched: fetch = %d calls, import = %d calls, want 1 each", fetcher.calls, art.entityCalls)
	}
}

func TestApplyEntityPosterFailuresAreBestEffort(t *testing.T) {
	cases := map[string]struct {
		fetcher *fakeFetcher
		artwork *fakeArtwork
	}{
		"fetch fails":  {&fakeFetcher{err: errors.New("blocked")}, &fakeArtwork{}},
		"import fails": {&fakeFetcher{data: []byte("x")}, &fakeArtwork{entityErr: errors.New("disk full")}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			engine := &fakeEngine{lookupResult: hydratedLookupResultWithArtwork()}
			entities := &fakeEntityStore{library: "lib-main"}
			service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
			service.SetEntities(entities)
			service.SetArtwork(tc.fetcher, tc.artwork)
			if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
				t.Fatalf("entity poster failure must not fail Apply: %v", err)
			}
			if len(entities.entities) != 3 || len(entities.relations) != 3 {
				t.Fatalf("hydration writes = %d entities, %d relations, want 3 each", len(entities.entities), len(entities.relations))
			}
		})
	}
}

func TestApplyEntityPosterWithoutArtworkSeamsSkipsFetch(t *testing.T) {
	engine := &fakeEngine{lookupResult: hydratedLookupResultWithArtwork()}
	entities := &fakeEntityStore{library: "lib-main"}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetEntities(entities)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if len(entities.entities) != 3 {
		t.Fatalf("hydration writes = %d entities, want 3", len(entities.entities))
	}
}

func TestApplyPropagatesEntityWriteErrors(t *testing.T) {
	cases := map[string]*fakeEntityStore{
		"library lookup fails": {libraryErr: errors.New("media not found")},
		"entity write fails":   {library: "lib-main", upsertErr: errors.New("disk full")},
		"relation write fails": {library: "lib-main", relationErr: errors.New("disk full")},
	}
	for name, entities := range cases {
		t.Run(name, func(t *testing.T) {
			engine := &fakeEngine{lookupResult: hydratedLookupResult()}
			service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
			service.SetEntities(entities)
			if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err == nil {
				t.Fatal("entity write error not propagated")
			}
		})
	}
}

// Hydrated relations carry the cardinality their RelationSpec declares, so
// the store can enforce "one" (re-identifying a Media moves it between
// containers instead of linking both). Undeclared pairs stay default-many.
func TestApplyHydrationCarriesDeclaredCardinality(t *testing.T) {
	engine := &fakeEngine{lookupResult: hydratedLookupResult()}
	entities := &fakeEntityStore{library: "lib-main"}
	bundles := demoBundles(plugin.ScraperSpec{ID: "lookup"})
	bundle := bundles["demo"]
	bundle.EntityKinds = []plugin.EntityKind{
		{Name: "tone", Archetype: "category",
			Relations: []plugin.RelationSpec{{Name: "tone-of", ToKind: plugin.MediaKindName}}},
		{Name: "maker", Archetype: "person",
			Relations: []plugin.RelationSpec{{Name: "made", ToKind: plugin.MediaKindName, Cardinality: "one"}}},
	}
	bundles["demo"] = bundle
	service := identify.NewService(engine, bundles, &fakeStore{})
	service.SetEntities(entities)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if len(entities.relations) != 3 {
		t.Fatalf("relations = %d, want 3", len(entities.relations))
	}
	for i := 0; i < 2; i++ {
		if entities.relations[i].Name != "tone-of" || entities.relations[i].Cardinality != "" {
			t.Fatalf("tone relation %d = %#v, want default-many", i, entities.relations[i])
		}
	}
	if entities.relations[2].Name != "made" || entities.relations[2].Cardinality != "one" {
		t.Fatalf("maker relation = %#v, want cardinality one", entities.relations[2])
	}
	for i, want := range []int{0, 1} {
		if order := entities.relations[i].SortOrder; order == nil || *order != want {
			t.Fatalf("tone relation %d sort order = %v, want %d", i, order, want)
		}
	}
	if order := entities.relations[2].SortOrder; order != nil {
		t.Fatalf("scalar maker relation carries sort order: %v", *order)
	}
}

// A scraper lookup that resolves the person-archetype "maker" Entity's typed
// fields (the acceptance criterion: a field-populated Entity, not just a name)
// and repeats the same maker so dedupe collapses it onto one Entity row.
func TestApplyHydratesEntityFieldsAndDedupes(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
		Entities: []scrape.EntityResult{
			{
				Kind: "maker", Relation: "made", Name: "Ada Lovelace", SortOrder: identifyIntPtr(0),
				Fields: map[string]string{"bio": "Pioneer.", "born": "1815-12-10", "credits": "12"},
			},
			// The same maker resolved a second time (e.g. two roles): dedupe must
			// collapse onto one Entity ID, not fork a second row.
			{
				Kind: "maker", Relation: "made", Name: "Ada Lovelace", SortOrder: identifyIntPtr(1),
				Fields: map[string]string{"bio": "Pioneer.", "born": "1815-12-10", "credits": "12"},
			},
		},
	}}
	entities := &fakeEntityStore{library: "lib-main"}
	bundles := demoBundles(plugin.ScraperSpec{ID: "lookup"})
	bundle := bundles["demo"]
	bundle.EntityKinds = []plugin.EntityKind{
		{Name: "maker", Archetype: "person",
			Fields: []plugin.FieldSpec{
				{Name: "bio", Type: "text"},
				{Name: "born", Type: "date"},
				{Name: "credits", Type: "number"},
			},
			Relations: []plugin.RelationSpec{{Name: "made", ToKind: plugin.MediaKindName}}},
	}
	bundles["demo"] = bundle
	service := identify.NewService(engine, bundles, &fakeStore{})
	service.SetEntities(entities)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	// Two hydrated results, one distinct maker → two upserts of one ID (dedupe
	// is the shared (library, kind, name) key, idempotent on the store side).
	if len(entities.entities) != 2 {
		t.Fatalf("entity upserts = %d, want 2 (same maker twice)", len(entities.entities))
	}
	if entities.entities[0].ID != entities.entities[1].ID {
		t.Fatalf("duplicate maker forked IDs: %q vs %q", entities.entities[0].ID, entities.entities[1].ID)
	}
	got := entities.entities[0]
	if got.Kind != "maker" || got.Title != "Ada Lovelace" {
		t.Fatalf("entity = %#v, want maker Ada Lovelace", got)
	}
	byName := map[string]store.EntityField{}
	for _, f := range got.Fields {
		byName[f.Name] = f
	}
	if len(got.Fields) != 3 {
		t.Fatalf("fields = %#v, want bio/born/credits", got.Fields)
	}
	if f := byName["bio"]; f.Type != store.EntityFieldText || f.Text != "Pioneer." {
		t.Fatalf("bio field = %#v", f)
	}
	if f := byName["credits"]; f.Type != store.EntityFieldNumber || f.Number != 12 {
		t.Fatalf("credits field = %#v", f)
	}
	if f := byName["born"]; f.Type != store.EntityFieldDate || f.Date.Year() != 1815 || f.Date.Month() != 12 || f.Date.Day() != 10 {
		t.Fatalf("born field = %#v", f)
	}
}

// A hierarchical kind hydrates its same-kind parent and links child→parent
// under the declared parent relation (ADR-0003 §2 kind hierarchy).
func TestApplyHydratesEntityHierarchy(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
		Entities: []scrape.EntityResult{
			{Kind: "house", Relation: "from-house", Name: "Sub Label", ParentName: "Parent Label", ParentRelation: "child-of"},
		},
	}}
	entities := &fakeEntityStore{library: "lib-main"}
	bundles := demoBundles(plugin.ScraperSpec{ID: "lookup"})
	bundle := bundles["demo"]
	bundle.EntityKinds = []plugin.EntityKind{
		{Name: "house", Archetype: "studio", Hierarchical: true,
			Relations: []plugin.RelationSpec{
				{Name: "from-house", ToKind: plugin.MediaKindName},
				{Name: "child-of", ToKind: "house"},
			}},
	}
	bundles["demo"] = bundle
	service := identify.NewService(engine, bundles, &fakeStore{})
	service.SetEntities(entities)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	// Two entities: the hydrated child and its parent.
	if len(entities.entities) != 2 {
		t.Fatalf("entity upserts = %d, want 2 (child + parent)", len(entities.entities))
	}
	var child, parent store.Entity
	for _, e := range entities.entities {
		switch e.Title {
		case "Sub Label":
			child = e
		case "Parent Label":
			parent = e
		}
	}
	if child.ID == "" || parent.ID == "" {
		t.Fatalf("missing child or parent: %#v", entities.entities)
	}
	// Two relations: child→Media (from-house) and child→parent (child-of).
	var parentRel store.EntityRelation
	for _, r := range entities.relations {
		if r.Name == "child-of" {
			parentRel = r
		}
	}
	if parentRel.FromEntityID != child.ID || parentRel.ToEntityID != parent.ID || parentRel.ToMediaID != "" {
		t.Fatalf("parent relation = %#v, want %s → %s", parentRel, child.ID, parent.ID)
	}
}

// fakeFingerprints resolves canned content-identity values.
type fakeFingerprints map[store.MediaID]store.MediaFingerprints

func (f fakeFingerprints) MediaFingerprints(_ context.Context, id store.MediaID) (store.MediaFingerprints, error) {
	fp, ok := f[id]
	if !ok {
		return store.MediaFingerprints{}, errors.New("media not found")
	}
	return fp, nil
}

func TestSearchPassesFingerprintVariables(t *testing.T) {
	engine := &fakeEngine{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetFingerprints(fakeFingerprints{
		"media-1": {OSHash: "00000000075bcd15", PHash: "f0f0f0f0f0f0f0f0", DurationSeconds: 5400},
	})

	// Empty query is fine when a media ID sources the fingerprint variables.
	if _, err := service.Search(context.Background(), "demo", "lookup", "", "media-1"); err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	want := scrape.Variables{OSHash: "00000000075bcd15", PHash: "f0f0f0f0f0f0f0f0", DurationSeconds: 5400, PluginID: "demo"}
	if engine.seenVars != want {
		t.Fatalf("engine saw variables %+v, want %+v", engine.seenVars, want)
	}

	if _, err := service.Search(context.Background(), "demo", "lookup", "q", "media-nope"); err == nil {
		t.Fatal("Search with unknown media returned nil error")
	}
}

func TestApplyPassesFingerprintVariables(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{Fields: map[string]any{"title": "Found"}}}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), &fakeStore{})
	service.SetFingerprints(fakeFingerprints{
		"media-1": {OSHash: "00000000075bcd15", DurationSeconds: 90},
	})

	if err := service.Apply(context.Background(), "media-1", "demo", "lookup", "42"); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	want := scrape.Variables{OSHash: "00000000075bcd15", DurationSeconds: 90, PluginID: "demo"}
	if engine.seenVars != want {
		t.Fatalf("engine saw variables %+v, want %+v", engine.seenVars, want)
	}
}
