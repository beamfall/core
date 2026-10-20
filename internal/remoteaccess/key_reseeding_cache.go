package remoteaccess

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// keyReseedingCertificateOID marks the temporary, self-signed leaf used only
// inside autocert to recover a signer from an expired cache entry.
var keyReseedingCertificateOID = []int{1, 3, 6, 1, 4, 1, 57264, 1, 1}

type keyReseedingCache struct {
	autocert.Cache
	domain string
	now    func() time.Time
}

func (c *keyReseedingCache) Get(ctx context.Context, name string) ([]byte, error) {
	bundle, err := c.Cache.Get(ctx, name)
	if err != nil || (name != c.domain && name != c.domain+"+rsa") {
		return bundle, err
	}
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	reseeded, ok := reseedExpiredCertificate(bundle, c.domain, now)
	if !ok {
		return bundle, nil
	}
	return reseeded, nil
}

func reseedExpiredCertificate(bundle []byte, domain string, now time.Time) ([]byte, bool) {
	privatePEM, rest := pem.Decode(bundle)
	if privatePEM == nil || !strings.Contains(privatePEM.Type, "PRIVATE") {
		return nil, false
	}
	signer, err := parseCachedSigner(privatePEM.Bytes)
	if err != nil {
		return nil, false
	}
	certificatePEM, _ := pem.Decode(rest)
	if certificatePEM == nil || certificatePEM.Type != "CERTIFICATE" {
		return nil, false
	}
	leaf, err := x509.ParseCertificate(certificatePEM.Bytes)
	if err != nil || !now.After(leaf.NotAfter) || leaf.VerifyHostname(domain) != nil {
		return nil, false
	}
	wantPublic, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil || !bytes.Equal(wantPublic, leaf.RawSubjectPublicKeyInfo) {
		return nil, false
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, false
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Second),
		DNSNames:     []string{domain},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		ExtraExtensions: []pkix.Extension{{
			Id:    keyReseedingCertificateOID,
			Value: []byte{0x05, 0x00}, // ASN.1 NULL
		}},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, signer.Public(), signer)
	if err != nil {
		return nil, false
	}
	reseeded := append([]byte(nil), pem.EncodeToMemory(privatePEM)...)
	reseeded = append(reseeded, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	return reseeded, true
}

func parseCachedSigner(der []byte) (crypto.Signer, error) {
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	switch key := key.(type) {
	case *rsa.PrivateKey:
		return key, nil
	case *ecdsa.PrivateKey:
		return key, nil
	default:
		return nil, errors.New("unsupported cached private key type")
	}
}

func isKeyReseedingCertificate(cert *tls.Certificate) bool {
	if cert == nil || len(cert.Certificate) == 0 {
		return false
	}
	leaf := cert.Leaf
	if leaf == nil {
		var err error
		leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return false
		}
	}
	for _, extension := range leaf.Extensions {
		if extension.Id.Equal(keyReseedingCertificateOID) {
			return true
		}
	}
	return false
}
