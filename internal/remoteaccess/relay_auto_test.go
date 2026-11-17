package remoteaccess

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/quic-go/quic-go"

	"github.com/beamfall/core/internal/config"
)

// autoRelay is a RelayClient on the automatic transport (CORE-2283) whose
// TLS/TCP side dials a stub relay that acknowledges every carrier and whose
// QUIC side is a test hook that fails by default, as when UDP does not reach
// the relay.
type autoRelay struct {
	client    *RelayClient
	quicDials atomic.Int32

	mu        sync.Mutex
	tcpTokens [][]byte
}

func newAutoRelay(t *testing.T) *autoRelay {
	t.Helper()
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")
	h := &autoRelay{}
	relay := startStubRelay(t, func(conn net.Conn) (*yamux.Session, bool) {
		frame, err := readRelayFrame(conn)
		if err != nil || frame.Type != relayFrameHello || frame.Hello == nil {
			return nil, false
		}
		h.mu.Lock()
		h.tcpTokens = append(h.tcpTokens, frame.Hello.Token)
		h.mu.Unlock()
		_ = writeRelayFrame(conn, relayFrame{Type: relayFrameAck, Ack: &relayAck{
			Version: relayProtoVersion, CarrierID: "carrier-tcp", Hostnames: frame.Hello.Hostnames,
		}})
		s, _ := yamux.Server(conn, relayYamuxConfig(0))
		return s, true
	})
	h.client = newDialableRelay(t, relay.addr, dir)
	h.client.cfg.Transport = config.RelayTransportAuto
	h.client.dialQUIC = func(context.Context, string) (*quic.Conn, error) {
		h.quicDials.Add(1)
		return nil, errors.New("udp does not reach the relay")
	}
	return h
}

func (h *autoRelay) connect(t *testing.T) {
	t.Helper()
	session, err := h.client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
}

func (h *autoRelay) tokens() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]byte(nil), h.tcpTokens...)
}

// useQUICRelay points the client's QUIC side at a real QUIC listener that
// hands each carrier's first stream and Hello to answer, and returns the
// tokens it received.
func (h *autoRelay) useQUICRelay(t *testing.T, answer func(conn *quic.Conn, stream io.ReadWriter, hello *relayHello)) <-chan []byte {
	t.Helper()
	cert := selfSignedCert(t)
	listener, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{relayQUICALPN},
		MinVersion:   tls.VersionTLS13,
	}, relayQUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	tokens := make(chan []byte, 4)
	go func() {
		for {
			conn, aerr := listener.Accept(context.Background())
			if aerr != nil {
				return
			}
			stream, aerr := conn.AcceptStream(context.Background())
			if aerr != nil {
				continue
			}
			frame, aerr := readRelayFrame(stream)
			if aerr != nil || frame.Type != relayFrameHello || frame.Hello == nil {
				continue
			}
			tokens <- frame.Hello.Token
			answer(conn, stream, frame.Hello)
		}
	}()
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	addr := listener.Addr().String()
	h.client.dialQUIC = func(ctx context.Context, _ string) (*quic.Conn, error) {
		h.quicDials.Add(1)
		return quic.DialAddr(ctx, addr, &tls.Config{
			ServerName: "relay.example.com", RootCAs: roots, NextProtos: []string{relayQUICALPN}, MinVersion: tls.VersionTLS13,
		}, relayQUICConfig())
	}
	return tokens
}

// feature:self-hosted-relay — the default transport connects over TLS/TCP when
// UDP does not reach the relay, in the same Connect.
func TestRelayAutoFallsBackToTCPWhenQUICIsUnreachable(t *testing.T) {
	h := newAutoRelay(t)
	h.connect(t)

	if got := h.quicDials.Load(); got != 1 {
		t.Fatalf("QUIC dials = %d, want 1", got)
	}
	if got := h.client.Transport(); got != config.RelayTransportTCP {
		t.Fatalf("Transport() = %q, want %q", got, config.RelayTransportTCP)
	}
	if state, _ := h.client.Status(); state != RelayConnected {
		t.Fatalf("state = %q, want connected", state)
	}
	tokens := h.tokens()
	if len(tokens) != 1 {
		t.Fatalf("TLS/TCP carriers = %d, want 1", len(tokens))
	}
	assertMintedRelayToken(t, "carrier-secret", []string{"remote.example.com"}, tokens[0])
}

