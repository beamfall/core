package identify_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// actionStore is a fakeStore whose identification commit records the
// metadata's artwork as pending artwork actions, as *store.Store does in the
// identification transaction. commitOnly stands in for a process that stops
// after the commit and before any artwork fetch.
type actionStore struct {
	*fakeStore
	mu         sync.Mutex
	revision   int64
	pending    map[string]store.ArtworkAction
	outcomes   map[string]string
	completed  int
	commitOnly bool
}

func (a *actionStore) ApplyMediaIdentification(ctx context.Context, id store.MediaID, metadata store.MediaMetadata, provenance store.MediaMetadataProvenance, fields []store.MediaField) error {
	if err := a.fakeStore.ApplyMediaIdentification(ctx, id, metadata, provenance, fields); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.revision++
	for slot, url := range metadata.Artwork {
		a.pending[slot] = store.ArtworkAction{MediaID: id, Revision: a.revision, Slot: slot, SourceURL: url,
			PluginID: provenance.PluginID, ScraperID: provenance.ScraperID}
	}
	return nil
}

func (a *actionStore) PendingArtworkActionsForMedia(_ context.Context, id store.MediaID) ([]store.ArtworkAction, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.commitOnly {
		return nil, context.Canceled
	}
	return a.sortedPending(id), nil
}

