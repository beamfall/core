package wrapvectors

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const (
	bfkwSize       = 153
	bfopHeaderSize = 64
	bfopChunkSize  = 1 << 20
)

var (
	bfkwPrefix = [8]byte{'B', 'F', 'K', 'W', 1, 1, 0, 0}
	bfopPrefix = [8]byte{'B', 'F', 'O', 'P', 2, 1, 0, 0}
)

type aadFields struct {
	issuer, packID, profileID, enrollmentID, deviceKID, manifestDigest, revision string
}

func scalarKey(value byte) (*ecdh.PrivateKey, error) {
	raw := make([]byte, 32)
	raw[31] = value
	return ecdh.P256().NewPrivateKey(raw)
}

func keyHex(key *ecdh.PrivateKey) string {
	return hex.EncodeToString(key.Bytes())
}

func bytesHex(value []byte) string {
	return hex.EncodeToString(value)
}

func decodeHex(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode hex: %w", err)
	}
	return decoded, nil
}

func digestHex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func digestText(value []byte) string {
	digest := sha256.Sum256(value)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func deviceKID(public []byte) (string, error) {
	if _, err := parsePublic(public); err != nil {
		return "", err
	}
	preimage := append([]byte("BFOP-devkey-v1\x00"), public...)
	return digestText(preimage), nil
}

func parsePublic(encoded []byte) (*ecdh.PublicKey, error) {
	if len(encoded) != 65 || encoded[0] != 4 {
		return nil, fmt.Errorf("public key is not canonical uncompressed P-256")
	}
	key, err := ecdh.P256().NewPublicKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid P-256 public key")
	}
	return key, nil
}

func buildAAD(fields aadFields) ([]byte, error) {
	values := []string{
		fields.issuer,
		fields.packID,
		fields.profileID,
		fields.enrollmentID,
		fields.deviceKID,
		fields.manifestDigest,
		fields.revision,
	}
	out := append([]byte("BFOP-aad-v1\x00"), bfkwPrefix[:]...)
	var size [4]byte
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf("invalid AAD field length")
		}
		if len(value) > 4096 {
			return nil, fmt.Errorf("invalid AAD field length")
		}
		var valueLen uint32
		for i := range len(value) {
			if value[i] > 0x7f {
				return nil, fmt.Errorf("non-ASCII AAD field")
			}
			valueLen++
		}
		binary.BigEndian.PutUint32(size[:], valueLen)
		out = append(out, size[:]...)
		out = append(out, value...)
	}
	return out, nil
}

func parseAAD(aad []byte) ([7]string, error) {
	var fields [7]string
	if len(aad) < 48 || !bytes.Equal(aad[:12], []byte("BFOP-aad-v1\x00")) ||
		!bytes.Equal(aad[12:20], bfkwPrefix[:]) {
		return fields, fmt.Errorf("invalid AAD prefix")
	}
	offset := 20
	for i := range fields {
		if offset+4 > len(aad) {
			return fields, fmt.Errorf("truncated AAD")
		}
		size := int(binary.BigEndian.Uint32(aad[offset : offset+4]))
		offset += 4
		if size == 0 || size > 4096 || offset+size > len(aad) {
			return fields, fmt.Errorf("invalid AAD field")
		}
		fields[i] = string(aad[offset : offset+size])
		offset += size
	}
	if offset != len(aad) {
		return fields, fmt.Errorf("trailing AAD bytes")
	}
	return fields, nil
}

func deriveWrap(shared, ephemeralPublic, devicePublic, aad []byte) ([]byte, []byte, error) {
	var salt [sha256.Size]byte
	prk, err := hkdf.Extract(sha256.New, shared, salt[:])
	if err != nil {
		return nil, nil, err
	}
	info := append([]byte("BFOP-P256-HKDF-SHA256-A256GCM-v1/kek\x00"), ephemeralPublic...)
	info = append(info, devicePublic...)
	info = append(info, aad...)
	okm, err := hkdf.Expand(sha256.New, prk, string(info), 44)
	if err != nil {
		return nil, nil, err
	}
	return okm[:32], okm[32:], nil
}

func wrap(devicePublic, aad, cek []byte, ephemeral *ecdh.PrivateKey) ([]byte, error) {
	device, err := parsePublic(devicePublic)
	if err != nil {
		return nil, err
	}
	fields, err := parseAAD(aad)
	if err != nil {
		return nil, err
	}
	kid, err := deviceKID(devicePublic)
	if err != nil || fields[4] != kid {
		return nil, fmt.Errorf("device kid mismatch")
	}
	shared, err := ephemeral.ECDH(device)
	if err != nil {
		return nil, err
	}
	kek, nonce, err := deriveWrap(shared, ephemeral.PublicKey().Bytes(), devicePublic, aad)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, cek, aad)
	out := make([]byte, bfkwSize)
	copy(out, bfkwPrefix[:])
	kidRaw, _ := base64.RawURLEncoding.DecodeString(kid)
	copy(out[8:40], kidRaw)
	copy(out[40:105], ephemeral.PublicKey().Bytes())
	copy(out[105:], sealed)
	return out, nil
}

