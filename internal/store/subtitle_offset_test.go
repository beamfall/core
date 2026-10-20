package store_test

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestSubtitleOffsetRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1000, 0).UTC()
	fingerprint := "sha256:track-one"

	if got, err := st.SubtitleOffset(ctx, "profile-default", fingerprint); err != nil || got != 0 {
		t.Fatalf("SubtitleOffset on empty store = %d, err %v; want 0", got, err)
	}
	if err := st.PutSubtitleOffset(ctx, "profile-default", fingerprint, 500, now); err != nil {
		t.Fatalf("PutSubtitleOffset: %v", err)
	}
	got, err := st.SubtitleOffset(ctx, "profile-default", fingerprint)
	if err != nil || got != 500 {
		t.Fatalf("SubtitleOffset = %d, err %v; want 500", got, err)
	}
	// A negative nudge (pull cues earlier) round-trips.
	if err := st.PutSubtitleOffset(ctx, "profile-default", fingerprint, -250, now); err != nil {
		t.Fatalf("PutSubtitleOffset(negative): %v", err)
	}
	if got, err := st.SubtitleOffset(ctx, "profile-default", fingerprint); err != nil || got != -250 {
		t.Fatalf("SubtitleOffset = %d, err %v; want -250", got, err)
	}
	// Nudging back to centre removes the row rather than storing a zero.
	if err := st.PutSubtitleOffset(ctx, "profile-default", fingerprint, 0, now); err != nil {
		t.Fatalf("PutSubtitleOffset(zero): %v", err)
	}
	if got, err := st.SubtitleOffset(ctx, "profile-default", fingerprint); err != nil || got != 0 {
		t.Fatalf("SubtitleOffset after centring = %d, err %v; want 0", got, err)
	}
}

func TestSubtitleOffsetIsPerProfileAndPerTrack(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1000, 0).UTC()
	if err := st.PutSubtitleOffset(ctx, "profile-default", "sha256:a", 400, now); err != nil {
		t.Fatalf("PutSubtitleOffset: %v", err)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-managed", "sha256:a"); got != 0 {
		t.Fatalf("another profile sees the nudge (%d); it is per-profile", got)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", "sha256:b"); got != 0 {
		t.Fatalf("another track sees the nudge (%d); it is per-track", got)
	}
}

// AC-1: the nudge key is the track BYTE fingerprint, so it
// survives a rename/remux (bytes unchanged) and stops matching only when a real
// content/timing edit rewrites the bytes.
func TestSubtitleOffsetKeySurvivesRenameAndInvalidatesOnEdit(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1000, 0).UTC()
	trackBytes := []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n")

	fingerprint, err := store.SubtitleTrackFingerprint(bytes.NewReader(trackBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutSubtitleOffset(ctx, "profile-default", fingerprint, 300, now); err != nil {
		t.Fatalf("PutSubtitleOffset: %v", err)
	}

	// Rename/remux: the same bytes under a different path hash identically, so the
	// nudge is still found.
	renamed, err := store.SubtitleTrackFingerprint(bytes.NewReader(trackBytes))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", renamed); got != 300 {
		t.Fatalf("nudge lost across rename/remux: got %d, want 300", got)
	}

	// A genuine timing edit rewrites the bytes: the new hash simply stops matching,
	// so the stale nudge cannot double-shift the corrected track.
	edited, err := store.SubtitleTrackFingerprint(bytes.NewReader([]byte("WEBVTT\n\n00:00:01.300 --> 00:00:02.300\nHello\n")))
	if err != nil {
		t.Fatal(err)
	}
	if edited == fingerprint {
		t.Fatal("a timing edit produced the same fingerprint")
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", edited); got != 0 {
		t.Fatalf("stale nudge matched the edited track: got %d, want 0", got)
	}
}

func TestPutSubtitleOffsetValidates(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1000, 0).UTC()
	if err := st.PutSubtitleOffset(ctx, "profile-nope", "sha256:a", 100, now); err == nil {
		t.Fatal("accepted an unknown profile")
	}
	if err := st.PutSubtitleOffset(ctx, "profile-default", "", 100, now); err == nil {
		t.Fatal("accepted an empty fingerprint")
	}
	if err := st.PutSubtitleOffset(ctx, "profile-default", "sha256:a", 1<<30, now); err == nil {
		t.Fatal("accepted an out-of-range offset")
	}
}

func TestPruneSubtitleOffsetsReapsStaleRows(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	old := time.Unix(1000, 0).UTC()
	recent := time.Unix(9000, 0).UTC()
	if err := st.PutSubtitleOffset(ctx, "profile-default", "sha256:old", 100, old); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSubtitleOffset(ctx, "profile-default", "sha256:new", 200, recent); err != nil {
		t.Fatal(err)
	}
	removed, err := st.PruneSubtitleOffsets(ctx, time.Unix(5000, 0).UTC(), nil)
	if err != nil {
		t.Fatalf("PruneSubtitleOffsets: %v", err)
	}
	if removed != 1 {
		t.Fatalf("pruned %d rows, want 1", removed)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", "sha256:new"); got != 200 {
		t.Fatalf("prune removed the recent nudge: got %d", got)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", "sha256:old"); got != 0 {
		t.Fatalf("prune left the stale nudge: got %d", got)
	}
}

