package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func requireValidationError(t *testing.T, what string, err error) {
	t.Helper()
	var verr ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("%s error = %v, want ValidationError", what, err)
	}
}

// TestJobAndOutboxRejectMalformedJSON proves every caller-supplied JSON
// document is checked before it is persisted: malformed bytes are a
// ValidationError and leave the stored job, its events and the outbox as they
// were, while an empty document is still stored as "{}".
func TestJobAndOutboxRejectMalformedJSON(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(100, 0).UTC()
	malformed := []byte(`{"cursor":`)

	_, err = st.EnqueueJob(ctx, Job{ID: "job-bad-payload", Kind: "scan", Payload: malformed, CreatedAt: now})
	requireValidationError(t, "enqueue payload", err)
	_, err = st.EnqueueJob(ctx, Job{ID: "job-bad-checkpoint", Kind: "scan", Checkpoint: []byte("not json"), CreatedAt: now})
	requireValidationError(t, "enqueue checkpoint", err)
	if jobs, err := st.ListJobs(ctx); err != nil || len(jobs) != 0 {
		t.Fatalf("jobs after rejected enqueues = %#v (err %v), want none", jobs, err)
	}

	if _, err := st.EnqueueJob(ctx, Job{ID: "job-ok", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJobCheckpoint(ctx, "job-ok", claimed.LeaseOwner, []byte(`{"cursor":"a"}`), JobProgress{}, now); err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := st.ListJobEvents(ctx, "job-ok")
	if err != nil {
		t.Fatal(err)
	}

	requireValidationError(t, "checkpoint",
		st.UpdateJobCheckpoint(ctx, "job-ok", claimed.LeaseOwner, malformed, JobProgress{}, now.Add(time.Second)))
	requireValidationError(t, "job event",
		st.AppendJobEvent(ctx, "job-ok", "note", malformed, now.Add(time.Second)))
	requireValidationError(t, "yield checkpoint",
		st.YieldJob(ctx, "job-ok", claimed.LeaseOwner, malformed, now.Add(time.Minute), now.Add(time.Second)))

	job, err := st.Job(ctx, "job-ok")
	if err != nil {
		t.Fatal(err)
	}
	if string(job.Checkpoint) != `{"cursor":"a"}` || job.Status != JobStatusRunning {
		t.Fatalf("job after rejected writes = status %q checkpoint %s, want running {\"cursor\":\"a\"}", job.Status, job.Checkpoint)
	}
	eventsAfter, err := st.ListJobEvents(ctx, "job-ok")
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("job events = %d after rejected writes, want %d", len(eventsAfter), len(eventsBefore))
	}

	if err := st.AppendJobEvent(ctx, "job-ok", "note", nil, now.Add(2*time.Second)); err != nil {
		t.Fatalf("empty job event payload: %v", err)
	}

	outboxBefore, err := st.ReadEventOutbox(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	requireValidationError(t, "media event",
		st.AppendMediaEvent(ctx, MediaEventUpdated, "media-alpha", malformed, now))
	st.mu.RLock()
	batchErr := st.appendMediaEventsLocked(ctx, MediaEventUpdated, []MediaID{"media-alpha", "media-beta"}, malformed, now)
	st.mu.RUnlock()
	requireValidationError(t, "batched media events", batchErr)
	outboxAfter, err := st.ReadEventOutbox(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(outboxAfter) != len(outboxBefore) {
		t.Fatalf("outbox rows = %d after rejected appends, want %d", len(outboxAfter), len(outboxBefore))
	}
	if err := st.AppendMediaEvent(ctx, MediaEventUpdated, "media-alpha", []byte(`{"ok":true}`), now); err != nil {
		t.Fatalf("valid media event: %v", err)
	}
}
