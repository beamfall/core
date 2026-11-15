package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/store"
)

type offlinePackBuildView struct {
	ID            string  `json:"id"`
	State         string  `json:"state"`
	Progress      float64 `json:"progress"`
	FailureReason string  `json:"failureReason"`
	ItemCount     int     `json:"itemCount"`
	ManifestToken string  `json:"manifestToken"`
}

func listOfflinePackBuilds(t *testing.T, handler *Handler) []offlinePackBuildView {
	t.Helper()
	data, errs := post(t, handler, `{"query":"query { offlinePacks { id state progress failureReason itemCount manifestToken } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("offlinePacks errors = %#v", errs)
	}
	var packs []offlinePackBuildView
	if err := json.Unmarshal(data["offlinePacks"], &packs); err != nil {
		t.Fatalf("decode offlinePacks: %v", err)
	}
	return packs
}

func offlinePackMutation(t *testing.T, handler *Handler, field, body string) (offlinePackBuildView, []graphError) {
	t.Helper()
	data, errs := post(t, handler, body, authorizeViewer)
	var view offlinePackBuildView
	if len(errs) == 0 {
		if err := json.Unmarshal(data[field], &view); err != nil {
			t.Fatalf("decode %s: %v", field, err)
		}
	}
	return view, errs
}

func queueOfflinePackBuildForTest(t *testing.T, handler *Handler) offlinePackBuildView {
	t.Helper()
	view, errs := offlinePackMutation(t, handler, "createOfflinePack",
		`{"query":"mutation { createOfflinePack { id state progress failureReason } }","variables":{"targetKind":"media","targetRef":"media-alpha","quality":"low"}}`)
	if len(errs) != 0 {
		t.Fatalf("createOfflinePack errors = %#v", errs)
	}
	return view
}

func runOfflinePackBuildJob(t *testing.T, handler *Handler) {
	t.Helper()
	if _, err := handler.jobRunner.RunOne(t.Context(), []store.JobClass{store.JobClassBackground}, time.Now().UTC()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
}

func retryOfflinePackForTest(t *testing.T, handler *Handler, id string) (offlinePackBuildView, []graphError) {
	t.Helper()
	return offlinePackMutation(t, handler, "retryOfflinePack",
		`{"query":"mutation R($id:String!){ retryOfflinePack(id:$id){ id state progress failureReason itemCount manifestToken } }","variables":{"id":"`+id+`"}}`)
}

// TestOfflinePacksListsBuildingThenReady covers CORE-1743's building and ready
// states: a queued background build is listed under its pack ID before it
// seals, and the sealed pack replaces that row once the job finishes.
func TestOfflinePacksListsBuildingThenReady(t *testing.T) {
	handler, _ := newOfflinePackHandler(t)
	created := queueOfflinePackBuildForTest(t, handler)
	if created.State != "building" || created.Progress != 0 || created.FailureReason != "" {
		t.Fatalf("created = %+v, want building at progress 0 with no reason", created)
	}
	packs := listOfflinePackBuilds(t, handler)
	if len(packs) != 1 || packs[0].ID != created.ID || packs[0].State != "building" || packs[0].ManifestToken != "" {
		t.Fatalf("offlinePacks before the job = %+v, want one building row %q without tokens", packs, created.ID)
	}
	runOfflinePackBuildJob(t, handler)
	packs = listOfflinePackBuilds(t, handler)
	if len(packs) != 1 || packs[0].ID != created.ID || packs[0].State != "ready" || packs[0].Progress != 1 ||
		packs[0].ItemCount != 1 || packs[0].ManifestToken == "" {
		t.Fatalf("offlinePacks after the job = %+v, want only the ready pack %q", packs, created.ID)
	}
}

// TestOfflinePackFailedBuildRetriesIdempotently covers CORE-1743's failed
// state and retry: a build whose source vanished is listed failed with a
// closed reason code, a retry by pack ID returns it to building exactly once,
// and the retried job seals the pack under the same ID.
func TestOfflinePackFailedBuildRetriesIdempotently(t *testing.T) {
	handler, st := newOfflinePackHandler(t)
	file, err := st.PrimaryMediaFile(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(file.Path)
	if err != nil {
		t.Fatal(err)
	}
	created := queueOfflinePackBuildForTest(t, handler)
	if err := os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	runOfflinePackBuildJob(t, handler)
	packs := listOfflinePackBuilds(t, handler)
	if len(packs) != 1 || packs[0].ID != created.ID || packs[0].State != "failed" ||
		packs[0].FailureReason != offlinepack.FailureSourceUnavailable {
		t.Fatalf("offlinePacks after a failed job = %+v, want %q failed with %q", packs, created.ID, offlinepack.FailureSourceUnavailable)
	}

	if err := os.WriteFile(file.Path, original, 0o600); err != nil { // #nosec G703 -- test fixture path from the seeded catalog under t.TempDir()
		t.Fatal(err)
	}
	for attempt := range 2 {
		retried, errs := retryOfflinePackForTest(t, handler, created.ID)
		if len(errs) != 0 {
			t.Fatalf("retry %d errors = %#v", attempt, errs)
		}
		if retried.ID != created.ID || retried.State != "building" || retried.Progress != 0 || retried.FailureReason != "" {
			t.Fatalf("retry %d = %+v, want %q building with progress and reason cleared", attempt, retried, created.ID)
		}
	}
	queued, err := st.ListJobs(t.Context(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued jobs after two retries = %d, want 1", len(queued))
	}
	runOfflinePackBuildJob(t, handler)
	packs = listOfflinePackBuilds(t, handler)
	if len(packs) != 1 || packs[0].ID != created.ID || packs[0].State != "ready" || packs[0].FailureReason != "" {
		t.Fatalf("offlinePacks after the retried job = %+v, want %q ready", packs, created.ID)
	}
	sealed, errs := retryOfflinePackForTest(t, handler, created.ID)
	if len(errs) != 0 || sealed.State != "ready" || sealed.ID != created.ID {
		t.Fatalf("retry of a sealed pack = %+v / %#v, want the ready pack", sealed, errs)
	}
}

// TestOfflinePackRetryJoinsOperatorJobRetry pins that a pack retry and an
// operator jobRetry of the same failed build share one active job, in either
// order, so two builders never run under one pack ID.
func TestOfflinePackRetryJoinsOperatorJobRetry(t *testing.T) {
	for _, operatorFirst := range []bool{true, false} {
		handler, st := newOfflinePackHandler(t)
		file, err := st.PrimaryMediaFile(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true})
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(file.Path)
		if err != nil {
			t.Fatal(err)
		}
		created := queueOfflinePackBuildForTest(t, handler)
		if err := os.Remove(file.Path); err != nil {
			t.Fatal(err)
		}
		runOfflinePackBuildJob(t, handler)
		failed, err := st.ListJobs(t.Context(), store.JobStatusFailed)
		if err != nil || len(failed) != 1 {
			t.Fatalf("failed jobs = %d / %v, want 1", len(failed), err)
		}
		if err := os.WriteFile(file.Path, original, 0o600); err != nil { // #nosec G703 -- test fixture path from the seeded catalog under t.TempDir()
			t.Fatal(err)
		}
		operatorRetry := func() {
			if _, err := st.RetryJob(t.Context(), failed[0].ID, time.Now().UTC()); err != nil {
				t.Fatalf("RetryJob: %v", err)
			}
		}
		if operatorFirst {
			operatorRetry()
		}
		if _, errs := retryOfflinePackForTest(t, handler, created.ID); len(errs) != 0 {
			t.Fatalf("operatorFirst=%v: retryOfflinePack errors = %#v", operatorFirst, errs)
		}
		if !operatorFirst {
			operatorRetry()
		}
		queued, err := st.ListJobs(t.Context(), store.JobStatusQueued)
		if err != nil {
			t.Fatal(err)
		}
		if len(queued) != 1 {
			t.Fatalf("operatorFirst=%v: queued jobs = %d, want 1", operatorFirst, len(queued))
		}
		runOfflinePackBuildJob(t, handler)
		packs := listOfflinePackBuilds(t, handler)
		if len(packs) != 1 || packs[0].ID != created.ID || packs[0].State != "ready" {
			t.Fatalf("operatorFirst=%v: offlinePacks = %+v, want %q ready", operatorFirst, packs, created.ID)
		}
	}
}

// TestOfflinePackRetryRefusesToJoinAnotherPacksJob pins that retrying a failed
// pack while a newer pack of the same selection is building does not join
// that pack's job: the retry is refused, the failed pack stays retryable, and
// it rebuilds under its own ID once the newer build finishes.
func TestOfflinePackRetryRefusesToJoinAnotherPacksJob(t *testing.T) {
	handler, st := newOfflinePackHandler(t)
	file, err := st.PrimaryMediaFile(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(file.Path)
	if err != nil {
		t.Fatal(err)
	}
	failed := queueOfflinePackBuildForTest(t, handler)
	if err := os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	runOfflinePackBuildJob(t, handler)
	if err := os.WriteFile(file.Path, original, 0o600); err != nil { // #nosec G703 -- test fixture path from the seeded catalog under t.TempDir()
		t.Fatal(err)
	}
	newer := queueOfflinePackBuildForTest(t, handler)
	if newer.ID == failed.ID {
		t.Fatalf("newer build reused the failed pack ID %q", failed.ID)
	}
	if _, errs := retryOfflinePackForTest(t, handler, failed.ID); len(errs) == 0 {
		t.Fatal("retry while another pack of the same selection is building succeeded")
	}
	states := map[string]string{}
	for _, pack := range listOfflinePackBuilds(t, handler) {
		states[pack.ID] = pack.State
	}
	if states[failed.ID] != "failed" || states[newer.ID] != "building" {
		t.Fatalf("states after the refused retry = %v, want %q failed and %q building", states, failed.ID, newer.ID)
	}
	runOfflinePackBuildJob(t, handler)
	retried, errs := retryOfflinePackForTest(t, handler, failed.ID)
	if len(errs) != 0 || retried.State != "building" {
		t.Fatalf("retry after the newer build = %+v / %#v, want building", retried, errs)
	}
	runOfflinePackBuildJob(t, handler)
	for _, pack := range listOfflinePackBuilds(t, handler) {
		if pack.State != "ready" {
			t.Fatalf("pack %q = %q after both builds, want ready", pack.ID, pack.State)
		}
	}
}

// TestOfflinePackRetryAndRevokeOfUnknownOrBuildingIDs pins the edges: an ID
// with no build is not found, a building build cannot be revoked, and revoking
// a failed build dismisses it from the list.
func TestOfflinePackRetryAndRevokeOfUnknownOrBuildingIDs(t *testing.T) {
	handler, st := newOfflinePackHandler(t)
	unknown, err := offlinepack.NewPackID()
	if err != nil {
		t.Fatal(err)
	}
	if _, errs := retryOfflinePackForTest(t, handler, string(unknown)); len(errs) == 0 {
		t.Fatal("retry of an unknown pack ID succeeded")
	}
	created := queueOfflinePackBuildForTest(t, handler)
	revokeBody := `{"query":"mutation R($id:String!){ revokeOfflinePack(id:$id){ id state } }","variables":{"id":"` + created.ID + `"}}`
	if _, errs := offlinePackMutation(t, handler, "revokeOfflinePack", revokeBody); len(errs) == 0 {
		t.Fatal("revoke of a building pack succeeded")
	}
	file, err := st.PrimaryMediaFile(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	runOfflinePackBuildJob(t, handler)
	revoked, errs := offlinePackMutation(t, handler, "revokeOfflinePack", revokeBody)
	if len(errs) != 0 || revoked.ID != created.ID || revoked.State != "revoked" {
		t.Fatalf("revoke of a failed build = %+v / %#v, want it revoked", revoked, errs)
	}
	if packs := listOfflinePackBuilds(t, handler); len(packs) != 0 {
		t.Fatalf("offlinePacks after dismissing the failed build = %+v, want none", packs)
	}
}

// failedOfflinePackBuildForTest queues a build, fails it by removing its
// source file, then restores the file so a retry can succeed.
func failedOfflinePackBuildForTest(t *testing.T, handler *Handler, st *store.Store) offlinePackBuildView {
	t.Helper()
	file, err := st.PrimaryMediaFile(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(file.Path)
	if err != nil {
		t.Fatal(err)
	}
	created := queueOfflinePackBuildForTest(t, handler)
	if err := os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	runOfflinePackBuildJob(t, handler)
	if err := os.WriteFile(file.Path, original, 0o600); err != nil { // #nosec G703 -- test fixture path from the seeded catalog under t.TempDir()
		t.Fatal(err)
	}
	if state := offlinePackBuildState(t, handler, created.ID); state != "failed" {
		t.Fatalf("build %q = %q after its source vanished, want failed", created.ID, state)
	}
	return created
}

func offlinePackBuildState(t *testing.T, handler *Handler, id string) string {
	t.Helper()
	for _, pack := range listOfflinePackBuilds(t, handler) {
		if pack.ID == id {
			return pack.State
		}
	}
	return ""
}

// activeOfflinePackBuildJobs returns the queued, running or yielded build
// jobs whose payload names the pack ID.
func activeOfflinePackBuildJobs(t *testing.T, st *store.Store, id string) []store.Job {
	t.Helper()
	var active []store.Job
	for _, status := range []store.JobStatus{store.JobStatusQueued, store.JobStatusRunning, store.JobStatusYielded} {
		jobs, err := st.ListJobs(t.Context(), status)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			var payload offlinepack.BuildJobPayload
			if job.Kind == offlinepack.BuildJobKind && json.Unmarshal(job.Payload, &payload) == nil && string(payload.PackID) == id {
				active = append(active, job)
			}
		}
	}
	return active
}

// requireBuildHasWorker fails unless the pack's build row is failed (and so
// retryable) or is building with an active job for its pack ID.
func requireBuildHasWorker(t *testing.T, handler *Handler, st *store.Store, id string) {
	t.Helper()
	switch state := offlinePackBuildState(t, handler, id); state {
	case "failed":
	case "building":
		if jobs := activeOfflinePackBuildJobs(t, st, id); len(jobs) == 0 {
			t.Fatalf("build %q is building with no queued or running job", id)
		}
	default:
		t.Fatalf("build %q = %q, want failed, or building with a job", id, state)
	}
}

// cancelAfterRestartStore cancels the request after RestartOfflinePackBuild
// commits, the window CORE-1867 found stranding a building row.
type cancelAfterRestartStore struct {
	*store.Store
	cancel    context.CancelFunc
	restarted bool
}

func (s *cancelAfterRestartStore) RestartOfflinePackBuild(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID, payload []byte, now time.Time) (bool, error) {
	restarted, err := s.Store.RestartOfflinePackBuild(ctx, id, profileID, payload, now)
	s.restarted = s.restarted || restarted
	if s.cancel != nil {
		s.cancel()
	}
	return restarted, err
}

// TestOfflinePackRetryCancelledAfterRestartKeepsAJob covers CORE-1867: a
// retry request cancelled after its restart commits still ends with a queued
// job for the pack, never a building row with no job.
func TestOfflinePackRetryCancelledAfterRestartKeepsAJob(t *testing.T) {
	var wrapped *cancelAfterRestartStore
	handler, st, _, _ := newOfflinePackHandlerWith(t, func(st *store.Store) offlinepack.Store {
		wrapped = &cancelAfterRestartStore{Store: st}
		return wrapped
	})
	created := failedOfflinePackBuildForTest(t, handler, st)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wrapped.cancel = cancel
	body := `{"query":"mutation R($id:String!){ retryOfflinePack(id:$id){ id state } }","variables":{"id":"` + created.ID + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body))).WithContext(ctx)
	authorizeViewer(req)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	wrapped.cancel = nil
	if !wrapped.restarted {
		t.Fatal("the retry never restarted the failed build")
	}
	requireBuildHasWorker(t, handler, st, created.ID)
	if jobs := activeOfflinePackBuildJobs(t, st, created.ID); len(jobs) != 1 {
		t.Fatalf("active jobs for %q after the cancelled retry = %d, want 1", created.ID, len(jobs))
	}
	runOfflinePackBuildJob(t, handler)
	if state := offlinePackBuildState(t, handler, created.ID); state != "ready" {
		t.Fatalf("build %q after its job = %q, want ready", created.ID, state)
	}
}

func cancelJobAsOperator(t *testing.T, handler *Handler, id store.JobID) {
	t.Helper()
	body := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($id:String!){ jobCancel(id:$id){ id status } }","variables":{"id":"`+string(id)+`"}}`)
	var resp struct {
		Data struct {
			JobCancel JobInfo `json:"jobCancel"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode jobCancel: %v (%s)", err, body)
	}
	if len(resp.Errors) != 0 || resp.Data.JobCancel.Status != string(store.JobStatusCanceled) {
		t.Fatalf("jobCancel = %#v errors=%#v", resp.Data.JobCancel, resp.Errors)
	}
}

// TestOfflinePackOperatorCancelFailsTheBuild covers CORE-1866: an operator
// jobCancel of a queued build job lists the build failed, a retry queues a
// fresh job for it, and once that job is cancelled too the build can be
// dismissed by revoking it.
func TestOfflinePackOperatorCancelFailsTheBuild(t *testing.T) {
	handler, st := newOfflinePackHandler(t)
	openManagerAdminSession(t, st)
	created := queueOfflinePackBuildForTest(t, handler)
	queued := activeOfflinePackBuildJobs(t, st, created.ID)
	if len(queued) != 1 {
		t.Fatalf("active jobs for %q = %d, want 1", created.ID, len(queued))
	}

	cancelJobAsOperator(t, handler, queued[0].ID)
	packs := listOfflinePackBuilds(t, handler)
	if len(packs) != 1 || packs[0].ID != created.ID || packs[0].State != "failed" || packs[0].FailureReason != offlinepack.FailureBuild {
		t.Fatalf("offlinePacks after jobCancel = %+v, want %q failed with %q", packs, created.ID, offlinepack.FailureBuild)
	}

	retried, errs := retryOfflinePackForTest(t, handler, created.ID)
	if len(errs) != 0 || retried.State != "building" {
		t.Fatalf("retry after jobCancel = %+v / %#v, want building", retried, errs)
	}
	requeued := activeOfflinePackBuildJobs(t, st, created.ID)
	if len(requeued) != 1 || requeued[0].ID == queued[0].ID || requeued[0].Status != store.JobStatusQueued {
		t.Fatalf("active jobs after the retry = %+v, want one new queued job", requeued)
	}

	cancelJobAsOperator(t, handler, requeued[0].ID)
	if state := offlinePackBuildState(t, handler, created.ID); state != "failed" {
		t.Fatalf("build %q after the second jobCancel = %q, want failed", created.ID, state)
	}
	revokeBody := `{"query":"mutation R($id:String!){ revokeOfflinePack(id:$id){ id state } }","variables":{"id":"` + created.ID + `"}}`
	revoked, errs := offlinePackMutation(t, handler, "revokeOfflinePack", revokeBody)
	if len(errs) != 0 || revoked.ID != created.ID || revoked.State != "revoked" {
		t.Fatalf("revoke after jobCancel = %+v / %#v, want it revoked", revoked, errs)
	}
	if packs := listOfflinePackBuilds(t, handler); len(packs) != 0 {
		t.Fatalf("offlinePacks after dismissing = %+v, want none", packs)
	}
}

// TestOfflinePackRetryWhileFailedJobIsFinishing covers CORE-1865: a retry
// made after the build wrote failed but before its job turned terminal must
// not join that dying job. The retry is refused, the row stays failed, and a
// later retry queues a job that seals the pack.
func TestOfflinePackRetryWhileFailedJobIsFinishing(t *testing.T) {
	handler, st, registry, svc := newOfflinePackHandlerWith(t, nil)
	file, err := st.PrimaryMediaFile(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(file.Path)
	if err != nil {
		t.Fatal(err)
	}
	created := queueOfflinePackBuildForTest(t, handler)
	if err := os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	var windowRetry offlinePackBuildView
	var windowErrs []graphError
	retriedInWindow := false
	registry.Register(offlinepack.BuildJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		_, err := svc.HandleBuildJob(ctx, job.Payload)
		if err != nil && !retriedInWindow {
			// The build row is failed now; the job is still running.
			retriedInWindow = true
			if writeErr := os.WriteFile(file.Path, original, 0o600); writeErr != nil { // #nosec G703 -- test fixture path from the seeded catalog under t.TempDir()
				t.Error(writeErr)
			}
			windowRetry, windowErrs = retryOfflinePackForTest(t, handler, created.ID)
		}
		return jobs.Result{}, err
	})
	runOfflinePackBuildJob(t, handler)
	if !retriedInWindow {
		t.Fatal("the build job did not fail, so no retry ran in the window")
	}
	if len(windowErrs) == 0 && windowRetry.State == "building" {
		t.Fatalf("retry while the failed job was finishing = %+v, want it refused rather than building", windowRetry)
	}
	requireBuildHasWorker(t, handler, st, created.ID)

	retried, errs := retryOfflinePackForTest(t, handler, created.ID)
	if len(errs) != 0 || retried.State != "building" {
		t.Fatalf("retry after the job ended = %+v / %#v, want building", retried, errs)
	}
	requireBuildHasWorker(t, handler, st, created.ID)
	runOfflinePackBuildJob(t, handler)
	if state := offlinePackBuildState(t, handler, created.ID); state != "ready" {
		t.Fatalf("build %q after the retried job = %q, want ready", created.ID, state)
	}
}
