package remoteaccess

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SelfSignedIdentity is the baseline TLS identity every server holds.
// It is a persistent self-signed leaf whose SPKI is pinned at LAN enrollment
// (ADR-0046 machinery), so the overlay path and the relay carrier
// (ADR-0018/0025) can terminate client TLS WITHOUT operator ACME. For native
// clients that pin the SPKI at LAN enrollment this is STRONGER than system-trust
// ACME (the pin refuses any impostor cert, DNS-01-minted or otherwise); ACME
// remains the documented residual for browsers, which cannot pin.
//
// The keypair is generated once and persisted under <DataDir>/tls (0700 dir,
// 0600 key), then reused across restarts so the SPKI — and therefore every
// client's pin — stays stable. The private key is load-bearing pinning material
// (invariant 5): it lives only in the 0600 key file, is never logged, and is
// never copied elsewhere. The SPKI pin is a hash of the PUBLIC key and carries
// no secret.
type SelfSignedIdentity struct {
	cert tls.Certificate
	leaf *x509.Certificate

	mu   sync.Mutex
	conf *tls.Config
}

// selfSignedCertFile / selfSignedKeyFile are the persisted PEM filenames under
// the identity directory.
const (
	selfSignedCertFile = "self-signed.crt"
	selfSignedKeyFile  = "self-signed.key"
)

// LoadOrCreateSelfSigned loads the persistent self-signed identity from dir, or
// generates and persists a fresh one on first boot. dir is created 0700; the
// key PEM is written 0600 (invariant 5). Generation is deterministic in shape
// but the keypair is random, so each server gets a distinct identity and a
// distinct SPKI pin. Never returns nil without an error: every server has a TLS
// identity.
func LoadOrCreateSelfSigned(dir string) (*SelfSignedIdentity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating self-signed TLS dir: %w", err)
	}
	certPath := filepath.Join(dir, selfSignedCertFile)
	keyPath := filepath.Join(dir, selfSignedKeyFile)
	if _, err := os.Stat(certPath); err == nil {
		return loadSelfSigned(certPath, keyPath)
	}
	return generateSelfSigned(certPath, keyPath)
}

// loadSelfSigned reads an existing persisted keypair. A parse failure is a hard
// error (fail-closed): a malformed identity must never silently regenerate and
// mass-invalidate every client's pin.
func loadSelfSigned(certPath, keyPath string) (*SelfSignedIdentity, error) {
	info, err := os.Stat(keyPath)
	if err != nil {
		return nil, fmt.Errorf("stat self-signed key: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("self-signed key %s must be mode 0600", keyPath)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("loading self-signed identity: %w", err)
	}
	return newSelfSignedIdentity(cert)
}

// generateSelfSigned mints a fresh P-256 self-signed leaf and persists it. The
// certificate carries the loopback + a generic CN; clients validate it by SPKI
// pin, not by name/CA trust, so SANs are only cosmetic here.
func generateSelfSigned(certPath, keyPath string) (*SelfSignedIdentity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating self-signed key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generating self-signed serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "beamfall-server"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(100 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("creating self-signed certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshaling self-signed key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writePrivateFile(keyPath, keyPEM); err != nil {
		return nil, fmt.Errorf("writing self-signed key: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return nil, fmt.Errorf("writing self-signed cert: %w", err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("assembling self-signed identity: %w", err)
	}
	return newSelfSignedIdentity(cert)
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// newSelfSignedIdentity parses the leaf from the assembled keypair so the SPKI
// pin can be computed without re-reading the file.
func newSelfSignedIdentity(cert tls.Certificate) (*SelfSignedIdentity, error) {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parsing self-signed leaf: %w", err)
	}
	return &SelfSignedIdentity{cert: cert, leaf: leaf}, nil
}

// TLSConfig returns the *tls.Config serving this identity's fixed certificate.
// The config is built once and reused; it terminates client TLS for overlay
// endpoints and the relay carrier backend.
func (s *SelfSignedIdentity) TLSConfig() *tls.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conf == nil {
		s.conf = &tls.Config{
			Certificates: []tls.Certificate{s.cert},
			MinVersion:   tls.VersionTLS12,
		}
	}
	return s.conf
}

// ComposeWithACME returns ONE *tls.Config for the whole listener that selects the
// served identity by SNI: the operator's public FQDN
// (directDomain) is served the browser-trusted ACME config (acmeConf), while every
// other ServerName — a LAN IP, an overlay/Tailscale IP, the loopback relay-carrier
// backend, or a client that sends no SNI at all — is served this self-signed
// identity. This is the fix for the overlay/relay dead-endpoint (GAP 1/2): the
// former wiring wholesale-replaced the self-signed config with acmeConf, so an
// overlay or relay client (LAN-IP / no-FQDN SNI) hit the ACME HostPolicy, which
// refuses any SNI but the public domain and dead-ends. Composing by SNI keeps the
// self-signed identity backing overlay and relay REGARDLESS of whether ACME is
// configured, while ACME still issues + serves the public-domain cert (including
// the TLS-ALPN-01 challenge, which carries the FQDN SNI).
//
// When acmeConf is nil or directDomain is empty (the cert-less LAN/overlay
// majority), every connection is served the self-signed identity. directDomain is
// matched case-insensitively; SNI is lowercased by crypto/tls before it reaches
// GetConfigForClient, and directDomain is lowercased here so the two compare
// stably.
func (s *SelfSignedIdentity) ComposeWithACME(acmeConf *tls.Config, directDomain string) *tls.Config {
	selfConf := s.TLSConfig()
	domain := strings.ToLower(strings.TrimSpace(directDomain))
	if acmeConf == nil || domain == "" {
		return selfConf
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if strings.ToLower(hello.ServerName) == domain {
				return acmeConf, nil
			}
			return selfConf, nil
		},
	}
}

// ServerSPKIPin returns the HPKP-form SPKI pin of the persistent self-signed
// leaf, the identity a LAN-enrolling native client records TOFU
// and enforces on every later connection over overlay/relay. It reads the
// already-parsed leaf, so it never fails and never triggers issuance. ctx is
// accepted to satisfy the SPKIProvider seam; it is unused.
func (s *SelfSignedIdentity) ServerSPKIPin(_ context.Context) (string, error) {
	return spkiPin(s.leaf), nil
}
