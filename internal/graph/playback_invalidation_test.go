package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestReportPlaybackInvalidatesEngagementWithThrottleAndProfileScope(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	clock := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	handler.nowFunc = func() time.Time { return clock }

	report := func(authorize func(*http.Request), vars map[string]any) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"query":     `mutation R($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int!, $finished: Boolean, $paused: Boolean, $ambient: Boolean) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, finished: $finished, paused: $paused, ambient: $ambient) { mediaID } }`,
			"variables": vars,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, errs := post(t, handler, string(payload), authorize); len(errs) != 0 {
			t.Fatalf("reportPlayback errors = %#v", errs)
		}
	}

	// Ambient play never writes engagement or emits an invalidation.
	report(authorizeViewer, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 30, "durationSeconds": 120, "ambient": true,
	})
	if got := engagementInvalidations(t, st, "profile-default"); got != 0 {
		t.Fatalf("ambient report emitted %d invalidations, want 0", got)
	}

	// First periodic report emits 1; subsequent 15s reports within the 60s
	// cooldown are throttled.
	report(authorizeViewer, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 15, "durationSeconds": 120, "paused": false,
	})
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("initial report invalidations = %d, want 1", got)
	}
	for _, offset := range []time.Duration{15 * time.Second, 30 * time.Second, 45 * time.Second} {
		clock = clock.Add(15 * time.Second)
		report(authorizeViewer, map[string]any{
			"mediaID": "media-alpha", "positionSeconds": int(15 + offset.Seconds()), "durationSeconds": 120, "paused": false,
		})
		if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
			t.Fatalf("periodic report at +%s emitted extra invalidation (%d total)", offset, got)
		}
	}

	// Another Profile is not throttled by profile-default's cooldown.
	report(authorizeManaged, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 15, "durationSeconds": 120, "paused": false,
	})
	if got := engagementInvalidations(t, st, "profile-managed"); got != 1 {
		t.Fatalf("profile-managed invalidations = %d, want 1", got)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("profile-managed report affected profile-default (%d)", got)
	}

	// Once the cooldown elapses, the next periodic report emits again.
	clock = clock.Add(15 * time.Second)
	report(authorizeViewer, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 75, "durationSeconds": 120, "paused": false,
	})
	if got := engagementInvalidations(t, st, "profile-default"); got != 2 {
		t.Fatalf("post-cooldown report invalidations = %d, want 2", got)
	}

	// Pausing inside the cooldown emits immediately, and endPlayback after a
	// pause does not double-emit.
	clock = clock.Add(5 * time.Second)
	report(authorizeViewer, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 80, "durationSeconds": 120, "paused": true,
	})
	if got := engagementInvalidations(t, st, "profile-default"); got != 3 {
		t.Fatalf("pause invalidations = %d, want 3", got)
	}
	if _, errs := post(t, handler, `{"query":"mutation { endPlayback(mediaId: \"media-alpha\") }"}`, authorizeViewer); len(errs) != 0 {
		t.Fatalf("endPlayback errors = %#v", errs)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 3 {
		t.Fatalf("endPlayback after clean pause emitted extra invalidation (%d)", got)
	}

	// Throttled periodic progress followed by endPlayback (closing mid-play)
	// flushes one invalidation on stop.
	clock = clock.Add(5 * time.Second)
	report(authorizeViewer, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 85, "durationSeconds": 120, "paused": false,
	})
	if got := engagementInvalidations(t, st, "profile-default"); got != 3 {
		t.Fatalf("throttled resume invalidations = %d, want 3", got)
	}
	if _, errs := post(t, handler, `{"query":"mutation { endPlayback(mediaId: \"media-alpha\") }"}`, authorizeViewer); len(errs) != 0 {
		t.Fatalf("endPlayback errors = %#v", errs)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 4 {
		t.Fatalf("endPlayback flush invalidations = %d, want 4", got)
	}

	// Crossing the watched threshold (without finished: true) also invalidates
	// immediately inside the cooldown.
	clock = clock.Add(5 * time.Second)
	report(authorizeViewer, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 115, "durationSeconds": 120, "paused": false,
	})
	if got := engagementInvalidations(t, st, "profile-default"); got != 5 {
		t.Fatalf("watched-threshold invalidations = %d, want 5", got)
	}
}

