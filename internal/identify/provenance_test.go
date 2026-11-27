package identify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/artwork"
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

// CORE-3516 #8: retrying an identical FixMatchRequest after the first commit
// updated provenance (and auto-cleared the operator ordinal) converges on the
// original receipt instead of conflicting on recomputed ClearOrdinal.
func TestApplyFixMatchRequestRetryAfterProvenanceChangeConverges(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	ord := store.MediaOrdinal{Sequence: "season", Index: 4}
	initialTitle := "Initial Match"
	if _, err := st.CommitMediaIdentification(ctx, "media-alpha", store.IdentificationCommit{
		Metadata:   store.MediaMetadata{Title: &initialTitle, OperatorOrdinal: &ord},
		Provenance: store.MediaMetadataProvenance{PluginID: "demo", ScraperID: "lookup", CandidateID: "cand-1"},
	}); err != nil {
		t.Fatalf("seed prior match: %v", err)
	}
	service := identify.NewService(scraperEngine{}, twoScraperBundles(), st)
	req := identify.FixMatchRequest{
		RequestID:   "fix-retry-1",
		MediaID:     "media-alpha",
		PluginID:    "demo",
		ScraperID:   "lookup",
		CandidateID: "cand-2",
		Filter:      store.LibraryFilter{AllLibraries: true, Revealed: true},
	}
	if err := service.ApplyFixMatchRequest(ctx, req); err != nil {
		t.Fatalf("first ApplyFixMatchRequest: %v", err)
	}
	if ord, err := st.MediaOperatorOrdinal(ctx, "media-alpha"); err != nil || ord != nil {
		t.Fatalf("operator ordinal after candidate change = %+v, err=%v; want nil", ord, err)
	}
	// Identical retry after provenance is already cand-2 must converge cleanly.
	if err := service.ApplyFixMatchRequest(ctx, req); err != nil {
		t.Fatalf("replayed ApplyFixMatchRequest: %v", err)
	}
	revisions, err := st.MediaIdentificationRevisions(ctx, "media-alpha")
	if err != nil || len(revisions) != 2 {
		t.Fatalf("revisions after retry = %+v, err=%v; want 2 rows", revisions, err)
	}
	// Reusing the same RequestID for a different intent must still conflict.
	conflictReq := req
	conflictReq.CandidateID = "cand-3"
	if err := service.ApplyFixMatchRequest(ctx, conflictReq); !errors.Is(err, store.ErrIdentificationRequestConflict) {
		t.Fatalf("conflicting candidate retry err = %v, want ErrIdentificationRequestConflict", err)
	}
	keepReq := req
	keepReq.ClearLocks = []string{}
	if err := service.ApplyFixMatchRequest(ctx, keepReq); !errors.Is(err, store.ErrIdentificationRequestConflict) {
		t.Fatalf("conflicting ClearLocks=[] retry err = %v, want ErrIdentificationRequestConflict", err)
	}
	ordReq := req
	ordReq.Ordinal = &store.MediaOrdinal{Sequence: "season", Index: 9}
	if err := service.ApplyFixMatchRequest(ctx, ordReq); !errors.Is(err, store.ErrIdentificationRequestConflict) {
		t.Fatalf("conflicting Ordinal retry err = %v, want ErrIdentificationRequestConflict", err)
	}
}

// CORE-3516 follow-up: retrying an identical FixMatchRequest after the scraper
// candidate metadata changed converges without calling Lookup again or
// conflicting on mutable provider output, while still draining any pending
// committed artwork actions.
func TestApplyFixMatchRequestRetryAfterScraperChangesConverges(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields:  map[string]any{"title": "First Provider Title"},
		Artwork: map[string]string{artwork.SlotPoster: "https://img.example.test/p/42.jpg"},
	}}
	fetcher := &fakeFetcher{byURL: map[string]fetchedResource{
		"https://img.example.test/p/42.jpg": {data: []byte("poster-bytes")},
	}}
	art := &fakeArtwork{}
	service := identify.NewService(engine, twoScraperBundles(), st)
	req := identify.FixMatchRequest{
		RequestID:   "review-retry-mutable",
		MediaID:     "media-alpha",
		PluginID:    "demo",
		ScraperID:   "lookup",
		CandidateID: "42",
		Ordinal:     &store.MediaOrdinal{Sequence: "season", Index: 7},
		Filter:      store.LibraryFilter{AllLibraries: true, Revealed: true},
	}
	if err := service.ApplyFixMatchRequest(ctx, req); err != nil {
		t.Fatalf("baseline ApplyFixMatchRequest: %v", err)
	}
	// Mutate scraper output after commit (simulating remote provider drift) and
	// attach artwork importer for replay of the persisted artwork action.
	engine.lookupResult = scrape.Result{
		Fields:  map[string]any{"title": "Updated Provider Title"},
		Artwork: map[string]string{artwork.SlotPoster: "https://img.example.test/p/42-new.jpg"},
	}
	service.SetArtwork(fetcher, art)
	if err := service.ApplyFixMatchRequest(ctx, req); err != nil {
		t.Fatalf("identical retry after provider drift: %v", err)
	}
	media, err := st.Media(ctx, "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || media.Title != "First Provider Title" {
		t.Fatalf("committed title drifted on replay: %+v, %v", media, err)
	}
	// Conflicting ordinal on the same RequestID must still conflict even after provider drift.
	conflictOrd := req
	conflictOrd.Ordinal = &store.MediaOrdinal{Sequence: "season", Index: 8}
	if err := service.ApplyFixMatchRequest(ctx, conflictOrd); !errors.Is(err, store.ErrIdentificationRequestConflict) {
		t.Fatalf("conflicting ordinal err = %v, want ErrIdentificationRequestConflict", err)
	}
}

