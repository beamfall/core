package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestCompactEvents covers PANEL-9: the retention compaction prunes the
// genuinely-disposable bookkeeping (expired Stream-Token ledger rows + auth
// attempts older than retention) and leaves fresh/recent rows — and the
// append-only audit log — alone.
func TestCompactEvents(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	retention := 180 * 24 * time.Hour

	// Stream tokens: one already expired (dead weight), one still valid.
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "direct", "media-expired", false, now.Add(-time.Hour)); err != nil {
		t.Fatalf("record expired token: %v", err)
	}
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "hls", "media-fresh", false, now.Add(time.Hour)); err != nil {
		t.Fatalf("record fresh token: %v", err)
	}

	// Auth attempts: one beyond retention, one recent.
	if err := st.RecordAuthAttempt(ctx, AuthAttempt{OccurredAt: now.Add(-retention - 24*time.Hour), Action: "reveal", Key: "k", Success: false}); err != nil {
		t.Fatalf("record old auth: %v", err)
	}
	if err := st.RecordAuthAttempt(ctx, AuthAttempt{OccurredAt: now.Add(-time.Minute), Action: "reveal", Key: "k", Success: false}); err != nil {
		t.Fatalf("record recent auth: %v", err)
	}

	// An old audit-log entry: must SURVIVE (append-only, ADR-0012 §11).
	if err := st.AppendActivity(ctx, ActivityEntry{OccurredAt: now.Add(-2 * retention), Action: "reveal"}); err != nil {
		t.Fatalf("append activity: %v", err)
	}
	if err := st.AppendMediaEvent(ctx, MediaEventUpdated, "media-alpha", nil, now.Add(-2*retention)); err != nil {
		t.Fatalf("append old outbox 1: %v", err)
	}
	if err := st.AppendMediaEvent(ctx, MediaEventUpdated, "media-alpha", nil, now.Add(-retention-24*time.Hour)); err != nil {
		t.Fatalf("append old outbox 2: %v", err)
	}
	if err := st.AppendMediaEvent(ctx, MediaEventUpdated, "media-alpha", nil, now); err != nil {
		t.Fatalf("append fresh outbox: %v", err)
	}

	res, err := st.CompactEvents(ctx, retention, now)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if res.ExpiredStreamTokens != 1 {
		t.Fatalf("expected 1 expired stream token pruned, got %d", res.ExpiredStreamTokens)
	}
	if res.OldAuthAttempts != 1 {
		t.Fatalf("expected 1 old auth attempt pruned, got %d", res.OldAuthAttempts)
	}
	if res.OldOutboxEvents != 2 {
		t.Fatalf("expected 2 old outbox events pruned, got %d", res.OldOutboxEvents)
	}
	if _, err := st.ReadEventOutbox(ctx, 1, 100); !errors.Is(err, ErrEventCursorTooOld) {
		t.Fatalf("old cursor error = %v, want ErrEventCursorTooOld", err)
	}
	events, err := st.ReadEventOutbox(ctx, 2, 100)
	if err != nil {
		t.Fatalf("read retained outbox: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 3 {
		t.Fatalf("retained outbox = %+v, want seq 3 only", events)
	}

	// The still-valid token survives.
	toks, err := st.MyStreamTokens(ctx, "profile-default", now)
	if err != nil {
		t.Fatalf("my tokens: %v", err)
	}
	if len(toks) != 1 {
		t.Fatalf("expected exactly the fresh token to survive, got %d: %+v", len(toks), toks)
	}

	// The audit log is untouched (CompactEvents must never prune activity_entry).
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: LibraryFilter{AllLibraries: true, Revealed: true}, Limit: 50})
	if err != nil {
		t.Fatalf("activity read: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "reveal" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("compaction pruned an append-only audit-log entry")
	}
}
