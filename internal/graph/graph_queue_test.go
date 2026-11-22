package graph

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const queueStateSelection = "{ entries { id mediaID } currentEntryID positionMS shuffle repeatMode }"

func TestLegacyQueueWriteUsesRevealAtWrite(t *testing.T) {
	for _, tc := range []struct {
		field, body string
	}{
		{"queueClear", qaClear},
		{"queueRemove", qaRemoveAlpha},
	} {
		t.Run(tc.field, func(t *testing.T) {
			st := storetest.Open(t)
			h := newTestHandlerWithStore(st)
			qaItems(t, h, qaAddAlpha, authorizeRevealed, "queueAdd")
			if got := qaItems(t, h, qaAddArchive, authorizeRevealed, "queueAdd"); !qaEqualIDs(got, "media-alpha", "media-archive") {
				t.Fatalf("seed queue = %v", qaIDs(got))
			}
			var relocked bool
			h.mutationAuditAppend = func(ctx context.Context, entry store.ActivityEntry) error {
				if entry.Action == store.ActivityMutationAttempt {
					if err := st.SetSessionRevealed(ctx, "session-revealed", false); err != nil {
						t.Fatal(err)
					}
					relocked = true
				}
				return st.AppendActivity(ctx, entry)
			}
			got := qaItems(t, h, tc.body, authorizeRevealed, tc.field)
			if !relocked {
				t.Fatal("request did not reach the relock hook")
			}
			if len(got) != 0 {
				t.Errorf("response after relock = %v, want empty", qaIDs(got))
			}
			if stored := pqQueue(t, st, pqDefault); !slices.Equal(stored, []string{"media-archive"}) {
				t.Errorf("stored queue after relock = %v, want [media-archive]", stored)
			}
		})
	}
}

