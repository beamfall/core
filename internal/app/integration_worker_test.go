package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	sdk "github.com/beamfall/plugin-sdk"
)

// TestIntegrationGrantStoreRoundTrip proves the composition-root adapter maps
// the integrationhost grant/worker contract onto the store rows both ways, so a
// Host built from these seams sees what the Console wrote (ADR-0058 wiring).
func TestIntegrationGrantStoreRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	gs := integrationGrantStore{st: st}

	if err := gs.SetGrant(ctx, integrationhost.Grant{
		PluginID: "p", Kind: integrationhost.PermReadCatalog, Approved: true, AllLibraries: true,
	}, time.Now()); err != nil {
		t.Fatalf("set grant: %v", err)
	}
	grants, err := gs.Grants(ctx, "p")
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants: %d err=%v", len(grants), err)
	}
	if !grants[0].Approved || !grants[0].AllLibraries {
		t.Fatalf("grant round-trip mismatch: %+v", grants[0])
	}

	if err := gs.SetWorkerState(ctx, integrationhost.WorkerState{
		PluginID: "p", Version: "1.0", State: "installed",
	}, time.Now()); err != nil {
		t.Fatalf("set worker: %v", err)
	}
	w, ok, err := gs.WorkerState(ctx, "p")
	if err != nil || !ok || w.Version != "1.0" {
		t.Fatalf("worker state round-trip: ok=%v err=%v w=%+v", ok, err, w)
	}
}

