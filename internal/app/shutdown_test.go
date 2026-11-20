package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestShutdownSequenceStopsClaimsBeforeCancelJoinCheckpointAndStoreClose(t *testing.T) {
	var phases []string
	record := func(phase string) func() {
		return func() { phases = append(phases, phase) }
	}
	recordErr := func(phase string) func() error {
		return func() error {
			phases = append(phases, phase)
			return nil
		}
	}
	claimsDrained := make(chan struct{})
	close(claimsDrained)

	sequence := shutdownSequence{
		stopClaims:    record("stop-claims"),
		claimsDrained: claimsDrained,
		gracePeriod:   time.Second,
		after:         time.After,
		cancelJobs:    record("cancel-jobs"),
		joinWorkers:   record("join-workers"),
		joinBound:     time.Second,
		joinAfter:     time.After,
		checkpoint:    recordErr("checkpoint-state"),
		closeStore:    recordErr("close-store"),
	}
	if err := sequence.run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	want := []string{"stop-claims", "cancel-jobs", "join-workers", "checkpoint-state", "close-store"}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("shutdown phases = %v, want %v", phases, want)
	}
}

func TestRunShutdownCancelsBlockingJobAtDrainBound(t *testing.T) {
	beamfall := newShutdownTestApp(t)
	bound := make(chan time.Time, 1)
	armed := make(chan time.Duration, 1)
	const gracePeriod = 123 * time.Millisecond
	beamfall.drainGrace = gracePeriod
	beamfall.drainAfter = func(got time.Duration) <-chan time.Time {
		armed <- got
		return bound
	}

	const kind store.JobKind = "shutdown-blocking-test"
	handlerStarted := make(chan struct{})
	handlerCanceled := make(chan struct{})
	beamfall.jobs.Register(kind, func(ctx context.Context, _ store.Job) (jobs.Result, error) {
		close(handlerStarted)
		<-ctx.Done()
		close(handlerCanceled)
		return jobs.Result{}, ctx.Err()
	})
	enqueueShutdownTestJob(t, beamfall, kind)

	cancel, runDone := startShutdownTestApp(t, beamfall)
	waitForShutdownSignal(t, handlerStarted, "job handler did not start")
	cancel()

	if got := waitForShutdownDuration(t, armed); got != gracePeriod {
		t.Fatalf("grace period = %v, want %v", got, gracePeriod)
	}
	select {
	case <-handlerCanceled:
		t.Fatal("job handler canceled before the graceful-drain bound")
	default:
	}
	bound <- time.Now()
	waitForShutdownSignal(t, handlerCanceled, "job handler did not receive cancellation at the bound")
	waitForShutdownRun(t, runDone)
	assertShutdownStoreClosed(t, beamfall)
}

func TestRunShutdownLetsFastJobDrainWithoutCancellation(t *testing.T) {
	beamfall := newShutdownTestApp(t)
	bound := make(chan time.Time)
	armed := make(chan time.Duration, 1)
	beamfall.drainAfter = func(got time.Duration) <-chan time.Time {
		armed <- got
		return bound
	}

	const kind store.JobKind = "shutdown-fast-test"
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	handlerCompleted := make(chan struct{})
	beamfall.jobs.Register(kind, func(ctx context.Context, _ store.Job) (jobs.Result, error) {
		close(handlerStarted)
		select {
		case <-releaseHandler:
			close(handlerCompleted)
			return jobs.Result{}, nil
		case <-ctx.Done():
			return jobs.Result{}, ctx.Err()
		}
	})
	enqueueShutdownTestJob(t, beamfall, kind)

	cancel, runDone := startShutdownTestApp(t, beamfall)
	waitForShutdownSignal(t, handlerStarted, "job handler did not start")
	cancel()
	if got := waitForShutdownDuration(t, armed); got != defaultJobDrainGracePeriod {
		t.Fatalf("grace period = %v, want %v", got, defaultJobDrainGracePeriod)
	}
	close(releaseHandler)
	waitForShutdownSignal(t, handlerCompleted, "fast job did not drain")
	waitForShutdownRun(t, runDone)
	assertShutdownStoreClosed(t, beamfall)
}

