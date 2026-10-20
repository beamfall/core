package offlinepack

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	BFOPSuite               = "BFOP-P256-HKDF-SHA256-A256GCM-v1"
	BFKWEnvelopeSize        = 153
	BFKWEncodedSize         = 204
	BFOPHeaderSize          = 64
	BFOPChunkSize    uint64 = 1 << 20
	BFOPMaxPackItems        = 4096
	BFOPMaxItemSize  uint64 = 4 << 30

	bfopTagSize = 16
)

const (
	WrapSuite           = BFOPSuite
	WrappedEnvelopeSize = BFKWEnvelopeSize
	MaxManifestItems    = BFOPMaxPackItems
	MaxItemSize         = BFOPMaxItemSize
)

var (
	errInvalidOfflinePackBytes = errors.New("invalid offline pack bytes")
	bfkwPrefix                 = [8]byte{'B', 'F', 'K', 'W', 1, 1, 0, 0}
	bfopPrefix                 = [8]byte{'B', 'F', 'O', 'P', 2, 1, 0, 0}
	strictBase64URL            = base64.RawURLEncoding.Strict()
)

// WrapAADFields are the seven AAD_wrap claims, in protocol order.
type WrapAADFields struct {
	Issuer              string
	PackID              string
	ProfileID           string
	DeviceEnrollmentID  string
	DeviceKeyThumbprint string
	ManifestSHA256      string
	ManifestRevision    string
}

// BFKWEnvelope is a parsed BFKW v1 envelope.
type BFKWEnvelope struct {
	DeviceKID          [sha256.Size]byte
	EphemeralPublicKey [65]byte
	WrappedCEK         [48]byte
}

// DeviceKID identifies a canonical uncompressed P-256 public key.
func DeviceKID(devicePublicKey []byte) (string, error) {
	kid, err := deviceKIDBytes(devicePublicKey)
	if err != nil {
		return "", err
	}
	return strictBase64URL.EncodeToString(kid[:]), nil
}

func deviceKIDBytes(devicePublicKey []byte) ([sha256.Size]byte, error) {
	if _, err := parseP256PublicKey(devicePublicKey); err != nil {
		return [sha256.Size]byte{}, err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("BFOP-devkey-v1\x00"))
	_, _ = h.Write(devicePublicKey)
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func parseP256PublicKey(encoded []byte) (*ecdh.PublicKey, error) {
	if len(encoded) != 65 || encoded[0] != 4 {
		return nil, fmt.Errorf("%w: P-256 public key is not canonical SEC1 uncompressed", errInvalidOfflinePackBytes)
	}
	key, err := ecdh.P256().NewPublicKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid P-256 public key", errInvalidOfflinePackBytes)
	}
	return key, nil
}

// BuildWrapAAD encodes the D6 transcript.
func BuildWrapAAD(fields WrapAADFields) ([]byte, error) {
	values := []string{
		fields.Issuer,
		fields.PackID,
		fields.ProfileID,
		fields.DeviceEnrollmentID,
		fields.DeviceKeyThumbprint,
		fields.ManifestSHA256,
		fields.ManifestRevision,
	}
	for i, value := range values {
		if err := validateAADField(value); err != nil {
			return nil, fmt.Errorf("AAD field %d: %w", i, err)
		}
	}
	if err := validateDigestText(fields.DeviceKeyThumbprint); err != nil {
		return nil, fmt.Errorf("device_key_thumbprint: %w", err)
	}
	if err := validateDigestText(fields.ManifestSHA256); err != nil {
		return nil, fmt.Errorf("manifest_sha256: %w", err)
	}
	if !canonicalDecimal(fields.ManifestRevision) {
		return nil, fmt.Errorf("%w: manifest_revision is not canonical decimal", errInvalidOfflinePackBytes)
	}

	out := make([]byte, 0, 20+7*4+sumStringLengths(values))
	out = append(out, "BFOP-aad-v1\x00"...)
	out = append(out, bfkwPrefix[:]...)
	var size [4]byte
	for _, value := range values {
		binary.BigEndian.PutUint32(size[:], uint32(len(value))) //nolint:gosec // validateAADField caps each value at 4096 bytes
		out = append(out, size[:]...)
		out = append(out, value...)
	}
	return out, nil
}

func validateAADField(value string) error {
	if len(value) == 0 || len(value) > 4096 {
		return fmt.Errorf("%w: AAD field length outside 1..4096", errInvalidOfflinePackBytes)
	}
	for i := range len(value) {
		if value[i] > 0x7f {
			return fmt.Errorf("%w: AAD field is not ASCII", errInvalidOfflinePackBytes)
		}
	}
	return nil
}

func validateDigestText(value string) error {
	if len(value) != 43 {
		return fmt.Errorf("%w: digest text must be 43 characters", errInvalidOfflinePackBytes)
	}
	raw, err := strictBase64URL.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return fmt.Errorf("%w: digest text is not canonical unpadded base64url", errInvalidOfflinePackBytes)
	}
	return nil
}

