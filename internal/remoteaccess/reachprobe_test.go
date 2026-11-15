package remoteaccess

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// recordingConn keeps every byte the prober writes so a test can prove the
// probe carries nothing but TLS handshake and alert records.
type recordingConn struct {
	net.Conn
	mu      sync.Mutex
	written []byte
}

func (c *recordingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.written = append(c.written, p...)
	c.mu.Unlock()
	return c.Conn.Write(p)
}

func (c *recordingConn) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.written...)
}

// tlsRecordTypes splits a byte stream into TLS record content types.
func tlsRecordTypes(t *testing.T, b []byte) []byte {
	t.Helper()
	var types []byte
	for len(b) > 0 {
		if len(b) < 5 {
			t.Fatalf("truncated TLS record header: % x", b)
		}
		n := int(b[3])<<8 | int(b[4])
		if len(b) < 5+n {
			t.Fatalf("truncated TLS record body")
		}
		types = append(types, b[0])
		b = b[5+n:]
	}
	return types
}

// pipeProber wires the prober's dial to an in-memory server that runs
// serve on its end of the pipe.
func pipeProber(responder *ProbeResponder, serve func(net.Conn)) (*ReachProber, *[]string, func() *recordingConn) {
	p := NewReachProber(responder)
	p.timeout = 2 * time.Second
	var mu sync.Mutex
	var addrs []string
	var last *recordingConn
	p.dial = func(_ context.Context, addr string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			serve(server)
		}()
		rc := &recordingConn{Conn: client}
		mu.Lock()
		addrs = append(addrs, addr)
		last = rc
		mu.Unlock()
		return rc, nil
	}
	return p, &addrs, func() *recordingConn {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// serveTLS runs this server's TLS handshake with the responder installed.
func serveTLS(responder *ProbeResponder, sni *string) func(net.Conn) {
	base := &tls.Config{MinVersion: tls.VersionTLS12}
	wrapped := responder.WrapTLSConfig(base)
	return func(c net.Conn) {
		cfg := wrapped.Clone()
		inner := wrapped.GetConfigForClient
		cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if sni != nil {
				*sni = hello.ServerName
			}
			return inner(hello)
		}
		_ = tls.Server(c, cfg).Handshake()
	}
}

var mappedStatus = PortMapStatus{
	Enabled:         true,
	State:           PortMapMapped,
	ExternalPort:    18443,
	ExternalAddress: netip.MustParseAddr("203.0.113.7"),
}

func TestReachDirectReachableSendsOnlyAClientHello(t *testing.T) {
	responder := NewProbeResponder()
	p, addrs, last := pipeProber(responder, serveTLS(responder, nil))
	report := p.Check(context.Background(), ReachInputs{Mapping: mappedStatus})
	if !report.Direct.Reachable || report.Direct.Reason != "" || report.Direct.Path != ReachPathDirect {
		t.Fatalf("direct = %+v, want reachable", report.Direct)
	}
	if report.Direct.Method != ReachMethodGatewayLoopback {
		t.Fatalf("direct method = %q, want gateway_loopback: a pass is not an outside test", report.Direct.Method)
	}
	if got := (*addrs)[0]; got != "203.0.113.7:18443" {
		t.Fatalf("dialed %q", got)
	}
	for _, typ := range tlsRecordTypes(t, last().bytes()) {
		if typ != 0x16 && typ != 0x15 {
			t.Fatalf("probe wrote a TLS record of type %#x; only handshake and alert are allowed", typ)
		}
	}
	if len(responder.pending) != 0 {
		t.Fatalf("nonce left pending: %d", len(responder.pending))
	}
}

func TestReachRelayGoesThroughPublicHost(t *testing.T) {
	responder := NewProbeResponder()
	var sni string
	p, addrs, _ := pipeProber(responder, serveTLS(responder, &sni))
	report := p.Check(context.Background(), ReachInputs{
		RelayConfigured: true,
		RelayConnected:  true,
		RelayPublicHost: "media.example.test",
	})
	if !report.Relay.Reachable || report.Relay.Method != ReachMethodRelay {
		t.Fatalf("relay = %+v, want reachable via relay", report.Relay)
	}
	if got := (*addrs)[0]; got != "media.example.test:443" {
		t.Fatalf("dialed %q, want the relay public host on 443", got)
	}
	if sni != "media.example.test" {
		t.Fatalf("SNI = %q, want the public host the relay routes by", sni)
	}
	if report.Direct.Reason != ReachReasonNotMapped {
		t.Fatalf("direct = %+v, want not_mapped", report.Direct)
	}
}

func TestReachWrongResponder(t *testing.T) {
	ours := NewProbeResponder()
	someoneElse := NewProbeResponder()
	p, _, _ := pipeProber(ours, serveTLS(someoneElse, nil))
	if ok, reason := p.Probe(context.Background(), "203.0.113.7:18443", "203.0.113.7"); ok || reason != ReachReasonWrongResponder {
		t.Fatalf("probe = %v %q, want wrong_responder", ok, reason)
	}

	closer, _, _ := pipeProber(ours, func(c net.Conn) {
		buf := make([]byte, 5)
		_, _ = io.ReadFull(c, buf)
	})
	if ok, reason := closer.Probe(context.Background(), "203.0.113.7:18443", "203.0.113.7"); ok || reason != ReachReasonWrongResponder {
		t.Fatalf("hang-up probe = %v %q, want wrong_responder", ok, reason)
	}
}

