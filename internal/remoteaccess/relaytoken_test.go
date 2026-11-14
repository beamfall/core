package remoteaccess

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// relayHMACTokenV2Vectors mirrors the relay's published golden vector file,
// vendored byte-identical from beamfall-relay
// contracts/protocol/v1/hmac-token-v2-vectors.json.
type relayHMACTokenV2Vectors struct {
	SchemaVersion int    `json:"schema_version"`
	Spec          string `json:"spec"`
	SharedKeyUTF8 string `json:"shared_key_utf8"`
	MaxTTLSeconds int64  `json:"max_ttl_seconds"`
	Vectors       []struct {
		Name          string   `json:"name"`
		MintHostnames []string `json:"mint_hostnames"`
		IssuedAtUnix  int64    `json:"issued_at_unix"`
		ExpiresAtUnix int64    `json:"expires_at_unix"`
		NonceHex      string   `json:"nonce_hex"`
		MACInputHex   string   `json:"mac_input_hex"`
		MACHex        string   `json:"mac_hex"`
		TokenText     string   `json:"token_text"`
		Reason        string   `json:"reason"`
	} `json:"vectors"`
}

func loadRelayHMACTokenV2Vectors(t *testing.T) relayHMACTokenV2Vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/relay_hmac_token_v2_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v relayHMACTokenV2Vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.SchemaVersion != 1 || len(v.Vectors) == 0 {
		t.Fatalf("unexpected vector file: schema %d, %d vectors", v.SchemaVersion, len(v.Vectors))
	}
	return v
}

// Conformance: every vendored vector's MAC input, MAC and token text reproduce
// from its mint inputs. reject_bad_mac carries a deliberately altered MAC in its
// token text, so for it the minted token must equal the unaltered form instead.
func TestRelayHMACTokenV2ConformanceVectors(t *testing.T) {
	v := loadRelayHMACTokenV2Vectors(t)
	if v.MaxTTLSeconds != int64(relayTokenV2Lifetime/time.Second) {
		t.Fatalf("vector max_ttl_seconds = %d, minter lifetime = %v", v.MaxTTLSeconds, relayTokenV2Lifetime)
	}
	key := []byte(v.SharedKeyUTF8)
	for _, vec := range v.Vectors {
		t.Run(vec.Name, func(t *testing.T) {
			nonce, err := hex.DecodeString(vec.NonceHex)
			if err != nil || len(nonce) != relayTokenV2NonceBytes {
				t.Fatalf("vector nonce %q: %v", vec.NonceHex, err)
			}
			macInput := relayTokenV2MACInput(vec.MintHostnames, vec.IssuedAtUnix, vec.ExpiresAtUnix, hex.EncodeToString(nonce))
			if got := hex.EncodeToString(macInput); got != vec.MACInputHex {
				t.Fatalf("mac input = %s, want %s", got, vec.MACInputHex)
			}
			token := string(relayTokenV2(key, vec.MintHostnames, vec.IssuedAtUnix, vec.ExpiresAtUnix, nonce))
			want := "v2." + strconv.FormatInt(vec.IssuedAtUnix, 10) + "." + strconv.FormatInt(vec.ExpiresAtUnix, 10) + "." + vec.NonceHex + "." + vec.MACHex
			if token != want {
				t.Fatalf("token = %s, want %s", token, want)
			}
			if vec.Reason == "bad_mac" {
				if token == vec.TokenText {
					t.Fatal("bad_mac vector token text unexpectedly equals the correct token")
				}
				return
			}
			if token != vec.TokenText {
				t.Fatalf("token = %s, want golden %s", token, vec.TokenText)
			}
		})
	}
}

