package integrationhost

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/store"
)

// mustJSONBytes marshals a value for a test frame.
func mustJSONBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// fixtureWorkerPath is the built fixture-worker binary, set by TestMain. The
// supervisor isolation tests launch it as a real out-of-process worker.
var (
	fixtureWorkerPath     string
	beamfallShimPath      string
	supervisorFixturePath string
)

// TestMain builds the fixture worker binary once for the whole package (the
// build-in-TestMain pattern introduced by this ticket — no prior precedent).
// A build failure fails the suite fast with a clear message rather than letting
// the isolation tests silently skip.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "integrationhost-fixture-")
	if err != nil {
		panic("integrationhost test: temp dir: " + err.Error())
	}
	bin := filepath.Join(dir, "fixture-worker")
	// Built for the host GOOS/GOARCH; each CI platform rebuilds it here.
	build := exec.Command("go", "build", "-o", bin, "./testdata/fixtureworker")
	if out, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		panic("integrationhost test: go binary not found or fixture build failed: " + err.Error() + "\n" + string(out))
	}
	fixtureWorkerPath = bin
	if runtime.GOOS != "windows" {
		beamfallShimPath = filepath.Join(dir, "beamfall")
		// #nosec G204 -- go is a fixed tool; output is the private TestMain temp directory.
		build = exec.Command("go", "build", "-o", beamfallShimPath, "../../cmd/beamfall")
		if out, err := build.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			panic("integrationhost test: beamfall shim build failed: " + err.Error() + "\n" + string(out))
		}
		supervisorFixturePath = filepath.Join(dir, "supervisor-fixture")
		// #nosec G204 -- go is a fixed tool; output is the private TestMain temp directory.
		build = exec.Command("go", "build", "-o", supervisorFixturePath, "./testdata/supervisorfixture")
		if out, err := build.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			panic("integrationhost test: supervisor fixture build failed: " + err.Error() + "\n" + string(out))
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fixedGrants backs GrantSource with a static set (a worker's approved grants).
type fixedGrants struct {
	grants map[string][]Grant
}

func (f fixedGrants) Grants(_ context.Context, pluginID string) ([]Grant, error) {
	return f.grants[pluginID], nil
}

func (f fixedGrants) DeclaredPermissions(_ context.Context, pluginID string) ([]string, error) {
	grants := f.grants[pluginID]
	permissions := make([]string, 0, len(grants))
	for _, grant := range grants {
		permissions = append(permissions, grant.Kind)
	}
	return permissions, nil
}

// recordingCatalog records that ReadCatalog reached the catalog seam (proving the
// out-of-process worker reached Core ONLY through the Host API, never a DB
// handle) and returns a marker Library. `called` is written from the supervisor's
// serve goroutine and read from the test goroutine, so it is an atomic.
type recordingCatalog struct{ called atomic.Bool }

func (r *recordingCatalog) Libraries(_ context.Context, _ store.LibraryFilter) ([]store.Library, error) {
	r.called.Store(true)
	return []store.Library{{ID: store.LibraryID("lib-visible")}}, nil
}

type generationStatusWriter struct{ writes atomic.Int32 }

func (w *generationStatusWriter) WriteStatus(context.Context, string, string, string) error {
	w.writes.Add(1)
	return nil
}

func TestSupervisorGenerationFenceRejectsStalePublishAfterABA(t *testing.T) {
	const pluginID = "fixture"
	writes := &generationStatusWriter{}
	host := NewHost(pluginID, Deps{Status: writes}, fixedGrants{grants: map[string][]Grant{
		pluginID: {{PluginID: pluginID, Kind: PermStatusWrite, Approved: true}},
	}}, nil)
	fence := &GenerationFence{}
	firstLease := fence.Acquire()
	first := &Supervisor{pluginID: pluginID, host: host, generation: firstLease}
	request := hostRequest{Method: methodWriteStatus, Kind: "ready", Value: "first"}
	if response := first.dispatch(t.Context(), request); response.Err != "" {
		t.Fatalf("first generation publish: %s", response.Err)
	}

	secondLease := fence.Acquire()
	second := &Supervisor{pluginID: pluginID, host: host, generation: secondLease}
	thirdLease := fence.Acquire()
	third := &Supervisor{pluginID: pluginID, host: host, generation: thirdLease}
	if response := first.dispatch(t.Context(), request); !strings.Contains(response.Err, ErrWorkerGenerationRetired.Error()) {
		t.Fatalf("first generation after A→B→A = %q, want retired", response.Err)
	}
	if response := second.dispatch(t.Context(), request); !strings.Contains(response.Err, ErrWorkerGenerationRetired.Error()) {
		t.Fatalf("second generation after replacement = %q, want retired", response.Err)
	}
	if response := third.dispatch(t.Context(), request); response.Err != "" {
		t.Fatalf("current generation publish: %s", response.Err)
	}
	if got := writes.writes.Load(); got != 2 {
		t.Fatalf("status writes = %d, want first + current only", got)
	}
	if !third.claimRun() {
		t.Fatal("fresh supervisor start was rejected")
	}
	if third.claimRun() {
		t.Fatal("overlapping supervisor start acquired ownership")
	}
	third.releaseRun()
	if !third.claimRun() {
		t.Fatal("supervisor restart after retirement was rejected")
	}
	third.releaseRun()
}

// TestSupervisorWorkerIsolation launches the fixture worker out-of-process and
// proves: it runs as a separate process, connects only through the Host API
// socket, and a granted read-catalog call reaches the catalog seam — while the
// worker itself never received a DB handle, admin token, or GraphQL endpoint
// (it has no such inputs: only the socket path and the fd3 token).
func TestSupervisorWorkerIsolation(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Integration Worker transport is unsupported on this platform")
	}
	const pluginID = "fixture"
	catalog := &recordingCatalog{}
	deps := Deps{Catalog: catalog}
	grants := fixedGrants{grants: map[string][]Grant{
		pluginID: {{PluginID: pluginID, Kind: PermReadCatalog, Approved: true, AllLibraries: true}},
	}}
	host := NewHost(pluginID, deps, grants, nil)

	digest, err := HashFile(fixtureWorkerPath)
	if err != nil {
		t.Fatal(err)
	}
	artifact := Artifact{PluginID: pluginID, Path: fixtureWorkerPath, VerifiedSHA256: digest}
	sup := NewSupervisor(pluginID, artifact, host, nil, nil)
	sup.SetHardeningShim(beamfallShimPath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The worker performs one read-catalog probe then exits; Run returns
	// ErrWorkerExited (crash containment: a worker exit is never a server failure).
	err = sup.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "worker exited") {
		t.Fatalf("expected ErrWorkerExited, got %v", err)
	}
	if !catalog.called.Load() {
		t.Fatal("worker did not reach the catalog seam through the Host API")
	}
}

