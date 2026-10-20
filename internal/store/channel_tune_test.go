package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// importChannelMedia seeds n visible media into library-local and returns their
// IDs in import order.
func importChannelMedia(t *testing.T, st *Store, n int) []MediaID {
	t.Helper()
	ctx := context.Background()
	ids := make([]MediaID, 0, n)
	for i := 0; i < n; i++ {
		suffix := string(rune('a' + i))
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               "/media/chan-" + suffix + ".mp4",
			Title:              "Channel Item " + suffix,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: "fp-chan-" + suffix,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, media.ID)
	}
	return ids
}

func sameSet(a, b []MediaID) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[MediaID]int, len(a))
	for _, id := range a {
		seen[id]++
	}
	for _, id := range b {
		seen[id]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

// TestTuneChannelProducesPlayableOrdinaryQueue pins CHAN-2:
// tuning a Channel compiles its source + ordering policy into an ORDINARY Play
// Queue via the standard queue-replace path. A channel-unaware reader (the plain
// "queue" Source, no channel vocabulary) plays exactly the tuned window.
func TestTuneChannelProducesPlayableOrdinaryQueue(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := profileDefaultFilter(true)
	importChannelMedia(t, st, 6)

	channel, err := st.CreateChannel(ctx, Channel{
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Name:       "Chrono Channel",
		SourceExpr: `{"kind":"all-items"}`,
		Ordering:   ChannelOrderingChronological,
	}, time.Unix(1000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	window, err := st.TuneChannel(ctx, "profile-default", channel.ID, false, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) == 0 {
		t.Fatal("tuned window is empty")
	}

	// A client with no channel awareness just reads its Play Queue and plays it.
	queue := mustSourceItems(t, st, filter, SourceQuery{Kind: "queue", ProfileID: "profile-default"})
	if !sameSet(sourceItemIDs(window), sourceItemIDs(queue)) {
		t.Fatalf("tuned window %v != installed queue %v", sourceItemIDs(window), sourceItemIDs(queue))
	}

	// Chronological ordering installs the source's natural feed order.
	source := mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", LibraryID: "library-local", ProfileID: "profile-default", Limit: channelTuneWindow})
	wantIDs := sourceItemIDs(source)
	gotIDs := sourceItemIDs(queue)
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("queue len %d, want %d", len(gotIDs), len(wantIDs))
	}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("chronological order = %v, want %v", gotIDs, wantIDs)
		}
	}
}

// TestTuneChannelIsScopedToOwningProfile pins CHAN-TUNE-OWNERSHIP-1: tuning
// honors a Channel's configuration (source expression, ordering, repeat), so a
// Channel's owner — not just Library visibility — gates the tune. Every Channel
// here lives in library-local, which BOTH seeded Profiles can see, and the
// foreign Profile first tunes its OWN Channel in that Library; the only
// remaining explanation for the denial below is Channel ownership.
func TestTuneChannelIsScopedToOwningProfile(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	ownerFilter := profileDefaultFilter(true)
	managedFilter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}
	importChannelMedia(t, st, 4)

	newChannel := func(owner ProfileID, name string, at int64) Channel {
		t.Helper()
		c, err := st.CreateChannel(ctx, Channel{
			ProfileID:  owner,
			LibraryID:  "library-local",
			Name:       name,
			SourceExpr: `{"kind":"all-items"}`,
			Ordering:   ChannelOrderingChronological,
		}, time.Unix(at, 0).UTC())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	// The graph layer's createChannel always stamps a ProfileID, so a genuinely
	// server-shared (NULL-profile) Channel is seeded through the store directly.
	ownerCh := newChannel("profile-default", "Owner Channel", 1000)
	managedCh := newChannel("profile-managed", "Managed Channel", 1001)
	sharedCh := newChannel("", "Shared Channel", 1002)

	// Baseline: profile-managed can see library-local and tune its own Channel there.
	if window, err := st.TuneChannel(ctx, "profile-managed", managedCh.ID, true, managedFilter); err != nil {
		t.Fatalf("managed profile tuning its own channel: %v", err)
	} else if len(window) == 0 {
		t.Fatal("managed profile tuned its own channel to an empty window")
	}

	// Denied: another Profile's private Channel, despite shared Library visibility.
	window, err := st.TuneChannel(ctx, "profile-managed", ownerCh.ID, true, managedFilter)
	if !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("tuning a foreign private channel: err = %v, want ErrChannelNotFound", err)
	}
	if window != nil {
		t.Fatalf("denied tune returned a window: %v", sourceItemIDs(window))
	}

	// The owning Profile is unaffected.
	if window, err := st.TuneChannel(ctx, "profile-default", ownerCh.ID, true, ownerFilter); err != nil {
		t.Fatalf("owning profile tuning its own channel: %v", err)
	} else if len(window) == 0 {
		t.Fatal("owning profile tuned its own channel to an empty window")
	}

	// A server-shared Channel stays tunable by any Profile that sees its Library.
	for _, tc := range []struct {
		profile ProfileID
		filter  LibraryFilter
	}{
		{"profile-default", ownerFilter},
		{"profile-managed", managedFilter},
	} {
		shared, err := st.TuneChannel(ctx, tc.profile, sharedCh.ID, true, tc.filter)
		if err != nil {
			t.Fatalf("%s tuning the server-shared channel: %v", tc.profile, err)
		}
		if len(shared) == 0 {
			t.Fatalf("%s tuned the server-shared channel to an empty window", tc.profile)
		}
	}
}

