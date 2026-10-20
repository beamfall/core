package libencrypt

import (
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

// v2 at-rest format. The byte contract is
// exact and immutable — every constant below is pinned by committed fixtures:
//
//	header  = "BFE2" || 0x02 || u32be(65536) || salt[32]              (41 bytes)
//	key     = HKDF-SHA256(IKM=DEK, salt=salt,
//	          info="beamfall/libencrypt/atrest/v2/object-key" || 0x00 || context,
//	          L=32)
//	frame   = u32be(len(ciphertext||tag)) || ciphertext || tag
//	nonce   = 0x0000000000000000 || u32be(index)
//	AAD     = raw-header || context || u32be(index) || final-byte
//	          (final-byte: 0x00 non-final, 0x01 final)
//
// context is the caller-supplied ObjectContext encoding (objectcontext.go);
// the header never carries it and is never trusted for identity. The object key
// is unique per object write (random 32-byte salt), so the deterministic
// counter nonce never repeats under one key. Frame index > 0xffffffff and
// plaintext > 2^48−1 bytes are rejected.
const (
	atRestV2Magic     = "BFE2"
	atRestV2Version   = 0x02
	atRestV2ChunkSize = 64 * 1024
	atRestV2SaltLen   = 32
	// atRestV2HeaderLen: magic(4) + version(1) + chunkSize(4) + salt(32).
	atRestV2HeaderLen = 4 + 1 + 4 + atRestV2SaltLen
	// atRestV2TagLen is the AES-256-GCM tag length.
	atRestV2TagLen = 16
	// atRestV2FrameOverhead: length prefix(4) + tag(16). v2 frames carry no
	// nonce on the wire — the nonce is the deterministic frame counter.
	atRestV2FrameOverhead = 4 + atRestV2TagLen
	// atRestV2MaxFrameIndex is the last valid frame index (u32be nonce counter).
	atRestV2MaxFrameIndex = uint64(0xffffffff)
	// atRestV2MaxPlaintext is the largest allowed object plaintext (2^48 − 1).
	atRestV2MaxPlaintext = int64(1)<<48 - 1
)

// atRestV2KeyInfoPrefix is the ASCII HKDF info-domain separator; a single 0x00
// byte separates it from the context so the two can never be confused.
const atRestV2KeyInfoPrefix = "beamfall/libencrypt/atrest/v2/object-key"

// deriveV2ObjectKey derives the per-object AES-256 key. The caller must zero
// the returned key after building the AEAD (see newV2GCM).
func deriveV2ObjectKey(dek, salt, context []byte) ([]byte, error) {
	if err := assertDEKLen(dek); err != nil {
		return nil, err
	}
	info := make([]byte, 0, len(atRestV2KeyInfoPrefix)+1+len(context))
	info = append(info, atRestV2KeyInfoPrefix...)
	info = append(info, 0x00)
	info = append(info, context...)
	return hkdf.Key(sha256.New, dek, salt, string(info), dekLen)
}

// newV2GCM derives the object key, builds the AEAD, and zeroes the key slice —
// the object key never outlives AEAD construction.
func newV2GCM(dek, salt, context []byte) (cipher.AEAD, error) {
	key, err := deriveV2ObjectKey(dek, salt, context)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	zero(key)
	return gcm, err
}

// v2Nonce is the deterministic frame nonce: 8 zero bytes || u32be(index).
func v2Nonce(index uint64) []byte {
	nonce := make([]byte, gcmNonceLen)
	binary.BigEndian.PutUint32(nonce[8:], uint32(index)) //nolint:gosec // index is bounds-checked against atRestV2MaxFrameIndex
	return nonce
}

// v2AAD builds one frame's AAD: raw-header || context || u32be(index) ||
// final-byte. headerAndContext is the precomputed raw-header||context prefix.
func v2AAD(headerAndContext []byte, index uint64, final bool) []byte {
	aad := make([]byte, 0, len(headerAndContext)+5)
	aad = append(aad, headerAndContext...)
	aad = binary.BigEndian.AppendUint32(aad, uint32(index)) //nolint:gosec // index is bounds-checked against atRestV2MaxFrameIndex
	if final {
		return append(aad, 0x01)
	}
	return append(aad, 0x00)
}

// v2Header assembles the exact 41-byte header for a salt.
func v2Header(salt []byte) []byte {
	header := make([]byte, 0, atRestV2HeaderLen)
	header = append(header, atRestV2Magic...)
	header = append(header, atRestV2Version)
	header = binary.BigEndian.AppendUint32(header, atRestV2ChunkSize)
	return append(header, salt...)
}

// parseV2Header validates a candidate header byte-exactly (magic, version, and
// the fixed 65536 chunk size) and returns the object salt. Identity is NEVER
// read from the header — only the caller's ObjectContext supplies it.
func parseV2Header(header []byte) ([]byte, error) {
	if len(header) != atRestV2HeaderLen {
		return nil, ErrCorrupt
	}
	if string(header[:4]) != atRestV2Magic {
		return nil, ErrCorrupt
	}
	if header[4] != atRestV2Version {
		return nil, ErrCorrupt
	}
	if binary.BigEndian.Uint32(header[5:9]) != atRestV2ChunkSize {
		return nil, ErrCorrupt
	}
	return header[9:], nil
}

// EncryptStreamV2 encrypts everything read from src into a v2 at-rest object
// bound to obj, written to dst under dek. A fresh random salt keys this object;
// the derived object key is zeroed after AEAD construction. Inputs larger than
// 2^48−1 bytes are rejected.
func EncryptStreamV2(dst io.Writer, src io.Reader, dek []byte, obj ObjectContext) error {
	salt, err := randomBytes(atRestV2SaltLen)
	if err != nil {
		return err
	}
	return encryptStreamV2WithSalt(dst, src, dek, obj, salt)
}

// encryptStreamV2WithSalt is EncryptStreamV2 with an explicit salt. It exists
// so the byte-exact fixture tests can regenerate a committed ciphertext
// deterministically; production always uses a fresh random salt.
func encryptStreamV2WithSalt(dst io.Writer, src io.Reader, dek []byte, obj ObjectContext, salt []byte) error {
	context, err := obj.encode()
	if err != nil {
		return err
	}
	if len(salt) != atRestV2SaltLen {
		return errors.New("libencrypt: v2 salt must be 32 bytes")
	}
	gcm, err := newV2GCM(dek, salt, context)
	if err != nil {
		return err
	}
	header := v2Header(salt)
	if _, err := dst.Write(header); err != nil {
		return err
	}
	aadPrefix := append(header, context...)

	plain := make([]byte, atRestV2ChunkSize)
	var index uint64
	var total int64
	for {
		n, readErr := io.ReadFull(src, plain)
		final := false
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			// io.EOF: chunk-aligned input — a final zero-length frame carries the
			// EOF marker. io.ErrUnexpectedEOF: a short final frame.
			final = true
		} else if readErr != nil {
			return readErr
		}
		if index > atRestV2MaxFrameIndex {
			return errors.New("libencrypt: v2 frame index overflow")
		}
		total += int64(n)
		if total > atRestV2MaxPlaintext {
			return errors.New("libencrypt: v2 object exceeds maximum plaintext size")
		}
		ciphertext := gcm.Seal(nil, v2Nonce(index), plain[:n], v2AAD(aadPrefix, index, final))
		frame := make([]byte, 4+len(ciphertext))
		binary.BigEndian.PutUint32(frame, uint32(len(ciphertext))) //nolint:gosec // bounded by chunk size + tag
		copy(frame[4:], ciphertext)
		if _, err := dst.Write(frame); err != nil {
			return err
		}
		if final {
			return nil
		}
		index++
	}
}

