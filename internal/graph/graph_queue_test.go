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

	write := func(field, args string) PlayQueueInfo {
		t.Helper()
		var state PlayQueueInfo
		execGraph(t, h, authorizeRevealed, "mutation { "+field+"("+args+") "+queueStateSelection+" }", nil, field, &state)
		return state
	}
	assertPlayQueueOrder(t, write("queueMoveEntry", fmt.Sprintf("entryID: %q, position: 0", duplicate)), duplicate, a, b)
	state = write("queueSetCurrent", fmt.Sprintf("entryID: %q, positionMS: 90500", duplicate))
	if state.CurrentEntryID == nil || *state.CurrentEntryID != duplicate || state.PositionMS != 90500 {
		t.Fatalf("current state = %+v", state)
	}
	assertPlayQueueOrder(t, write("queuePlayNext", fmt.Sprintf("entryID: %q", b)), duplicate, b, a)
	state = write("queueAddMany", `mediaIDs: ["media-alpha", "media-archive"], placement: "next"`)
	if len(state.Entries) != 5 || state.Entries[1].MediaID != "media-alpha" || state.Entries[2].MediaID != "media-archive" {
		t.Fatalf("next batch = %+v", state.Entries)
	}
	nextA, nextB := state.Entries[1].ID, state.Entries[2].ID
	assertPlayQueueOrder(t, state, duplicate, nextA, nextB, b, a)
	state = write("queueRemoveEntry", fmt.Sprintf("entryID: %q", duplicate))
	assertPlayQueueOrder(t, state, nextA, nextB, b, a)
	if state.CurrentEntryID != nil || state.PositionMS != 0 {
		t.Fatalf("removing current entry left cursor = %+v", state)
	}
	assertPlayQueueOrder(t, write("queuePlayNext", fmt.Sprintf("entryID: %q", a)), a, nextA, nextB, b)

	qaItems(t, h, qaClear, authorizeRevealed, "queueClear")
	var cleared PlayQueueInfo
	execGraph(t, h, authorizeRevealed, "{ playQueue "+queueStateSelection+" }", nil, "playQueue", &cleared)
	if cleared.Entries == nil || len(cleared.Entries) != 0 || cleared.CurrentEntryID != nil || cleared.PositionMS != 0 {
		t.Fatalf("cleared state = %+v", cleared)
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
