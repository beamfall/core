package remoteaccess

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/quic-go/quic-go"

	"github.com/beamfall/core/internal/config"
)

// feature:direct-https-acme — disabled config yields no DirectHTTPS path.
func TestDirectHTTPSDisabledReturnsNil(t *testing.T) {
	d, err := NewDirectHTTPS(config.RemoteAccessConfig{})
	if err != nil {
		t.Fatalf("NewDirectHTTPS error: %v", err)
	}
	if d != nil {
		t.Fatal("expected nil DirectHTTPS when not enabled")
	}
}

// feature:direct-https-acme — enabled config builds an autocert-backed TLS path
// pinned to the one domain, creates the cache dir 0700, and reports an honest
// pending cert state before any issuance.
func TestDirectHTTPSEnabledBuildsTLSAndPendingState(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "acme")
	ra := config.RemoteAccessConfig{
		DirectHTTPS: config.DirectHTTPSConfig{
			PublicDomain:      "media.example.com",
			ACMETermsAccepted: true,
			ACMECacheDir:      cacheDir,
		},
	}
	d, err := NewDirectHTTPS(ra)
	if err != nil {
		t.Fatalf("NewDirectHTTPS error: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil DirectHTTPS")
	}
	info, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("cache dir not created: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir mode = %v, want 0700", info.Mode().Perm())
	}
	if tc := d.TLSConfig(); tc == nil || tc.GetCertificate == nil {
		t.Fatal("TLSConfig missing GetCertificate")
	}
	if got := d.CertState(context.Background()); got != CertPending {
		t.Fatalf("CertState = %q, want %q before issuance", got, CertPending)
	}
	if d.Domain() != "media.example.com" {
		t.Fatalf("Domain = %q", d.Domain())
	}
}

// feature:client-cert-pinning — the pin ServerSPKIPin
// exposes is the SHA-256 of the served leaf's SubjectPublicKeyInfo in HPKP
// form, sourced from the live keypair in the autocert cache. Before any
// certificate is cached, ServerSPKIPin reports ErrNoServedCert, not a pin.
func TestServerSPKIPinMatchesServedLeaf(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "acme")
	const domain = "media.example.com"
	ra := config.RemoteAccessConfig{
		DirectHTTPS: config.DirectHTTPSConfig{
			PublicDomain:      domain,
			ACMETermsAccepted: true,
			ACMECacheDir:      cacheDir,
		},
	}
	d, err := NewDirectHTTPS(ra)
	if err != nil {
		t.Fatalf("NewDirectHTTPS: %v", err)
	}

	// No certificate cached yet: no pin, not an error to surface.
	if _, err := d.ServerSPKIPin(context.Background()); !errors.Is(err, ErrNoServedCert) {
		t.Fatalf("ServerSPKIPin before issuance err = %v, want ErrNoServedCert", err)
	}

	// Seed the autocert cache with a real keypair the way autocert stores it:
	// the private-key PEM block first, then the leaf certificate block.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{domain},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var bundle []byte
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	if err := d.Manager().Cache.Put(context.Background(), domain, bundle); err != nil {
		t.Fatalf("cache put: %v", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	want := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])

	got, err := d.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatalf("ServerSPKIPin: %v", err)
	}
	if got != want {
		t.Fatalf("ServerSPKIPin = %q, want %q (SHA-256 of served leaf SPKI)", got, want)
	}
}

// feature:client-cert-pinning — a routine ACME renewal that
// REUSES the keypair keeps the SPKI pin constant. We seed the cache with a
// keypair, capture the pin, then simulate a ~45-day renewal: a brand-new leaf
// (new serial, new validity) signed by and carrying the SAME public key, the
// way autocert reuses the cached private key to build the renewal CSR. The
// served leaf changes, but the SPKI — and thus the pinned hash — must not, or
// every renewal would brick pinned remote-only clients.
func TestSPKIStableAcrossKeyReusingRenewal(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "acme")
	const domain = "media.example.com"
	d := newEnabledDirectHTTPS(t, domain, cacheDir)

	key := genKey(t)
	seedCache(t, d, domain, key, big.NewInt(1), time.Now().Add(-time.Hour), time.Now().Add(45*24*time.Hour))
	before, err := d.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatalf("ServerSPKIPin before renewal: %v", err)
	}

	// Routine renewal: SAME key, fresh certificate (new serial + validity).
	seedCache(t, d, domain, key, big.NewInt(2), time.Now(), time.Now().Add(90*24*time.Hour))
	after, err := d.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatalf("ServerSPKIPin after renewal: %v", err)
	}

	if before != after {
		t.Fatalf("SPKI changed across a key-reusing renewal: before=%q after=%q", before, after)
	}
}