// The prune POLICY: age alone never deletes a nudge whose track fingerprint is
// still reachable. The viewer need not replay the track inside the retention
// window to keep the correction.
func TestPruneSubtitleOffsetsSparesReachableRowsRegardlessOfAge(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	written := time.Unix(1000, 0).UTC()

	for _, fingerprint := range []string{"sha256:reachable", "sha256:orphaned"} {
		if err := st.PutSubtitleOffset(ctx, "profile-default", fingerprint, 700, written); err != nil {
			t.Fatalf("PutSubtitleOffset(%s): %v", fingerprint, err)
		}
	}

	removed, err := st.PruneSubtitleOffsets(ctx, time.Unix(5000, 0).UTC(), []string{"sha256:reachable"})
	if err != nil {
		t.Fatalf("PruneSubtitleOffsets: %v", err)
	}
	if removed != 1 {
		t.Fatalf("pruned %d rows, want only the orphaned one", removed)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", "sha256:reachable"); got != 700 {
		t.Fatalf("reachable nudge = %d, want 700 — the prune reaped a live fingerprint", got)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", "sha256:orphaned"); got != 0 {
		t.Fatalf("orphaned nudge = %d, want it reaped", got)
	}
}

// A lookup that MISSES creates nothing.
func TestSubtitleOffsetLookupMissDoesNotCreateARow(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(9000, 0).UTC()

	if got, err := st.SubtitleOffset(ctx, "profile-default", "sha256:absent"); err != nil || got != 0 {
		t.Fatalf("SubtitleOffset = %d, err %v; want 0", got, err)
	}
	removed, err := st.PruneSubtitleOffsets(ctx, now.Add(time.Hour), nil)
	if err != nil {
		t.Fatalf("PruneSubtitleOffsets: %v", err)
	}
	if removed != 0 {
		t.Fatalf("pruned %d rows, want 0 — a lookup miss wrote a row", removed)
	}
}

// CONCURRENCY: the nudge is keyed by (profile, track) PRIMARY KEY and written
// through an upsert, so the LAST write wins and the row never forks. Sequential
// first: the later write supersedes the earlier one outright.
func TestPutSubtitleOffsetLastWriteWins(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	first := time.Unix(1000, 0).UTC()
	second := time.Unix(2000, 0).UTC()

	if err := st.PutSubtitleOffset(ctx, "profile-default", "sha256:a", 100, first); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSubtitleOffset(ctx, "profile-default", "sha256:a", -250, second); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", "sha256:a"); got != -250 {
		t.Fatalf("offset = %d, want the later write (-250)", got)
	}

	// An OLDER timestamp arriving late still wins on value: the row records the
	// last write received, not the highest clock — two devices nudging the same
	// track must not leave the loser's value in place.
	if err := st.PutSubtitleOffset(ctx, "profile-default", "sha256:a", 999, first); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", "sha256:a"); got != 999 {
		t.Fatalf("offset = %d, want the last write received (999)", got)
	}
}

// Concurrent writers to the SAME (profile, track): every write must succeed, and
// the survivor must be one of the values actually written — never an error, a
// torn value, or a duplicated row.
func TestPutSubtitleOffsetConcurrentWritersLeaveOneWinner(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1000, 0).UTC()
	const writers = 8

	values := make([]int, 0, writers)
	for i := 1; i <= writers; i++ {
		values = append(values, i*100)
	}
	errs := make(chan error, writers)
	start := make(chan struct{})
	for _, value := range values {
		go func() {
			<-start
			errs <- st.PutSubtitleOffset(ctx, "profile-default", "sha256:contended", value, now)
		}()
	}
	close(start)
	for range writers {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent PutSubtitleOffset: %v", err)
		}
	}

	got, err := st.SubtitleOffset(ctx, "profile-default", "sha256:contended")
	if err != nil {
		t.Fatalf("SubtitleOffset: %v", err)
	}
	if !slices.Contains(values, got) {
		t.Fatalf("offset = %d, want one of the written values %v", got, values)
	}
	// Exactly one row survived: pruning past every stamp removes one, not many.
	removed, err := st.PruneSubtitleOffsets(ctx, now.Add(time.Hour), nil)
	if err != nil {
		t.Fatalf("PruneSubtitleOffsets: %v", err)
	}
	if removed != 1 {
		t.Fatalf("pruned %d rows, want exactly 1 surviving row", removed)
	}
}

// A concurrent CENTRING (offset 0 = delete) racing a nudge must leave the track
// in one of the two coherent states — cleared, or carrying the nudge — and must
// never error.
func TestPutSubtitleOffsetCentringRacesNudgeCoherently(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1000, 0).UTC()
	if err := st.PutSubtitleOffset(ctx, "profile-default", "sha256:race", 500, now); err != nil {
		t.Fatal(err)
	}

	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, value := range []int{0, 750} {
		go func() {
			<-start
			errs <- st.PutSubtitleOffset(ctx, "profile-default", "sha256:race", value, now)
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent PutSubtitleOffset: %v", err)
		}
	}

	got, err := st.SubtitleOffset(ctx, "profile-default", "sha256:race")
	if err != nil {
		t.Fatalf("SubtitleOffset: %v", err)
	}
	if got != 0 && got != 750 {
		t.Fatalf("offset = %d, want either cleared (0) or the nudge (750)", got)
	}
}
