package identify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type provenanceRecord struct {
	pluginID, scraperID, candidateID string
}

type fakeProvenanceStore struct {
	byMedia map[store.MediaID]provenanceRecord
	writes  int
	err     error
}

func (f *fakeProvenanceStore) SetMediaMetadataProvenance(_ context.Context, mediaID store.MediaID, pluginID, scraperID, candidateID string) error {
	if f.err != nil {
		return f.err
	}
	if f.byMedia == nil {
		f.byMedia = map[store.MediaID]provenanceRecord{}
	}
	f.byMedia[mediaID] = provenanceRecord{pluginID: pluginID, scraperID: scraperID, candidateID: candidateID}
	f.writes++
	return nil
}

func twoScraperBundles() fakeBundles {
	bundles := demoBundles(plugin.ScraperSpec{ID: "lookup"})
	demo := bundles["demo"]
	demo.Scrapers = append(demo.Scrapers, plugin.ScraperSpec{ID: "second"})
	bundles["demo"] = demo
	return bundles
}

func TestApplyRecordsMetadataProvenance(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{Fields: map[string]any{"title": "Blue Harvest"}}}
	provenance := &fakeProvenanceStore{}
	service := identify.NewService(engine, twoScraperBundles(), &fakeStore{provenance: provenance})

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if got, want := provenance.byMedia["media-alpha"], (provenanceRecord{"demo", "lookup", "42"}); got != want {
		t.Fatalf("provenance = %#v, want %#v", got, want)
	}
}

func TestApplyOverwritesMetadataProvenanceOnReidentify(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{Fields: map[string]any{"title": "Blue Harvest"}}}
	provenance := &fakeProvenanceStore{}
	service := identify.NewService(engine, twoScraperBundles(), &fakeStore{provenance: provenance})
	ctx := context.Background()

	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(ctx, "media-alpha", "demo", "second", "43"); err != nil {
		t.Fatal(err)
	}
	if got, want := provenance.byMedia["media-alpha"], (provenanceRecord{"demo", "second", "43"}); got != want {
		t.Fatalf("provenance after re-identify = %#v, want %#v", got, want)
	}
	if provenance.writes != 2 {
		t.Fatalf("provenance writes = %d, want 2", provenance.writes)
	}
}

func TestApplyPropagatesProvenanceWriteError(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{Fields: map[string]any{"title": "Blue Harvest"}}}
	boom := errors.New("provenance write failed")
	service := identify.NewService(engine, twoScraperBundles(), &fakeStore{provenance: &fakeProvenanceStore{err: boom}})

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); !errors.Is(err, boom) {
		t.Fatalf("Apply err = %v, want %v", err, boom)
	}
}

func TestApplyFailureBeforeMetadataWriteRecordsNoProvenance(t *testing.T) {
	engine := &fakeEngine{err: errors.New("lookup failed")}
	provenance := &fakeProvenanceStore{}
	service := identify.NewService(engine, twoScraperBundles(), &fakeStore{provenance: provenance})

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err == nil {
		t.Fatal("Apply succeeded despite lookup failure")
	}
	if provenance.writes != 0 {
		t.Fatalf("provenance writes = %d, want 0", provenance.writes)
	}
}

// scraperEngine returns a title naming the scraper looked up, safe for
// concurrent Apply calls.
type scraperEngine struct{}

func (scraperEngine) CredentialAvailable(string) bool { return false }

func (scraperEngine) Search(context.Context, plugin.ScraperSpec, string, ...scrape.Variables) ([]scrape.Result, error) {
	return nil, nil
}

func (scraperEngine) Lookup(_ context.Context, spec plugin.ScraperSpec, _ string, _ ...scrape.Variables) (scrape.Result, error) {
	return scrape.Result{Fields: map[string]any{"title": spec.ID}}, nil
}

