package remoteaccess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"
)

// Automatic port mapping (CORE-1969). When the operator opts in, the server
// asks the home gateway for a TCP forward of its listening port over UPnP IGD
// or NAT-PMP (RFC 6886), renews it at half the granted lease, and deletes it
// on disable and on shutdown. Every gateway exchange sits behind Gateway and
// GatewayDiscoverer so tests drive the state machine with a fake gateway and
// never touch a real network.

// PortMapState is the honest mapping state surfaced to the operator Console.
type PortMapState string

const (
	// PortMapDisabled: the operator has not opted in, or turned it off.
	PortMapDisabled PortMapState = "disabled"
	// PortMapMapping: opted in, the first request has not finished yet.
	PortMapMapping PortMapState = "mapping"
	// PortMapMapped: the gateway granted a mapping whose lease is still live.
	PortMapMapped PortMapState = "mapped"
	// PortMapFailed: opted in, but no live mapping is held; Reason says why.
	PortMapFailed PortMapState = "failed"
)

// Port-mapping reason codes, a closed vocabulary the Console localizes.
const (
	PortMapReasonNoGateway      = "no_gateway"
	PortMapReasonGatewayRefused = "gateway_refused"
	PortMapReasonGatewayError   = "gateway_error"
	PortMapReasonLeaseExpired   = "lease_expired"
	// PortMapReasonRemovalFailed: the gateway did not confirm a removal; the
	// mapper keeps retrying it until it does.
	PortMapReasonRemovalFailed = "removal_failed"
)

// Gateway protocol names reported in PortMapStatus.Protocol.
const (
	PortMapProtocolUPnP   = "upnp"
	PortMapProtocolNATPMP = "natpmp"
)

// ErrNoGateway reports that no UPnP or NAT-PMP gateway answered.
var ErrNoGateway = errors.New("remoteaccess: no port-mapping gateway answered")

// GatewayRefusedError is a well-formed refusal from the gateway: a NAT-PMP
// result code or a UPnP SOAP fault errorCode.
type GatewayRefusedError struct {
	Protocol string
	Code     int
	Reason   string
}

func (e *GatewayRefusedError) Error() string {
	return fmt.Sprintf("remoteaccess: %s gateway refused the request (code %d: %s)", e.Protocol, e.Code, e.Reason)
}

// Gateway is one port-mapping capable router. Implementations bound every
// exchange by ctx.
type Gateway interface {
	// Protocol is PortMapProtocolUPnP or PortMapProtocolNATPMP.
	Protocol() string
	// ExternalAddress is the gateway's WAN address.
	ExternalAddress(ctx context.Context) (netip.Addr, error)
	// AddMapping requests (or renews) a TCP mapping. lifetime 0 asks for a
	// permanent mapping. It returns the external port and lease the gateway
	// actually granted; a granted lease of 0 means permanent.
	AddMapping(ctx context.Context, internalPort, externalPort uint16, lifetime time.Duration) (uint16, time.Duration, error)
	// DeleteMapping removes the TCP mapping.
	DeleteMapping(ctx context.Context, internalPort, externalPort uint16) error
}

// GatewayDiscoverer finds the local port-mapping gateway. It returns
// ErrNoGateway when nothing answers.
type GatewayDiscoverer interface {
	Discover(ctx context.Context) (Gateway, error)
}

// PortMapStatus is a snapshot of the mapper for the Console.
type PortMapStatus struct {
	Enabled         bool
	State           PortMapState
	Protocol        string
	InternalPort    uint16
	ExternalPort    uint16
	ExternalAddress netip.Addr
	// Lease is the granted lease; 0 with State mapped means permanent.
	Lease time.Duration
	// ExpiresAt is when the lease runs out; zero for a permanent lease.
	ExpiresAt time.Time
	// Reason is a PortMapReason* code when State is failed, or the reason of
	// the last failed renewal while a live mapping is still held.
	Reason string
	// GatewayCode is the gateway's refusal code for PortMapReasonGatewayRefused.
	GatewayCode int
}

const (
	portMapRequestedLease   = 2 * time.Hour
	portMapPermanentRefresh = 30 * time.Minute
	portMapRetryMin         = 30 * time.Second
	portMapRetryMax         = 10 * time.Minute
	portMapOpTimeout        = 15 * time.Second
	portMapIdleWait         = time.Hour
)