// The relay refuses a replayed nonce, so a token it may have seen over QUIC
// must not be the one sent over TLS/TCP.
func TestRelayAutoFallbackSendsAFreshToken(t *testing.T) {
	h := newAutoRelay(t)
	h.client.quicProbeBudget = 200 * time.Millisecond
	quicTokens := h.useQUICRelay(t, func(*quic.Conn, io.ReadWriter, *relayHello) {
		// The Hello arrived and then the path died: no answer within the budget.
	})
	h.connect(t)

	var overQUIC []byte
	select {
	case overQUIC = <-quicTokens:
	case <-time.After(2 * time.Second):
		t.Fatal("the QUIC relay never received the hello")
	}
	tokens := h.tokens()
	if len(tokens) != 1 {
		t.Fatalf("TLS/TCP carriers = %d, want 1", len(tokens))
	}
	if bytes.Equal(tokens[0], overQUIC) {
		t.Fatal("the TLS/TCP hello replayed the token already sent over QUIC")
	}
	assertMintedRelayToken(t, "carrier-secret", []string{"remote.example.com"}, tokens[0])
}

func TestRelayAutoFallsBackWhenQUICGetsNoAnswer(t *testing.T) {
	h := newAutoRelay(t)
	h.client.quicProbeBudget = 30 * time.Millisecond
	h.client.dialQUIC = func(ctx context.Context, _ string) (*quic.Conn, error) {
		h.quicDials.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	h.connect(t)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("fallback took %v, want about the 30ms probe budget", elapsed)
	}
	if got := h.client.Transport(); got != config.RelayTransportTCP {
		t.Fatalf("Transport() = %q, want %q", got, config.RelayTransportTCP)
	}
}

func TestRelayAutoSkipsQUICWhileHeldOff(t *testing.T) {
	h := newAutoRelay(t)
	h.connect(t)
	h.connect(t)

	if got := h.quicDials.Load(); got != 1 {
		t.Fatalf("QUIC dials after a second Connect = %d, want 1 (held off)", got)
	}
	if got := len(h.tokens()); got != 2 {
		t.Fatalf("TLS/TCP carriers = %d, want 2", got)
	}
}

func TestRelayAutoTriesQUICAgainAfterHoldOff(t *testing.T) {
	h := newAutoRelay(t)
	h.client.quicHoldOff = time.Millisecond
	h.connect(t)
	time.Sleep(5 * time.Millisecond)
	h.connect(t)

	if got := h.quicDials.Load(); got != 2 {
		t.Fatalf("QUIC dials after the hold-off passed = %d, want 2", got)
	}
}

// A refusal is the relay's answer. Sending the same registration over TLS/TCP
// would only be refused again, and UDP plainly reaches the relay.
func TestRelayAutoDoesNotFallBackWhenRelayRefusesOverQUIC(t *testing.T) {
	h := newAutoRelay(t)
	h.useQUICRelay(t, func(_ *quic.Conn, stream io.ReadWriter, _ *relayHello) {
		_ = writeRelayFrame(stream, relayFrame{Type: relayFrameError, Error: "unauthorized"})
	})

	_, err := h.client.Connect(context.Background())
	if !errors.Is(err, errRelayRejected) || !contains(err.Error(), "unauthorized") {
		t.Fatalf("Connect err = %v, want the relay's refusal", err)
	}
	if got := len(h.tokens()); got != 0 {
		t.Fatalf("TLS/TCP carriers after a refusal = %d, want 0", got)
	}
	if h.client.quicHeldOff() {
		t.Fatal("a relay refusal must not hold QUIC off")
	}
	if state, _ := h.client.Status(); state != RelayError {
		t.Fatalf("state = %q, want error", state)
	}
}

