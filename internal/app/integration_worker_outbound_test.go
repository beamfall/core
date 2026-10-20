package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestIntegrationHostDepsBuildsHost proves IntegrationHostDeps + the lifecycle
// wire a usable Host from *store.Store + the 0600 secret store, and that a
// granted read-catalog reaches the store catalog read through the Reveal filter.
func TestIntegrationHostDepsBuildsHost(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	dir := t.TempDir()
	secrets := secretFiles{dir: filepath.Join(dir, "secrets")}
	manifestBinding := strings.Repeat("a", 64)

	deps := IntegrationHostDeps(st, secrets, integrationLifecycle(st))
	grants := integrationGrantStore{st: st, manifestBinding: manifestBinding, declaredPermissions: []string{
		integrationhost.PermReadCatalog,
		integrationhost.PermStatusWrite,
		integrationhost.PermProfileLink,
	}}
	if err := grants.SetGrant(ctx, integrationhost.Grant{
		PluginID: "p", Kind: integrationhost.PermReadCatalog, Approved: true, AllLibraries: true,
	}, time.Now()); err != nil {
		t.Fatalf("set grant: %v", err)
	}
	if err := grants.SetGrantApprovalBinding(ctx, "p", integrationhost.PermReadCatalog, manifestBinding); err != nil {
		t.Fatalf("bind read-catalog approval: %v", err)
	}
	host := integrationhost.NewHost("p", deps, grants, nil)

	// A granted, approved read-catalog reaches the store catalog read (no panic,
	// no permission-denied) — the seams are wired end to end.
	if _, err := host.ReadCatalog(ctx); err != nil {
		t.Fatalf("read-catalog through wired host: %v", err)
	}
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID: "p", Version: "1.0", State: "installed",
	}, time.Now()); err != nil {
		t.Fatalf("seed worker row: %v", err)
	}
	if err := grants.SetGrant(ctx, integrationhost.Grant{
		PluginID: "p", Kind: integrationhost.PermStatusWrite, Approved: true,
	}, time.Now()); err != nil {
		t.Fatalf("set status grant: %v", err)
	}
	if err := host.WriteStatus(ctx, "deadletter", "retry later"); err != nil {
		t.Fatalf("status-write through wired host: %v", err)
	}
	worker, ok, err := st.IntegrationWorker(ctx, "p")
	if err != nil || !ok {
		t.Fatalf("worker after status-write: ok=%v err=%v", ok, err)
	}
	if worker.State != "deadletter" || worker.LastError != "retry later" {
		t.Fatalf("worker status = state %q lastError %q, want deadletter/retry later", worker.State, worker.LastError)
	}
	if err := grants.SetGrant(ctx, integrationhost.Grant{
		PluginID: "p", Kind: integrationhost.PermProfileLink, Approved: true,
	}, time.Now()); err != nil {
		t.Fatalf("set profile-link grant: %v", err)
	}
	if err := grants.SetGrantApprovalBinding(ctx, "p", integrationhost.PermProfileLink, manifestBinding); err != nil {
		t.Fatalf("bind profile-link approval: %v", err)
	}
	if err := host.LinkProfile(ctx, "profile-default", "acct-123"); err != nil {
		t.Fatalf("profile-link through wired host: %v", err)
	}
	ref, linked, err := st.PluginProfileLink(ctx, "p", "profile-default")
	if err != nil || !linked || ref != "acct-123" {
		t.Fatalf("profile link = ref %q linked=%v err=%v, want acct-123/true/nil", ref, linked, err)
	}
	activity, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{AllLibraries: true}, Limit: 10})
	if err != nil {
		t.Fatalf("activity entries: %v", err)
	}
	foundProfileLinkAudit := false
	for _, entry := range activity {
		if entry.Action == store.ActivityPluginProfileLink &&
			entry.TargetID == "p" &&
			entry.TargetType == "plugin" &&
			entry.ProfileID == "profile-default" {
			foundProfileLinkAudit = true
			break
		}
	}
	if !foundProfileLinkAudit {
		t.Fatalf("profile-link audit action %q missing from activity entries: %+v", store.ActivityPluginProfileLink, activity)
	}

	// Grant approval requires declarations from the verified active manifest.
	lc := integrationhost.NewLifecycle(integrationGrantStore{
		st: st, declaredPermissions: []string{integrationhost.PermProfileLink}, manifestBinding: manifestBinding,
	}, st, time.Now)
	if err := lc.ApproveGrant(ctx, integrationhost.Grant{
		PluginID: "p", Kind: integrationhost.PermProfileLink,
	}); err != nil {
		t.Fatalf("lifecycle approve: %v", err)
	}
}

