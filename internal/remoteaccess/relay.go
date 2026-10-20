package remoteaccess

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/quic-go/quic-go"

	"github.com/beamfall/core/internal/config"
)

// RelayState is the honest relay-carrier connection state surfaced to the
// operator Console. It deliberately carries no bytes, no token, and no relayed
// connection detail (ADR-0018 byte-blindness).
type RelayState string

const (
	// RelayDisabled: no relay endpoint configured.
	RelayDisabled RelayState = "disabled"
	// RelayDisconnected: configured but not currently carrying (never dialed,
	// or the carrier dropped and is awaiting redial).
	RelayDisconnected RelayState = "disconnected"
	// RelayConnecting: an outbound dial / registration is in flight.
	RelayConnecting RelayState = "connecting"
	// RelayConnected: the outbound carrier is established, the host is
	// registered with the relay, and the yamux session is live and accepting
	// inbound relayed streams — i.e. the tunnel actually forwards traffic.
	RelayConnected RelayState = "connected"
	// RelayError: the last dial or registration attempt failed; LastError
	// carries the reason (never the token).
	RelayError RelayState = "error"
)

// RelayClient dials OUT to a self-hosted relay (ADR-0018/0025), authenticates
// with the carrier token, registers this server's public host, then carries
// inbound relayed connections byte-blind: the relay opens one yamux stream per
// inbound public client and the client splices each stream to the local server.
// The token is resolved from a 0600 secret at dial time and held only in memory
// for the carrier — never logged, never placed in config (invariant 5).
//
// Client TLS terminates on THIS server: the relay forwards raw
// bytes, so each yamux stream carries a pristine client ClientHello that the
// local backend (a loopback TLS listener fronting the app, wired by the caller)
// terminates against the server's own certificate. The relay holds no keys and
// sees no plaintext.
type RelayClient struct {
	cfg        config.RelayConfig
	secretDir  string
	dial       dialFunc
	dialQUIC   quicDialFunc
	maxStreams int
	// backendAddr is the loopback address inbound relayed streams are spliced
	// to (the server's own TLS-terminating listener). Empty means the carry
	// has no local target and Carry refuses to start — set by the caller once
	// the listener address is known.
	backendAddr string

	mu        sync.Mutex
	state     RelayState
	lastError string
	lastDial  time.Time
}

// dialFunc abstracts the outbound TLS dial so tests can substitute a local
// listener without a real relay. The default dials TLS 1.3 over TCP.
type dialFunc func(ctx context.Context, endpoint string) (net.Conn, error)
type quicDialFunc func(ctx context.Context, endpoint string) (*quic.Conn, error)

// NewRelayClient constructs the relay client from config. It returns
// (nil, nil) when no relay endpoint is configured. secretDir is the directory
// holding the relay-token secret file (typically <DataDir>/secrets).
func NewRelayClient(ra config.RemoteAccessConfig, secretDir string) (*RelayClient, error) {
	if !ra.RelayConfigured() {
		return nil, nil
	}
	dial := dialTLS13
	quicDial := dialQUIC
	if ra.Relay.CAFile != "" {
		dial = dialTLS13WithCA(ra.Relay.CAFile)
		quicDial = dialQUICWithCA(ra.Relay.CAFile)
	}
	return &RelayClient{
		cfg:       ra.Relay,
		secretDir: secretDir,
		dial:      dial,
		dialQUIC:  quicDial,
		state:     RelayDisconnected,
	}, nil
}

// relayTokenFile is the fixed secret name under secretDir (invariant 5).
const relayTokenFile = "relay-token"

const defaultMaxRelayStreams = 128

// ErrRelayInvalidHello marks a terminal carrier-protocol rejection. Retrying
// the same hello cannot recover until the server or relay configuration changes.
var ErrRelayInvalidHello = errors.New("relay rejected carrier: invalid hello")

// SetBackendAddr sets the loopback address inbound relayed streams are spliced
// to (the server's own TLS-terminating listener). The caller wires this once
// the local listener is bound, before starting the carry. It is not a secret.
func (c *RelayClient) SetBackendAddr(addr string) {
	c.mu.Lock()
	c.backendAddr = addr
	c.mu.Unlock()
}

// Endpoint returns the configured relay carrier address (no secret).
func (c *RelayClient) Endpoint() string { return c.cfg.Endpoint }

// PublicHost returns the SNI host the relay routes by (no secret).
func (c *RelayClient) PublicHost() string { return c.cfg.PublicHost }

// Status returns the current connection state and last error for the Console.
// The error string is sanitized at set time to never contain the token.
func (c *RelayClient) Status() (RelayState, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.lastError
}

