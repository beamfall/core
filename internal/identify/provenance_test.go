package identify_test

import (
	"context"
	"errors"
	"testing"

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
