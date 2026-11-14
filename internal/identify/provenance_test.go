package identify_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

type provenanceRecord struct {
	pluginID, scraperID string
}

type fakeProvenanceStore struct {
	byMedia map[store.MediaID]provenanceRecord
	writes  int
	err     error
}

func (f *fakeProvenanceStore) SetMediaMetadataProvenance(_ context.Context, mediaID store.MediaID, pluginID, scraperID string) error {
	if f.err != nil {
		return f.err
	}
	if f.byMedia == nil {
		f.byMedia = map[store.MediaID]provenanceRecord{}
	}
	f.byMedia[mediaID] = provenanceRecord{pluginID: pluginID, scraperID: scraperID}
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
	service := identify.NewService(engine, twoScraperBundles(), &fakeStore{})
	service.SetProvenance(provenance)

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if got, want := provenance.byMedia["media-alpha"], (provenanceRecord{"demo", "lookup"}); got != want {
		t.Fatalf("provenance = %#v, want %#v", got, want)
	}
}

func TestApplyOverwritesMetadataProvenanceOnReidentify(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{Fields: map[string]any{"title": "Blue Harvest"}}}
	provenance := &fakeProvenanceStore{}
	service := identify.NewService(engine, twoScraperBundles(), &fakeStore{})
	service.SetProvenance(provenance)
	ctx := context.Background()

	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(ctx, "media-alpha", "demo", "second", "43"); err != nil {
		t.Fatal(err)
	}
	if got, want := provenance.byMedia["media-alpha"], (provenanceRecord{"demo", "second"}); got != want {
		t.Fatalf("provenance after re-identify = %#v, want %#v", got, want)
	}
	if provenance.writes != 2 {
		t.Fatalf("provenance writes = %d, want 2", provenance.writes)
	}
}

func TestApplyPropagatesProvenanceWriteError(t *testing.T) {
	engine := &fakeEngine{lookupResult: scrape.Result{Fields: map[string]any{"title": "Blue Harvest"}}}
	boom := errors.New("provenance write failed")
	service := identify.NewService(engine, twoScraperBundles(), &fakeStore{})
	service.SetProvenance(&fakeProvenanceStore{err: boom})

	if err := service.Apply(context.Background(), "media-alpha", "demo", "lookup", "42"); !errors.Is(err, boom) {
		t.Fatalf("Apply err = %v, want %v", err, boom)
	}
}

func TestApplyFailureBeforeMetadataWriteRecordsNoProvenance(t *testing.T) {
	engine := &fakeEngine{err: errors.New("lookup failed")}
	provenance := &fakeProvenanceStore{}
	service := identify.NewService(engine, twoScraperBundles(), &fakeStore{})
	service.SetProvenance(provenance)

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

// interleavingStore holds the "lookup" scraper's metadata write open until a
// second write lands or a short wait passes, so an unserialized Apply lets
// the "second" scraper's metadata and provenance commit in between.
type interleavingStore struct {
	mu          sync.Mutex
	title       string
	firstWrote  chan struct{}
	secondWrote chan struct{}
}

func (s *interleavingStore) UpdateMediaMetadata(_ context.Context, _ store.MediaID, metadata store.MediaMetadata) error {
	s.mu.Lock()
	s.title = *metadata.Title
	s.mu.Unlock()
	if *metadata.Title == "lookup" {
		close(s.firstWrote)
		select {
		case <-s.secondWrote:
		case <-time.After(200 * time.Millisecond):
		}
		return nil
	}
	close(s.secondWrote)
	return nil
}

type lockedProvenanceStore struct {
	mu      sync.Mutex
	scraper string
}

func (s *lockedProvenanceStore) SetMediaMetadataProvenance(_ context.Context, _ store.MediaID, _, scraperID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scraper = scraperID
	return nil
}

// TestConcurrentApplyKeepsMetadataAndProvenanceTogether pins that two
// identifications of one Media item racing each other leave the attribution
// naming the scraper whose metadata was kept.
func TestConcurrentApplyKeepsMetadataAndProvenanceTogether(t *testing.T) {
	metadata := &interleavingStore{firstWrote: make(chan struct{}), secondWrote: make(chan struct{})}
	provenance := &lockedProvenanceStore{}
	service := identify.NewService(scraperEngine{}, twoScraperBundles(), metadata)
	service.SetProvenance(provenance)
	ctx := context.Background()

	errs := make(chan error, 2)
	go func() { errs <- service.Apply(ctx, "media-alpha", "demo", "lookup", "42") }()
	<-metadata.firstWrote
	go func() { errs <- service.Apply(ctx, "media-alpha", "demo", "second", "43") }()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if metadata.title != provenance.scraper {
		t.Fatalf("metadata from %q attributed to %q", metadata.title, provenance.scraper)
	}
}
