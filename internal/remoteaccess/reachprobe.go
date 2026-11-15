package remoteaccess

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Reachability self-test (CORE-1969). The server dials its own public path
// and sends nothing but a TLS ClientHello whose ALPN list carries a one-time
// nonce. Its own TLS listener (wrapped by ProbeResponder) recognises the
// nonce, records it, and aborts the handshake before any certificate, request
// or response is exchanged. No library data, request or credential ever
// travels. The relay path is tested through the relay's public host: the
// relay routes by SNI and forwards the ClientHello byte-blind like any other
// public connection (ADR-0018), so no relay-side probe protocol is needed.
//
// The direct check depends on the gateway looping a connection to its own
// public address back to the LAN (NAT hairpin). A gateway without hairpin
// reports timeout or connection_refused even when outside clients could
// connect.

// Reachability paths.
const (
	ReachPathDirect = "direct"
	ReachPathRelay  = "relay"
)

// Reachability reason codes, a closed vocabulary the Console localizes.
const (
	ReachReasonNotMapped          = "not_mapped"
	ReachReasonDoubleNAT          = "double_nat"
	ReachReasonConnectionRefused  = "connection_refused"
	ReachReasonTimeout            = "timeout"
	ReachReasonWrongResponder     = "wrong_responder"
	ReachReasonRelayNotConfigured = "relay_not_configured"
	ReachReasonRelayDisconnected  = "relay_disconnected"
	ReachReasonProbeError         = "probe_error"
)

const (
	reachALPNPrefix  = "beamfall-reach/"
	reachTimeout     = 5 * time.Second
	reachAlertGrace  = 250 * time.Millisecond
	relayDefaultPort = "443"
)

var errReachProbeSeen = errors.New("remoteaccess: reachability probe received")

// ReachVerdict is the outcome for one path.
type ReachVerdict struct {
	Path      string
	Reachable bool
	// Reason is a ReachReason* code when Reachable is false.
	Reason string
}

// ReachReport is one self-test run over both paths.
type ReachReport struct {
	CheckedAt time.Time
	Direct    ReachVerdict
	Relay     ReachVerdict
}

// ProbeResponder is the listener half: it recognises probe nonces in the
// ClientHello and aborts those handshakes.
type ProbeResponder struct {
	mu      sync.Mutex
	pending map[string]chan struct{}
}

// NewProbeResponder returns an empty responder.
func NewProbeResponder() *ProbeResponder {
	return &ProbeResponder{pending: map[string]chan struct{}{}}
}

// WrapTLSConfig returns a clone of base that aborts any handshake whose
// ClientHello offers a reachability-probe ALPN, and otherwise delegates to
// base's own GetConfigForClient (SNI composition, ACME) unchanged.
func (r *ProbeResponder) WrapTLSConfig(base *tls.Config) *tls.Config {
	if base == nil {
		return nil
	}
	wrapped := base.Clone()
	inner := base.GetConfigForClient
	wrapped.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if r.observe(hello.SupportedProtos) {
			return nil, errReachProbeSeen
		}
		if inner != nil {
			return inner(hello)
		}
		return nil, nil
	}
	return wrapped
}

// observe reports whether protos carry a probe ALPN, signalling a pending
// nonce. An unknown nonce is still a probe and is still aborted.
func (r *ProbeResponder) observe(protos []string) bool {
	probe := false
	for _, p := range protos {
		nonce, ok := strings.CutPrefix(p, reachALPNPrefix)
		if !ok {
			continue
		}
		probe = true
		r.mu.Lock()
		if ch, pending := r.pending[nonce]; pending {
			delete(r.pending, nonce)
			close(ch)
		}
		r.mu.Unlock()
	}
	return probe
}

func (r *ProbeResponder) register(nonce string) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	r.mu.Lock()
	r.pending[nonce] = ch
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.pending, nonce)
		r.mu.Unlock()
	}
}

// ReachProber is the sending half.
type ReachProber struct {
	responder *ProbeResponder
	dial      func(ctx context.Context, addr string) (net.Conn, error)
	timeout   time.Duration
	now       func() time.Time
}