// Independent service instances share the catalog but have separate item locks.
func TestConcurrentApplyKeepsMetadataAndProvenanceTogether(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, scraperID := range []string{"lookup", "second"} {
		service := identify.NewService(scraperEngine{}, twoScraperBundles(), st)
		go func() {
			<-start
			errs <- service.Apply(ctx, "media-alpha", "demo", scraperID, scraperID)
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	filter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	media, err := st.Media(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	provenance, found, err := st.MediaMetadataProvenance(ctx, "media-alpha")
	if err != nil || !found || media.Title != provenance.ScraperID || media.Title != provenance.CandidateID {
		t.Fatalf("title = %q, provenance = %+v, found %v, err %v", media.Title, provenance, found, err)
	}
}

type driftingEngine struct {
	title    string
	entities []scrape.EntityResult
	lookups  int
}

func (e *driftingEngine) CredentialAvailable(string) bool { return false }

func (e *driftingEngine) Search(context.Context, plugin.ScraperSpec, string, ...scrape.Variables) ([]scrape.Result, error) {
	return nil, nil
}

func (e *driftingEngine) Lookup(context.Context, plugin.ScraperSpec, string, ...scrape.Variables) (scrape.Result, error) {
	e.lookups++
	return scrape.Result{Fields: map[string]any{"title": e.title}, Entities: e.entities}, nil
}

type failOnceEntityStore struct {
	*store.Store
	failNext  bool
	linkCalls int
}

func (f *failOnceEntityStore) UpsertHydratedEntityRelationWritten(ctx context.Context, relation store.EntityRelation) (bool, error) {
	f.linkCalls++
	if f.failNext {
		f.failNext = false
		return false, errors.New("transient entity link error")
	}
	return f.Store.UpsertHydratedEntityRelationWritten(ctx, relation)
}

func TestApplyRequestAndFixMatchReplayTolerateProviderDriftAndRejectIntentConflict(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	filter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	engine := &driftingEngine{title: "Initial Upstream Title"}
	service := identify.NewService(engine, twoScraperBundles(), st)

	// 1. ApplyFixMatchRequest commits under client intent and skips scraper on retry even if provider data drifted.
	fixReq := identify.FixMatchRequest{
		RequestID:   "fix-replay-drift-1",
		MediaID:     "media-alpha",
		PluginID:    "demo",
		ScraperID:   "lookup",
		CandidateID: "cand-42",
		ClearLocks:  []string{"summary"},
		Filter:      filter,
	}
	if err := service.ApplyFixMatchRequest(ctx, fixReq); err != nil {
		t.Fatalf("ApplyFixMatchRequest #1: %v", err)
	}
	engine.title = "Mutated Upstream Title"
	if err := service.ApplyFixMatchRequest(ctx, fixReq); err != nil {
		t.Fatalf("ApplyFixMatchRequest replay after provider drift: %v", err)
	}
	if engine.lookups != 1 {
		t.Fatalf("fix-match replay hit scraper (%d lookups), want 1", engine.lookups)
	}
	media, err := st.Media(ctx, "media-alpha", filter)
	if err != nil || media.Title != "Initial Upstream Title" {
		t.Fatalf("media title after replay = %q, %v; want Initial Upstream Title", media.Title, err)
	}

	// Conflicting client intent (different ClearLocks, ordinal, candidate, or kind) returns Conflict without scraping.
	conflictLocks := fixReq
	conflictLocks.ClearLocks = []string{"year"}
	if err := service.ApplyFixMatchRequest(ctx, conflictLocks); !errors.Is(err, store.ErrIdentificationRequestConflict) {
		t.Fatalf("conflict ClearLocks = %v, want ErrIdentificationRequestConflict", err)
	}
	conflictCand := fixReq
	conflictCand.CandidateID = "cand-99"
	if err := service.ApplyFixMatchRequest(ctx, conflictCand); !errors.Is(err, store.ErrIdentificationRequestConflict) {
		t.Fatalf("conflict CandidateID = %v, want ErrIdentificationRequestConflict", err)
	}
	if err := service.ApplyRequest(ctx, identify.IdentifyRequest{
		RequestID:   fixReq.RequestID,
		MediaID:     "media-alpha",
		PluginID:    "demo",
		ScraperID:   "lookup",
		CandidateID: "cand-42",
	}); !errors.Is(err, store.ErrIdentificationRequestConflict) {
		t.Fatalf("cross-kind RequestID reuse = %v, want ErrIdentificationRequestConflict", err)
	}
	if engine.lookups != 1 {
		t.Fatalf("conflict checks hit scraper (%d lookups), want 1", engine.lookups)
	}

	// 2. ApplyRequest follows the same client-intent idempotency contract.
	applyReq := identify.IdentifyRequest{
		RequestID:   "apply-replay-drift-1",
		MediaID:     "media-alpha",
		PluginID:    "demo",
		ScraperID:   "lookup",
		CandidateID: "cand-77",
	}
	engine.title = "Second Candidate Initial"
	if err := service.ApplyRequest(ctx, applyReq); err != nil {
		t.Fatalf("ApplyRequest #1: %v", err)
	}
	engine.title = "Second Candidate Mutated"
	if err := service.ApplyRequest(ctx, applyReq); err != nil {
		t.Fatalf("ApplyRequest replay after drift: %v", err)
	}
	if engine.lookups != 2 {
		t.Fatalf("apply replay hit scraper (%d lookups), want 2", engine.lookups)
	}
	applyConflict := applyReq
	applyConflict.CandidateID = "cand-78"
	if err := service.ApplyRequest(ctx, applyConflict); !errors.Is(err, store.ErrIdentificationRequestConflict) {
		t.Fatalf("ApplyRequest conflict = %v, want ErrIdentificationRequestConflict", err)
	}

	// 3. When entity hydration fails after the revision commits, retrying the
	// same RequestID completes entity hydration even if upstream title drifted.
	entBundles := twoScraperBundles()
	demo := entBundles["demo"]
	demo.EntityKinds = []plugin.EntityKind{{
		Name:      "creator",
		Relations: []plugin.RelationSpec{{Name: "directed-by", ToKind: plugin.MediaKindName, Cardinality: "many"}},
	}}
	entBundles["demo"] = demo
	entEngine := &driftingEngine{
		title:    "Hydrated Initial",
		entities: []scrape.EntityResult{{Kind: "creator", Name: "Director Name", Relation: "directed-by"}},
	}
	entStore := &failOnceEntityStore{Store: st, failNext: true}
	entService := identify.NewService(entEngine, entBundles, st)
	entService.SetEntities(entStore)
	entReq := identify.IdentifyRequest{
		RequestID:   "apply-hydration-retry-1",
		MediaID:     "media-alpha",
		PluginID:    "demo",
		ScraperID:   "lookup",
		CandidateID: "cand-hydrate",
	}
	if err := entService.ApplyRequest(ctx, entReq); err == nil {
		t.Fatal("first ApplyRequest with failing entity store unexpectedly succeeded")
	}
	entEngine.title = "Hydrated Mutated"
	if err := entService.ApplyRequest(ctx, entReq); err != nil {
		t.Fatalf("retry ApplyRequest after entity failure: %v", err)
	}
	if entStore.linkCalls != 2 {
		t.Fatalf("entity LinkEntityToMedia calls = %d, want 2 (initial fail + retry)", entStore.linkCalls)
	}

	// 4. Empty-entity replay still runs applyEntities cleanup if s.entities is wired.
	entEngine.entities = nil
	clearReq := identify.IdentifyRequest{
		RequestID:   "apply-hydration-empty-retry-1",
		MediaID:     "media-alpha",
		PluginID:    "demo",
		ScraperID:   "lookup",
		CandidateID: "cand-empty",
	}
	if err := entService.ApplyRequest(ctx, clearReq); err != nil {
		t.Fatalf("empty-entity ApplyRequest #1: %v", err)
	}
	if err := entService.ApplyRequest(ctx, clearReq); err != nil {
		t.Fatalf("empty-entity ApplyRequest replay: %v", err)
	}
}
