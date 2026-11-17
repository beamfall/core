package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

const refreshLibraryQuery = `mutation RefreshLibraryMetadata($libraryID: String!) { refreshLibraryMetadata(libraryID: $libraryID) { id kind status target { libraryID libraryTitle } } }`

// requestLibraryRefresh calls refreshLibraryMetadata as the admin manager.
func requestLibraryRefresh(t *testing.T, handler *Handler, libraryID string) JobInfo {
	t.Helper()
	var job JobInfo
	execGraph(t, handler, authorizeManager, refreshLibraryQuery, map[string]any{"libraryID": libraryID}, "refreshLibraryMetadata", &job)
	return job
}

// TestRefreshLibraryMetadataQueuesOneJobPerLibrary covers job creation, the
// Operations target, dedupe and the mutation's authority (CORE-1136).
func TestRefreshLibraryMetadataQueuesOneJobPerLibrary(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)

	// Without an Admin Session the manager is refused and nothing is queued.
	resp := postGraphJSON(t, handler, authorizeManager, refreshLibraryQuery, map[string]any{"libraryID": "library-local"})
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("manager without admin session errors = %+v", resp.Errors)
	}
	resp = postGraphJSON(t, handler, authorizeViewer, refreshLibraryQuery, map[string]any{"libraryID": "library-local"})
	if len(resp.Errors) == 0 {
		t.Fatalf("viewer refresh was not refused")
	}
	assertNoRefreshJobs(t, st)

	openManagerAdminSession(t, st)
	first := requestLibraryRefresh(t, handler, "library-local")
	if first.ID == "" || first.Kind != string(identify.RefreshJobKind) || first.Status != string(store.JobStatusQueued) {
		t.Fatalf("refreshLibraryMetadata = %+v", first)
	}
	if first.Target == nil || first.Target.LibraryID != "library-local" || first.Target.LibraryTitle != "Local Library" {
		t.Fatalf("job target = %+v, want the Library", first.Target)
	}
	if second := requestLibraryRefresh(t, handler, "library-local"); second.ID != first.ID {
		t.Fatalf("second request job = %q, want the active job %q", second.ID, first.ID)
	}

	// A Library the session cannot see is unknown, as is a missing one.
	for _, libraryID := range []string{"library-archive", "library-missing", ""} {
		resp := postGraphJSON(t, handler, authorizeManager, refreshLibraryQuery, map[string]any{"libraryID": libraryID})
		if len(resp.Errors) == 0 {
			t.Fatalf("refresh of %q was not refused", libraryID)
		}
	}

	// Once the job is finished a new request queues a fresh one.
	if err := st.CancelJob(context.Background(), store.JobID(first.ID), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if again := requestLibraryRefresh(t, handler, "library-local"); again.ID == first.ID {
		t.Fatalf("request after cancel reused the canceled job %q", again.ID)
	}
}