func (c *RelayClient) setState(s RelayState, errMsg string) {
	c.mu.Lock()
	c.state = s
	c.lastError = errMsg
	c.mu.Unlock()
}

// Connect performs one dial + register + multiplexer-start attempt. It resolves
// the carrier token, dials the relay over TLS 1.3, writes the hello frame,
// reads the relay's ack, and starts the yamux client over the same carrier. On
// success the state is RelayConnected and the live session is returned for the
// caller's Carry loop; on any failure the state is RelayError with a token-free
// reason.
func (c *RelayClient) Connect(ctx context.Context) (relaySession, error) {
	if c.cfg.PublicHost == "" {
		err := errors.New("relay endpoint set but no public host to register (set --relay-public-host or --public-domain)")
		c.setState(RelayError, err.Error())
		return nil, err
	}
	token, err := c.resolveToken()
	if err != nil {
		// resolveToken never includes the token in its error (it reports path
		// + permission problems only), so this is safe to surface.
		c.setState(RelayError, err.Error())
		return nil, err
	}
	c.setState(RelayConnecting, "")
	c.mu.Lock()
	c.lastDial = time.Now().UTC()
	c.mu.Unlock()

	var session relaySession
	if c.cfg.Transport == config.RelayTransportQUIC {
		var conn *quic.Conn
		conn, err = c.dialQUIC(ctx, c.cfg.Endpoint)
		if err != nil {
			c.setState(RelayError, sanitizeDialError(err, token))
			return nil, fmt.Errorf("dialing relay: %w", err)
		}
		session, err = c.registerQUIC(ctx, conn, token)
		if err != nil {
			_ = conn.CloseWithError(0, "")
		}
	} else {
		var conn net.Conn
		conn, err = c.dial(ctx, c.cfg.Endpoint)
		if err != nil {
			c.setState(RelayError, sanitizeDialError(err, token))
			return nil, fmt.Errorf("dialing relay: %w", err)
		}
		session, err = c.registerTCP(conn, token)
		if err != nil {
			_ = conn.Close()
		}
	}
	if err != nil {
		c.setState(RelayError, err.Error())
		return nil, fmt.Errorf("relay registration: %w", err)
	}
	c.setState(RelayConnected, "")
	return session, nil
}

// register authenticates the carrier, registers the public host, and starts the
// yamux multiplexer. It writes the length-prefixed JSON hello
// frame (carrier token + the public host as the routed hostname), reads the
// relay's ack, and on acceptance upgrades the same TLS carrier to a yamux
// client session. A non-ack frame (e.g. error, or a bad version the relay
// refused) is a rejected carrier. The token rides the encrypted carrier and is
// never logged here or elsewhere.
func (c *RelayClient) registerTCP(conn net.Conn, token []byte) (*yamux.Session, error) {
	// Bound the handshake so a silent or slow relay cannot pin the dial
	// goroutine forever; cleared before yamux (which carries no deadline).
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := c.handshake(conn, token); err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	session, err := yamux.Client(conn, relayYamuxConfig())
	if err != nil {
		return nil, fmt.Errorf("start yamux client: %w", err)
	}
	return session, nil
}

func (c *RelayClient) registerQUIC(ctx context.Context, conn *quic.Conn, token []byte) (relaySession, error) {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("open quic carrier handshake stream: %w", err)
	}
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	if err := c.handshake(stream, token); err != nil {
		return nil, err
	}
	_ = stream.SetDeadline(time.Time{})
	return &relayQUICSession{conn: conn}, nil
}

func (c *RelayClient) handshake(rw io.ReadWriter, token []byte) error {
	hello := relayFrame{Type: relayFrameHello, Hello: &relayHello{
		Version: relayProtoVersion, Token: token, Hostnames: []string{c.cfg.PublicHost},
	}}
	if err := writeRelayFrame(rw, hello); err != nil {
		return err
	}
	frame, err := readRelayFrame(rw)
	if err != nil {
		return err
	}
	if frame.Type != relayFrameAck {
		if frame.Type == relayFrameError && frame.Error != "" {
			if strings.TrimSpace(frame.Error) == "invalid hello" {
				return ErrRelayInvalidHello
			}
			return fmt.Errorf("relay rejected carrier: %s", redactToken(frame.Error, token))
		}
		return errors.New("relay rejected carrier")
	}
	if frame.Ack == nil {
		return errors.New("relay rejected carrier: invalid ack")
	}
	return nil
}

