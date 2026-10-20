package libencrypt

import (
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ENC-3 at-rest cipher. This is the DEK consumer libencrypt's
// header comment named as "ENC-3, does not exist yet": the application-level
// encrypt/decrypt of the three at-rest tiers (original bytes, derivatives, and
// the per-Encrypted-Library metadata DB file) under the in-memory DEK.
//
// Why application-level and not SQLCipher: the store uses the PURE-GO
// modernc.org/sqlite driver (no cgo). SQLCipher is a cgo C extension and is
// incompatible with it, and switching drivers is forbidden (it would break the
// cgo-free CI gate). The ticket's "SQLCipher-style" is therefore achieved as an
// EFFECT — ciphertext at rest, plaintext only in memory / a protected working
// copy while Unsealed — implemented here with AES-256-GCM, the same AEAD ENC-2
// uses to wrap the DEK.
//
// Scheme — file-level, chunked AEAD (NOT page-level / VFS):
//   - The whole file (a DB file, a media blob, a derivative) is encrypted as a
//     sequence of fixed-size plaintext chunks. Each chunk gets a fresh random
//     12-byte nonce and is sealed with AES-256-GCM under the DEK.
//   - A small header records a magic, the format version, and the chunk size, so
//     a stolen file is self-describing only as ciphertext (no plaintext leaks).
//   - The chunk index and a final-chunk flag are bound into the AEAD additional
//     data, so an attacker cannot reorder, duplicate, or truncate chunks without
//     the open failing. Truncation is the threat a plain per-chunk AEAD misses;
//     binding the index + EOF marker closes it.
//
// File-level (decrypt-the-whole-file on unseal) is the v1 choice over page-level
// in-place encryption: page-level would need a custom SQLite VFS the pure-Go
// driver does not cleanly expose, and the metadata DB is small (one Library's
// EXIF/Location/Album/FTS rows), so decrypting it whole to a protected working
// copy on unseal is cheap and simple. The cost — the working copy is plaintext
// on disk while Unsealed — is exactly the server-readable-at-rest trust model of
// §2.2 (a running server holds the key and plaintext); at rest / Sealed there is
// no working copy and no key, so a stolen disk reveals nothing.
//
// Invariant #5: the DEK passed in is held only in memory by the caller (from the
// Manager); nothing here logs it, persists it, or puts it in an error. Errors are
// generic (ErrCorrupt / ErrUnwrap) and never echo key material or plaintext.

const (
	// atRestMagic identifies a libencrypt at-rest stream. Four bytes, so a
	// truncated/foreign file fails the header check immediately as ciphertext.
	atRestMagic = "BFE1"
	// atRestVersion is the stream format version. A future page-level or
	// different-AEAD format bumps this; DecryptStream rejects unknown versions.
	atRestVersion = 1
	// atRestChunkSize is the plaintext chunk size (64 KiB). Bounded memory for
	// arbitrarily large media bytes; small enough that the per-chunk nonce+tag
	// overhead (28 bytes / 64 KiB ≈ 0.04%) is negligible.
	atRestChunkSize = 64 * 1024
	// atRestHeaderLen is the fixed header length: magic(4) + version(1) +
	// chunkSize(4, big-endian uint32).
	atRestHeaderLen = 4 + 1 + 4
)

const atRestFrameOverhead = 4 + gcmNonceLen

// ErrCorrupt is returned when an at-rest stream is malformed at the framing
// level (bad magic, unknown version, truncated header). A failure inside the
// AEAD (wrong DEK, tampered chunk, reordered/truncated body) collapses to
// ErrUnwrap, reusing ENC-2's generic unwrap error so the caller never learns
// which input was wrong and no key/plaintext is echoed (invariant #5).
var ErrCorrupt = errors.New("libencrypt: corrupt at-rest stream")

// EncryptStream encrypts everything read from src and writes the libencrypt
// at-rest stream (header + sealed chunks) to dst, using dek (a 32-byte DEK from
// the Manager). It streams in atRestChunkSize chunks so memory stays bounded for
// large media bytes. The caller owns closing src and dst.
//
// dek must be exactly 32 bytes and must come from an Unsealed library (the
// caller holds it in memory only). A short final chunk (including a zero-length
// final chunk for an empty or chunk-aligned input) carries the EOF marker so
// truncation is detectable on decrypt.
func EncryptStream(dst io.Writer, src io.Reader, dek []byte) error {
	gcm, err := newGCM(dek)
	if err != nil {
		return err
	}
	header := make([]byte, atRestHeaderLen)
	copy(header, atRestMagic)
	header[4] = atRestVersion
	binary.BigEndian.PutUint32(header[5:], uint32(atRestChunkSize))
	if _, err := dst.Write(header); err != nil {
		return err
	}

	plain := make([]byte, atRestChunkSize)
	var index uint64
	for {
		n, readErr := io.ReadFull(src, plain)
		final := false
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			// io.EOF: chunk-aligned input — emit a final zero-length chunk so the
			// reader sees the EOF marker. io.ErrUnexpectedEOF: a short final chunk.
			final = true
		} else if readErr != nil {
			return readErr
		}
		if err := writeChunk(dst, gcm, plain[:n], index, final); err != nil {
			return err
		}
		if final {
			return nil
		}
		index++
	}
}