func canonicalDecimal(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func sumStringLengths(values []string) int {
	n := 0
	for _, value := range values {
		n += len(value)
	}
	return n
}

// WrapCEK wraps a CEK with a fresh one-use P-256 key.
func WrapCEK(devicePublicKey, aad, cek []byte) ([]byte, error) {
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate BFKW ephemeral key: %w", err)
	}
	return wrapCEKWithEphemeral(devicePublicKey, aad, cek, ephemeral)
}

func wrapCEKWithEphemeral(devicePublicKey, aad, cek []byte, ephemeral *ecdh.PrivateKey) ([]byte, error) {
	if len(cek) != sha256.Size {
		return nil, fmt.Errorf("%w: CEK must be 32 bytes", errInvalidOfflinePackBytes)
	}
	deviceKey, err := parseP256PublicKey(devicePublicKey)
	if err != nil {
		return nil, err
	}
	kid, err := deviceKIDBytes(devicePublicKey)
	if err != nil {
		return nil, err
	}
	aadFields, err := parseWrapAAD(aad)
	if err != nil {
		return nil, err
	}
	claimKID, err := decodeDigestText(aadFields[4])
	if err != nil || !bytes.Equal(kid[:], claimKID) {
		return nil, fmt.Errorf("%w: device_kid does not match device_key_thumbprint", errInvalidOfflinePackBytes)
	}
	if ephemeral == nil {
		return nil, fmt.Errorf("%w: missing ephemeral key", errInvalidOfflinePackBytes)
	}
	ephemeralPublic := ephemeral.PublicKey().Bytes()
	if _, err := parseP256PublicKey(ephemeralPublic); err != nil {
		return nil, err
	}
	shared, err := ephemeral.ECDH(deviceKey)
	if err != nil || allZero(shared) {
		return nil, fmt.Errorf("%w: invalid ECDH shared secret", errInvalidOfflinePackBytes)
	}
	kek, nonce, err := deriveWrapMaterial(shared, ephemeralPublic, devicePublicKey, aad)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	wrapped := gcm.Seal(nil, nonce, cek, aad)
	if len(wrapped) != 48 {
		return nil, fmt.Errorf("%w: invalid wrapped CEK length", errInvalidOfflinePackBytes)
	}

	out := make([]byte, BFKWEnvelopeSize)
	copy(out, bfkwPrefix[:])
	copy(out[8:40], kid[:])
	copy(out[40:105], ephemeralPublic)
	copy(out[105:], wrapped)
	return out, nil
}

// UnwrapCEK validates every BFKW binding before returning the CEK.
func UnwrapCEK(devicePrivateKey *ecdh.PrivateKey, envelope, aad []byte, expectedDeviceKID, expectedEnvelopeDigest string) ([]byte, error) {
	parsed, err := ParseBFKW(envelope)
	if err != nil {
		return nil, err
	}
	if err := ValidateBFKWDigest(envelope, expectedEnvelopeDigest); err != nil {
		return nil, err
	}
	if devicePrivateKey == nil {
		return nil, fmt.Errorf("%w: missing device private key", errInvalidOfflinePackBytes)
	}
	devicePublic := devicePrivateKey.PublicKey().Bytes()
	deviceKID, err := deviceKIDBytes(devicePublic)
	if err != nil {
		return nil, err
	}
	aadFields, err := parseWrapAAD(aad)
	if err != nil {
		return nil, err
	}
	claimKID, err := decodeDigestText(expectedDeviceKID)
	if err != nil ||
		aadFields[4] != expectedDeviceKID ||
		!bytes.Equal(parsed.DeviceKID[:], claimKID) ||
		!bytes.Equal(parsed.DeviceKID[:], deviceKID[:]) {
		return nil, fmt.Errorf("%w: device_kid mismatch", errInvalidOfflinePackBytes)
	}
	ephemeral, err := parseP256PublicKey(parsed.EphemeralPublicKey[:])
	if err != nil {
		return nil, err
	}
	shared, err := devicePrivateKey.ECDH(ephemeral)
	if err != nil || allZero(shared) {
		return nil, fmt.Errorf("%w: invalid ECDH shared secret", errInvalidOfflinePackBytes)
	}
	kek, nonce, err := deriveWrapMaterial(shared, parsed.EphemeralPublicKey[:], devicePublic, aad)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	cek, err := gcm.Open(nil, nonce, parsed.WrappedCEK[:], aad)
	if err != nil || len(cek) != sha256.Size {
		return nil, fmt.Errorf("%w: BFKW authentication failed", errInvalidOfflinePackBytes)
	}
	return cek, nil
}

