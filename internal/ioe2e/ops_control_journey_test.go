//go:build io_e2e

package ioe2e

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/jobs"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/schedule"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/watcher"
)

// TestOpsControlReleaseJourney drives the operations-control surfaces that
// ADR-0013/0032/0076 promise for repeatable local operations: job cancel/retry
// eligibility with durable audit rows, process-restart recovery, missed-schedule
// catch-up-once, watcher degradation with the Schedule-scan backstop, and
// governor preemption under the low-power posture.
//
// The operator's *control* actions differ from the operator's *observability*.
// Job cancel/retry is fully wire-driven (admin-gated mutations). Restart
// recovery, schedule ticks, watcher arming, and transcode preemption have no
// graph/App trigger — those are internal timers/lifecycle — so this journey
// drives them through their store methods and leaf services, then asserts the
// Console-visible result over the graph wire (job / schedules / watcherStatus /
// operationsGovernor admin reads), exactly the surfaces the Console renders.
//
// scenario:operations-control-journey
func TestOpsControlReleaseJourney(t *testing.T) {
	t.Run("job_cancel_retry_audit", opsJobCancelRetryAudit)
	t.Run("restart_recovery", opsRestartRecovery)
	t.Run("schedule_catch_up_once", opsScheduleCatchUpOnce)
	t.Run("watcher_degraded_backstop", opsWatcherDegradedBackstop)
	t.Run("governor_preemption_low_power", opsGovernorPreemptionLowPower)
}

