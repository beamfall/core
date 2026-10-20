package integrationhost

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHostNegotiationCompatibilityMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		raw          []byte
		wantLegacy   byte
		wantFrame    string
		wantConsumed bool
		capabilities []string
	}{
		{
			name:       "old worker omitted range",
			raw:        []byte(`{"token":"token"}`),
			wantLegacy: legacyACK,
		},
		{
			name:      "explicit v1",
			raw:       []byte(`{"token":"token","minVersion":1,"maxVersion":1}`),
			wantFrame: `{"selectedVersion":1,"capabilities":[]}`,
		},
		{
			name:      "v1 through v2 chooses highest",
			raw:       []byte(`{"token":"token","minVersion":1,"maxVersion":2}`),
			wantFrame: `{"selectedVersion":2,"capabilities":[]}`,
		},
		{
			name:      "v2 only",
			raw:       []byte(`{"token":"token","minVersion":2,"maxVersion":2}`),
			wantFrame: `{"selectedVersion":2,"capabilities":[]}`,
		},
		{
			name:      "overlap above host chooses v2",
			raw:       []byte(`{"token":"token","minVersion":2,"maxVersion":3}`),
			wantFrame: `{"selectedVersion":2,"capabilities":[]}`,
		},
		{
			name:         "capability intersection sorted and unknown ignored",
			raw:          []byte(`{"token":"token","minVersion":1,"maxVersion":2,"capabilities":["unknown","beta","alpha","beta"],"future":true}`),
			wantFrame:    `{"selectedVersion":2,"capabilities":["alpha","beta"]}`,
			capabilities: []string{"beta", "alpha"},
		},
		{
			name:         "wrong legacy token",
			raw:          []byte(`{"token":"wrong"}`),
			wantLegacy:   legacyNACK,
			wantConsumed: false,
		},
		{
			name:         "minimum greater than maximum",
			raw:          []byte(`{"token":"token","minVersion":2,"maxVersion":1}`),
			wantFrame:    `{"error":"invalid version range"}`,
			wantConsumed: true,
		},
		{
			name:         "no overlap",
			raw:          []byte(`{"token":"token","minVersion":3,"maxVersion":4}`),
			wantFrame:    `{"error":"no compatible version"}`,
			wantConsumed: true,
		},
		{
			name:         "partial range",
			raw:          []byte(`{"token":"token","minVersion":1}`),
			wantFrame:    `{"error":"invalid version range"}`,
			wantConsumed: true,
		},
		{
			name:         "capabilities without range",
			raw:          []byte(`{"token":"token","capabilities":["alpha"]}`),
			wantFrame:    `{"error":"invalid version range"}`,
			wantConsumed: true,
		},
		{
			name:         "duplicate key",
			raw:          []byte(`{"token":"token","minVersion":1,"minVersion":2,"maxVersion":2}`),
			wantFrame:    `{"error":"invalid handshake"}`,
			wantConsumed: true,
		},
		{
			name:         "duplicate token valid then wrong",
			raw:          []byte(`{"token":"token","token":"wrong"}`),
			wantLegacy:   legacyNACK,
			wantConsumed: true,
		},
		{
			name:         "duplicate token wrong then valid",
			raw:          []byte(`{"token":"wrong","token":"token"}`),
			wantLegacy:   legacyNACK,
			wantConsumed: true,
		},
		{
			name:         "duplicate unknown legacy member",
			raw:          []byte(`{"token":"token","future":1,"future":2}`),
			wantLegacy:   legacyNACK,
			wantConsumed: true,
		},
		{
			name:         "malformed after token",
			raw:          []byte(`{"token":"token","minVersion":1,`),
			wantFrame:    `{"error":"invalid handshake"}`,
			wantConsumed: true,
		},
		{
			name:         "too many capabilities",
			raw:          handshakeJSON(1, 2, repeatedCapabilities(65, "a")),
			wantFrame:    `{"error":"invalid capabilities"}`,
			wantConsumed: true,
		},
		{
			name:         "capability name too long",
			raw:          handshakeJSON(1, 2, []string{strings.Repeat("a", 65)}),
			wantFrame:    `{"error":"invalid capabilities"}`,
			wantConsumed: true,
		},
		{
			name:      "maximum capability aggregate",
			raw:       handshakeJSON(1, 2, repeatedCapabilities(64, strings.Repeat("a", 64))),
			wantFrame: `{"selectedVersion":2,"capabilities":[]}`,
		},
		{
			name:         "capabilities null",
			raw:          []byte(`{"token":"token","minVersion":1,"maxVersion":2,"capabilities":null}`),
			wantFrame:    `{"error":"invalid handshake"}`,
			wantConsumed: true,
		},
		{
			name:         "capabilities scalar",
			raw:          []byte(`{"token":"token","minVersion":1,"maxVersion":2,"capabilities":"alpha"}`),
			wantFrame:    `{"error":"invalid handshake"}`,
			wantConsumed: true,
		},
		{
			name:         "capabilities non-string member",
			raw:          []byte(`{"token":"token","minVersion":1,"maxVersion":2,"capabilities":["alpha",5]}`),
			wantFrame:    `{"error":"invalid handshake"}`,
			wantConsumed: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sup := &Supervisor{token: "token", capabilities: tc.capabilities}
			client, server := net.Pipe()
			done := make(chan bool, 1)
			go func() {
				done <- sup.negotiateHost(server)
				_ = server.Close()
			}()
			if err := writeFrame(client, tc.raw); err != nil {
				t.Fatalf("write handshake: %v", err)
			}
			if tc.wantFrame != "" {
				var header [4]byte
				if _, err := io.ReadFull(client, header[:]); err != nil {
					t.Fatalf("read frame header: %v", err)
				}
				if got := binary.BigEndian.Uint32(header[:]); int64(got) != int64(len(tc.wantFrame)) {
					t.Fatalf("frame length = %d, want %d; raw=%x", got, len(tc.wantFrame), header)
				}
				raw := make([]byte, len(tc.wantFrame))
				if _, err := io.ReadFull(client, raw); err != nil {
					t.Fatalf("read frame payload: %v", err)
				}
				if got := string(raw); got != tc.wantFrame {
					t.Fatalf("reply = %s, want %s", got, tc.wantFrame)
				}
			} else {
				var reply [1]byte
				if _, err := io.ReadFull(client, reply[:]); err != nil {
					t.Fatalf("read legacy reply: %v", err)
				}
				if reply[0] != tc.wantLegacy {
					t.Fatalf("reply = 0x%02x, want 0x%02x", reply[0], tc.wantLegacy)
				}
			}
			_ = client.Close()
			<-done
			consumed := !sup.consumeToken("token")
			if tc.wantFrame == "" && tc.wantLegacy != legacyACK && consumed != tc.wantConsumed {
				t.Fatalf("token consumed = %v, want %v", consumed, tc.wantConsumed)
			}
			if tc.wantFrame != "" && strings.Contains(tc.wantFrame, `"error"`) && !consumed {
				t.Fatal("valid token replayed after rejected handshake")
			}
		})
	}
}