// DecryptStreamV2 reverses EncryptStreamV2 under the caller-supplied obj. Any
// identity mismatch (wrong context), splice, swap, reorder, duplicate,
// truncation, trailing data, header edit, or index overflow fails with
// ErrUnwrap/ErrCorrupt before the affected plaintext is written.
func DecryptStreamV2(dst io.Writer, src io.Reader, dek []byte, obj ObjectContext) error {
	context, err := obj.encode()
	if err != nil {
		return err
	}
	header := make([]byte, atRestV2HeaderLen)
	if _, err := io.ReadFull(src, header); err != nil {
		return ErrCorrupt
	}
	salt, err := parseV2Header(header)
	if err != nil {
		return err
	}
	gcm, err := newV2GCM(dek, salt, context)
	if err != nil {
		return err
	}
	aadPrefix := append(header, context...)

	lenBuf := make([]byte, 4)
	var index uint64
	for {
		if index > atRestV2MaxFrameIndex {
			return ErrUnwrap
		}
		if _, err := io.ReadFull(src, lenBuf); err != nil {
			// The stream ended without an authenticated final frame — truncated.
			return ErrUnwrap
		}
		frameLen := int(binary.BigEndian.Uint32(lenBuf))
		if frameLen < atRestV2TagLen || frameLen > atRestV2ChunkSize+atRestV2TagLen {
			return ErrUnwrap
		}
		ciphertext := make([]byte, frameLen)
		if _, err := io.ReadFull(src, ciphertext); err != nil {
			return ErrUnwrap
		}
		final, plain, err := openV2Frame(gcm, aadPrefix, ciphertext, index)
		if err != nil {
			return ErrUnwrap
		}
		if _, err := dst.Write(plain); err != nil {
			return err
		}
		if final {
			var trailing [1]byte
			n, err := src.Read(trailing[:])
			if n != 0 || err != io.EOF {
				return ErrUnwrap
			}
			return nil
		}
		index++
	}
}

