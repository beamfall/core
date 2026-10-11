package remoteaccess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

// Beamfall Relay carrier wire protocol (ADR-0018 §1, the SniTun contract). This
// is a native AGPL reimplementation of the byte-level frame format the relay
// node speaks — length-prefixed JSON before yamux starts — so this open-source
// server can carry inbound connections without importing the proprietary relay
// module. The frame shape (4-byte big-endian length + UTF-8 JSON, 64 KiB cap,
// hello/ack envelope) is the authoritative contract; both ends must agree on it
// byte-for-byte.
const (
	// relayProtoVersion is the carrier handshake version this client speaks.
	relayProtoVersion = 1
	// relayMaxFrameSize bounds a single handshake frame (the relay refuses
	// larger). The carry never sends payload through frames — only the hello —
	// so this is generous for a token + a few hostnames.
	relayMaxFrameSize = 64 * 1024

	relayFrameHello = "hello"
	relayFrameAck   = "ack"
	relayFrameError = "error"

	relayQUICALPN = "beamfall-relay-carrier/1"
)

// relaySession is the transport-neutral carrier multiplexer consumed by
// RelayClient.Carry. Both *yamux.Session and relayQUICSession satisfy it.
type relaySession interface {
	Accept() (net.Conn, error)
	Close() error
}

// relayQUICConfig pins the consumer to the producer's NGAUDIT-X-QUIC flow
// control and keepalive contract.
func relayQUICConfig() *quic.Config {
	return &quic.Config{
		MaxStreamReceiveWindow:     8 * 1024 * 1024,
		MaxConnectionReceiveWindow: 64 * 1024 * 1024,
		MaxIncomingStreams:         1024,
		KeepAlivePeriod:            15 * time.Second,
	}
}

type relayQUICSession struct{ conn *quic.Conn }

func (s *relayQUICSession) Accept() (net.Conn, error) {
	stream, err := s.conn.AcceptStream(context.Background())
	if err != nil {
		return nil, err
	}
	return &relayQUICStream{Stream: stream, conn: s.conn}, nil
}

func (s *relayQUICSession) Close() error { return s.conn.CloseWithError(0, "") }

type relayQUICStream struct {
	*quic.Stream
	conn *quic.Conn
}

func (s *relayQUICStream) LocalAddr() net.Addr  { return s.conn.LocalAddr() }
func (s *relayQUICStream) RemoteAddr() net.Addr { return s.conn.RemoteAddr() }
func (s *relayQUICStream) CloseWrite() error    { return s.Close() }

// errRelayFrameTooLarge is returned when a frame exceeds relayMaxFrameSize on
// read or write — a malformed or hostile peer, never a normal handshake.
var errRelayFrameTooLarge = errors.New("relay protocol frame too large")

// relayFrame represents the carrier control envelope, keyed by Type. Cache
// payloads preserve the relay wire contract; they do not enable cache handling.
type relayFrame struct {
	Type  string      `json:"type"`
	Hello *relayHello `json:"hello,omitempty"`
	Ack   *relayAck   `json:"ack,omitempty"`
	Error string      `json:"error,omitempty"`

	CachePush             *relayCachePush             `json:"cache_push,omitempty"`
	CacheAck              *relayCacheAck              `json:"cache_ack,omitempty"`
	CacheInventoryRequest *relayCacheInventoryRequest `json:"cache_inventory_request,omitempty"`
	CacheInventory        *relayCacheInventory        `json:"cache_inventory,omitempty"`
}

// relayHello is the client's registration: the opaque carrier token (JSON
// marshals a []byte as a base64 string, matching the relay's `[]byte` field)
// and the hostnames it serves. The token is a secret — it rides the encrypted
// carrier and is never logged.
type relayHello struct {
	Version   int      `json:"version"`
	Token     []byte   `json:"token"`
	Hostnames []string `json:"hostnames"`
}

// relayAck is the relay's acceptance, returning the carrier id and the
// hostnames it registered the carrier for.
type relayAck struct {
	Version   int      `json:"version"`
	CarrierID string   `json:"carrier_id"`
	Hostnames []string `json:"hostnames"`
}

// Cache payload field order and omission rules match the authoritative v1
// relay contract, including signed 64-bit byte counts and RFC3339 timestamps.
type relayCachePush struct {
	SegmentSetID string    `json:"segment_set_id"`
	Segment      string    `json:"segment"`
	ContentType  string    `json:"content_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Bytes        int64     `json:"bytes"`
}

type relayCacheAck struct {
	Status string               `json:"status"`
	Error  string               `json:"error,omitempty"`
	Quota  relayCacheQuotaState `json:"quota"`
}

type relayCacheQuotaState struct {
	UsedBytes int64 `json:"used_bytes"`
	CapBytes  int64 `json:"cap_bytes"`
}

type relayCacheInventoryRequest struct {
	SegmentSetID string `json:"segment_set_id"`
}

type relayCacheInventory struct {
	Entries []relayCacheInventoryEntry `json:"entries"`
	Quota   relayCacheQuotaState       `json:"quota"`
}

type relayCacheInventoryEntry struct {
	Segment     string    `json:"segment"`
	Bytes       int64     `json:"bytes"`
	ContentType string    `json:"content_type"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// writeRelayFrame marshals and length-prefixes one handshake frame.
func writeRelayFrame(w io.Writer, frame relayFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal relay frame: %w", err)
	}
	if len(payload) > relayMaxFrameSize {
		return errRelayFrameTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("write relay frame length: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write relay frame payload: %w", err)
	}
	return nil
}

// readRelayFrame reads one length-prefixed handshake frame, refusing an
// over-large length before allocating.
func readRelayFrame(r io.Reader) (relayFrame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return relayFrame{}, fmt.Errorf("read relay frame length: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > relayMaxFrameSize {
		return relayFrame{}, errRelayFrameTooLarge
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return relayFrame{}, fmt.Errorf("read relay frame payload: %w", err)
	}
	var frame relayFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return relayFrame{}, fmt.Errorf("decode relay frame: %w", err)
	}
	return frame, nil
}