// feature:client-cert-pinning — a server that was offline
// through certificate expiry re-seeds autocert with the cached private key. The
// synthetic leaf is valid only long enough to trigger immediate ACME renewal;
// the renewed leaf therefore retains the expired leaf's SPKI.
func TestSPKIStableAcrossExpiredCacheReissuance(t *testing.T) {
	const domain = "media.example.com"
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	cacheDir := filepath.Join(t.TempDir(), "acme")
	d := newEnabledDirectHTTPS(t, domain, cacheDir)
	key := genKey(t)
	seedCache(t, d, domain, key, big.NewInt(1), now.Add(-90*24*time.Hour), now.Add(-time.Hour))

	cache, ok := d.Manager().Cache.(*keyReseedingCache)
	if !ok {
		t.Fatalf("cache type = %T, want *keyReseedingCache", d.Manager().Cache)
	}
	cache.now = func() time.Time { return now }
	reseeded, err := cache.Get(context.Background(), domain)
	if err != nil {
		t.Fatalf("get expired cache entry: %v", err)
	}
	reseededPair, err := tls.X509KeyPair(reseeded, reseeded)
	if err != nil {
		t.Fatalf("parse reseeded keypair: %v", err)
	}
	reseededLeaf, err := x509.ParseCertificate(reseededPair.Certificate[0])
	if err != nil {
		t.Fatalf("parse reseeded leaf: %v", err)
	}
	reseededPair.Leaf = reseededLeaf
	if !isKeyReseedingCertificate(&reseededPair) {
		t.Fatal("expired cache entry was not marked as a key-reseeding certificate")
	}
	if !reseededLeaf.NotAfter.After(now) {
		t.Fatal("key-reseeding certificate is not currently valid")
	}

	// autocert loads the reseeded signer into renewal state and uses it for the
	// ACME CSR. Model the CA response with a fresh leaf carrying that signer.
	reseededKey, ok := reseededPair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("reseeded private key type = %T, want *ecdsa.PrivateKey", reseededPair.PrivateKey)
	}
	seedCache(t, d, domain, reseededKey, big.NewInt(2), now, now.Add(90*24*time.Hour))
	renewed, err := d.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatalf("ServerSPKIPin after reissuance: %v", err)
	}
	want := publicKeySPKIPin(t, &key.PublicKey)
	if renewed != want {
		t.Fatalf("SPKI changed after expired-cache reissuance: got=%q want=%q", renewed, want)
	}
}

// feature:client-cert-pinning — a DELIBERATE operator rotation
// yields a NEW SPKI. RotateKey removes the cached keypair; the next issuance
// mints a fresh key, so the served leaf's SPKI — and the pin clients must match
// — changes by design, invalidating remote-only pins and forcing a LAN re-pair.
func TestRotateKeyChangesSPKI(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "acme")
	const domain = "media.example.com"
	d := newEnabledDirectHTTPS(t, domain, cacheDir)

	seedCache(t, d, domain, genKey(t), big.NewInt(1), time.Now().Add(-time.Hour), time.Now().Add(45*24*time.Hour))
	before, err := d.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatalf("ServerSPKIPin before rotation: %v", err)
	}

	// Operator-initiated rotation removes the cached keypair.
	if err := d.RotateKey(context.Background()); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	// After removal the pin is simply unavailable until re-issuance.
	if _, err := d.ServerSPKIPin(context.Background()); !errors.Is(err, ErrNoServedCert) {
		t.Fatalf("ServerSPKIPin after rotation err = %v, want ErrNoServedCert", err)
	}

	// Next issuance mints a FRESH key (simulated by seeding a new keypair).
	seedCache(t, d, domain, genKey(t), big.NewInt(1), time.Now(), time.Now().Add(90*24*time.Hour))
	after, err := d.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatalf("ServerSPKIPin after re-issuance: %v", err)
	}

	if before == after {
		t.Fatalf("SPKI unchanged after a deliberate rotation: %q", before)
	}
}

