package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

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
	storetest.BreakEngagementRead(t, st, dir, pqDefault)

	resp := postGraphJSON(t, h, authorizeRevealed,
		`mutation Add($mediaID: String!) { queueAdd(mediaID: $mediaID, position: 0) { id title restricted } }`,
		map[string]any{"mediaID": "media-archive"})
	got := committedQueuePage(t, resp, "queueAdd")
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
	const queued = 10_000
	locator := &countingArtworkLocator{
		fakeArtworkLocator: fakeArtworkLocator{media: map[store.MediaID]map[string]bool{}},
		calls:              map[string]int{},
	}
	h.SetArtwork(locator)
	ids := storetest.SeedAudioMedia(t, dir, "library-local", queued+1)
	for _, id := range ids[:pageSize] {
		locator.media[id] = map[string]bool{artwork.SlotPoster: true}
	}
	if err := st.QueueReplaceWithStation(ctx, pqDefault, ids[:queued], false, filter); err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	want := make([]string, pageSize)
	for i, id := range ids[:pageSize] {
		want[i] = string(id)
	}
	target := string(ids[queued])

	const add = `mutation Add($mediaID: String!) { tail: queueAdd(mediaID: $mediaID) { id title mediaClass artworkSlots } }`
	resp := postGraphJSON(t, h, authorizeRevealed, add, map[string]any{"mediaID": target})
	got := committedQueuePage(t, resp, "tail")
	if !qaEqualIDs(got, want...) {
		t.Fatalf("queueAdd returned %d rows, want the original %d-row first page", len(got), pageSize)
	}
	for i, item := range got {
		if item.Title == "" || item.MediaClass != string(store.MediaClassAudio) || item.ArtworkSlots[artwork.SlotPoster] == "" {
			t.Errorf("queueAdd row %d = %#v, want enriched card", i, item)
		}
	}
	for _, slot := range artwork.KnownSlots {
		if calls := locator.calls[slot]; calls != pageSize {
			t.Errorf("artwork lookup count for %s = %d, want %d", slot, calls, pageSize)
		}
	}

	storetest.BreakEngagementRead(t, st, dir, pqDefault)
	clear(locator.calls)

	resp = postGraphJSON(t, h, authorizeRevealed, add, map[string]any{"mediaID": "media-archive"})
	got = committedQueuePage(t, resp, "tail")
	if !qaEqualIDs(got, want...) || got[0].Title == "" {
		t.Fatalf("queueAdd fallback returned %d rows, want the original first page with basic fields", len(got))
	}
	if len(locator.calls) != 0 {
		t.Errorf("queueAdd fallback made artwork lookups: %v", locator.calls)
	}
	stored, err := st.SourceItems(ctx, filter, store.SourceQuery{Kind: "queue", ProfileID: pqDefault, Limit: 3, Offset: queued})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || stored[0].ID != store.MediaID(target) || stored[1].ID != "media-archive" {
		t.Fatalf("stored queue tail = %#v, want [%s media-archive]", stored, target)
	}
}

