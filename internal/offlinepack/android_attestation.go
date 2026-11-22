package offlinepack

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	_ "embed"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// Android Key Attestation (ADR-0208 DEVICE_KEY_ATTESTATION, CORE-3189).
//
// The attestation a Device sends is the Keystore certificate chain of its
// device key as concatenated DER certificates, leaf first — the order
// KeyStore.getCertificateChain returns. The key must be generated with the
// enrollment ID (the Device ID) as its attestation challenge.
//
// A chain is accepted when every link verifies, the last certificate carries
// a Google Hardware Attestation root key, the leaf's KeyDescription says the
// key lives in a TEE or StrongBox and was generated there (so it cannot be
// exported), the challenge is the enrollment ID, and the leaf key is the
// device key being registered. Revocation lists are not consulted: the server
// may run offline.

// keyDescriptionOID is the Android Key Attestation extension.
var keyDescriptionOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}

const (
	attestationSecuritySoftware = 0
	// attestationSecurityTEE and StrongBox (2) are hardware levels.
	attestationSecurityTEE = 1
	// authorizationTagOrigin is the KeyMint Tag::ORIGIN number; 0 is GENERATED.
	authorizationTagOrigin = 702
	keyOriginGenerated     = 0
)

//go:embed google_attestation_roots.pem
var googleAttestationRootsPEM []byte

// AndroidKeyAttestationOptions configures NewAndroidKeyAttestationVerifier.
type AndroidKeyAttestationOptions struct {
	// AllowSoftware accepts a software-level chain that does not reach a
	// Google root, as an emulator produces. It is for development only: the
	// composition root enables it only on an explicit opt-in in a development
	// build, never by default.
	AllowSoftware bool
	// Now is the verification clock; nil means time.Now.
	Now func() time.Time
	// roots replaces the Google roots in tests.
	roots []*x509.Certificate
}

// AndroidKeyAttestationVerifier verifies Android Key Attestation chains.
type AndroidKeyAttestationVerifier struct {
	opts  AndroidKeyAttestationOptions
	roots []*x509.Certificate
}

// NewAndroidKeyAttestationVerifier returns a verifier trusting the embedded
// Google Hardware Attestation roots.
func NewAndroidKeyAttestationVerifier(opts AndroidKeyAttestationOptions) (*AndroidKeyAttestationVerifier, error) {
	roots := opts.roots
	if roots == nil {
		var err error
		roots, err = parseAttestationRoots(googleAttestationRootsPEM)
		if err != nil {
			return nil, err
		}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &AndroidKeyAttestationVerifier{opts: opts, roots: roots}, nil
}

func parseAttestationRoots(data []byte) ([]*x509.Certificate, error) {
	var roots []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse attestation root: %w", err)
		}
		roots = append(roots, cert)
	}
	if len(roots) == 0 {
		return nil, errors.New("no attestation roots")
	}
	return roots, nil
}

// VerifyDeviceKey implements DeviceKeyAttestationVerifier.
func (v *AndroidKeyAttestationVerifier) VerifyDeviceKey(_ context.Context, enrollmentID string, publicKey, attestation []byte) error {
	if len(attestation) == 0 || attestation[0] != 0x30 {
		// Not a DER chain. Apple Secure Enclave attestation is CORE-1926.
		return errors.New("attestation is not an Android Key Attestation certificate chain")
	}
	chain, err := x509.ParseCertificates(attestation)
	if err != nil {
		return fmt.Errorf("attestation chain is malformed: %w", err)
	}
	if len(chain) < 2 {
		return errors.New("attestation chain needs a leaf and an issuer")
	}
	if err := v.verifyChain(chain); err != nil {
		return err
	}
	desc, err := leafKeyDescription(chain[0])
	if err != nil {
		return err
	}
	if !bytes.Equal(desc.AttestationChallenge, []byte(enrollmentID)) {
		return errors.New("attestation challenge is not the enrollment ID")
	}
	if !v.opts.AllowSoftware {
		if desc.AttestationSecurityLevel < attestationSecurityTEE || desc.KeymasterSecurityLevel < attestationSecurityTEE {
			return errors.New("attested key is not in a TEE or StrongBox")
		}
	}
	enforced := desc.HardwareEnforced
	if v.opts.AllowSoftware && desc.AttestationSecurityLevel == attestationSecuritySoftware {
		enforced = desc.SoftwareEnforced
	}
	origin, ok, err := authorizationInt(enforced, authorizationTagOrigin)
	if err != nil {
		return err
	}
	if !ok || origin != keyOriginGenerated {
		return errors.New("attested key was not generated in the secure element")
	}
	return leafKeyMatches(chain[0], publicKey)
}