// NewReachProber returns a prober whose probes the responder recognises.
func NewReachProber(responder *ProbeResponder) *ReachProber {
	return &ReachProber{
		responder: responder,
		dial: func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
		timeout: reachTimeout,
		now:     time.Now,
	}
}

// ReachInputs is what the self-test needs to know about each path.
type ReachInputs struct {
	Mapping         PortMapStatus
	RelayConfigured bool
	RelayConnected  bool
	RelayPublicHost string
}

// Check probes both paths concurrently.
func (p *ReachProber) Check(ctx context.Context, in ReachInputs) ReachReport {
	report := ReachReport{CheckedAt: p.now().UTC()}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		report.Direct = p.checkDirect(ctx, in.Mapping)
	}()
	go func() {
		defer wg.Done()
		report.Relay = p.checkRelay(ctx, in)
	}()
	wg.Wait()
	return report
}

func (p *ReachProber) checkDirect(ctx context.Context, m PortMapStatus) ReachVerdict {
	v := ReachVerdict{Path: ReachPathDirect}
	if m.State != PortMapMapped || m.ExternalPort == 0 {
		v.Reason = ReachReasonNotMapped
		return v
	}
	addr := m.ExternalAddress.Unmap()
	if !addr.IsValid() {
		v.Reason = ReachReasonProbeError
		return v
	}
	if !publicAddr(addr) {
		v.Reason = ReachReasonDoubleNAT
		return v
	}
	v.Reachable, v.Reason = p.Probe(ctx, netip.AddrPortFrom(addr, m.ExternalPort).String(), addr.String())
	return v
}

func (p *ReachProber) checkRelay(ctx context.Context, in ReachInputs) ReachVerdict {
	v := ReachVerdict{Path: ReachPathRelay}
	host := strings.TrimSpace(in.RelayPublicHost)
	switch {
	case !in.RelayConfigured || host == "":
		v.Reason = ReachReasonRelayNotConfigured
		return v
	case !in.RelayConnected:
		v.Reason = ReachReasonRelayDisconnected
		return v
	}
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		name, port = host, relayDefaultPort
	}
	v.Reachable, v.Reason = p.Probe(ctx, net.JoinHostPort(name, port), name)
	return v
}

// Probe sends one nonce ClientHello to addr with SNI serverName and reports
// whether this server's own listener received it.
func (p *ReachProber) Probe(ctx context.Context, addr, serverName string) (bool, string) {
	nonce, err := reachNonce()
	if err != nil {
		return false, ReachReasonProbeError
	}
	seen, done := p.responder.register(nonce)
	defer done()

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	conn, err := p.dial(ctx, addr)
	if err != nil {
		return false, dialReason(err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	client := tls.Client(conn, &tls.Config{
		ServerName: serverName,
		NextProtos: []string{reachALPNPrefix + nonce},
		MinVersion: tls.VersionTLS12,
	})
	herr := client.HandshakeContext(ctx)
	select {
	case <-seen:
		return true, ""
	default:
	}
	if herr == nil {
		// Something completed a handshake with us: not this server, which
		// always aborts a probe.
		return false, ReachReasonWrongResponder
	}
	// The responder records the nonce before it sends the abort alert, so a
	// short grace covers scheduling between the two.
	grace := time.NewTimer(reachAlertGrace)
	defer grace.Stop()
	select {
	case <-seen:
		return true, ""
	case <-grace.C:
	}
	if ctx.Err() != nil || isTimeout(herr) {
		return false, ReachReasonTimeout
	}
	return false, ReachReasonWrongResponder
}

func dialReason(err error) string {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return ReachReasonConnectionRefused
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return ReachReasonTimeout
	default:
		return ReachReasonProbeError
	}
}

// publicAddr is false for private, shared (RFC 6598 carrier-grade NAT),
// loopback, link-local and unspecified addresses: a gateway reporting one of
// those sits behind another NAT, so a mapping on it is not reachable from the
// internet.
func publicAddr(a netip.Addr) bool {
	if a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() || a.IsMulticast() {
		return false
	}
	return !cgnatPrefix.Contains(a)
}

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

func reachNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
