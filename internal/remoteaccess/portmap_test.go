package remoteaccess

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// fakeGateway is an in-memory port-mapping gateway. Nothing here touches a
// network.
type fakeGateway struct {
	mu       sync.Mutex
	protocol string
	external netip.Addr
	lease    time.Duration // granted lease; 0 = permanent
	addErrs  []error       // consumed one per AddMapping; nil entry = success
	adds     int
	deletes  int
	// addBlock / deleteBlock make the call wait for ctx to end.
	addBlock    chan struct{}
	deleteBlock bool
	// added receives once per AddMapping call, when set.
	added chan struct{}
}

func (g *fakeGateway) Protocol() string { return g.protocol }

func (g *fakeGateway) ExternalAddress(context.Context) (netip.Addr, error) {
	return g.external, nil
}

func (g *fakeGateway) AddMapping(ctx context.Context, _, externalPort uint16, _ time.Duration) (uint16, time.Duration, error) {
	g.mu.Lock()
	block, added := g.addBlock, g.added
	g.mu.Unlock()
	if added != nil {
		added <- struct{}{}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.adds++
	if len(g.addErrs) > 0 {
		err := g.addErrs[0]
		g.addErrs = g.addErrs[1:]
		if err != nil {
			return 0, 0, err
		}
	}
	return externalPort, g.lease, nil
}

func (g *fakeGateway) DeleteMapping(ctx context.Context, _, _ uint16) error {
	g.mu.Lock()
	block := g.deleteBlock
	g.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	g.mu.Lock()
	g.deletes++
	g.mu.Unlock()
	return nil
}

func (g *fakeGateway) counts() (adds, deletes int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.adds, g.deletes
}

type fakeDiscoverer struct {
	gw  Gateway
	err error
}

func (d *fakeDiscoverer) Discover(context.Context) (Gateway, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.gw, nil
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestMapper(d GatewayDiscoverer) (*PortMapper, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	m := NewPortMapper(d, 8443, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.now = clock.Now
	return m, clock
}

func testGateway() *fakeGateway {
	return &fakeGateway{
		protocol: PortMapProtocolNATPMP,
		external: netip.MustParseAddr("203.0.113.7"),
		lease:    2 * time.Hour,
	}
}

func TestPortMapperStartsDisabledAndSendsNothing(t *testing.T) {
	gw := testGateway()
	m, _ := newTestMapper(&fakeDiscoverer{gw: gw})
	if st := m.Status(); st.State != PortMapDisabled || st.Enabled {
		t.Fatalf("initial status = %+v, want disabled", st)
	}
	if d := m.Maintain(context.Background()); d != portMapIdleWait {
		t.Fatalf("idle wait = %v", d)
	}
	if adds, _ := gw.counts(); adds != 0 {
		t.Fatalf("disabled mapper sent %d map requests", adds)
	}
}

func TestPortMapperMapsOnEnable(t *testing.T) {
	gw := testGateway()
	m, clock := newTestMapper(&fakeDiscoverer{gw: gw})
	st := m.SetEnabled(context.Background(), true)
	if st.State != PortMapMapped || !st.Enabled {
		t.Fatalf("status = %+v, want mapped", st)
	}
	if st.Protocol != PortMapProtocolNATPMP || st.ExternalPort != 8443 || st.InternalPort != 8443 {
		t.Fatalf("mapping = %+v", st)
	}
	if st.ExternalAddress != gw.external {
		t.Fatalf("external address = %v", st.ExternalAddress)
	}
	if st.Lease != 2*time.Hour || !st.ExpiresAt.Equal(clock.Now().Add(2*time.Hour)) {
		t.Fatalf("lease = %v expires %v", st.Lease, st.ExpiresAt)
	}
	if d := m.wait(); d != time.Hour {
		t.Fatalf("next renewal in %v, want half the lease", d)
	}
}

func TestPortMapperRenewsAtHalfLease(t *testing.T) {
	gw := testGateway()
	m, clock := newTestMapper(&fakeDiscoverer{gw: gw})
	m.SetEnabled(context.Background(), true)

	clock.Advance(time.Hour - time.Second)
	m.Maintain(context.Background())
	if adds, _ := gw.counts(); adds != 1 {
		t.Fatalf("renewed early: %d requests", adds)
	}
	clock.Advance(time.Second)
	m.Maintain(context.Background())
	if adds, _ := gw.counts(); adds != 2 {
		t.Fatalf("no renewal at half lease: %d requests", adds)
	}
	st := m.Status()
	if st.State != PortMapMapped || !st.ExpiresAt.Equal(clock.Now().Add(2*time.Hour)) {
		t.Fatalf("renewed status = %+v", st)
	}
}

func TestPortMapperLeaseExpiresWhenRenewalsFail(t *testing.T) {
	gw := testGateway()
	m, clock := newTestMapper(&fakeDiscoverer{gw: gw})
	m.SetEnabled(context.Background(), true)
	renewErr := errors.New("gateway went quiet")
	gw.mu.Lock()
	gw.addErrs = []error{renewErr, renewErr, renewErr, renewErr, renewErr, renewErr, renewErr, renewErr}
	gw.mu.Unlock()

	clock.Advance(time.Hour)
	m.Maintain(context.Background())
	st := m.Status()
	if st.State != PortMapMapped || st.Reason != PortMapReasonGatewayError {
		t.Fatalf("after failed renewal = %+v, want still mapped with the renewal reason", st)
	}
	if d := m.wait(); d != portMapRetryMin {
		t.Fatalf("retry in %v, want %v", d, portMapRetryMin)
	}

	clock.Advance(time.Hour)
	m.Maintain(context.Background())
	st = m.Status()
	if st.State != PortMapFailed || st.Reason != PortMapReasonLeaseExpired {
		t.Fatalf("after expiry = %+v, want failed lease_expired", st)
	}
	if st.ExternalPort != 0 || !st.ExpiresAt.IsZero() {
		t.Fatalf("expired mapping still advertised: %+v", st)
	}

	// The next success restores the mapping.
	gw.mu.Lock()
	gw.addErrs = nil
	gw.mu.Unlock()
	clock.Advance(portMapRetryMax)
	m.Maintain(context.Background())
	if st := m.Status(); st.State != PortMapMapped || st.Reason != "" {
		t.Fatalf("after recovery = %+v", st)
	}
}

func TestPortMapperGatewayRefusal(t *testing.T) {
	gw := testGateway()
	gw.addErrs = []error{&GatewayRefusedError{Protocol: PortMapProtocolNATPMP, Code: 2, Reason: "refused"}}
	m, clock := newTestMapper(&fakeDiscoverer{gw: gw})
	st := m.SetEnabled(context.Background(), true)
	if st.State != PortMapFailed || st.Reason != PortMapReasonGatewayRefused || st.GatewayCode != 2 {
		t.Fatalf("status = %+v, want failed gateway_refused code 2", st)
	}
	if st.ExternalPort != 0 {
		t.Fatalf("refused mapping advertises port %d", st.ExternalPort)
	}
	// Backoff, then a retry.
	clock.Advance(portMapRetryMin - time.Second)
	m.Maintain(context.Background())
	if adds, _ := gw.counts(); adds != 1 {
		t.Fatalf("retried before backoff: %d", adds)
	}
	clock.Advance(time.Second)
	m.Maintain(context.Background())
	if st := m.Status(); st.State != PortMapMapped {
		t.Fatalf("retry status = %+v", st)
	}
}

func TestPortMapperBackoffDoublesAndCaps(t *testing.T) {
	gw := testGateway()
	m, clock := newTestMapper(&fakeDiscoverer{gw: gw})
	gw.addErrs = make([]error, 12)
	for i := range gw.addErrs {
		gw.addErrs[i] = &GatewayRefusedError{Protocol: PortMapProtocolNATPMP, Code: 4, Reason: "out of resources"}
	}
	m.SetEnabled(context.Background(), true)
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, portMapRetryMax, portMapRetryMax}
	for i, w := range want {
		clock.Advance(m.wait())
		m.Maintain(context.Background())
		if got := m.wait(); got != w {
			t.Fatalf("retry %d waits %v, want %v", i+2, got, w)
		}
	}
}

func TestPortMapperNoGateway(t *testing.T) {
	m, _ := newTestMapper(&fakeDiscoverer{err: ErrNoGateway})
	st := m.SetEnabled(context.Background(), true)
	if st.State != PortMapFailed || st.Reason != PortMapReasonNoGateway {
		t.Fatalf("status = %+v, want failed no_gateway", st)
	}
}

func TestPortMapperDisableRemovesMapping(t *testing.T) {
	gw := testGateway()
	m, _ := newTestMapper(&fakeDiscoverer{gw: gw})
	m.SetEnabled(context.Background(), true)
	st := m.SetEnabled(context.Background(), false)
	if st.State != PortMapDisabled || st.Enabled || st.ExternalPort != 0 {
		t.Fatalf("status = %+v, want disabled", st)
	}
	if _, deletes := gw.counts(); deletes != 1 {
		t.Fatalf("deletes = %d, want 1", deletes)
	}
	// Disabled: maintenance sends nothing.
	m.Maintain(context.Background())
	if adds, _ := gw.counts(); adds != 1 {
		t.Fatalf("disabled mapper renewed: %d", adds)
	}
}

func TestPortMapperDisableAfterFailedRenewalStillRemoves(t *testing.T) {
	gw := testGateway()
	m, clock := newTestMapper(&fakeDiscoverer{gw: gw})
	m.SetEnabled(context.Background(), true)
	gw.mu.Lock()
	gw.addErrs = []error{errors.New("transient")}
	gw.mu.Unlock()
	clock.Advance(time.Hour)
	m.Maintain(context.Background())
	m.SetEnabled(context.Background(), false)
	if _, deletes := gw.counts(); deletes != 1 {
		t.Fatalf("live mapping not removed after a failed renewal: deletes=%d", deletes)
	}
}

func TestPortMapperPermanentLeaseRefreshes(t *testing.T) {
	gw := testGateway()
	gw.lease = 0
	m, _ := newTestMapper(&fakeDiscoverer{gw: gw})
	st := m.SetEnabled(context.Background(), true)
	if st.State != PortMapMapped || st.Lease != 0 || !st.ExpiresAt.IsZero() {
		t.Fatalf("permanent status = %+v", st)
	}
	if d := m.wait(); d != portMapPermanentRefresh {
		t.Fatalf("refresh in %v", d)
	}
}

func TestPortMapperCloseRemovesMapping(t *testing.T) {
	gw := testGateway()
	m, _ := newTestMapper(&fakeDiscoverer{gw: gw})
	m.SetEnabled(context.Background(), true)
	if err := m.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, deletes := gw.counts(); deletes != 1 {
		t.Fatalf("deletes = %d, want 1", deletes)
	}
	// Closed for good: a later enable sends nothing.
	m.SetEnabled(context.Background(), true)
	if adds, _ := gw.counts(); adds != 1 {
		t.Fatalf("closed mapper mapped again: %d", adds)
	}
}

func TestPortMapperCloseIsBoundedBySilentGateway(t *testing.T) {
	gw := testGateway()
	m, _ := newTestMapper(&fakeDiscoverer{gw: gw})
	m.SetEnabled(context.Background(), true)
	gw.mu.Lock()
	gw.deleteBlock = true
	gw.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := m.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close err = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %v", elapsed)
	}
}

func TestPortMapperCloseIsBoundedByInFlightRequest(t *testing.T) {
	gw := testGateway()
	gw.addBlock = make(chan struct{})
	gw.added = make(chan struct{}, 1)
	m, _ := newTestMapper(&fakeDiscoverer{gw: gw})
	runCtx, stopRun := context.WithCancel(context.Background())
	enabled := make(chan struct{})
	go func() {
		defer close(enabled)
		m.SetEnabled(runCtx, true)
	}()
	<-gw.added // the request now holds the op slot
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := m.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close err = %v, want deadline exceeded", err)
	}
	stopRun()
	<-enabled
	if st := m.Status(); st.State == PortMapMapped {
		t.Fatalf("mapped after Close: %+v", st)
	}
}

func TestPortMapperRunRenewsAndStops(t *testing.T) {
	gw := testGateway()
	gw.lease = 200 * time.Millisecond
	gw.added = make(chan struct{}, 4)
	m := NewPortMapper(&fakeDiscoverer{gw: gw}, 8443, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.lease = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Run(ctx)
	}()
	m.SetEnabled(context.Background(), true)
	for range 2 {
		select {
		case <-gw.added:
		case <-time.After(10 * time.Second):
			t.Fatal("Run never renewed")
		}
	}
	cancel()
	<-done
}
