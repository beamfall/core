package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSubscriptionRetentionKeepLastNCountsDistinctMediaAndRestoresTrash(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	keep := 2
	sub, media := subscriptionRetentionFixture(t, st, RetentionKeepLastN, &keep, keep+7+2)
	if err := st.SetMediaFavorite(ctx, "profile-default", media[0], true, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaKeep(ctx, "profile-default", media[10], true, filter); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{1, 9} {
		path := filepath.Join(sub.Root, fmt.Sprintf("alternate-%d.mp3", i))
		if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
			MediaID: media[i], Label: "Alternate",
			File: ImportMediaFile{
				LibraryID: sub.LibraryID, Path: path, Title: "Alternate", Class: MediaClassAudio,
				Container: "mp3", Codec: "mp3", ContentFingerprint: HashMediaFilePath(path),
			},
		}); err != nil {
			t.Fatal(err)
		}
		seen := sub.AddedAt.Add(time.Duration(i+1)*time.Second + time.Millisecond)
		if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, fmt.Sprintf("alternate-%d", i), path, seen); err != nil {
			t.Fatal(err)
		}
	}

	sweepSubscriptionRetention(t, st, 7)
	sweepSubscriptionRetention(t, st, 0)
	for _, id := range []MediaID{media[0], media[8], media[9], media[10]} {
		if _, err := st.Media(ctx, id, filter); err != nil {
			t.Fatalf("retained media %s is missing: %v", id, err)
		}
	}
	entries, err := st.TrashedMediaList(ctx, filter)
	if err != nil || len(entries) != 7 {
		t.Fatalf("trash has %d entries, want 7: %v", len(entries), err)
	}
	for _, id := range media[1:8] {
		if err := st.RestoreMedia(ctx, id, filter); err != nil {
			t.Fatalf("restore %s: %v", id, err)
		}
		if _, err := st.Media(ctx, id, filter); err != nil {
			t.Fatalf("restored media %s is missing: %v", id, err)
		}
	}
	files, err := st.MediaFilesForMedia(ctx, media[1], filter)
	if err != nil || len(files) != 2 {
		t.Fatalf("restored media has %d files, want both versions: %v", len(files), err)
	}
}

func TestSubscriptionRetentionDeleteAfterPlayedUsesAllFollowers(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	sub, media := subscriptionRetentionFixture(t, st, RetentionDeleteAfterPlayed, nil, 2)
	second, err := st.CreateProfile(ctx, "Second Follower", ProfileAdult, filter)
	if err != nil {
		t.Fatal(err)
	}
	nonFollower, err := st.CreateProfile(ctx, "Non-Follower", ProfileAdult, filter)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFavorite(ctx, nonFollower.ID, media[1], true, filter); err != nil {
		t.Fatal(err)
	}
	recordPlayback := func(profileID ProfileID, position int, finished bool) {
		t.Helper()
		for _, id := range media {
			if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
				ProfileID: profileID, MediaID: id, PositionSeconds: position, DurationSeconds: 120,
			}, finished, ResumePolicy{Class: MediaClassAudio}); err != nil {
				t.Fatal(err)
			}
		}
	}
	recordPlayback("profile-default", 120, true)
	sweepSubscriptionRetention(t, st, 0) // Playback alone is insufficient with zero followers.
	for _, id := range []ProfileID{"profile-default", second.ID} {
		if err := st.SetFollow(ctx, id, sub.ID, true, sub.AddedAt); err != nil {
			t.Fatal(err)
		}
	}
	recordPlayback(second.ID, 30, false)
	sweepSubscriptionRetention(t, st, 0)
	recordPlayback(second.ID, 120, true)
	sweepSubscriptionRetention(t, st, 1)
	entries, err := st.TrashedMediaList(ctx, filter)
	if err != nil || len(entries) != 1 || entries[0].ID != media[0] {
		t.Fatalf("trash = %+v, want only %s: %v", entries, media[0], err)
	}
	if _, err := st.Media(ctx, media[1], filter); err != nil {
		t.Fatalf("favorited media is missing: %v", err)
	}
	if err := st.RestoreMedia(ctx, media[0], filter); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionRetentionSkipsTrashedLibrary(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	keep := 1
	sub, media := subscriptionRetentionFixture(t, st, RetentionKeepLastN, &keep, 2)
	if err := st.DeleteLibrary(ctx, sub.LibraryID); err != nil {
		t.Fatal(err)
	}
	sweepSubscriptionRetention(t, st, 0)
	if err := st.RestoreLibrary(ctx, sub.LibraryID); err != nil {
		t.Fatal(err)
	}
	for _, id := range media {
		if _, err := st.Media(ctx, id, LibraryFilter{AllowedIDs: []LibraryID{sub.LibraryID}}); err != nil {
			t.Fatalf("media %s is missing after library restore: %v", id, err)
		}
	}
	sweepSubscriptionRetention(t, st, 1)
}

