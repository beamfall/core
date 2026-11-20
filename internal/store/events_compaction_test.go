package store

import (
	"context"
	"errors"
	"slices"
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

	// Auth attempts: one beyond retention in each database, one recent.
	if err := st.RecordAuthAttempt(ctx, AuthAttempt{OccurredAt: now.Add(-retention - 24*time.Hour), Action: "reveal", Key: "k", Success: false}); err != nil {
		t.Fatalf("record old auth: %v", err)
	}
	if err := st.RecordAuthAttempt(ctx, AuthAttempt{OccurredAt: now.Add(-time.Minute), Action: "reveal", Key: "k", Success: false}); err != nil {
		t.Fatalf("record recent auth: %v", err)
	}
	if err := st.RecordAuthAttempt(ctx, AuthAttempt{OccurredAt: now.Add(-retention - time.Hour), Action: IntegrationAttemptAction, Key: "profile:fixture"}); err != nil {
		t.Fatalf("record old integration auth: %v", err)
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
	if res.OldAuthAttempts != 2 {
		t.Fatalf("expected 2 old auth attempts pruned, got %d", res.OldAuthAttempts)
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

// Finished jobs leave the queue after their retention; failed ones stay longer,
// and queued or running work is never touched (CORE-2285).
func TestCompactEventsPrunesFinishedJobs(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()

	finish := func(id JobID, at time.Time, fail bool) {
		t.Helper()
		if _, err := st.EnqueueJob(ctx, Job{ID: id, Kind: JobKind("kind-" + string(id)), CreatedAt: at}); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		claimed, err := st.ClaimNextJob(ctx, "worker", nil, at)
		if err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
		if claimed.ID != id {
			t.Fatalf("claimed %q, want %q", claimed.ID, id)
		}
		if fail {
			err = st.FailJob(ctx, id, claimed.LeaseOwner, "boom", at)
		} else {
			err = st.CompleteJob(ctx, id, claimed.LeaseOwner, at)
		}
		if err != nil {
			t.Fatalf("finish %s: %v", id, err)
		}
	}
	finish("job-done-old", now.Add(-FinishedJobRetention-time.Hour), false)
	finish("job-done-recent", now.Add(-time.Hour), false)
	finish("job-failed-recent", now.Add(-FinishedJobRetention-time.Hour), true)
	finish("job-failed-old", now.Add(-FailedJobRetention-time.Hour), true)
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-canceled-old", Kind: "kind-canceled", CreatedAt: now.Add(-FailedJobRetention)}); err != nil {
		t.Fatal(err)
	}
	if err := st.CancelJob(ctx, "job-canceled-old", now.Add(-FinishedJobRetention-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-queued-old", Kind: "kind-queued", CreatedAt: now.Add(-2 * FailedJobRetention)}); err != nil {
		t.Fatal(err)
	}

	res, err := st.CompactEvents(ctx, 0, now)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if res.OldFinishedJobs != 3 {
		t.Fatalf("pruned %d finished jobs, want 3", res.OldFinishedJobs)
	}
	jobs, err := st.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, job := range jobs {
		kept = append(kept, string(job.ID))
	}
	want := []string{"job-queued-old", "job-failed-recent", "job-done-recent"}
	if !slices.Equal(kept, want) {
		t.Fatalf("kept jobs = %v, want %v", kept, want)
	}
	if events, err := st.ListJobEvents(ctx, "job-done-old"); err == nil && len(events) != 0 {
		t.Fatalf("pruned job still has %d events", len(events))
	}
}
