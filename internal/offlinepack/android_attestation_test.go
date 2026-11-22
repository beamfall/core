package offlinepack

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"
)

const testEnrollmentID = "device-1"

var testNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

type attestationFixture struct {
	rootKey, interKey, leafKey *ecdsa.PrivateKey
	root, inter                *x509.Certificate
}

func newAttestationFixture(t *testing.T) *attestationFixture {
	t.Helper()
	return newAttestationFixtureWith(t, false)
}

// newAttestationFixtureWith can give the root pathLenConstraint=0, which the
// fixture's intermediate CA then violates.
func newAttestationFixtureWith(t *testing.T, rootPathLenZero bool) *attestationFixture {
	t.Helper()
	f := &attestationFixture{rootKey: newTestKey(t), interKey: newTestKey(t), leafKey: newTestKey(t)}
	f.root = signTestCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		MaxPathLenZero: rootPathLenZero,
	}, nil, &f.rootKey.PublicKey, f.rootKey)
	f.inter = signTestCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test intermediate"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}, f.root, &f.interKey.PublicKey, f.rootKey)
	return f
}

type leafSpec struct {
	challenge      string
	attestLevel    int
	keymasterLevel int
	origin         int
	noOrigin       bool
	dupOrigin      int // when non-zero, a second origin entry with this value
	purposes       []int
	trailing       bool
	extraField     bool // a ninth element inside the KeyDescription SEQUENCE
	emptyWrapper   bool // an explicit tag wrapping no value
	notAfter       time.Time
}

func defaultLeaf() leafSpec {
	return leafSpec{
		challenge: testEnrollmentID, attestLevel: 1, keymasterLevel: 1,
		purposes: []int{keyPurposeAgreeKey}, notAfter: testNow.Add(time.Hour),
	}
}

// chain returns the DER chain leaf → intermediate → root.
func (f *attestationFixture) chain(t *testing.T, spec leafSpec) []byte {
	t.Helper()
	leaf := signTestCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "Android Keystore Key"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: spec.notAfter,
		ExtraExtensions: []pkix.Extension{{Id: keyDescriptionOID, Value: keyDescriptionDER(t, spec)}},
	}, f.inter, &f.leafKey.PublicKey, f.interKey)
	var out []byte
	for _, cert := range []*x509.Certificate{leaf, f.inter, f.root} {
		out = append(out, cert.Raw...)
	}
	return out
}