func TestPausedHeartbeatsDoNotBypassCooldown(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	clock := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	handler.nowFunc = func() time.Time { return clock }

	report := func(vars map[string]any) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"query":     `mutation R($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int!, $paused: Boolean) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, paused: $paused) { mediaID } }`,
			"variables": vars,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, errs := post(t, handler, string(payload), authorizeViewer); len(errs) != 0 {
			t.Fatalf("reportPlayback errors = %#v", errs)
		}
	}

	report(map[string]any{"mediaID": "media-alpha", "positionSeconds": 15, "durationSeconds": 120, "paused": false})
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("initial report invalidations = %d, want 1", got)
	}

	clock = clock.Add(5 * time.Second)
	report(map[string]any{"mediaID": "media-alpha", "positionSeconds": 20, "durationSeconds": 120, "paused": true})
	if got := engagementInvalidations(t, st, "profile-default"); got != 2 {
		t.Fatalf("pause transition invalidations = %d, want 2", got)
	}

	// Repeated 15-second paused heartbeats at an unchanged position must not emit.
	for _, step := range []int{1, 2, 3, 4, 5} {
		clock = clock.Add(15 * time.Second)
		report(map[string]any{"mediaID": "media-alpha", "positionSeconds": 20, "durationSeconds": 120, "paused": true})
		if got := engagementInvalidations(t, st, "profile-default"); got != 2 {
			t.Fatalf("paused heartbeat #%d emitted extra invalidation (%d total, want 2)", step, got)
		}
	}

	// Resuming and pausing again emits on the new pause transition.
	clock = clock.Add(5 * time.Second)
	report(map[string]any{"mediaID": "media-alpha", "positionSeconds": 35, "durationSeconds": 120, "paused": false})
	if got := engagementInvalidations(t, st, "profile-default"); got != 3 {
		t.Fatalf("resume after cooldown invalidations = %d, want 3", got)
	}
	clock = clock.Add(5 * time.Second)
	report(map[string]any{"mediaID": "media-alpha", "positionSeconds": 40, "durationSeconds": 120, "paused": true})
	if got := engagementInvalidations(t, st, "profile-default"); got != 4 {
		t.Fatalf("second pause transition invalidations = %d, want 4", got)
	}
}

func TestCooldownDeferredChangeSchedulesFlush(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	clock := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	handler.nowFunc = func() time.Time { return clock }

	type scheduled struct {
		delay   time.Duration
		fire    func()
		stopped bool
	}
	var timers []*scheduled
	handler.afterFunc = func(d time.Duration, fn func()) func() bool {
		entry := &scheduled{delay: d, fire: fn}
		timers = append(timers, entry)
		return func() bool {
			wasActive := !entry.stopped
			entry.stopped = true
			return wasActive
		}
	}

	reportOmittedPaused := func(pos int) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"query":     `mutation R($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int!) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds) { mediaID } }`,
			"variables": map[string]any{"mediaID": "media-alpha", "positionSeconds": pos, "durationSeconds": 120},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, errs := post(t, handler, string(payload), authorizeViewer); len(errs) != 0 {
			t.Fatalf("reportPlayback errors = %#v", errs)
		}
	}

	reportOmittedPaused(15)
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("opening report invalidations = %d, want 1", got)
	}

	// Client omits optional paused field and sends its final position during
	// the cooldown, then stays silent without calling endPlayback.
	clock = clock.Add(20 * time.Second)
	reportOmittedPaused(35)
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("cooldown report invalidations = %d, want 1", got)
	}
	if len(timers) != 1 {
		t.Fatalf("scheduled timers = %d, want 1", len(timers))
	}
	if want := 40 * time.Second; timers[0].delay != want {
		t.Fatalf("scheduled flush delay = %s, want %s", timers[0].delay, want)
	}

	clock = clock.Add(40 * time.Second)
	timers[0].fire()
	if got := engagementInvalidations(t, st, "profile-default"); got != 2 {
		t.Fatalf("scheduled flush invalidations = %d, want 2", got)
	}
}

