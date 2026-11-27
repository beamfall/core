package offlinepack

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
)

// Apple App Attest (ADR-0208 DEVICE_KEY_ATTESTATION).
//
// iOS devices generate a non-exportable Secure Enclave P-256 ECDH key for
// wrapping Offline Pack keys, and attest it with DCAppAttestService using:
//
//	clientDataHash = SHA256("BFOP-apple-attest-v1\x00" || enrollmentID || "\x00" || devicePublicKeySEC1)
//
// The device sends the raw CBOR attestation object returned by
// DCAppAttestService.attestKey. This verifier validates the CBOR structure,
// verifies the x5c certificate chain up to the embedded Apple App Attestation
// Root CA, checks authData flags, signCount == 0, AAGUID, credID == SHA256(leafPubKey),
// optional rpIdHash against AllowedAppIDs, and verifies the leaf certificate
// nonce extension (OID 1.2.840.113635.100.8.2) equals SHA256(authData || clientDataHash).

//go:embed apple_app_attest_root.pem
var appleAttestationRootPEM []byte

var (
	appleNonceExtensionOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 2}
	appleProdAAGUID        = [16]byte{'a', 'p', 'p', 'a', 't', 't', 'e', 's', 't', 0, 0, 0, 0, 0, 0, 0}
	appleDevAAGUID         = [16]byte{'a', 'p', 'p', 'a', 't', 't', 'e', 's', 't', 'd', 'e', 'v', 'e', 'l', 'o', 'p'}
)

// AppleAppAttestOptions configures NewAppleAppAttestVerifier.
type AppleAppAttestOptions struct {
	// AllowDevelopment accepts the "appattestdevelop" AAGUID produced by
	// Xcode debug/development builds in addition to production "appattest".
	AllowDevelopment bool
	// AllowedAppIDs optionally restricts authData.rpIdHash to SHA256(appID)
	// for one of the listed "<TeamID>.<BundleID>" identifiers.
	AllowedAppIDs []string
	// Now is the verification clock; nil means time.Now.
	Now func() time.Time
	// roots replaces the embedded Apple root in tests.
	roots []*x509.Certificate
}

// AppleAppAttestVerifier verifies Apple App Attest CBOR attestation objects.
type AppleAppAttestVerifier struct {
	opts  AppleAppAttestOptions
	roots []*x509.Certificate
}

