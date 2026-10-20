package store

import (
	"context"
	"testing"
	"time"
)

// The engagement event log is domain-agnostic: core never names a kind. These
// tests use a synthetic plugin-declared kind to prove the mechanism, never a
// domain term (invariant #1 / TestVocab).
const testEventKind = "plugin-counter"

func TestAppendEngagementEventAndAggregate(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Three increment events for one Profile+Media+kind.
	for i := 0; i < 3; i++ {
		if _, err := st.AppendEngagementEvent(ctx, EngagementEvent{
			ProfileID: "profile-default",
			MediaID:   "media-alpha",
			Kind:      testEventKind,
			Value:     1,
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	target := EngagementEventTarget{MediaID: "media-alpha"}
	agg, err := st.EngagementEventAggregateFor(ctx, "profile-default", target, testEventKind)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Count != 3 || agg.Sum != 3 {
		t.Fatalf("aggregate = count %d sum %d, want count 3 sum 3", agg.Count, agg.Sum)
	}
	if agg.LastOccurredAt.IsZero() {
		t.Fatal("aggregate LastOccurredAt is zero, want the latest event time")
	}

	events, err := st.EngagementEventsFor(ctx, "profile-default", target, testEventKind)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("history len = %d, want 3", len(events))
	}
	for _, ev := range events {
		if ev.ID == "" {
			t.Fatal("event id not server-issued")
		}
		if ev.OccurredAt.IsZero() {
			t.Fatal("event OccurredAt not defaulted to now")
		}
	}
}

// A correction is a NEW compensating event (a negative value cancelling a prior
// increment), never an UPDATE/DELETE of history. The net aggregate falls, but
// the full timeline — including the cancelled increment — is preserved.
func TestEngagementEventCorrectionIsAppendOnly(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	target := EngagementEventTarget{MediaID: "media-alpha"}

	for i := 0; i < 2; i++ {
		if _, err := st.AppendEngagementEvent(ctx, EngagementEvent{
			ProfileID: "profile-default", MediaID: "media-alpha", Kind: testEventKind, Value: 1,
		}); err != nil {
			t.Fatalf("increment %d: %v", i, err)
		}
	}
	// Remove-last correction: a compensating -1 event.
	if _, err := st.AppendEngagementEvent(ctx, EngagementEvent{
		ProfileID: "profile-default", MediaID: "media-alpha", Kind: testEventKind, Value: -1,
	}); err != nil {
		t.Fatalf("correction: %v", err)
	}

	agg, err := st.EngagementEventAggregateFor(ctx, "profile-default", target, testEventKind)
	if err != nil {
		t.Fatal(err)
	}
	// Net running count is 1 (2 increments minus 1 correction)...
	if agg.Sum != 1 {
		t.Fatalf("net sum = %d, want 1 (2 increments - 1 correction)", agg.Sum)
	}
	// ...but ALL THREE rows survive — history is never edited.
	if agg.Count != 3 {
		t.Fatalf("history count = %d, want 3 (correction is a new row, not a delete)", agg.Count)
	}
	events, err := st.EngagementEventsFor(ctx, "profile-default", target, testEventKind)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("timeline len = %d, want 3 preserved rows", len(events))
	}
}

// Events are Profile-private: a read scoped to one Profile never sees another
// Profile's events, even for the same target and kind.
func TestEngagementEventProfileScoped(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	target := EngagementEventTarget{MediaID: "media-alpha"}

	if _, err := st.AppendEngagementEvent(ctx, EngagementEvent{
		ProfileID: "profile-default", MediaID: "media-alpha", Kind: testEventKind, Value: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEngagementEvent(ctx, EngagementEvent{
		ProfileID: "profile-managed", MediaID: "media-alpha", Kind: testEventKind, Value: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Each Profile sees only its own one event.
	for _, p := range []ProfileID{"profile-default", "profile-managed"} {
		agg, err := st.EngagementEventAggregateFor(ctx, p, target, testEventKind)
		if err != nil {
			t.Fatal(err)
		}
		if agg.Count != 1 || agg.Sum != 1 {
			t.Fatalf("profile %s aggregate = count %d sum %d, want count 1 sum 1 (no cross-profile leak)", p, agg.Count, agg.Sum)
		}
		events, err := st.EngagementEventsFor(ctx, p, target, testEventKind)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || events[0].ProfileID != p {
			t.Fatalf("profile %s history = %#v, want exactly its own event", p, events)
		}
	}
}

// An event may target an Entity instead of (or as well as) a Media, and reads
// scope to the named target independently (ADR-0027 entity aggregates).
func TestEngagementEventEntityTarget(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if _, err := st.AppendEngagementEvent(ctx, EngagementEvent{
		ProfileID: "profile-default", EntityID: "entity-demo-comedy", Kind: testEventKind, Value: 5,
	}); err != nil {
		t.Fatal(err)
	}
	// A Media-targeted event of the same kind must not bleed into the entity read.
	if _, err := st.AppendEngagementEvent(ctx, EngagementEvent{
		ProfileID: "profile-default", MediaID: "media-alpha", Kind: testEventKind, Value: 1,
	}); err != nil {
		t.Fatal(err)
	}

	agg, err := st.EngagementEventAggregateFor(ctx, "profile-default",
		EngagementEventTarget{EntityID: "entity-demo-comedy"}, testEventKind)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Count != 1 || agg.Sum != 5 {
		t.Fatalf("entity aggregate = count %d sum %d, want count 1 sum 5", agg.Count, agg.Sum)
	}
}

// The kind discriminates: a target's events of one kind are invisible to a read
// for another kind, so plugin-declared kinds never collide.
func TestEngagementEventKindIsolation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	target := EngagementEventTarget{MediaID: "media-alpha"}

	if _, err := st.AppendEngagementEvent(ctx, EngagementEvent{
		ProfileID: "profile-default", MediaID: "media-alpha", Kind: testEventKind, Value: 1,
	}); err != nil {
		t.Fatal(err)
	}
	agg, err := st.EngagementEventAggregateFor(ctx, "profile-default", target, "other-plugin-kind")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Count != 0 || agg.Sum != 0 || !agg.LastOccurredAt.IsZero() {
		t.Fatalf("other-kind aggregate = %#v, want zero (kinds isolated)", agg)
	}
}

func TestAppendEngagementEventValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	cases := []struct {
		name  string
		event EngagementEvent
	}{
		{"no profile", EngagementEvent{MediaID: "media-alpha", Kind: testEventKind}},
		{"no target", EngagementEvent{ProfileID: "profile-default", Kind: testEventKind}},
		{"no kind", EngagementEvent{ProfileID: "profile-default", MediaID: "media-alpha"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.AppendEngagementEvent(ctx, tc.event); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

// OccurredAt is the true event instant when supplied (the timestamped
// requirement); the log preserves a backdated time rather than overwriting it
// with now.
func TestEngagementEventPreservesOccurredAt(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	backdated := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

	ev, err := st.AppendEngagementEvent(ctx, EngagementEvent{
		ProfileID: "profile-default", MediaID: "media-alpha", Kind: testEventKind, Value: 1,
		OccurredAt: backdated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ev.OccurredAt.Equal(backdated) {
		t.Fatalf("returned OccurredAt = %v, want %v", ev.OccurredAt, backdated)
	}
	events, err := st.EngagementEventsFor(ctx, "profile-default",
		EngagementEventTarget{MediaID: "media-alpha"}, testEventKind)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !events[0].OccurredAt.Equal(backdated) {
		t.Fatalf("stored OccurredAt = %v, want %v", events, backdated)
	}
}

// Engagement events are a My-Data export citizen: the export carries
// the Profile's event rows, reveal-filtered — a Media-targeted event on a hidden
// Library is dropped while relocked and present once revealed; an Entity-only
// event (no Media reference) always exports.
func TestEngagementEventMyDataExportRevealFilters(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// A visible-Media event, a restricted-Media event, and an Entity-only event.
	for _, ev := range []EngagementEvent{
		{ProfileID: "profile-default", MediaID: "media-alpha", Kind: testEventKind, Value: 1},
		{ProfileID: "profile-default", MediaID: "media-archive", Kind: testEventKind, Value: 1},
		{ProfileID: "profile-default", EntityID: "entity-demo-comedy", Kind: testEventKind, Value: 1},
	} {
		if _, err := st.AppendEngagementEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	relocked, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(false))
	if err != nil {
		t.Fatal(err)
	}
	// Relocked: media-archive (restricted) dropped; media-alpha + the entity-only
	// row survive.
	if got := len(relocked.EngagementEvents); got != 2 {
		t.Fatalf("relocked export events = %d, want 2 (media-archive hidden)", got)
	}
	for _, ev := range relocked.EngagementEvents {
		if ev.MediaID == "media-archive" {
			t.Fatalf("relocked export leaked restricted media-archive: %+v", ev)
		}
	}

	revealed, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(true))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(revealed.EngagementEvents); got != 3 {
		t.Fatalf("revealed export events = %d, want all 3", got)
	}
}

// The My-Data hard-delete clears engagement events too: delete-all
// removes every row (Media AND Entity-only); a per-Media delete removes only that
// Media's rows and leaves the Entity-only row intact.
func TestEngagementEventMyDataHardDelete(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := profileDefaultFilter(true)
	target := EngagementEventTarget{MediaID: "media-alpha"}
	entityTarget := EngagementEventTarget{EntityID: "entity-demo-comedy"}

	seed := func() {
		for _, ev := range []EngagementEvent{
			{ProfileID: "profile-default", MediaID: "media-alpha", Kind: testEventKind, Value: 1},
			{ProfileID: "profile-default", EntityID: "entity-demo-comedy", Kind: testEventKind, Value: 1},
		} {
			if _, err := st.AppendEngagementEvent(ctx, ev); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Per-Media delete: only media-alpha's rows go; the entity-only row stays.
	seed()
	if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteMedia, "media-alpha", revealed); err != nil {
		t.Fatal(err)
	}
	if agg, _ := st.EngagementEventAggregateFor(ctx, "profile-default", target, testEventKind); agg.Count != 0 {
		t.Fatalf("media-alpha events after per-media delete = %d, want 0", agg.Count)
	}
	if agg, _ := st.EngagementEventAggregateFor(ctx, "profile-default", entityTarget, testEventKind); agg.Count != 1 {
		t.Fatalf("entity events after per-media delete = %d, want 1 (untouched)", agg.Count)
	}

	// Delete-all: the remaining entity-only row goes too.
	if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteAll, "", revealed); err != nil {
		t.Fatal(err)
	}
	if agg, _ := st.EngagementEventAggregateFor(ctx, "profile-default", entityTarget, testEventKind); agg.Count != 0 {
		t.Fatalf("entity events after delete-all = %d, want 0", agg.Count)
	}
}

// The orphan prune sweeps Media-targeted event rows whose Media no longer exists,
// but never an Entity-only row (it has no Media-existence anchor).
func TestEngagementEventPruneSweepsMediaOrphansOnly(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Live Media, missing Media, and Entity-only events.
	for _, ev := range []EngagementEvent{
		{ProfileID: "profile-default", MediaID: "media-alpha", Kind: testEventKind, Value: 1},
		{ProfileID: "profile-default", MediaID: "media-missing", Kind: testEventKind, Value: 1},
		{ProfileID: "profile-default", EntityID: "entity-demo-comedy", Kind: testEventKind, Value: 1},
	} {
		if _, err := st.AppendEngagementEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	counts, err := st.PruneOrphanedEngagementRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.EventRows != 1 {
		t.Fatalf("pruned event rows = %d, want 1 (the missing-Media orphan)", counts.EventRows)
	}
	if got := countEventRows(t, st, "engagement_event", "media-alpha"); got != 1 {
		t.Fatalf("live media-alpha event rows = %d, want 1", got)
	}
	if got := countEventRows(t, st, "engagement_event", "media-missing"); got != 0 {
		t.Fatalf("orphaned media-missing event rows = %d, want 0", got)
	}
	// The Entity-only row survives (no Media anchor).
	if agg, _ := st.EngagementEventAggregateFor(ctx, "profile-default",
		EngagementEventTarget{EntityID: "entity-demo-comedy"}, testEventKind); agg.Count != 1 {
		t.Fatalf("entity-only event after prune = %d, want 1 (never orphaned)", agg.Count)
	}
}