func unwrap(device *ecdh.PrivateKey, envelope, aad []byte, expectedKID, expectedDigest string) ([]byte, error) {
	if len(envelope) != bfkwSize || !bytes.Equal(envelope[:8], bfkwPrefix[:]) {
		return nil, fmt.Errorf("invalid BFKW envelope")
	}
	if digestText(envelope) != expectedDigest {
		return nil, fmt.Errorf("envelope digest mismatch")
	}
	fields, err := parseAAD(aad)
	if err != nil {
		return nil, err
	}
	kid, err := deviceKID(device.PublicKey().Bytes())
	if err != nil || kid != expectedKID || fields[4] != expectedKID {
		return nil, fmt.Errorf("device kid mismatch")
	}
	kidRaw, err := base64.RawURLEncoding.DecodeString(expectedKID)
	if err != nil || !bytes.Equal(envelope[8:40], kidRaw) {
		return nil, fmt.Errorf("envelope kid mismatch")
	}
	ephemeral, err := parsePublic(envelope[40:105])
	if err != nil {
		return nil, err
	}
	shared, err := device.ECDH(ephemeral)
	if err != nil {
		return nil, err
	}
	kek, nonce, err := deriveWrap(shared, envelope[40:105], device.PublicKey().Bytes(), aad)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, envelope[105:], aad)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func deriveCIK(cek, manifest []byte) ([]byte, error) {
	prk, err := hkdf.Extract(sha256.New, cek, manifest)
	if err != nil {
		return nil, err
	}
	return hkdf.Expand(sha256.New, prk, "BFOP-cik-v1", sha256.Size)
}

func bfopHeader(manifest, itemDigest []byte, itemIndex uint32, plaintextLen uint64) []byte {
	header := make([]byte, bfopHeaderSize)
	copy(header[:8], bfopPrefix[:])
	binary.BigEndian.PutUint32(header[8:12], itemIndex)
	binary.BigEndian.PutUint32(header[12:16], bfopChunkSize)
	binary.BigEndian.PutUint64(header[16:24], plaintextLen)
	copy(header[24:32], itemDigest[:8])
	copy(header[32:64], manifest)
	return header
}

func chunkInputs(header []byte, itemIndex uint32, chunkIndex uint64, final bool) ([]byte, []byte) {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint32(nonce[:4], itemIndex)
	binary.BigEndian.PutUint64(nonce[4:], chunkIndex)
	aad := make([]byte, 73)
	copy(aad, header)
	binary.BigEndian.PutUint64(aad[64:72], chunkIndex)
	if final {
		aad[72] = 1
	}
	return nonce, aad
}

func bfopSize(plaintextLen uint64) uint64 {
	count := uint64(1)
	if plaintextLen > 0 {
		count = (plaintextLen + bfopChunkSize - 1) / bfopChunkSize
	}
	return bfopHeaderSize + plaintextLen + 16*count
}

func chunkRange(plaintextLen, chunkIndex uint64) (uint64, uint64) {
	count := uint64(1)
	if plaintextLen > 0 {
		count = (plaintextLen + bfopChunkSize - 1) / bfopChunkSize
	}
	size := uint64(bfopChunkSize)
	if plaintextLen == 0 {
		size = 0
	} else if chunkIndex == count-1 && plaintextLen%bfopChunkSize != 0 {
		size = plaintextLen % bfopChunkSize
	}
	return bfopHeaderSize + chunkIndex*(bfopChunkSize+16), size + 16
}

func encryptItem(cek, manifest []byte, itemIndex uint32, plaintext []byte) ([]byte, error) {
	itemDigest := sha256.Sum256(plaintext)
	header := bfopHeader(manifest, itemDigest[:], itemIndex, uint64(len(plaintext)))
	cik, err := deriveCIK(cek, manifest)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(cik)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), header...)
	count := uint64(1)
	if len(plaintext) > 0 {
		count = (uint64(len(plaintext)) + bfopChunkSize - 1) / bfopChunkSize
	}
	for index := uint64(0); index < count; index++ {
		start := index * bfopChunkSize
		end := min(start+bfopChunkSize, uint64(len(plaintext)))
		nonce, aad := chunkInputs(header, itemIndex, index, index == count-1)
		out = gcm.Seal(out, nonce, plaintext[start:end], aad)
	}
	return out, nil
}

func decryptItem(cek, manifest []byte, itemIndex uint32, plaintextLen uint64, itemDigest, file []byte) ([]byte, error) {
	if uint64(len(file)) != bfopSize(plaintextLen) || len(file) < bfopHeaderSize {
		return nil, fmt.Errorf("BFOP file length mismatch")
	}
	wantHeader := bfopHeader(manifest, itemDigest, itemIndex, plaintextLen)
	if !bytes.Equal(file[:bfopHeaderSize], wantHeader) {
		return nil, fmt.Errorf("BFOP header mismatch")
	}
	cik, err := deriveCIK(cek, manifest)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(cik)
	if err != nil {
		return nil, err
	}
	count := uint64(1)
	if plaintextLen > 0 {
		count = (plaintextLen + bfopChunkSize - 1) / bfopChunkSize
	}
	out := make([]byte, 0, plaintextLen)
	for index := uint64(0); index < count; index++ {
		offset, size := chunkRange(plaintextLen, index)
		nonce, aad := chunkInputs(file[:bfopHeaderSize], itemIndex, index, index == count-1)
		out, err = gcm.Open(out, nonce, file[offset:offset+size], aad)
		if err != nil {
			return nil, fmt.Errorf("BFOP chunk authentication failed")
		}
	}
	got := sha256.Sum256(out)
	if !bytes.Equal(got[:], itemDigest) {
		return nil, fmt.Errorf("item digest mismatch")
	}
	return out, nil
}

func bytePattern(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}
