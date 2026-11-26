package offlinepack

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"math/big"
	"testing"
	"time"
)

func TestAppleAppAttestVerifierAcceptsProdAndDev(t *testing.T) {
	now := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)
	root, inter, rootKey, interKey := newTestAppleChain(t, now)
	deviceKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name     string
		aaguid   [16]byte
		allowDev bool
		wantErr  bool
	}{
		{"prod", appleProdAAGUID, false, false},
		{"dev_allowed", appleDevAAGUID, true, false},
		{"dev_rejected", appleDevAAGUID, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier, err := NewAppleAppAttestVerifier(AppleAppAttestOptions{
				AllowDevelopment: tc.allowDev,
				AllowedAppIDs:    []string{"T3Y5YFTYRN.com.beamfall.mobile"},
				Now:              func() time.Time { return now },
				roots:            []*x509.Certificate{root},
			})
			if err != nil {
				t.Fatal(err)
			}
			cbor := buildTestAppleAttestation(t, now, inter, interKey, tc.aaguid, "T3Y5YFTYRN.com.beamfall.mobile", "device-1", deviceKey.PublicKey().Bytes(), 0, "apple-appattest", false)
			err = verifier.VerifyDeviceKey(context.Background(), "device-1", deviceKey.PublicKey().Bytes(), cbor)
			if (err != nil) != tc.wantErr {
				t.Fatalf("VerifyDeviceKey error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
	_ = rootKey
}

func TestAppleAppAttestVerifierRejectsTampering(t *testing.T) {
	now := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)
	root, inter, _, interKey := newTestAppleChain(t, now)
	deviceKey, _ := ecdh.P256().GenerateKey(rand.Reader)
	otherKey, _ := ecdh.P256().GenerateKey(rand.Reader)

	verifier, err := NewAppleAppAttestVerifier(AppleAppAttestOptions{
		AllowDevelopment: true,
		AllowedAppIDs:    []string{"T3Y5YFTYRN.com.beamfall.mobile"},
		Now:              func() time.Time { return now },
		roots:            []*x509.Certificate{root},
	})
	if err != nil {
		t.Fatal(err)
	}

	valid := buildTestAppleAttestation(t, now, inter, interKey, appleDevAAGUID, "T3Y5YFTYRN.com.beamfall.mobile", "device-1", deviceKey.PublicKey().Bytes(), 0, "apple-appattest", false)
	if err := verifier.VerifyDeviceKey(context.Background(), "device-2", deviceKey.PublicKey().Bytes(), valid); err == nil {
		t.Fatal("expected enrollment mismatch error")
	}
	if err := verifier.VerifyDeviceKey(context.Background(), "device-1", otherKey.PublicKey().Bytes(), valid); err == nil {
		t.Fatal("expected publicKey mismatch error")
	}
	badCount := buildTestAppleAttestation(t, now, inter, interKey, appleDevAAGUID, "T3Y5YFTYRN.com.beamfall.mobile", "device-1", deviceKey.PublicKey().Bytes(), 1, "apple-appattest", false)
	if err := verifier.VerifyDeviceKey(context.Background(), "device-1", deviceKey.PublicKey().Bytes(), badCount); err == nil {
		t.Fatal("expected non-zero signCount error")
	}
	badFmt := buildTestAppleAttestation(t, now, inter, interKey, appleDevAAGUID, "T3Y5YFTYRN.com.beamfall.mobile", "device-1", deviceKey.PublicKey().Bytes(), 0, "packed", false)
	if err := verifier.VerifyDeviceKey(context.Background(), "device-1", deviceKey.PublicKey().Bytes(), badFmt); err == nil {
		t.Fatal("expected bad fmt error")
	}
	badNonce := buildTestAppleAttestation(t, now, inter, interKey, appleDevAAGUID, "T3Y5YFTYRN.com.beamfall.mobile", "device-1", deviceKey.PublicKey().Bytes(), 0, "apple-appattest", true)
	if err := verifier.VerifyDeviceKey(context.Background(), "device-1", deviceKey.PublicKey().Bytes(), badNonce); err == nil {
		t.Fatal("expected bad nonce error")
	}
	badRP := buildTestAppleAttestation(t, now, inter, interKey, appleDevAAGUID, "OTHER.com.evil.app", "device-1", deviceKey.PublicKey().Bytes(), 0, "apple-appattest", false)
	if err := verifier.VerifyDeviceKey(context.Background(), "device-1", deviceKey.PublicKey().Bytes(), badRP); err == nil {
		t.Fatal("expected rpIdHash mismatch error")
	}
}

func TestMultiPlatformAttestationVerifierRoutesBoth(t *testing.T) {
	now := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)
	root, inter, _, interKey := newTestAppleChain(t, now)
	apple, err := NewAppleAppAttestVerifier(AppleAppAttestOptions{
		AllowDevelopment: true,
		Now:              func() time.Time { return now },
		roots:            []*x509.Certificate{root},
	})
	if err != nil {
		t.Fatal(err)
	}
	android, err := NewAndroidKeyAttestationVerifier(AndroidKeyAttestationOptions{AllowSoftware: true})
	if err != nil {
		t.Fatal(err)
	}
	multi := NewMultiPlatformAttestationVerifier(android, apple)
	deviceKey, _ := ecdh.P256().GenerateKey(rand.Reader)
	cbor := buildTestAppleAttestation(t, now, inter, interKey, appleDevAAGUID, "T3Y5YFTYRN.com.beamfall.mobile", "device-1", deviceKey.PublicKey().Bytes(), 0, "apple-appattest", false)
	if err := multi.VerifyDeviceKey(context.Background(), "device-1", deviceKey.PublicKey().Bytes(), cbor); err != nil {
		t.Fatalf("multi apple route failed: %v", err)
	}
	if err := multi.VerifyDeviceKey(context.Background(), "device-1", deviceKey.PublicKey().Bytes(), []byte{0x01, 0x02}); err == nil {
		t.Fatal("expected unrecognized format error")
	}
}

func newTestAppleChain(t *testing.T, now time.Time) (*x509.Certificate, *x509.Certificate, *ecdsa.PrivateKey, *ecdsa.PrivateKey) {
	t.Helper()
	rootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Apple Root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	rootCert, _ := x509.ParseCertificate(rootDER)

	interKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	interTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Test Apple Intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTmpl, rootCert, &interKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	interCert, _ := x509.ParseCertificate(interDER)
	return rootCert, interCert, rootKey, interKey
}

func buildTestAppleAttestation(
	t *testing.T,
	now time.Time,
	inter *x509.Certificate,
	interKey *ecdsa.PrivateKey,
	aaguid [16]byte,
	appID, enrollmentID string,
	devicePublicKey []byte,
	signCount uint32,
	fmtName string,
	corruptNonce bool,
) []byte {
	t.Helper()
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafECDH, _ := leafKey.ECDH()
	credID := sha256.Sum256(leafECDH.PublicKey().Bytes())
	rpHash := sha256.Sum256([]byte(appID))

	authData := make([]byte, 87)
	copy(authData[0:32], rpHash[:])
	authData[32] = 0x41 // UP + AT
	binary.BigEndian.PutUint32(authData[33:37], signCount)
	copy(authData[37:53], aaguid[:])
	binary.BigEndian.PutUint16(authData[53:55], 32)
	copy(authData[55:87], credID[:])

	clientDataHash := appleClientDataHash(enrollmentID, devicePublicKey)
	nonceInput := append(append([]byte{}, authData...), clientDataHash[:]...)
	nonce := sha256.Sum256(nonceInput)
	if corruptNonce {
		nonce[0] ^= 0xff
	}

	nonceOctets, _ := asn1.Marshal(nonce[:])
	tagged := asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: nonceOctets}
	taggedBytes, _ := asn1.Marshal(tagged)
	extSeq := asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: taggedBytes}
	extValue, _ := asn1.Marshal(extSeq)

	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "Test Leaf"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtraExtensions: []pkix.Extension{
			{Id: appleNonceExtensionOID, Value: extValue},
		},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, inter, &leafKey.PublicKey, interKey)
	if err != nil {
		t.Fatal(err)
	}

	return encodeTestCBORMap(map[string]any{
		"fmt":      fmtName,
		"authData": authData,
		"attStmt": map[string]any{
			"x5c": [][]byte{leafDER, inter.Raw},
		},
	})
}

func encodeTestCBORMap(m map[string]any) []byte {
	var out []byte
	out = append(out, 0xa0|byte(len(m)))
	for k, v := range m {
		out = append(out, encodeTestCBORText(k)...)
		switch val := v.(type) {
		case string:
			out = append(out, encodeTestCBORText(val)...)
		case []byte:
			out = append(out, encodeTestCBORBytes(val)...)
		case [][]byte:
			out = append(out, 0x80|byte(len(val)))
			for _, elem := range val {
				out = append(out, encodeTestCBORBytes(elem)...)
			}
		case map[string]any:
			out = append(out, encodeTestCBORMap(val)...)
		}
	}
	return out
}

func encodeTestCBORText(s string) []byte {
	return append(encodeTestCBORHeader(3, len(s)), s...)
}

func encodeTestCBORBytes(b []byte) []byte {
	return append(encodeTestCBORHeader(2, len(b)), b...)
}

func encodeTestCBORHeader(major byte, n int) []byte {
	m := major << 5
	switch {
	case n < 24:
		return []byte{m | byte(n)}
	case n <= 0xff:
		return []byte{m | 24, byte(n)}
	default:
		return []byte{m | 25, byte(n >> 8), byte(n)}
	}
}