// The relay closes the connection right after its refusal and the close can
// overtake the frame, and an admission refusal closes with no frame at all.
// Both are the relay's answer.
func TestRelayAutoDoesNotFallBackWhenRelayClosesTheQUICConnection(t *testing.T) {
	for name, answer := range map[string]func(conn *quic.Conn, stream io.ReadWriter){
		"refusal frame then close": func(conn *quic.Conn, stream io.ReadWriter) {
			_ = writeRelayFrame(stream, relayFrame{Type: relayFrameError, Error: "unauthorized"})
			_ = conn.CloseWithError(1, "carrier refused")
		},
		"close without a frame": func(conn *quic.Conn, _ io.ReadWriter) {
			_ = conn.CloseWithError(1, "carrier refused")
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newAutoRelay(t)
			h.useQUICRelay(t, func(conn *quic.Conn, stream io.ReadWriter, _ *relayHello) { answer(conn, stream) })

			if _, err := h.client.Connect(context.Background()); err == nil {
				t.Fatal("Connect succeeded, want the relay's refusal")
			}
			if got := len(h.tokens()); got != 0 {
				t.Fatalf("TLS/TCP carriers after the relay closed QUIC = %d, want 0", got)
			}
			if h.client.quicHeldOff() {
				t.Fatal("a connection the relay closed must not hold QUIC off")
			}
		})
	}
}

// The hold-off records that TLS/TCP works where QUIC does not. When neither
// connects, the next Connect probes QUIC again.
func TestRelayAutoDoesNotHoldQUICOffWhenTCPAlsoFails(t *testing.T) {
	h := newAutoRelay(t)
	h.client.dial = func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("relay is down")
	}

	if _, err := h.client.Connect(context.Background()); err == nil {
		t.Fatal("Connect succeeded with the relay down")
	}
	if h.client.quicHeldOff() {
		t.Fatal("QUIC is held off although TLS/TCP did not connect either")
	}
}

// A caller's deadline shorter than the probe budget still leaves TLS/TCP half
// of it.
func TestRelayAutoProbeLeavesTimeForTCPUnderAShortDeadline(t *testing.T) {
	h := newAutoRelay(t)
	h.client.dialQUIC = func(ctx context.Context, _ string) (*quic.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := h.client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = session.Close()
	if got := h.client.Transport(); got != config.RelayTransportTCP {
		t.Fatalf("Transport() = %q, want %q", got, config.RelayTransportTCP)
	}
}

func TestRelayAutoUsesQUICWhenItAnswers(t *testing.T) {
	h := newAutoRelay(t)
	serverConn := make(chan *quic.Conn, 1)
	h.useQUICRelay(t, func(conn *quic.Conn, stream io.ReadWriter, hello *relayHello) {
		if writeRelayFrame(stream, relayFrame{Type: relayFrameAck, Ack: &relayAck{
			Version: relayProtoVersion, CarrierID: "carrier-quic", Hostnames: hello.Hostnames,
		}}) == nil {
			serverConn <- conn
		}
	})
	h.connect(t)

	if got := h.client.Transport(); got != config.RelayTransportQUIC {
		t.Fatalf("Transport() = %q, want %q", got, config.RelayTransportQUIC)
	}
	if got := len(h.tokens()); got != 0 {
		t.Fatalf("TLS/TCP carriers = %d, want 0", got)
	}
	// The probe's context ended when Connect returned; the carrier must not
	// have ended with it.
	select {
	case conn := <-serverConn:
		select {
		case <-conn.Context().Done():
			t.Fatal("the QUIC carrier closed when the probe budget's context ended")
		case <-time.After(100 * time.Millisecond):
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the QUIC relay never completed the handshake")
	}
}

func TestRelayExplicitQUICDoesNotFallBack(t *testing.T) {
	h := newAutoRelay(t)
	h.client.cfg.Transport = config.RelayTransportQUIC

	if _, err := h.client.Connect(context.Background()); err == nil {
		t.Fatal("Connect succeeded, want the QUIC dial error")
	}
	if got := len(h.tokens()); got != 0 {
		t.Fatalf("TLS/TCP carriers with transport quic = %d, want 0", got)
	}
}

func TestRelayExplicitTCPDoesNotTryQUIC(t *testing.T) {
	h := newAutoRelay(t)
	h.client.cfg.Transport = config.RelayTransportTCP
	h.connect(t)

	if got := h.quicDials.Load(); got != 0 {
		t.Fatalf("QUIC dials with transport tcp = %d, want 0", got)
	}
}
