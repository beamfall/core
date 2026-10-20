package remoteaccess

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// feature:client-cert-pinning — the self-signed identity is
// generated + persisted 0600 on first boot, reused (SAME SPKI) on the next boot,
// and its pin is the SHA-256 of the leaf SPKI in HPKP form.
func TestSelfSignedPersistsAndPinsStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	first, err := LoadOrCreateSelfSigned(dir)
	if err != nil {
		t.Fatalf("first LoadOrCreateSelfSigned: %v", err)
	}
	keyPath := filepath.Join(dir, selfSignedKeyFile)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("key not persisted: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v, want 0600 (invariant 5)", info.Mode().Perm())
	}

	pin1, err := first.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatalf("ServerSPKIPin: %v", err)
	}
	// The pin is the SHA-256 of the served leaf's SPKI in HPKP form.
	leaf, err := x509.ParseCertificate(first.cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	want := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
	if pin1 != want {
		t.Fatalf("pin = %q, want %q", pin1, want)
	}

	// A second load reuses the persisted keypair — the SPKI (and every client's
	// pin) is stable across restart.
	second, err := LoadOrCreateSelfSigned(dir)
	if err != nil {
		t.Fatalf("second LoadOrCreateSelfSigned: %v", err)
	}
	pin2, err := second.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatalf("ServerSPKIPin (2): %v", err)
	}
	if pin1 != pin2 {
		t.Fatalf("pin changed across restart: %q -> %q (would mass-invalidate pins)", pin1, pin2)
	}
}

func TestSelfSignedRecoversAfterInterruptedInitialWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, selfSignedKeyFile)
	if err := os.Mkdir(keyPath, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrCreateSelfSigned(dir); err == nil {
		t.Fatal("expected initial key write to fail")
	}
	if _, err := os.Stat(filepath.Join(dir, selfSignedCertFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed generation left a certificate-only identity: %v", err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSelfSigned(dir); err != nil {
		t.Fatalf("retry after transient write failure: %v", err)
	}
}

func TestSelfSignedTightensPreexistingKeyFileMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, selfSignedKeyFile)
	if err := os.WriteFile(keyPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrCreateSelfSigned(dir); err != nil {
		t.Fatalf("LoadOrCreateSelfSigned: %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("key mode = %o, want 600", got)
	}
}

// feature:client-cert-pinning — a malformed persisted identity is a
// hard error (fail-closed), never a silent regeneration that would invalidate
// every client's pin.
func TestSelfSignedFailsClosedOnMalformed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, selfSignedCertFile), []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, selfSignedKeyFile), []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSelfSigned(dir); err == nil {
		t.Fatal("expected error on malformed identity, got nil (must fail closed)")
	}
}

func TestSelfSignedRejectsLooseKeyMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if _, err := LoadOrCreateSelfSigned(dir); err != nil {
		t.Fatalf("initial LoadOrCreateSelfSigned: %v", err)
	}
	keyPath := filepath.Join(dir, selfSignedKeyFile)
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSelfSigned(dir); err == nil {
		t.Fatal("expected loose key mode error, got nil")
	}
}