// NewAppleAppAttestVerifier returns a verifier trusting Apple's App Attestation Root CA.
func NewAppleAppAttestVerifier(opts AppleAppAttestOptions) (*AppleAppAttestVerifier, error) {
	roots := opts.roots
	if roots == nil {
		var err error
		roots, err = parseAttestationRoots(appleAttestationRootPEM)
		if err != nil {
			return nil, err
		}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &AppleAppAttestVerifier{opts: opts, roots: roots}, nil
}

// VerifyDeviceKey implements DeviceKeyAttestationVerifier.
func (v *AppleAppAttestVerifier) VerifyDeviceKey(_ context.Context, enrollmentID string, publicKey, attestation []byte) error {
	if _, err := parseP256PublicKey(publicKey); err != nil {
		return err
	}
	if enrollmentID == "" {
		return errors.New("enrollment ID is required")
	}
	if len(attestation) == 0 || (attestation[0]&0xe0) != 0xa0 {
		return errors.New("attestation is not an Apple App Attest CBOR map")
	}

	root, err := decodeStrictCBOR(attestation)
	if err != nil {
		return fmt.Errorf("malformed Apple attestation CBOR: %w", err)
	}
	topMap, ok := root.(map[string]any)
	if !ok {
		return errors.New("apple attestation root must be a CBOR map")
	}
	fmtStr, ok := topMap["fmt"].(string)
	if !ok || fmtStr != "apple-appattest" {
		return errors.New("apple attestation fmt must be apple-appattest")
	}
	authData, ok := topMap["authData"].([]byte)
	if !ok || len(authData) < 87 {
		return errors.New("apple attestation authData is missing or truncated")
	}
	attStmt, ok := topMap["attStmt"].(map[string]any)
	if !ok {
		return errors.New("apple attestation attStmt is missing")
	}
	x5cRaw, ok := attStmt["x5c"].([]any)
	if !ok || len(x5cRaw) < 2 {
		return errors.New("apple attestation x5c must contain at least leaf and intermediate certificates")
	}

	chain := make([]*x509.Certificate, 0, len(x5cRaw))
	for i, item := range x5cRaw {
		der, ok := item.([]byte)
		if !ok {
			return fmt.Errorf("apple attestation x5c[%d] is not a byte string", i)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("parse Apple attestation x5c[%d]: %w", i, err)
		}
		chain = append(chain, cert)
	}
	if err := v.verifyChain(chain); err != nil {
		return err
	}

	if err := v.verifyAuthData(authData, chain[0]); err != nil {
		return err
	}

	clientDataHash := appleClientDataHash(enrollmentID, publicKey)
	nonceInput := make([]byte, len(authData)+len(clientDataHash))
	copy(nonceInput, authData)
	copy(nonceInput[len(authData):], clientDataHash[:])
	expectedNonce := sha256.Sum256(nonceInput)

	certNonce, err := extractAppleNonceExtension(chain[0])
	if err != nil {
		return err
	}
	if !bytes.Equal(certNonce, expectedNonce[:]) {
		return errors.New("apple attestation nonce does not match device key and enrollment")
	}
	return nil
}

func (v *AppleAppAttestVerifier) verifyChain(chain []*x509.Certificate) error {
	if chain[0].IsCA {
		return errors.New("apple attestation leaf certificate cannot be a CA")
	}
	roots := x509.NewCertPool()
	for _, r := range v.roots {
		roots.AddCert(r)
	}
	intermediates := x509.NewCertPool()
	for _, cert := range chain[1:] {
		intermediates.AddCert(cert)
	}
	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   v.opts.Now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	if _, err := chain[0].Verify(opts); err != nil {
		return fmt.Errorf("verify Apple attestation chain: %w", err)
	}
	return nil
}

func (v *AppleAppAttestVerifier) verifyAuthData(authData []byte, leaf *x509.Certificate) error {
	rpHash := authData[:32]
	configured := false
	matched := false
	for _, appID := range v.opts.AllowedAppIDs {
		appID = strings.TrimSpace(appID)
		if appID == "" {
			continue
		}
		configured = true
		sum := sha256.Sum256([]byte(appID))
		if bytes.Equal(rpHash, sum[:]) {
			matched = true
			break
		}
	}
	if !configured {
		return errors.New("apple attestation allowed app IDs are not configured")
	}
	if !matched {
		return errors.New("apple attestation rpIdHash does not match any allowed app ID")
	}

	flags := authData[32]
	if flags&0x40 == 0 {
		return errors.New("apple attestation authData missing attested credential data flag")
	}
	signCount := binary.BigEndian.Uint32(authData[33:37])
	if signCount != 0 {
		return errors.New("apple attestation signCount must be zero")
	}
	var aaguid [16]byte
	copy(aaguid[:], authData[37:53])
	switch aaguid {
	case appleProdAAGUID:
	case appleDevAAGUID:
		if !v.opts.AllowDevelopment {
			return errors.New("apple development attestation is not allowed")
		}
	default:
		return errors.New("apple attestation AAGUID is unknown")
	}

	credIDLen := binary.BigEndian.Uint16(authData[53:55])
	if credIDLen != 32 || len(authData) < 55+int(credIDLen) {
		return errors.New("apple attestation credentialId length must be 32")
	}
	credID := authData[55:87]

	leafEC, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || leafEC.Curve != elliptic.P256() {
		return errors.New("apple attestation leaf key is not P-256")
	}
	ecdhKey, err := leafEC.ECDH()
	if err != nil {
		return fmt.Errorf("apple attestation leaf key: %w", err)
	}
	leafSum := sha256.Sum256(ecdhKey.Bytes())
	if !bytes.Equal(credID, leafSum[:]) {
		return errors.New("apple attestation credentialId does not match leaf public key")
	}
	return nil
}

func appleClientDataHash(enrollmentID string, publicKey []byte) [sha256.Size]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("BFOP-apple-attest-v1\x00"))
	_, _ = h.Write([]byte(enrollmentID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(publicKey)
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

func extractAppleNonceExtension(cert *x509.Certificate) ([]byte, error) {
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(appleNonceExtensionOID) {
			var outer asn1.RawValue
			rest, err := asn1.Unmarshal(ext.Value, &outer)
			if err != nil || len(rest) != 0 || outer.Class != asn1.ClassUniversal || outer.Tag != asn1.TagSequence || !outer.IsCompound {
				return nil, errors.New("apple attestation nonce extension is malformed")
			}
			var inner asn1.RawValue
			rest, err = asn1.Unmarshal(outer.Bytes, &inner)
			if err != nil || len(rest) != 0 || inner.Class != asn1.ClassContextSpecific || inner.Tag != 1 || !inner.IsCompound {
				return nil, errors.New("apple attestation nonce extension missing [1] field")
			}
			var nonce []byte
			rest, err = asn1.Unmarshal(inner.Bytes, &nonce)
			if err != nil || len(rest) != 0 || len(nonce) != sha256.Size {
				return nil, errors.New("apple attestation nonce octet string is invalid")
			}
			return nonce, nil
		}
	}
	return nil, errors.New("apple attestation leaf certificate missing nonce extension")
}

// MultiPlatformAttestationVerifier dispatches Android DER certificate chains
// (0x30) and Apple App Attest CBOR maps (0xa0..0xbf) to their respective verifiers.
type MultiPlatformAttestationVerifier struct {
	android DeviceKeyAttestationVerifier
	apple   DeviceKeyAttestationVerifier
}

// NewMultiPlatformAttestationVerifier combines Android and Apple attestation verifiers.
func NewMultiPlatformAttestationVerifier(android, apple DeviceKeyAttestationVerifier) *MultiPlatformAttestationVerifier {
	return &MultiPlatformAttestationVerifier{android: android, apple: apple}
}

// VerifyDeviceKey implements DeviceKeyAttestationVerifier.
func (m *MultiPlatformAttestationVerifier) VerifyDeviceKey(ctx context.Context, enrollmentID string, publicKey, attestation []byte) error {
	if len(attestation) == 0 {
		return errors.New("device attestation is empty")
	}
	switch {
	case attestation[0] == 0x30:
		if m.android == nil {
			return errors.New("android key attestation is not configured")
		}
		return m.android.VerifyDeviceKey(ctx, enrollmentID, publicKey, attestation)
	case (attestation[0] & 0xe0) == 0xa0:
		if m.apple == nil {
			return errors.New("apple App Attest is not configured")
		}
		return m.apple.VerifyDeviceKey(ctx, enrollmentID, publicKey, attestation)
	default:
		return errors.New("unrecognized device attestation format")
	}
}

// --- Minimal strict CBOR decoder for Apple App Attest ---

func decodeStrictCBOR(data []byte) (any, error) {
	r := &cborReader{data: data}
	val, err := r.readValue(0)
	if err != nil {
		return nil, err
	}
	if r.pos != len(r.data) {
		return nil, errors.New("trailing CBOR bytes")
	}
	return val, nil
}

type cborReader struct {
	data []byte
	pos  int
}

func (r *cborReader) readByte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, errors.New("unexpected end of CBOR")
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *cborReader) readBytes(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.data) {
		return nil, errors.New("unexpected end of CBOR")
	}
	out := r.data[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *cborReader) readUint(info byte) (uint64, error) {
	switch {
	case info < 24:
		return uint64(info), nil
	case info == 24:
		b, err := r.readByte()
		return uint64(b), err
	case info == 25:
		bs, err := r.readBytes(2)
		if err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint16(bs)), nil
	case info == 26:
		bs, err := r.readBytes(4)
		if err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint32(bs)), nil
	case info == 27:
		bs, err := r.readBytes(8)
		if err != nil {
			return 0, err
		}
		return binary.BigEndian.Uint64(bs), nil
	default:
		return 0, errors.New("unsupported CBOR length")
	}
}

