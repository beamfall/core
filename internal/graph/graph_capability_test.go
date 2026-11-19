//go:build !beamfall_appstore && !beamfall_no_powertools

package graph

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/workflow/convert"
)

func TestConversionCapabilityBlocksRetry(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	now := time.Now().UTC()
	job, err := convert.NewConvertJob("library-local", filepath.Join(t.TempDir(), "source.mp4"), "h264-mp4", store.LibraryFilter{AllLibraries: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	job, err = st.EnqueueJob(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CancelJob(t.Context(), job.ID, now); err != nil {
		t.Fatal(err)
	}

	setWorkflowCapability(t, handler, "conversion", false)
	retry := workflowMutation(t, `mutation($id:String!) { jobRetry(id:$id) { id kind status } }`,
		map[string]any{"id": job.ID})
	_, errs := post(t, handler, retry, func(req *http.Request) {
		*req = *req.WithContext(l10n.WithLocale(req.Context(), "de"))
		authorizeManager(req)
	})
	assertCapabilityDisabled(t, errs, "Dieses Werkzeug ist unter Konsole → Optionale Funktionen deaktiviert.")
	if jobs, err := st.ListJobs(t.Context()); err != nil || len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].Status != store.JobStatusCanceled {
		t.Fatalf("jobs after disabled retry = %v, %v", jobs, err)
	}

	setWorkflowCapability(t, handler, "conversion", true)
	data, errs := post(t, handler, retry, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("retry after enabling: %#v", errs)
	}
	var retried JobInfo
	mustUnmarshal(t, data["jobRetry"], &retried)
	if retried.ID == "" || retried.ID == string(job.ID) || retried.Kind != string(convert.JobKind) || retried.Status != string(store.JobStatusQueued) {
		t.Fatalf("retried job = %#v", retried)
	}
}

func TestRenameCapabilityBlocksSingleAndBatchMutations(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const libraryID = "library-local"
	if err := st.SetLibraryRoots(t.Context(), libraryID, []string{root}); err != nil {
		t.Fatal(err)
	}
	singlePath := filepath.Join(root, "single.mp4")
	batchPath := filepath.Join(root, "batch.mp4")
	importGraphFile(t, st, libraryID, singlePath, "Single", "0000000045000002")
	importGraphFile(t, st, libraryID, batchPath, "Batch", "0000000045000003")

	single := workflowMutation(t, `mutation($libraryID:String!,$path:String!,$name:String!) {
		renameManagedPath(libraryID:$libraryID,path:$path,name:$name) { path }
	}`, map[string]any{"libraryID": libraryID, "path": singlePath, "name": "single-renamed.mp4"})
	batch := workflowMutation(t, `mutation($libraryID:String!,$renames:[ManagedPathRenameInput!]!) {
		batchRenameManagedPaths(libraryID:$libraryID,renames:$renames) { affected }
	}`, map[string]any{"libraryID": libraryID, "renames": []map[string]string{
		{"path": batchPath, "name": "batch-renamed.mp4"},
	}})
	openManagerAdminSession(t, st)
	setWorkflowCapability(t, handler, "rename", false)
	if err := st.SetSessionAdminUntil(t.Context(), "session-manager", time.Time{}); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{single, batch} {
		_, errs := post(t, handler, mutation, authorizeManager)
		assertCapabilityDisabled(t, errs, "This workflow is disabled in Console → Optional features.")
	}
	for _, path := range []string{singlePath, batchPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("disabled rename altered %s: %v", path, err)
		}
	}

	openManagerAdminSession(t, st)
	setWorkflowCapability(t, handler, "rename", true)
	for _, mutation := range []string{single, batch} {
		if _, errs := post(t, handler, mutation, authorizeManager); len(errs) != 0 {
			t.Fatalf("rename after enabling: %#v", errs)
		}
	}
	for _, name := range []string{"single-renamed.mp4", "batch-renamed.mp4"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("enabled rename did not create %s: %v", name, err)
		}
	}
}

