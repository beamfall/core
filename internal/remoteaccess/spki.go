package remoteaccess

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
)

// ErrNoServedCert means the direct-HTTPS path has no certificate to pin yet —
// none is cached, so issuance has not happened (the cert is minted on the first
// inbound TLS handshake). Callers treat this as "no pin available", never an
// error to surface.
var ErrNoServedCert = errors.New("no served certificate to pin")

// spkiPin returns the HPKP-form pin of a leaf certificate: the standard
// "sha256-" + base64(SHA-256(SubjectPublicKeyInfo)). The pin is
// over the SPKI, not the leaf bytes, so it survives Let's Encrypt renewals that
// reuse the keypair while rotating the certificate.
func spkiPin(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// ServerSPKIPin computes the SPKI pin of the live TLS keypair the server is
// actually serving for its configured public domain. It reads
// the leaf from the autocert cache — a lookup only, exactly like CertState, so
// reading the pin never triggers issuance and cannot burn an ACME rate-limit
// slot. When no certificate is cached yet it returns ("", ErrNoServedCert): the
// pin is simply not available, not an error.
//
// The SPKI is sourced from the certificate the server actually serves, so a
// LAN-enrolling client pins the same identity it will later validate over the
// relay. Only the leaf's public key is parsed; the private key in the cache
// bundle is left untouched.
func (d *DirectHTTPS) ServerSPKIPin(ctx context.Context) (string, error) {
	data, err := d.manager.Cache.Get(ctx, d.cfg.PublicDomain)
	if err != nil {
		return "", ErrNoServedCert
	}
	// autocert stores the private-key PEM block first, then the certificate
	// chain. Walk the blocks and take the first CERTIFICATE (the leaf); the key
	// blocks are intentionally not parsed here.
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return "", ErrNoServedCert
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("parsing served leaf certificate: %w", err)
		}
		return spkiPin(leaf), nil
	}
}
