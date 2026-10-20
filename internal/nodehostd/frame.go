// Package nodehostd is the node-facing mTLS transport for the ADR-0123 §5 /
// ADR-0129 Origin-Node contract: the seam that turns nodehost's stdlib-only pin
// logic into a real network path. Per ADR-0129 §5 the NODE dials the MAIN and
// runs no listener; this package provides both ends of the node↔main SESSION
// leg with SPKI pinning enforced in BOTH directions:
//
//   - Server (main side): a dedicated mTLS listener (tls.RequireAnyClientCert on
//     a clone of the main's self-signed identity — never the shared browser
//     config) that extracts the node's self-signed leaf via
//     nodehost.PeerLeafFromPinnedTLS and admits an open-session only when
//     nodehost.OpenSession → verifyPinnedLeaf accepts the presented leaf against
//     the enrollment-time node pin (main authenticates node).
//   - Dialer (node side): DialMain pins the served main leaf's SPKI to the
//     enrollment-time MainSPKIPin via nodehost.VerifyMainLeafSPKI (node
//     authenticates main, ADR-0046 §2.1).
//
// Scope (slice 1): the session leg only. The node-facing ENROLLMENT transport
// (with the operator mint/confirm ceremony) and the main→node multiplexed
// down-op leg are separate tickets; this package composes nodehost, not the
// reverse, so nodehost stays a leaf.
package nodehostd

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Frame wire contract (mirrors internal/remoteaccess/relayproto byte-for-byte in
// shape: 4-byte big-endian length + UTF-8 JSON, with a hard cap refused before
// allocation). Both ends must agree on it exactly.
const (
	// protoVersion is the pre-contract session-transport version this build
	// speaks. Fails closed on an unknown version, one layer before the ADR-0129
	// §2 contract-revision negotiation.
	protoVersion = 1
	// maxFrameSize bounds a single frame; a larger length is a malformed or
	// hostile peer and is refused before any allocation.
	maxFrameSize = 64 * 1024
)

// errFrameTooLarge is returned when a frame's declared length exceeds
// maxFrameSize on read or write — never a normal exchange.
var errFrameTooLarge = errors.New("nodehostd: frame too large")

// openSessionRequest is the node's start-of-session request. It deliberately
// carries NO operator-confirmation field: operator fingerprint confirmation is a
// main-side enrollment-ceremony action and can never be asserted
// by a node over the wire.
type openSessionRequest struct {
	Type     string `json:"type"` // must be "open-session"
	Version  int    `json:"version"`
	NodeID   string `json:"node_id"`
	Revision int    `json:"revision"`
}

// openSessionResponse is the main's admission decision for a session. Exactly
// one of Admission / Error is populated.
type openSessionResponse struct {
	Type      string `json:"type"` // "open-session-result"
	Admission string `json:"admission,omitempty"`
	Error     string `json:"error,omitempty"`
}

// writeFrame marshals v and writes it length-prefixed, refusing an over-large
// payload before writing.
func writeFrame(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("nodehostd: marshal frame: %w", err)
	}
	if len(payload) > maxFrameSize {
		return errFrameTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("nodehostd: write frame length: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("nodehostd: write frame payload: %w", err)
	}
	return nil
}

// readFrame reads one length-prefixed frame into v, refusing an over-large
// declared length before allocating the payload buffer.
func readFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return fmt.Errorf("nodehostd: read frame length: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrameSize {
		return errFrameTooLarge
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return fmt.Errorf("nodehostd: read frame payload: %w", err)
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("nodehostd: decode frame: %w", err)
	}
	return nil
}
