package graph

import (
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

func TestQueueAddReturnsOffPageTailWhenCardReadFails(t *testing.T) {
	// feature:play-queue
	dir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dir)
	h := newTestHandlerWithStore(st)
	ctx := t.Context()
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local", "library-archive"}, Revealed: true}
	const pageSize = 50
	want := make([]string, 0, pageSize+1)
	for i := range pageSize {
		key := fmt.Sprintf("%02d-queue-tail", i)
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: "library-local", Path: "/queue/" + key + ".flac", Title: key, ContentFingerprint: key,
			Container: "flac", Codec: "flac", Class: store.MediaClassAudio,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.QueueAddForProfile(ctx, pqDefault, "session-revealed", media.ID, nil, filter, h.auth.CertificationConstraints); err != nil {
			t.Fatal(err)
		}
		want = append(want, string(media.ID))
	}
	if got := pqQueue(t, st, pqDefault); !slices.Equal(got, want) {
		t.Fatalf("seed queue = %v, want %v", got, want)
	}
	breakQueueCardRead(t, st, dir)

	var got []SourceItem
	execGraph(t, h, authorizeRevealed,
		`mutation Add($mediaID: String!) { queueAdd(mediaID: $mediaID) { id title restricted } }`,
		map[string]any{"mediaID": "media-archive"}, "queueAdd", &got)
	want = append(want, "media-archive")
	if !qaEqualIDs(got, want...) || got[pageSize].Title == "" || !got[pageSize].Restricted {
		t.Fatalf("queueAdd tail fallback IDs = %v, want %v with the restricted tail card", qaIDs(got), want)
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
