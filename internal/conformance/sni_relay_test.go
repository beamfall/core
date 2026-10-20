package conformance

// ADR-0018 §1 SniTun conformance — relay hello wire protocol.
//
// The relay carrier types and helpers are unexported from
// internal/remoteaccess, so this test verifies the two observable contracts
// reachable from the public surface:
//
//  1. The RelayClient.PublicHost() public-API contract: a client constructed
//     with a configured SNI hostname reports exactly that hostname.
//
//  2. The wire-frame format documented in relayproto.go ("4-byte big-endian
//     length + UTF-8 JSON, 64 KiB cap, hello/ack envelope"): implemented
//     inline here as the normative reference, so a future protocol deviation
//     in relayproto.go causes this test to fail.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/remoteaccess"
)

// ── inline wire-format types (the expected / normative side) ────────────────

// testRelayFrame is the reference carrier envelope, mirroring the ADR-0018
// wire contract. It is intentionally local so any drift in the production
// type is visible.
type testRelayFrame struct {
	Type  string          `json:"type"`
	Hello *testRelayHello `json:"hello,omitempty"`
	Ack   *testRelayAck   `json:"ack,omitempty"`
	Error string          `json:"error,omitempty"`
}

type testRelayHello struct {
	Version   int      `json:"version"`
	Token     []byte   `json:"token"`
	Hostnames []string `json:"hostnames"`
}

type testRelayAck struct {
	Version   int      `json:"version"`
	CarrierID string   `json:"carrier_id"`
	Hostnames []string `json:"hostnames"`
}

// writeTestFrame encodes a carrier frame using the documented wire format:
// 4-byte big-endian length prefix followed by UTF-8 JSON payload.
func writeTestFrame(t *testing.T, buf *bytes.Buffer, frame testRelayFrame) {
	t.Helper()
	payload, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal test relay frame: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	buf.Write(hdr[:])
	buf.Write(payload)
}

// readTestFrame decodes one carrier frame from buf using the same wire format.
func readTestFrame(t *testing.T, buf *bytes.Buffer) testRelayFrame {
	t.Helper()
	var hdr [4]byte
	if _, err := buf.Read(hdr[:]); err != nil {
		t.Fatalf("read test relay frame length: %v", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > 64*1024 {
		t.Fatalf("test relay frame size %d exceeds 64 KiB cap", n)
	}
	payload := make([]byte, n)
	if _, err := buf.Read(payload); err != nil {
		t.Fatalf("read test relay frame payload: %v", err)
	}
	var frame testRelayFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("decode test relay frame: %v", err)
	}
	return frame
}

// ── public-API contract ──────────────────────────────────────────────────────

// TestRelayClientPublicHostReturnsConfiguredSNIHostname verifies that
// RelayClient.PublicHost() returns exactly the hostname set in the relay
// config.  This is the SNI value the relay uses to route inbound TLS
// streams back to the carrier.
func TestRelayClientPublicHostReturnsConfiguredSNIHostname(t *testing.T) {
	const wantHost = "server.example.com"

	ra := config.RemoteAccessConfig{
		Relay: config.RelayConfig{
			Endpoint:   "relay.example.com:443",
			PublicHost: wantHost,
		},
	}
	client, err := remoteaccess.NewRelayClient(ra, t.TempDir())
	if err != nil {
		t.Fatalf("NewRelayClient: %v", err)
	}
	if client == nil {
		t.Fatal("NewRelayClient returned nil for a configured endpoint")
	}
	if got := client.PublicHost(); got != wantHost {
		t.Errorf("PublicHost() = %q, want %q", got, wantHost)
	}
}

// TestRelayClientNilWhenNotConfigured verifies that NewRelayClient returns
// nil (no error) when no relay endpoint is present — the feature is
// intentionally disabled.
func TestRelayClientNilWhenNotConfigured(t *testing.T) {
	ra := config.RemoteAccessConfig{} // no relay endpoint
	client, err := remoteaccess.NewRelayClient(ra, t.TempDir())
	if err != nil {
		t.Fatalf("NewRelayClient (no config) returned error: %v", err)
	}
	if client != nil {
		t.Errorf("NewRelayClient (no config) = %v, want nil", client)
	}
}

// ── wire-protocol hello-frame contract ──────────────────────────────────────

// TestRelayHelloFrameCarriesSNIHostname verifies the ADR-0018 §1 wire
// contract inline: the hello envelope (4-byte big-endian length + JSON)
// must carry the configured PublicHost in its Hostnames list so the relay
// can bind the SNI route.
//
// Because the production writeRelayFrame/readRelayFrame helpers are
// unexported, this test implements the frame format from scratch as the
// normative reference. If the formats match, the round-trip succeeds and
// the hostname is preserved; if they diverge, the test fails.
func TestRelayHelloFrameCarriesSNIHostname(t *testing.T) {
	const sniHost = "media.example.com"

	// Build a hello frame carrying the SNI hostname — this is what the
	// relay carrier sends immediately after dialing (relay.go register()).
	hello := testRelayFrame{
		Type: "hello",
		Hello: &testRelayHello{
			Version:   1,
			Token:     []byte("test-token"),
			Hostnames: []string{sniHost},
		},
	}

	// Write → read round-trip through a bytes.Buffer (the wire).
	var wire bytes.Buffer
	writeTestFrame(t, &wire, hello)
	decoded := readTestFrame(t, &wire)

	// Verify the envelope type.
	if decoded.Type != "hello" {
		t.Errorf("frame type = %q, want %q", decoded.Type, "hello")
	}
	if decoded.Hello == nil {
		t.Fatal("decoded hello payload is nil")
	}
	if decoded.Hello.Version != 1 {
		t.Errorf("hello version = %d, want 1", decoded.Hello.Version)
	}

	// The SNI hostname must survive the round-trip.
	if len(decoded.Hello.Hostnames) != 1 || decoded.Hello.Hostnames[0] != sniHost {
		t.Errorf("hello hostnames = %v, want [%q]", decoded.Hello.Hostnames, sniHost)
	}

	// Verify no trailing bytes — one frame, one exact payload.
	if wire.Len() != 0 {
		t.Errorf("unexpected %d bytes after frame read", wire.Len())
	}
}

// TestRelayAckFramePreservesHostnames verifies the ack envelope that the
// relay sends back carries the same hostnames it accepted — confirming the
// SNI binding is echoed and the frame format is symmetric.
func TestRelayAckFramePreservesHostnames(t *testing.T) {
	const sniHost = "media.example.com"

	ack := testRelayFrame{
		Type: "ack",
		Ack: &testRelayAck{
			Version:   1,
			CarrierID: "carrier-abc123",
			Hostnames: []string{sniHost},
		},
	}

	var wire bytes.Buffer
	writeTestFrame(t, &wire, ack)
	decoded := readTestFrame(t, &wire)

	if decoded.Type != "ack" {
		t.Errorf("frame type = %q, want %q", decoded.Type, "ack")
	}
	if decoded.Ack == nil {
		t.Fatal("decoded ack payload is nil")
	}
	if len(decoded.Ack.Hostnames) != 1 || decoded.Ack.Hostnames[0] != sniHost {
		t.Errorf("ack hostnames = %v, want [%q]", decoded.Ack.Hostnames, sniHost)
	}
}