func (r *cborReader) readValue(depth int) (any, error) {
	if depth > 16 {
		return nil, errors.New("CBOR nesting too deep")
	}
	head, err := r.readByte()
	if err != nil {
		return nil, err
	}
	major := head >> 5
	info := head & 0x1f
	n, err := r.readUint(info)
	if err != nil {
		return nil, err
	}
	switch major {
	case 0: // unsigned int
		return n, nil
	case 2: // byte string
		if n > uint64(len(r.data)-r.pos) { // #nosec G115 -- n is bounded by the remaining input above
			return nil, errors.New("CBOR byte string out of bounds")
		}
		bs, err := r.readBytes(int(n)) // #nosec G115 -- n is bounded by the remaining input above
		if err != nil {
			return nil, err
		}
		return slices.Clone(bs), nil
	case 3: // text string
		if n > uint64(len(r.data)-r.pos) { // #nosec G115 -- n is bounded by the remaining input above
			return nil, errors.New("CBOR text string out of bounds")
		}
		bs, err := r.readBytes(int(n)) // #nosec G115 -- n is bounded by the remaining input above
		if err != nil {
			return nil, err
		}
		return string(bs), nil
	case 4: // array
		if n > uint64(len(r.data)-r.pos) { // #nosec G115 -- n is bounded by the remaining input above
			return nil, errors.New("CBOR array too large")
		}
		out := make([]any, 0, int(n)) // #nosec G115 -- n is bounded by the remaining input above
		for range n {
			elem, err := r.readValue(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, elem)
		}
		return out, nil
	case 5: // map
		if n > uint64(len(r.data)-r.pos) { // #nosec G115 -- bounded input
			return nil, errors.New("CBOR map too large")
		}
		out := make(map[string]any, int(n)) // #nosec G115 -- bounded input
		for range n {
			k, err := r.readValue(depth + 1)
			if err != nil {
				return nil, err
			}
			ks, ok := k.(string)
			if !ok {
				return nil, errors.New("CBOR map key must be text string")
			}
			if _, exists := out[ks]; exists {
				return nil, errors.New("duplicate CBOR map key")
			}
			v, err := r.readValue(depth + 1)
			if err != nil {
				return nil, err
			}
			out[ks] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported CBOR major type %d", major)
	}
}

// Unused import guard for big.Int if needed in future ASN.1 checks.
var _ = big.NewInt
var _ = (*ecdh.PrivateKey)(nil)