func TestPlayQueueRemoveEntryUsesRevealAtWrite(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	ctx := t.Context()
	var before PlayQueueInfo
	execGraph(t, h, authorizeRevealed, `mutation {
		queueAddMany(mediaIDs: ["media-alpha", "media-archive", "media-archive"], placement: "end")
	`+queueStateSelection+" }", nil, "queueAddMany", &before)
	if len(before.Entries) != 3 || before.Entries[1].MediaID != "media-archive" || before.Entries[2].MediaID != "media-archive" {
		t.Fatalf("seed queue = %+v", before.Entries)
	}
	scope, err := h.auth.Scope(ctx, "session-revealed")
	if err != nil || !scope.Revealed {
		t.Fatalf("resolve revealed scope: revealed = %v, err = %v", scope.Revealed, err)
	}
	if err := st.SetSessionRevealed(ctx, scope.SessionID, false); err != nil {
		t.Fatal(err)
	}
	got, err := h.writePlayQueue(ctx, scope, map[string]bool{"queueremoveentry": true}, request{Variables: map[string]any{
		"entryID": before.Entries[1].ID, "expectedProfileID": string(scope.ProfileID),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Entries, before.Entries[:1]) {
		t.Errorf("remove after relock returned %+v, want only %+v", got.Entries, before.Entries[:1])
	}
	stored, err := st.PlayQueue(ctx, scope.ProfileID, scope.LibraryFilter())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(playQueueInfo(stored).Entries, before.Entries) {
		t.Errorf("remove after relock changed stored entries to %+v", playQueueInfo(stored).Entries)
	}
}

func TestPlayQueueWriteMapsScheduleEndingBeforeCommit(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	now := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.Local)
	deadline := now.Add(time.Minute)
	ctx := t.Context()
	var writeClockReads int
	var writing bool
	h.auth = auth.NewServiceWithClock(st, func() time.Time {
		if writing {
			writeClockReads++
			if writeClockReads > 1 {
				return deadline
			}
		}
		return now
	})
	if err := st.SetProfileAccessWindows(ctx, pqManaged, []store.ProfileAccessWindow{
		{Weekday: int(now.Weekday()), StartMinute: 600, EndMinute: 601},
	}); err != nil {
		t.Fatal(err)
	}
	h.mutationAuditAppend = func(ctx context.Context, entry store.ActivityEntry) error {
		if entry.Action == store.ActivityMutationAttempt {
			writing = true
		}
		return st.AppendActivity(ctx, entry)
	}
	const body = `{"query":"mutation { queueAddMany(mediaIDs: [\"media-alpha\"], placement: \"end\") { entries { id } } }"}`
	status, resp := postGraphStatus(t, h, body, authorizeManaged)
	if writeClockReads < 2 {
		t.Fatalf("queue add did not reach the commit-time clock check: %+v", resp.Errors)
	}
	if status != http.StatusForbidden || len(resp.Errors) != 1 {
		t.Fatalf("status = %d, errors = %+v, want 403 and one error", status, resp.Errors)
	}
	got := resp.Errors[0]
	if got.Extensions.Code != ErrorCodeForbidden || got.Extensions.Reason != ErrorReasonAccessScheduleEnded || got.Message != accessScheduleEndedMessage {
		t.Errorf("queue add error = %#v, want FORBIDDEN / ACCESS_SCHEDULE_ENDED", got)
	}
	if ids := pqQueue(t, st, pqManaged); len(ids) != 0 {
		t.Errorf("queue add after deadline wrote %v", ids)
	}
}

func TestQueueClearRefusesScheduleEndingBeforeCommit(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	if got := qaItems(t, h, qaAddAlpha, authorizeManaged, "queueAdd"); !qaEqualIDs(got, "media-alpha") {
		t.Fatalf("seed queue = %v", qaIDs(got))
	}
	now := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.Local)
	if err := st.SetProfileAccessWindows(t.Context(), pqManaged, []store.ProfileAccessWindow{
		{Weekday: int(now.Weekday()), StartMinute: 600, EndMinute: 601},
	}); err != nil {
		t.Fatal(err)
	}
	var writing bool
	var writeClockReads int
	h.auth = auth.NewServiceWithClock(st, func() time.Time {
		if writing {
			writeClockReads++
			if writeClockReads >= 3 {
				// Close the window at the final check, after the SQL edit.
				return now.Add(time.Minute)
			}
		}
		return now
	})
	h.mutationAuditAppend = func(ctx context.Context, entry store.ActivityEntry) error {
		if entry.Action == store.ActivityMutationAttempt {
			writing = true
		}
		return st.AppendActivity(ctx, entry)
	}
	status, resp := postGraphStatus(t, h, qaClear, authorizeManaged)
	if writeClockReads < 3 || status != http.StatusForbidden || len(resp.Errors) != 1 {
		t.Errorf("queueClear clock reads = %d, status = %d, errors = %+v, want expiry at commit and one 403 error", writeClockReads, status, resp.Errors)
	} else if got := resp.Errors[0]; got.Extensions.Code != ErrorCodeForbidden || got.Extensions.Reason != ErrorReasonAccessScheduleEnded {
		t.Errorf("queueClear error = %#v, want FORBIDDEN / ACCESS_SCHEDULE_ENDED", got)
	}
	if stored := pqQueue(t, st, pqManaged); !slices.Equal(stored, []string{"media-alpha"}) {
		t.Errorf("stored queue after deadline = %v, want [media-alpha]", stored)
	}
}

func TestPlayQueueStateWriteRequiresProfile(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	const body = `{"query":"mutation { queueSetRepeat(mode: \"one\") { repeatMode } }"}`
	_, errs := post(t, h, body, tokenAuthorizer(t, st, store.APITokenAccessCLI))
	if len(errs) != 1 || errs[0].Extensions.Code != ErrorCodeInvalid || errs[0].Message != "profile ID is required" {
		t.Fatalf("queue write without a Profile errors = %+v, want INVALID / profile ID is required", errs)
	}
}