// PortMapper owns the opt-in mapping lifecycle for one internal port.
type PortMapper struct {
	discover     GatewayDiscoverer
	internalPort uint16
	lease        time.Duration
	now          func() time.Time
	logger       *slog.Logger

	// op serializes gateway exchanges. It is a one-slot semaphore rather than
	// a mutex so Close can give up on a held slot when its deadline passes.
	op   chan struct{}
	wake chan struct{}

	mu       sync.Mutex
	enabled  bool
	closed   bool
	gw       Gateway
	status   PortMapStatus
	next     time.Time // next renew or retry; zero means due now
	failures int

	// orphan is a mapping whose removal the gateway did not confirm. It is
	// retried with backoff, enabled or not, and once more on Close, so a
	// failed delete never leaves a public port open unattended.
	orphanGW       Gateway
	orphanExt      uint16
	orphanNext     time.Time
	orphanFailures int
}

// NewPortMapper builds a mapper for internalPort. It starts disabled; nothing
// is sent until SetEnabled(true).
func NewPortMapper(discover GatewayDiscoverer, internalPort uint16, logger *slog.Logger) *PortMapper {
	if logger == nil {
		logger = slog.Default()
	}
	return &PortMapper{
		discover:     discover,
		internalPort: internalPort,
		lease:        portMapRequestedLease,
		now:          time.Now,
		logger:       logger,
		op:           make(chan struct{}, 1),
		wake:         make(chan struct{}, 1),
		status:       PortMapStatus{State: PortMapDisabled, InternalPort: internalPort},
	}
}

func (m *PortMapper) lockOp(ctx context.Context) error {
	select {
	case m.op <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *PortMapper) unlockOp() { <-m.op }

// Status returns the current snapshot.
func (m *PortMapper) Status() PortMapStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// SetEnabled turns the mapping on or off. Enabling requests the mapping
// before returning; disabling deletes a held mapping from the gateway. ctx
// bounds the gateway exchange.
func (m *PortMapper) SetEnabled(ctx context.Context, enabled bool) PortMapStatus {
	if err := m.lockOp(ctx); err != nil {
		return m.Status()
	}
	defer m.unlockOp()
	m.mu.Lock()
	if m.closed {
		st := m.status
		m.mu.Unlock()
		return st
	}
	was := m.enabled
	m.enabled = enabled
	if enabled && !was {
		m.status = PortMapStatus{Enabled: true, State: PortMapMapping, InternalPort: m.internalPort}
	}
	m.mu.Unlock()
	if enabled {
		m.attempt(ctx)
	} else {
		err := m.release(ctx)
		m.mu.Lock()
		m.gw = nil
		m.next = time.Time{}
		m.failures = 0
		m.status = PortMapStatus{State: PortMapDisabled, InternalPort: m.internalPort}
		if err != nil {
			m.status.Reason = PortMapReasonRemovalFailed
		}
		m.mu.Unlock()
	}
	m.signal()
	return m.Status()
}

// Maintain runs whatever is due: expiry of a lease that was never renewed,
// then a renewal or retry once its time has come. It returns how long the
// caller may wait before calling again.
func (m *PortMapper) Maintain(ctx context.Context) time.Duration {
	if err := m.lockOp(ctx); err != nil {
		return portMapRetryMin
	}
	defer m.unlockOp()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return portMapIdleWait
	}
	now := m.now()
	orphanDue := m.orphanGW != nil && !now.Before(m.orphanNext)
	m.mu.Unlock()
	if orphanDue {
		m.retryOrphan(ctx)
	}
	m.mu.Lock()
	if !m.enabled {
		m.mu.Unlock()
		return m.wait()
	}
	if m.status.State == PortMapMapped && !m.status.ExpiresAt.IsZero() && !now.Before(m.status.ExpiresAt) {
		m.status.State = PortMapFailed
		m.status.Reason = PortMapReasonLeaseExpired
		m.status.GatewayCode = 0
		m.status.ExternalPort = 0
		m.status.Lease = 0
		m.status.ExpiresAt = time.Time{}
		m.logger.Warn("port mapping lease expired without a renewal", "internal_port", m.internalPort)
	}
	due := m.next.IsZero() || !now.Before(m.next)
	m.mu.Unlock()
	if due {
		m.attempt(ctx)
	}
	return m.wait()
}