// feature:dual-url-endpoints — the TLS-mux serves BOTH a cleartext HTTP
// client (LAN path) and a TLS client (overlay path) on ONE bound port. The TLS
// client pins the self-signed SPKI, proving the advertised https overlay scheme
// actually handshakes against the server's own identity — the fix for the
// overlay dead-endpoint.
// feature:client-cert-pinning (NET-02/03, ADR-0046 §7.3) — ComposeWithACME must
// select the served identity by SNI: the public FQDN gets the ACME config, while
// every other ServerName (overlay/LAN IP, no SNI) stays on the self-signed
// identity. This is the GAP 1/2 fix — the earlier wholesale replacement pointed
// overlay/relay at the ACME HostPolicy and dead-ended them.
func TestComposeWithACMESelectsIdentityBySNI(t *testing.T) {
	const domain = "home.example.com"
	selfID, err := LoadOrCreateSelfSigned(filepath.Join(t.TempDir(), "self"))
	if err != nil {
		t.Fatalf("self LoadOrCreateSelfSigned: %v", err)
	}
	// A second, distinct self-signed identity stands in for the ACME cert: it has
	// a different SPKI, so a served-pin comparison unambiguously tells the two
	// apart. Its config carries a HostPolicy-style GetCertificate that ONLY answers
	// for the FQDN, mirroring autocert refusing any other SNI.
	acmeID, err := LoadOrCreateSelfSigned(filepath.Join(t.TempDir(), "acme"))
	if err != nil {
		t.Fatalf("acme LoadOrCreateSelfSigned: %v", err)
	}
	acmeConf := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.ServerName != domain {
				return nil, errors.New("acme HostPolicy: refusing SNI " + hello.ServerName)
			}
			return &acmeID.cert, nil
		},
	}
	selfPin, err := selfID.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	acmePin, err := acmeID.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if selfPin == acmePin {
		t.Fatal("test setup: self and acme identities must have distinct SPKIs")
	}

	composed := selfID.ComposeWithACME(acmeConf, domain)

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := NewTLSMuxListener(raw, composed)
	defer ln.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	go srv.Serve(ln)
	defer srv.Close()
	addr := raw.Addr().String()

	servedPin := func(t *testing.T, sni string) string {
		t.Helper()
		conn, err := tls.Dial("tcp", addr, &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true, // pin-based, not CA/name
			MinVersion:         tls.VersionTLS12,
		})
		if err != nil {
			t.Fatalf("dial SNI=%q: %v", sni, err)
		}
		defer conn.Close()
		leaf := conn.ConnectionState().PeerCertificates[0]
		sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
	}

	// Public FQDN SNI → ACME cert (browser trust).
	if got := servedPin(t, domain); got != acmePin {
		t.Fatalf("FQDN SNI served pin = %q, want ACME pin %q", got, acmePin)
	}
	// Overlay/LAN-IP SNI (not the FQDN) → self-signed identity, NOT the ACME
	// HostPolicy. This is the endpoint that used to dead-end.
	if got := servedPin(t, "192.168.1.20"); got != selfPin {
		t.Fatalf("overlay-IP SNI served pin = %q, want self-signed pin %q", got, selfPin)
	}
	// No SNI at all (bare IP dial with no ServerName) → self-signed identity.
	if got := servedPin(t, ""); got != selfPin {
		t.Fatalf("no-SNI served pin = %q, want self-signed pin %q", got, selfPin)
	}
}

// feature:client-cert-pinning — with no ACME configured, ComposeWithACME
// returns the plain self-signed config (every connection served self-signed).
func TestComposeWithACMEFallsBackToSelfSignedWithoutACME(t *testing.T) {
	selfID, err := LoadOrCreateSelfSigned(filepath.Join(t.TempDir(), "self"))
	if err != nil {
		t.Fatalf("LoadOrCreateSelfSigned: %v", err)
	}
	if got := selfID.ComposeWithACME(nil, "home.example.com"); got != selfID.TLSConfig() {
		t.Fatal("nil ACME config: want the plain self-signed config")
	}
	if got := selfID.ComposeWithACME(&tls.Config{}, ""); got != selfID.TLSConfig() {
		t.Fatal("empty direct domain: want the plain self-signed config")
	}
}

func TestTLSMuxServesBothSchemesOnOnePort(t *testing.T) {
	id, err := LoadOrCreateSelfSigned(filepath.Join(t.TempDir(), "tls"))
	if err != nil {
		t.Fatalf("LoadOrCreateSelfSigned: %v", err)
	}
	pin, err := id.ServerSPKIPin(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := NewTLSMuxListener(raw, id.TLSConfig())
	defer ln.Close()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	go srv.Serve(ln)
	defer srv.Close()

	addr := raw.Addr().String()

	// Cleartext HTTP (LAN scheme) connects on the same port.
	httpClient := &http.Client{Timeout: 3 * time.Second}
	resHTTP, err := httpClient.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("http GET on mux port: %v", err)
	}
	resHTTP.Body.Close()
	if resHTTP.StatusCode != http.StatusOK {
		t.Fatalf("http status = %d, want 200", resHTTP.StatusCode)
	}

	// TLS (overlay scheme) connects on the same port, validated by SPKI pin.
	httpsClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // trust is established by the SPKI pin below, not CA/name
			VerifyConnection: func(cs tls.ConnectionState) error {
				got := "sha256-" + base64.StdEncoding.EncodeToString(func() []byte {
					s := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
					return s[:]
				}())
				if got != pin {
					return errors.New("SPKI pin mismatch")
				}
				return nil
			},
			MinVersion: tls.VersionTLS12,
		}},
	}
	resHTTPS, err := httpsClient.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("https GET on mux port (pinned): %v", err)
	}
	resHTTPS.Body.Close()
	if resHTTPS.StatusCode != http.StatusOK {
		t.Fatalf("https status = %d, want 200", resHTTPS.StatusCode)
	}
}
