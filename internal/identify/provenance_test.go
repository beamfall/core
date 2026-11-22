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