// feature:client-cert-pinning — rotating before any cert is
// cached is a harmless no-op (autocert's Cache.Delete returns nil for a missing
// key), so an operator can rotate without first triggering issuance.
func TestRotateKeyNoCachedCertIsNoop(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "acme")
	d := newEnabledDirectHTTPS(t, "media.example.com", cacheDir)
	if err := d.RotateKey(context.Background()); err != nil {
		t.Fatalf("RotateKey with no cached cert = %v, want nil", err)
	}
}

func newEnabledDirectHTTPS(t *testing.T, domain, cacheDir string) *DirectHTTPS {
	t.Helper()
	d, err := NewDirectHTTPS(config.RemoteAccessConfig{
		DirectHTTPS: config.DirectHTTPSConfig{
			PublicDomain:      domain,
			ACMETermsAccepted: true,
			ACMECacheDir:      cacheDir,
		},
	})
	if err != nil {
		t.Fatalf("NewDirectHTTPS: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil DirectHTTPS")
	}
	return d
}

func genKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func publicKeySPKIPin(t *testing.T, publicKey any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// seedCache writes a cert+key bundle into the autocert cache the way autocert
// stores it: the EC private-key PEM block first, then the leaf CERTIFICATE.
func seedCache(t *testing.T, d *DirectHTTPS, domain string, key *ecdsa.PrivateKey, serial *big.Int, notBefore, notAfter time.Time) {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		DNSNames:     []string{domain},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var bundle []byte
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	if err := d.Manager().Cache.Put(context.Background(), domain, bundle); err != nil {
		t.Fatalf("cache put: %v", err)
	}
}

// feature:self-hosted-relay — no endpoint yields no relay client.
func TestRelayDisabledReturnsNil(t *testing.T) {
	c, err := NewRelayClient(config.RemoteAccessConfig{}, t.TempDir())
	if err != nil {
		t.Fatalf("NewRelayClient error: %v", err)
	}
	if c != nil {
		t.Fatal("expected nil RelayClient when no endpoint")
	}
}

// feature:self-hosted-relay — a group/other-readable token file is refused at
// dial time and the state is error with a token-free reason (invariant 5).
func TestRelayRefusesInsecureTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, relayTokenFile)
	if err := os.WriteFile(tokenPath, []byte("carrier-secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newConfiguredRelay(t, dir)
	_, err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect accepted a 0644 token file")
	}
	state, reason := c.Status()
	if state != RelayError {
		t.Fatalf("state = %q, want error", state)
	}
	if reason == "" {
		t.Fatal("error state with empty reason")
	}
	if contains(reason, "carrier-secret") {
		t.Fatal("error reason leaked the token contents")
	}
	if contains(reason, tokenPath) {
		t.Fatal("error reason leaked the token path")
	}
}

func TestRelayRedactsTokenPathFromResolveErrors(t *testing.T) {
	dir := t.TempDir()
	c := newConfiguredRelay(t, dir)
	_, err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect accepted missing relay token")
	}
	tokenPath := filepath.Join(dir, relayTokenFile)
	if contains(err.Error(), tokenPath) {
		t.Fatal("connect error leaked the token path")
	}
	_, reason := c.Status()
	if contains(reason, tokenPath) {
		t.Fatal("error state leaked the token path")
	}

	if err := os.WriteFile(tokenPath, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = c.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect accepted empty relay token")
	}
	if contains(err.Error(), tokenPath) {
		t.Fatal("empty-token error leaked the token path")
	}
	_, reason = c.Status()
	if contains(reason, tokenPath) {
		t.Fatal("empty-token state leaked the token path")
	}
}

