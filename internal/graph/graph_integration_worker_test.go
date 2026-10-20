//go:build !beamfall_appstore

package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestPluginWorkerSurfaceDescriptorsServerDeclared proves the Integration Worker
// Plugin Surface renders server-declared data (grants with risk + lifecycle
// action ids) — never downloaded plugin UI. The client gets data; the wire
// carries no JS/HTML/views.
func TestPluginWorkerSurfaceDescriptorsServerDeclared(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query W($pluginID: String!){ pluginWorker(pluginID:$pluginID){ pluginID state installed grants{ kind highRisk approved } lifecycleActions } }","variables":{"pluginID":"demo"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("pluginWorker errors = %#v", errs)
	}
	var info PluginWorkerInfo
	mustUnmarshal(t, data["pluginWorker"], &info)
	if info.PluginID != "demo" || info.Installed {
		t.Fatalf("expected demo, not installed; got %+v", info)
	}
	if len(info.LifecycleActions) == 0 {
		t.Fatal("server must declare the closed lifecycle action set")
	}
	// The descriptor is pure data and advertises only implemented actions.
	wantActions := []string{"apply-update", "disable", "run-action"}
	if !slices.Equal(info.LifecycleActions, wantActions) {
		t.Fatalf("executable lifecycle actions = %v, want apply/disable/run", info.LifecycleActions)
	}
}

