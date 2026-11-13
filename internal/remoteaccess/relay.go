package remoteaccess

import (
	"context"
	"crypto/rand"
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
// with a freshly minted carrier token, registers this server's public host, then
// carries inbound relayed connections byte-blind: the relay opens one yamux
// stream per inbound public client and the client splices each stream to the
// local server. The shared HMAC secret is resolved from a 0600 file at dial time
// and only a single-use v2 token derived from it goes on the wire; neither is
// logged nor placed in config (invariant 5).
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
	// now and nonce feed the per-dial token minter; tests inject them.
	now   func() time.Time
	nonce io.Reader

	mu        sync.Mutex
	state     RelayState
	lastError string
	lastDial  time.Time
	// generation counts successful registrations. Only the newest
	// generation's Carry exit may change the relay state; a superseded
	// (rotated-out) session draining away is not a carrier loss.
	generation uint64
}

// RelaySession is a live carrier session returned by Connect and consumed by
// Carry.
type RelaySession = relaySession

// relayGenerationSession tags a session with the registration generation that
// produced it.
type relayGenerationSession struct {
	relaySession
	generation uint64
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
		now:       time.Now,
		nonce:     rand.Reader,
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

// setGenerationState changes the state only when generation is still the
// newest registration (0 marks an untracked session and always applies).
func (c *RelayClient) setGenerationState(generation uint64, s RelayState, errMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != 0 && generation != c.generation {
		return
	}
	c.state = s
	c.lastError = errMsg
}

// Connect performs one dial + register + multiplexer-start attempt. It resolves
// the shared secret, mints a fresh single-use v2 carrier token, dials the relay
// over TLS 1.3, writes the hello frame, reads the relay's ack, and starts the
// yamux client over the same carrier. On success the state is RelayConnected
// and the live session is returned for the caller's Carry loop; on any failure
// the state is RelayError with a secret- and token-free reason.
//
// A Connect made while a session is already connected is a rotation: it
// registers a same-identity replacement and leaves the state untouched on
// failure, since the current session is still carrying.
func (c *RelayClient) Connect(ctx context.Context) (relaySession, error) {
	c.mu.Lock()
	rotating := c.state == RelayConnected
	c.mu.Unlock()
	report := func(s RelayState, msg string) {
		if !rotating {
			c.setState(s, msg)
		}
	}
	if c.cfg.PublicHost == "" {
		err := errors.New("relay endpoint set but no public host to register (set --relay-public-host or --public-domain)")
		report(RelayError, err.Error())
		return nil, err
	}
	secret, err := c.resolveToken()
	if err != nil {
		// resolveToken never includes the secret in its error (it reports path
		// + permission problems only), so this is safe to surface.
		report(RelayError, err.Error())
		return nil, err
	}
	hostnames := []string{c.cfg.PublicHost}
	token, err := mintRelayTokenV2(secret, hostnames, c.now(), c.nonce)
	if err != nil {
		// The minter's errors are fixed strings with no secret or token.
		report(RelayError, err.Error())
		return nil, err
	}
	report(RelayConnecting, "")
	c.mu.Lock()
	c.lastDial = time.Now().UTC()
	c.mu.Unlock()

	var session relaySession
	if c.cfg.Transport == config.RelayTransportQUIC {
		var conn *quic.Conn
		conn, err = c.dialQUIC(ctx, c.cfg.Endpoint)
		if err != nil {
			report(RelayError, sanitizeDialError(err, secret, token))
			return nil, fmt.Errorf("dialing relay: %w", err)
		}
		session, err = c.registerQUIC(ctx, conn, hostnames, token, secret)
		if err != nil {
			_ = conn.CloseWithError(0, "")
		}
	} else {
		var conn net.Conn
		conn, err = c.dial(ctx, c.cfg.Endpoint)
		if err != nil {
			report(RelayError, sanitizeDialError(err, secret, token))
			return nil, fmt.Errorf("dialing relay: %w", err)
		}
		session, err = c.registerTCP(conn, hostnames, token, secret)
		if err != nil {
			_ = conn.Close()
		}
	}
	if err != nil {
		report(RelayError, err.Error())
		return nil, fmt.Errorf("relay registration: %w", err)
	}
	c.mu.Lock()
	c.generation++
	generation := c.generation
	c.state = RelayConnected
	c.lastError = ""
	c.mu.Unlock()
	return &relayGenerationSession{relaySession: session, generation: generation}, nil
}

// register authenticates the carrier, registers the public host, and starts the
// yamux multiplexer. It writes the length-prefixed JSON hello
// frame (minted carrier token + the public host as the routed hostname), reads
// the relay's ack, and on acceptance upgrades the same TLS carrier to a yamux
// client session. A non-ack frame (e.g. error, or a bad version the relay
// refused) is a rejected carrier. The token rides the encrypted carrier and is
// never logged here or elsewhere; secret is used only for redaction.
func (c *RelayClient) registerTCP(conn net.Conn, hostnames []string, token, secret []byte) (*yamux.Session, error) {
	// Bound the handshake so a silent or slow relay cannot pin the dial
	// goroutine forever; cleared before yamux (which carries no deadline).
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := relayHandshake(conn, hostnames, token, secret); err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	session, err := yamux.Client(conn, relayYamuxConfig(c.cfg.YamuxStreamWindowBytes))
	if err != nil {
		return nil, fmt.Errorf("start yamux client: %w", err)
	}
	return session, nil
}

func (c *RelayClient) registerQUIC(ctx context.Context, conn *quic.Conn, hostnames []string, token, secret []byte) (relaySession, error) {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("open quic carrier handshake stream: %w", err)
	}
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	if err := relayHandshake(stream, hostnames, token, secret); err != nil {
		return nil, err
	}
	_ = stream.SetDeadline(time.Time{})
	return &relayQUICSession{conn: conn}, nil
}