// DecryptStream reverses EncryptStream: it reads the at-rest stream from src,
// verifies and decrypts each chunk under dek, and writes the recovered plaintext
// to dst. A wrong DEK, a tampered/reordered chunk, or a truncated body fails with
// ErrUnwrap; a malformed header fails with ErrCorrupt. The caller owns closing
// src and dst.
func DecryptStream(dst io.Writer, src io.Reader, dek []byte) error {
	gcm, err := newGCM(dek)
	if err != nil {
		return err
	}
	header := make([]byte, atRestHeaderLen)
	if _, err := io.ReadFull(src, header); err != nil {
		return ErrCorrupt
	}
	if string(header[:4]) != atRestMagic {
		return ErrCorrupt
	}
	if header[4] != atRestVersion {
		return ErrCorrupt
	}
	chunkSize := int(binary.BigEndian.Uint32(header[5:]))
	if chunkSize <= 0 || chunkSize > 16*1024*1024 {
		return ErrCorrupt
	}

	// Each sealed chunk on the wire is len(4, big-endian) || nonce || ciphertext+tag.
	lenBuf := make([]byte, 4)
	var index uint64
	for {
		if _, err := io.ReadFull(src, lenBuf); err != nil {
			// A clean EOF here means the stream ended without the EOF-marked final
			// chunk — i.e. it was truncated mid-stream. That is an integrity failure.
			return ErrUnwrap
		}
		sealedLen := int(binary.BigEndian.Uint32(lenBuf))
		if sealedLen < gcmNonceLen || sealedLen > chunkSize+gcmNonceLen+gcm.Overhead() {
			return ErrUnwrap
		}
		sealed := make([]byte, sealedLen)
		if _, err := io.ReadFull(src, sealed); err != nil {
			return ErrUnwrap
		}
		nonce, ciphertext := sealed[:gcmNonceLen], sealed[gcmNonceLen:]
		// The chunk index + final flag must match what was sealed (chunkAAD), so a
		// reordered, duplicated, or truncated body fails the AEAD open.
		final, plain, err := openChunk(gcm, nonce, ciphertext, index)
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

// DecryptReader is a consumer-neutral, seekable plaintext view over one
// libencrypt at-rest object. It locates fixed-size encrypted chunks and
// authenticates only the chunk needed by Read; it does not implement readahead
// or range caching. The caller retains ownership of src.
type DecryptReader struct {
	src       io.ReaderAt
	gcm       cipher.AEAD
	chunkSize int64
	plainSize int64
	final     uint64
	pos       int64
}

// NewDecryptReader opens a seekable plaintext view over an at-rest object.
// ciphertextSize must be the exact size of src. Opening authenticates the final
// chunk, preserving the format's fail-closed truncation and trailing-data
// semantics before any plaintext is returned.
func NewDecryptReader(src io.ReaderAt, ciphertextSize int64, dek []byte) (*DecryptReader, error) {
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	header := make([]byte, atRestHeaderLen)
	if _, err := src.ReadAt(header, 0); err != nil {
		return nil, ErrCorrupt
	}
	if string(header[:4]) != atRestMagic || header[4] != atRestVersion {
		return nil, ErrCorrupt
	}
	chunkSize := int64(binary.BigEndian.Uint32(header[5:]))
	if chunkSize <= 0 || chunkSize > 16*1024*1024 {
		return nil, ErrCorrupt
	}
	fullFrameSize := int64(atRestFrameOverhead+gcm.Overhead()) + chunkSize
	bodySize := ciphertextSize - atRestHeaderLen
	minFrameSize := int64(atRestFrameOverhead + gcm.Overhead())
	if bodySize < minFrameSize {
		return nil, ErrUnwrap
	}
	fullChunks := (bodySize - minFrameSize) / fullFrameSize
	finalFrameSize := bodySize - fullChunks*fullFrameSize
	if finalFrameSize < minFrameSize || finalFrameSize >= fullFrameSize {
		return nil, ErrUnwrap
	}
	r := &DecryptReader{
		src:       src,
		gcm:       gcm,
		chunkSize: chunkSize,
		plainSize: fullChunks*chunkSize + finalFrameSize - minFrameSize,
		final:     uint64(fullChunks), //nolint:gosec // non-negative quotient of validated sizes
	}
	plain, final, err := r.decryptChunk(r.final)
	if err != nil || !final || int64(len(plain)) != finalFrameSize-minFrameSize {
		return nil, ErrUnwrap
	}
	return r, nil
}

// Read implements io.Reader without buffering more than one decrypted chunk.
func (r *DecryptReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.pos >= r.plainSize {
		return 0, io.EOF
	}
	written := 0
	for len(p) > 0 && r.pos < r.plainSize {
		index := uint64(r.pos / r.chunkSize) //nolint:gosec // pos is non-negative here
		chunk, _, err := r.decryptChunk(index)
		if err != nil {
			return written, err
		}
		offset := int(r.pos % r.chunkSize)
		n := copy(p, chunk[offset:])
		p = p[n:]
		written += n
		r.pos += int64(n)
	}
	return written, nil
}

// Seek implements io.Seeker over plaintext offsets.
func (r *DecryptReader) Seek(offset int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = r.pos
	case io.SeekEnd:
		base = r.plainSize
	default:
		return 0, fmt.Errorf("libencrypt: invalid seek whence")
	}
	next := base + offset
	if next < 0 {
		return 0, fmt.Errorf("libencrypt: negative seek position")
	}
	r.pos = next
	return next, nil
}

func (r *DecryptReader) decryptChunk(index uint64) ([]byte, bool, error) {
	if index > r.final {
		return nil, false, io.EOF
	}
	fullFrameSize := int64(atRestFrameOverhead+r.gcm.Overhead()) + r.chunkSize
	offset := int64(atRestHeaderLen) + int64(index)*fullFrameSize //nolint:gosec // index is derived from bounded file size
	var lenBuf [4]byte
	if _, err := r.src.ReadAt(lenBuf[:], offset); err != nil {
		return nil, false, ErrUnwrap
	}
	sealedLen := int64(binary.BigEndian.Uint32(lenBuf[:]))
	wantLen := int64(gcmNonceLen+r.gcm.Overhead()) + r.chunkSize
	if index == r.final {
		wantLen = int64(gcmNonceLen+r.gcm.Overhead()) + r.plainSize - int64(index)*r.chunkSize //nolint:gosec // index is bounded by final
	}
	if sealedLen != wantLen {
		return nil, false, ErrUnwrap
	}
	sealed := make([]byte, sealedLen)
	if _, err := r.src.ReadAt(sealed, offset+4); err != nil {
		return nil, false, ErrUnwrap
	}
	final, plain, err := openChunk(r.gcm, sealed[:gcmNonceLen], sealed[gcmNonceLen:], index)
	if err != nil || final != (index == r.final) {
		return nil, false, ErrUnwrap
	}
	return plain, final, nil
}

// writeChunk seals one plaintext chunk and frames it as len || nonce ||
// ciphertext+tag. The chunk index and final flag are bound as AEAD additional
// data via chunkAAD.
func writeChunk(dst io.Writer, gcm cipher.AEAD, plain []byte, index uint64, final bool) error {
	nonce, err := randomBytes(gcmNonceLen)
	if err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, nonce, plain, chunkAAD(index, final))
	frame := make([]byte, 4+gcmNonceLen+len(ciphertext))
	binary.BigEndian.PutUint32(frame, uint32(gcmNonceLen+len(ciphertext)))
	copy(frame[4:], nonce)
	copy(frame[4+gcmNonceLen:], ciphertext)
	_, err = dst.Write(frame)
	return err
}