// verifyChain checks each link and its validity window, and that the chain
// ends at a trusted root key. The root is matched by key, as Google advises,
// because the same root key has been reissued under several certificates.
func (v *AndroidKeyAttestationVerifier) verifyChain(chain []*x509.Certificate) error {
	now := v.opts.Now()
	last := chain[len(chain)-1]
	trusted := false
	for _, root := range v.roots {
		if bytes.Equal(last.RawSubjectPublicKeyInfo, root.RawSubjectPublicKeyInfo) {
			trusted = true
			break
		}
	}
	if !trusted && !v.opts.AllowSoftware {
		return errors.New("attestation chain does not end at a Google attestation root")
	}
	for i, cert := range chain {
		isRoot := i == len(chain)-1
		checkValidity := !isRoot || !trusted
		if checkValidity && (now.Before(cert.NotBefore) || now.After(cert.NotAfter)) {
			return fmt.Errorf("attestation certificate %d is outside its validity period", i)
		}
		parent := cert
		if !isRoot {
			parent = chain[i+1]
		}
		if err := cert.CheckSignatureFrom(parent); err != nil {
			return fmt.Errorf("attestation certificate %d does not verify: %w", i, err)
		}
	}
	return nil
}

type keyDescription struct {
	AttestationVersion       int
	AttestationSecurityLevel asn1.Enumerated
	KeymasterVersion         int
	KeymasterSecurityLevel   asn1.Enumerated
	AttestationChallenge     []byte
	UniqueID                 []byte
	SoftwareEnforced         asn1.RawValue
	HardwareEnforced         asn1.RawValue
}

func leafKeyDescription(leaf *x509.Certificate) (keyDescription, error) {
	for _, ext := range leaf.Extensions {
		if !ext.Id.Equal(keyDescriptionOID) {
			continue
		}
		var desc keyDescription
		if _, err := asn1.Unmarshal(ext.Value, &desc); err != nil {
			return keyDescription{}, fmt.Errorf("attestation KeyDescription is malformed: %w", err)
		}
		return desc, nil
	}
	return keyDescription{}, errors.New("attestation leaf has no KeyDescription")
}

// authorizationInt reads an INTEGER tag from an AuthorizationList, whose
// entries are explicitly context-tagged by KeyMint tag number.
func authorizationInt(list asn1.RawValue, tag int) (int, bool, error) {
	rest := list.Bytes
	for len(rest) > 0 {
		var entry asn1.RawValue
		var err error
		rest, err = asn1.Unmarshal(rest, &entry)
		if err != nil {
			return 0, false, fmt.Errorf("attestation authorization list is malformed: %w", err)
		}
		if entry.Class != asn1.ClassContextSpecific || entry.Tag != tag {
			continue
		}
		var value int
		if _, err := asn1.Unmarshal(entry.Bytes, &value); err != nil {
			return 0, false, fmt.Errorf("attestation authorization %d is malformed: %w", tag, err)
		}
		return value, true, nil
	}
	return 0, false, nil
}

func leafKeyMatches(leaf *x509.Certificate, publicKey []byte) error {
	key, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("attested key is not an EC key")
	}
	ecdhKey, err := key.ECDH()
	if err != nil {
		return errors.New("attested key is not a usable EC key")
	}
	if !bytes.Equal(ecdhKey.Bytes(), publicKey) {
		return errors.New("attested key is not the device key")
	}
	return nil
}
