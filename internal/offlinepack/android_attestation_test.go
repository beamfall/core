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
	"math/big"
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
	f := &attestationFixture{rootKey: newTestKey(t), interKey: newTestKey(t), leafKey: newTestKey(t)}
	f.root = signTestCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
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
	notAfter       time.Time
}

func defaultLeaf() leafSpec {
	return leafSpec{challenge: testEnrollmentID, attestLevel: 1, keymasterLevel: 1, notAfter: testNow.Add(time.Hour)}
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
	v, err := NewAndroidKeyAttestationVerifier(AndroidKeyAttestationOptions{
		AllowSoftware: allowSoftware, Now: func() time.Time { return testNow }, roots: []*x509.Certificate{f.root},
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
	var hw []byte
	if !spec.noOrigin {
		value, err := asn1.Marshal(spec.origin)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: authorizationTagOrigin, IsCompound: true, Bytes: value})
		if err != nil {
			t.Fatal(err)
		}
		hw = entry
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
	if _, err := NewAndroidKeyAttestationVerifier(AndroidKeyAttestationOptions{}); err != nil {
		t.Fatal(err)
	}
}