func TestPlayQueueEntryOperations(t *testing.T) {
	// feature:play-queue
	h := newTestHandlerWithStore(storetest.Open(t))
	var state PlayQueueInfo
	execGraph(t, h, authorizeRevealed, `mutation ($ids: [String!]!) { queueAddMany(mediaIDs: $ids, placement: "end") `+queueStateSelection+" }",
		map[string]any{"ids": []string{"media-alpha", "media-archive", "media-alpha"}}, "queueAddMany", &state)
	if len(state.Entries) != 3 || state.Entries[0].MediaID != "media-alpha" ||
		state.Entries[1].MediaID != "media-archive" || state.Entries[2].MediaID != "media-alpha" {
		t.Fatalf("batch order = %+v", state.Entries)
	}
	a, b, duplicate := state.Entries[0].ID, state.Entries[1].ID, state.Entries[2].ID
	if a == "" || b == "" || duplicate == "" || a == duplicate || b == a || b == duplicate {
		t.Fatalf("invalid entry IDs: %+v", state.Entries)
	}

	write := func(field, operation string) PlayQueueInfo {
		t.Helper()
		var state PlayQueueInfo
		execGraph(t, h, authorizeRevealed, operation+" "+queueStateSelection+" }", nil, field, &state)
		return state
	}
	assertPlayQueueOrder(t, write("queueMoveEntry", fmt.Sprintf(`mutation { queueMoveEntry(entryID: %q, position: 0)`, duplicate)), duplicate, a, b)
	state = write("queueSetCurrent", fmt.Sprintf(`mutation { queueSetCurrent(entryID: %q, positionMS: 90500)`, duplicate))
	if state.CurrentEntryID == nil || *state.CurrentEntryID != duplicate || state.PositionMS != 90500 {
		t.Fatalf("current state = %+v", state)
	}
	assertPlayQueueOrder(t, write("queuePlayNext", fmt.Sprintf(`mutation { queuePlayNext(entryID: %q)`, b)), duplicate, b, a)
	state = write("queueAddMany", `mutation { queueAddMany(mediaIDs: ["media-alpha", "media-archive"], placement: "next")`)
	if len(state.Entries) != 5 || state.Entries[1].MediaID != "media-alpha" || state.Entries[2].MediaID != "media-archive" {
		t.Fatalf("next batch = %+v", state.Entries)
	}
	nextA, nextB := state.Entries[1].ID, state.Entries[2].ID
	assertPlayQueueOrder(t, state, duplicate, nextA, nextB, b, a)
	state = write("queueRemoveEntry", fmt.Sprintf(`mutation { queueRemoveEntry(entryID: %q)`, duplicate))
	assertPlayQueueOrder(t, state, nextA, nextB, b, a)
	if state.CurrentEntryID != nil || state.PositionMS != 0 {
		t.Fatalf("removing current entry left cursor = %+v", state)
	}
	assertPlayQueueOrder(t, write("queuePlayNext", fmt.Sprintf(`mutation { queuePlayNext(entryID: %q)`, a)), a, nextA, nextB, b)

	qaItems(t, h, qaClear, authorizeRevealed, "queueClear")
	var cleared PlayQueueInfo
	execGraph(t, h, authorizeRevealed, "{ playQueue "+queueStateSelection+" }", nil, "playQueue", &cleared)
	if cleared.Entries == nil || len(cleared.Entries) != 0 || cleared.CurrentEntryID != nil || cleared.PositionMS != 0 {
		t.Fatalf("cleared state = %+v", cleared)
	}
}

func TestPlayQueueNewWritesRefuseLockedMedia(t *testing.T) {
	// feature:play-queue
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	fx := seedMusicFixture(t, st)
	h.auth.SetCertificationScales(map[string][]string{stationPluginID: {"low", "high"}})
	const add = `mutation ($ids: [String!]!) { queueAddMany(mediaIDs: $ids, placement: "end") ` + queueStateSelection + " }"
	var before PlayQueueInfo
	execGraph(t, h, authorizeViewer, add, map[string]any{"ids": []string{"media-alpha", string(fx.trackA)}}, "queueAddMany", &before)
	if len(before.Entries) != 2 || before.Entries[1].MediaID != fx.trackA {
		t.Fatalf("seed queue = %+v", before)
	}
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
	assertRefused := func(query string, vars map[string]any) {
		t.Helper()
		resp := postGraphJSON(t, h, authorizeViewer, query, vars)
		if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != ErrorCodeForbidden {
			t.Fatalf("queue write returned %+v, want FORBIDDEN", resp.Errors)
		}
		var after PlayQueueInfo
		execGraph(t, h, authorizeViewer, "{ playQueue "+queueStateSelection+" }", nil, "playQueue", &after)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("refused write changed queue from %+v to %+v", before, after)
		}
	}
	assertRefused(add, map[string]any{"ids": []string{"media-alpha", string(fx.trackA)}})
	assertRefused(`mutation ($id: String!) { queuePlayNext(entryID: $id) `+queueStateSelection+" }",
		map[string]any{"id": before.Entries[1].ID})
}