func TestRunShutdownRepeatedlyJoinsWorkers(t *testing.T) {
	for range 3 {
		beamfall := newShutdownTestApp(t)
		cancel, runDone := startShutdownTestApp(t, beamfall)
		cancel()
		waitForShutdownRun(t, runDone)
		assertShutdownStoreClosed(t, beamfall)
	}
}

// A worker that never returns must cost a bounded delay, not the whole
// shutdown: the durable checkpoint and the store close are what an operator
// loses when the join stalls, so both still run past the bound.
func TestShutdownSequenceCheckpointsAndClosesStorePastTheWorkerJoinBound(t *testing.T) {
	var phases []string
	recordErr := func(phase string) func() error {
		return func() error {
			phases = append(phases, phase)
			return nil
		}
	}
	claimsDrained := make(chan struct{})
	close(claimsDrained)
	releaseWorker := make(chan struct{})
	t.Cleanup(func() { close(releaseWorker) })
	joinBound := make(chan time.Time, 1)

	sequence := shutdownSequence{
		stopClaims:    func() {},
		claimsDrained: claimsDrained,
		gracePeriod:   time.Second,
		after:         time.After,
		cancelJobs:    func() {},
		joinWorkers:   func() { <-releaseWorker },
		joinBound:     42 * time.Second,
		joinAfter: func(got time.Duration) <-chan time.Time {
			if got != 42*time.Second {
				t.Errorf("join bound = %v, want %v", got, 42*time.Second)
			}
			return joinBound
		},
		checkpoint: recordErr("checkpoint-state"),
		closeStore: recordErr("close-store"),
	}

	done := make(chan error, 1)
	go func() { done <- sequence.run() }()
	select {
	case <-done:
		t.Fatal("shutdown abandoned the worker join before the bound elapsed")
	case <-time.After(50 * time.Millisecond):
	}
	joinBound <- time.Now()

	select {
	case err := <-done:
		if !errors.Is(err, errWorkerJoinTimedOut) {
			t.Fatalf("run past the join bound = %v, want %v", err, errWorkerJoinTimedOut)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return after the worker join bound elapsed")
	}
	want := []string{"checkpoint-state", "close-store"}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("phases past the join bound = %v, want %v", phases, want)
	}
}

// A media byte route streams without a write deadline, so a live connection can
// outlast any graceful window. drainHTTPServer must close it outright at the
// bound rather than leave the connection and its handler goroutine alive.
func TestDrainHTTPServerClosesLiveConnectionsAtTheBound(t *testing.T) {
	requestArrived := make(chan struct{})
	releaseHandler := make(chan struct{})
	t.Cleanup(func() { close(releaseHandler) })
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			close(requestArrived)
			<-releaseHandler
		}),
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	response, err := http.Get("http://" + listener.Addr().String() + "/stream") //nolint:noctx // the handler is released by the test, not by a deadline
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	waitForShutdownSignal(t, requestArrived, "streaming request did not reach the handler")

	forced, drainErr := drainHTTPServer(server, serveErr, 100*time.Millisecond)
	if drainErr != nil {
		t.Fatalf("drainHTTPServer: %v", drainErr)
	}
	if !forced {
		t.Fatal("drainHTTPServer reported a graceful drain while a connection was still streaming")
	}
	assertShutdownBodyEnded(t, response.Body)
	assertShutdownPortReleased(t, listener.Addr().String())
}

// assertShutdownBodyEnded proves the connection behind an in-flight response was
// closed by the server: the read ends rather than blocking on a handler that is
// still holding it. It is bounded, because the defect it guards leaves the read
// blocked forever.
func assertShutdownBodyEnded(t *testing.T, body io.Reader) {
	t.Helper()
	read := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(body)
		read <- err
	}()
	select {
	case <-read:
	case <-time.After(5 * time.Second):
		t.Fatal("connection outlived the drain bound instead of being closed")
	}
}

