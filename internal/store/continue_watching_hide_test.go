package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func continueWatchingIDs(t *testing.T, st *Store, profile ProfileID) []MediaID {
	t.Helper()
	items := mustSourceItems(t, st, itemOpenFilter, SourceQuery{Kind: "continue-watching", ProfileID: profile})
	ids := make([]MediaID, 0, len(items))
	for _, item := range items {
		ids = append(ids, MediaID(item.ID))
	}
	return ids
}

func TestContinueWatchingHideKeepsProgressAndReturnsOnPlay(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	start := timeToMillis(time.Now().UTC())
	report := func(position int, eventTime int64) {
		t.Helper()
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
			ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: position, DurationSeconds: 120, EventTimeMS: eventTime,
		}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
	}
	report(30, start)
	if ids := continueWatchingIDs(t, st, "profile-default"); !slices.Contains(ids, "media-alpha") {
		t.Fatalf("continue-watching = %v, want media-alpha before the removal", ids)
	}

	if err := st.SetContinueWatchingHidden(ctx, "profile-default", "media-alpha", true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if ids := continueWatchingIDs(t, st, "profile-default"); slices.Contains(ids, "media-alpha") {
		t.Fatalf("continue-watching = %v still lists the removed Media", ids)
	}
	progress, _, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if progress.PositionSeconds != 30 || progress.Watched {
		t.Fatalf("removal changed progress: %+v", progress)
	}
	// The resume-progress filter still sees a removed item: it is in progress.
	inProgress := mustSourceItems(t, st, itemOpenFilter, SourceQuery{Kind: "resume-progress", Value: "in-progress", ProfileID: "profile-default"})
	if !slices.ContainsFunc(inProgress, func(item SourceItem) bool { return item.ID == "media-alpha" }) {
		t.Fatal("resume-progress dropped a removed item")
	}

	// A paused player repeating the same position leaves the removal in place.
	report(30, start+1000)
	if ids := continueWatchingIDs(t, st, "profile-default"); slices.Contains(ids, "media-alpha") {
		t.Fatal("a same-position report undid the removal")
	}

	// Rebuilding the projection reproduces the removal.
	live := snapshotEngagementProjections(t, st.eventsR)
	if err := st.rebuildEngagementProjections(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if name := firstDifferingProjection(live, snapshotEngagementProjections(t, st.eventsR)); name != "" {
		t.Fatalf("rebuild differs from live projection %q", name)
	}
	if ids := continueWatchingIDs(t, st, "profile-default"); slices.Contains(ids, "media-alpha") {
		t.Fatal("rebuild brought back the removed Media")
	}

	// Playing again puts it back.
	report(40, start+2000)
	if ids := continueWatchingIDs(t, st, "profile-default"); !slices.Contains(ids, "media-alpha") {
		t.Fatalf("continue-watching = %v, want media-alpha after playing again", ids)
	}
}

func TestContinueWatchingHideUndoAndEdges(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	before := continueWatchingIDs(t, st, "profile-default")
	if err := st.SetContinueWatchingHidden(ctx, "profile-default", "media-alpha", true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	// A Profile with no progress on the Media has nothing to remove and gets no row.
	if err := st.SetContinueWatchingHidden(ctx, "profile-managed", "media-alpha", true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-managed", "media-alpha"); err != nil || ok {
		t.Fatalf("removal created progress: ok=%v err=%v", ok, err)
	}
	// Another Profile's shelf is untouched.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-managed", MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if ids := continueWatchingIDs(t, st, "profile-managed"); !slices.Contains(ids, "media-alpha") {
		t.Fatal("removal reached another Profile")
	}
	// Undo restores the shelf exactly, in its original place.
	if err := st.SetContinueWatchingHidden(ctx, "profile-default", "media-alpha", false, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if after := continueWatchingIDs(t, st, "profile-default"); !slices.Equal(before, after) {
		t.Fatalf("after undo continue-watching = %v, want %v", after, before)
	}

	// A hidden Media is refused as unknown.
	if err := st.SetContinueWatchingHidden(ctx, "profile-default", "media-archive", true, itemOpenFilter); err == nil {
		t.Fatal("SetContinueWatchingHidden accepted a hidden Media")
	}
	if err := st.SetContinueWatchingHidden(ctx, "profile-default", "", true, itemOpenFilter); err == nil {
		t.Fatal("empty media ID accepted")
	}
}
