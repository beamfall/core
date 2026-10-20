package store

import (
	"context"
	"testing"
)

// TestChannelContextExcludedFromResumeProjection pins CHAN-3 (ADR-0158
// Decision 4): a channel-context partial play is flagged and EXCLUDED from the
// continue-watching/resume projection, while direct playback is unaffected and
// the real signals (watched-marking + history) still apply.
func TestChannelContextExcludedFromResumeProjection(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	video := ResumePolicy{Class: MediaClassVideo}

	media := importChannelMedia(t, st, 2)
	chanMedia, directMedia := media[0], media[1]

	// A channel-context partial play: past the resume floor, not finished.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         chanMedia,
		PositionSeconds: 45,
		DurationSeconds: 120,
		ChannelContext:  true,
	}, false, video); err != nil {
		t.Fatal(err)
	}
	// A direct partial play on a different item, same numbers.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         directMedia,
		PositionSeconds: 45,
		DurationSeconds: 120,
	}, false, video); err != nil {
		t.Fatal(err)
	}

	// The channel play DID write a resumable row (position preserved) — it is the
	// projection that excludes it, not a dropped write.
	stored, ok, err := st.PlaybackProgressFor(ctx, "profile-default", chanMedia)
	if err != nil || !ok {
		t.Fatalf("channel progress read: ok=%v err=%v", ok, err)
	}
	if stored.PositionSeconds != 45 {
		t.Fatalf("channel position = %d, want 45 (written but excluded)", stored.PositionSeconds)
	}

	// Continue-watching shows ONLY the direct play; the channel play is excluded.
	items := mustSourceItems(t, st, filter, SourceQuery{Kind: "continue-watching", ProfileID: "profile-default"})
	ids := sourceItemIDs(items)
	if len(ids) != 1 || ids[0] != directMedia {
		t.Fatalf("continue-watching = %v, want [%s] (channel play excluded)", ids, directMedia)
	}

	// The resume-progress "in-progress" band shares the projection: same exclusion.
	resume := mustSourceItems(t, st, filter, SourceQuery{Kind: "resume-progress", Value: "in-progress", ProfileID: "profile-default"})
	if resumeIDs := sourceItemIDs(resume); len(resumeIDs) != 1 || resumeIDs[0] != directMedia {
		t.Fatalf("resume-progress = %v, want [%s]", resumeIDs, directMedia)
	}

	// A channel play PAST the completion threshold still marks watched and counts
	// a play (history) — the real signals survive the flag.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         chanMedia,
		PositionSeconds: 120,
		DurationSeconds: 120,
		ChannelContext:  true,
	}, false, video); err != nil {
		t.Fatal(err)
	}
	watched, ok, err := st.PlaybackProgressFor(ctx, "profile-default", chanMedia)
	if err != nil || !ok {
		t.Fatalf("watched read: ok=%v err=%v", ok, err)
	}
	if !watched.Watched {
		t.Fatal("channel play past threshold did not mark watched")
	}
	if watched.PlayCount < 1 {
		t.Fatalf("channel watched play count = %d, want >= 1 (history)", watched.PlayCount)
	}
	// Still excluded from continue-watching (watched + channel-flagged).
	after := mustSourceItems(t, st, filter, SourceQuery{Kind: "continue-watching", ProfileID: "profile-default"})
	if afterIDs := sourceItemIDs(after); len(afterIDs) != 1 || afterIDs[0] != directMedia {
		t.Fatalf("after watched: continue-watching = %v, want [%s]", afterIDs, directMedia)
	}
}