// TestRefreshLibraryMetadataJobKeepsLocksAndReportsProgress runs the job
// through the job registry across a yield: identified items are refreshed
// with locked fields kept, items that cannot be refreshed are skipped, a
// trashed item is not walked, and progress counts every live item.
func TestRefreshLibraryMetadataJobKeepsLocksAndReportsProgress(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	handler.identify.SetProvenance(st)
	openManagerAdminSession(t, st)
	ctx := context.Background()
	everything := store.LibraryFilter{Revealed: true, AllLibraries: true}

	// media-alpha: identified, then its title is locked by an edit and its
	// unlocked summary drifts from the provider's value.
	execGraph(t, handler, authorizeManager, `mutation IdentifyApply($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { identifyApply(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id } }`,
		map[string]any{"mediaID": "media-alpha", "pluginID": "demo", "scraperID": "lookup", "candidateID": "42"}, "identifyApply", &struct{}{})
	var edited MediaMetadataInfo
	execGraph(t, handler, authorizeManager, `mutation { editMediaMetadata { mediaID title summary } }`,
		map[string]any{"mediaID": "media-alpha", "title": "Admin Title", "summary": "Drifted."}, "editMediaMetadata", &edited)
	if _, err := st.SetMediaFieldLock(ctx, "media-alpha", "summary", false, everything); err != nil {
		t.Fatal(err)
	}

	// Eleven unidentified items push the walk past one batch.
	for i := range 11 {
		importRefreshMedia(t, st, fmt.Sprintf("r%02d", i))
	}
	// An item whose plugin is gone is skipped, not failed.
	gone := importRefreshMedia(t, st, "gone")
	if err := st.SetMediaMetadataProvenance(ctx, gone, "retired-plugin", "lookup", "7"); err != nil {
		t.Fatal(err)
	}
	// A trashed item is not part of the walk.
	trashed := importRefreshMedia(t, st, "trashed")
	if err := st.TrashMedia(ctx, trashed, everything); err != nil {
		t.Fatal(err)
	}
	const live = 13 // media-alpha + 11 unidentified + the retired-plugin item

	job := requestLibraryRefresh(t, handler, "library-local")
	registry := jobs.NewRegistry(st, "refresh-test-worker")
	registry.Register(identify.RefreshJobKind, identify.RefreshJobHandler(st, handler.identify, 0))
	classes := []store.JobClass{store.JobClassBackground}

	// First claim: one batch, then a yield with progress recorded.
	if _, err := registry.RunOne(ctx, classes, time.Now().UTC()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	stored, err := st.Job(ctx, store.JobID(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != store.JobStatusYielded {
		t.Fatalf("after one batch status = %q, want yielded", stored.Status)
	}
	if stored.Progress.Current != 10 || stored.Progress.Total != live || stored.Progress.Unit != "items" || stored.Progress.Message != identify.RefreshJobProgressMessage {
		t.Fatalf("progress after one batch = %+v", stored.Progress)
	}

	// Resume until done.
	for range 5 {
		if _, err := registry.RunOne(ctx, classes, time.Now().UTC().Add(time.Second)); err != nil {
			if errors.Is(err, store.ErrNoRunnableJob) {
				break
			}
			t.Fatalf("resume: %v", err)
		}
	}
	stored, err = st.Job(ctx, store.JobID(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != store.JobStatusCompleted {
		t.Fatalf("final status = %q, want completed", stored.Status)
	}
	if stored.Progress.Current != live || stored.Progress.Total != live {
		t.Fatalf("final progress = %+v, want %d of %d", stored.Progress, live, live)
	}
	var checkpoint struct {
		Refreshed int `json:"refreshed"`
		Skipped   int `json:"skipped"`
		Failed    int `json:"failed"`
	}
	if err := json.Unmarshal(stored.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Refreshed != 1 || checkpoint.Skipped != 12 || checkpoint.Failed != 0 {
		t.Fatalf("checkpoint = %+v, want 1 refreshed and 12 skipped", checkpoint)
	}

	media, err := st.Media(ctx, "media-alpha", everything)
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "Admin Title" {
		t.Fatalf("locked title after refresh = %q", media.Title)
	}
	if media.Summary != "A classic." || media.Year != 1977 {
		t.Fatalf("unlocked fields after refresh = %q / %d, want the provider's values", media.Summary, media.Year)
	}
}

// TestRefreshLibraryMetadataCancelStopsTheWalk cancels a claimed refresh
// through jobCancel: the handler's per-item lease check refuses before the
// first provider call, so no item is refreshed and the job stays canceled.
func TestRefreshLibraryMetadataCancelStopsTheWalk(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	handler.identify.SetProvenance(st)
	openManagerAdminSession(t, st)
	ctx := context.Background()
	everything := store.LibraryFilter{Revealed: true, AllLibraries: true}

	execGraph(t, handler, authorizeManager, `mutation IdentifyApply($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { identifyApply(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id } }`,
		map[string]any{"mediaID": "media-alpha", "pluginID": "demo", "scraperID": "lookup", "candidateID": "42"}, "identifyApply", &struct{}{})
	var edited MediaMetadataInfo
	execGraph(t, handler, authorizeManager, `mutation { editMediaMetadata { mediaID summary } }`,
		map[string]any{"mediaID": "media-alpha", "summary": "Drifted."}, "editMediaMetadata", &edited)
	if _, err := st.SetMediaFieldLock(ctx, "media-alpha", "summary", false, everything); err != nil {
		t.Fatal(err)
	}

	job := requestLibraryRefresh(t, handler, "library-local")
	claimed, err := st.ClaimNextJob(ctx, "refresh-test-worker", []store.JobClass{store.JobClassBackground}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if string(claimed.ID) != job.ID {
		t.Fatalf("claimed %q, want %q", claimed.ID, job.ID)
	}
	var canceled JobInfo
	execGraph(t, handler, authorizeManager, `mutation JobCancel($id: String!) { jobCancel(id: $id) { id status } }`,
		map[string]any{"id": job.ID}, "jobCancel", &canceled)
	if canceled.Status != string(store.JobStatusCanceled) {
		t.Fatalf("jobCancel = %+v", canceled)
	}

	_, err = identify.RefreshJobHandler(st, handler.identify, 0)(ctx, claimed)
	if !errors.Is(err, store.ErrJobLeaseLost) {
		t.Fatalf("handler after cancel err = %v, want lease lost", err)
	}
	media, err := st.Media(ctx, "media-alpha", everything)
	if err != nil {
		t.Fatal(err)
	}
	if media.Summary != "Drifted." {
		t.Fatalf("canceled refresh still refreshed media-alpha: summary %q", media.Summary)
	}
	stored, err := st.Job(ctx, store.JobID(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != store.JobStatusCanceled {
		t.Fatalf("status after cancel = %q", stored.Status)
	}
}

// importRefreshMedia adds an unidentified video to library-local; name keeps
// the content fingerprint, and so the Media id, distinct.
func importRefreshMedia(t *testing.T, st *store.Store, name string) store.MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: "library-local", Path: "/refresh/" + name + ".mp4", Title: "Refresh " + name,
		Container: "mp4", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: "fp-" + name,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile %s: %v", name, err)
	}
	return media.ID
}

func assertNoRefreshJobs(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.ClaimNextJob(context.Background(), "probe", []store.JobClass{store.JobClassBackground}, time.Now().UTC()); !errors.Is(err, store.ErrNoRunnableJob) {
		t.Fatalf("a job was queued: %v", err)
	}
}