func TestRelayHMACTokenV2MintUsesClockAndNonce(t *testing.T) {
	secret := []byte("relay-hmac-test-secret-0123456789abcdef")
	now := time.Unix(1790000000, 999_000_000)
	nonce := bytes.Repeat([]byte{0xab}, relayTokenV2NonceBytes)
	token, err := mintRelayTokenV2(secret, []string{"Remote.Example.com."}, now, bytes.NewReader(nonce))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(string(token), ".")
	if len(fields) != 5 || fields[0] != "v2" {
		t.Fatalf("token shape = %d fields", len(fields))
	}
	if fields[1] != "1789999970" {
		t.Fatalf("issued_at = %s, want whole-second now minus 30s", fields[1])
	}
	if fields[2] != "1790000270" {
		t.Fatalf("expires_at = %s, want issued_at + 300s", fields[2])
	}
	if fields[3] != hex.EncodeToString(nonce) {
		t.Fatalf("nonce field = %s, want injected nonce", fields[3])
	}
	want := relayTokenV2(secret, []string{"remote.example.com"}, 1789999970, 1790000270, nonce)
	if !hmac.Equal(token, want) {
		t.Fatal("minted token does not match the normalized-hostname token")
	}

	if _, err := mintRelayTokenV2(secret, []string{"remote.example.com"}, now, bytes.NewReader(nonce[:4])); err == nil {
		t.Fatal("mint accepted a short nonce read")
	}
	if _, err := mintRelayTokenV2(nil, []string{"remote.example.com"}, now, bytes.NewReader(nonce)); err == nil {
		t.Fatal("mint accepted an empty secret")
	}
	if _, err := mintRelayTokenV2(secret, nil, now, bytes.NewReader(nonce)); err == nil {
		t.Fatal("mint accepted no hostnames")
	}
}

// assertMintedRelayToken checks that token is a valid v2 token minted from
// secret over hostnames, with no trace of the secret, and returns its nonce.
func assertMintedRelayToken(t *testing.T, secret string, hostnames []string, token []byte) string {
	t.Helper()
	if bytes.Contains(token, []byte(secret)) || bytes.Contains(token, []byte(base64.StdEncoding.EncodeToString([]byte(secret)))) {
		t.Fatal("hello token carries the shared secret")
	}
	fields := strings.Split(string(token), ".")
	if len(fields) != 5 || fields[0] != "v2" {
		t.Fatalf("hello token is not a v2 token (%d fields)", len(fields))
	}
	iat, err1 := strconv.ParseInt(fields[1], 10, 64)
	exp, err2 := strconv.ParseInt(fields[2], 10, 64)
	nonce, err3 := hex.DecodeString(fields[3])
	if err1 != nil || err2 != nil || err3 != nil || len(nonce) != relayTokenV2NonceBytes {
		t.Fatal("hello token fields do not parse")
	}
	if exp-iat != int64(relayTokenV2Lifetime/time.Second) {
		t.Fatalf("token lifetime = %ds", exp-iat)
	}
	if !hmac.Equal(token, relayTokenV2([]byte(secret), hostnames, iat, exp, nonce)) {
		t.Fatal("hello token MAC does not verify against the shared secret")
	}
	return fields[3]
}

// startHelloRecordingRelay runs a stub relay that records each hello and then
// either acks (holding the yamux session) or answers with errMsg built from the
// received token.
func startHelloRecordingRelay(t *testing.T, hellos chan<- *relayHello, reject func(token []byte) string) stubRelay {
	t.Helper()
	return startStubRelay(t, func(conn net.Conn) (*yamux.Session, bool) {
		frame, err := readRelayFrame(conn)
		if err != nil || frame.Type != relayFrameHello || frame.Hello == nil {
			return nil, false
		}
		hellos <- frame.Hello
		if reject != nil {
			_ = writeRelayFrame(conn, relayFrame{Type: relayFrameError, Error: reject(frame.Hello.Token)})
			return nil, false
		}
		_ = writeRelayFrame(conn, relayFrame{Type: relayFrameAck, Ack: &relayAck{
			Version: relayProtoVersion, CarrierID: frame.Hello.Hostnames[0], Hostnames: frame.Hello.Hostnames,
		}})
		s, serr := yamux.Server(conn, relayYamuxConfig(0))
		if serr != nil {
			return nil, false
		}
		return s, true
	})
}

func recvHello(t *testing.T, hellos <-chan *relayHello) *relayHello {
	t.Helper()
	select {
	case h := <-hellos:
		return h
	case <-time.After(2 * time.Second):
		t.Fatal("relay never received the hello frame")
		return nil
	}
}

