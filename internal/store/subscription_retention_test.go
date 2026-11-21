package store

import (
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