// openV2Frame opens one frame at the expected index, first as non-final then as
// final (exactly one AAD authenticates for a genuine frame).
func openV2Frame(gcm cipher.AEAD, aadPrefix, ciphertext []byte, index uint64) (final bool, plain []byte, err error) {
	nonce := v2Nonce(index)
	if p, e := gcm.Open(nil, nonce, ciphertext, v2AAD(aadPrefix, index, false)); e == nil {
		return false, p, nil
	}
	if p, e := gcm.Open(nil, nonce, ciphertext, v2AAD(aadPrefix, index, true)); e == nil {
		return true, p, nil
	}
	return false, nil, ErrUnwrap
}

// DecryptReaderV2 is the seekable plaintext view over one v2 object. Like the
// v1 reader it authenticates only the frame each Read needs, but every open and
// every frame is bound to the caller's ObjectContext.
type DecryptReaderV2 struct {
	src       io.ReaderAt
	gcm       cipher.AEAD
	aadPrefix []byte
	plainSize int64
	final     uint64
	pos       int64
}

// NewDecryptReaderV2 opens a seekable plaintext view over a v2 object bound to
// obj. ciphertextSize must be the exact size of src. Opening validates the
// header byte-exactly, derives geometry solely from the ciphertext size, and
// authenticates the final frame — truncation, trailing bytes, and identity
// mismatch all fail before any plaintext is returned.
func NewDecryptReaderV2(src io.ReaderAt, ciphertextSize int64, dek []byte, obj ObjectContext) (*DecryptReaderV2, error) {
	context, err := obj.encode()
	if err != nil {
		return nil, err
	}
	header := make([]byte, atRestV2HeaderLen)
	if _, err := src.ReadAt(header, 0); err != nil {
		return nil, ErrCorrupt
	}
	salt, err := parseV2Header(header)
	if err != nil {
		return nil, err
	}
	gcm, err := newV2GCM(dek, salt, context)
	if err != nil {
		return nil, err
	}
	const fullFrameSize = int64(atRestV2FrameOverhead + atRestV2ChunkSize)
	const minFrameSize = int64(atRestV2FrameOverhead)
	bodySize := ciphertextSize - atRestV2HeaderLen
	if bodySize < minFrameSize {
		return nil, ErrUnwrap
	}
	fullChunks := (bodySize - minFrameSize) / fullFrameSize
	finalFrameSize := bodySize - fullChunks*fullFrameSize
	if finalFrameSize < minFrameSize || finalFrameSize >= fullFrameSize {
		return nil, ErrUnwrap
	}
	if uint64(fullChunks) > atRestV2MaxFrameIndex { //nolint:gosec // non-negative quotient of validated sizes
		return nil, ErrUnwrap
	}
	plainSize := fullChunks*atRestV2ChunkSize + finalFrameSize - minFrameSize
	if plainSize > atRestV2MaxPlaintext {
		return nil, ErrUnwrap
	}
	r := &DecryptReaderV2{
		src:       src,
		gcm:       gcm,
		aadPrefix: append(header, context...),
		plainSize: plainSize,
		final:     uint64(fullChunks), //nolint:gosec // non-negative quotient of validated sizes
	}
	plain, err := r.decryptChunk(r.final)
	if err != nil || int64(len(plain)) != finalFrameSize-minFrameSize {
		return nil, ErrUnwrap
	}
	return r, nil
}

