package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// holderLockFixture serves the scraper-holder documents and imports one Media
// into a demo Library.
func holderLockFixture(t *testing.T, docs map[string]string) (*store.Store, store.LibraryID, store.MediaID, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc, ok := docs[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(server.Close)
	ctx := t.Context()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	library, err := st.CreateLibrary(ctx, "Items", false, "demo", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: library.ID, Path: "/synthetic/locked.mkv",
		Title: "locked", Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: "fp-holder-locked",
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, library.ID, media.ID, server.URL
}

func holderOf(t *testing.T, service *identify.Service, media store.MediaID) store.EntityID {
	t.Helper()
	chain, err := service.ContainerEntitiesForMedia(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) == 0 {
		return ""
	}
	return chain[0].ID
}

// TestIdentifyLockedHolderKeepsCuratedTitle (CORE-3131): an operator locks
// the Media to holder 77 and titles it. A lookup naming the same group links
// nothing, since the lock keeps the scraper's link out, so the container map
// must not retitle the holder the scraper never linked.
func TestIdentifyLockedHolderKeepsCuratedTitle(t *testing.T) {
	ctx := context.Background()
	st, library, media, endpoint := holderLockFixture(t, map[string]string{
		"/item/1": `{"title":"Origin","release_date":"1999-03-31","group":{"id":77,"name":"Scraped Name"}}`,
	})
	set := identify.EntityIDForName(library, "set", "77")
	if err := st.UpsertEntity(ctx, store.Entity{ID: set, LibraryID: library, Kind: "set", Title: "Curated Set"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetOperatorMediaRelation(ctx, store.EntityRelation{
		FromEntityID: set, Name: "set-member", ToMediaID: media, Cardinality: "one",
	}, store.Entity{}, store.LibraryFilter{AllLibraries: true}, store.ActivityEntry{
		OperationID: "lock-holder", ProfileID: "profile-default", OccurredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), scraperHolderBundle(endpoint, "group"), st)
	service.SetEntities(st)
	if err := service.Apply(ctx, media, "demo", "item", "1"); err != nil {
		t.Fatal(err)
	}
	entity, err := st.Entity(ctx, set, metadataLibraryFilter)
	if err != nil || entity.Title != "Curated Set" {
		t.Fatalf("locked holder title = %q, %v; want Curated Set kept", entity.Title, err)
	}
	if got := holderOf(t, service, media); got != set {
		t.Fatalf("locked Media held by %q, want %q", got, set)
	}
}

// failingIdentification refuses the Media metadata commit.
type failingIdentification struct{ *store.Store }

func (failingIdentification) ApplyMediaIdentification(context.Context, store.MediaID, store.MediaMetadata, store.MediaMetadataProvenance, []store.MediaField) error {
	return errors.New("synthetic metadata commit failure")
}

// TestFixMatchOrdinalFailureKeepsHolderLink (CORE-3128): a Fix Match with an
// ordinal hydrates only after the Media's metadata commits. When that commit
// fails, or the request is rejected, the scraper's earlier holder link stays:
// neither a stale link removal nor a move to the new candidate's holder runs.
func TestFixMatchOrdinalFailureKeepsHolderLink(t *testing.T) {
	ctx := context.Background()
	st, library, media, endpoint := holderLockFixture(t, map[string]string{
		"/item/1": `{"title":"Origin","release_date":"1999-03-31","group":{"id":77,"name":"Synthetic Set"}}`,
		"/item/3": `{"title":"Loner","release_date":"1999-03-31","group":null}`,
		"/item/4": `{"title":"Origin","release_date":"1999-03-31","group":{"id":88,"name":"Other Set"}}`,
	})
	bundle := scraperHolderBundle(endpoint, "group")
	service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), bundle, st)
	service.SetEntities(st)
	if err := service.Apply(ctx, media, "demo", "item", "1"); err != nil {
		t.Fatal(err)
	}
	set := identify.EntityIDForName(library, "set", "77")
	if got := holderOf(t, service, media); got != set {
		t.Fatalf("held by %q, want %q", got, set)
	}
	failing := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), bundle, failingIdentification{st})
	failing.SetEntities(st)
	manual := store.MediaOrdinal{Sequence: "1", Index: 2}
	if err := failing.ApplyFixMatch(ctx, media, "demo", "item", "3", nil, &manual, "profile-default"); err == nil {
		t.Fatal("fix match succeeded despite the failed metadata commit")
	}
	if got := holderOf(t, service, media); got != set {
		t.Fatalf("held by %q after a failed fix match, want the old holder %q", got, set)
	}
	if err := service.ApplyFixMatch(ctx, media, "demo", "item", "4", []string{"no-such-field"}, &manual, "profile-default"); err == nil {
		t.Fatal("fix match accepted an unknown lock to clear")
	}
	if got := holderOf(t, service, media); got != set {
		t.Fatalf("held by %q after a rejected fix match, want the old holder %q", got, set)
	}
	if err := service.ApplyFixMatch(ctx, media, "demo", "item", "3", nil, &manual, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if got := holderOf(t, service, media); got != "" {
		t.Fatalf("held by %q after a committed fix match with no group, want none", got)
	}
}

// TestFixMatchZeroEntityDateIsAbsent (CORE-3128): hydration follows the
// metadata commit, so a scraped Entity value the store refuses would leave a
// committed ordinal that no retry can hydrate. 0001-01-01 parses as Go's zero
// time, which the store reads as no value; it is dropped as absent instead.
func TestFixMatchZeroEntityDateIsAbsent(t *testing.T) {
	ctx := context.Background()
	st, library, media, endpoint := holderLockFixture(t, map[string]string{
		"/item/1": `{"title":"Origin","release_date":"1999-03-31","group":{"id":77,"name":"Original Set","first":"2000-01-01"}}`,
		"/item/2": `{"title":"Replacement","release_date":"1999-03-31","group":{"id":88,"name":"New Set","first":"0001-01-01"}}`,
	})
	bundle := scraperHolderBundle(endpoint, "group")
	bundle.EntityKinds[0].Fields = append(bundle.EntityKinds[0].Fields, plugin.FieldSpec{Name: "first", Type: "date"})
	bundle.Scrapers[0].EntityMaps[0].FieldMap["first"] = "first"
	service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), bundle, st)
	service.SetEntities(st)
	if err := service.Apply(ctx, media, "demo", "item", "1"); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyFixMatch(ctx, media, "demo", "item", "2", nil, &store.MediaOrdinal{Index: 3}, "profile-default"); err != nil {
		t.Fatalf("fix match with a zero entity date: %v", err)
	}
	replacement := identify.EntityIDForName(library, "set", "88")
	if got := holderOf(t, service, media); got != replacement {
		t.Fatalf("held by %q, want %q", got, replacement)
	}
	entity, err := st.Entity(ctx, replacement, metadataLibraryFilter)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range entity.Fields {
		if field.Name == "first" {
			t.Fatalf("zero date stored as %v, want the field absent", field.Date)
		}
	}
}