func TestAdvertisedLifecycleActionsDispatchAndExecute(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	installVerifiedGraphWorker(t, st, handler, "demo")
	runner := &fakeActionRunner{}
	lifecycle := &recordingIntegrationLifecycle{}
	handler.SetIntegrationActionRunner(runner)
	handler.integrationLifecycle = lifecycle
	if err := st.SetPluginGrant(context.Background(), store.PluginGrantRow{
		PluginID: "demo",
		Kind:     "declared-outbound",
		Approved: true,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query W($pluginID: String!){ pluginWorker(pluginID:$pluginID){ lifecycleActions } }","variables":{"pluginID":"demo"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("pluginWorker errors = %#v", errs)
	}
	var info PluginWorkerInfo
	mustUnmarshal(t, data["pluginWorker"], &info)
	handlers := pluginLifecycleActionHandlers()
	if len(info.LifecycleActions) != len(handlers) {
		t.Fatalf("advertised actions %v do not match %d executing handlers", info.LifecycleActions, len(handlers))
	}

	for _, action := range info.LifecycleActions {
		if _, ok := handlers[action]; !ok {
			t.Fatalf("advertised lifecycle action %q has no executing handler", action)
		}
		query := `{"query":"mutation R($pluginID: String!, $action: String!, $actionID: String){ runPluginLifecycleAction(pluginID:$pluginID, action:$action, actionID:$actionID){ pluginID state } }","variables":{"pluginID":"demo","action":"` + action + `","actionID":"test-connection"}}`
		_, errs := post(t, handler, query, authorizeManager)
		if len(errs) != 0 {
			t.Fatalf("advertised lifecycle action %q was not dispatchable: %#v", action, errs)
		}
		switch action {
		case integrationhost.ActionApplyUpdate:
			if lifecycle.calls != 1 || lifecycle.pluginID != "demo" {
				t.Fatalf("apply-update effect = calls %d plugin %q", lifecycle.calls, lifecycle.pluginID)
			}
		case integrationhost.ActionDisable:
			worker, ok, err := st.IntegrationWorker(context.Background(), "demo")
			if err != nil || !ok || worker.State != "disabled" {
				t.Fatalf("disable effect = worker %+v ok %v err %v", worker, ok, err)
			}
		case integrationhost.ActionRunAction:
			if runner.calls != 1 || runner.pluginID != "demo" || runner.actionID != "test-connection" {
				t.Fatalf("run-action effect = calls %d plugin %q action %q", runner.calls, runner.pluginID, runner.actionID)
			}
		default:
			t.Fatalf("advertised lifecycle action %q has no asserted effect", action)
		}
	}
}

type recordingIntegrationLifecycle struct {
	pluginID string
	calls    int
}

func (r *recordingIntegrationLifecycle) ApplyUpdate(_ context.Context, pluginID string) error {
	r.pluginID = pluginID
	r.calls++
	return nil
}

// TestApprovePluginPermissionGatesHighRisk proves the Console approval mutation
// persists a grant and that a conflicting Reveal-scope binding is refused.
func TestApprovePluginPermission(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	installVerifiedGraphWorker(t, st, handler, "demo")
	openManagerAdminSession(t, st)

	// Approve read-catalog bound to all Libraries.
	data, errs := post(t, handler,
		`{"query":"mutation A($pluginID: String!, $kind: String!, $approved: Boolean!, $allLibraries: Boolean!){ approvePluginPermission(pluginID:$pluginID, kind:$kind, approved:$approved, allLibraries:$allLibraries){ grants{ kind approved allLibraries } } }","variables":{"pluginID":"demo","kind":"read-catalog","approved":true,"allLibraries":true}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("approvePluginPermission errors = %#v", errs)
	}
	var info PluginWorkerInfo
	mustUnmarshal(t, data["approvePluginPermission"], &info)
	found := false
	for _, g := range info.Grants {
		if g.Kind == "read-catalog" {
			found = true
			if !g.Approved || !g.AllLibraries {
				t.Fatalf("grant not approved/all-libraries: %+v", g)
			}
		}
	}
	if !found {
		t.Fatal("read-catalog grant not persisted")
	}

	// A grant binding BOTH a Profile and all-Libraries is refused (Reveal-bypass guard).
	_, errs = post(t, handler,
		`{"query":"mutation A($pluginID: String!, $kind: String!, $approved: Boolean!, $allLibraries: Boolean!, $boundProfileID: String!){ approvePluginPermission(pluginID:$pluginID, kind:$kind, approved:$approved, allLibraries:$allLibraries, boundProfileID:$boundProfileID){ grants{ kind } } }","variables":{"pluginID":"demo","kind":"read-catalog","approved":true,"allLibraries":true,"boundProfileID":"p1"}}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("a grant binding both a Profile and all-Libraries must be refused")
	}
}

func TestApprovePluginPermissionRejectsKnownButUndeclaredPermission(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	// The mutable registry declares read-catalog, but the verified active
	// artifact does not. Eligibility must follow the artifact.
	installVerifiedGraphWorker(t, st, handler, "demo", integrationhost.PermReadCatalog)
	openManagerAdminSession(t, st)

	_, errs := post(t, handler,
		`{"query":"mutation A($pluginID: String!, $kind: String!, $approved: Boolean!){ approvePluginPermission(pluginID:$pluginID, kind:$kind, approved:$approved){ grants{ kind } } }","variables":{"pluginID":"demo","kind":"read-catalog","approved":true}}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("known but manifest-undeclared permission was approved")
	}
	grants, err := st.PluginGrants(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 0 {
		t.Fatalf("undeclared grant persisted: %+v", grants)
	}
}

func TestIntegrationMutationAuditFailureLeavesStateIneffective(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	installVerifiedGraphWorker(t, st, handler, "demo")
	handler.mutationAuditAppend = func(context.Context, store.ActivityEntry) error {
		return errors.New("audit unavailable")
	}
	openManagerAdminSession(t, st)

	_, errs := post(t, handler,
		`{"query":"mutation A($pluginID: String!, $kind: String!, $approved: Boolean!){ approvePluginPermission(pluginID:$pluginID, kind:$kind, approved:$approved){ grants{ kind } } }","variables":{"pluginID":"demo","kind":"read-catalog","approved":true}}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("grant mutation succeeded without durable audit")
	}
	grants, err := st.PluginGrants(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 0 {
		t.Fatalf("audit failure persisted grants: %+v", grants)
	}
	_, errs = post(t, handler,
		`{"query":"mutation S($pluginID: String!, $kind: String!, $location: String!, $pinKind: String!, $pin: String!){ setPluginUpdateSource(pluginID:$pluginID, kind:$kind, location:$location, pinKind:$pinKind, pin:$pin){ pluginID } }","variables":{"pluginID":"demo","kind":"folder","location":"/srv/plugins","pinKind":"hash","pin":"abc123"}}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("update-source mutation succeeded without durable audit")
	}
	if _, ok, err := st.PluginUpdateSource(context.Background(), "demo"); err != nil || ok {
		t.Fatalf("audit failure update source = ok %v err %v", ok, err)
	}
}

// TestSetPluginUpdateSource proves the per-plugin Update Source persists and
// reads back, and that an invalid kind/pin vocabulary is refused (ADR-0058: an
// operator-configured source pinned by hash or signing key, not a marketplace).
func TestSetPluginUpdateSource(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"mutation S($pluginID: String!, $kind: String!, $location: String!, $pinKind: String!, $pin: String!){ setPluginUpdateSource(pluginID:$pluginID, kind:$kind, location:$location, pinKind:$pinKind, pin:$pin){ updateSource{ kind location pinKind pin } } }","variables":{"pluginID":"demo","kind":"folder","location":"/srv/plugins","pinKind":"hash","pin":"abc123"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setPluginUpdateSource errors = %#v", errs)
	}
	var info PluginWorkerInfo
	mustUnmarshal(t, data["setPluginUpdateSource"], &info)
	if info.UpdateSource == nil || info.UpdateSource.Kind != "folder" || info.UpdateSource.PinKind != "hash" {
		t.Fatalf("update source not persisted: %+v", info.UpdateSource)
	}

	// An invalid pin kind is refused.
	_, errs = post(t, handler,
		`{"query":"mutation S($pluginID: String!, $kind: String!, $location: String!, $pinKind: String!, $pin: String!){ setPluginUpdateSource(pluginID:$pluginID, kind:$kind, location:$location, pinKind:$pinKind, pin:$pin){ updateSource{ kind } } }","variables":{"pluginID":"demo","kind":"folder","location":"/srv/plugins","pinKind":"telepathy","pin":"x"}}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("an invalid pin kind must be refused")
	}
}

// TestRunPluginLifecycleActionClosed proves an unknown lifecycle action is
// refused — a Manager cannot invent server actions.
func TestRunPluginLifecycleActionClosed(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	_, errs := post(t, handler,
		`{"query":"mutation R($pluginID: String!, $action: String!){ runPluginLifecycleAction(pluginID:$pluginID, action:$action){ state } }","variables":{"pluginID":"demo","action":"rm-rf"}}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("unknown lifecycle action must be refused")
	}
}

func TestRunPluginDescriptorActionRequiresApprovedPermission(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	installVerifiedGraphWorker(t, st, handler, "demo")
	runner := &fakeActionRunner{}
	handler.SetIntegrationActionRunner(runner)
	openManagerAdminSession(t, st)

	_, errs := post(t, handler,
		`{"query":"mutation R($pluginID: String!, $action: String!, $actionID: String!){ runPluginLifecycleAction(pluginID:$pluginID, action:$action, actionID:$actionID){ state } }","variables":{"pluginID":"demo","action":"run-action","actionID":"test-connection"}}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("run-action without required permission grant must be refused")
	}
	if runner.calls != 0 {
		t.Fatalf("run-action dispatched without grant: %d calls", runner.calls)
	}

	if err := st.SetPluginGrant(context.Background(), store.PluginGrantRow{
		PluginID: "demo",
		Kind:     "declared-outbound",
		Approved: true,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetPluginGrant: %v", err)
	}
	data, errs := post(t, handler,
		`{"query":"mutation R($pluginID: String!, $action: String!, $actionID: String!){ runPluginLifecycleAction(pluginID:$pluginID, action:$action, actionID:$actionID){ pluginID state } }","variables":{"pluginID":"demo","action":"run-action","actionID":"test-connection"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("run-action with grant errors = %#v", errs)
	}
	var info PluginWorkerInfo
	mustUnmarshal(t, data["runPluginLifecycleAction"], &info)
	if info.PluginID != "demo" {
		t.Fatalf("run-action plugin = %q", info.PluginID)
	}
	if runner.pluginID != "demo" || runner.actionID != "test-connection" || runner.calls != 1 {
		t.Fatalf("runner call = plugin %q action %q calls %d", runner.pluginID, runner.actionID, runner.calls)
	}
	entries := allActivity(t, st)
	found := false
	for _, entry := range entries {
		if entry.Action == actionPluginRunAction && entry.TargetType == "plugin" && entry.TargetID == "demo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("run-action audit missing from %#v", entries)
	}
}

func installVerifiedGraphWorker(t *testing.T, st *store.Store, handler *Handler, pluginID string, omitPermissions ...string) {
	t.Helper()
	bundle, ok := handler.plugins.Bundle(plugin.ID(pluginID))
	if !ok {
		t.Fatalf("plugin %q missing from registry", pluginID)
	}
	base, err := os.ReadFile(filepath.Join("..", "plugin", "testdata", "package-integration.json")) // #nosec G304 -- fixed repository test fixture path.
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := plugin.ParsePackageManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ID = pluginID
	manifest.Version = bundle.Version
	omitted := map[string]bool{}
	for _, kind := range omitPermissions {
		omitted[kind] = true
	}
	manifest.Permissions = nil
	for _, permission := range bundle.Permissions {
		if !omitted[permission.Kind] {
			manifest.Permissions = append(manifest.Permissions, permission)
		}
	}
	manifest.Actions = bundle.Actions
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	artifactBytes := []byte("verified-graph-worker-" + pluginID)
	sourcePath := filepath.Join(t.TempDir(), pluginID)
	if err := os.WriteFile(sourcePath, artifactBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifactBytes)
	installed, err := integrationhost.InstallVerifiedUpdateCandidate(st.IntegrationArtifactRoot(), integrationhost.Artifact{
		PluginID: pluginID, Version: bundle.Version, Path: sourcePath, OS: runtime.GOOS, Arch: runtime.GOARCH,
	}, raw, integrationhost.Pin{Kind: integrationhost.PinHash, Hash: hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetIntegrationWorker(t.Context(), store.IntegrationWorkerRow{
		PluginID: pluginID, Version: bundle.Version, ArtifactSHA256: installed.VerifiedSHA256, State: "installed",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

type fakeActionRunner struct {
	pluginID string
	actionID string
	calls    int
}

func (f *fakeActionRunner) RunAction(_ context.Context, pluginID, actionID string) error {
	f.pluginID = pluginID
	f.actionID = actionID
	f.calls++
	return nil
}

func TestDeclaredDormantLifecycleActionsFailExplicitly(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	for _, action := range []string{"check-update", "stage-update", "restart-worker", "retry-dead-letter", "test-connection"} {
		_, errs := post(t, handler,
			`{"query":"mutation R($pluginID: String!, $action: String!){ runPluginLifecycleAction(pluginID:$pluginID, action:$action){ state } }","variables":{"pluginID":"demo","action":"`+action+`"}}`,
			authorizeManager)
		if len(errs) == 0 {
			t.Fatalf("%s must fail explicitly until its runtime controller is wired", action)
		}
	}
}
