package graph

import (
	"encoding/json"
	"os"
	"testing"
	"time"

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