// opsJobCancelRetryAudit drives the operations job controls over the graph wire:
// retry eligibility rejects a non-terminal job, cancel reaches the terminal
// canceled state, the cancel is durable in the job event audit log, and a
// terminal job retries into a fresh queued clone.
func opsJobCancelRetryAudit(t *testing.T) {
	st := storetest.Open(t)
	h := basicJourneyHandler(st)
	ctx := context.Background()
	grantAdmin(t, st)

	job, err := st.EnqueueJob(ctx, store.Job{Kind: "ops-artwork-sweep", Class: store.JobClassBackground})
	if err != nil {
		t.Fatal(err)
	}

	retryQueued := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($id:String!){ jobRetry(id:$id){ id status } }`,
		map[string]any{"id": string(job.ID)})
	if len(retryQueued.Errors) == 0 {
		t.Fatalf("retry of a non-terminal (queued) job succeeded — eligibility bypass: %s", retryQueued.body)
	}

	cancel := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($id:String!){ jobCancel(id:$id){ id status } }`,
		map[string]any{"id": string(job.ID)})
	cancel.mustHaveNoErrors(t)
	if got := graphField(t, cancel, "jobCancel", "status"); got != "canceled" {
		t.Fatalf("jobCancel final status = %q, want canceled", got)
	}

	events := postJourneyGraph(t, h, authorizeJourneyManager,
		`query($id:String!){ jobEvents(id:$id){ type } }`,
		map[string]any{"id": string(job.ID)})
	events.mustHaveNoErrors(t)
	if !strings.Contains(events.body, `"queued"`) || !strings.Contains(events.body, `"canceled"`) {
		t.Fatalf("job audit log missing enqueue/cancel rows: %s", events.body)
	}

	retry := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($id:String!){ jobRetry(id:$id){ id status } }`,
		map[string]any{"id": string(job.ID)})
	retry.mustHaveNoErrors(t)
	retryID := graphField(t, retry, "jobRetry", "id")
	if retryID == "" || retryID == string(job.ID) {
		t.Fatalf("jobRetry did not mint a new job: retryID=%q original=%q", retryID, job.ID)
	}
	if got := graphField(t, retry, "jobRetry", "status"); got != "queued" {
		t.Fatalf("retried job status = %q, want queued", got)
	}
}

// opsRestartRecovery proves process-restart recovery: a job left running when
// the process died is reclaimed to the claimable yielded state on the recovery
// pass, carries a durable resume audit row, is re-claimable, and reports the
// recovered state over the wire (the operator's Console job read).
func opsRestartRecovery(t *testing.T) {
	st := storetest.Open(t)
	h := basicJourneyHandler(st)
	ctx := context.Background()
	now := time.Now().UTC()
	grantAdmin(t, st)

	// CreatedAt pins RunAfter to now so the claim below is deterministic (a
	// default enqueue timestamps RunAfter a hair after the captured now).
	job, err := st.EnqueueJob(ctx, store.Job{Kind: "ops-restart-scan", Class: store.JobClassBackground, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker-before-restart", []store.JobClass{store.JobClassBackground}, now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != job.ID || claimed.Status != store.JobStatusRunning {
		t.Fatalf("claim = {id:%s status:%s}, want running %s", claimed.ID, claimed.Status, job.ID)
	}

	recovered, err := jobs.NewRegistry(st, "worker-after-process-restart").Resume(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if recovered < 1 {
		t.Fatalf("ResumeInterruptedJobs recovered %d jobs, want >=1", recovered)
	}
	after, err := st.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != store.JobStatusYielded {
		t.Fatalf("recovered job status = %q, want yielded (claimable after restart)", after.Status)
	}

	events, err := st.ListJobEvents(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasJobEvent(events, "yielded") {
		t.Fatalf("restart recovery left no yielded audit row: %+v", events)
	}

	// The recovered state is Console-visible over the wire.
	wire := postJourneyGraph(t, h, authorizeJourneyManager,
		`query($id:String!){ job(id:$id){ id status } }`,
		map[string]any{"id": string(job.ID)})
	wire.mustHaveNoErrors(t)
	if got := graphField(t, wire, "job", "status"); got != "yielded" {
		t.Fatalf("Console job read after restart = %q, want yielded", got)
	}

	reclaimed, err := st.ClaimNextJob(ctx, "worker-after-restart", []store.JobClass{store.JobClassBackground}, now)
	if err != nil {
		t.Fatalf("recovered job not re-claimable after restart: %v", err)
	}
	if reclaimed.ID != job.ID {
		t.Fatalf("re-claim got %s, want recovered %s", reclaimed.ID, job.ID)
	}
}

// opsScheduleCatchUpOnce proves a schedule overdue across a downtime catches up
// exactly ONCE: the first tick fires the missed schedule and advances the
// cursor; a duplicate tick in the same window must not re-fire (duplicate
// catch-up reds). The advanced cursor is Console-visible via the schedules read.
func opsScheduleCatchUpOnce(t *testing.T) {
	st := storetest.Open(t)
	h := basicJourneyHandler(st)
	ctx := context.Background()
	now := time.Now().UTC()
	grantAdmin(t, st)

	const schedID = store.ScheduleID("ops-catchup")
	if err := st.CreateSchedule(ctx, store.Schedule{
		ID:                   schedID,
		JobKind:              "ops-catchup-scan",
		Trigger:              schedule.Every(1, schedule.Minutes),
		Enabled:              true,
		LastCompletedEnqueue: now.Add(-6 * time.Hour), // overdue by hundreds of intervals
	}); err != nil {
		t.Fatal(err)
	}

	svc := schedule.NewService(st)
	built := 0
	svc.Register(schedule.Spec{
		ID:             string(schedID),
		JobKind:        "ops-catchup-scan",
		DefaultTrigger: schedule.Every(1, schedule.Minutes),
		BuildJobs: func(context.Context, time.Time) ([]store.Job, error) {
			built++
			return []store.Job{{Kind: "ops-catchup-scan", Class: store.JobClassBackground}}, nil
		},
	})

	if err := svc.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}

	if built != 1 {
		t.Fatalf("overdue schedule built %d job batches across two ticks, want exactly 1 (duplicate catch-up must red)", built)
	}
	queued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if n := countJobKind(queued, "ops-catchup-scan"); n != 1 {
		t.Fatalf("catch-up enqueued %d jobs, want exactly 1", n)
	}

	// The catch-up advanced the cursor: the Console schedules read now reports a
	// future nextDue for the schedule (it is no longer overdue).
	schedules := postJourneyGraph(t, h, authorizeJourneyManager,
		`query{ schedules{ id nextDue lastEnqueued } }`, nil)
	schedules.mustHaveNoErrors(t)
	nextDue := scheduleFieldByID(t, schedules, string(schedID), "nextDue")
	if nextDue == "" {
		t.Fatalf("Console schedules read missing %s after catch-up: %s", schedID, schedules.body)
	}
	due, err := time.Parse(time.RFC3339, nextDue)
	if err != nil {
		t.Fatalf("parse nextDue %q: %v", nextDue, err)
	}
	if !due.After(now) {
		t.Fatalf("catch-up did not advance the cursor: nextDue %s is not after now %s (schedule still overdue)", due, now)
	}
}

// opsWatcherDegradedBackstop proves the watcher degrades visibly when a root
// cannot be armed — including over the wire via the Console watcherStatus read —
// and that the periodic Schedule scan (ADR-0032, keyed by schedule.ScanScheduleID)
// is the correctness backstop that still enqueues the library scan while the
// watcher is unavailable.
func opsWatcherDegradedBackstop(t *testing.T) {
	st := storetest.Open(t)
	h := basicJourneyHandler(st)
	ctx := context.Background()
	now := time.Now().UTC()
	grantAdmin(t, st)
	root := t.TempDir()
	library, err := st.CreateLibraryWithRoots(ctx, "Ops Backstop", false, "", "profile-default", []string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	lib := library.ID

	w := watcher.NewService(watcher.Options{})
	t.Cleanup(func() { _ = w.Close() })
	h.SetWatcherStatus(watcherStatusAdapter{svc: w})

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	status := w.Observe(lib, missing)
	if status.Available || status.Error == "" {
		t.Fatalf("watcher over an unwatchable root = %+v, want degraded (Available=false, Error set)", status)
	}

	// The degradation is Console-visible over the wire.
	wire := postJourneyGraph(t, h, authorizeJourneyManager,
		`query{ watcherStatus{ libraryID state reason } }`, nil)
	wire.mustHaveNoErrors(t)
	if state := watcherStateByLibrary(t, wire, string(lib)); state != "unavailable" {
		t.Fatalf("Console watcherStatus for %s = %q, want unavailable (degraded): %s", lib, state, wire.body)
	}

	// Scan backstop: the real scan:<library> Schedule row is
	// auto-created with the Library and still enqueues the shared scan job while
	// the watcher is down. The completion cursor is monotonic
	// (AUDITFIX-SCHEDULER-DURABILITY) so it cannot be back-dated to fake
	// overdueness; ticking past the 12h default interval reaches it honestly.
	svc := schedule.NewService(st)
	svc.Register(realScanScheduleSpec(st, lib))
	if err := svc.Tick(ctx, now.Add(13*time.Hour)); err != nil {
		t.Fatal(err)
	}
	queued, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	scanJob := scanJobForLibrary(queued, lib)
	if scanJob.ID == "" {
		t.Fatalf("degraded-watcher backstop did not enqueue scan job for %s: %#v", lib, queued)
	}
	events, err := st.ListJobEvents(ctx, scanJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasJobEvent(events, "queued") {
		t.Fatalf("scheduled scan job missing queued audit row: %+v", events)
	}
}

// opsGovernorPreemptionLowPower proves the low-power governor (single generation
// worker, one-slot budget) preempts background generation whenever a realtime
// transcode is active, that the preemption is Console-visible over the wire via
// operationsGovernor (lowPowerMode + transcodePreemptingBackground + the
// generationAllowed flip), and that in-flight preemptible heavy-class jobs are
// cancelled on the disable path.
func opsGovernorPreemptionLowPower(t *testing.T) {
	st := storetest.Open(t)
	h := basicJourneyHandler(st)
	grantAdmin(t, st)

	g := governor.NewWithOptions(governor.Options{GenerationWorkers: 1, BackgroundCPUBudget: 1})
	h.SetGovernor(g)
	if g.GenerationWorkers() != 1 || g.BackgroundCPUBudget() != 1 {
		t.Fatalf("low-power governor = workers %d budget %d, want 1/1", g.GenerationWorkers(), g.BackgroundCPUBudget())
	}

	// Idle low-power posture is Console-visible: low power, generation allowed.
	idle := postJourneyGraph(t, h, authorizeJourneyManager,
		`query{ operationsGovernor{ lowPowerMode generationAllowed activeTranscodes transcodePreemptingBackground } }`, nil)
	idle.mustHaveNoErrors(t)
	if !governorFlag(t, idle, "lowPowerMode") {
		t.Fatalf("operationsGovernor.lowPowerMode = false on a 1/1 governor: %s", idle.body)
	}
	if !governorFlag(t, idle, "generationAllowed") {
		t.Fatalf("operationsGovernor.generationAllowed = false while idle: %s", idle.body)
	}

	// A realtime transcode preempts background generation. Asserted with NO
	// slot held so the denial isolates the transcode guard (a free one-slot
	// budget would otherwise admit generation) — the preemption invariant, not
	// the throttle.
	g.TranscodeStarted()
	if g.GenerationAllowed() {
		t.Fatalf("generation allowed while a transcode preempts it (activeTranscodes=%d, budget free)", g.ActiveTranscodes())
	}
	preempt := postJourneyGraph(t, h, authorizeJourneyManager,
		`query{ operationsGovernor{ lowPowerMode generationAllowed activeTranscodes transcodePreemptingBackground } }`, nil)
	preempt.mustHaveNoErrors(t)
	if governorFlag(t, preempt, "generationAllowed") {
		t.Fatalf("operationsGovernor.generationAllowed = true during transcode preemption: %s", preempt.body)
	}
	g.TranscodeStopped()

	// An in-flight generation slot being preempted by an arriving transcode is
	// Console-visible via transcodePreemptingBackground.
	if !g.AcquireGeneration() {
		t.Fatal("generation not admitted on an idle low-power governor")
	}
	g.TranscodeStarted()
	busy := postJourneyGraph(t, h, authorizeJourneyManager,
		`query{ operationsGovernor{ lowPowerMode generationAllowed activeTranscodes transcodePreemptingBackground } }`, nil)
	busy.mustHaveNoErrors(t)
	if !governorFlag(t, busy, "transcodePreemptingBackground") {
		t.Fatalf("operationsGovernor.transcodePreemptingBackground = false while a transcode preempts an in-flight slot: %s", busy.body)
	}

	// Preemption releases when the transcode ends.
	g.ReleaseGeneration()
	g.TranscodeStopped()
	if !g.AcquireGeneration() {
		t.Fatal("generation not resumed after the preempting transcode ended")
	}
	g.ReleaseGeneration()

	// Preemptible heavy classes (inference) are cancelled in-flight on disable.
	canceled := false
	release := g.StartJob(governor.ClassInference, func() { canceled = true })
	if g.ActiveJobs(governor.ClassInference) != 1 {
		t.Fatalf("inference job not registered: active=%d", g.ActiveJobs(governor.ClassInference))
	}
	if n := g.CancelClass(governor.ClassInference); n != 1 {
		t.Fatalf("CancelClass cancelled %d inference jobs, want 1", n)
	}
	if !canceled || g.ActiveJobs(governor.ClassInference) != 0 {
		t.Fatalf("inference preemption incomplete: canceled=%v active=%d", canceled, g.ActiveJobs(governor.ClassInference))
	}
	release() // completion after cancel is a no-op — never re-opens the class
}

// grantAdmin opens the admin-session window on the dev-seeded manager session so
// the wire journey can reach the admin-gated Operations reads/mutations.
func grantAdmin(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.SetSessionAdminUntil(context.Background(), "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
}

// watcherStatusAdapter bridges *watcher.Service to the graph.WatcherStatusProvider
// seam the Console watcherStatus read consumes, exactly as the app
// composition root wires it.
type watcherStatusAdapter struct{ svc *watcher.Service }

func (a watcherStatusAdapter) Status(libraryID store.LibraryID) (active bool, available bool, reason string) {
	s := a.svc.Status(libraryID)
	return s.Active, s.Available, s.Error
}

var _ graph.WatcherStatusProvider = watcherStatusAdapter{}
var _ graph.GovernorStatusProvider = (*governor.Governor)(nil)

// graphField extracts a string field from a single-object GraphQL response
// datum (e.g. data.jobCancel.status).
func graphField(t *testing.T, resp journeyGraphResponse, key, field string) string {
	t.Helper()
	raw, ok := resp.Data[key]
	if !ok {
		t.Fatalf("response missing %q datum: %s", key, resp.body)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode %q datum: %v\n%s", key, err, resp.body)
	}
	value, _ := obj[field].(string)
	return value
}

// governorFlag reads a boolean field from the operationsGovernor datum.
func governorFlag(t *testing.T, resp journeyGraphResponse, field string) bool {
	t.Helper()
	raw, ok := resp.Data["operationsGovernor"]
	if !ok {
		t.Fatalf("response missing operationsGovernor datum: %s", resp.body)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode operationsGovernor: %v\n%s", err, resp.body)
	}
	value, _ := obj[field].(bool)
	return value
}

// scheduleFieldByID returns a string field of the schedule with the given id
// from a schedules query datum.
func scheduleFieldByID(t *testing.T, resp journeyGraphResponse, id, field string) string {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(resp.Data["schedules"], &rows); err != nil {
		t.Fatalf("decode schedules: %v\n%s", err, resp.body)
	}
	for _, row := range rows {
		if row["id"] == id {
			value, _ := row[field].(string)
			return value
		}
	}
	return ""
}

// watcherStateByLibrary returns the state string for the given library from a
// watcherStatus query datum.
func watcherStateByLibrary(t *testing.T, resp journeyGraphResponse, libraryID string) string {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(resp.Data["watcherStatus"], &rows); err != nil {
		t.Fatalf("decode watcherStatus: %v\n%s", err, resp.body)
	}
	for _, row := range rows {
		if row["libraryID"] == libraryID {
			value, _ := row["state"].(string)
			return value
		}
	}
	return ""
}

// hasJobEvent reports whether the audit log carries an event of the given type.
func hasJobEvent(events []store.JobEvent, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

// countJobKind counts jobs of the given kind.
func countJobKind(jobs []store.Job, kind store.JobKind) int {
	count := 0
	for _, job := range jobs {
		if job.Kind == kind {
			count++
		}
	}
	return count
}

func realScanScheduleSpec(st *store.Store, libraryID store.LibraryID) schedule.Spec {
	return schedule.Spec{
		ID:             string(schedule.ScanScheduleID(libraryID)),
		JobKind:        scanpkg.JobKind,
		DefaultTrigger: schedule.Every(12, schedule.Hours),
		BuildJobs: func(ctx context.Context, now time.Time) ([]store.Job, error) {
			roots, err := st.LibraryRoots(ctx, libraryID)
			if err != nil {
				return nil, err
			}
			out := make([]store.Job, 0, len(roots))
			for _, root := range roots {
				job, err := scanpkg.NewJob(scanpkg.JobRequest{LibraryID: libraryID, Root: root, CreatedAt: now})
				if err != nil {
					return nil, err
				}
				out = append(out, job)
			}
			return out, nil
		},
	}
}

func scanJobForLibrary(jobs []store.Job, libraryID store.LibraryID) store.Job {
	needle := `"library_id":"` + string(libraryID) + `"`
	for _, job := range jobs {
		if job.Kind == scanpkg.JobKind && strings.Contains(string(job.Payload), needle) {
			return job
		}
	}
	return store.Job{}
}