func TestDrainHTTPServerJoinsServeGoroutineOnTheGracefulPath(t *testing.T) {
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.NewServeMux()}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	forced, drainErr := drainHTTPServer(server, serveErr, 5*time.Second)
	if drainErr != nil {
		t.Fatalf("drainHTTPServer: %v", drainErr)
	}
	if forced {
		t.Fatal("drainHTTPServer forced a close on an idle server")
	}
	if len(serveErr) != 0 {
		t.Fatal("drainHTTPServer left the Serve result unconsumed")
	}
	assertShutdownPortReleased(t, listener.Addr().String())
}

// Run owns single-ownership resources — the bound listener, the address latch,
// and the store's close. A second Run once panicked on the latch; it must be
// refused instead, both while the first is live and after it returned.
func TestRunRefusesASecondStart(t *testing.T) {
	beamfall := newShutdownTestApp(t)
	cancel, runDone := startShutdownTestApp(t, beamfall)
	if err := beamfall.Run(context.Background()); !errors.Is(err, errAlreadyRun) {
		t.Fatalf("Run while running = %v, want %v", err, errAlreadyRun)
	}
	cancel()
	waitForShutdownRun(t, runDone)
	if err := beamfall.Run(context.Background()); !errors.Is(err, errAlreadyRun) {
		t.Fatalf("Run after shutdown = %v, want %v", err, errAlreadyRun)
	}
}

// Close after Run is the in-process harness pattern; it must stay quiet rather
// than re-report a resource it no longer owns.
func TestCloseAfterRunStaysQuiet(t *testing.T) {
	beamfall := newShutdownTestApp(t)
	cancel, runDone := startShutdownTestApp(t, beamfall)
	cancel()
	waitForShutdownRun(t, runDone)
	if err := beamfall.Close(); err != nil {
		t.Fatalf("Close after Run: %v", err)
	}
}

func TestRunReportsScratchCleanupError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the fixture needs directory permissions to prevent unlink")
	}
	beamfall, _, playlist := newShutdownHLSTestApp(t)
	dir := filepath.Dir(playlist)
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // Keep the fixture directory traversable by its owner.
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // Restore the fixture's owner-only directory permissions.
			t.Error(err)
		}
	})

	cancel, runDone := startShutdownTestApp(t, beamfall)
	cancel()
	select {
	case err := <-runDone:
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("Run returned %v, want scratch removal permission error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after scratch cleanup failed")
	}
	if err := beamfall.Close(); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Close returned %v, want the same scratch cleanup failure", err)
	}
}

