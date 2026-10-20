package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestScheduleOccurrenceJournalPublishesEachMemberExactlyOnce(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	due := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)
	started := due.Add(3 * time.Minute)
	jobs := []Job{
		{Kind: "scan", Payload: []byte(`{"root":"/media/a"}`), CreatedAt: started},
		{Kind: "scan", Payload: []byte(`{"root":"/media/b"}`), CreatedAt: started},
	}
	occurrence, err := st.BeginScheduleOccurrence(ctx, "scan:library-local", due, started, jobs)
	if err != nil {
		t.Fatalf("BeginScheduleOccurrence returned error: %v", err)
	}
	if len(occurrence.Members) != 2 || occurrence.Members[0].JobID != "" {
		t.Fatalf("journaled occurrence = %#v", occurrence)
	}

	// Idempotent begin: a crash-retry with a DIFFERENT build result must get
	// the frozen journal back, never a second member set.
	again, err := st.BeginScheduleOccurrence(ctx, "scan:library-local", due, started.Add(time.Minute), jobs[:1])
	if err != nil {
		t.Fatalf("repeat BeginScheduleOccurrence returned error: %v", err)
	}
	if len(again.Members) != 2 {
		t.Fatalf("repeat begin members = %d, want frozen 2", len(again.Members))
	}

	first, err := st.EnqueueScheduleOccurrenceMember(ctx, "scan:library-local", due, 0, started)
	if err != nil {
		t.Fatalf("EnqueueScheduleOccurrenceMember returned error: %v", err)
	}
	if first.ID == "" || first.Kind != "scan" {
		t.Fatalf("published member job = %#v", first)
	}
	// Replay of an already-published member returns the recorded job and
	// inserts nothing.
	replayed, err := st.EnqueueScheduleOccurrenceMember(ctx, "scan:library-local", due, 0, started.Add(time.Minute))
	if err != nil {
		t.Fatalf("replayed EnqueueScheduleOccurrenceMember returned error: %v", err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("replayed job ID = %s, want %s", replayed.ID, first.ID)
	}
	queued, err := st.ListJobs(ctx, JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued jobs after replay = %d, want 1", len(queued))
	}

	// Completion refuses while a member is unpublished.
	if err := st.CompleteScheduleOccurrence(ctx, "scan:library-local", due, due); err == nil {
		t.Fatal("CompleteScheduleOccurrence succeeded with an unpublished member")
	}

	if _, err := st.EnqueueScheduleOccurrenceMember(ctx, "scan:library-local", due, 1, started); err != nil {
		t.Fatalf("second member enqueue returned error: %v", err)
	}
	if err := st.CompleteScheduleOccurrence(ctx, "scan:library-local", due, due); err != nil {
		t.Fatalf("CompleteScheduleOccurrence returned error: %v", err)
	}
	latest, found, err := st.LatestScheduleOccurrence(ctx, "scan:library-local")
	if err != nil || !found {
		t.Fatalf("LatestScheduleOccurrence = found %v, err %v", found, err)
	}
	if !latest.Completed() || !latest.CompletedAt.Equal(due) {
		t.Fatalf("completed occurrence = %#v", latest)
	}
	// Idempotent completion keeps the first journaled instant.
	if err := st.CompleteScheduleOccurrence(ctx, "scan:library-local", due, due.Add(time.Hour)); err != nil {
		t.Fatalf("repeat CompleteScheduleOccurrence returned error: %v", err)
	}
	latest, _, err = st.LatestScheduleOccurrence(ctx, "scan:library-local")
	if err != nil {
		t.Fatal(err)
	}
	if !latest.CompletedAt.Equal(due) {
		t.Fatalf("completion instant drifted to %s, want %s", latest.CompletedAt, due)
	}
}

func TestScheduleOccurrenceJournalSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)
	jobs := []Job{
		{Kind: "scan", Payload: []byte(`{"root":"/media/a"}`), CreatedAt: due},
		{Kind: "scan", Payload: []byte(`{"root":"/media/b"}`), CreatedAt: due},
	}
	if _, err := st.BeginScheduleOccurrence(ctx, "scan:library-local", due, due, jobs); err != nil {
		t.Fatal(err)
	}
	published, err := st.EnqueueScheduleOccurrenceMember(ctx, "scan:library-local", due, 0, due)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	latest, found, err := reopened.LatestScheduleOccurrence(ctx, "scan:library-local")
	if err != nil || !found {
		t.Fatalf("LatestScheduleOccurrence after reopen = found %v, err %v", found, err)
	}
	if latest.Completed() || len(latest.Members) != 2 {
		t.Fatalf("reopened occurrence = %#v", latest)
	}
	if latest.Members[0].JobID != published.ID || latest.Members[1].JobID != "" {
		t.Fatalf("reopened members = %#v, want member 0 published and member 1 pending", latest.Members)
	}
	if _, err := reopened.EnqueueScheduleOccurrenceMember(ctx, "scan:library-local", due, 1, due.Add(time.Minute)); err != nil {
		t.Fatalf("post-reopen member enqueue returned error: %v", err)
	}
	if err := reopened.CompleteScheduleOccurrence(ctx, "scan:library-local", due, due); err != nil {
		t.Fatalf("post-reopen completion returned error: %v", err)
	}
	queued, err := reopened.ListJobs(ctx, JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 2 {
		t.Fatalf("queued jobs after recovery = %d, want exactly 2", len(queued))
	}
}

func TestScheduleOccurrenceMemberAdoptsActiveDedupeJob(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	active, err := st.EnqueueJob(ctx, Job{Kind: "ingest-poll", DedupeKey: "ingest-poll"})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)
	if _, err := st.BeginScheduleOccurrence(ctx, "ingest-poll", due, due, []Job{
		{Kind: "ingest-poll", DedupeKey: "ingest-poll", CreatedAt: due},
	}); err != nil {
		t.Fatal(err)
	}
	adopted, err := st.EnqueueScheduleOccurrenceMember(ctx, "ingest-poll", due, 0, due)
	if err != nil {
		t.Fatalf("EnqueueScheduleOccurrenceMember returned error: %v", err)
	}
	if adopted.ID != active.ID {
		t.Fatalf("adopted job ID = %s, want active %s", adopted.ID, active.ID)
	}
	queued, err := st.ListJobs(ctx, JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued jobs = %d, want dedupe-suppressed 1", len(queued))
	}
	if err := st.CompleteScheduleOccurrence(ctx, "ingest-poll", due, due); err != nil {
		t.Fatalf("CompleteScheduleOccurrence returned error: %v", err)
	}
}

func TestCompleteScheduleOccurrencePrunesOlderCompletedOccurrences(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	first := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	for _, due := range []time.Time{first, second} {
		if _, err := st.BeginScheduleOccurrence(ctx, "hourly", due, due, nil); err != nil {
			t.Fatal(err)
		}
		if err := st.CompleteScheduleOccurrence(ctx, "hourly", due, due); err != nil {
			t.Fatal(err)
		}
	}
	var remaining int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT count(*) FROM schedule_occurrence WHERE schedule_id = ?`, "hourly").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("journaled occurrences after prune = %d, want only the newest", remaining)
	}
	latest, found, err := st.LatestScheduleOccurrence(ctx, "hourly")
	if err != nil || !found {
		t.Fatalf("LatestScheduleOccurrence = found %v, err %v", found, err)
	}
	if !latest.Due.Equal(second) {
		t.Fatalf("surviving occurrence due = %s, want %s", latest.Due, second)
	}
}

func TestMarkScheduleCompletedIsMonotonic(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	anchor := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	if err := st.CreateSchedule(ctx, Schedule{
		ID:                   "hourly",
		JobKind:              "hourly",
		Trigger:              ScheduleTrigger{Kind: ScheduleTriggerEvery, N: 1, Unit: ScheduleEveryHours},
		Enabled:              true,
		LastCompletedEnqueue: anchor,
	}); err != nil {
		t.Fatal(err)
	}
	forward := anchor.Add(2 * time.Hour)
	if err := st.MarkScheduleCompleted(ctx, "hourly", forward); err != nil {
		t.Fatal(err)
	}
	// A stale replay (or a backward clock jump) must never rewind the cursor.
	if err := st.MarkScheduleCompleted(ctx, "hourly", anchor.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	row, err := st.Schedule(ctx, "hourly")
	if err != nil {
		t.Fatal(err)
	}
	if !row.LastCompletedEnqueue.Equal(forward) {
		t.Fatalf("cursor = %s, want monotonic %s", row.LastCompletedEnqueue, forward)
	}
	var missing ValidationError
	err = st.MarkScheduleCompleted(ctx, "absent", forward)
	if err == nil || !errors.As(err, &missing) {
		t.Fatalf("missing schedule error = %v, want validation error", err)
	}
}
