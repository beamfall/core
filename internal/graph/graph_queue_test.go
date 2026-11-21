package graph

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const queueStateSelection = "{ entries { id mediaID } currentEntryID positionMS shuffle repeatMode }"

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