func TestIntegrationOutboundApprovalPersistsAndSerializes(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lifecycle := integrationLifecycle(st)
	if err := lifecycle.RegisterOutboundDeclaration(ctx, plugin.OutboundDeclaration{
		PluginID: "worker",
		Origins:  []string{"https://one.example.test", "https://two.example.test"},
	}); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	go func() { errs <- lifecycle.ApproveOutboundOrigin(ctx, "worker", "https://one.example.test") }()
	go func() { errs <- lifecycle.ApproveOutboundOrigin(ctx, "worker", "https://two.example.test") }()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	reloaded := integrationLifecycle(st)
	policy, ok, err := reloaded.OutboundPolicy(ctx, "worker")
	if err != nil || !ok {
		t.Fatalf("reload policy: ok=%v err=%v", ok, err)
	}
	if !policy.Allows("https://one.example.test") || !policy.Allows("https://two.example.test") {
		t.Fatalf("concurrent approvals lost an update: %+v", policy)
	}

	go func() { errs <- reloaded.RevokeOutboundOrigin(ctx, "worker", "https://one.example.test") }()
	go func() { errs <- reloaded.ApproveOutboundOrigin(ctx, "worker", "https://two.example.test") }()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	policy, ok, err = integrationLifecycle(st).OutboundPolicy(ctx, "worker")
	if err != nil || !ok {
		t.Fatalf("reload after revoke: ok=%v err=%v", ok, err)
	}
	if policy.Allows("https://one.example.test") || !policy.Allows("https://two.example.test") {
		t.Fatalf("concurrent revocation was lost: %+v", policy)
	}
}

func TestBuildIntegrationManagerRefusesDeclaredButUnapprovedOutbound(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"
	const origin = "https://declared.example.test"
	pluginDir := t.TempDir()
	artifact := []byte("verified-worker-bytes")
	if err := os.WriteFile(filepath.Join(pluginDir, pluginID), artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeIntegrationPackageManifest(pluginDir, pluginID, []string{origin}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(artifact)
	digest := hex.EncodeToString(sum[:])
	pin := digest
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID: pluginID, Version: "1.0", ArtifactSHA256: digest, State: "installed",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginUpdateSource(ctx, store.PluginUpdateSourceRow{
		PluginID: pluginID, Kind: "folder", Location: pluginDir, PinKind: "hash", Pin: pin,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginGrant(ctx, store.PluginGrantRow{
		PluginID: pluginID, Kind: integrationhost.PermDeclaredOutbound, Approved: true,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	lifecycle := integrationLifecycle(st)
	manager, err := buildIntegrationManagerFor(
		ctx, st, pluginID, secretFiles{dir: filepath.Join(pluginDir, "secrets")}, lifecycle, slog.Default(), "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil || manager.deps.Outbound == nil {
		t.Fatal("production manager did not wire the hardened outbound proxy")
	}
	host := integrationhost.NewHost(pluginID, manager.deps, integrationGrantStore{st: st, declaredPermissions: manager.permissions}, nil)
	token, err := manager.deps.Causation.TokenFor(t.Name(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.RequestOutbound(causation.WithToken(ctx, token), integrationhost.OutboundRequest{
		Method: "GET",
		URL:    origin + "/v1/items",
	}); !errors.Is(err, integrationhost.ErrPermissionDenied) {
		t.Fatalf("declared but operator-unapproved origin: want permission denied, got %v", err)
	}

	if err := lifecycle.ApproveOutboundOrigin(ctx, pluginID, origin); err != nil {
		t.Fatal(err)
	}
	if _, err := buildIntegrationManagerFor(
		ctx, st, pluginID, secretFiles{dir: filepath.Join(pluginDir, "secrets")}, integrationLifecycle(st), slog.Default(), "",
	); err != nil {
		t.Fatalf("manager reconstruction: %v", err)
	}
	policy, ok, err := integrationLifecycle(st).OutboundPolicy(ctx, pluginID)
	if err != nil || !ok || !policy.Allows(origin) {
		t.Fatalf("operator approval did not survive manager reconstruction: policy=%+v ok=%v err=%v", policy, ok, err)
	}
}

func TestBuildIntegrationManagerDoesNotRegisterPolicyBeforeArtifactVerification(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"
	pluginDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pluginDir, pluginID), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeIntegrationPackageManifest(pluginDir, pluginID, []string{"https://declared.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID: pluginID, Version: "1.0", State: "installed",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginUpdateSource(ctx, store.PluginUpdateSourceRow{
		PluginID: pluginID,
		Kind:     "folder",
		Location: pluginDir,
		PinKind:  integrationhost.PinHash,
		Pin:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	lifecycle := integrationLifecycle(st)
	if _, err := buildIntegrationManagerFor(
		ctx, st, pluginID, secretFiles{dir: filepath.Join(pluginDir, "secrets")}, lifecycle, slog.Default(), "",
	); !errors.Is(err, integrationhost.ErrVerification) {
		t.Fatalf("tampered artifact: want verification failure, got %v", err)
	}
	if policy, ok, err := lifecycle.OutboundPolicy(ctx, pluginID); err != nil || ok {
		t.Fatalf("unverified artifact registered policy: policy=%+v ok=%v err=%v", policy, ok, err)
	}
}

func writeIntegrationPackageManifest(root, pluginID string, outbound []string) error {
	raw, err := os.ReadFile(filepath.Join("..", "plugin", "testdata", "package-integration.json"))
	if err != nil {
		return err
	}
	manifest, err := plugin.ParsePackageManifest(raw)
	if err != nil {
		return err
	}
	manifest.ID = pluginID
	manifest.Outbound = outbound
	raw, err = json.Marshal(manifest)
	if err != nil {
		return err
	}
	// #nosec G703 -- root and pluginID are always test-controlled (t.TempDir() and a fixed test constant), never external input.
	return os.WriteFile(filepath.Join(root, pluginID+integrationPackageManifestSuffix), raw, 0o600)
}