func TestReachTimeout(t *testing.T) {
	responder := NewProbeResponder()
	hold := make(chan struct{})
	defer close(hold)
	p, _, _ := pipeProber(responder, func(c net.Conn) {
		go func() { _, _ = io.Copy(io.Discard, c) }()
		<-hold
	})
	p.timeout = 50 * time.Millisecond
	if ok, reason := p.Probe(context.Background(), "203.0.113.7:18443", "203.0.113.7"); ok || reason != ReachReasonTimeout {
		t.Fatalf("probe = %v %q, want timeout", ok, reason)
	}
}

func TestReachDialFailures(t *testing.T) {
	p := NewReachProber(NewProbeResponder())
	cases := map[string]error{
		ReachReasonConnectionRefused: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)},
		ReachReasonTimeout:           context.DeadlineExceeded,
		ReachReasonProbeError:        &net.DNSError{Err: "no such host", Name: "relay.example.test", IsNotFound: true},
	}
	for want, err := range cases {
		p.dial = func(context.Context, string) (net.Conn, error) { return nil, err }
		if ok, reason := p.Probe(context.Background(), "203.0.113.7:18443", "x"); ok || reason != want {
			t.Fatalf("dial error %v: probe = %v %q, want %q", err, ok, reason, want)
		}
	}
}

// A gateway without NAT hairpin refuses or drops the inside-out probe even
// when the mapping works from outside, so the direct path must not report
// those as a closed port. The relay path keeps the plain codes.
func TestReachDirectFailureIsHairpinUnverified(t *testing.T) {
	cases := map[string]error{
		"refused": &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)},
		"timeout": context.DeadlineExceeded,
	}
	relay := ReachInputs{RelayConfigured: true, RelayConnected: true, RelayPublicHost: "media.example.test"}
	for name, err := range cases {
		p := NewReachProber(NewProbeResponder())
		p.dial = func(context.Context, string) (net.Conn, error) { return nil, err }
		in := relay
		in.Mapping = mappedStatus
		report := p.Check(context.Background(), in)
		if d := report.Direct; d.Reachable || d.Reason != ReachReasonHairpinUnverified || d.Method != ReachMethodGatewayLoopback {
			t.Fatalf("%s: direct = %+v, want hairpin_unverified via gateway_loopback", name, d)
		}
		want := map[string]string{"refused": ReachReasonConnectionRefused, "timeout": ReachReasonTimeout}[name]
		if r := report.Relay; r.Reachable || r.Reason != want || r.Method != ReachMethodRelay {
			t.Fatalf("%s: relay = %+v, want %q via relay", name, r, want)
		}
	}
}

func TestReachDirectPreconditions(t *testing.T) {
	p := NewReachProber(NewProbeResponder())
	p.dial = func(context.Context, string) (net.Conn, error) {
		t.Fatal("dialed although the verdict was decided without a probe")
		return nil, nil
	}
	cases := []struct {
		name   string
		status PortMapStatus
		want   string
	}{
		{"disabled", PortMapStatus{State: PortMapDisabled}, ReachReasonNotMapped},
		{"failed", PortMapStatus{State: PortMapFailed, Reason: PortMapReasonGatewayRefused}, ReachReasonNotMapped},
		{"cgnat", withAddr("100.64.12.3"), ReachReasonDoubleNAT},
		{"private", withAddr("192.168.0.2"), ReachReasonDoubleNAT},
		{"unknown address", withAddr(""), ReachReasonProbeError},
	}
	for _, tc := range cases {
		v := p.checkDirect(context.Background(), tc.status)
		if v.Reachable || v.Reason != tc.want || v.Method != "" {
			t.Fatalf("%s: verdict = %+v, want %q with no probe method", tc.name, v, tc.want)
		}
	}
}

func withAddr(a string) PortMapStatus {
	st := mappedStatus
	st.ExternalAddress = netip.Addr{}
	if a != "" {
		st.ExternalAddress = netip.MustParseAddr(a)
	}
	return st
}

func TestReachRelayPreconditions(t *testing.T) {
	p := NewReachProber(NewProbeResponder())
	if v := p.checkRelay(context.Background(), ReachInputs{}); v.Reason != ReachReasonRelayNotConfigured {
		t.Fatalf("unconfigured relay = %+v", v)
	}
	if v := p.checkRelay(context.Background(), ReachInputs{RelayConfigured: true, RelayPublicHost: "media.example.test"}); v.Reason != ReachReasonRelayDisconnected {
		t.Fatalf("disconnected relay = %+v", v)
	}
}

func TestProbeResponderDelegatesOrdinaryHandshakes(t *testing.T) {
	responder := NewProbeResponder()
	called := 0
	base := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			called++
			return nil, nil
		},
	}
	wrapped := responder.WrapTLSConfig(base)
	if _, err := wrapped.GetConfigForClient(&tls.ClientHelloInfo{SupportedProtos: []string{"h2", "http/1.1"}}); err != nil || called != 1 {
		t.Fatalf("ordinary hello: err=%v inner calls=%d", err, called)
	}
	if _, err := wrapped.GetConfigForClient(&tls.ClientHelloInfo{SupportedProtos: []string{reachALPNPrefix + "unknown"}}); err == nil || called != 1 {
		t.Fatalf("probe hello: err=%v inner calls=%d, want aborted without delegation", err, called)
	}
	if base.GetConfigForClient == nil || strings.Contains(base.ServerName, "reach") {
		t.Fatal("base config was mutated")
	}
	if responder.WrapTLSConfig(nil) != nil {
		t.Fatal("nil config should stay nil")
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"203.0.113.7":   true,
		"198.51.100.1":  true,
		"10.1.2.3":      false,
		"172.16.0.1":    false,
		"100.64.0.1":    false,
		"100.127.255.1": false,
		"169.254.1.1":   false,
		"0.0.0.0":       false,
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Fatalf("publicAddr(%s) = %v", addr, got)
		}
	}
}