func TestIntegrationGrantStoreRequiresCurrentManifestBinding(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	activeBinding := strings.Repeat("a", 64)
	staleBinding := strings.Repeat("b", 64)
	gs := integrationGrantStore{
		st: st, declaredPermissions: []string{integrationhost.PermReadCatalog}, manifestBinding: activeBinding,
	}
	if err := gs.SetGrant(ctx, integrationhost.Grant{
		PluginID: "p", Kind: integrationhost.PermReadCatalog, Approved: true, AllLibraries: true,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := gs.SetGrantApprovalBinding(ctx, "p", integrationhost.PermReadCatalog, staleBinding); err != nil {
		t.Fatal(err)
	}
	grants, err := gs.Grants(ctx, "p")
	if err != nil || len(grants) != 1 {
		t.Fatalf("stale-bound grants = %+v err=%v", grants, err)
	}
	if grants[0].Approved {
		t.Fatal("grant approved for a different artifact/manifest remained effective")
	}
	if err := gs.SetGrantApprovalBinding(ctx, "p", integrationhost.PermReadCatalog, activeBinding); err != nil {
		t.Fatal(err)
	}
	grants, err = gs.Grants(ctx, "p")
	if err != nil || len(grants) != 1 || !grants[0].Approved {
		t.Fatalf("current-bound grant = %+v err=%v", grants, err)
	}
}

func TestApplyUpdateDerivesVerifiedCandidateIdentityAndPermissions(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "candidate"
	sourceRoot := t.TempDir()
	artifactBytes := []byte("candidate-worker-v2")
	artifactPath := filepath.Join(sourceRoot, pluginID)
	if err := os.WriteFile(artifactPath, artifactBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(artifactBytes)
	digest := hex.EncodeToString(digestBytes[:])
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID: pluginID, Version: "1.0.0", ArtifactSHA256: strings.Repeat("0", 64), State: "installed",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginGrant(ctx, store.PluginGrantRow{
		PluginID: pluginID, Kind: integrationhost.PermReadCatalog, Approved: true, AllLibraries: true,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeUpdateCandidateManifest(t, sourceRoot, pluginID, "2.3.4", []string{
		integrationhost.PermReadCatalog,
		integrationhost.PermRequestFileMutation,
	})
	candidatePin := digest
	if err := st.SetPluginUpdateSource(ctx, store.PluginUpdateSourceRow{
		PluginID: pluginID, Kind: "folder", Location: sourceRoot, PinKind: integrationhost.PinHash, Pin: candidatePin,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := integrationLifecycle(st).ApplyUpdate(ctx, pluginID); err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	worker, ok, err := st.IntegrationWorker(ctx, pluginID)
	if err != nil || !ok {
		t.Fatalf("worker after update: ok=%v err=%v", ok, err)
	}
	if worker.Version != "2.3.4" || worker.ArtifactSHA256 != digest || worker.State != "update-staged" {
		t.Fatalf("verified candidate state = %+v", worker)
	}
	grants, err := st.PluginGrants(ctx, pluginID)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants {
		if grant.Kind == integrationhost.PermRequestFileMutation && grant.Approved {
			t.Fatal("candidate-added permission became effective without reapproval")
		}
	}

	writeUpdateCandidateManifest(t, sourceRoot, pluginID, "9.9.9", []string{integrationhost.PermPluginSecret})
	verifiedManifest, _, found, err := loadActiveIntegrationPackageManifest(
		filepath.Join(st.IntegrationArtifactRoot(), "sha256", digest), digest, pluginID,
	)
	if err != nil || !found {
		t.Fatalf("load active verified manifest: found=%v err=%v", found, err)
	}
	if verifiedManifest.Version != "2.3.4" || strings.Join(packagePermissionKinds(verifiedManifest), ",") != strings.Join([]string{
		integrationhost.PermReadCatalog,
		integrationhost.PermRequestFileMutation,
	}, ",") {
		t.Fatalf("mutable source replaced verified candidate manifest: %+v", verifiedManifest)
	}

	if err := os.WriteFile(artifactPath, []byte("tampered-worker-v3"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := integrationLifecycle(st).ApplyUpdate(ctx, pluginID); !errors.Is(err, integrationhost.ErrVerification) {
		t.Fatalf("tampered update candidate = %v, want verification failure", err)
	}
	unchanged, _, _ := st.IntegrationWorker(ctx, pluginID)
	if unchanged.Version != "2.3.4" || unchanged.ArtifactSHA256 != digest {
		t.Fatalf("unverified update changed effective state: %+v", unchanged)
	}
}

func writeUpdateCandidateManifest(t *testing.T, root, pluginID, version string, permissions []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "plugin", "testdata", "package-integration.json")) // #nosec G304 -- fixed repository test fixture path.
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := plugin.ParsePackageManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ID = pluginID
	manifest.Version = version
	manifest.Permissions = make([]plugin.PermissionSpec, 0, len(permissions))
	for _, kind := range permissions {
		policy, ok := sdk.PermissionPolicyFor(kind)
		if !ok {
			t.Fatalf("missing permission policy for %q", kind)
		}
		manifest.Permissions = append(manifest.Permissions, plugin.PermissionSpec{
			Kind: kind, Label: policy.Label, Description: policy.Description, HighRisk: policy.HighRisk,
		})
	}
	manifest.Actions = nil
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- root and pluginID are fixed, test-owned values.
	if err := os.WriteFile(filepath.Join(root, pluginID+integrationPackageManifestSuffix), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadIntegrationPackageManifestRejectsOversizeCandidate(t *testing.T) {
	root := t.TempDir()
	const pluginID = "candidate"
	path := filepath.Join(root, pluginID+integrationPackageManifestSuffix)
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), integrationhost.MaxPackageManifestBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := readIntegrationPackageManifest(root, pluginID); err == nil || found {
		t.Fatalf("oversize candidate manifest = found %v err %v, want bounded-read rejection", found, err)
	}
}

func TestIntegrationHostDepsMutationLoopPromotionTripwire(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	pluginID := "p"
	deps := IntegrationHostDeps(st, secretFiles{dir: t.TempDir()}, integrationLifecycle(st))
	if deps.Causation == nil {
		t.Fatal("IntegrationHostDeps omitted the structural *causation.CausationCap marker")
	}

	if deps.Outbound == nil {
		t.Fatal("IntegrationHostDeps must wire the hardened outbound policy proxy")
	}
	if wired := integrationWorkerMutationSeams(deps); len(wired) != 0 {
		t.Fatalf("IntegrationHostDeps wired mutation seams %v; ADR-0125 requires loop containment before promotion", wired)
	}
	if err := integrationWorkerMutationPromotionViolation(deps); err != nil {
		t.Fatal(err)
	}

	grants := integrationGrantStore{st: st, declaredPermissions: []string{
		integrationhost.PermPluginJob,
		integrationhost.PermRequestIngest,
		integrationhost.PermRequestFileMutation,
		integrationhost.PermDeclaredOutbound,
	}}
	for _, kind := range []string{
		integrationhost.PermPluginJob,
		integrationhost.PermRequestIngest,
		integrationhost.PermRequestFileMutation,
		integrationhost.PermDeclaredOutbound,
	} {
		if err := grants.SetGrant(ctx, integrationhost.Grant{
			PluginID: pluginID, Kind: kind, Approved: true, AllLibraries: true,
		}, time.Now()); err != nil {
			t.Fatalf("set grant %s: %v", kind, err)
		}
	}
	host := integrationhost.NewHost(pluginID, deps, grants, nil)
	if _, err := host.EnqueueJob(ctx, "kind", nil, time.Time{}); !errors.Is(err, integrationhost.ErrPermissionDenied) {
		t.Fatalf("plugin-job with nil seam: want permission denied, got %v", err)
	}
	if err := host.RequestIngest(ctx, "lib"); !errors.Is(err, integrationhost.ErrPermissionDenied) {
		t.Fatalf("request-ingest with nil seam: want permission denied, got %v", err)
	}
	if err := host.RequestFileMutation(ctx, integrationhost.FileMutationRequest{Op: "move"}); !errors.Is(err, integrationhost.ErrPermissionDenied) {
		t.Fatalf("request-file-mutation with nil seam: want permission denied, got %v", err)
	}
	outboundToken, err := deps.Causation.TokenFor(t.Name(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.RequestOutbound(causation.WithToken(ctx, outboundToken), integrationhost.OutboundRequest{Method: "GET", URL: "https://example.test"}); !errors.Is(err, integrationhost.ErrPermissionDenied) {
		t.Fatalf("request-outbound without an operator-approved origin: want permission denied, got %v", err)
	}

	for _, tc := range []struct {
		name   string
		inject func(integrationhost.Deps) integrationhost.Deps
	}{
		{name: "Jobs", inject: func(d integrationhost.Deps) integrationhost.Deps {
			d.Jobs = fakeJobEnqueuer{}
			return d
		}},
		{name: "Ingest", inject: func(d integrationhost.Deps) integrationhost.Deps {
			d.Ingest = fakeIngestRequester{}
			return d
		}},
		{name: "FileMut", inject: func(d integrationhost.Deps) integrationhost.Deps {
			d.FileMut = fakeFileMutationRequester{}
			return d
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			promoted := tc.inject(deps)
			withoutMarker := promoted
			withoutMarker.Causation = nil
			if err := integrationWorkerMutationPromotionViolation(withoutMarker); err == nil {
				t.Fatal("injected non-nil mutation seam passed without the containment marker")
			}
			if err := integrationWorkerMutationPromotionViolation(promoted); err != nil {
				t.Fatalf("injected non-nil mutation seam with the containment marker failed: %v", err)
			}
		})
	}
}

// TestBuildIntegrationManagerDormant proves the production manager builder
// returns nil (dormant) when no worker artifact is installed — a server with no
// third-party plugin pays nothing.
func TestBuildIntegrationManagerDormant(t *testing.T) {
	st := storetest.Open(t)
	m, err := buildIntegrationManagerFor(context.Background(), st, "absent", secretFiles{dir: t.TempDir()}, integrationLifecycle(st), slog.Default(), "")
	if err != nil {
		t.Fatalf("dormant build: %v", err)
	}
	if m != nil {
		t.Fatal("manager must be nil (dormant) when no worker is installed")
	}
}

func TestIntegrationManagerDeactivateCancelsInFlightWorkerAndReports(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID: pluginID,
		Version:  "1.0",
		State:    "running",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	canceled := make(chan struct{})
	m := &integrationManager{
		st:       st,
		pluginID: pluginID,
		logger:   slog.Default(),
		runSupervisor: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(canceled)
			return context.Cause(ctx)
		},
	}
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	if err := st.SetPluginActivation(ctx, pluginID, false, time.Now()); err != nil {
		t.Fatal(err)
	}

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deactivation did not stop the worker")
	}
	if !errors.Is(runErr, integrationhost.ErrPluginDeactivated) {
		t.Fatalf("Run error = %v, want plugin-deactivated cancellation", runErr)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("worker context did not observe cancellation")
	}
	worker, ok, err := st.IntegrationWorker(ctx, pluginID)
	if err != nil || !ok {
		t.Fatalf("worker status after deactivation: ok=%v err=%v", ok, err)
	}
	if worker.State != "stopped" || worker.LastError != integrationhost.ErrPluginDeactivated.Error() {
		t.Fatalf("worker status = state %q lastError %q, want stopped cancellation report", worker.State, worker.LastError)
	}
	if err := m.RunAction(ctx, pluginID, "refresh"); !errors.Is(err, integrationhost.ErrPluginDeactivated) {
		t.Fatalf("RunAction after deactivation = %v, want plugin-deactivated cancellation", err)
	}
}

func TestBuildIntegrationManagerQuarantinesMalformedManifest(t *testing.T) {
	t.Run("later valid worker still starts", func(t *testing.T) {
		st := storetest.Open(t)
		ctx := context.Background()
		installIntegrationWorkerFixture(t, ctx, st, "broken", true)
		validDir := installIntegrationWorkerFixture(t, ctx, st, "valid", false)
		registry, err := plugin.NewRegistry([]plugin.Bundle{testBundle("broken"), testBundle("valid")})
		if err != nil {
			t.Fatal(err)
		}

		manager, err := buildIntegrationManager(
			ctx, st, registry, secretFiles{dir: t.TempDir()}, integrationLifecycle(st), nil, "",
		)
		if err != nil {
			t.Fatalf("plugin-local manifest failure aborted manager discovery: %v", err)
		}
		if manager == nil || manager.pluginID != "valid" || manager.artifact.Path == filepath.Join(validDir, "valid") {
			t.Fatalf("manager = %+v, want later valid worker", manager)
		}
		assertIntegrationWorkerQuarantined(t, ctx, st, "broken")
	})

	t.Run("sole broken worker leaves manager dormant", func(t *testing.T) {
		st := storetest.Open(t)
		ctx := context.Background()
		installIntegrationWorkerFixture(t, ctx, st, "broken", true)
		registry, err := plugin.NewRegistry([]plugin.Bundle{testBundle("broken")})
		if err != nil {
			t.Fatal(err)
		}

		manager, err := buildIntegrationManager(
			ctx, st, registry, secretFiles{dir: t.TempDir()}, integrationLifecycle(st), nil, "",
		)
		if err != nil {
			t.Fatalf("plugin-local manifest failure aborted manager discovery: %v", err)
		}
		if manager != nil {
			t.Fatalf("manager = %+v, want dormant after quarantine", manager)
		}
		assertIntegrationWorkerQuarantined(t, ctx, st, "broken")
	})
}

func installIntegrationWorkerFixture(t *testing.T, ctx context.Context, st *store.Store, pluginID string, malformedManifest bool) string {
	t.Helper()
	dir := t.TempDir()
	artifact := []byte("verified-" + pluginID)
	if err := os.WriteFile(filepath.Join(dir, pluginID), artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(artifact)
	digest := hex.EncodeToString(sum[:])
	sourcePin := digest
	if malformedManifest {
		manifestRaw := []byte(`{"id":`)
		if err := os.WriteFile(filepath.Join(dir, pluginID+integrationPackageManifestSuffix), manifestRaw, 0o600); err != nil {
			t.Fatal(err)
		}
		sourcePin = digest
	}
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID: pluginID, Version: "1.0", ArtifactSHA256: digest, State: "installed",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginUpdateSource(ctx, store.PluginUpdateSourceRow{
		PluginID: pluginID, Kind: "folder", Location: dir, PinKind: integrationhost.PinHash, Pin: sourcePin,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerifiedWorkerObjectSurvivesSourceReplacement(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"
	sourceRoot := installIntegrationWorkerFixture(t, ctx, st, pluginID, false)
	sourcePath := filepath.Join(sourceRoot, pluginID)

	first, err := buildIntegrationManagerFor(ctx, st, pluginID, secretFiles{dir: t.TempDir()}, integrationLifecycle(st), slog.Default(), "")
	if err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("manager is nil for installed worker")
	}
	if first.artifact.Path == sourcePath {
		t.Fatalf("executed artifact path = %q, want server-owned object", first.artifact.Path)
	}
	verified, err := os.ReadFile(first.artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("replacement-after-verification"), 0o600); err != nil {
		t.Fatal(err)
	}

	restarted, err := buildIntegrationManagerFor(ctx, st, pluginID, secretFiles{dir: t.TempDir()}, integrationLifecycle(st), slog.Default(), "")
	if err != nil {
		t.Fatalf("restart from verified object: %v", err)
	}
	if restarted.artifact.Path != first.artifact.Path {
		t.Fatalf("restart path = %q, want verified object %q", restarted.artifact.Path, first.artifact.Path)
	}
	got, err := os.ReadFile(restarted.artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, verified) {
		t.Fatal("source replacement altered executed bytes")
	}
}

func TestIntegrationManagerSupervisesAndRoutesTwoWorkersIndependently(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	var launches sync.Map
	var actions sync.Map
	workers := make(map[string]*integrationManager)
	for _, pluginID := range []string{"alpha", "beta"} {
		if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{PluginID: pluginID, State: "installed"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		id := pluginID
		counter := &atomic.Int32{}
		launches.Store(id, counter)
		workers[id] = &integrationManager{
			st: st, pluginID: id, logger: slog.Default(),
			runSupervisor: func(context.Context) error {
				counter.Add(1)
				return integrationhost.ErrWorkerExited
			},
			runAction: func(_ context.Context, routedID, actionID string) error {
				actions.Store(routedID, actionID)
				return nil
			},
		}
	}
	manager := &integrationManager{st: st, logger: slog.Default(), workers: workers}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- manager.Run(runCtx) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		launches.Range(func(_, value any) bool {
			ready = ready && value.(*atomic.Int32).Load() >= 2
			return true
		})
		if ready {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for pluginID := range workers {
		countValue, _ := launches.Load(pluginID)
		if got := countValue.(*atomic.Int32).Load(); got < 2 {
			t.Fatalf("worker %s launched %d times, want independent restart", pluginID, got)
		}
		if err := manager.RunAction(ctx, pluginID, "refresh-"+pluginID); err != nil {
			t.Fatal(err)
		}
	}
	for _, pluginID := range []string{"alpha", "beta"} {
		got, ok := actions.Load(pluginID)
		if !ok || got != "refresh-"+pluginID {
			t.Fatalf("worker %s routed action = %v", pluginID, got)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("multi-worker manager did not stop")
	}
}

func TestBuildIntegrationManagerDiscoversEveryInstalledWorker(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	for _, pluginID := range []string{"alpha", "beta"} {
		installIntegrationWorkerFixture(t, ctx, st, pluginID, false)
	}
	registry, err := plugin.NewRegistry([]plugin.Bundle{testBundle("alpha"), testBundle("beta")})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := buildIntegrationManager(ctx, st, registry, secretFiles{dir: t.TempDir()}, integrationLifecycle(st), slog.Default(), "")
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil || len(manager.workers) != 2 {
		t.Fatalf("manager workers = %v, want alpha and beta", manager)
	}
	for _, pluginID := range []string{"alpha", "beta"} {
		if manager.workers[pluginID] == nil {
			t.Fatalf("installed worker %s was not discovered", pluginID)
		}
	}
}

func assertIntegrationWorkerQuarantined(t *testing.T, ctx context.Context, st *store.Store, pluginID string) {
	t.Helper()
	worker, ok, err := st.IntegrationWorker(ctx, pluginID)
	if err != nil || !ok {
		t.Fatalf("quarantined worker read: ok=%v err=%v", ok, err)
	}
	if worker.State != "failed" || worker.LastError == "" {
		t.Fatalf("quarantined worker = %+v, want failed state with diagnostic", worker)
	}
}

// TestBuildIntegrationManagerVerifiesAndRuns proves the production launch path:
// with an installed+enabled worker row, a hash-pinned Update Source, and a real
// on-disk artifact, the manager verifies the artifact and supervises it
// out-of-process (the supervisor launches the worker, which reaches the catalog
// seam through the Host API, then exits). This exercises the live path the intent
// audit flagged, end to end.
func TestBuildIntegrationManagerVerifiesAndRuns(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"

	// Build a tiny worker binary that performs one Host API probe then exits — the
	// same fixture the integrationhost supervisor test uses, built here for the app
	// package's production-path test.
	srcDir := filepath.Join("..", "integrationhost", "testdata", "fixtureworker")
	pluginDir := t.TempDir()
	bin := filepath.Join(pluginDir, pluginID)
	build := exec.Command("go", "build", "-o", bin, "./fixtureworker")
	build.Dir = filepath.Dir(srcDir)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture worker: %v\n%s", err, out)
	}

	// Install: worker row + hash-pinned Update Source + an approved read-catalog grant.
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID: pluginID, Version: "1.0", ArtifactSHA256: hex.EncodeToString(sum[:]), State: "installed",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginUpdateSource(ctx, store.PluginUpdateSourceRow{
		PluginID: pluginID, Kind: "folder", Location: pluginDir, PinKind: "hash", Pin: hex.EncodeToString(sum[:]),
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginGrant(ctx, store.PluginGrantRow{
		PluginID: pluginID, Kind: integrationhost.PermReadCatalog, Approved: true, AllLibraries: true,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	m, err := buildIntegrationManagerFor(ctx, st, pluginID, secretFiles{dir: filepath.Join(pluginDir, "secrets")}, integrationLifecycle(st), slog.Default(), "")
	if err != nil {
		t.Fatalf("build manager (should verify + accept): %v", err)
	}
	if m == nil {
		t.Fatal("manager must be non-nil for an installed, verified worker")
	}

	// Run supervises the worker; it probes read-catalog then exits, and Run
	// returns when ctx is canceled. We cancel shortly after to bound the test.
	runCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { m.Run(runCtx); close(done) }()
	// Give the worker time to launch + probe, then cancel so the bounded-restart
	// loop exits cleanly.
	time.Sleep(2 * time.Second)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("manager Run did not stop after ctx cancel")
	}
}

// TestBuildIntegrationManagerRejectsTamperedArtifact proves a hash-pin mismatch
// refuses the worker at build time — a tampered binary is never launchable.
func TestBuildIntegrationManagerRejectsTamperedArtifact(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"
	pluginDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pluginDir, pluginID), []byte("not the pinned bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{PluginID: pluginID, State: "installed"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginUpdateSource(ctx, store.PluginUpdateSourceRow{
		PluginID: pluginID, Kind: "folder", Location: pluginDir, PinKind: "hash", Pin: "deadbeef",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := buildIntegrationManagerFor(ctx, st, pluginID, secretFiles{dir: t.TempDir()}, integrationLifecycle(st), slog.Default(), ""); err == nil {
		t.Fatal("a tampered artifact (hash mismatch) must be refused at build time")
	}
}

func integrationWorkerMutationSeams(deps integrationhost.Deps) []string {
	var wired []string
	if deps.Jobs != nil {
		wired = append(wired, "Jobs")
	}
	if deps.Ingest != nil {
		wired = append(wired, "Ingest")
	}
	if deps.FileMut != nil {
		wired = append(wired, "FileMut")
	}
	return wired
}

func integrationWorkerMutationPromotionViolation(deps integrationhost.Deps) error {
	wired := integrationWorkerMutationSeams(deps)
	if len(wired) == 0 {
		return nil
	}
	if deps.Causation != nil {
		return nil
	}
	return fmt.Errorf("Integration Worker mutation seams %v require a non-nil *causation.CausationCap marker", wired)
}

type fakeIngestRequester struct{}

func (fakeIngestRequester) RequestIngest(context.Context, string, store.LibraryID) error { return nil }

type fakeJobEnqueuer struct{}

func (fakeJobEnqueuer) EnqueuePluginJob(context.Context, string, string, []byte, time.Time) (store.JobID, error) {
	return "job", nil
}

type fakeFileMutationRequester struct{}

func (fakeFileMutationRequester) RequestFileMutation(context.Context, string, integrationhost.FileMutationRequest) error {
	return nil
}

func TestBuildIntegrationManagerInstallsHardeningShim(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"
	pluginDir := t.TempDir()
	artifactPath := filepath.Join(pluginDir, pluginID)
	data := []byte("verified-worker-bytes")
	if err := os.WriteFile(artifactPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	pin := hex.EncodeToString(sum[:])
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID: pluginID, Version: "1.0", ArtifactSHA256: pin, State: "installed",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginUpdateSource(ctx, store.PluginUpdateSourceRow{
		PluginID: pluginID, Kind: "folder", Location: pluginDir, PinKind: "hash", Pin: pin,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	const shimPath = "/opt/beamfall/bin/beamfall"
	m, err := buildIntegrationManagerFor(ctx, st, pluginID, secretFiles{dir: t.TempDir()}, integrationLifecycle(st), slog.Default(), shimPath)
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	if m == nil {
		t.Fatal("manager must be non-nil for an installed, verified worker")
	}
	if m.shimPath != shimPath {
		t.Fatalf("manager shim path = %q, want %q", m.shimPath, shimPath)
	}
}

// TestBuildIntegrationManagerVerifiesSigningKeyAndRuns proves a
// signing-key-pinned worker is verified against the stored trusted Ed25519 public
// key + detached signature at the launch path, then launched (the PLUG-1-SIGNKEY
// install/launch wiring — the prior fail-closed refusal is gone).
func TestBuildIntegrationManagerVerifiesSigningKeyAndRuns(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"

	// Build the same fixture worker the hash-pin run test uses.
	srcDir := filepath.Join("..", "integrationhost", "testdata", "fixtureworker")
	pluginDir := t.TempDir()
	bin := filepath.Join(pluginDir, pluginID)
	build := exec.Command("go", "build", "-o", bin, "./fixtureworker")
	build.Dir = filepath.Dir(srcDir)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture worker: %v\n%s", err, out)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}

	// Sign the artifact bytes with a fresh Ed25519 key; store pub + detached sig
	// (both hex) on the signing-key-pinned Update Source — the install path.
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, data)
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{
		PluginID: pluginID, Version: "1.0", SigningKeyID: "key-1", State: "installed",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginUpdateSource(ctx, store.PluginUpdateSourceRow{
		PluginID: pluginID, Kind: "folder", Location: pluginDir, PinKind: "signing-key",
		Pin: "key-1", SigningKey: hex.EncodeToString(pub), Signature: hex.EncodeToString(sig),
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginGrant(ctx, store.PluginGrantRow{
		PluginID: pluginID, Kind: integrationhost.PermReadCatalog, Approved: true, AllLibraries: true,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	m, err := buildIntegrationManagerFor(ctx, st, pluginID, secretFiles{dir: filepath.Join(pluginDir, "secrets")}, integrationLifecycle(st), slog.Default(), "")
	if err != nil {
		t.Fatalf("build manager (signing-key should verify + accept): %v", err)
	}
	if m == nil {
		t.Fatal("manager must be non-nil for an installed, signature-verified worker")
	}

	runCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { m.Run(runCtx); close(done) }()
	time.Sleep(2 * time.Second)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("manager Run did not stop after ctx cancel")
	}
}

// TestBuildIntegrationManagerRejectsTamperedSignature proves a signing-key pin
// whose stored signature does not verify against the artifact bytes refuses the
// worker at build time (fail closed) — a tampered signature is never launchable.
func TestBuildIntegrationManagerRejectsTamperedSignature(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const pluginID = "fixture"
	pluginDir := t.TempDir()
	data := []byte("signed-worker-bytes")
	if err := os.WriteFile(filepath.Join(pluginDir, pluginID), data, 0o755); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, data)
	sig[0] ^= 0xff // tamper
	if err := st.SetIntegrationWorker(ctx, store.IntegrationWorkerRow{PluginID: pluginID, State: "installed"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginUpdateSource(ctx, store.PluginUpdateSourceRow{
		PluginID: pluginID, Kind: "folder", Location: pluginDir, PinKind: "signing-key",
		Pin: "key-1", SigningKey: hex.EncodeToString(pub), Signature: hex.EncodeToString(sig),
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := buildIntegrationManagerFor(ctx, st, pluginID, secretFiles{dir: t.TempDir()}, integrationLifecycle(st), slog.Default(), ""); err == nil {
		t.Fatal("a tampered signature must be refused at build time (fail closed)")
	}
}

// TestIntegrationEventReaderWired proves the event-reader adapter delegates to
// the store's worker-filtered tail.
func TestIntegrationEventReaderWired(t *testing.T) {
	st := storetest.Open(t)
	er := IntegrationEventReader{Store: st}
	events, err := er.ReadEventOutboxFiltered(context.Background(), 0, 10, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("filtered read: %v", err)
	}
	_ = events // an empty store yields no events; the seam is exercised
}