// relayHandshake sends the hello for hostnames (the same list the token was minted
// over) and reads the relay's answer, redacting secret and token from any
// relay-supplied reason.
func relayHandshake(rw io.ReadWriter, hostnames []string, token, secret []byte) error {
	hello := relayFrame{Type: relayFrameHello, Hello: &relayHello{
		Version: relayProtoVersion, Token: token, Hostnames: hostnames,
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
			return fmt.Errorf("relay rejected carrier: %s", redactToken(frame.Error, secret, token))
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
// A session superseded by a newer registration (rotation) leaves the state to
// its replacement when it ends.
//
// Carry takes ownership of the session and closes it on return.
func (c *RelayClient) Carry(ctx context.Context, session relaySession) error {
	var generation uint64
	if gs, ok := session.(*relayGenerationSession); ok {
		generation = gs.generation
	}
	c.mu.Lock()
	backend := c.backendAddr
	c.mu.Unlock()
	if backend == "" {
		_ = session.Close()
		err := errors.New("relay carry has no local backend address")
		c.setGenerationState(generation, RelayError, err.Error())
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
		c.setGenerationState(generation, RelayDisconnected, "")
	} else {
		msg := "relay carrier session ended"
		if carryErr != nil {
			msg = "relay carrier session ended: " + carryErr.Error()
		}
		c.setGenerationState(generation, RelayError, msg)
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

// resolveToken reads the shared HMAC secret from <secretDir>/relay-token (the
// same value as relayd --token-file). It refuses a group/other-readable file
// (invariant 5) and an empty file. The returned error never contains the
// secret's contents or path. Leading and trailing white space is trimmed, as
// relayd does; the bytes are used raw, never base64-decoded, and never sent.
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
// occurrence of the secret or token redacted. Dial errors carry network detail,
// not secrets, but this is belt-and-suspenders for the byte-blindness invariant.
func sanitizeDialError(err error, secrets ...[]byte) string {
	return redactToken(err.Error(), secrets...)
}

// redactToken replaces every raw or base64 occurrence of each secret in msg.
func redactToken(msg string, secrets ...[]byte) string {
	for _, secret := range secrets {
		if len(secret) > 0 {
			msg = strings.ReplaceAll(msg, string(secret), "[redacted]")
			msg = strings.ReplaceAll(msg, base64.StdEncoding.EncodeToString(secret), "[redacted]")
		}
	}
	return msg
}

// defaultRelayYamuxStreamWindowBytes mirrors the relay's
// protocol.DefaultYamuxStreamWindowBytes (beamfall/relay
// internal/protocol/yamux.go); TestRelayYamuxConfigMatchesRelay pins both.
// yamux refills a stream's window only after half of it is drained, so one
// stream moves about window/2 per round trip: 4 MiB keeps a relayed upload above
// 40 MB/s at 50 ms, where 1 MiB held it near 10 MB/s (CORE-1386).
const defaultRelayYamuxStreamWindowBytes = 4 * 1024 * 1024

// relayYamuxConfig returns the carrier-session multiplexer config. It mirrors
// the relay node's protocol.YamuxConfig so window-size, close and keep-alive
// expectations agree end to end: a session-wide write timeout long enough to
// ride out a stalled player without tearing down the whole carrier, a stream
// window sized for the bandwidth-delay product of a high-bitrate remux, a 30s
// close timeout so a half-closed stream whose peer never answers is reset
// instead of holding its stream slot for yamux's default five minutes,
// keepalive on so a dead carrier is reaped, and yamux's own stderr logging
// suppressed. streamWindowBytes <= 0 uses the default.
func relayYamuxConfig(streamWindowBytes int) *yamux.Config {
	if streamWindowBytes <= 0 {
		streamWindowBytes = defaultRelayYamuxStreamWindowBytes
	}
	cfg := yamux.DefaultConfig()
	cfg.ConnectionWriteTimeout = 30 * time.Second
	cfg.MaxStreamWindowSize = uint32(streamWindowBytes) //nolint:gosec // G115: config validation bounds the window to [256 KiB, MaxInt].
	cfg.StreamCloseTimeout = 30 * time.Second
	cfg.EnableKeepAlive = true
	cfg.LogOutput = io.Discard
	return cfg
}