func TestSubscriptionRetentionWaitsForFollowersOfEveryLinkedSubscription(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	first, media := subscriptionRetentionFixture(t, st, RetentionDeleteAfterPlayed, nil, 1)
	second, err := st.AddSubscription(ctx, Subscription{
		LibraryID: first.LibraryID, FeedURL: "https://feeds.example.com/second.xml",
		Root: "/media/local/second", RetentionKind: RetentionDeleteAfterPlayed,
	}, first.AddedAt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(second.Root, "alternate.mp3")
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID: media[0], Label: "Alternate",
		File: ImportMediaFile{
			LibraryID: second.LibraryID, Path: path, Title: "Alternate", Class: MediaClassAudio,
			Container: "mp3", Codec: "mp3", ContentFingerprint: HashMediaFilePath(path),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, second.ID, "alternate", path, first.AddedAt); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProfile(ctx, "Second Subscription Follower", ProfileAdult, filter)
	if err != nil {
		t.Fatal(err)
	}
	for profile, subscriptionID := range map[ProfileID]SubscriptionID{
		"profile-default": first.ID, other.ID: second.ID,
	} {
		if err := st.SetFollow(ctx, profile, subscriptionID, true, first.AddedAt); err != nil {
			t.Fatal(err)
		}
	}
	finish := func(profile ProfileID) {
		t.Helper()
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
			ProfileID: profile, MediaID: media[0], PositionSeconds: 120, DurationSeconds: 120,
		}, true, ResumePolicy{Class: MediaClassAudio}); err != nil {
			t.Fatal(err)
		}
	}
	finish("profile-default")
	sweepSubscriptionRetention(t, st, 0)
	if _, err := st.Media(ctx, media[0], filter); err != nil {
		t.Fatal(err)
	}
	finish(other.ID)
	sweepSubscriptionRetention(t, st, 1)
	sweepSubscriptionRetention(t, st, 0)
	if err := st.RestoreMedia(ctx, media[0], filter); err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForMedia(ctx, media[0], filter)
	if err != nil || len(files) != 2 {
		t.Fatalf("restored files = %v, want both Versions: %v", files, err)
	}
}

