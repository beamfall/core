package graph

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func captureGraphLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// A field cut short because the client abandoned the request (the web UI
// aborts superseded requests) is not a server failure: debug, never error.
func TestGraphFailMessageClientCancelLogsDebug(t *testing.T) {
	logs := captureGraphLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := graphFailMessage(ctx, "sourceItems", fmt.Errorf("query rows: %w", context.Canceled))

	if got != "sourceItems unavailable" {
		t.Fatalf("message = %q, want generic unavailable", got)
	}
	if out := logs.String(); !strings.Contains(out, "level=DEBUG") || strings.Contains(out, "level=ERROR") {
		t.Fatalf("client cancel should log at DEBUG only, got %q", out)
	}
}

// A cancellation the server caused while the request is still live stays an
// error: only the request's own context ending marks a client abort.
func TestGraphFailMessageServerSideCancelStaysError(t *testing.T) {
	logs := captureGraphLogs(t)

	graphFailMessage(context.Background(), "sourceItems", context.Canceled)

	if out := logs.String(); !strings.Contains(out, "level=ERROR") {
		t.Fatalf("live-request cancel should log at ERROR, got %q", out)
	}
}

// Once dispatch has run, a client disconnect must not drop the finished
// audit entry: the trail would keep an attempt with no outcome.
func TestMutationFinishedAuditSurvivesClientDisconnect(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	reqCtx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	var finishCtxErr error
	h.mutationAuditAppend = func(ctx context.Context, entry store.ActivityEntry) error {
		if entry.Action == store.ActivityMutationFinished {
			disconnect() // the client leaves after the resolver ran
			finishCtxErr = ctx.Err()
		}
		return st.AppendActivity(ctx, entry)
	}
	body := `{"query":"mutation F($mediaID: String!, $favorite: Boolean!){ setFavorite(mediaID:$mediaID, favorite:$favorite){ id } }","variables":{"mediaID":"media-alpha","favorite":true}}`
	req := httptest.NewRequestWithContext(reqCtx, http.MethodPost, "/api/graphql", strings.NewReader(body))
	authorizeViewer(req)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if finishCtxErr != nil {
		t.Fatalf("finished audit ran under a cancelled context: %v", finishCtxErr)
	}
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{
		Filter:                   store.LibraryFilter{Revealed: true, AllLibraries: true},
		ProfileID:                "profile-default",
		Limit:                    10,
		IncludeMutationLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Action != store.ActivityMutationFinished || entries[0].TargetType != "graphql:success" {
		t.Fatalf("activity entries = %+v, want attempt and finished", entries)
	}
}

// countingArtworkLocator counts version lookups per slot.
type countingArtworkLocator struct {
	fakeArtworkLocator
	calls map[string]int
}

func (c *countingArtworkLocator) ArtworkVersion(id store.MediaID, slot string) (string, bool) {
	c.calls[slot]++
	return c.fakeArtworkLocator.ArtworkVersion(id, slot)
}

func TestSourceItemResolvesEachArtworkSlotOnce(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	locator := &countingArtworkLocator{
		fakeArtworkLocator: fakeArtworkLocator{media: map[store.MediaID]map[string]bool{
			"media-alpha": {artwork.SlotPoster: true},
		}},
		calls: map[string]int{},
	}
	h.SetArtwork(locator)

	item := h.sourceItem(store.SourceItem{ID: "media-alpha", Kind: "media"}, false)

	if item.Artwork == nil || item.Artwork.Poster != item.ArtworkSlots[artwork.SlotPoster] {
		t.Fatalf("poster = %+v, slots = %v; want the poster slot URL", item.Artwork, item.ArtworkSlots)
	}
	for _, slot := range artwork.KnownSlots {
		if locator.calls[slot] != 1 {
			t.Fatalf("version lookups = %v, want one per slot", locator.calls)
		}
	}
}
