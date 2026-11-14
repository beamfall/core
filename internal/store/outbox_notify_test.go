package store

import (
	"context"
	"testing"
	"time"
)

// TestEventOutboxChangedSignalsEveryWritePath proves each way of writing an
// outbox row wakes a waiting reader (CORE-1158): the autocommit rows, the
// batched Media rows and the transactions that carry a job or audit row.
func TestEventOutboxChangedSignalsEveryWritePath(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(100, 0).UTC()
	st := openSeeded(t)
	job, err := st.EnqueueJob(ctx, Job{Kind: "scan", CreatedAt: now})
	if err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	control := SessionControlPayload{Command: "pause", ProfileID: "profile-default"}

	writes := []struct {
		name  string
		write func() error
	}{
		{"media event", func() error { return st.AppendMediaEvent(ctx, MediaEventUpdated, "media-alpha", nil, now) }},
		{"media event batch", func() error {
			st.mu.RLock()
			defer st.mu.RUnlock()
			return st.appendMediaEventsLocked(ctx, MediaEventUpdated, []MediaID{"media-alpha", "media-beta"}, nil, now)
		}},
		{"session control", func() error { return st.AppendSessionControlEvent(ctx, "session-tv", control, now) }},
		{"session control with activity", func() error {
			return st.AppendSessionControlEventWithActivity(ctx, "session-tv", control, ActivityEntry{Action: "session.control"}, now)
		}},
		{"extension event", func() error {
			return st.AppendExtensionEvent(ctx, ExtensionEventPayload{RequestID: "request-1", ProfileID: "profile-default"}, now)
		}},
		{"invalidation", func() error {
			return st.AppendInvalidationEvent(ctx, InvalidationPayload{ProfileID: "profile-default", Reason: InvalidationReasonAccessChanged}, now)
		}},
		{"group sync", func() error {
			return st.AppendGroupSyncEvent(ctx, GroupSyncPayload{GroupID: "group-1", ProfileID: "profile-default", Signal: GroupSignalTransition}, now)
		}},
		{"job enqueue", func() error { _, err := st.EnqueueJob(ctx, Job{Kind: "scan", CreatedAt: now}); return err }},
		{"job event", func() error { return st.AppendJobEvent(ctx, job.ID, "progress", nil, now) }},
		{"job cancel", func() error { return st.CancelJob(ctx, job.ID, now) }},
	}
	for _, tc := range writes {
		t.Run(tc.name, func(t *testing.T) {
			before, err := st.ReadEventOutbox(ctx, 0, 1000)
			if err != nil {
				t.Fatalf("read outbox: %v", err)
			}
			changed := st.EventOutboxChanged()
			select {
			case <-changed:
				t.Fatal("signalled before any write")
			default:
			}
			if err := tc.write(); err != nil {
				t.Fatalf("write: %v", err)
			}
			select {
			case <-changed:
			default:
				t.Fatal("committed outbox write did not signal")
			}
			after, err := st.ReadEventOutbox(ctx, 0, 1000)
			if err != nil {
				t.Fatalf("read outbox: %v", err)
			}
			if len(after) <= len(before) {
				t.Fatalf("write added no outbox row (%d before, %d after)", len(before), len(after))
			}
		})
	}
}

// TestEventOutboxChangedStaysQuietOnFailedWrite proves a write that commits
// nothing does not wake readers.
func TestEventOutboxChangedStaysQuietOnFailedWrite(t *testing.T) {
	st := openSeeded(t)
	changed := st.EventOutboxChanged()
	if err := st.AppendJobEvent(context.Background(), "job-missing", "progress", []byte("{not json"), time.Unix(100, 0).UTC()); err == nil {
		t.Fatal("invalid job event payload was accepted")
	}
	select {
	case <-changed:
		t.Fatal("failed write signalled readers")
	default:
	}
}

// TestEventOutboxNotifyWithoutWaiterAllocatesNothing pins the idle cost: with
// no open stream a write does not build a channel.
func TestEventOutboxNotifyWithoutWaiterAllocatesNothing(t *testing.T) {
	var n outboxNotifier
	if allocs := testing.AllocsPerRun(100, n.notify); allocs != 0 {
		t.Fatalf("notify with no waiter allocated %v times, want 0", allocs)
	}
}