// feature:self-hosted-relay — the carrier handshake frame is the real
// PROTOCOL.md hello: a length-prefixed JSON {type:hello, hello:{version,token,
// hostnames}}, where the token rides as opaque bytes (JSON base64) and the
// hostnames carry the public host. WriteFrame/ReadFrame round-trip the exact
// wire the relay node reads, bounding an over-large frame.
func TestRelayFrameRoundTripAndBound(t *testing.T) {
	var buf bytes.Buffer
	want := relayFrame{Type: relayFrameHello, Hello: &relayHello{
		Version:   relayProtoVersion,
		Token:     []byte("carrier-secret"),
		Hostnames: []string{"remote.example.com"},
	}}
	if err := writeRelayFrame(&buf, want); err != nil {
		t.Fatalf("writeRelayFrame: %v", err)
	}
	// 4-byte big-endian length prefix, then UTF-8 JSON.
	if buf.Len() <= 4 {
		t.Fatalf("frame too short: %d bytes", buf.Len())
	}
	n := binary.BigEndian.Uint32(buf.Bytes()[:4])
	if int(n) != buf.Len()-4 {
		t.Fatalf("length prefix %d != payload %d", n, buf.Len()-4)
	}
	got, err := readRelayFrame(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("readRelayFrame: %v", err)
	}
	if got.Type != relayFrameHello || got.Hello == nil {
		t.Fatalf("decoded frame = %+v", got)
	}
	if string(got.Hello.Token) != "carrier-secret" || got.Hello.Version != relayProtoVersion {
		t.Fatalf("hello mismatch: %+v", got.Hello)
	}
	if len(got.Hello.Hostnames) != 1 || got.Hello.Hostnames[0] != "remote.example.com" {
		t.Fatalf("hostnames = %v", got.Hello.Hostnames)
	}

	// An over-large declared length is refused before allocation.
	var big [4]byte
	binary.BigEndian.PutUint32(big[:], relayMaxFrameSize+1)
	if _, err := readRelayFrame(bytes.NewReader(big[:])); !errors.Is(err, errRelayFrameTooLarge) {
		t.Fatalf("over-large frame err = %v, want errRelayFrameTooLarge", err)
	}
}

// feature:self-hosted-relay — with a 0600 token and a reachable TLS endpoint,
// Connect dials OUT over TLS 1.3, writes the real hello frame (carrying the
// public host + opaque token bytes), reads the relay's ack, and starts the
// yamux client, reaching the connected state. A stub relay performs the real
// server-side handshake.
func TestRelayConnectHandshakeAndAck(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")

	hello := make(chan *relayHello, 1)
	relay := startStubRelay(t, func(conn net.Conn) (*yamux.Session, bool) {
		frame, err := readRelayFrame(conn)
		if err != nil || frame.Type != relayFrameHello || frame.Hello == nil {
			return nil, false
		}
		hello <- frame.Hello
		_ = writeRelayFrame(conn, relayFrame{Type: relayFrameAck, Ack: &relayAck{
			Version: relayProtoVersion, CarrierID: "carrier-1", Hostnames: frame.Hello.Hostnames,
		}})
		s, _ := yamux.Server(conn, relayYamuxConfig())
		return s, true
	})

	c := newDialableRelay(t, relay.addr, dir)
	session, err := c.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	state, _ := c.Status()
	if state != RelayConnected {
		t.Fatalf("state = %q, want connected", state)
	}
	select {
	case h := <-hello:
		if string(h.Token) != "carrier-secret" {
			t.Fatalf("hello token = %q, want the carrier secret", h.Token)
		}
		if len(h.Hostnames) != 1 || h.Hostnames[0] != "remote.example.com" {
			t.Fatalf("hello hostnames = %v, want [remote.example.com]", h.Hostnames)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay never received the hello frame")
	}
}

// feature:self-hosted-relay — a relay that REJECTS the carrier (non-ack frame)
// fails Connect with a token-free error state, not a false connected.
func TestRelayConnectRejectedCarrier(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")

	relay := startStubRelay(t, func(conn net.Conn) (*yamux.Session, bool) {
		_, _ = readRelayFrame(conn)
		_ = writeRelayFrame(conn, relayFrame{Type: relayFrameError, Error: "unauthorized"})
		return nil, false
	})

	c := newDialableRelay(t, relay.addr, dir)
	if _, err := c.Connect(context.Background()); err == nil {
		t.Fatal("Connect accepted a rejected carrier")
	} else {
		if !contains(err.Error(), "unauthorized") {
			t.Fatalf("registration error = %q, want relay reason", err)
		}
		if errors.Is(err, ErrRelayInvalidHello) {
			t.Fatalf("unauthorized rejection classified as invalid hello: %v", err)
		}
	}
	state, reason := c.Status()
	if state != RelayError {
		t.Fatalf("state = %q, want error", state)
	}
	if contains(reason, "carrier-secret") {
		t.Fatal("error reason leaked the token")
	}
}

func TestRelayConnectClassifiesInvalidHello(t *testing.T) {
	dir := t.TempDir()
	// Classification must inspect the protocol reason before token redaction;
	// an opaque token can legally equal the relay's fixed reason string.
	writeSecret(t, dir, "invalid hello")

	relay := startStubRelay(t, func(conn net.Conn) (*yamux.Session, bool) {
		_, _ = readRelayFrame(conn)
		_ = writeRelayFrame(conn, relayFrame{Type: relayFrameError, Error: "invalid hello"})
		return nil, false
	})

	c := newDialableRelay(t, relay.addr, dir)
	_, err := c.Connect(context.Background())
	if !errors.Is(err, ErrRelayInvalidHello) {
		t.Fatalf("registration error = %v, want ErrRelayInvalidHello", err)
	}
	if !contains(err.Error(), "invalid hello") {
		t.Fatalf("registration error = %q, want relay reason", err)
	}
	state, reason := c.Status()
	if state != RelayError || !contains(reason, "invalid hello") {
		t.Fatalf("status = (%q, %q), want error with relay reason", state, reason)
	}
}

func TestRelayConnectRedactsTokenFromRelayError(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")
	wireToken := base64.StdEncoding.EncodeToString([]byte("carrier-secret"))

	relay := startStubRelay(t, func(conn net.Conn) (*yamux.Session, bool) {
		_, _ = readRelayFrame(conn)
		_ = writeRelayFrame(conn, relayFrame{Type: relayFrameError, Error: "bad tokens carrier-secret " + wireToken})
		return nil, false
	})

	c := newDialableRelay(t, relay.addr, dir)
	_, err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect accepted a rejected carrier")
	}
	if contains(err.Error(), "carrier-secret") {
		t.Fatal("registration error leaked the token")
	}
	if contains(err.Error(), wireToken) {
		t.Fatal("registration error leaked the wire token")
	}
	if !contains(err.Error(), "[redacted]") {
		t.Fatalf("registration error = %q, want redaction marker", err)
	}
	_, reason := c.Status()
	if contains(reason, "carrier-secret") {
		t.Fatal("error state leaked the token")
	}
	if contains(reason, wireToken) {
		t.Fatal("error state leaked the wire token")
	}
}