// Carry serves inbound relayed streams for a live session: the relay opens one
// yamux stream per inbound public client connection, and Carry splices each to
// the local backend (the server's loopback TLS listener) bidirectionally. It
// blocks until the session ends (relay loss, carrier tear-down) or ctx is
// cancelled, then marks the carrier disconnected so the Console state is honest
// — RelayConnected only ever reflects a session that is actually forwarding.
//
// Carry takes ownership of the session and closes it on return.
func (c *RelayClient) Carry(ctx context.Context, session relaySession) error {
	c.mu.Lock()
	backend := c.backendAddr
	c.mu.Unlock()
	if backend == "" {
		_ = session.Close()
		err := errors.New("relay carry has no local backend address")
		c.setState(RelayError, err.Error())
		return err
	}

	// Tear the session down on ctx cancel so the blocking Accept unblocks.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-done:
		}
	}()

	maxStreams := c.maxStreams
	if maxStreams <= 0 {
		maxStreams = defaultMaxRelayStreams
	}
	streamSlots := make(chan struct{}, maxStreams)
	var wg sync.WaitGroup
	var carryErr error
	for {
		stream, err := session.Accept()
		if err != nil {
			if ctx.Err() != nil {
				carryErr = nil
			} else {
				carryErr = err
			}
			break
		}
		select {
		case streamSlots <- struct{}{}:
		default:
			_ = stream.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer func() {
				<-streamSlots
				wg.Done()
			}()
			c.handleStream(ctx, stream, backend)
		}()
	}
	wg.Wait()
	_ = session.Close()
	// The carrier is no longer forwarding. Reflect that honestly: a clean
	// ctx-driven exit is a disconnect; an unexpected session loss is an error.
	if ctx.Err() != nil {
		c.setState(RelayDisconnected, "")
	} else {
		msg := "relay carrier session ended"
		if carryErr != nil {
			msg = "relay carrier session ended: " + carryErr.Error()
		}
		c.setState(RelayError, msg)
	}
	return carryErr
}

// handleStream splices one inbound relayed stream to a fresh local backend
// connection. The stream carries the remote client's raw TLS bytes; the backend
// (the server's own TLS-terminating loopback listener) completes the handshake
// against the server's certificate, so the relay never sees plaintext.
//
// The splice mirrors the relay's own byte-blind splice: a clean EOF on one
// direction half-closes the destination's write side (so a one-sided close does
// not truncate the still-active direction — a client that half-closes mid-request
// still expects the in-flight download to finish), while an abrupt error on
// either direction force-closes BOTH conns so the surviving copy cannot park
// forever on a quiet-but-open peer (leaking the goroutine, fd, and stream slot).
// It deliberately carries no idle/inactivity deadline: a byte-blind timeout would
// also cut genuinely idle long-lived connections (websockets, SSE, long-poll);
// total exposure is bounded by maxStreams instead.
func (c *RelayClient) handleStream(ctx context.Context, stream net.Conn, backend string) {
	defer stream.Close()
	local, err := new(net.Dialer).DialContext(ctx, "tcp", backend)
	if err != nil {
		// No token, no payload, no relayed-connection detail in the state.
		c.setState(c.currentState(), "")
		return
	}
	defer local.Close()

	// Close both halves on ctx cancel (carrier teardown / shutdown) so a copy
	// blocked on a read unwinds promptly instead of parking until its peer closes.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
			_ = local.Close()
		case <-stop:
		}
	}()

	finished := make(chan error, 2)
	copyDir := func(dst, src net.Conn) {
		_, cerr := io.Copy(dst, src)
		if cerr == nil {
			// Clean EOF: propagate the half-close so the peer sees it, but leave
			// the live opposite direction running.
			halfCloseWrite(dst)
		}
		finished <- cerr
	}
	go copyDir(local, stream)
	go copyDir(stream, local)

	if cerr := <-finished; cerr != nil {
		// One side ended abruptly. Force-close both so the other copy's blocking
		// read unwinds instead of parking on a quiet-but-open peer.
		_ = stream.Close()
		_ = local.Close()
	}
	<-finished
}

// halfCloseWrite signals "no more writes" to dst while leaving its read side
// open, so one clean splice direction can finish without cutting the other. TCP
// exposes this as CloseWrite; a *yamux.Stream (what the relay hands us for each
// inbound client) has no CloseWrite, so its Close — a local write-side FIN — is
// the equivalent. Without the yamux fallback the backend->client EOF would never
// propagate as a FIN over the stream (mirrors relay protocol.HalfCloseWrite).
func halfCloseWrite(dst net.Conn) {
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	if ys, ok := dst.(*yamux.Stream); ok {
		_ = ys.Close()
	}
}