type failingEntityStore struct {
	*store.Store
	failNext  bool
	upserted  []store.Entity
	relations []store.EntityRelation
}

func (f *failingEntityStore) EnsureEntity(ctx context.Context, entity store.Entity) error {
	if f.failNext {
		f.failNext = false
		return errors.New("transient entity store failure")
	}
	f.upserted = append(f.upserted, entity)
	return f.Store.EnsureEntity(ctx, entity)
}

func (f *failingEntityStore) UpsertEntity(ctx context.Context, entity store.Entity) error {
	if f.failNext {
		f.failNext = false
		return errors.New("transient entity store failure")
	}
	f.upserted = append(f.upserted, entity)
	return f.Store.UpsertEntity(ctx, entity)
}

func (f *failingEntityStore) UpsertHydratedEntityRelation(ctx context.Context, relation store.EntityRelation) error {
	f.relations = append(f.relations, relation)
	return f.Store.UpsertHydratedEntityRelation(ctx, relation)
}

func (f *failingEntityStore) UpsertHydratedEntityRelationWritten(ctx context.Context, relation store.EntityRelation) (bool, error) {
	f.relations = append(f.relations, relation)
	return f.Store.UpsertHydratedEntityRelationWritten(ctx, relation)
}

// CORE-3516 follow-up: if CommitFixMatch succeeds and post-commit entity
// hydration fails, retrying the identical FixMatchRequest resumes entity
// hydration without re-committing or failing on drifted provider titles.
func TestApplyFixMatchRequestRetryResumesFailedEntityHydration(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	bundles := twoScraperBundles()
	demo := bundles["demo"]
	demo.Scrapers[0].EntityMaps = []plugin.EntityMap{
		{Kind: "director", Relation: "directed-by", Path: "crew"},
	}
	bundles["demo"] = demo

	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Committed Match Title"},
		Entities: []scrape.EntityResult{
			{Kind: "director", Relation: "directed-by", Name: "Ada Director"},
		},
	}}
	entities := &failingEntityStore{Store: st, failNext: true}
	service := identify.NewService(engine, bundles, st)
	service.SetEntities(entities)

	req := identify.FixMatchRequest{
		RequestID:   "fix-entity-retry",
		MediaID:     "media-alpha",
		PluginID:    "demo",
		ScraperID:   "lookup",
		CandidateID: "42",
		Filter:      store.LibraryFilter{AllLibraries: true, Revealed: true},
	}
	if err := service.ApplyFixMatchRequest(ctx, req); err == nil {
		t.Fatal("first ApplyFixMatchRequest succeeded despite entity store failure")
	}
	if len(entities.upserted) != 0 || len(entities.relations) != 0 {
		t.Fatalf("failed attempt recorded entities=%v relations=%v", entities.upserted, entities.relations)
	}

	// Simulate provider title drift before the operator retries the same RequestID.
	engine.lookupResult.Fields["title"] = "Drifted Provider Title"
	if err := service.ApplyFixMatchRequest(ctx, req); err != nil {
		t.Fatalf("retry ApplyFixMatchRequest after entity failure: %v", err)
	}
	if len(entities.upserted) != 1 || entities.upserted[0].Title != "Ada Director" || len(entities.relations) != 1 {
		t.Fatalf("retried hydration entities=%+v relations=%+v, want Ada Director + 1 relation", entities.upserted, entities.relations)
	}
	media, err := st.Media(ctx, "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || media.Title != "Committed Match Title" {
		t.Fatalf("media after replay = %+v, %v; want Committed Match Title", media, err)
	}
	revisions, err := st.MediaIdentificationRevisions(ctx, "media-alpha")
	if err != nil || len(revisions) != 1 {
		t.Fatalf("revisions = %+v, %v; want 1 revision", revisions, err)
	}

	// Once a newer Fix Match supersedes req, retrying the historical RequestID
	// must NOT overwrite the newer match's entities/relations.
	engine.lookupResult = scrape.Result{
		Fields: map[string]any{"title": "Newer Match Title"},
		Entities: []scrape.EntityResult{
			{Kind: "director", Relation: "directed-by", Name: "Grace Director"},
		},
	}
	newerReq := identify.FixMatchRequest{
		RequestID:   "fix-entity-newer",
		MediaID:     "media-alpha",
		PluginID:    "demo",
		ScraperID:   "lookup",
		CandidateID: "99",
		Filter:      store.LibraryFilter{AllLibraries: true, Revealed: true},
	}
	if err := service.ApplyFixMatchRequest(ctx, newerReq); err != nil {
		t.Fatalf("newer ApplyFixMatchRequest: %v", err)
	}
	upsertedBeforeSupersededRetry := len(entities.upserted)
	relationsBeforeSupersededRetry := len(entities.relations)
	engine.lookupResult = scrape.Result{
		Fields: map[string]any{"title": "Committed Match Title"},
		Entities: []scrape.EntityResult{
			{Kind: "director", Relation: "directed-by", Name: "Ada Director"},
		},
	}
	if err := service.ApplyFixMatchRequest(ctx, req); err != nil {
		t.Fatalf("superseded retry ApplyFixMatchRequest: %v", err)
	}
	if len(entities.upserted) != upsertedBeforeSupersededRetry || len(entities.relations) != relationsBeforeSupersededRetry {
		t.Fatalf("superseded retry rewrote entities/relations: upserted=%d->%d relations=%d->%d",
			upsertedBeforeSupersededRetry, len(entities.upserted),
			relationsBeforeSupersededRetry, len(entities.relations))
	}
}
