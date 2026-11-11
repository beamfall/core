package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/remoteaccess"
)

type fakeRelayTimer struct {
	d  time.Duration
	ch chan time.Time
}

func (ft fakeRelayTimer) fire() { ft.ch <- time.Time{} }

type fakeRelaySession struct {
	id      int
	endOnce sync.Once
	ended   chan struct{}
}

func (s *fakeRelaySession) Accept() (net.Conn, error) {
	<-s.ended
	return nil, errors.New("session closed")
}

func (s *fakeRelaySession) Close() error {
	s.endOnce.Do(func() { close(s.ended) })
	return nil
}

// fakeRelayCarrier records dials and carries; Connect fails while connectErrs
// has entries.
type fakeRelayCarrier struct {
	mu          sync.Mutex
	dials       int
	connectErrs []error
	sessions    []*fakeRelaySession

	connects    chan int // dial number, sent on every Connect
	carryStarts chan int // session id
	carryEnds   chan int // session id
}

func newFakeRelayCarrier() *fakeRelayCarrier {
	return &fakeRelayCarrier{
		connects:    make(chan int, 16),
		carryStarts: make(chan int, 16),
		carryEnds:   make(chan int, 16),
	}
}

func (f *fakeRelayCarrier) Connect(context.Context) (remoteaccess.RelaySession, error) {
	f.mu.Lock()
	f.dials++
	n := f.dials
	var err error
	if len(f.connectErrs) > 0 {
		err, f.connectErrs = f.connectErrs[0], f.connectErrs[1:]
	}
	var s *fakeRelaySession
	if err == nil {
		s = &fakeRelaySession{id: len(f.sessions) + 1, ended: make(chan struct{})}
		f.sessions = append(f.sessions, s)
	}
	f.mu.Unlock()
	f.connects <- n
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (f *fakeRelayCarrier) Carry(ctx context.Context, rs remoteaccess.RelaySession) error {
	s := rs.(*fakeRelaySession)
	f.carryStarts <- s.id
	select {
	case <-s.ended:
	case <-ctx.Done():
		_ = s.Close()
	}
	f.carryEnds <- s.id
	return errors.New("relay carrier session ended")
}

func (f *fakeRelayCarrier) Endpoint() string   { return "relay.example.com:7443" }
func (f *fakeRelayCarrier) PublicHost() string { return "remote.example.com" }

func (f *fakeRelayCarrier) session(id int) *fakeRelaySession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[id-1]
}

type relayLoopHarness struct {
	t       *testing.T
	carrier *fakeRelayCarrier
	timers  chan fakeRelayTimer
	cancel  context.CancelFunc
	done    chan struct{}
}

func startRelayLoop(t *testing.T, carrier *fakeRelayCarrier) *relayLoopHarness {
	t.Helper()
	h := &relayLoopHarness{t: t, carrier: carrier, timers: make(chan fakeRelayTimer, 16), done: make(chan struct{})}
	after := func(d time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		h.timers <- fakeRelayTimer{d: d, ch: ch}
		return ch
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		defer close(h.done)
		runRelayCarrierLoop(ctx, carrier, logger, after)
	}()
	t.Cleanup(h.stop)
	return h
}

func (h *relayLoopHarness) stop() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		h.t.Fatal("relay carrier loop did not stop on cancel")
	}
}

func recvRelay[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func expectRelayQuiet[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpected %s: %v", what, v)
	case <-time.After(150 * time.Millisecond):
	}
}

func assertBackoffDelay(t *testing.T, d, base time.Duration) {
	t.Helper()
	if d < base-base/5 || d > base+base/5 {
		t.Fatalf("backoff delay = %v, want %v +/-20%%", d, base)
	}
}

