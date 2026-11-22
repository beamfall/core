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
	"slices"
	"strings"
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
// device key being registered, generated for key agreement. Every
// certificate is checked against Google's revocation status list; a server
// that cannot fetch a fresh list for 30 days refuses rather than trust it.

// keyDescriptionOID is the Android Key Attestation extension.
var keyDescriptionOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}

var extKeyUsageOID = asn1.ObjectIdentifier{2, 5, 29, 37}

const (
	attestationSecuritySoftware  = 0
	attestationSecurityTEE       = 1
	attestationSecurityStrongBox = 2
	// authorizationTagPurpose is KeyMint Tag::PURPOSE; 6 is AGREE_KEY, the
	// ECDH purpose an ADR-0208 device key needs to unwrap a pack.
	authorizationTagPurpose = 1
	keyPurposeAgreeKey      = 6
	// authorizationTagOrigin is KeyMint Tag::ORIGIN; 0 is GENERATED.
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
	// Status returns Google's current revocation status list. It is
	// required unless AllowSoftware is set; an error refuses verification.
	Status func(context.Context) (*AttestationStatusList, error)
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
	if opts.Status == nil && !opts.AllowSoftware {
		return nil, errors.New("android key attestation needs a revocation status source")
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
func (v *AndroidKeyAttestationVerifier) VerifyDeviceKey(ctx context.Context, enrollmentID string, publicKey, attestation []byte) error {
	if len(attestation) == 0 || attestation[0] != 0x30 {
		// Not a DER chain. Apple Secure Enclave attestation is CORE-1926.
		return errors.New("attestation is not an Android Key Attestation certificate chain")
	}
	chain, err := parseAttestationChain(attestation)
	if err != nil {
		return fmt.Errorf("attestation chain is malformed: %w", err)
	}
	if len(chain) < 2 {
		return errors.New("attestation chain needs a leaf and an issuer")
	}
	// Only the leaf may carry a KeyDescription. An issuer carrying one is an
	// app-generated attestation key, whose signature on the leaf proves
	// nothing the hardware vouched for.
	for i, cert := range chain[1:] {
		if hasExtension(cert, keyDescriptionOID) {
			return fmt.Errorf("attestation certificate %d is an attested key, not a hardware issuer", i+1)
		}
	}
	if err := v.verifyChain(chain); err != nil {
		return err
	}
	if err := v.checkRevocation(ctx, chain); err != nil {
		return err
	}
	desc, err := leafKeyDescription(chain[0])
	if err != nil {
		return err
	}
	if !bytes.Equal(desc.AttestationChallenge, []byte(enrollmentID)) {
		return errors.New("attestation challenge is not the enrollment ID")
	}
	software := false
	for _, level := range []asn1.Enumerated{desc.AttestationSecurityLevel, desc.KeymasterSecurityLevel} {
		switch level {
		case attestationSecurityTEE, attestationSecurityStrongBox:
		case attestationSecuritySoftware:
			if !v.opts.AllowSoftware {
				return errors.New("attested key is not in a TEE or StrongBox")
			}
			software = true
		default:
			return fmt.Errorf("attestation security level %d is unknown", level)
		}
	}
	softwareList, err := parseAuthorizationList(desc.SoftwareEnforced)
	if err != nil {
		return err
	}
	enforced, err := parseAuthorizationList(desc.HardwareEnforced)
	if err != nil {
		return err
	}
	if software && desc.AttestationSecurityLevel == attestationSecuritySoftware {
		enforced = softwareList
	}
	origin, ok, err := enforced.integer(authorizationTagOrigin)
	if err != nil {
		return err
	}
	if !ok || origin != keyOriginGenerated {
		return errors.New("attested key was not generated in the secure element")
	}
	agree, err := enforced.hasPurpose(keyPurposeAgreeKey)
	if err != nil {
		return err
	}
	if !agree {
		return errors.New("attested key is not authorized for key agreement")
	}
	return leafKeyMatches(chain[0], publicKey)
}

// checkRevocation refuses a chain with any certificate on Google's status
// list. The dev emulator opt-in tolerates an unavailable list, never a
// listed certificate.
func (v *AndroidKeyAttestationVerifier) checkRevocation(ctx context.Context, chain []*x509.Certificate) error {
	if v.opts.Status == nil {
		return nil // AllowSoftware only; enforced by the constructor.
	}
	// On error the source may still return the last list it held: its
	// entries stay refused even in the dev opt-in.
	list, err := v.opts.Status(ctx)
	if err != nil && !v.opts.AllowSoftware {
		return err
	}
	for i, cert := range chain {
		if status, listed := list.status(cert.SerialNumber); listed {
			return fmt.Errorf("attestation certificate %d is %s", i, strings.ToLower(status))
		}
	}
	return nil
}

// parseAttestationChain reads the chain leaf first. Android sends
// CertPath.getEncoded(), a PkiPath: one SEQUENCE of certificates ordered from
// the trust anchor down (RFC 4158 / X.509 PkiPath), so it is reversed here.
// A plain concatenation of DER certificates, leaf first, is also accepted.
func parseAttestationChain(data []byte) ([]*x509.Certificate, error) {
	var elems []asn1.RawValue
	if rest, err := asn1.Unmarshal(data, &elems); err == nil && len(rest) == 0 && len(elems) > 0 {
		chain := make([]*x509.Certificate, 0, len(elems))
		for _, elem := range elems {
			cert, err := x509.ParseCertificate(elem.FullBytes)
			if err != nil {
				chain = nil
				break
			}
			chain = append(chain, cert)
		}
		if chain != nil {
			slices.Reverse(chain)
			return chain, nil
		}
	}
	return x509.ParseCertificates(data)
}

func hasExtension(cert *x509.Certificate, oid asn1.ObjectIdentifier) bool {
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oid) {
			return true
		}
	}
	return false
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
		// Under a trusted root only the leaf's window is checked: Google keeps
		// trusting factory-provisioned intermediates past their expiry, so an
		// older device's expired, unrevoked intermediate is still valid.
		checkValidity := i == 0 || !trusted
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
	if !trusted {
		return nil // dev emulator chain: links checked, no root to path-validate against.
	}
	// Full path validation enforces what single links cannot: path length
	// constraints, name constraints and unhandled critical extensions.
	// ExtKeyUsageAny below skips Go's EKU checks, so refuse any certificate
	// restricted to some other purpose. Attestation certificates carry none.
	// An EKU extension, even an empty one, must permit any purpose.
	for i, cert := range chain {
		if hasExtension(cert, extKeyUsageOID) && !slices.Contains(cert.ExtKeyUsage, x509.ExtKeyUsageAny) {
			return fmt.Errorf("attestation certificate %d is restricted to another extended key usage", i)
		}
	}
	pool := x509.NewCertPool()
	for _, root := range v.roots {
		pool.AddCert(root)
	}
	intermediates := x509.NewCertPool()
	for _, cert := range chain[1:] {
		intermediates.AddCert(cert)
	}
	// Verify checks every certificate's window at one instant. Expiry was
	// settled above, so pick the instant nearest now inside the window all the
	// chain's certificates share.
	at, ok := sharedValidityInstant(chain, now)
	if !ok {
		return errors.New("attestation chain's certificates share no validity period")
	}
	if _, err := chain[0].Verify(x509.VerifyOptions{
		Roots: pool, Intermediates: intermediates, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return fmt.Errorf("attestation chain does not verify to a Google attestation root: %w", err)
	}
	return nil
}

// sharedValidityInstant clamps now into the intersection of the chain's
// validity windows, reporting false when they do not overlap.
func sharedValidityInstant(chain []*x509.Certificate, now time.Time) (time.Time, bool) {
	from, until := chain[0].NotBefore, chain[0].NotAfter
	for _, cert := range chain[1:] {
		if cert.NotBefore.After(from) {
			from = cert.NotBefore
		}
		if cert.NotAfter.Before(until) {
			until = cert.NotAfter
		}
	}
	if from.After(until) {
		return time.Time{}, false
	}
	switch {
	case now.Before(from):
		return from, true
	case now.After(until):
		return until, true
	}
	return now, true
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
	var found []byte
	for _, ext := range leaf.Extensions {
		if !ext.Id.Equal(keyDescriptionOID) {
			continue
		}
		if found != nil {
			return keyDescription{}, errors.New("attestation leaf has more than one KeyDescription")
		}
		found = ext.Value
	}
	if found == nil {
		return keyDescription{}, errors.New("attestation leaf has no KeyDescription")
	}
	var desc keyDescription
	rest, err := asn1.Unmarshal(found, &desc)
	if err != nil {
		return keyDescription{}, fmt.Errorf("attestation KeyDescription is malformed: %w", err)
	}
	if len(rest) != 0 {
		return keyDescription{}, errors.New("attestation KeyDescription has trailing data")
	}
	// The struct decoder ignores extra SEQUENCE elements; count them.
	var outer asn1.RawValue
	if _, err := asn1.Unmarshal(found, &outer); err != nil {
		return keyDescription{}, fmt.Errorf("attestation KeyDescription is malformed: %w", err)
	}
	fields := 0
	for body := outer.Bytes; len(body) > 0; fields++ {
		var field asn1.RawValue
		if body, err = asn1.Unmarshal(body, &field); err != nil {
			return keyDescription{}, fmt.Errorf("attestation KeyDescription is malformed: %w", err)
		}
	}
	if fields != 8 {
		return keyDescription{}, fmt.Errorf("attestation KeyDescription has %d fields, want 8", fields)
	}
	return desc, nil
}

// authorizationList is a KeyMint AuthorizationList: a SEQUENCE of entries,
// each explicitly context-tagged by its KeyMint tag number, at most once.
type authorizationList map[int][]byte

func parseAuthorizationList(raw asn1.RawValue) (authorizationList, error) {
	if raw.Class != asn1.ClassUniversal || raw.Tag != asn1.TagSequence || !raw.IsCompound {
		return nil, errors.New("attestation authorization list is not a SEQUENCE")
	}
	list := authorizationList{}
	rest := raw.Bytes
	for len(rest) > 0 {
		var entry asn1.RawValue
		var err error
		rest, err = asn1.Unmarshal(rest, &entry)
		if err != nil {
			return nil, fmt.Errorf("attestation authorization list is malformed: %w", err)
		}
		if entry.Class != asn1.ClassContextSpecific || !entry.IsCompound {
			return nil, errors.New("attestation authorization entry is not explicitly tagged")
		}
		if _, dup := list[entry.Tag]; dup {
			return nil, fmt.Errorf("attestation authorization %d appears twice", entry.Tag)
		}
		// Each explicit tag wraps exactly one value.
		var value asn1.RawValue
		if tail, err := asn1.Unmarshal(entry.Bytes, &value); err != nil || len(tail) != 0 {
			return nil, fmt.Errorf("attestation authorization %d is malformed", entry.Tag)
		}
		list[entry.Tag] = entry.Bytes
	}
	return list, nil
}

// integer reads an INTEGER-valued entry.
func (l authorizationList) integer(tag int) (int, bool, error) {
	body, ok := l[tag]
	if !ok {
		return 0, false, nil
	}
	var value int
	rest, err := asn1.Unmarshal(body, &value)
	if err != nil || len(rest) != 0 {
		return 0, false, fmt.Errorf("attestation authorization %d is malformed", tag)
	}
	return value, true, nil
}

// hasPurpose reports whether the SET OF INTEGER purpose entry holds purpose.
func (l authorizationList) hasPurpose(purpose int) (bool, error) {
	body, ok := l[authorizationTagPurpose]
	if !ok {
		return false, nil
	}
	var purposes []int
	rest, err := asn1.UnmarshalWithParams(body, &purposes, "set")
	if err != nil || len(rest) != 0 {
		return false, errors.New("attestation key purposes are malformed")
	}
	for _, p := range purposes {
		if p == purpose {
			return true, nil
		}
	}
	return false, nil
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
