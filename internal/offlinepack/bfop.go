package offlinepack

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
)

// DeriveCIK derives the per-manifest content-item key.
func DeriveCIK(cek, manifestSHA256 []byte) ([sha256.Size]byte, error) {
	if len(cek) != sha256.Size || len(manifestSHA256) != sha256.Size {
		return [sha256.Size]byte{}, fmt.Errorf("%w: CEK and manifest digest must be 32 bytes", errInvalidOfflinePackBytes)
	}
	prk, err := hkdf.Extract(sha256.New, cek, manifestSHA256)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("BFOP CIK HKDF extract: %w", err)
	}
	key, err := hkdf.Expand(sha256.New, prk, "BFOP-cik-v1", sha256.Size)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("BFOP CIK HKDF expand: %w", err)
	}
	var cik [sha256.Size]byte
	copy(cik[:], key)
	return cik, nil
}

// ValidatePackResourceBounds enforces D9 limits.
func ValidatePackResourceBounds(itemSizes []uint64) error {
	if len(itemSizes) > BFOPMaxPackItems {
		return fmt.Errorf("%w: pack has more than %d items", errInvalidOfflinePackBytes, BFOPMaxPackItems)
	}
	for _, size := range itemSizes {
		if err := ValidateBFOPItemSize(size); err != nil {
			return err
		}
	}
	return nil
}

// ValidateBFOPItemSize enforces the inclusive 4 GiB limit.
func ValidateBFOPItemSize(size uint64) error {
	if size > BFOPMaxItemSize {
		return fmt.Errorf("%w: BFOP item exceeds 4 GiB", errInvalidOfflinePackBytes)
	}
	return nil
}

// BFOPChunkCount returns max(1, ceil(len/1MiB)).
func BFOPChunkCount(plaintextLen uint64) (uint64, error) {
	if err := ValidateBFOPItemSize(plaintextLen); err != nil {
		return 0, err
	}
	if plaintextLen == 0 {
		return 1, nil
	}
	return (plaintextLen + BFOPChunkSize - 1) / BFOPChunkSize, nil
}

// BFOPChunkRange returns a sealed chunk's file range.
func BFOPChunkRange(plaintextLen, chunkIndex uint64) (uint64, uint64, error) {
	count, err := BFOPChunkCount(plaintextLen)
	if err != nil {
		return 0, 0, err
	}
	if chunkIndex >= count {
		return 0, 0, fmt.Errorf("%w: BFOP chunk index out of range", errInvalidOfflinePackBytes)
	}
	plainLen := BFOPChunkSize
	if plaintextLen == 0 {
		plainLen = 0
	} else if chunkIndex == count-1 && plaintextLen%BFOPChunkSize != 0 {
		plainLen = plaintextLen % BFOPChunkSize
	}
	return BFOPHeaderSize + chunkIndex*(BFOPChunkSize+bfopTagSize), plainLen + bfopTagSize, nil
}

// BFOPFileSize returns the exact framed size.
func BFOPFileSize(plaintextLen uint64) (uint64, error) {
	count, err := BFOPChunkCount(plaintextLen)
	if err != nil {
		return 0, err
	}
	return BFOPHeaderSize + plaintextLen + bfopTagSize*count, nil
}

// EncryptPackItem emits one BFOP v2 item.
func EncryptPackItem(cek, manifestDigest []byte, itemIndex uint32, plaintext []byte) ([]byte, error) {
	itemDigest := sha256.Sum256(plaintext)
	header, err := makeBFOPHeader(manifestDigest, itemDigest[:], itemIndex, uint64(len(plaintext)))
	if err != nil {
		return nil, err
	}
	cik, err := DeriveCIK(cek, manifestDigest)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(cik[:])
	if err != nil {
		return nil, err
	}
	fileSize, err := BFOPFileSize(uint64(len(plaintext)))
	if err != nil || fileSize > uint64(maxInt()) { //nolint:gosec // maxInt() is non-negative by construction
		return nil, fmt.Errorf("%w: BFOP file is not addressable", errInvalidOfflinePackBytes)
	}
	out := make([]byte, 0, int(fileSize)) //nolint:gosec // bounded by the maxInt() guard above
	out = append(out, header[:]...)
	count, _ := BFOPChunkCount(uint64(len(plaintext)))
	for i := uint64(0); i < count; i++ {
		start, end := i*BFOPChunkSize, min((i+1)*BFOPChunkSize, uint64(len(plaintext)))
		nonce, aad := bfopChunkInputs(header[:], itemIndex, i, i == count-1)
		out = gcm.Seal(out, nonce[:], plaintext[start:end], aad[:])
	}
	return out, nil
}

// EncryptPackItemTo emits one BFOP v2 item without retaining its plaintext or
// ciphertext body. At most one plaintext chunk and one sealed chunk are live.
func EncryptPackItemTo(dst io.Writer, cek, manifestDigest []byte, itemIndex uint32, plaintextLen uint64, itemSHA256 []byte, plaintext io.Reader) error {
	header, err := makeBFOPHeader(manifestDigest, itemSHA256, itemIndex, plaintextLen)
	if err != nil {
		return err
	}
	cik, err := DeriveCIK(cek, manifestDigest)
	if err != nil {
		return err
	}
	gcm, err := newGCM(cik[:])
	if err != nil {
		return err
	}
	if err := writeBFOPBytes(dst, header[:]); err != nil {
		return err
	}
	hash := sha256.New()
	buffer := make([]byte, BFOPChunkSize)
	count, _ := BFOPChunkCount(plaintextLen)
	for i := uint64(0); i < count; i++ {
		chunkLen := min(BFOPChunkSize, plaintextLen-i*BFOPChunkSize)
		chunk := buffer[:chunkLen]
		if _, err := io.ReadFull(plaintext, chunk); err != nil {
			return fmt.Errorf("%w: plaintext shorter than catalog size", errInvalidOfflinePackBytes)
		}
		_, _ = hash.Write(chunk)
		nonce, aad := bfopChunkInputs(header[:], itemIndex, i, i == count-1)
		sealed := gcm.Seal(nil, nonce[:], chunk, aad[:])
		if err := writeBFOPBytes(dst, sealed); err != nil {
			return err
		}
	}
	var extra [1]byte
	if n, err := io.ReadFull(plaintext, extra[:]); n != 0 || err != io.EOF {
		return fmt.Errorf("%w: plaintext longer than catalog size", errInvalidOfflinePackBytes)
	}
	if !bytes.Equal(hash.Sum(nil), itemSHA256) {
		return fmt.Errorf("%w: item SHA-256 changed during build", errInvalidOfflinePackBytes)
	}
	return nil
}

