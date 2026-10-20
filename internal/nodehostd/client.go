package nodehostd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/beamfall/core/internal/nodehost"
)

// DialConfig is the node-side dial material for the session leg.
type DialConfig struct {
	// Addr is the main's node-listener address the node dials.
	Addr string
	// ServerName is the TLS SNI to send; may be empty (the served main identity
	// is self-signed and selected for non-FQDN SNI).
	ServerName string
	// NodeCert is the node's enrolled client cert + key, presented so the main
	// can pin it (main→node direction of the mutual pin).
	NodeCert tls.Certificate
	// MainSPKIPin is the main SPKI the node captured at enrollment; the served
	// main leaf must match it or the handshake fails closed.
	MainSPKIPin string
}

// OpenSession dials the main over mTLS and runs one ADR-0129 §2 start-of-session
// exchange. It enforces the node→main direction of the mutual pin: the served
// main leaf's SPKI must equal the enrollment-time MainSPKIPin,
// checked in VerifyPeerCertificate so a mismatched main fails the handshake
// before any session data. The node's own client cert is presented so the main
// can pin the node in turn. It returns the main's Admission, or an error on a
// pin failure or a refused session.
func OpenSession(ctx context.Context, cfg DialConfig, nodeID string, revision int) (nodehost.Admission, error) {
	if len(cfg.NodeCert.Certificate) == 0 {
		return nodehost.AdmitRefuse, errors.New("nodehostd: node client certificate required")
	}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cfg.NodeCert},
		ServerName:   cfg.ServerName,
		MinVersion:   tls.VersionTLS12,
		// The served main identity is self-signed and pinned by SPKI, so default
		// CA/hostname verification is disabled ONLY to substitute the stronger
		// SPKI pin below — never to skip verification. VerifyPeerCertificate fails
		// the handshake closed on any SPKI mismatch or empty pin.
		InsecureSkipVerify: true, //nolint:gosec // G402: SPKI pinning replaces CA/hostname trust (ADR-0046 §2.1)
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return nodehost.VerifyMainLeafSPKI(rawCerts, cfg.MainSPKIPin)
		},
	}
	dialer := &tls.Dialer{Config: tlsConf}
	raw, err := dialer.DialContext(ctx, "tcp", cfg.Addr)
	if err != nil {
		// Includes the pinned-main handshake failure (VerifyPeerCertificate).
		return nodehost.AdmitRefuse, err
	}
	conn, ok := raw.(*tls.Conn)
	if !ok {
		_ = raw.Close()
		return nodehost.AdmitRefuse, errors.New("nodehostd: dial did not yield a TLS connection")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	req := openSessionRequest{Type: "open-session", Version: protoVersion, NodeID: nodeID, Revision: revision}
	if err := writeFrame(conn, req); err != nil {
		return nodehost.AdmitRefuse, err
	}
	var resp openSessionResponse
	if err := readFrame(conn, &resp); err != nil {
		return nodehost.AdmitRefuse, err
	}
	if resp.Type != "open-session-result" {
		return nodehost.AdmitRefuse, fmt.Errorf("nodehostd: invalid session response type %q", resp.Type)
	}
	if resp.Error != "" {
		return nodehost.AdmitRefuse, fmt.Errorf("nodehostd: session refused: %s", resp.Error)
	}
	admission := nodehost.Admission(resp.Admission)
	switch admission {
	case nodehost.AdmitServing, nodehost.AdmitDegraded, nodehost.AdmitRefuse:
		return admission, nil
	default:
		return nodehost.AdmitRefuse, fmt.Errorf("nodehostd: invalid session admission %q", resp.Admission)
	}
}