func committedQueuePage(t *testing.T, resp graphTestResponse, key string) []SourceItem {
	t.Helper()
	if len(resp.Errors) != 0 {
		t.Fatalf("queueAdd errors = %+v", resp.Errors)
	}
	var items []SourceItem
	if err := json.Unmarshal(resp.Data[key], &items); err != nil {
		t.Fatal(err)
	}
	return items
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

func TestQueueAddKeepsCommittedAliasWhenLaterAliasIsRefused(t *testing.T) {
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

	resp := postGraphJSON(t, h, authorizeViewer, `
		mutation Add($locked: String!) {
			next: queueAdd(mediaID: "media-alpha", position: 0) { id }
			tail: queueAdd(mediaID: $locked) { id }
		}
	`, map[string]any{"locked": fx.trackA})
	if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != ErrorCodeForbidden ||
		!slices.Equal(resp.Errors[0].Path, []string{"tail"}) {
		t.Fatalf("queueAdd errors = %+v, want FORBIDDEN at tail", resp.Errors)
	}
	var items []SourceItem
	if err := json.Unmarshal(resp.Data["next"], &items); err != nil {
		t.Fatal(err)
	}
	if !qaEqualIDs(items, "media-alpha") || string(resp.Data["tail"]) != "null" {
		t.Fatalf("queueAdd data = %v, want only next's committed page", resp.Data)
	}
	if got := pqQueue(t, st, pqDefault); !slices.Equal(got, []string{"media-alpha"}) {
		t.Fatalf("committed queue = %v, want [media-alpha]", got)
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
				_, err := h.queueAdd(ctx, scope, request{Variables: map[string]any{
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

func TestQueueAddResolvesProfileAfterSwitch(t *testing.T) {
	// feature:play-queue
	for _, test := range []struct {
		name     string
		expected map[string]any
	}{
		{name: "omitted"},
		{name: "null", expected: map[string]any{"expectedProfileID": nil}},
		{name: "explicit", expected: map[string]any{"expectedProfileID": pqDefault}},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := storetest.Open(t)
			h := newTestHandlerWithStore(st)
			ctx := t.Context()
			scope, err := h.auth.Scope(ctx, "session-default")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionProfile(ctx, scope.SessionID, pqManaged); err != nil {
				t.Fatal(err)
			}
			variables := map[string]any{"mediaID": "media-alpha"}
			for arg, value := range test.expected {
				variables[arg] = value
			}
			items, err := h.queueAdd(ctx, scope, request{Variables: variables})
			if test.name == "explicit" {
				pqWantMismatch(t, "queueAdd", []graphError{fieldGraphError(ctx, "queueAdd", err)})
				if got := pqQueue(t, st, pqManaged); len(got) != 0 {
					t.Fatalf("explicit precondition changed the new Profile's queue: %v", got)
				}
			} else {
				if err != nil || !qaEqualIDs(items, "media-alpha") {
					t.Fatalf("queueAdd after switch = %v, %v", qaIDs(items), err)
				}
				if got := pqQueue(t, st, pqManaged); !slices.Equal(got, []string{"media-alpha"}) {
					t.Fatalf("new Profile's queue = %v, want [media-alpha]", got)
				}
			}
			if got := pqQueue(t, st, pqDefault); len(got) != 0 {
				t.Fatalf("old Profile's queue changed: %v", got)
			}
		})
	}
}

func TestQueueAddUsesSwitchedProfilesEligibility(t *testing.T) {
	// feature:play-queue
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	ctx := t.Context()
	scope, err := h.auth.Scope(ctx, "session-revealed")
	if err != nil || !scope.Revealed {
		t.Fatalf("resolve revealed scope: %v (revealed = %v)", err, scope.Revealed)
	}
	library, err := st.CreateLibrary(ctx, "Managed library", false, "", pqManaged)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: library.ID, Path: "/managed/item.mkv", Title: "Managed item", ContentFingerprint: "queue-managed",
		Container: "matroska", Codec: "h264", Class: store.MediaClassVideo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionProfile(ctx, scope.SessionID, pqManaged); err != nil {
		t.Fatal(err)
	}

	add := func(id store.MediaID) ([]SourceItem, error) {
		items, err := h.queueAdd(ctx, scope, request{Variables: map[string]any{"mediaID": string(id)}})
		return items, err
	}
	if _, err := add("media-archive"); err == nil || fieldGraphError(ctx, "queueAdd", err).Extensions.Code != ErrorCodeNotFound {
		t.Fatalf("queueAdd retained the previous Profile's Reveal: %v", err)
	}
	if items, err := add(media.ID); err != nil || !qaEqualIDs(items, string(media.ID)) {
		t.Fatalf("queueAdd with the new Profile's library grant = %v, %v", qaIDs(items), err)
	}
	if err := st.SetProfileAccessWindows(ctx, pqManaged, []store.ProfileAccessWindow{
		{Weekday: (int(time.Now().Weekday()) + 3) % 7, StartMinute: 0, EndMinute: 1440},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := add("media-alpha"); err == nil || fieldGraphError(ctx, "queueAdd", err).Extensions.Code != ErrorCodeUnauthenticated {
		t.Fatalf("queueAdd outside the new Profile's schedule = %v, want UNAUTHENTICATED", err)
	}
	if got := pqQueue(t, st, pqManaged); !slices.Equal(got, []string{string(media.ID)}) {
		t.Fatalf("new Profile's queue = %v, want [%s]", got, media.ID)
	}
}