func parseWrapAAD(aad []byte) ([7]string, error) {
	var fields [7]string
	if len(aad) < 48 || !bytes.Equal(aad[:12], []byte("BFOP-aad-v1\x00")) || !bytes.Equal(aad[12:20], bfkwPrefix[:]) {
		return fields, fmt.Errorf("%w: invalid AAD_wrap prefix or length", errInvalidOfflinePackBytes)
	}
	offset := 20
	for i := range fields {
		if offset+4 > len(aad) {
			return fields, fmt.Errorf("%w: truncated AAD_wrap", errInvalidOfflinePackBytes)
		}
		length := int(binary.BigEndian.Uint32(aad[offset : offset+4]))
		offset += 4
		if length == 0 || length > 4096 || offset+length > len(aad) {
			return fields, fmt.Errorf("%w: invalid AAD_wrap field length", errInvalidOfflinePackBytes)
		}
		fields[i] = string(aad[offset : offset+length])
		if err := validateAADField(fields[i]); err != nil {
			return fields, err
		}
		offset += length
	}
	if offset != len(aad) {
		return fields, fmt.Errorf("%w: trailing AAD_wrap bytes", errInvalidOfflinePackBytes)
	}
	if err := validateDigestText(fields[4]); err != nil {
		return fields, err
	}
	if err := validateDigestText(fields[5]); err != nil {
		return fields, err
	}
	if !canonicalDecimal(fields[6]) {
		return fields, fmt.Errorf("%w: manifest_revision is not canonical decimal", errInvalidOfflinePackBytes)
	}
	return fields, nil
}

func deriveWrapMaterial(shared, ephemeralPublic, devicePublic, aad []byte) ([]byte, []byte, error) {
	if len(shared) != sha256.Size || len(ephemeralPublic) != 65 || len(devicePublic) != 65 {
		return nil, nil, fmt.Errorf("%w: invalid BFKW derivation input", errInvalidOfflinePackBytes)
	}
	var salt [sha256.Size]byte
	prk, err := hkdf.Extract(sha256.New, shared, salt[:])
	if err != nil {
		return nil, nil, fmt.Errorf("BFKW HKDF extract: %w", err)
	}
	info := make([]byte, 0, len(BFOPSuite)+5+1+65+65+len(aad))
	info = append(info, BFOPSuite...)
	info = append(info, "/kek\x00"...)
	info = append(info, ephemeralPublic...)
	info = append(info, devicePublic...)
	info = append(info, aad...)
	okm, err := hkdf.Expand(sha256.New, prk, string(info), 44)
	if err != nil {
		return nil, nil, fmt.Errorf("BFKW HKDF expand: %w", err)
	}
	return okm[:32], okm[32:], nil
}

func allZero(value []byte) bool {
	var combined byte
	for _, b := range value {
		combined |= b
	}
	return combined == 0
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid AES key", errInvalidOfflinePackBytes)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return gcm, nil
}

// ParseBFKW strictly parses BFKW v1.
func ParseBFKW(envelope []byte) (BFKWEnvelope, error) {
	if len(envelope) != BFKWEnvelopeSize {
		return BFKWEnvelope{}, fmt.Errorf("%w: BFKW length must be %d", errInvalidOfflinePackBytes, BFKWEnvelopeSize)
	}
	if !bytes.Equal(envelope[:4], bfkwPrefix[:4]) ||
		envelope[4] != bfkwPrefix[4] ||
		envelope[5] != bfkwPrefix[5] ||
		envelope[6] != 0 ||
		envelope[7] != 0 {
		return BFKWEnvelope{}, fmt.Errorf("%w: invalid BFKW prefix", errInvalidOfflinePackBytes)
	}
	if _, err := parseP256PublicKey(envelope[40:105]); err != nil {
		return BFKWEnvelope{}, err
	}
	var parsed BFKWEnvelope
	copy(parsed.DeviceKID[:], envelope[8:40])
	copy(parsed.EphemeralPublicKey[:], envelope[40:105])
	copy(parsed.WrappedCEK[:], envelope[105:])
	return parsed, nil
}

// DecodeWrappedEnvelope decodes canonical unpadded base64url BFKW bytes.
func DecodeWrappedEnvelope(encoded string) ([]byte, error) {
	if len(encoded) != BFKWEncodedSize {
		return nil, fmt.Errorf("%w: encoded BFKW length must be %d", errInvalidOfflinePackBytes, BFKWEncodedSize)
	}
	envelope, err := strictBase64URL.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid BFKW base64url", errInvalidOfflinePackBytes)
	}
	if _, err := ParseBFKW(envelope); err != nil {
		return nil, err
	}
	return envelope, nil
}

// WrappedEnvelopeSHA256 returns the manifest's envelope digest.
func WrappedEnvelopeSHA256(envelope []byte) (string, error) {
	if _, err := ParseBFKW(envelope); err != nil {
		return "", err
	}
	digest := sha256.Sum256(envelope)
	return strictBase64URL.EncodeToString(digest[:]), nil
}

// ValidateBFKWDigest checks the manifest envelope digest.
func ValidateBFKWDigest(envelope []byte, expected string) error {
	if _, err := ParseBFKW(envelope); err != nil {
		return err
	}
	want, err := decodeDigestText(expected)
	if err != nil {
		return err
	}
	got := sha256.Sum256(envelope)
	if !bytes.Equal(got[:], want) {
		return fmt.Errorf("%w: BFKW digest mismatch", errInvalidOfflinePackBytes)
	}
	return nil
}

func decodeDigestText(value string) ([]byte, error) {
	if err := validateDigestText(value); err != nil {
		return nil, err
	}
	raw, err := strictBase64URL.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid digest", errInvalidOfflinePackBytes)
	}
	return raw, nil
}