func TestPlayQueueLegacyReadsDeduplicateBeforePagination(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	var state PlayQueueInfo
	execGraph(t, h, authorizeRevealed, `mutation {
		queueAddMany(mediaIDs: ["media-alpha", "media-alpha", "media-archive", "media-alpha", "media-archive"], placement: "end")
	`+queueStateSelection+" }", nil, "queueAddMany", &state)
	if len(state.Entries) != 5 {
		t.Fatalf("entry-based response lost duplicates: %+v", state.Entries)
	}

	page := func(limit, offset int, want ...string) {
		t.Helper()
		var items []SourceItem
		query := fmt.Sprintf(`{ sourceItems(kind: "queue", limit: %d, offset: %d) { id } }`, limit, offset)
		execGraph(t, h, authorizeRevealed, query, nil, "sourceItems", &items)
		if !qaEqualIDs(items, want...) {
			t.Fatalf("queue page (limit %d, offset %d) = %v, want %v", limit, offset, qaIDs(items), want)
		}
	}
	page(2, 0, "media-alpha", "media-archive")
	page(2, 2)
	page(1, 0, "media-alpha")
	page(1, 1, "media-archive")
	page(1, 2)

	items := qaItems(t, h, qaAddArchive, authorizeRevealed, "queueAdd")
	if !qaEqualIDs(items, "media-alpha", "media-archive") {
		t.Fatalf("legacy queueAdd response = %v", qaIDs(items))
	}
}

func TestPlayQueueLegacyPositionsCountDistinctMedia(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	a, b := "media-alpha", "media-archive"
	third, err := st.CreateManualMedia(t.Context(), store.ManualMedia{LibraryID: "library-local", Title: "Third queue item"})
	if err != nil {
		t.Fatal(err)
	}
	c := string(third)

	for _, tc := range []struct {
		name, field, target string
		initial, want       []string
	}{
		{"move", "queueMove", b, []string{a, a, b, c}, []string{a, c, b}},
		{"add queued media", "queueAdd", b, []string{a, a, b, c}, []string{a, c, b}},
		{"add new media", "queueAdd", b, []string{a, a, c}, []string{a, c, b}},
		{"move duplicate media", "queueMove", a, []string{a, b, a, c}, []string{b, c, a}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			qaItems(t, h, qaClear, authorizeRevealed, "queueClear")
			var state PlayQueueInfo
			execGraph(t, h, authorizeRevealed, `mutation ($ids: [String!]!) { queueAddMany(mediaIDs: $ids, placement: "end") `+queueStateSelection+" }",
				map[string]any{"ids": tc.initial}, "queueAddMany", &state)

			query := fmt.Sprintf(`mutation { %s(mediaID: %q, position: 2) { id } }`, tc.field, tc.target)
			var items []SourceItem
			execGraph(t, h, authorizeRevealed, query, nil, tc.field, &items)
			if !qaEqualIDs(items, tc.want...) {
				t.Fatalf("%s response = %v, want %v", tc.field, qaIDs(items), tc.want)
			}
			var updated PlayQueueInfo
			execGraph(t, h, authorizeRevealed, "{ playQueue "+queueStateSelection+" }", nil, "playQueue", &updated)
			for _, entry := range state.Entries {
				if !slices.ContainsFunc(updated.Entries, func(e PlayQueueEntryInfo) bool { return e.ID == entry.ID }) {
					t.Fatalf("%s lost queue entry %s", tc.field, entry.ID)
				}
			}
		})
	}
}