// Read implements io.Reader without buffering more than one decrypted frame.
func (r *DecryptReaderV2) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.pos >= r.plainSize {
		return 0, io.EOF
	}
	written := 0
	for len(p) > 0 && r.pos < r.plainSize {
		index := uint64(r.pos / atRestV2ChunkSize) //nolint:gosec // pos is non-negative here
		chunk, err := r.decryptChunk(index)
		if err != nil {
			return written, err
		}
		offset := int(r.pos % atRestV2ChunkSize)
		n := copy(p, chunk[offset:])
		p = p[n:]
		written += n
		r.pos += int64(n)
	}
	return written, nil
}

// Seek implements io.Seeker over plaintext offsets.
func (r *DecryptReaderV2) Seek(offset int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = r.pos
	case io.SeekEnd:
		base = r.plainSize
	default:
		return 0, errors.New("libencrypt: invalid seek whence")
	}
	next := base + offset
	if next < 0 {
		return 0, errors.New("libencrypt: negative seek position")
	}
	r.pos = next
	return next, nil
}

func (r *DecryptReaderV2) decryptChunk(index uint64) ([]byte, error) {
	if index > r.final {
		return nil, io.EOF
	}
	const fullFrameSize = int64(atRestV2FrameOverhead + atRestV2ChunkSize)
	offset := int64(atRestV2HeaderLen) + int64(index)*fullFrameSize //nolint:gosec // index is derived from bounded file size
	var lenBuf [4]byte
	if _, err := r.src.ReadAt(lenBuf[:], offset); err != nil {
		return nil, ErrUnwrap
	}
	frameLen := int64(binary.BigEndian.Uint32(lenBuf[:]))
	wantLen := int64(atRestV2TagLen + atRestV2ChunkSize)
	final := index == r.final
	if final {
		wantLen = int64(atRestV2TagLen) + r.plainSize - int64(index)*atRestV2ChunkSize //nolint:gosec // index is bounded by final
	}
	if frameLen != wantLen {
		return nil, ErrUnwrap
	}
	ciphertext := make([]byte, frameLen)
	if _, err := r.src.ReadAt(ciphertext, offset+4); err != nil {
		return nil, ErrUnwrap
	}
	plain, err := r.gcm.Open(nil, v2Nonce(index), ciphertext, v2AAD(r.aadPrefix, index, final))
	if err != nil {
		return nil, ErrUnwrap
	}
	return plain, nil
}