// currentState reads the state under the lock (used by handleStream to avoid
// clobbering RelayConnected on a transient backend dial miss).
func (c *RelayClient) currentState() RelayState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// resolveToken reads the carrier token from <secretDir>/relay-token. It refuses
// a group/other-readable file (invariant 5) and an empty file. The returned
// error never contains the token's contents or secret path.
// The token is opaque bytes (the relay passes them to its verifier seam); the
// trailing newline a 0600 secret file commonly carries is trimmed.
func (c *RelayClient) resolveToken() ([]byte, error) {
	path := filepath.Join(c.secretDir, relayTokenFile)
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("relay token unavailable")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("relay token file must be mode 0600 — refusing group/other-accessible secret")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("relay token file unreadable")
	}
	token := []byte(strings.TrimSpace(string(data)))
	if len(token) == 0 {
		return nil, errors.New("relay token file is empty")
	}
	return token, nil
}

// dialTLS13 dials the relay over TLS 1.3 (ADR-0018 §1: TLS 1.3 over TCP). The
// server name is the relay host (the part before the port), so the relay's own
// certificate is verified — distinct from the client TLS that terminates on
// THIS server against its own cert.
func dialTLS13(ctx context.Context, endpoint string) (net.Conn, error) {
	return dialTLS13WithConfig(ctx, endpoint, nil)
}

func dialTLS13WithCA(caPath string) dialFunc {
	return func(ctx context.Context, endpoint string) (net.Conn, error) {
		roots, err := loadRelayRoots(caPath)
		if err != nil {
			return nil, err
		}
		return dialTLS13WithConfig(ctx, endpoint, roots)
	}
}

func dialTLS13WithConfig(ctx context.Context, endpoint string, roots *x509.CertPool) (net.Conn, error) {
	tlsConfig, err := relayTLSConfig(endpoint, roots, false)
	if err != nil {
		return nil, err
	}
	d := &tls.Dialer{Config: tlsConfig}
	return d.DialContext(ctx, "tcp", endpoint)
}

func dialQUIC(ctx context.Context, endpoint string) (*quic.Conn, error) {
	return dialQUICWithConfig(ctx, endpoint, nil)
}

func dialQUICWithCA(caPath string) quicDialFunc {
	return func(ctx context.Context, endpoint string) (*quic.Conn, error) {
		roots, err := loadRelayRoots(caPath)
		if err != nil {
			return nil, err
		}
		return dialQUICWithConfig(ctx, endpoint, roots)
	}
}

func dialQUICWithConfig(ctx context.Context, endpoint string, roots *x509.CertPool) (*quic.Conn, error) {
	tlsConfig, err := relayTLSConfig(endpoint, roots, true)
	if err != nil {
		return nil, err
	}
	return quic.DialAddr(ctx, endpoint, tlsConfig, relayQUICConfig())
}

func relayTLSConfig(endpoint string, roots *x509.CertPool, quicCarrier bool) (*tls.Config, error) {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid relay endpoint %q: %w", endpoint, err)
	}
	cfg := &tls.Config{ServerName: host, RootCAs: roots, MinVersion: tls.VersionTLS13}
	if quicCarrier {
		cfg.NextProtos = []string{relayQUICALPN}
	}
	return cfg, nil
}

func loadRelayRoots(caPath string) (*x509.CertPool, error) {
	// #nosec G304 -- caPath is an explicit operator-configured trust-store path;
	// reading that exact file is the feature and its bytes are parsed only as PEM certificates.
	pemBytes, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read relay CA: %w", err)
	}
	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM(pemBytes); !ok {
		return nil, fmt.Errorf("relay CA file %q contained no certificates", caPath)
	}
	return roots, nil
}

// sanitizeDialError returns the dial error string with any accidental
// occurrence of the token redacted. Dial errors carry network detail, not
// secrets, but this is belt-and-suspenders for the byte-blindness invariant.
func sanitizeDialError(err error, token []byte) string {
	return redactToken(err.Error(), token)
}

func redactToken(msg string, token []byte) string {
	if len(token) > 0 {
		msg = strings.ReplaceAll(msg, string(token), "[redacted]")
		msg = strings.ReplaceAll(msg, base64.StdEncoding.EncodeToString(token), "[redacted]")
	}
	return msg
}

// relayYamuxConfig returns the carrier-session multiplexer config. It MUST
// match the relay node's config so window-size and keep-alive
// expectations agree end to end: a session-wide write timeout long enough to
// ride out a stalled player without tearing down the whole carrier, a stream
// window sized for the bandwidth-delay product of a high-bitrate remux, keepalive
// on so a dead carrier is reaped, and yamux's own stderr logging suppressed.
func relayYamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.ConnectionWriteTimeout = 30 * time.Second
	cfg.MaxStreamWindowSize = 1024 * 1024 // 1 MiB (BDP headroom for 40 Mbps / 50 ms RTT)
	cfg.EnableKeepAlive = true
	cfg.LogOutput = io.Discard
	return cfg
}