func TestLegacyHandshakeAllowsImmediateRequest(t *testing.T) {
	t.Parallel()
	sup := &Supervisor{token: "token"}
	client, server := net.Pipe()
	go sup.handleConn(t.Context(), server)
	defer func() { _ = client.Close() }()

	if err := writeFrame(client, []byte(`{"token":"token"}`)); err != nil {
		t.Fatal(err)
	}
	var ack [1]byte
	if _, err := io.ReadFull(client, ack[:]); err != nil || ack[0] != legacyACK {
		t.Fatalf("legacy ACK = %x, %v", ack, err)
	}
	if err := writeFrame(client, []byte(`{bad`)); err != nil {
		t.Fatal(err)
	}
	raw, err := readFrame(client)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{"err":"malformed request"}`; got != want {
		t.Fatalf("immediate response = %s, want %s", got, want)
	}
}

func TestWorkerNegotiationCompatibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		reply   byte
		close   bool
		offer   VersionOffer
		want    NegotiatedVersion
		wantErr string
	}{
		{
			name:  "new worker downgrades on old host ACK",
			reply: legacyACK,
			offer: VersionOffer{MinVersion: 1, MaxVersion: 2},
			want:  NegotiatedVersion{SelectedVersion: 1, Capabilities: []string{}},
		},
		{
			name:    "new worker rejects old host when v1 not offered",
			reply:   legacyACK,
			offer:   VersionOffer{MinVersion: 2, MaxVersion: 2},
			wantErr: "legacy host selected unsupported version",
		},
		{
			name:    "old host NACK",
			reply:   legacyNACK,
			close:   true,
			offer:   VersionOffer{MinVersion: 1, MaxVersion: 2},
			wantErr: "legacy handshake rejected",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client, server := net.Pipe()
			go func() {
				_, _ = readFrame(server)
				_, _ = server.Write([]byte{tc.reply})
				if tc.close {
					_ = server.Close()
				}
			}()
			got, err := NegotiateWorker(client, "token", tc.offer, 100*time.Millisecond)
			_ = client.Close()
			_ = server.Close()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("result = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestWorkerNegotiatesHighestOverlapWithNewHost(t *testing.T) {
	t.Parallel()
	for _, offer := range []VersionOffer{
		{MinVersion: 1, MaxVersion: 1},
		{MinVersion: 1, MaxVersion: 2},
		{MinVersion: 2, MaxVersion: 2},
		{MinVersion: 2, MaxVersion: 3},
	} {
		client, server := net.Pipe()
		sup := &Supervisor{token: "token"}
		go func() {
			_ = sup.negotiateHost(server)
			_ = server.Close()
		}()
		got, err := NegotiateWorker(client, "token", offer, time.Second)
		_ = client.Close()
		if err != nil {
			t.Fatalf("offer %+v: %v", offer, err)
		}
		want := min(offer.MaxVersion, hostMaxVersion)
		if got.SelectedVersion != want {
			t.Fatalf("offer %+v selected %d, want %d", offer, got.SelectedVersion, want)
		}
	}
}

func TestWorkerRejectsMalformedNegotiationResponses(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		response string
		offer    VersionOffer
	}{
		"malformed": {
			response: `{"selectedVersion":2,`,
			offer:    VersionOffer{MinVersion: 1, MaxVersion: 2},
		},
		"duplicate selected version": {
			response: `{"selectedVersion":1,"selectedVersion":2,"capabilities":[]}`,
			offer:    VersionOffer{MinVersion: 1, MaxVersion: 2},
		},
		"missing capabilities": {
			response: `{"selectedVersion":2}`,
			offer:    VersionOffer{MinVersion: 1, MaxVersion: 2},
		},
		"null capabilities": {
			response: `{"selectedVersion":2,"capabilities":null}`,
			offer:    VersionOffer{MinVersion: 1, MaxVersion: 2},
		},
		"duplicate capability": {
			response: `{"selectedVersion":2,"capabilities":["alpha","alpha"]}`,
			offer:    VersionOffer{MinVersion: 1, MaxVersion: 2, Capabilities: []string{"alpha"}},
		},
		"unoffered capability": {
			response: `{"selectedVersion":2,"capabilities":["beta"]}`,
			offer:    VersionOffer{MinVersion: 1, MaxVersion: 2, Capabilities: []string{"alpha"}},
		},
		"ambiguous error and ack": {
			response: `{"error":"no compatible version","selectedVersion":2,"capabilities":[]}`,
			offer:    VersionOffer{MinVersion: 1, MaxVersion: 2},
		},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, server := net.Pipe()
			go func() {
				_, _ = readFrame(server)
				_ = writeFrame(server, []byte(tc.response))
				_ = server.Close()
			}()
			if _, err := NegotiateWorker(client, "token", tc.offer, time.Second); err == nil {
				t.Fatalf("response %s was accepted", tc.response)
			}
			_ = client.Close()
		})
	}
}

func TestWorkerAcceptsCapabilityNamesWithSelectedPrefix(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	go func() {
		_, _ = readFrame(server)
		_ = writeFrame(server, []byte(`{"selectedVersion":2,"capabilities":["a","selected:a"],"future":true}`))
		_ = server.Close()
	}()
	got, err := NegotiateWorker(client, "token", VersionOffer{
		MinVersion:   1,
		MaxVersion:   2,
		Capabilities: []string{"a", "selected:a"},
	}, time.Second)
	_ = client.Close()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "selected:a"}; !reflect.DeepEqual(got.Capabilities, want) {
		t.Fatalf("capabilities = %v, want %v", got.Capabilities, want)
	}
}

func TestHandshakeTimeoutAndClose(t *testing.T) {
	t.Parallel()
	t.Run("host timeout", func(t *testing.T) {
		sup := &Supervisor{token: "token", handshakeTimeout: 20 * time.Millisecond}
		client, server := net.Pipe()
		defer func() { _ = client.Close() }()
		if sup.negotiateHost(server) {
			t.Fatal("silent worker negotiated")
		}
	})
	t.Run("host close", func(t *testing.T) {
		sup := &Supervisor{token: "token"}
		client, server := net.Pipe()
		_ = client.Close()
		if sup.negotiateHost(server) {
			t.Fatal("closed worker negotiated")
		}
	})
	t.Run("worker timeout", func(t *testing.T) {
		client, server := net.Pipe()
		go func() {
			_, _ = readFrame(server)
			time.Sleep(50 * time.Millisecond)
			_ = server.Close()
		}()
		_, err := NegotiateWorker(client, "token", VersionOffer{MinVersion: 1, MaxVersion: 2}, 20*time.Millisecond)
		_ = client.Close()
		if err == nil {
			t.Fatal("silent host negotiated")
		}
	})
	t.Run("worker close", func(t *testing.T) {
		client, server := net.Pipe()
		go func() {
			_, _ = readFrame(server)
			_ = server.Close()
		}()
		_, err := NegotiateWorker(client, "token", VersionOffer{MinVersion: 1, MaxVersion: 2}, time.Second)
		_ = client.Close()
		if err == nil {
			t.Fatal("closed host negotiated")
		}
	})
}

func handshakeJSON(minVersion, maxVersion int, capabilities []string) []byte {
	raw, _ := json.Marshal(handshakeFrame{
		Token:        "token",
		MinVersion:   minVersion,
		MaxVersion:   maxVersion,
		Capabilities: capabilities,
	})
	return raw
}

func repeatedCapabilities(count int, capability string) []string {
	values := make([]string, count)
	for i := range values {
		values[i] = capability
	}
	return values
}