// TestTuneChannelDeniedWhenLibraryNotVisible pins
// CHAN-TUNE-LIBRARY-VISIBILITY-1: tuning a Channel whose Library the caller
// cannot see is refused outright, instead of resolving the source to zero
// items and replacing the Profile's existing Play Queue with an empty one.
// The owning Profile (which can see the Library) is unaffected.
func TestTuneChannelDeniedWhenLibraryNotVisible(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	ownerFilter := profileDefaultFilter(true)
	// library-local excluded: the caller can see library-archive only.
	blindFilter := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}, Revealed: true}
	mediaIDs := importChannelMedia(t, st, 4)

	channel, err := st.CreateChannel(ctx, Channel{
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Name:       "Hidden Library Channel",
		SourceExpr: `{"kind":"all-items"}`,
		Ordering:   ChannelOrderingChronological,
	}, time.Unix(1000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	// Seed an existing Play Queue so the denial can be proven to leave it
	// untouched rather than replacing it with an empty one.
	if err := st.QueueReplaceWithStation(ctx, "profile-default", mediaIDs[:2], false, ownerFilter); err != nil {
		t.Fatal(err)
	}
	before := mustSourceItems(t, st, ownerFilter, SourceQuery{Kind: "queue", ProfileID: "profile-default"})
	if len(before) == 0 {
		t.Fatal("seeded play queue is empty")
	}

	window, err := st.TuneChannel(ctx, "profile-default", channel.ID, true, blindFilter)
	if !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("tuning a channel in an invisible library: err = %v, want ErrChannelNotFound", err)
	}
	if window != nil {
		t.Fatalf("denied tune returned a window: %v", sourceItemIDs(window))
	}

	after := mustSourceItems(t, st, ownerFilter, SourceQuery{Kind: "queue", ProfileID: "profile-default"})
	if !sameSet(sourceItemIDs(before), sourceItemIDs(after)) {
		t.Fatalf("play queue changed after denied tune: before %v after %v", sourceItemIDs(before), sourceItemIDs(after))
	}

	// The owning Profile, which CAN see library-local, is unaffected.
	if window, err := st.TuneChannel(ctx, "profile-default", channel.ID, true, ownerFilter); err != nil {
		t.Fatalf("owning profile tuning its own visible-library channel: %v", err)
	} else if len(window) == 0 {
		t.Fatal("owning profile tuned its own channel to an empty window")
	}
}

// TestTuneChannelShuffleIsDeterministicPermutation pins the shuffle ordering
// policy: it reorders the same source set (a permutation, no items lost/added)
// and is reproducible across tunes (seeded by Channel ID).
func TestTuneChannelShuffleIsDeterministicPermutation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := profileDefaultFilter(true)
	importChannelMedia(t, st, 8)

	shuffleCh, err := st.CreateChannel(ctx, Channel{
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Name:       "Shuffle Channel",
		SourceExpr: `{"kind":"all-items"}`,
		Ordering:   ChannelOrderingShuffle,
	}, time.Unix(1000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	chronoCh, err := st.CreateChannel(ctx, Channel{
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Name:       "Chrono Channel",
		SourceExpr: `{"kind":"all-items"}`,
		Ordering:   ChannelOrderingChronological,
	}, time.Unix(1001, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	first, err := st.TuneChannel(ctx, "profile-default", shuffleCh.ID, true, filter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.TuneChannel(ctx, "profile-default", shuffleCh.ID, true, filter)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs, secondIDs := sourceItemIDs(first), sourceItemIDs(second)

	// Reproducible: the same Channel tunes to the same order every time.
	for i := range firstIDs {
		if firstIDs[i] != secondIDs[i] {
			t.Fatalf("shuffle not deterministic: %v vs %v", firstIDs, secondIDs)
		}
	}

	chrono, err := st.TuneChannel(ctx, "profile-default", chronoCh.ID, true, filter)
	if err != nil {
		t.Fatal(err)
	}
	chronoIDs := sourceItemIDs(chrono)

	// Same set of media, but shuffle diverges from the natural order.
	if !sameSet(firstIDs, chronoIDs) {
		t.Fatalf("shuffle changed the media set: %v vs %v", firstIDs, chronoIDs)
	}
	same := true
	for i := range firstIDs {
		if firstIDs[i] != chronoIDs[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("shuffle order matched chronological order: %v", firstIDs)
	}
}