func TestRelayConnectMintsFreshTokenPerDial(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")
	hellos := make(chan *relayHello, 2)
	relay := startHelloRecordingRelay(t, hellos, nil)
	c := newDialableRelay(t, relay.addr, dir)

	s1, err := c.Connect(context.Background())
	if err != nil {
		t.Fatalf("first Connect: %v", err)
	}
	defer s1.Close()
	h1 := recvHello(t, hellos)
	s2, err := c.Connect(context.Background())
	if err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	defer s2.Close()
	h2 := recvHello(t, hellos)

	if bytes.Equal(h1.Token, h2.Token) {
		t.Fatal("two Connect calls carried the same token")
	}
	n1 := assertMintedRelayToken(t, "carrier-secret", h1.Hostnames, h1.Token)
	n2 := assertMintedRelayToken(t, "carrier-secret", h2.Hostnames, h2.Token)
	if n1 == n2 {
		t.Fatal("two Connect calls reused a nonce")
	}
}

// After an "unauthorized" rejection the next attempt mints a new token with a
// new nonce, and the rejection reason never echoes the secret or the token.
func TestRelayUnauthorizedRetryMintsNewNonceAndRedacts(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")
	secretB64 := base64.StdEncoding.EncodeToString([]byte("carrier-secret"))
	hellos := make(chan *relayHello, 2)
	relay := startHelloRecordingRelay(t, hellos, func(token []byte) string {
		return "unauthorized " + string(token) + " " + base64.StdEncoding.EncodeToString(token) + " carrier-secret " + secretB64
	})
	c := newDialableRelay(t, relay.addr, dir)

	var tokens [][]byte
	for i := 0; i < 2; i++ {
		_, err := c.Connect(context.Background())
		if err == nil {
			t.Fatal("Connect accepted an unauthorized carrier")
		}
		h := recvHello(t, hellos)
		tokens = append(tokens, h.Token)
		state, reason := c.Status()
		if state != RelayError {
			t.Fatalf("state = %q, want error", state)
		}
		if !contains(err.Error(), "unauthorized") || !contains(err.Error(), "[redacted]") {
			t.Fatalf("error = %q, want unauthorized with redaction", err)
		}
		for _, leak := range []string{"carrier-secret", secretB64, string(h.Token), base64.StdEncoding.EncodeToString(h.Token)} {
			if contains(err.Error(), leak) || contains(reason, leak) {
				t.Fatal("error or relay state leaked the secret or minted token")
			}
		}
	}
	n1 := assertMintedRelayToken(t, "carrier-secret", []string{"remote.example.com"}, tokens[0])
	n2 := assertMintedRelayToken(t, "carrier-secret", []string{"remote.example.com"}, tokens[1])
	if n1 == n2 {
		t.Fatal("retry after unauthorized reused the nonce")
	}
}

// A rotated-out generation ending does not overwrite the state its replacement
// owns; the newest generation's end still reports honestly.
func TestRelaySupersededCarryKeepsState(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	hellos := make(chan *relayHello, 2)
	relay := startHelloRecordingRelay(t, hellos, nil)
	c := newDialableRelay(t, relay.addr, dir)
	c.SetBackendAddr(backend.Addr().String())

	s1, err := c.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan struct{})
	go func() { _ = c.Carry(ctx, s1); close(firstDone) }()

	s2, err := c.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondDone := make(chan struct{})
	go func() { _ = c.Carry(ctx, s2); close(secondDone) }()

	// The first (superseded) session ends unexpectedly: no state change.
	_ = s1.Close()
	<-firstDone
	if state, reason := c.Status(); state != RelayConnected || reason != "" {
		t.Fatalf("status after superseded end = (%q, %q), want connected", state, reason)
	}

	// The current session ending is a real loss.
	_ = s2.Close()
	<-secondDone
	if state, _ := c.Status(); state != RelayError {
		t.Fatalf("status after current end = %q, want error", state)
	}
}

// A failed rotation dial leaves the live session's connected state intact.
func TestRelayFailedRotationKeepsConnectedState(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")
	hellos := make(chan *relayHello, 1)
	relay := startHelloRecordingRelay(t, hellos, nil)
	c := newDialableRelay(t, relay.addr, dir)
	s1, err := c.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	c.dial = func(context.Context, string) (net.Conn, error) { return nil, errors.New("relay unreachable") }
	if _, err := c.Connect(context.Background()); err == nil {
		t.Fatal("rotation Connect succeeded against an unreachable relay")
	}
	if state, reason := c.Status(); state != RelayConnected || reason != "" {
		t.Fatalf("status after failed rotation = (%q, %q), want connected", state, reason)
	}
}
