package libencrypt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"path/filepath"
)

// EncryptObjectFrom encrypts a plaintext stream into the at-rest object at
// ciphertextPath, under the DEK of the (Unsealed) library and bound to the
// caller-supplied ObjectContext. It is the streaming counterpart of
// EncryptFile for producers whose plaintext only ever exists in memory or on a
// pipe (an extracted subtitle, a transcoder's stdout): plaintext is read from
// src and only ciphertext reaches the disk.
//
// The object is written in the library's committed key-generation format, into
// an O_EXCL temporary file in the target directory, fsynced, renamed into place
// and the directory synced. On any error the temporary file is removed and
// ciphertextPath is left untouched. A Sealed/unprovisioned library returns
// ErrSealed and nothing is written.
//
// It takes the SHARED ownership lease: a write never waits on open readers of
// the same library, while seal, unseal and rewrap still exclude it, so an object
// can never be written under a key generation that is being retired.
func (m *Manager) EncryptObjectFrom(libraryID string, src io.Reader, ciphertextPath string, obj ObjectContext) error {
	if src == nil {
		return errors.New("libencrypt: nil object source")
	}
	release := m.acquireLibraryShared(libraryID)
	defer release()
	dek, format, err := m.objectKeyAndFormatLocked(libraryID, obj)
	if err != nil {
		return err
	}
	defer zero(dek)
	if err := assertDEKLen(dek); err != nil {
		return err
	}
	if err := atomicWrite(ciphertextPath, func(w io.Writer) error {
		if format == atRestFormatV2 {
			return EncryptStreamV2(w, src, dek, obj)
		}
		return EncryptStream(w, src, dek)
	}); err != nil {
		return err
	}
	return syncDirs([]string{filepath.Dir(ciphertextPath)})
}

// HasObjectHeader reports whether r starts with a libencrypt at-rest header
// (v1 or v2 magic, version and chunk size). It is a cheap structural check for
// telling a ciphertext object from a plaintext file, such as when a reconciler
// decides which derivatives to keep; it authenticates nothing, so a positive
// answer never stands in for decryption. A stream shorter than the header is
// not an object and returns false with no error.
func HasObjectHeader(r io.Reader) (bool, error) {
	header := make([]byte, atRestHeaderLen)
	if _, err := io.ReadFull(r, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(header, objectHeaderPrefix(atRestMagic, atRestVersion, atRestChunkSize)) ||
		bytes.Equal(header, objectHeaderPrefix(atRestV2Magic, atRestV2Version, atRestV2ChunkSize)), nil
}

func objectHeaderPrefix(magic string, version byte, chunkSize uint32) []byte {
	prefix := make([]byte, 0, atRestHeaderLen)
	prefix = append(prefix, magic...)
	prefix = append(prefix, version)
	return binary.BigEndian.AppendUint32(prefix, chunkSize)
}