func workflowMutation(t *testing.T, query string, variables map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func setWorkflowCapability(t *testing.T, handler *Handler, id string, enabled bool) {
	t.Helper()
	body := workflowMutation(t, `mutation($capability:String!,$enabled:Boolean!) {
		setCapabilityEnabled(capability:$capability,enabled:$enabled) { id enabled }
	}`, map[string]any{"capability": id, "enabled": enabled})
	if _, errs := post(t, handler, body, authorizeManager); len(errs) != 0 {
		t.Fatalf("set %s to %t: %#v", id, enabled, errs)
	}
}

func assertCapabilityDisabled(t *testing.T, errs []graphError, message string) {
	t.Helper()
	if len(errs) != 1 || errs[0].Extensions.Code != ErrorCodeConflict ||
		errs[0].Extensions.Reason != "" || errs[0].Message != message {
		t.Fatalf("errors = %#v, want CONFLICT with message %q", errs, message)
	}
}

// TestSetCapabilityEnabledTogglesAndAudits is the POWERTOOLS-9 vertical slice:
// the Workflow-capability toggle is Admin-Session-gated, audited
// + attributed, and the flip is reflected in the next capabilityStates read.
func TestSetCapabilityEnabledTogglesAndAudits(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	ctx := t.Context()

	// Without the Admin Session window open, the toggle is refused.
	_, errs := post(t, handler,
		`{"query":"mutation T($capability: String!, $enabled: Boolean!){ setCapabilityEnabled(capability:$capability, enabled:$enabled){ id enabled } }","variables":{"capability":"conversion","enabled":false}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("toggle without admin session errors = %#v, want admin-session gate", errs)
	}

	openManagerAdminSession(t, st)

	// conversion defaults enabled — flip it off.
	data, errs := post(t, handler,
		`{"query":"mutation T($capability: String!, $enabled: Boolean!){ setCapabilityEnabled(capability:$capability, enabled:$enabled){ id kind enabled flippable reason } }","variables":{"capability":"conversion","enabled":false}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setCapabilityEnabled errors = %#v", errs)
	}
	var states []CapabilityStateInfo
	mustUnmarshal(t, data["setCapabilityEnabled"], &states)
	if enabledOf(states, "conversion") {
		t.Fatal("conversion enabled = true after disable, want false (flip not reflected)")
	}

	// The flip is reflected in a fresh capabilityStates read.
	data, errs = post(t, handler,
		`{"query":"query { capabilityStates { id kind enabled flippable reason } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("capabilityStates errors = %#v", errs)
	}
	mustUnmarshal(t, data["capabilityStates"], &states)
	if enabledOf(states, "conversion") {
		t.Fatal("capabilityStates conversion enabled = true, want false after flip")
	}

	// The flip is audited + attributed: an activity entry records the toggle with
	// the capability id and an attributed session.
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var audited *store.ActivityEntry
	for i := range entries {
		if entries[i].Action == store.ActivityCapabilityToggle {
			audited = &entries[i]
			break
		}
	}
	if audited == nil {
		t.Fatal("no capability.toggle activity entry — the flip must be audited")
	}
	if audited.TargetID != "conversion" || audited.TargetType != "capability" {
		t.Fatalf("audit entry target = (%q,%q), want (capability, conversion)", audited.TargetType, audited.TargetID)
	}
	if audited.SessionID == "" {
		t.Fatal("audit entry has no attribution (SessionID empty)")
	}
}

// TestSetCapabilityEnabledRejectsProbeAndUnknown proves a probe-derived or
// unknown capability id is a client error — probe caps are auto-detected, never
// operator-flipped, so the override table stays clean.
func TestSetCapabilityEnabledRejectsProbeAndUnknown(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	for _, id := range []string{"share-mount", "ghost"} {
		_, errs := post(t, handler,
			`{"query":"mutation T($capability: String!, $enabled: Boolean!){ setCapabilityEnabled(capability:$capability, enabled:$enabled){ id } }","variables":{"capability":"`+id+`","enabled":true}}`,
			authorizeManager)
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "non-flippable") {
			t.Fatalf("toggle %q errors = %#v, want non-flippable client error", id, errs)
		}
	}
}

// TestCapabilityStatesReportsProbeAbsent proves the probe-derived capability is
// observable as enabled:false, reason:probe-absent and is not flippable
// (ADR-0124 §5 degraded observable).
func TestCapabilityStatesReportsProbeAbsent(t *testing.T) {
	t.Setenv("BEAMFALL_PROBE_SHARE_MOUNT", "")
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { capabilityStates { id kind enabled flippable reason } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("capabilityStates errors = %#v", errs)
	}
	var states []CapabilityStateInfo
	mustUnmarshal(t, data["capabilityStates"], &states)
	var share *CapabilityStateInfo
	for i := range states {
		if states[i].ID == "share-mount" {
			share = &states[i]
		}
	}
	if share == nil {
		t.Fatal("share-mount probe capability not reported")
	}
	if share.Enabled {
		t.Fatal("share-mount enabled = true, want false (probe absent)")
	}
	if share.Reason != "probe-absent" {
		t.Fatalf("share-mount reason = %q, want probe-absent", share.Reason)
	}
	if share.Flippable {
		t.Fatal("share-mount flippable = true, want false (probe caps never operator-flipped)")
	}
}

func enabledOf(states []CapabilityStateInfo, id string) bool {
	for _, s := range states {
		if s.ID == id {
			return s.Enabled
		}
	}
	return false
}

func TestClientContractRenderingProfileWireProjection(t *testing.T) {
	got, err := json.Marshal(ClientContractWireProjection{RenderingProfile: RenderingCapabilityProfile{
		VocabularyVersion: "1.0.0",
		Capabilities:      []string{"layout-browse", "section-grid"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"renderingProfile":{"vocabularyVersion":"1.0.0","capabilities":["layout-browse","section-grid"]}}`
	if string(got) != want {
		t.Fatalf("Client Contract wire projection = %s, want %s", got, want)
	}
}