// A missing ack payload is malformed and must be rejected.
func TestRelayConnectRejectsNilAck(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")
	relay := startStubRelay(t, func(conn net.Conn) (*yamux.Session, bool) {
		_, _ = readRelayFrame(conn)
		_ = writeRelayFrame(conn, relayFrame{Type: relayFrameAck, Ack: nil})
		return nil, false
	})

	c := newDialableRelay(t, relay.addr, dir)
	if _, err := c.Connect(context.Background()); err == nil {
		t.Fatal("Connect accepted an ack with no payload")
	}
}

// Per PROTOCOL.md the ack `version` is informational: version negotiation
// happens at the hello stage, so the carrier must proceed on a higher ack
// version — a future relay may bump the ack version while still accepting
// version:1 hellos. Enforcing equality here would put the carrier into a
// permanent reject/reconnect loop against a relay that is otherwise serving it.
func TestRelayConnectAcceptsForwardCompatibleAckVersion(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")
	relay := startStubRelay(t, func(conn net.Conn) (*yamux.Session, bool) {
		frame, err := readRelayFrame(conn)
		if err != nil || frame.Type != relayFrameHello {
			return nil, false
		}
		_ = writeRelayFrame(conn, relayFrame{Type: relayFrameAck, Ack: &relayAck{Version: relayProtoVersion + 1, CarrierID: "carrier-1"}})
		s, serr := yamux.Server(conn, relayYamuxConfig())
		if serr != nil {
			return nil, false
		}
		return s, true
	})

	c := newDialableRelay(t, relay.addr, dir)
	session, err := c.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect rejected a forward-compatible ack version: %v", err)
	}
	_ = session.Close()
}