func writeBFOPBytes(dst io.Writer, data []byte) error {
	written, err := dst.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

// ValidatePackItem checks framing against manifest metadata.
func ValidatePackItem(manifestDigest []byte, itemIndex uint32, plaintextLen uint64, itemSHA256, file []byte) error {
	if len(manifestDigest) != sha256.Size || len(itemSHA256) != sha256.Size || itemIndex >= BFOPMaxPackItems {
		return fmt.Errorf("%w: invalid BFOP manifest metadata", errInvalidOfflinePackBytes)
	}
	if err := ValidateBFOPItemSize(plaintextLen); err != nil {
		return err
	}
	if len(file) < BFOPHeaderSize {
		return fmt.Errorf("%w: truncated BFOP header", errInvalidOfflinePackBytes)
	}
	header := file[:BFOPHeaderSize]
	if !bytes.Equal(header[:8], bfopPrefix[:]) ||
		binary.BigEndian.Uint32(header[8:12]) != itemIndex ||
		binary.BigEndian.Uint32(header[12:16]) != uint32(BFOPChunkSize) ||
		binary.BigEndian.Uint64(header[16:24]) != plaintextLen ||
		!bytes.Equal(header[24:32], itemSHA256[:8]) ||
		!bytes.Equal(header[32:64], manifestDigest) {
		return fmt.Errorf("%w: BFOP header does not match manifest item", errInvalidOfflinePackBytes)
	}
	size, err := BFOPFileSize(plaintextLen)
	if err != nil || uint64(len(file)) != size {
		return fmt.Errorf("%w: BFOP file length mismatch", errInvalidOfflinePackBytes)
	}
	return nil
}

// DecryptPackItem authenticates and decrypts one BFOP v2 item.
func DecryptPackItem(cek, manifestDigest []byte, itemIndex uint32, plaintextLen uint64, itemSHA256, file []byte) ([]byte, error) {
	if err := ValidatePackItem(manifestDigest, itemIndex, plaintextLen, itemSHA256, file); err != nil {
		return nil, err
	}
	if plaintextLen > uint64(maxInt()) { //nolint:gosec // maxInt() is non-negative by construction
		return nil, fmt.Errorf("%w: BFOP plaintext is not addressable", errInvalidOfflinePackBytes)
	}
	cik, err := DeriveCIK(cek, manifestDigest)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(cik[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, int(plaintextLen)) //nolint:gosec // bounded by the maxInt() guard above
	count, _ := BFOPChunkCount(plaintextLen)
	for i := uint64(0); i < count; i++ {
		offset, sealedLen, _ := BFOPChunkRange(plaintextLen, i)
		nonce, aad := bfopChunkInputs(file[:BFOPHeaderSize], itemIndex, i, i == count-1)
		out, err = gcm.Open(out, nonce[:], file[offset:offset+sealedLen], aad[:])
		if err != nil {
			return nil, fmt.Errorf("%w: BFOP chunk %d authentication failed", errInvalidOfflinePackBytes, i)
		}
	}
	digest := sha256.Sum256(out)
	if !bytes.Equal(digest[:], itemSHA256) {
		return nil, fmt.Errorf("%w: item SHA-256 mismatch", errInvalidOfflinePackBytes)
	}
	return out, nil
}

func makeBFOPHeader(manifestDigest, itemSHA256 []byte, itemIndex uint32, plaintextLen uint64) ([BFOPHeaderSize]byte, error) {
	var header [BFOPHeaderSize]byte
	if len(manifestDigest) != sha256.Size || len(itemSHA256) != sha256.Size || itemIndex >= BFOPMaxPackItems {
		return header, fmt.Errorf("%w: invalid BFOP manifest metadata", errInvalidOfflinePackBytes)
	}
	if err := ValidateBFOPItemSize(plaintextLen); err != nil {
		return header, err
	}
	copy(header[:8], bfopPrefix[:])
	binary.BigEndian.PutUint32(header[8:12], itemIndex)
	binary.BigEndian.PutUint32(header[12:16], uint32(BFOPChunkSize))
	binary.BigEndian.PutUint64(header[16:24], plaintextLen)
	copy(header[24:32], itemSHA256[:8])
	copy(header[32:64], manifestDigest)
	return header, nil
}

func bfopChunkInputs(header []byte, itemIndex uint32, chunkIndex uint64, final bool) ([12]byte, [73]byte) {
	var nonce [12]byte
	binary.BigEndian.PutUint32(nonce[:4], itemIndex)
	binary.BigEndian.PutUint64(nonce[4:], chunkIndex)
	var aad [73]byte
	copy(aad[:64], header)
	binary.BigEndian.PutUint64(aad[64:72], chunkIndex)
	if final {
		aad[72] = 1
	}
	return nonce, aad
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