// openChunk tries to open a chunk's ciphertext at the expected index, first as a
// non-final then as a final chunk (the final flag is part of the AAD, so exactly
// one will authenticate for a genuine chunk). Returns whether it was the final
// chunk and the recovered plaintext.
func openChunk(gcm cipher.AEAD, nonce, ciphertext []byte, index uint64) (final bool, plain []byte, err error) {
	if p, e := gcm.Open(nil, nonce, ciphertext, chunkAAD(index, false)); e == nil {
		return false, p, nil
	}
	if p, e := gcm.Open(nil, nonce, ciphertext, chunkAAD(index, true)); e == nil {
		return true, p, nil
	}
	return false, nil, ErrUnwrap
}

// chunkAAD builds the additional authenticated data for a chunk: the 8-byte
// big-endian chunk index plus a 1-byte final-chunk flag. Binding the index
// defeats reordering/duplication; binding the EOF flag defeats truncation (the
// stream is only complete once a final-flagged chunk authenticates).
func chunkAAD(index uint64, final bool) []byte {
	aad := make([]byte, 9)
	binary.BigEndian.PutUint64(aad, index)
	if final {
		aad[8] = 1
	}
	return aad
}

// assertDEKLen is a guard used by the metadata-DB lifecycle helpers so a caller
// that hands a wrong-length key gets a clear programming error rather than a
// confusing AEAD failure.
func assertDEKLen(dek []byte) error {
	if len(dek) != dekLen {
		return fmt.Errorf("libencrypt: DEK must be %d bytes, got %d", dekLen, len(dek))
	}
	return nil
}
