package nodehostd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/beamfall/core/internal/nodehost"
)

// handshakeTimeout bounds the whole per-connection exchange (TLS handshake +
// one session frame) so a slow or hostile dialer cannot pin a goroutine open.
const handshakeTimeout = 10 * time.Second

// SessionAdmitter is the subset of *nodehost.Service the session leg needs. It
// keeps the transport testable and depends on nodehost, never the reverse.
type SessionAdmitter interface {
	OpenSession(req nodehost.SessionRequest, policy nodehost.CompatibilityPolicy) (nodehost.Admission, error)
}

// Server is the main-side, node-facing mTLS session listener (ADR-0129 §5: the
// node dials the main). It enforces the main→node direction of the mutual pin:
// a presented node leaf whose SPKI does not match the enrollment-time pin is
// refused by nodehost.OpenSession.
type Server struct {
	admitter SessionAdmitter
	policy   nodehost.CompatibilityPolicy
	tlsConf  *tls.Config
	logger   *slog.Logger

	// mu guards the live-connection set and the retired flag together with the
	// handler counter, so a handler is never registered after Shutdown began to
	// wait for the ones already registered.
	mu       sync.Mutex
	live     map[net.Conn]struct{}
	retired  bool
	handlers sync.WaitGroup
}

// NewServer builds the session server over the node-mTLS *tls.Config the caller
// prepared (a CLONE of the main's self-signed identity config with
// ClientAuth=RequireAnyClientCert — never the shared browser config).
func NewServer(admitter SessionAdmitter, policy nodehost.CompatibilityPolicy, mtlsConfig *tls.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{admitter: admitter, policy: policy, tlsConf: mtlsConfig, logger: logger}
}

// Serve accepts node connections until ln is closed, handling each in its own
// goroutine. It returns the Accept error (the closed-listener error on
// shutdown), which the caller treats as expected after context cancellation.
// Every accepted connection is owned by this server until Shutdown retires it.
func (s *Server) Serve(ln net.Listener) error {
	for {
		raw, err := ln.Accept()
		if err != nil {
			return err
		}
		if !s.admit(raw) {
			_ = raw.Close()
			continue
		}
		go func() {
			defer s.release(raw)
			s.handleConn(raw)
		}()
	}
}

// Shutdown retires the session leg: it refuses further connections, closes
// every live one, and returns once the in-flight handlers have. Closing the
// listener alone is not enough — a handler holds its connection for up to
// handshakeTimeout, so without this join a node connection outlives shutdown.
// It is idempotent and safe to call without a preceding Serve.
func (s *Server) Shutdown() {
	s.mu.Lock()
	s.retired = true
	for conn := range s.live {
		_ = conn.Close()
	}
	s.mu.Unlock()
	s.handlers.Wait()
}

// admit registers a connection and its handler under one lock, so Shutdown
// cannot begin waiting between the two and miss the handler it just admitted.
func (s *Server) admit(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retired {
		return false
	}
	if s.live == nil {
		s.live = map[net.Conn]struct{}{}
	}
	s.live[conn] = struct{}{}
	s.handlers.Add(1)
	return true
}

func (s *Server) release(conn net.Conn) {
	s.mu.Lock()
	delete(s.live, conn)
	s.mu.Unlock()
	s.handlers.Done()
}

// handleConn runs the TLS handshake, extracts the node's self-signed leaf at the
// pinned-TLS boundary, reads one open-session frame, and responds. All
// disruption (handshake failure, no leaf, bad frame) closes the connection.
func (s *Server) handleConn(raw net.Conn) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(handshakeTimeout))
	conn := tls.Server(raw, s.tlsConf)
	if err := conn.HandshakeContext(context.Background()); err != nil {
		s.logger.Debug("nodehostd handshake failed", "error", err)
		return
	}
	state := conn.ConnectionState()
	leaf, err := nodehost.PeerLeafFromPinnedTLS(&state)
	if err != nil {
		s.logger.Debug("nodehostd rejected: no peer leaf", "error", err)
		return
	}
	var req openSessionRequest
	if err := readFrame(conn, &req); err != nil {
		s.logger.Debug("nodehostd read open-session failed", "error", err)
		return
	}
	s.respondOpenSession(conn, req, leaf)
}

// respondOpenSession runs the ADR-0129 §2 start-of-session admission and writes
// the result. A pin-mismatched node leaf surfaces as an error response; a
// revision/compat refusal surfaces as admission "refuse-serving".
func (s *Server) respondOpenSession(conn *tls.Conn, req openSessionRequest, leaf *x509.Certificate) {
	if req.Type != "open-session" || req.Version != protoVersion {
		_ = writeFrame(conn, openSessionResponse{Type: "open-session-result", Error: "unsupported session transport version"})
		return
	}
	adm, err := s.admitter.OpenSession(nodehost.SessionRequest{
		NodeID:        req.NodeID,
		Revision:      nodehost.Revision(req.Revision),
		PresentedLeaf: leaf,
	}, s.policy)
	if err != nil {
		_ = writeFrame(conn, openSessionResponse{Type: "open-session-result", Error: err.Error()})
		return
	}
	_ = writeFrame(conn, openSessionResponse{Type: "open-session-result", Admission: string(adm)})
}