func TestPlayQueueLegacySamePositionPreservesEntryOrder(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	a, b := "media-alpha", "media-archive"
	third, err := st.CreateManualMedia(t.Context(), store.ManualMedia{LibraryID: "library-local", Title: "Third queue item"})
	if err != nil {
		t.Fatal(err)
	}
	c := string(third)

	for _, field := range []string{"queueMove", "queueAdd"} {
		t.Run(field, func(t *testing.T) {
			qaItems(t, h, qaClear, authorizeRevealed, "queueClear")
			var initial PlayQueueInfo
			execGraph(t, h, authorizeRevealed, `mutation ($ids: [String!]!) { queueAddMany(mediaIDs: $ids, placement: "end") `+queueStateSelection+" }",
				map[string]any{"ids": []string{a, b, b, a, c}}, "queueAddMany", &initial)
			if len(initial.Entries) != 5 {
				t.Fatalf("expected five entries, got %+v", initial.Entries)
			}
			want := make([]string, len(initial.Entries))
			for i, entry := range initial.Entries {
				want[i] = entry.ID
			}

			query := fmt.Sprintf(`mutation { %s(mediaID: %q, position: 1) { id } }`, field, b)
			var items []SourceItem
			execGraph(t, h, authorizeRevealed, query, nil, field, &items)
			if !qaEqualIDs(items, a, b, c) {
				t.Fatalf("%s response = %v", field, qaIDs(items))
			}
			var updated PlayQueueInfo
			execGraph(t, h, authorizeRevealed, "{ playQueue "+queueStateSelection+" }", nil, "playQueue", &updated)
			assertPlayQueueOrder(t, updated, want...)
		})
	}
}

