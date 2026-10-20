package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store/storetest"
)

// TestReadyOnMigratedStore: a freshly opened, migrated, seeded store is ready
// (both databases reachable + migrations applied). After Close it is not.
func TestReadyOnMigratedStore(t *testing.T) {
	st := storetest.Open(t)
	if err := st.Ready(context.Background()); err != nil {
		t.Fatalf("Ready on migrated store = %v, want nil", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := st.Ready(context.Background()); err == nil {
		t.Fatal("Ready after Close = nil, want error")
	}
}

// TestLibraryMediaTotalCountsNonTrashed: the development seed has exactly two
// Media (media-alpha, media-archive), both non-trashed.
func TestLibraryMediaTotalCountsNonTrashed(t *testing.T) {
	st := storetest.Open(t)
	total, err := st.LibraryMediaTotal(context.Background())
	if err != nil {
		t.Fatalf("LibraryMediaTotal: %v", err)
	}
	if total != 2 {
		t.Fatalf("LibraryMediaTotal = %d, want 2 (seed media-alpha + media-archive)", total)
	}
}

// TestActiveSessionCountCountsDistinctLiveSessions: with no live tokens the count
// is zero; recording two media tokens for one session and one for another yields
// two distinct active sessions; an expired token does not count.
func TestActiveSessionCountCountsDistinctLiveSessions(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Unix(1_000_000, 0).UTC()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)

	if count, err := st.ActiveSessionCount(ctx, now); err != nil || count != 0 {
		t.Fatalf("ActiveSessionCount (no tokens) = %d, %v, want 0, nil", count, err)
	}

	// session-default holds two live media tokens (direct + hls) → one session.
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false, future); err != nil {
		t.Fatal(err)
	}
	// session-manager holds one live media token → a second distinct session.
	if err := st.RecordStreamToken(ctx, "session-manager", "profile-default", "device-mac", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatal(err)
	}
	// An expired token must not be counted.
	if err := st.RecordStreamToken(ctx, "session-expired", "profile-default", "device-tv", "media-direct", "media-alpha", false, past); err != nil {
		t.Fatal(err)
	}

	count, err := st.ActiveSessionCount(ctx, now)
	if err != nil {
		t.Fatalf("ActiveSessionCount: %v", err)
	}
	if count != 2 {
		t.Fatalf("ActiveSessionCount = %d, want 2 distinct live sessions", count)
	}
}
