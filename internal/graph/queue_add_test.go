package graph

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestQueueAddReturnsCommittedOrderWhenCardReadFails(t *testing.T) {
	// feature:play-queue
	dir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dir)
	h := newTestHandlerWithStore(st)
	if got := qaItems(t, h, qaAddAlpha, authorizeRevealed, "queueAdd"); !qaEqualIDs(got, "media-alpha") {
		t.Fatalf("seed queue = %v", qaIDs(got))
	}
	breakQueueCardRead(t, st, dir)

	var got []SourceItem
	execGraph(t, h, authorizeRevealed,
		`mutation Add($mediaID: String!) { queueAdd(mediaID: $mediaID, position: 0) { id title restricted } }`,
		map[string]any{"mediaID": "media-archive"}, "queueAdd", &got)
	if !qaEqualIDs(got, "media-archive", "media-alpha") || got[0].Title == "" || !got[0].Restricted {
		t.Fatalf("queueAdd fallback = %#v", got)
	}
	if stored := pqQueue(t, st, pqDefault); !slices.Equal(stored, []string{"media-archive", "media-alpha"}) {
		t.Fatalf("committed queue = %v", stored)
	}
}

func TestQueueAddBoundsCardLookupsAndPreservesOffPageTail(t *testing.T) {
	// feature:play-queue
	dir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dir)
	h := newTestHandlerWithStore(st)
	ctx := t.Context()
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local", "library-archive"}, Revealed: true}
	const pageSize = 50
	const queued = pageSize + 3
	locator := &countingArtworkLocator{
		fakeArtworkLocator: fakeArtworkLocator{media: map[store.MediaID]map[string]bool{}},
		calls:              map[string]int{},
	}
	h.SetArtwork(locator)
	want := make([]string, 0, queued+2)
	var target store.MediaID
	for i := range queued + 1 {
		key := fmt.Sprintf("%02d-queue-tail", i)
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: "library-local", Path: "/queue/" + key + ".flac", Title: key, ContentFingerprint: key,
			Container: "flac", Codec: "flac", Class: store.MediaClassAudio,
		})
		if err != nil {
			t.Fatal(err)
		}
		locator.media[media.ID] = map[string]bool{artwork.SlotPoster: true}
		want = append(want, string(media.ID))
		if i == queued {
			target = media.ID
			break
		}
		if _, err := st.QueueAddForProfile(ctx, pqDefault, "session-revealed", media.ID, nil, filter, h.auth.CertificationConstraints); err != nil {
			t.Fatal(err)
		}
	}

	const add = `mutation Add($mediaID: String!) { queueAdd(mediaID: $mediaID) { id title restricted mediaClass artworkSlots } }`
	var got []SourceItem
	execGraph(t, h, authorizeRevealed, add, map[string]any{"mediaID": string(target)}, "queueAdd", &got)
	if !qaEqualIDs(got, want...) || got[queued].ID != string(target) {
		t.Fatalf("queueAdd IDs = %v, want %v with target at index %d", qaIDs(got), want, queued)
	}
	for i, item := range got {
		enriched := i < pageSize || i == queued
		if item.Title == "" || (item.MediaClass == string(store.MediaClassAudio)) != enriched ||
			(item.ArtworkSlots[artwork.SlotPoster] != "") != enriched {
			t.Errorf("queueAdd row %d = %#v, want title and enriched=%t", i, item, enriched)
		}
	}
	for _, slot := range artwork.KnownSlots {
		if calls := locator.calls[slot]; calls != pageSize+1 {
			t.Errorf("artwork lookup count for %s = %d, want %d", slot, calls, pageSize+1)
		}
	}

	breakQueueCardRead(t, st, dir)
	clear(locator.calls)

	execGraph(t, h, authorizeRevealed, add, map[string]any{"mediaID": "media-archive"}, "queueAdd", &got)
	want = append(want, "media-archive")
	if !qaEqualIDs(got, want...) || got[queued+1].Title == "" || !got[queued+1].Restricted {
		t.Fatalf("queueAdd tail fallback IDs = %v, want %v with the restricted tail card", qaIDs(got), want)
	}
	if len(locator.calls) != 0 {
		t.Errorf("queueAdd fallback made artwork lookups: %v", locator.calls)
	}
	stored, err := st.SourceItems(ctx, filter, store.SourceQuery{Kind: "queue", ProfileID: pqDefault, Limit: len(want)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(stored, want, func(item store.SourceItem, id string) bool { return string(item.ID) == id }) {
		t.Fatalf("stored queue = %#v, want %v", stored, want)
	}
}

func breakQueueCardRead(t *testing.T, st *store.Store, dir string) {
	t.Helper()
	items := []store.SourceItem{{ID: "media-alpha", Kind: "media"}}
	if err := st.AnnotateEngagement(t.Context(), pqDefault, store.LibraryFilter{}, items, nil); err != nil {
		t.Fatalf("verify engagement fixture: %v", err)
	}

	// Break only the engagement read used to enrich the queue response.
	// The queue writer and independent verification use the store's queue API.
	db, err := sql.Open("sqlite", filepath.Join(dir, "db", "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE playback_progress RENAME TO unavailable_playback_progress"); err != nil {
		t.Fatal(err)
	}
	if err := st.AnnotateEngagement(t.Context(), pqDefault, store.LibraryFilter{}, items, nil); err == nil || !strings.Contains(err.Error(), "no such table: playback_progress") {
		t.Fatalf("fault did not reach the engagement read: %v", err)
	}
}

func TestQueueAddReturnsLocalizedForbiddenForLockedMedia(t *testing.T) {
	// feature:play-queue
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	fx := seedMusicFixture(t, st)
	h.auth.SetCertificationScales(map[string][]string{stationPluginID: {"low", "high"}})
	if err := st.SetMediaFields(t.Context(), fx.trackA, []store.MediaField{
		{Name: "certification", Type: store.EntityFieldText, Text: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetProfileCertificationCeiling(t.Context(), pqDefault, store.ProfileCertificationCeiling{
		PluginID: stationPluginID, Ceiling: "low", FilterVisibility: "locked",
	}); err != nil {
		t.Fatal(err)
	}

	authorizeFrenchViewer := func(req *http.Request) {
		authorizeViewer(req)
		*req = *req.WithContext(l10n.WithLocale(req.Context(), "fr"))
	}
	resp := postGraphJSON(t, h, authorizeFrenchViewer, pqAdd, map[string]any{"mediaID": fx.trackA})
	if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != ErrorCodeForbidden ||
		resp.Errors[0].Extensions.Reason != "" || resp.Errors[0].Message != "Cet élément est verrouillé." {
		t.Fatalf("queueAdd locked = %#v, want localized FORBIDDEN", resp.Errors)
	}
	if got := pqQueue(t, st, pqDefault); len(got) != 0 {
		t.Fatalf("locked target changed queue to %v", got)
	}
}

func TestQueueAddRefusesInvalidatedRevealScope(t *testing.T) {
	// feature:play-queue
	for _, test := range []struct {
		name       string
		invalidate func(context.Context, *store.Store) error
	}{
		{"revoked device", func(ctx context.Context, st *store.Store) error { return st.RevokeDevice(ctx, "device-tv") }},
		{"terminated session", func(ctx context.Context, st *store.Store) error { return st.TerminateSession(ctx, "session-revealed") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := storetest.Open(t)
			h := newTestHandlerWithStore(st)
			ctx := t.Context()
			scope, err := h.auth.Scope(ctx, "session-revealed")
			if err != nil || !scope.Revealed {
				t.Fatalf("resolve revealed scope: %v (revealed = %v)", err, scope.Revealed)
			}
			if err := test.invalidate(ctx, st); err != nil {
				t.Fatal(err)
			}
			for _, expected := range []any{nil, string(scope.ProfileID)} {
				_, err := h.queueOps(ctx, scope, map[string]bool{"queueadd": true}, request{Variables: map[string]any{
					"mediaID": "media-archive", "expectedProfileID": expected,
				}})
				if got := fieldGraphError(ctx, "queueAdd", err); got.Extensions.Code != ErrorCodeUnauthenticated || got.Message != "invalid session" {
					t.Errorf("queueAdd with expectedProfileID %v = %#v, want UNAUTHENTICATED", expected, got)
				}
			}
			if got := pqQueue(t, st, pqDefault); len(got) != 0 {
				t.Fatalf("invalidated session changed queue to %v", got)
			}
		})
	}
}