// At 2m a same-identity replacement registers while the first session is still
// carrying; the first session's end is neither a failure nor a redial trigger,
// and only the newest generation's end redials (after backoff).
func TestRelayCarrierRotatesAtTwoMinutesWithoutRedial(t *testing.T) {
	carrier := newFakeRelayCarrier()
	h := startRelayLoop(t, carrier)

	if n := recvRelay(t, carrier.connects, "first dial"); n != 1 {
		t.Fatalf("dial = %d, want 1", n)
	}
	recvRelay(t, carrier.carryStarts, "first carry")
	rotate := recvRelay(t, h.timers, "rotation timer")
	if rotate.d != 2*time.Minute {
		t.Fatalf("rotation timer = %v, want 2m", rotate.d)
	}

	rotate.fire()
	if n := recvRelay(t, carrier.connects, "rotation dial"); n != 2 {
		t.Fatalf("dial = %d, want 2", n)
	}
	select {
	case <-carrier.session(1).ended:
		t.Fatal("first session ended before the replacement registered")
	default:
	}
	if id := recvRelay(t, carrier.carryStarts, "replacement carry"); id != 2 {
		t.Fatalf("replacement carry session = %d, want 2", id)
	}
	if next := recvRelay(t, h.timers, "next rotation timer"); next.d != relayCarrierRotateEvery {
		t.Fatalf("next rotation timer = %v, want 2m", next.d)
	}

	// The superseded session drains away: no redial, no backoff.
	_ = carrier.session(1).Close()
	if id := recvRelay(t, carrier.carryEnds, "first carry end"); id != 1 {
		t.Fatalf("ended carry = %d, want 1", id)
	}
	expectRelayQuiet(t, carrier.connects, "redial after superseded session ended")
	expectRelayQuiet(t, h.timers, "timer after superseded session ended")

	// The current session ending is a real loss: backoff, then redial.
	_ = carrier.session(2).Close()
	recvRelay(t, carrier.carryEnds, "current carry end")
	backoff := recvRelay(t, h.timers, "redial backoff")
	assertBackoffDelay(t, backoff.d, relayCarrierBackoffMin)
	backoff.fire()
	if n := recvRelay(t, carrier.connects, "redial"); n != 3 {
		t.Fatalf("dial = %d, want 3", n)
	}
}

// An unauthorized rejection is retried with the existing backoff, both for the
// initial dial and for a rotation dial; each retry is a fresh Connect (which
// mints a fresh token).
func TestRelayCarrierRetriesUnauthorizedWithBackoff(t *testing.T) {
	unauthorized := errors.New("relay registration: relay rejected carrier: unauthorized")
	carrier := newFakeRelayCarrier()
	carrier.connectErrs = []error{unauthorized, unauthorized}
	h := startRelayLoop(t, carrier)

	recvRelay(t, carrier.connects, "first dial")
	first := recvRelay(t, h.timers, "first backoff")
	assertBackoffDelay(t, first.d, relayCarrierBackoffMin)
	first.fire()
	recvRelay(t, carrier.connects, "second dial")
	second := recvRelay(t, h.timers, "second backoff")
	assertBackoffDelay(t, second.d, 2*relayCarrierBackoffMin)
	second.fire()
	if n := recvRelay(t, carrier.connects, "third dial"); n != 3 {
		t.Fatalf("dial = %d, want 3", n)
	}
	recvRelay(t, carrier.carryStarts, "carry")
	rotate := recvRelay(t, h.timers, "rotation timer")

	// A rotation rejected as unauthorized keeps the current carry and retries.
	carrier.mu.Lock()
	carrier.connectErrs = []error{unauthorized}
	carrier.mu.Unlock()
	rotate.fire()
	recvRelay(t, carrier.connects, "rotation dial")
	retry := recvRelay(t, h.timers, "rotation retry backoff")
	assertBackoffDelay(t, retry.d, relayCarrierBackoffMin)
	select {
	case <-carrier.session(1).ended:
		t.Fatal("failed rotation tore down the current session")
	default:
	}
	retry.fire()
	recvRelay(t, carrier.connects, "rotation retry dial")
	if id := recvRelay(t, carrier.carryStarts, "replacement carry"); id != 2 {
		t.Fatalf("replacement carry session = %d, want 2", id)
	}
}

func TestRelayCarrierStopsOnInvalidHello(t *testing.T) {
	carrier := newFakeRelayCarrier()
	carrier.connectErrs = []error{remoteaccess.ErrRelayInvalidHello}
	h := startRelayLoop(t, carrier)
	recvRelay(t, carrier.connects, "dial")
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop kept running after invalid hello")
	}
}
