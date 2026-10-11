package remoteaccess

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRelayFrameReadBoundaries(t *testing.T) {
	framed := func(payload string) []byte {
		n := len(payload)
		if n < 0 || n > relayMaxFrameSize {
			t.Fatalf("fixture payload exceeds frame limit: %d", n)
			return nil
		}
		wire := make([]byte, 4+n)
		binary.BigEndian.PutUint32(wire[:4], uint32(n))
		copy(wire[4:], payload)
		return wire
	}
	oversized := make([]byte, 4)
	binary.BigEndian.PutUint32(oversized, relayMaxFrameSize+1)
	tests := []struct {
		name    string
		wire    []byte
		want    error
		message string
	}{
		{name: "empty header", want: io.EOF},
		{name: "truncated header", wire: []byte{0, 0}, want: io.ErrUnexpectedEOF},
		{name: "truncated payload", wire: framed(`{"type":"hello"}`)[:6], want: io.ErrUnexpectedEOF},
		{name: "oversized", wire: oversized, want: errRelayFrameTooLarge},
		{name: "invalid JSON", wire: framed(`{`), message: "decode relay frame"},
		{name: "wrong cache type", wire: framed(`{"type":"cache_push","cache_push":{"bytes":"ten"}}`), message: "decode relay frame"},
		{name: "invalid expiry", wire: framed(`{"type":"cache_push","cache_push":{"expires_at":"tomorrow"}}`), message: "decode relay frame"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readRelayFrame(bytes.NewReader(tt.wire))
			if err == nil {
				t.Fatal("malformed frame accepted")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("error %v, want %v", err, tt.want)
			}
			if tt.message != "" && !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("error %v lacks %q", err, tt.message)
			}
			if got.Type != "" || got.CachePush != nil {
				t.Fatalf("partial frame escaped: %+v", got)
			}
		})
	}
}

func TestRelayCacheEncodingDetails(t *testing.T) {
	const large int64 = 9007199254740993
	cases := []struct{ name, payload string }{
		{"push optional content type", `{"type":"cache_push","cache_push":{"segment_set_id":"set","segment":"0001.m4s","expires_at":"2026-07-07T12:00:00Z","bytes":9007199254740993}}`},
		{"ack optional error", `{"type":"cache_ack","cache_ack":{"status":"accepted","quota":{"used_bytes":9007199254740993,"cap_bytes":9223372036854775807}}}`},
		{"empty inventory", `{"type":"cache_inventory","cache_inventory":{"entries":[],"quota":{"used_bytes":0,"cap_bytes":0}}}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var frame relayFrame
			if err := json.Unmarshal([]byte(tt.payload), &frame); err != nil {
				t.Fatal(err)
			}
			if frame.CachePush != nil && frame.CachePush.Bytes != large {
				t.Fatal("byte precision lost")
			}
			var buf bytes.Buffer
			if err := writeRelayFrame(&buf, frame); err != nil {
				t.Fatal(err)
			}
			if string(buf.Bytes()[4:]) != tt.payload {
				t.Fatalf("payload %s, want %s", buf.Bytes()[4:], tt.payload)
			}
			decoded, err := readRelayFrame(&buf)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.payload {
				t.Fatalf("round-trip %s, want %s", got, tt.payload)
			}
		})
	}
}