// wait is the time until the next scheduled renewal, retry or expiry.
func (m *PortMapper) wait() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return portMapIdleWait
	}
	var at time.Time
	if m.enabled {
		if m.next.IsZero() {
			return 0
		}
		at = m.next
	} else if m.orphanGW == nil {
		return portMapIdleWait
	}
	if m.orphanGW != nil && (at.IsZero() || m.orphanNext.Before(at)) {
		at = m.orphanNext
	}
	if exp := m.status.ExpiresAt; m.status.State == PortMapMapped && !exp.IsZero() && (at.IsZero() || exp.Before(at)) {
		at = exp
	}
	if at.IsZero() {
		return 0
	}
	d := at.Sub(m.now())
	if d < 0 {
		return 0
	}
	return d
}

// Run maintains the mapping until ctx ends.
func (m *PortMapper) Run(ctx context.Context) {
	for {
		d := m.Maintain(ctx)
		timer := time.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// Close deletes a held mapping and stops the mapper for good. The caller
// bounds it with ctx: a gateway that never answers, or an exchange still in
// flight, cannot hold shutdown past the deadline.
func (m *PortMapper) Close(ctx context.Context) error {
	if err := m.lockOp(ctx); err != nil {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		return fmt.Errorf("remoteaccess: port mapping removal: %w", err)
	}
	defer m.unlockOp()
	err := errors.Join(m.release(ctx), m.releaseOrphan(ctx))
	m.mu.Lock()
	m.closed = true
	m.next = time.Time{}
	m.status.State = PortMapDisabled
	m.status.ExternalPort = 0
	m.status.Lease = 0
	m.status.ExpiresAt = time.Time{}
	m.mu.Unlock()
	m.signal()
	return err
}

func (m *PortMapper) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// release deletes the held mapping, if any. Called with the op slot held.
func (m *PortMapper) release(ctx context.Context) error {
	m.mu.Lock()
	gw, ext, held := m.gw, m.status.ExternalPort, m.status.State == PortMapMapped
	m.mu.Unlock()
	if gw == nil || !held || ext == 0 {
		return nil
	}
	if err := gw.DeleteMapping(ctx, m.internalPort, ext); err != nil {
		m.logger.Warn("port mapping removal failed; will retry", "protocol", gw.Protocol(), "error", err)
		m.mu.Lock()
		m.orphanGW, m.orphanExt = gw, ext
		m.orphanFailures = 0
		m.orphanNext = m.now().Add(portMapRetryMin)
		m.mu.Unlock()
		return err
	}
	m.logger.Info("port mapping removed", "protocol", gw.Protocol(), "external_port", ext)
	m.mu.Lock()
	if m.orphanExt == ext {
		m.clearOrphanLocked()
	}
	m.mu.Unlock()
	return nil
}

// releaseOrphan deletes a mapping whose earlier removal failed. Called with
// the op slot held. A live mapping on the same external port supersedes the
// orphan rather than being deleted.
func (m *PortMapper) releaseOrphan(ctx context.Context) error {
	m.mu.Lock()
	gw, ext := m.orphanGW, m.orphanExt
	live := m.status.State == PortMapMapped && m.status.ExternalPort == ext
	if gw != nil && live {
		m.clearOrphanLocked()
	}
	m.mu.Unlock()
	if gw == nil || live {
		return nil
	}
	if err := gw.DeleteMapping(ctx, m.internalPort, ext); err != nil {
		m.logger.Warn("port mapping removal retry failed", "protocol", gw.Protocol(), "external_port", ext, "error", err)
		return err
	}
	m.logger.Info("port mapping removed", "protocol", gw.Protocol(), "external_port", ext)
	m.mu.Lock()
	if m.orphanExt == ext {
		m.clearOrphanLocked()
	}
	m.mu.Unlock()
	return nil
}

// retryOrphan runs one bounded removal retry and schedules the next one.
func (m *PortMapper) retryOrphan(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, portMapOpTimeout)
	defer cancel()
	if m.releaseOrphan(ctx) == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orphanFailures++
	backoff := portMapRetryMax
	if shift := m.orphanFailures; shift < 5 {
		backoff = min(portMapRetryMin<<shift, portMapRetryMax)
	}
	m.orphanNext = m.now().Add(backoff)
}

// clearOrphanLocked forgets the orphan; m.mu must be held.
func (m *PortMapper) clearOrphanLocked() {
	m.orphanGW, m.orphanExt = nil, 0
	m.orphanNext, m.orphanFailures = time.Time{}, 0
	if !m.enabled && m.status.Reason == PortMapReasonRemovalFailed {
		m.status.Reason = ""
	}
}

// attempt requests or renews the mapping. Called with the op slot held.
func (m *PortMapper) attempt(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, portMapOpTimeout)
	defer cancel()

	m.mu.Lock()
	gw := m.gw
	ext := m.status.ExternalPort
	m.mu.Unlock()
	if ext == 0 {
		ext = m.internalPort
	}
	if gw == nil {
		found, err := m.discover.Discover(ctx)
		if err != nil {
			m.fail(err)
			return
		}
		gw = found
	}
	granted, lease, err := gw.AddMapping(ctx, m.internalPort, ext, m.lease)
	if err != nil {
		m.fail(err)
		return
	}
	addr, addrErr := gw.ExternalAddress(ctx)
	if addrErr != nil {
		m.logger.Warn("port mapping external address unavailable", "protocol", gw.Protocol(), "error", addrErr)
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gw = gw
	m.failures = 0
	if m.orphanGW != nil && m.orphanExt == granted {
		// The new grant took the orphan's port: it is live again, not stale.
		m.clearOrphanLocked()
	}
	m.status = PortMapStatus{
		Enabled:         true,
		State:           PortMapMapped,
		Protocol:        gw.Protocol(),
		InternalPort:    m.internalPort,
		ExternalPort:    granted,
		ExternalAddress: addr,
		Lease:           lease,
	}
	if lease > 0 {
		m.status.ExpiresAt = now.Add(lease)
		m.next = now.Add(lease / 2)
	} else {
		m.next = now.Add(portMapPermanentRefresh)
	}
	m.logger.Info("port mapping granted", "protocol", gw.Protocol(), "external_port", granted, "lease", lease)
}

// fail records a failed request. A live lease stays reported as mapped until
// it runs out; otherwise the state becomes failed with a reason code.
func (m *PortMapper) fail(err error) {
	reason, code := portMapReason(err)
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures++
	backoff := portMapRetryMax
	if shift := m.failures - 1; shift < 5 {
		backoff = min(portMapRetryMin<<shift, portMapRetryMax)
	}
	m.next = now.Add(backoff)
	m.status.Enabled = true
	if m.status.State != PortMapFailed || m.status.Reason != PortMapReasonLeaseExpired {
		// An expired lease keeps saying so while the retries continue; the
		// first success or a disable clears it.
		m.status.Reason = reason
		m.status.GatewayCode = code
	}
	live := m.status.State == PortMapMapped && (m.status.ExpiresAt.IsZero() || now.Before(m.status.ExpiresAt))
	if !live {
		m.status.State = PortMapFailed
		m.status.Protocol = ""
		m.status.ExternalPort = 0
		m.status.ExternalAddress = netip.Addr{}
		m.status.Lease = 0
		m.status.ExpiresAt = time.Time{}
	}
	if reason != PortMapReasonGatewayRefused && !live {
		// Rediscover next time: the gateway may have restarted or changed.
		// A live mapping keeps its gateway so disable and shutdown can
		// still delete it.
		m.gw = nil
	}
	m.logger.Warn("port mapping request failed", "reason", reason, "code", code, "error", err, "retry_in", backoff)
}

func portMapReason(err error) (string, int) {
	var refused *GatewayRefusedError
	switch {
	case errors.As(err, &refused):
		return PortMapReasonGatewayRefused, refused.Code
	case errors.Is(err, ErrNoGateway):
		return PortMapReasonNoGateway, 0
	default:
		return PortMapReasonGatewayError, 0
	}
}