func TestFailedOutboxAppendPreservesDirtyForEndPlayback(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	clock := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	var onNow func()
	handler.nowFunc = func() time.Time {
		if onNow != nil {
			onNow()
		}
		return clock
	}

	// Initial playing report emits, then a cooldown report marks the Profile dirty.
	if _, errs := post(t, handler, `{"query":"mutation { reportPlayback(mediaID: \"media-alpha\", positionSeconds: 15, durationSeconds: 120, paused: false) { mediaID } }"}`, authorizeViewer); len(errs) != 0 {
		t.Fatalf("opening report errors = %#v", errs)
	}
	clock = clock.Add(10 * time.Second)
	if _, errs := post(t, handler, `{"query":"mutation { reportPlayback(mediaID: \"media-alpha\", positionSeconds: 30, durationSeconds: 120, paused: false) { mediaID } }"}`, authorizeViewer); len(errs) != 0 {
		t.Fatalf("cooldown report errors = %#v", errs)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("invalidations before pause = %d, want 1", got)
	}

	// Pause is saved to the store, then the request context cancels before the
	// outbox append succeeds.
	clock = clock.Add(5 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	onNow = cancel
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"mutation { reportPlayback(mediaID: \"media-alpha\", positionSeconds: 45, durationSeconds: 120, paused: true) { mediaID } }"}`)))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	onNow = nil

	saved, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil || !ok || saved.PositionSeconds != 45 {
		t.Fatalf("saved progress = (%+v, %v, %v), want position 45", saved, ok, err)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("invalidations after canceled append = %d, want 1", got)
	}

	// Subsequent endPlayback must still flush the dirty state.
	if _, errs := post(t, handler, `{"query":"mutation { endPlayback(mediaId: \"media-alpha\") }"}`, authorizeViewer); len(errs) != 0 {
		t.Fatalf("endPlayback errors = %#v", errs)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 2 {
		t.Fatalf("invalidations after endPlayback = %d, want 2", got)
	}
}

type cancelAfterCommitContext struct {
	context.Context
	closed    chan struct{}
	sawUpsert bool
	canceled  bool
}

func newCancelAfterCommitContext(parent context.Context) *cancelAfterCommitContext {
	closed := make(chan struct{})
	close(closed)
	return &cancelAfterCommitContext{
		Context: parent,
		closed:  closed,
	}
}

func inUpsertPlaybackProgress() bool {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "UpsertPlaybackProgress") {
			return true
		}
		if !more {
			return false
		}
	}
}

func (c *cancelAfterCommitContext) Done() <-chan struct{} {
	if !c.canceled {
		if inUpsertPlaybackProgress() {
			c.sawUpsert = true
		} else if c.sawUpsert {
			c.canceled = true
		}
	}
	if c.canceled {
		return c.closed
	}
	return nil
}

func (c *cancelAfterCommitContext) Err() error {
	if c.canceled {
		return context.Canceled
	}
	return nil
}

func TestCanceledPostCommitReadMarksDirtyForEndPlayback(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	clock := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	handler.nowFunc = func() time.Time { return clock }

	// Opening report emits immediately, leaving no pending dirty state.
	if _, errs := post(t, handler, `{"query":"mutation { reportPlayback(mediaID: \"media-alpha\", positionSeconds: 15, durationSeconds: 120, paused: false) { mediaID } }"}`, authorizeViewer); len(errs) != 0 {
		t.Fatalf("opening report errors = %#v", errs)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("opening invalidations = %d, want 1", got)
	}

	// Pause commits, then the request cancels before PlaybackProgressFor reads
	// back the stored row. endPlayback must still flush the committed pause.
	clock = clock.Add(5 * time.Second)
	pauseCtx := newCancelAfterCommitContext(context.Background())
	req := httptest.NewRequestWithContext(pauseCtx, http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"mutation { reportPlayback(mediaID: \"media-alpha\", positionSeconds: 45, durationSeconds: 120, paused: true) { mediaID } }"}`)))
	authorizeViewer(req)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	saved, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil || !ok || saved.PositionSeconds != 45 {
		t.Fatalf("saved progress after pause = (%+v, %v, %v), want position 45", saved, ok, err)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 1 {
		t.Fatalf("invalidations after canceled pause read = %d, want 1", got)
	}
	if _, errs := post(t, handler, `{"query":"mutation { endPlayback(mediaId: \"media-alpha\") }"}`, authorizeViewer); len(errs) != 0 {
		t.Fatalf("endPlayback after pause errors = %#v", errs)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 2 {
		t.Fatalf("invalidations after endPlayback = %d, want 2", got)
	}

	// Same race for a finish report with no earlier notification pending.
	clock = clock.Add(5 * time.Second)
	finishCtx := newCancelAfterCommitContext(context.Background())
	req = httptest.NewRequestWithContext(finishCtx, http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"mutation { reportPlayback(mediaID: \"media-alpha\", positionSeconds: 120, durationSeconds: 120, finished: true) { mediaID } }"}`)))
	authorizeViewer(req)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	saved, ok, err = st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil || !ok || !saved.Watched {
		t.Fatalf("saved progress after finish = (%+v, %v, %v), want watched", saved, ok, err)
	}

	if got := engagementInvalidations(t, st, "profile-default"); got != 2 {
		t.Fatalf("invalidations after canceled finish read = %d, want 2", got)
	}
	if _, errs := post(t, handler, `{"query":"mutation { endPlayback(mediaId: \"media-alpha\") }"}`, authorizeViewer); len(errs) != 0 {
		t.Fatalf("endPlayback after finish errors = %#v", errs)
	}
	if got := engagementInvalidations(t, st, "profile-default"); got != 3 {
		t.Fatalf("invalidations after finish endPlayback = %d, want 3", got)
	}
}

func TestPausedHeartbeatDurationCorrectionSchedulesInvalidation(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	clock := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	handler.nowFunc = func() time.Time { return clock }

	type scheduled struct {
		delay   time.Duration
		fire    func()
		stopped bool
	}
	var timers []*scheduled
	handler.afterFunc = func(d time.Duration, fn func()) func() bool {
		entry := &scheduled{delay: d, fire: fn}
		timers = append(timers, entry)
		return func() bool {
			wasActive := !entry.stopped
			entry.stopped = true
			return wasActive
		}
	}

	report := func(pos, dur int, paused bool) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"query":     `mutation R($mediaID: String!, $positionSeconds: Int!, $durationSeconds: Int!, $paused: Boolean) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, paused: $paused) { mediaID } }`,
			"variables": map[string]any{"mediaID": "media-alpha", "positionSeconds": pos, "durationSeconds": dur, "paused": paused},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, errs := post(t, handler, string(payload), authorizeViewer); len(errs) != 0 {
			t.Fatalf("reportPlayback errors = %#v", errs)
		}
	}

	report(15, 120, false)
	clock = clock.Add(5 * time.Second)
	report(20, 120, true)
	if got := engagementInvalidations(t, st, "profile-default"); got != 2 {
		t.Fatalf("invalidations after pause = %d, want 2", got)
	}

	// A paused heartbeat at the same position that corrects the duration stores
	// the new duration and schedules a trailing invalidation.
	clock = clock.Add(15 * time.Second)
	report(20, 180, true)
	saved, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil || !ok || saved.DurationSeconds != 180 {
		t.Fatalf("saved progress = (%+v, %v, %v), want duration 180", saved, ok, err)
	}
	if len(timers) != 1 {
		t.Fatalf("scheduled timers = %d, want 1", len(timers))
	}
	if want := 45 * time.Second; timers[0].delay != want {
		t.Fatalf("scheduled flush delay = %s, want %s", timers[0].delay, want)
	}

	clock = clock.Add(45 * time.Second)
	timers[0].fire()
	if got := engagementInvalidations(t, st, "profile-default"); got != 3 {
		t.Fatalf("invalidations after duration-correction flush = %d, want 3", got)
	}
}

// A report that stages after another report for the same Profile staged
// between its prior read and its own stage must see a sequence gap, so it
// keeps the dirty state even when its readback matches its stale prior.
func TestPlaybackSeqDetectsInterleavedStage(t *testing.T) {
	handler := newTestHandlerWithStore(storetest.Open(t))
	before := handler.playbackSeq("profile-default")
	handler.stagePlaybackDirty("profile-default") // the other session's pause
	seq, _ := handler.stagePlaybackDirty("profile-default")
	if seq == before+1 {
		t.Fatalf("interleaved stage not visible: before=%d seq=%d", before, seq)
	}
	alone := handler.playbackSeq("profile-default")
	if seq, _ := handler.stagePlaybackDirty("profile-default"); seq != alone+1 {
		t.Fatalf("uncontended stage seq=%d, want %d", seq, alone+1)
	}
	// An item action (mark Finished) in between is a gap too.
	before = handler.playbackSeq("profile-default")
	handler.notifyEngagementChanged(t.Context(), auth.Scope{ProfileID: "profile-default"})
	if seq, _ := handler.stagePlaybackDirty("profile-default"); seq == before+1 {
		t.Fatalf("item action not visible to a racing heartbeat: before=%d seq=%d", before, seq)
	}
}