// TestSupervisorTokenIsOneShot proves the capability token is single-use: a
// second presentation (a leaked copy) is rejected. consumeToken is the atomic
// gate; this exercises it directly (the protocol path is covered by the
// isolation test).
func TestSupervisorTokenIsOneShot(t *testing.T) {
	sup := &Supervisor{token: "abc123"}
	if !sup.consumeToken("abc123") {
		t.Fatal("first presentation of the token should succeed")
	}
	if sup.consumeToken("abc123") {
		t.Fatal("second presentation of a one-shot token must be rejected")
	}
	if sup.consumeToken("") {
		t.Fatal("empty token must be rejected")
	}
}

// TestSupervisorRejectsBadHandshake proves a worker presenting the wrong token
// is denied: it never gets to call a Host API method. We drive the protocol with
// an in-memory pipe rather than a real process for determinism.
func TestSupervisorRejectsBadHandshake(t *testing.T) {
	const pluginID = "fixture"
	host := NewHost(pluginID, Deps{}, fixedGrants{}, nil)
	sup := &Supervisor{pluginID: pluginID, host: host, token: "real-token"}

	clientConn, serverConn := net.Pipe()
	go sup.handleConn(context.Background(), serverConn)

	// Present the WRONG token.
	if err := writeFrame(clientConn, mustJSONBytes(handshakeFrame{Token: "wrong-token"})); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	ack := make([]byte, 1)
	if _, err := clientConn.Read(ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack[0] != 0x00 {
		t.Fatalf("expected nack 0x00 for bad token, got 0x%02x", ack[0])
	}
	_ = clientConn.Close()
}

func TestSupervisorRunActionDispatchesToWorker(t *testing.T) {
	const pluginID = "fixture"
	host := NewHost(pluginID, Deps{Causation: causation.NewCausationCap(causation.DefaultLimits(), time.Now)}, fixedGrants{}, nil)
	sup := &Supervisor{pluginID: pluginID, host: host, token: "real-token"}

	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	go sup.handleConn(context.Background(), serverConn)

	if err := writeFrame(clientConn, mustJSONBytes(handshakeFrame{Token: "real-token"})); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	ack := make([]byte, 1)
	if _, err := clientConn.Read(ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack[0] != 0x01 {
		t.Fatalf("expected ack 0x01, got 0x%02x", ack[0])
	}
	actionConnReady := false
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		sup.actionMu.Lock()
		actionConnReady = sup.actionConn != nil
		sup.actionMu.Unlock()
		if actionConnReady {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !actionConnReady {
		t.Fatal("worker connection was not ready after handshake")
	}

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- sup.RunAction(ctx, pluginID, "refresh")
	}()

	raw, err := readFrame(clientConn)
	if err != nil {
		t.Fatalf("read action request: %v", err)
	}
	var req hostRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decode action request: %v", err)
	}
	if req.Method != methodRunAction || req.ActionID != "refresh" || req.RequestID == "" {
		t.Fatalf("action request = %+v", req)
	}
	if req.Causation == "" {
		t.Fatal("operator-dispatched action omitted its server-issued causation token")
	}
	if err := writeFrame(clientConn, mustJSONBytes(hostRequest{Method: methodActionResult, RequestID: req.RequestID})); err != nil {
		t.Fatalf("write action result: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunAction: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunAction did not receive worker result")
	}
}

func TestSupervisorActionTimeoutConfiguration(t *testing.T) {
	sup := &Supervisor{}
	if got := sup.configuredActionTimeout(); got != DefaultActionTimeout {
		t.Fatalf("zero-value action timeout = %v, want %v", got, DefaultActionTimeout)
	}
	sup.SetActionTimeout(250 * time.Millisecond)
	if got := sup.configuredActionTimeout(); got != 250*time.Millisecond {
		t.Fatalf("configured action timeout = %v, want 250ms", got)
	}
	sup.SetActionTimeout(0)
	if got := sup.configuredActionTimeout(); got != DefaultActionTimeout {
		t.Fatalf("reset action timeout = %v, want %v", got, DefaultActionTimeout)
	}
}

func TestSupervisorRunActionTimesOutAndDropsLateResult(t *testing.T) {
	const pluginID = "fixture"
	host := NewHost(pluginID, Deps{}, fixedGrants{}, nil)
	sup := &Supervisor{pluginID: pluginID, host: host, token: "real-token"}
	sup.SetActionTimeout(30 * time.Millisecond)

	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	go sup.handleConn(t.Context(), serverConn)
	if err := writeFrame(clientConn, mustJSONBytes(handshakeFrame{Token: "real-token"})); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	ack := make([]byte, 1)
	if _, err := clientConn.Read(ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack[0] != 0x01 {
		t.Fatalf("expected ack 0x01, got 0x%02x", ack[0])
	}
	actionConnReady := false
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		sup.actionMu.Lock()
		actionConnReady = sup.actionConn != nil
		sup.actionMu.Unlock()
		if actionConnReady {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !actionConnReady {
		t.Fatal("worker connection was not ready after handshake")
	}

	done := make(chan error, 1)
	go func() { done <- sup.RunAction(t.Context(), pluginID, "slow-action") }()
	raw, err := readFrame(clientConn)
	if err != nil {
		t.Fatalf("read slow action request: %v", err)
	}
	var timedOut hostRequest
	if err := json.Unmarshal(raw, &timedOut); err != nil {
		t.Fatalf("decode slow action request: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrActionTimeout) {
			t.Fatalf("RunAction error = %v, want %v", err, ErrActionTimeout)
		}
	case <-time.After(time.Second):
		t.Fatal("RunAction did not stop at the host-side timeout")
	}
	sup.actionMu.Lock()
	_, pending := sup.pendingActions[timedOut.RequestID]
	sup.actionMu.Unlock()
	if pending {
		t.Fatal("timed-out action remained pending")
	}

	// A late result for the dropped request is ignored and does not interfere
	// with the next action on the same worker connection.
	if err := writeFrame(clientConn, mustJSONBytes(hostRequest{Method: methodActionResult, RequestID: timedOut.RequestID})); err != nil {
		t.Fatalf("write late action result: %v", err)
	}
	nextDone := make(chan error, 1)
	go func() { nextDone <- sup.RunAction(t.Context(), pluginID, "next-action") }()
	raw, err = readFrame(clientConn)
	if err != nil {
		t.Fatalf("read next action request: %v", err)
	}
	var next hostRequest
	if err := json.Unmarshal(raw, &next); err != nil {
		t.Fatalf("decode next action request: %v", err)
	}
	if next.RequestID == timedOut.RequestID {
		t.Fatal("next action reused the timed-out request ID")
	}
	if err := writeFrame(clientConn, mustJSONBytes(hostRequest{Method: methodActionResult, RequestID: next.RequestID})); err != nil {
		t.Fatalf("write next action result: %v", err)
	}
	select {
	case err := <-nextDone:
		if err != nil {
			t.Fatalf("next RunAction: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("next RunAction did not receive its worker result")
	}
}