// feature:self-hosted-relay — the carry actually forwards traffic: after the
// handshake the relay opens one yamux stream per inbound client; Carry splices
// each to the local backend (here a stub echo server). Bytes written into the
// relay stream come back from the backend through the carrier unmodified — the
// tunnel is real, and RelayConnected reflects a connection that forwards.
func TestRelayCarryForwardsStreamToBackend(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")

	// Stub local backend: an echo server (stands in for the server's loopback
	// TLS listener; we assert byte transparency, not the TLS termination).
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			conn, aerr := backend.Accept()
			if aerr != nil {
				return
			}
			go func() { _, _ = io.Copy(conn, conn); conn.Close() }()
		}
	}()

	serverSession := make(chan *yamux.Session, 1)
	relay := startStubRelay(t, func(conn net.Conn) (*yamux.Session, bool) {
		frame, err := readRelayFrame(conn)
		if err != nil || frame.Type != relayFrameHello {
			return nil, false
		}
		_ = writeRelayFrame(conn, relayFrame{Type: relayFrameAck, Ack: &relayAck{Version: relayProtoVersion, CarrierID: "carrier-1"}})
		s, serr := yamux.Server(conn, relayYamuxConfig())
		if serr != nil {
			return nil, false
		}
		serverSession <- s
		return s, true
	})

	c := newDialableRelay(t, relay.addr, dir)
	c.SetBackendAddr(backend.Addr().String())

	session, err := c.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	carryDone := make(chan struct{})
	go func() { _ = c.Carry(ctx, session); close(carryDone) }()

	// The relay opens a stream toward the carrier (one per inbound client).
	var srv *yamux.Session
	select {
	case srv = <-serverSession:
	case <-time.After(2 * time.Second):
		t.Fatal("relay never got a yamux server session")
	}
	stream, err := srv.Open()
	if err != nil {
		t.Fatalf("relay open stream: %v", err)
	}
	defer stream.Close()

	payload := []byte("relayed-bytes-pass-through")
	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("write to relay stream: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(stream, got); err != nil {
		t.Fatalf("read echoed bytes: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echoed bytes = %q, want %q (carry altered payload)", got, payload)
	}

	if state, _ := c.Status(); state != RelayConnected {
		t.Fatalf("state = %q during live carry, want connected", state)
	}

	// Cancelling tears the carry down and reverts the state honestly.
	cancel()
	select {
	case <-carryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Carry did not exit on ctx cancel")
	}
	if state, _ := c.Status(); state != RelayDisconnected {
		t.Fatalf("state after carry exit = %q, want disconnected", state)
	}
}