func (a *actionStore) PendingArtworkActions(_ context.Context, limit int) ([]store.ArtworkAction, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.sortedPending("")
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (a *actionStore) sortedPending(id store.MediaID) []store.ArtworkAction {
	var out []store.ArtworkAction
	for _, action := range a.pending {
		if id == "" || action.MediaID == id {
			out = append(out, action)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

func (a *actionStore) CompleteArtworkAction(_ context.Context, action store.ArtworkAction, outcome string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.pending[action.Slot]
	if !ok || current.Revision != action.Revision {
		return false, nil
	}
	delete(a.pending, action.Slot)
	a.outcomes[action.Slot] = outcome
	a.completed++
	return true, nil
}

func (a *actionStore) RecordArtworkActionRetry(_ context.Context, action store.ArtworkAction, outcome string, _ time.Duration, maxAttempts int) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.pending[action.Slot]
	if !ok || current.Revision != action.Revision {
		return false, nil
	}
	current.Attempts++
	a.outcomes[action.Slot] = outcome
	if current.Attempts < maxAttempts {
		a.pending[action.Slot] = current
		return true, nil
	}
	delete(a.pending, action.Slot)
	a.completed++
	return false, nil
}

func artworkActionFixture(commitOnly bool) (*identify.Service, *actionStore, *fakeArtwork, *fakeFetcher) {
	engine := &fakeEngine{lookupResult: scrape.Result{
		Fields: map[string]any{"title": "Blue Harvest"},
		Artwork: map[string]string{
			artwork.SlotPoster:   "https://img.example.test/p/42.jpg",
			artwork.SlotBackdrop: "https://img.example.test/b/42.jpg",
		},
	}}
	fetcher := &fakeFetcher{byURL: map[string]fetchedResource{
		"https://img.example.test/p/42.jpg": {data: []byte("poster-bytes")},
		"https://img.example.test/b/42.jpg": {data: []byte("backdrop-bytes")},
	}}
	st := &actionStore{fakeStore: &fakeStore{}, pending: map[string]store.ArtworkAction{}, outcomes: map[string]string{}, commitOnly: commitOnly}
	art := &fakeArtwork{}
	service := identify.NewService(engine, demoBundles(plugin.ScraperSpec{ID: "lookup"}), st)
	service.SetArtwork(fetcher, art)
	return service, st, art, fetcher
}

// TestArtworkActionsApplyOnceAfterCommit: an identification records its
// artwork fetches as pending actions with its commit and completes each one
// exactly once; a later replay finds nothing pending and imports nothing.
func TestArtworkActionsApplyOnceAfterCommit(t *testing.T) {
	ctx := context.Background()
	service, st, art, _ := artworkActionFixture(false)
	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if st.completed != 2 || len(st.pending) != 0 || st.outcomes[artwork.SlotBackdrop] != "imported" {
		t.Fatalf("completed=%d pending=%v outcomes=%v", st.completed, st.pending, st.outcomes)
	}
	if replayed, err := service.ReplayArtworkActions(ctx); err != nil || replayed != 0 {
		t.Fatalf("ReplayArtworkActions = %d, %v; want 0", replayed, err)
	}
	if len(art.imports) != 2 {
		t.Fatalf("imports = %d, want exactly 2", len(art.imports))
	}
}

// TestArtworkActionsReplayAfterCrashBetweenCommitAndFetch: a process that
// stops after the identification commit and before any fetch leaves its
// artwork actions pending; the restart replay applies each exactly once and a
// second replay converges with no further imports.
func TestArtworkActionsReplayAfterCrashBetweenCommitAndFetch(t *testing.T) {
	ctx := context.Background()
	service, st, art, fetcher := artworkActionFixture(true)
	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if len(st.pending) != 2 || len(art.imports) != 0 || fetcher.calls != 0 {
		t.Fatalf("after crash: pending=%d imports=%d fetches=%d", len(st.pending), len(art.imports), fetcher.calls)
	}
	st.commitOnly = false
	if replayed, err := service.ReplayArtworkActions(ctx); err != nil || replayed != 2 {
		t.Fatalf("ReplayArtworkActions = %d, %v; want 2", replayed, err)
	}
	if replayed, err := service.ReplayArtworkActions(ctx); err != nil || replayed != 0 {
		t.Fatalf("second ReplayArtworkActions = %d, %v; want 0", replayed, err)
	}
	if len(art.imports) != 2 || fetcher.calls != 2 || len(st.pending) != 0 {
		t.Fatalf("imports=%d fetches=%d pending=%d, want 2/2/0", len(art.imports), fetcher.calls, len(st.pending))
	}
}

// TestArtworkActionsRetryFailedFetchAndRespectCap verifies that a failed artwork
// fetch keeps the action pending, succeeds once a subsequent replay fetches
// bytes cleanly, and stops after the configured retry limit on permanent errors.
func TestArtworkActionsRetryFailedFetchAndRespectCap(t *testing.T) {
	ctx := context.Background()
	service, st, art, fetcher := artworkActionFixture(false)
	service.SetArtworkRetry(3, func(int) time.Duration { return 0 })
	savedByURL := fetcher.byURL
	fetcher.byURL = nil
	fetcher.err = errors.New("temporary 503")
	if err := service.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if len(st.pending) != 2 || st.completed != 0 || st.pending[artwork.SlotPoster].Attempts != 1 {
		t.Fatalf("after failed initial fetch: pending=%+v completed=%d", st.pending, st.completed)
	}
	fetcher.err = nil
	fetcher.byURL = savedByURL
	if replayed, err := service.ReplayArtworkActions(ctx); err != nil || replayed != 2 {
		t.Fatalf("ReplayArtworkActions recovery = %d, %v; want 2", replayed, err)
	}
	if len(st.pending) != 0 || len(art.imports) != 2 || st.outcomes[artwork.SlotPoster] != "imported" {
		t.Fatalf("after recovery: pending=%v imports=%d outcomes=%v", st.pending, len(art.imports), st.outcomes)
	}

	service2, st2, art2, fetcher2 := artworkActionFixture(false)
	service2.SetArtworkRetry(2, func(int) time.Duration { return 0 })
	fetcher2.byURL = nil
	fetcher2.err = errors.New("permanent 404")
	if err := service2.Apply(ctx, "media-alpha", "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	if replayed, err := service2.ReplayArtworkActions(ctx); err != nil || replayed != 2 {
		t.Fatalf("ReplayArtworkActions exhausting cap = %d, %v; want 2 terminal", replayed, err)
	}
	if len(st2.pending) != 0 || len(art2.imports) != 0 || st2.outcomes[artwork.SlotPoster] != "fetch-failed" {
		t.Fatalf("after exhausted retries: pending=%v imports=%d outcomes=%v", st2.pending, len(art2.imports), st2.outcomes)
	}
}