func TestSubscriptionRetentionRecordsOneActorlessActivityPerMove(t *testing.T) {
	st := openSeeded(t)
	keep := 1
	sub, media := subscriptionRetentionFixture(t, st, RetentionKeepLastN, &keep, 3)
	sweepSubscriptionRetention(t, st, 2)
	sweepSubscriptionRetention(t, st, 0)
	entries, err := st.ActivityEntries(t.Context(), ActivityQuery{
		Filter: LibraryFilter{AllowedIDs: []LibraryID{sub.LibraryID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[MediaID]int{}
	for _, entry := range entries {
		if entry.Action != ActivityMediaTrash {
			continue
		}
		if entry.TargetType != "media" || entry.LibraryID != sub.LibraryID || entry.SessionID != "" ||
			entry.DeviceID != "" || entry.ProfileID != "" || entry.TokenID != "" || entry.APIKeyID != "" {
			t.Fatalf("unexpected retention activity: %+v", entry)
		}
		counts[MediaID(entry.TargetID)]++
	}
	if len(counts) != 2 || counts[media[0]] != 1 || counts[media[1]] != 1 {
		t.Fatalf("activity counts = %v, want one for each moved Media", counts)
	}
}

func TestSubscriptionRetentionRecoversActivityAfterRestart(t *testing.T) {
	ctx := t.Context()
	dir := migratedDataDir(t)
	opts := Options{SeedDevelopmentData: true}
	st, err := Open(ctx, dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	keep := 1
	sub, media := subscriptionRetentionFixture(t, st, RetentionKeepLastN, &keep, 3)
	// Stop both moves after their catalog commit. For one move, also stop
	// after the activity append but before clearing the pending journal row.
	for i, id := range media[:2] {
		tx, err := st.catalogW.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		activity, moved, err := st.commitSubscriptionRetention(ctx, tx, id, sub.AddedAt.Add(time.Hour))
		_ = tx.Rollback()
		if err != nil || !moved {
			t.Fatalf("catalog move = %v, %v", moved, err)
		}
		if i == 1 {
			if err := st.AppendActivity(ctx, activity); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertRetentionTrash(t, reopened, sub, media[:2]...)
	sweepSubscriptionRetention(t, reopened, 0)
	entries, err := reopened.ActivityEntries(ctx, ActivityQuery{
		Filter: LibraryFilter{AllowedIDs: []LibraryID{sub.LibraryID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[MediaID]int{}
	for _, activity := range entries {
		if activity.Action == ActivityMediaTrash {
			counts[MediaID(activity.TargetID)]++
		}
	}
	if len(counts) != 2 || counts[media[0]] != 1 || counts[media[1]] != 1 {
		t.Fatalf("recovered activity counts = %v, want one per move", counts)
	}
}

func TestSubscriptionRetentionProtectsPendingStars(t *testing.T) {
	for _, kind := range []RetentionKind{RetentionKeepLastN, RetentionDeleteAfterPlayed} {
		t.Run(string(kind), func(t *testing.T) {
			st := openSeeded(t)
			ctx := t.Context()
			const profile ProfileID = "profile-default"
			var limit *int
			if kind == RetentionKeepLastN {
				keep := 1
				limit = &keep
			}
			sub, media := subscriptionRetentionFixture(t, st, kind, limit, 3)
			// Protect the newest item. For keep-last-N it must also be
			// omitted from the count so the middle item survives. Protect
			// the oldest item too to exercise a normally doomed target.
			for _, index := range []int{0, 2} {
				if err := st.RecordItemStateImport(ctx, ItemStateImport{
					ProfileID: profile, SubscriptionID: sub.ID, Source: "fixture",
					ItemGUID: fmt.Sprintf("item-%d", index), Starred: true,
				}, sub.AddedAt); err != nil {
					t.Fatal(err)
				}
				favorite, err := st.MediaFavorited(ctx, profile, media[index])
				if err != nil || favorite {
					t.Fatalf("favorite before replay = %t, %v", favorite, err)
				}
			}
			wantTrashed := 0
			if kind == RetentionDeleteAfterPlayed {
				wantTrashed = 1
				if err := st.SetFollow(ctx, profile, sub.ID, true, sub.AddedAt); err != nil {
					t.Fatal(err)
				}
				for _, id := range media {
					if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
						ProfileID: profile, MediaID: id, PositionSeconds: 120, DurationSeconds: 120,
					}, true, ResumePolicy{Class: MediaClassAudio}); err != nil {
						t.Fatal(err)
					}
				}
			}
			sweepSubscriptionRetention(t, st, wantTrashed)
			if applied, err := st.ReplayItemStateImports(ctx, sub.AddedAt.Add(time.Minute)); err != nil || applied != 2 {
				t.Fatalf("replay = %d, %v, want two imports", applied, err)
			}
			for _, index := range []int{0, 2} {
				favorite, err := st.MediaFavorited(ctx, profile, media[index])
				if err != nil || !favorite {
					t.Fatalf("favorite after replay = %t, %v", favorite, err)
				}
			}
			sweepSubscriptionRetention(t, st, 0)
		})
	}
}

func TestSubscriptionRetentionStaleReplayPreservesPendingStar(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	keep := 1
	sub, media := subscriptionRetentionFixture(t, st, RetentionKeepLastN, &keep, 2)
	state := ItemStateImport{
		ProfileID: "profile-default", SubscriptionID: sub.ID, Source: "fixture", ItemGUID: "item-0",
	}
	if err := st.RecordItemStateImport(ctx, state, sub.AddedAt); err != nil {
		t.Fatal(err)
	}
	pending, err := st.pendingItemStateImports(ctx)
	if err != nil || len(pending) != 1 || pending[0].MediaID != media[0] {
		t.Fatalf("pending before re-import = %v, %v", pending, err)
	}
	state.Starred = true
	if err := st.RecordItemStateImport(ctx, state, sub.AddedAt); err != nil {
		t.Fatal(err)
	}
	if marked, err := st.markItemStateImportApplied(ctx, pending[0], sub.AddedAt.Add(time.Minute)); err != nil || marked {
		t.Fatalf("stale replay marked = %t, %v", marked, err)
	}
	sweepSubscriptionRetention(t, st, 0)

	pending, err = st.pendingItemStateImports(ctx)
	if err != nil || len(pending) != 1 || !pending[0].Starred {
		t.Fatalf("pending star = %v, %v", pending, err)
	}
	state.Starred = false
	if err := st.RecordItemStateImport(ctx, state, sub.AddedAt); err != nil {
		t.Fatal(err)
	}
	if complete, err := st.applyItemStateImport(ctx, pending[0]); err != nil || complete {
		t.Fatalf("stale star replay = %t, %v", complete, err)
	}
	if favorite, err := st.MediaFavorited(ctx, state.ProfileID, media[0]); err != nil || favorite {
		t.Fatalf("favorite from stale replay = %t, %v", favorite, err)
	}
	sweepSubscriptionRetention(t, st, 1)
}

func TestSubscriptionRetentionTakesCatalogWriterBeforeEventsWriter(t *testing.T) {
	st := openSeeded(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	keep := 1
	sub, media := subscriptionRetentionFixture(t, st, RetentionKeepLastN, &keep, 2)

	// Reserve the catalog writer so the sweep decides read-only and then waits
	// for its write transaction. Reads and events writes stay available.
	writer, err := st.catalogW.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	done := make(chan error, 1)
	waits := st.catalogW.Stats().WaitCount
	go func() {
		moved, skipped, err := st.ApplySubscriptionRetentionPolicies(ctx)
		if err == nil && (skipped || moved != 1) {
			err = fmt.Errorf("retention sweep = %d, %v, want 1, false", moved, skipped)
		}
		done <- err
	}()
	for st.catalogW.Stats().WaitCount == waits {
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal("sweep did not reach the catalog writer")
		}
	}
	if _, err := st.Profile(ctx, "profile-default"); err != nil {
		t.Fatalf("unrelated Profile read during retention: %v", err)
	}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: media[1], PositionSeconds: 30, DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassAudio}); err != nil {
		t.Fatalf("playback update during retention: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := st.Media(ctx, media[1], LibraryFilter{AllowedIDs: []LibraryID{sub.LibraryID}}); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionRetentionRechecksFollowersAfterTakingEventsWriter(t *testing.T) {
	st := openSeeded(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, media := subscriptionRetentionFixture(t, st, RetentionDeleteAfterPlayed, nil, 1)
	profile := ProfileID("profile-default")
	if err := st.SetFollow(ctx, profile, sub.ID, true, sub.AddedAt); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: profile, MediaID: media[0], DurationSeconds: 120,
	}, true, ResumePolicy{Class: MediaClassAudio}); err != nil {
		t.Fatal(err)
	}
	events, err := st.eventsW.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	done := make(chan error, 1)
	waits := st.eventsW.Stats().WaitCount
	go func() {
		moved, skipped, err := st.ApplySubscriptionRetentionPolicies(ctx)
		if err == nil && (skipped || moved != 0) {
			err = fmt.Errorf("retention sweep = %d, %v, want 0, false", moved, skipped)
		}
		done <- err
	}()
	for st.eventsW.Stats().WaitCount == waits {
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal("sweep did not reach the events writer")
		}
	}
	if _, err := events.ExecContext(ctx, `DELETE FROM follow WHERE profile_id = ? AND subscription_id = ?`, profile, sub.ID); err != nil {
		t.Fatal(err)
	}
	if err := events.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertRetentionTrash(t, st, sub)
}

func TestSubscriptionRetentionBreaksSamePollTiesByFeedOrder(t *testing.T) {
	st := openSeeded(t)
	keep := 2
	sub, media := subscriptionRetentionPollFixture(t, st, &keep, []*int{intPtr(3), intPtr(0), intPtr(2), intPtr(1)})
	sweepSubscriptionRetention(t, st, 2)
	assertRetentionTrash(t, st, sub, media[0], media[2])
}

func TestSubscriptionRetentionKeepsMixedLegacyTiesAtTheCutoff(t *testing.T) {
	st := openSeeded(t)
	keep := 1
	sub, media := subscriptionRetentionPollFixture(t, st, &keep, []*int{intPtr(0), intPtr(1), nil})
	sweepSubscriptionRetention(t, st, 0)
	assertRetentionTrash(t, st, sub)

	// A protected legacy entry is outside the cutoff calculation.
	filter := LibraryFilter{AllowedIDs: []LibraryID{sub.LibraryID}}
	if err := st.SetMediaFavorite(t.Context(), "profile-default", media[2], true, filter); err != nil {
		t.Fatal(err)
	}
	sweepSubscriptionRetention(t, st, 1)
	assertRetentionTrash(t, st, sub, media[1])
}

func TestSubscriptionRetentionKeepsLegacyTiesAtTheCutoff(t *testing.T) {
	st := openSeeded(t)
	keep := 2
	sub, media := subscriptionRetentionPollFixture(t, st, &keep, []*int{nil, nil, nil})
	// An older entry has every member of the tied group ahead of it.
	path := filepath.Join(sub.Root, "older.mp3")
	item, _, err := st.ImportMediaFile(t.Context(), ImportMediaFile{
		LibraryID: sub.LibraryID, Path: path, Title: "older", Class: MediaClassAudio,
		Container: "mp3", Codec: "mp3", ContentFingerprint: HashMediaFilePath(path),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(t.Context(), sub.ID, "older", path, sub.AddedAt); err != nil {
		t.Fatal(err)
	}
	sweepSubscriptionRetention(t, st, 1)
	assertRetentionTrash(t, st, sub, item.ID)
	for _, id := range media {
		if _, err := st.Media(t.Context(), id, LibraryFilter{AllowedIDs: []LibraryID{sub.LibraryID}}); err != nil {
			t.Fatalf("tied media %s is missing: %v", id, err)
		}
	}
}

func TestSubscriptionRetentionKeepAllSubscriptionProtectsSharedMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	keep := 1
	first, media := subscriptionRetentionFixture(t, st, RetentionKeepLastN, &keep, 3)
	second, err := st.AddSubscription(ctx, Subscription{
		LibraryID: first.LibraryID, FeedURL: "https://feeds.example.com/keep-all.xml",
		Root: "/media/local/keep-all", RetentionKind: RetentionKeepAll,
	}, first.AddedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, second.ID, "shared", filepath.Join(first.Root, "item-0.mp3"), first.AddedAt); err != nil {
		t.Fatal(err)
	}
	sweepSubscriptionRetention(t, st, 1)
	assertRetentionTrash(t, st, first, media[1])
}

// subscriptionRetentionPollFixture records one poll: every receipt shares a
// time, and positions[i] is entry i's feed position (nil for a legacy row).
func subscriptionRetentionPollFixture(t *testing.T, st *Store, keep *int, positions []*int) (Subscription, []MediaID) {
	t.Helper()
	ctx := t.Context()
	now := time.Date(2026, 6, 18, 14, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/poll.xml", Root: "/media/local/poll",
		RetentionKind: RetentionKeepLastN, RetentionN: keep,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	media := make([]MediaID, len(positions))
	for i, position := range positions {
		name := fmt.Sprintf("poll-%d", i)
		path := filepath.Join(sub.Root, name+".mp3")
		item, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: sub.LibraryID, Path: path, Title: name, Class: MediaClassAudio,
			Container: "mp3", Codec: "mp3", ContentFingerprint: HashMediaFilePath(path),
		})
		if err != nil {
			t.Fatal(err)
		}
		seen := now.Add(time.Minute)
		if position == nil {
			err = st.MarkSubscriptionEntrySeen(ctx, sub.ID, name, path, seen)
		} else {
			err = st.MarkSubscriptionFeedEntrySeen(ctx, sub.ID, name, path, seen, *position)
		}
		if err != nil {
			t.Fatal(err)
		}
		media[i] = item.ID
	}
	return sub, media
}

func assertRetentionTrash(t *testing.T, st *Store, sub Subscription, want ...MediaID) {
	t.Helper()
	entries, err := st.TrashedMediaList(t.Context(), LibraryFilter{AllowedIDs: []LibraryID{sub.LibraryID}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[MediaID]bool{}
	for _, entry := range entries {
		got[entry.ID] = true
	}
	if len(got) != len(want) {
		t.Fatalf("trash = %v, want %v", got, want)
	}
	for _, id := range want {
		if !got[id] {
			t.Fatalf("trash = %v, want %v", got, want)
		}
	}
}

func subscriptionRetentionFixture(t *testing.T, st *Store, kind RetentionKind, keep *int, count int) (Subscription, []MediaID) {
	t.Helper()
	ctx := t.Context()
	now := time.Date(2026, 6, 18, 14, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local", FeedURL: "https://feeds.example.com/retention.xml", Root: "/media/local/retention",
		RetentionKind: kind, RetentionN: keep,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	media := make([]MediaID, count)
	for i := range media {
		name := fmt.Sprintf("item-%d", i)
		path := filepath.Join(sub.Root, name+".mp3")
		item, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: sub.LibraryID, Path: path, Title: name, Class: MediaClassAudio,
			Container: "mp3", Codec: "mp3", ContentFingerprint: HashMediaFilePath(path),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, name, path, now.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatal(err)
		}
		media[i] = item.ID
	}
	return sub, media
}

func sweepSubscriptionRetention(t *testing.T, st *Store, want int) {
	t.Helper()
	trashed, skipped, err := st.ApplySubscriptionRetentionPolicies(t.Context())
	if err != nil || skipped || trashed != want {
		t.Fatalf("retention sweep = %d, %v, %v, want %d, false, nil", trashed, skipped, err, want)
	}
}

func TestEncryptedSubscriptionRetentionAppliesAndHidesPrivateMediaFromActivity(t *testing.T) {
	ctx := t.Context()
	st, encMedia := seededEncryptedMedia(t, "encrypted-retention")
	defer func() { _ = st.Close() }()

	keep := 1
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, Subscription{
		LibraryID: encMedia.LibraryID, FeedURL: "https://feeds.example.com/enc-ret.xml", Root: "/enc/ret",
		RetentionKind: RetentionKeepLastN, RetentionN: &keep,
	}, now)
	if err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	ids := make([]MediaID, 2)
	for i := range ids {
		path := filepath.Join(sub.Root, fmt.Sprintf("ep-%d.mp3", i))
		item, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: sub.LibraryID, Path: path, Title: fmt.Sprintf("Ep %d", i), Class: MediaClassAudio,
			Container: "mp3", Codec: "mp3", ContentFingerprint: HashMediaFilePath(path),
		})
		if err != nil {
			t.Fatalf("ImportMediaFile #%d: %v", i, err)
		}
		if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, fmt.Sprintf("ep-%d", i), path, now.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatalf("MarkSubscriptionEntrySeen #%d: %v", i, err)
		}
		ids[i] = item.ID
	}

	sweepSubscriptionRetention(t, st, 1)
	filter := LibraryFilter{AllowedIDs: []LibraryID{encMedia.LibraryID}, Revealed: true}
	if _, err := st.Media(ctx, ids[0], filter); err == nil {
		t.Fatalf("expected older encrypted episode %s to be trashed by retention", ids[0])
	}
	if _, err := st.Media(ctx, ids[1], filter); err != nil {
		t.Fatalf("newer encrypted episode %s should remain live: %v", ids[1], err)
	}
	plane, err := st.catalogPlaneForLibraryLocked(ctx, encMedia.LibraryID)
	if err != nil {
		t.Fatalf("catalogPlaneForLibraryLocked: %v", err)
	}
	var trashedAt sql.NullInt64
	if err := plane.reader.QueryRowContext(ctx, `SELECT trashed_at_ms FROM media WHERE id = ?`, ids[0]).Scan(&trashedAt); err != nil || !trashedAt.Valid {
		t.Fatalf("encrypted trashed_at_ms = %+v, %v; want valid timestamp", trashedAt, err)
	}

	var leaked int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity_entry WHERE target_id = ?`, ids[0]).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("shared activity leaked encrypted trashed media ID: count=%d, err=%v", leaked, err)
	}
}