// feature:self-hosted-relay — the v2 QUIC carrier performs the same pinned
// Hello/Ack exchange on its first stream and carries later relay-opened streams
// without the v1 TCP/yamux head-of-line coupling.
func TestRelayQUICConnectAndCarry(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")

	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			conn, aerr := backend.Accept()
			if aerr != nil {
				return
			}
			go func() { _, _ = io.Copy(conn, conn); _ = conn.Close() }()
		}
	}()

	cert := selfSignedCert(t)
	listener, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{relayQUICALPN},
		MinVersion:   tls.VersionTLS13,
	}, relayQUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverConn := make(chan *quic.Conn, 1)
	hello := make(chan *relayHello, 1)
	go func() {
		conn, aerr := listener.Accept(context.Background())
		if aerr != nil {
			return
		}
		stream, aerr := conn.AcceptStream(context.Background())
		if aerr != nil {
			return
		}
		frame, aerr := readRelayFrame(stream)
		if aerr != nil || frame.Type != relayFrameHello || frame.Hello == nil {
			return
		}
		hello <- frame.Hello
		if writeRelayFrame(stream, relayFrame{Type: relayFrameAck, Ack: &relayAck{
			Version: relayProtoVersion, CarrierID: "carrier-quic", Hostnames: frame.Hello.Hostnames,
		}}) != nil {
			return
		}
		serverConn <- conn
	}()

	ra := config.RemoteAccessConfig{Relay: config.RelayConfig{
		Endpoint: listener.Addr().String(), PublicHost: "remote.example.com", Transport: config.RelayTransportQUIC,
	}}
	c, err := NewRelayClient(ra, dir)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	c.dialQUIC = func(ctx context.Context, endpoint string) (*quic.Conn, error) {
		return quic.DialAddr(ctx, endpoint, &tls.Config{
			ServerName: "relay.example.com", RootCAs: roots, NextProtos: []string{relayQUICALPN}, MinVersion: tls.VersionTLS13,
		}, relayQUICConfig())
	}
	c.SetBackendAddr(backend.Addr().String())
	session, err := c.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect QUIC: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	carryDone := make(chan struct{})
	go func() { _ = c.Carry(ctx, session); close(carryDone) }()

	select {
	case got := <-hello:
		if string(got.Token) != "carrier-secret" || len(got.Hostnames) != 1 || got.Hostnames[0] != "remote.example.com" {
			t.Fatalf("QUIC hello = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("QUIC relay never received hello")
	}

	var relayConn *quic.Conn
	select {
	case relayConn = <-serverConn:
	case <-time.After(2 * time.Second):
		t.Fatal("QUIC relay never completed handshake")
	}
	stream, err := relayConn.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("quic-relayed-bytes")
	if _, err := stream.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(stream, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("QUIC echo = %q, want %q", got, payload)
	}
	_ = stream.Close()
	cancel()
	select {
	case <-carryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("QUIC Carry did not exit on cancellation")
	}
}

func TestRelayQUICConfigMatchesProducer(t *testing.T) {
	cfg := relayQUICConfig()
	if cfg.MaxStreamReceiveWindow != 8*1024*1024 || cfg.MaxConnectionReceiveWindow != 64*1024*1024 ||
		cfg.MaxIncomingStreams != 1024 || cfg.KeepAlivePeriod != 15*time.Second {
		t.Fatalf("QUIC config drifted from producer: %+v", cfg)
	}
}

func TestRelayCarryLimitsConcurrentStreams(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := backend.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	carrierConn, relayConn := net.Pipe()
	carrierSession, err := yamux.Client(carrierConn, relayYamuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	relaySession, err := yamux.Server(relayConn, relayYamuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer relaySession.Close()

	c := newConfiguredRelay(t, t.TempDir())
	c.maxStreams = 1
	c.SetBackendAddr(backend.Addr().String())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = c.Carry(ctx, carrierSession)
		close(done)
	}()

	first, err := relaySession.Open()
	if err != nil {
		t.Fatalf("open first stream: %v", err)
	}
	defer first.Close()
	var held net.Conn
	select {
	case held = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("first stream did not reach backend")
	}
	defer held.Close()

	second, err := relaySession.Open()
	if err != nil {
		t.Fatalf("open second stream: %v", err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	var b [1]byte
	if _, err := second.Read(b[:]); err == nil {
		t.Fatal("second stream stayed open despite stream limit")
	}

	cancel()
	_ = carrierSession.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Carry did not stop")
	}
}

// stubRelay is an in-process relay carrier listener for tests: a real TLS 1.3
// listener that runs a caller-supplied server-side handshake per accepted
// carrier and keeps the resulting yamux session alive until the test ends.
type stubRelay struct{ addr string }

func startStubRelay(t *testing.T, handshake func(net.Conn) (*yamux.Session, bool)) stubRelay {
	t.Helper()
	cert := selfSignedCert(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			session, ok := handshake(conn)
			if !ok {
				conn.Close()
				continue
			}
			// Hold the session open for the test's lifetime.
			t.Cleanup(func() { session.Close() })
		}
	}()
	return stubRelay{addr: ln.Addr().String()}
}

// newDialableRelay builds a configured RelayClient whose dial trusts the stub
// relay's self-signed cert (test-only).
func newDialableRelay(t *testing.T, endpoint, secretDir string) *RelayClient {
	t.Helper()
	ra := config.RemoteAccessConfig{Relay: config.RelayConfig{
		Endpoint:   endpoint,
		PublicHost: "remote.example.com",
	}}
	c, err := NewRelayClient(ra, secretDir)
	if err != nil {
		t.Fatal(err)
	}
	c.dial = func(ctx context.Context, ep string) (net.Conn, error) {
		d := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}}
		return d.DialContext(ctx, "tcp", ep)
	}
	return c
}

// feature:self-hosted-relay — an endpoint with no public host (no domain to
// route by) is an honest error, not a silent connect.
func TestRelayRequiresPublicHost(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "carrier-secret")
	ra := config.RemoteAccessConfig{Relay: config.RelayConfig{Endpoint: "relay.example.com:7443"}}
	c, err := NewRelayClient(ra, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(context.Background()); err == nil {
		t.Fatal("Connect succeeded with no public host")
	}
}

func newConfiguredRelay(t *testing.T, secretDir string) *RelayClient {
	t.Helper()
	ra := config.RemoteAccessConfig{Relay: config.RelayConfig{
		Endpoint:   "relay.example.com:7443",
		PublicHost: "remote.example.com",
	}}
	c, err := NewRelayClient(ra, secretDir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeSecret(t *testing.T, dir, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, relayTokenFile), []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "relay-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"relay.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