func (f *attestationFixture) verifier(t *testing.T, allowSoftware bool) *AndroidKeyAttestationVerifier {
	t.Helper()
	empty, err := ParseAttestationStatusList([]byte(`{"entries":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewAndroidKeyAttestationVerifier(AndroidKeyAttestationOptions{
		AllowSoftware: allowSoftware, Now: func() time.Time { return testNow }, roots: []*x509.Certificate{f.root},
		Status: func(context.Context) (*AttestationStatusList, error) { return empty, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *attestationFixture) devicePublicKey(t *testing.T) []byte {
	t.Helper()
	key, err := f.leafKey.PublicKey.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	return key.Bytes()
}

func keyDescriptionDER(t *testing.T, spec leafSpec) []byte {
	t.Helper()
	tagged := func(tag int, value any, params string) []byte {
		body, err := asn1.MarshalWithParams(value, params)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tag, IsCompound: true, Bytes: body})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	var hw []byte
	if spec.purposes != nil {
		hw = append(hw, tagged(authorizationTagPurpose, spec.purposes, "set")...)
	}
	if !spec.noOrigin {
		hw = append(hw, tagged(authorizationTagOrigin, spec.origin, "")...)
	}
	if spec.emptyWrapper {
		empty, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, IsCompound: true})
		if err != nil {
			t.Fatal(err)
		}
		hw = append(hw, empty...)
	}
	if spec.dupOrigin != 0 {
		hw = append(hw, tagged(authorizationTagOrigin, spec.dupOrigin, "")...)
	}
	list := func(b []byte) asn1.RawValue {
		return asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: b}
	}
	desc := keyDescription{
		AttestationVersion: 200, AttestationSecurityLevel: asn1.Enumerated(spec.attestLevel),
		KeymasterVersion: 200, KeymasterSecurityLevel: asn1.Enumerated(spec.keymasterLevel),
		AttestationChallenge: []byte(spec.challenge), UniqueID: []byte{},
		SoftwareEnforced: list(nil), HardwareEnforced: list(hw),
	}
	if spec.attestLevel == attestationSecuritySoftware {
		desc.SoftwareEnforced, desc.HardwareEnforced = list(hw), list(nil)
	}
	der, err := asn1.Marshal(desc)
	if err != nil {
		t.Fatal(err)
	}
	if spec.extraField {
		var outer asn1.RawValue
		if _, err := asn1.Unmarshal(der, &outer); err != nil {
			t.Fatal(err)
		}
		outer.Bytes = append(outer.Bytes, 0x05, 0x00)
		outer.FullBytes = nil
		if der, err = asn1.Marshal(outer); err != nil {
			t.Fatal(err)
		}
	}
	if spec.trailing {
		der = append(der, 0x05, 0x00)
	}
	return der
}

func newTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signTestCert(t *testing.T, tmpl, parent *x509.Certificate, pub *ecdsa.PublicKey, signer *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	if parent == nil {
		parent = tmpl
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestAndroidAttestationAcceptsHardwareKeys(t *testing.T) {
	f := newAttestationFixture(t)
	for _, level := range []int{1, 2} {
		spec := defaultLeaf()
		spec.attestLevel, spec.keymasterLevel = level, level
		if err := f.verifier(t, false).VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), f.chain(t, spec)); err != nil {
			t.Fatalf("security level %d refused: %v", level, err)
		}
	}
}

// Android sends CertPath.getEncoded(): a PkiPath SEQUENCE ordered root first.
func TestAndroidAttestationAcceptsPkiPath(t *testing.T) {
	f := newAttestationFixture(t)
	certs, err := x509.ParseCertificates(f.chain(t, defaultLeaf()))
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(certs)
	raws := make([]asn1.RawValue, len(certs))
	for i, cert := range certs {
		raws[i] = asn1.RawValue{FullBytes: cert.Raw}
	}
	pkiPath, err := asn1.Marshal(raws)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.verifier(t, false).VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), pkiPath); err != nil {
		t.Fatalf("PkiPath chain refused: %v", err)
	}
}

// Google keeps trusting factory intermediates past their expiry.
func TestAndroidAttestationAcceptsExpiredFactoryIntermediate(t *testing.T) {
	f := newAttestationFixture(t)
	f.inter = signTestCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test intermediate"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(-30 * time.Minute),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}, f.root, &f.interKey.PublicKey, f.rootKey)
	if err := f.verifier(t, false).VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), f.chain(t, defaultLeaf())); err != nil {
		t.Fatalf("expired factory intermediate refused: %v", err)
	}
}

func TestAndroidAttestationRefusals(t *testing.T) {
	f := newAttestationFixture(t)
	other := newAttestationFixture(t)
	cases := map[string]struct {
		spec   func(*leafSpec)
		chain  func([]byte) []byte
		key    func([]byte) []byte
		now    time.Time
		reason string
	}{
		"software level":   {spec: func(s *leafSpec) { s.attestLevel, s.keymasterLevel = 0, 0 }, reason: "TEE or StrongBox"},
		"software keymint": {spec: func(s *leafSpec) { s.keymasterLevel = 0 }, reason: "TEE or StrongBox"},
		"wrong challenge":  {spec: func(s *leafSpec) { s.challenge = "device-2" }, reason: "challenge"},
		"imported key":     {spec: func(s *leafSpec) { s.origin = 2 }, reason: "not generated"},
		"duplicate origin": {spec: func(s *leafSpec) { s.dupOrigin = 2 }, reason: "appears twice"},
		"unknown level":    {spec: func(s *leafSpec) { s.attestLevel, s.keymasterLevel = 3, 3 }, reason: "unknown"},
		"signing key":      {spec: func(s *leafSpec) { s.purposes = []int{2, 3} }, reason: "key agreement"},
		"no purpose":       {spec: func(s *leafSpec) { s.purposes = nil }, reason: "key agreement"},
		"trailing data":    {spec: func(s *leafSpec) { s.trailing = true }, reason: "trailing"},
		"ninth field":      {spec: func(s *leafSpec) { s.extraField = true }, reason: "9 fields"},
		"empty wrapper":    {spec: func(s *leafSpec) { s.emptyWrapper = true }, reason: "authorization 2 is malformed"},
		"no origin":        {spec: func(s *leafSpec) { s.noOrigin = true }, reason: "not generated"},
		"wrong key":        {key: func([]byte) []byte { return other.devicePublicKey(t) }, reason: "not the device key"},
		"expired leaf":     {now: testNow.Add(2 * time.Hour), reason: "validity"},
		"untrusted root":   {chain: func([]byte) []byte { return other.chain(t, defaultLeaf()) }, reason: "Google attestation root"},
		"leaf only":        {chain: func(c []byte) []byte { certs, _ := x509.ParseCertificates(c); return certs[0].Raw }, reason: "leaf and an issuer"},
		"cbor attestation": {chain: func([]byte) []byte { return []byte{0xa2, 0x01} }, reason: "not an Android Key Attestation"},
		"broken link": {chain: func(c []byte) []byte {
			certs, _ := x509.ParseCertificates(c)
			foreign, _ := x509.ParseCertificates(other.chain(t, defaultLeaf()))
			return append(append(append([]byte{}, foreign[0].Raw...), certs[1].Raw...), certs[2].Raw...)
		}, reason: "does not verify"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			spec := defaultLeaf()
			if tc.spec != nil {
				tc.spec(&spec)
			}
			chain := f.chain(t, spec)
			if tc.chain != nil {
				chain = tc.chain(chain)
			}
			key := f.devicePublicKey(t)
			if tc.key != nil {
				key = tc.key(key)
			}
			v := f.verifier(t, false)
			if !tc.now.IsZero() {
				v.opts.Now = func() time.Time { return tc.now }
			}
			err := v.VerifyDeviceKey(context.Background(), testEnrollmentID, key, chain)
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("err = %v, want refusal mentioning %q", err, tc.reason)
			}
		})
	}
}

func TestAndroidAttestationDevModeAcceptsEmulatorChain(t *testing.T) {
	f := newAttestationFixture(t)
	emulator := newAttestationFixture(t) // its root is not trusted
	spec := defaultLeaf()
	spec.attestLevel, spec.keymasterLevel = 0, 0
	chain := emulator.chain(t, spec)
	if err := f.verifier(t, true).VerifyDeviceKey(context.Background(), testEnrollmentID, emulator.devicePublicKey(t), chain); err != nil {
		t.Fatalf("dev mode refused an emulator chain: %v", err)
	}
	if err := f.verifier(t, false).VerifyDeviceKey(context.Background(), testEnrollmentID, emulator.devicePublicKey(t), chain); err == nil {
		t.Fatal("emulator chain accepted without the dev opt-in")
	}
	// The dev opt-in still binds the challenge and the key.
	if err := f.verifier(t, true).VerifyDeviceKey(context.Background(), "device-2", emulator.devicePublicKey(t), chain); err == nil {
		t.Fatal("dev mode accepted a wrong challenge")
	}
	if err := f.verifier(t, true).VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), chain); err == nil {
		t.Fatal("dev mode accepted a wrong key")
	}
}

func TestEmbeddedGoogleAttestationRoots(t *testing.T) {
	roots, err := parseAttestationRoots(googleAttestationRootsPEM)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"cedb1cb6": false, "6d9db4ce": false, "c1984a3e": false, "1ef1a04b": false, "ab664117": false,
	}
	for _, root := range roots {
		sum := sha256.Sum256(root.Raw)
		prefix := hex.EncodeToString(sum[:4])
		if _, ok := want[prefix]; !ok {
			t.Fatalf("unexpected embedded root %s (%s)", prefix, root.Subject)
		}
		want[prefix] = true
	}
	for prefix, seen := range want {
		if !seen {
			t.Fatalf("embedded root %s missing", prefix)
		}
	}
	if _, err := NewAndroidKeyAttestationVerifier(AndroidKeyAttestationOptions{AllowSoftware: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAndroidAttestationRefusesAttestedIssuer(t *testing.T) {
	// An app attestation key (itself attested) signs a leaf claiming another
	// key and challenge: the issuer's KeyDescription must not be ignored.
	f := newAttestationFixture(t)
	attestKey := newTestKey(t)
	issuer := signTestCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(4), Subject: pkix.Name{CommonName: "attest key"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		ExtraExtensions: []pkix.Extension{{Id: keyDescriptionOID, Value: keyDescriptionDER(t, defaultLeaf())}},
	}, f.inter, &attestKey.PublicKey, f.interKey)
	leaf := signTestCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(5), Subject: pkix.Name{CommonName: "Android Keystore Key"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(time.Hour),
		ExtraExtensions: []pkix.Extension{{Id: keyDescriptionOID, Value: keyDescriptionDER(t, defaultLeaf())}},
	}, issuer, &f.leafKey.PublicKey, attestKey)
	var chain []byte
	for _, cert := range []*x509.Certificate{leaf, issuer, f.inter, f.root} {
		chain = append(chain, cert.Raw...)
	}
	err := f.verifier(t, false).VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), chain)
	if err == nil || !strings.Contains(err.Error(), "attested key, not a hardware issuer") {
		t.Fatalf("err = %v, want attested-issuer refusal", err)
	}
}

func TestAndroidAttestationEnforcesPathLength(t *testing.T) {
	f := newAttestationFixtureWith(t, true)
	err := f.verifier(t, false).VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), f.chain(t, defaultLeaf()))
	if err == nil || !strings.Contains(err.Error(), "does not verify to a Google attestation root") {
		t.Fatalf("err = %v, want path validation refusal", err)
	}
}

func TestAndroidAttestationRevocation(t *testing.T) {
	f := newAttestationFixture(t)
	chain := f.chain(t, defaultLeaf())
	revoked, err := ParseAttestationStatusList([]byte(`{"entries":{"2":{"status":"REVOKED","reason":"KEY_COMPROMISE"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		dev    bool
		status func(context.Context) (*AttestationStatusList, error)
		reason string
	}{
		"revoked intermediate": {status: func(context.Context) (*AttestationStatusList, error) { return revoked, nil }, reason: "revoked"},
		"revoked in dev mode":  {dev: true, status: func(context.Context) (*AttestationStatusList, error) { return revoked, nil }, reason: "revoked"},
		"status unavailable": {status: func(context.Context) (*AttestationStatusList, error) {
			return nil, errors.New("attestation revocation status is unavailable")
		}, reason: "unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			v := f.verifier(t, tc.dev)
			v.opts.Status = tc.status
			err := v.VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), chain)
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("err = %v, want refusal mentioning %q", err, tc.reason)
			}
		})
	}
	// The dev opt-in tolerates an unreachable list, never a listed certificate.
	v := f.verifier(t, true)
	v.opts.Status = func(context.Context) (*AttestationStatusList, error) { return nil, errors.New("offline") }
	if err := v.VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), chain); err != nil {
		t.Fatalf("dev mode refused on an unavailable status list: %v", err)
	}
	if _, err := NewAndroidKeyAttestationVerifier(AndroidKeyAttestationOptions{}); err == nil {
		t.Fatal("production verifier built without a revocation status source")
	}
}