func TestPlayQueueStateAcrossDevicesAndVisibility(t *testing.T) {
	// feature:play-queue
	h := newTestHandlerWithStore(storetest.Open(t))
	var initial, current, shuffled, otherDevice, unshuffled PlayQueueInfo
	execGraph(t, h, authorizeViewer, `mutation { queueAddMany(mediaIDs: ["media-alpha", "media-alpha", "media-alpha"], placement: "end") `+queueStateSelection+" }",
		nil, "queueAddMany", &initial)
	a, b, c := initial.Entries[0].ID, initial.Entries[1].ID, initial.Entries[2].ID
	setCurrent := "mutation { queueSetCurrent(entryID: %q, positionMS: %d) " + queueStateSelection + " }"
	execGraph(t, h, authorizeViewer, fmt.Sprintf(setCurrent, b, 500), nil, "queueSetCurrent", &current)
	execGraph(t, h, authorizeViewer, fmt.Sprintf(setCurrent, b, 2500), nil, "queueSetCurrent", &current)
	execGraph(t, h, authorizeViewer, "mutation { queueSetShuffle(enabled: true) "+queueStateSelection+" }", nil, "queueSetShuffle", &shuffled)
	if !shuffled.Shuffle || shuffled.CurrentEntryID == nil || *shuffled.CurrentEntryID != b ||
		shuffled.PositionMS != 2500 || shuffled.Entries[0].ID != b {
		t.Fatalf("shuffle changed cursor or failed to anchor current entry: %+v", shuffled)
	}
	gotIDs := []string{shuffled.Entries[0].ID, shuffled.Entries[1].ID, shuffled.Entries[2].ID}
	if !slices.Contains(gotIDs, a) || !slices.Contains(gotIDs, c) {
		t.Fatalf("shuffle lost an entry: %v", gotIDs)
	}

	read := "{ playQueue " + queueStateSelection + " }"
	execGraph(t, h, authorizeManager, read, nil, "playQueue", &otherDevice)
	if !reflect.DeepEqual(otherDevice, shuffled) {
		t.Fatalf("other device = %+v, want %+v", otherDevice, shuffled)
	}
	execGraph(t, h, authorizeManager, "mutation { queueSetShuffle(enabled: true) "+queueStateSelection+" }", nil, "queueSetShuffle", &otherDevice)
	assertPlayQueueOrder(t, otherDevice, gotIDs...)
	execGraph(t, h, authorizeManager, "mutation { queueSetShuffle(enabled: false) "+queueStateSelection+" }", nil, "queueSetShuffle", &unshuffled)
	assertPlayQueueOrder(t, unshuffled, a, b, c)
	if unshuffled.Shuffle || unshuffled.CurrentEntryID == nil || *unshuffled.CurrentEntryID != b || unshuffled.PositionMS != 2500 {
		t.Fatalf("disable shuffle changed the cursor: %+v", unshuffled)
	}

	for _, mode := range []store.QueueRepeatMode{store.QueueRepeatAll, store.QueueRepeatOne, store.QueueRepeatOff} {
		query := fmt.Sprintf("mutation { queueSetRepeat(mode: %q) %s }", mode, queueStateSelection)
		execGraph(t, h, authorizeManager, query, nil, "queueSetRepeat", &otherDevice)
		execGraph(t, h, authorizeViewer, read, nil, "playQueue", &current)
		if current.RepeatMode != mode {
			t.Fatalf("other device read repeat = %q, want %q", current.RepeatMode, mode)
		}
	}

	query := `mutation { queueAddMany(mediaIDs: ["media-alpha", "media-archive"], placement: "end") ` + queueStateSelection + " }"
	if errs := postGraphJSON(t, h, authorizeViewer, query, nil).Errors; len(errs) != 1 {
		t.Fatalf("batch containing hidden Media errors = %+v", errs)
	}
	execGraph(t, h, authorizeViewer, read, nil, "playQueue", &current)
	assertPlayQueueOrder(t, current, a, b, c)

	execGraph(t, h, authorizeRevealed, query, nil, "queueAddMany", &current)
	hidden := current.Entries[len(current.Entries)-1].ID
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(setCurrent, hidden, 10), nil, "queueSetCurrent", &current)
	execGraph(t, h, authorizeViewer, "mutation { queueSetCurrent(entryID: null, positionMS: 0) "+queueStateSelection+" }",
		nil, "queueSetCurrent", &otherDevice)
	if otherDevice.CurrentEntryID != nil || otherDevice.PositionMS != 0 || len(otherDevice.Entries) != 4 {
		t.Fatalf("locked device saw hidden state: %+v", otherDevice)
	}
	qaItems(t, h, qaClear, authorizeViewer, "queueClear")
	execGraph(t, h, authorizeRevealed, read, nil, "playQueue", &current)
	assertPlayQueueOrder(t, current, hidden)
	if current.CurrentEntryID == nil || *current.CurrentEntryID != hidden || current.PositionMS != 10 {
		t.Fatalf("locked clear changed hidden state: %+v", current)
	}
	execGraph(t, h, qaAuthorizeManaged, read, nil, "playQueue", &otherDevice)
	assertPlayQueueOrder(t, otherDevice)
}

func TestPlayQueueWritesHonorExpectedProfile(t *testing.T) {
	// feature:play-queue
	h := newTestHandlerWithStore(storetest.Open(t))
	for field, args := range map[string]string{
		"queueAddMany":     `mediaIDs: ["media-alpha"], placement: "end"`,
		"queueMoveEntry":   `entryID: "missing", position: 0`,
		"queueRemoveEntry": `entryID: "missing"`,
		"queuePlayNext":    `entryID: "missing"`,
		"queueSetCurrent":  `entryID: "missing", positionMS: 0`,
		"queueSetShuffle":  `enabled: true`,
		"queueSetRepeat":   `mode: "one"`,
	} {
		query := fmt.Sprintf("mutation { %s(%s, expectedProfileID: %q) %s }", field, args, pqManaged, queueStateSelection)
		pqWantMismatch(t, field, postGraphJSON(t, h, authorizeViewer, query, nil).Errors)
	}
}

func assertPlayQueueOrder(t *testing.T, state PlayQueueInfo, want ...string) {
	t.Helper()
	got := make([]string, 0, len(state.Entries))
	for _, entry := range state.Entries {
		got = append(got, entry.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("queue IDs = %v, want %v", got, want)
	}
}