func TestRunClosesBlockedHLSResponseOnListenerFailure(t *testing.T) {
	beamfall, path, playlist := newShutdownHLSTestApp(t)
	// Keep the handler inside ServeContent when the connection stops accepting
	// bytes. A small playlist could fit in net/http's buffers and release early.
	body := "#EXTM3U\n" + strings.Repeat("# playlist comment\n", 4096)
	if err := os.WriteFile(playlist, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	serverConn, clientConn := net.Pipe()
	conn := &observedWriteConn{Conn: serverConn, writing: make(chan string, 1)}
	listener := newFailingListener()
	listener.firstConn = conn
	oldListenTCP := listenTCP
	listenTCP = func(_, _ string) (net.Listener, error) { return listener, nil }
	defer func() { listenTCP = oldListenTCP }()

	finished := make(chan struct{})
	var runErr error
	go func() {
		defer close(finished)
		runErr = beamfall.Run(t.Context())
	}()
	defer func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		listener.failNow()
		waitForShutdownSignal(t, finished, "Run did not retire after closing the test connection")
	}()
	if err := clientConn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(clientConn, "GET %s HTTP/1.1\r\nHost: localhost\r\n\r\n", path); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-conn.writing:
		if status != "HTTP/1.1 200 OK" {
			t.Fatalf("blocked response status = %q, want successful HLS", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HLS response did not reach its blocked write")
	}
	listener.failNow()
	waitForShutdownSignal(t, finished, "Run did not finish while the HLS client was stalled")
	if !errors.Is(runErr, errTestListenerFailed) {
		t.Fatalf("Run returned %v, want %v", runErr, errTestListenerFailed)
	}
	if _, err := os.Stat(playlist); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("HLS scratch artifact survives Run: %v", err)
	}
	root := filepath.Dir(filepath.Dir(playlist))
	next, err := playback.NewSessionsInDir(nil, root, nil)
	if err != nil {
		t.Fatalf("reopen scratch after Run: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

type observedWriteConn struct {
	net.Conn
	once    sync.Once
	writing chan string
}

func (c *observedWriteConn) Write(p []byte) (int, error) {
	c.once.Do(func() {
		status, _, _ := strings.Cut(string(p), "\r\n")
		c.writing <- status
	})
	return c.Conn.Write(p)
}

func newShutdownHLSTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	cfg := testConfig(t)
	cfg.Addr = "127.0.0.1:0"
	cfg.Discovery.Enabled = false
	cfg.SeedDevelopmentData = true
	ctx := t.Context()
	beamfall, err := New(ctx, cfg, discardLogger(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = beamfall.Close() })
	const mediaID store.MediaID = "media-alpha"
	src := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := beamfall.store.SetPrimaryMediaFile(ctx, mediaID, src); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.NewService(beamfall.store).Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	token, err := streamtoken.NewService(cfg.StreamTokenKey, time.Now).Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaHLS,
		MediaID:   mediaID,
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/media/hls/" + token + "/master.m3u8"
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("fixture HLS status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	playlists, err := filepath.Glob(filepath.Join(cfg.DataDir, "transcode", "session-*", "master.m3u8"))
	if err != nil || len(playlists) != 1 {
		t.Fatalf("fixture HLS playlists = %v, %v, want one live playlist", playlists, err)
	}
	return beamfall, path, playlists[0]
}

func assertShutdownPortReleased(t *testing.T, addr string) {
	t.Helper()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s still bound after shutdown: %v", addr, err)
	}
	_ = listener.Close()
}

func newShutdownTestApp(t *testing.T) *App {
	t.Helper()
	cfg := testConfig(t)
	cfg.Addr = "127.0.0.1:0"
	cfg.Discovery.Enabled = false
	beamfall, err := New(context.Background(), cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return beamfall
}

func enqueueShutdownTestJob(t *testing.T, beamfall *App, kind store.JobKind) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := beamfall.store.EnqueueJob(context.Background(), store.Job{
		Kind:      kind,
		Class:     store.JobClassBackground,
		Priority:  100,
		CreatedAt: now,
		RunAfter:  now,
	}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
}

func startShutdownTestApp(t *testing.T, beamfall *App) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- beamfall.Run(ctx) }()
	addrCtx, stopWaiting := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopWaiting()
	if addr := beamfall.Addr(addrCtx); addr == "" {
		cancel()
		t.Fatal("server did not bind a listener")
	}
	return cancel, runDone
}

func waitForShutdownDuration(t *testing.T, signal <-chan time.Duration) time.Duration {
	t.Helper()
	select {
	case duration := <-signal:
		return duration
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not arm the graceful-drain bound")
		return 0
	}
}

func waitForShutdownSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

func waitForShutdownRun(t *testing.T, runDone <-chan error) {
	t.Helper()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}
}

func assertShutdownStoreClosed(t *testing.T, beamfall *App) {
	t.Helper()
	_, err := beamfall.store.EnqueueJob(context.Background(), store.Job{Kind: "closed-store-probe"})
	if !errors.Is(err, store.ErrClosed) {
		t.Fatalf("EnqueueJob after Run = %v, want %v", err, store.ErrClosed)
	}
}