func TestAndroidAttestationRefusesEKURestrictedIntermediate(t *testing.T) {
	f := newAttestationFixture(t)
	f.inter = signTestCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "tls-only intermediate"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, f.root, &f.interKey.PublicKey, f.rootKey)
	err := f.verifier(t, false).VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), f.chain(t, defaultLeaf()))
	if err == nil || !strings.Contains(err.Error(), "extended key usage") {
		t.Fatalf("err = %v, want EKU refusal", err)
	}
}

func TestAndroidAttestationRefusesEmptyEKUExtension(t *testing.T) {
	f := newAttestationFixture(t)
	f.inter = signTestCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "empty-eku intermediate"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		ExtraExtensions: []pkix.Extension{{Id: extKeyUsageOID, Value: []byte{0x30, 0x00}}},
	}, f.root, &f.interKey.PublicKey, f.rootKey)
	err := f.verifier(t, false).VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), f.chain(t, defaultLeaf()))
	if err == nil {
		t.Fatal("accepted an intermediate with an empty EKU extension")
	}
}

func TestAndroidAttestationDevModeParsesHardwareList(t *testing.T) {
	f := newAttestationFixture(t)
	spec := defaultLeaf()
	spec.attestLevel, spec.keymasterLevel, spec.emptyWrapper = 0, 0, true
	err := f.verifier(t, true).VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), f.chain(t, spec))
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("err = %v, want malformed hardware list refused in dev mode", err)
	}
}

func TestAndroidAttestationDevModeKeepsStaleRevocations(t *testing.T) {
	f := newAttestationFixture(t)
	revoked, err := ParseAttestationStatusList([]byte(`{"entries":{"2":{"status":"REVOKED"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	v := f.verifier(t, true)
	v.opts.Status = func(context.Context) (*AttestationStatusList, error) {
		return revoked, errors.New("attestation revocation status is unavailable: expired")
	}
	err = v.VerifyDeviceKey(context.Background(), testEnrollmentID, f.devicePublicKey(t), f.chain(t, defaultLeaf()))
	if err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("err = %v, want stale revocation honoured", err)
	}
}
