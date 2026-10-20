// Package remoteaccess builds the two server-side paths by which a Beamfall
// server is reachable from outside the LAN: direct HTTPS with an ACME-issued
// certificate and an outbound self-hosted relay carrier
// (ADR-0018/0025). Both are off by default — a plain LAN server uses neither.
//
// Byte-blindness: nothing in this package logs relayed bytes or the
// carrier token. The token is an opaque carrier credential resolved from a
// 0600 secret at use time (invariant 5), never held in config and never logged.
package remoteaccess

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"

	"github.com/beamfall/core/internal/config"
	"golang.org/x/crypto/acme/autocert"
)

// DirectHTTPS is the autocert-backed direct-HTTPS path. It owns the ACME
// account/cert cache and produces the *tls.Config the HTTP server listens with.
// The private key and ACME client live here on the home server and nowhere
// else.
type DirectHTTPS struct {
	cfg     config.DirectHTTPSConfig
	manager *autocert.Manager
}

var errExpiredCertificateReissuance = errors.New("direct HTTPS certificate reissuance pending")

// NewDirectHTTPS constructs the direct-HTTPS path from config. It returns
// (nil, nil) when the path is not enabled (no public domain, or terms not
// accepted) — a LAN-only server, which is not an error. When enabled it creates
// the autocert cache directory (0700) and wires a HostPolicy pinned to the one
// configured domain, so autocert never attempts a cert for an attacker-supplied
// SNI.
func NewDirectHTTPS(ra config.RemoteAccessConfig) (*DirectHTTPS, error) {
	if !ra.DirectHTTPSEnabled() {
		return nil, nil
	}
	dc := ra.DirectHTTPS
	if dc.ACMECacheDir == "" {
		return nil, errors.New("direct HTTPS enabled but ACME cache dir is empty")
	}
	if err := os.MkdirAll(dc.ACMECacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating ACME cache dir: %w", err)
	}
	cache := &keyReseedingCache{
		Cache:  autocert.DirCache(dc.ACMECacheDir),
		domain: dc.PublicDomain,
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS, // gated upstream by ACMETermsAccepted (DirectHTTPSEnabled)
		Cache:      cache,
		HostPolicy: autocert.HostWhitelist(dc.PublicDomain),
	}
	return &DirectHTTPS{cfg: dc, manager: m}, nil
}

// TLSConfig returns the *tls.Config the HTTPS listener serves with: autocert's
// GetCertificate obtains/renews the certificate on demand and advertises the
// ACME TLS-ALPN challenge so a fresh issuance completes on the same listener.
func (d *DirectHTTPS) TLSConfig() *tls.Config {
	cfg := d.manager.TLSConfig()
	cfg.MinVersion = tls.VersionTLS12
	getCertificate := cfg.GetCertificate
	cfg.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, err := getCertificate(hello)
		if err != nil {
			return nil, err
		}
		if isKeyReseedingCertificate(cert) {
			// The synthetic certificate exists only to put the expired bundle's
			// signer into autocert's renewal state. Never send it to a client: the
			// immediately scheduled renewal replaces it with the CA-issued leaf,
			// and a retry then succeeds with the original SPKI.
			return nil, errExpiredCertificateReissuance
		}
		return cert, nil
	}
	return cfg
}

// Manager exposes the underlying autocert manager so the app layer can wire the
// ACME HTTP-01 challenge handler (manager.HTTPHandler) onto the plain-HTTP
// listener and serve the HTTPS listener with TLSConfig.
func (d *DirectHTTPS) Manager() *autocert.Manager { return d.manager }

// Domain returns the configured public domain.
func (d *DirectHTTPS) Domain() string { return d.cfg.PublicDomain }

// RotateKey performs a DELIBERATE, operator-initiated key rotation (ADR-0046
// §2.2): it removes the cached certificate+private-key bundle for the configured
// domain so the next ACME issuance mints a FRESH keypair, producing a NEW
// SubjectPublicKeyInfo and therefore a NEW pinned SPKI. This is the opposite of
// a routine renewal: renewal before expiry reuses the cached key (autocert
// builds the CSR from the live certificate state's crypto.Signer), whereas
// rotation intentionally invalidates the pin.
//
// Expiry is materially different in golang.org/x/crypto v0.54.0: cacheGet
// rejects an expired bundle before returning its signer, then createCert creates
// a new key. keyReseedingCache substitutes a short-lived certificate carrying
// the expired bundle's existing signer. autocert accepts it into renewal state,
// schedules renewal immediately, and builds the new CSR with that signer. The
// TLS wrapper above refuses to serve the synthetic certificate while renewal is
// pending, so an offline-past-expiry restart retains the enrolled SPKI safely.
//
// Because the new pin differs from what remote-only clients recorded at LAN
// enrollment, those clients fail safe: they refuse to connect and surface the
// "re-pair on your home network (LAN)" path. Rotation is therefore an explicit
// operator act (compromise response or scheduled hygiene), never automatic.
//
// The cached key is load-bearing pinning material (invariant #5): it lives only
// in the 0700 autocert cache dir, written by autocert as a 0600 PEM bundle, and
// is never copied elsewhere here. RotateKey deletes it; it does not read, log,
// or duplicate the key bytes.
//
// Note on timing: stock autocert keeps the live keypair in an in-memory state
// map for the process lifetime and only re-reads the cache on startup, so a
// rotation takes effect on the next issuance — i.e. after the server restarts
// (or once the cached, now-removed cert is needed again on a cold path). The
// caller is expected to restart the listener after rotating. Removing a key
// that was never cached is a no-op (autocert's Cache.Delete returns nil for a
// missing key), so calling RotateKey before first issuance is harmless.
func (d *DirectHTTPS) RotateKey(ctx context.Context) error {
	if err := d.manager.Cache.Delete(ctx, d.cfg.PublicDomain); err != nil {
		return fmt.Errorf("rotating direct-HTTPS keypair: %w", err)
	}
	return nil
}

// CertState reports the honest certificate state for the Console: whether a
// certificate for the configured domain is already present in the autocert
// cache. It performs a cache lookup only — it never triggers issuance, so the
// Console reading status cannot accidentally burn an ACME rate-limit slot. An
// expired-but-cached cert still reports issued; autocert auto-renews it on the
// next handshake.
func (d *DirectHTTPS) CertState(ctx context.Context) CertState {
	_, err := d.manager.Cache.Get(ctx, d.cfg.PublicDomain)
	if err == nil {
		return CertIssued
	}
	return CertPending
}

// CertState is the honest direct-HTTPS certificate state surfaced to the
// operator Console.
type CertState string

const (
	// CertPending: direct HTTPS is enabled but no certificate is cached yet —
	// issuance happens on the first inbound TLS handshake for the domain.
	CertPending CertState = "pending"
	// CertIssued: a certificate for the domain is present in the cache.
	CertIssued CertState = "issued"
)
