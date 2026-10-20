package libencrypt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrRewrapInsufficientSpace is returned when the filesystem backing a library
// file lacks room to stage its re-encrypted copy. Staging precedes the journal,
// so a re-wrap that hits this aborts before touching any committed state and the
// old key material stays intact (ENCPRIV-6, explicit space accounting).
var ErrRewrapInsufficientSpace = errors.New("libencrypt: insufficient space to stage rewrap")

// freeBytesFn queries free space on the filesystem backing a directory. It is a
// seam so tests can force a shortfall deterministically; production wires it to
// the per-platform freeBytesOnFS (statfs_*.go).
var freeBytesFn = freeBytesOnFS

// renameRewrapFile is the forward-swap rename seam. Recovery renames keep using
// os.Rename so a test can inject one deterministic swap fault without also
// sabotaging the rollback it is meant to verify.
var renameRewrapFile = os.Rename

// Per-Encrypted-Library metadata DB lifecycle (ADR-0045 §2.3, the "third
// DB-file class"). These helpers are the at-rest envelope around one Library's
// own SQLite file: at rest it is a single ciphertext blob on disk; while the
// Library is Unsealed it is decrypted to a protected working copy the store
// opens with the pure-Go driver; on Seal the working copy is re-encrypted and
// wiped.
//
// The cycle, by library id (the Manager already holds the in-memory DEK keyed by
// id — these methods look it up, so a Sealed library cannot be decrypted):
//
//	Provision/Unseal → DEK in memory
//	UnsealDBFile(id, ciphertextPath, workingPath)  → writes plaintext workingPath
//	... store opens workingPath, serves/queries while Unsealed ...
//	SealDBFile(id, workingPath, ciphertextPath)    → re-encrypts, wipes workingPath
//	Seal(id) → DEK dropped
//
// Sealed safety: between SealDBFile and the next UnsealDBFile, only ciphertextPath
// exists on disk and the DEK is not in memory, so the metadata is unrecoverable
// from the disk alone (§2.2 stolen-disk threat). The working copy is plaintext
// only while Unsealed — the server-readable-at-rest trust model, not a leak.

// objectKeyAndFormat resolves the in-memory DEK and the library's committed
// at-rest format for one object operation, validating the caller-supplied
// ObjectContext up front (required on every call, even for a v1 generation, so
// a missing identity is a caller bug regardless of format).
func (m *Manager) objectKeyAndFormatLocked(libraryID string, obj ObjectContext) ([]byte, int, error) {
	if _, err := obj.encode(); err != nil {
		return nil, 0, err
	}
	dek, err := m.dekCopyLocked(libraryID)
	if err != nil {
		return nil, 0, err
	}
	format, err := m.atRestFormatLocked(libraryID)
	if err != nil {
		zero(dek)
		return nil, 0, err
	}
	return dek, format, nil
}

// EncryptFile encrypts the file at plaintextPath into an at-rest stream written
// to ciphertextPath, under the DEK of the (Unsealed) library and bound to the
// caller-supplied ObjectContext. The object is written in the library's
// committed key-generation format: a v1 generation keeps writing v1 (no
// same-DEK partial upgrade — v2 arrives only via Rewrap), a v2 generation
// writes context-bound v2. ciphertextPath is written atomically (temp + rename)
// so a crash never leaves a half-written ciphertext shadowing a good one. A
// Sealed/unprovisioned library returns ErrSealed and nothing is written.
func (m *Manager) encryptFileLocked(libraryID, plaintextPath, ciphertextPath string, obj ObjectContext) error {
	dek, format, err := m.objectKeyAndFormatLocked(libraryID, obj)
	if err != nil {
		return err
	}
	defer zero(dek)
	return encryptFileWithDEK(plaintextPath, ciphertextPath, dek, format, obj)
}

// DecryptFile decrypts the at-rest stream at ciphertextPath into plaintextPath,
// under the DEK of the (Unsealed) library and the caller-supplied
// ObjectContext. The expected format is the key generation's commitment, never
// sniffed from the file: a v2 generation rejects restored v1 ciphertext (and
// vice versa) before any plaintext is written. plaintextPath is created 0600
// (the working copy is protected like every other libencrypt at-rest artifact,
// invariant #5) and written atomically. A wrong/sealed DEK, wrong context, or
// tampered ciphertext fails (ErrSealed / ErrUnwrap / ErrCorrupt) and the
// partial output is removed.
func (m *Manager) decryptFileLocked(libraryID, ciphertextPath, plaintextPath string, obj ObjectContext) error {
	dek, format, err := m.objectKeyAndFormatLocked(libraryID, obj)
	if err != nil {
		return err
	}
	defer zero(dek)
	return decryptFileWithDEK(ciphertextPath, plaintextPath, dek, format, obj)
}

// OpenDecryptReader opens a seekable, streaming plaintext view of one encrypted
// object bound to the caller-supplied ObjectContext. The decoder is selected by
// the library's committed key generation (v1 seek/range stays readable ONLY in
// a legacy v1 generation; a v2 generation opens only context-bound v2 objects).
// Closing the returned reader closes the ciphertext file; plaintext is never
// materialized as a whole file or byte slice.
func (m *Manager) openDecryptReaderLocked(libraryID, ciphertextPath string, obj ObjectContext) (io.ReadSeekCloser, error) {
	dek, format, err := m.objectKeyAndFormatLocked(libraryID, obj)
	if err != nil {
		return nil, err
	}
	defer zero(dek)
	src, err := os.Open(ciphertextPath) //nolint:gosec // library-owned path selected by the authenticated byte route
	if err != nil {
		return nil, err
	}
	info, err := src.Stat()
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	var reader io.ReadSeeker
	if format == atRestFormatV2 {
		reader, err = NewDecryptReaderV2(src, info.Size(), dek, obj)
	} else {
		reader, err = NewDecryptReader(src, info.Size(), dek)
	}
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	return &decryptReadSeekCloser{ReadSeeker: reader, Closer: src}, nil
}

type decryptReadSeekCloser struct {
	io.ReadSeeker
	io.Closer
}

// UnsealDBFile decrypts a Library's at-rest metadata DB (ciphertextPath) to a
// plaintext working copy (workingPath) the store can open. It is the on-unseal
// half of the metadata-DB cycle. workingPath should sit on storage the OS
// protects (the data dir, 0600); it is created 0600. If ciphertextPath does not
// yet exist (a freshly-provisioned Library that has never been sealed), it is a
// no-op success — the store will create the working DB fresh and SealDBFile will
// mint the first ciphertext.
func (m *Manager) unsealDBFileLocked(libraryID, ciphertextPath, workingPath string, obj ObjectContext) error {
	if _, err := os.Stat(ciphertextPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return m.decryptFileLocked(libraryID, ciphertextPath, workingPath, obj)
}

// SealDBFile re-encrypts the plaintext working copy (workingPath) back to the
// at-rest ciphertext (ciphertextPath) and then securely removes the working copy,
// returning the Library's metadata to ciphertext-at-rest. It is the on-seal half
// of the cycle. The DEK must still be in memory (call this BEFORE Seal drops it).
// The wipe is best-effort overwrite-then-unlink: Go cannot guarantee the bytes
// leave the physical medium (the FS/SSD may copy-on-write), but clearing the
// live file shrinks the window a disk image catches plaintext.
func (m *Manager) sealDBFileLocked(libraryID, workingPath, ciphertextPath string, obj ObjectContext) error {
	if err := m.encryptFileLocked(libraryID, workingPath, ciphertextPath, obj); err != nil {
		return err
	}
	return wipeFile(workingPath)
}

func encryptFileWithDEK(plaintextPath, ciphertextPath string, dek []byte, format int, obj ObjectContext) error {
	if err := assertDEKLen(dek); err != nil {
		return err
	}
	src, err := os.Open(plaintextPath)
	if err != nil {
		return err
	}
	defer src.Close()
	return atomicWrite(ciphertextPath, func(w io.Writer) error {
		if format == atRestFormatV2 {
			return EncryptStreamV2(w, src, dek, obj)
		}
		return EncryptStream(w, src, dek)
	})
}

func decryptFileWithDEK(ciphertextPath, plaintextPath string, dek []byte, format int, obj ObjectContext) error {
	if err := assertDEKLen(dek); err != nil {
		return err
	}
	src, err := os.Open(ciphertextPath)
	if err != nil {
		return err
	}
	defer src.Close()
	return atomicWrite(plaintextPath, func(w io.Writer) error {
		if format == atRestFormatV2 {
			return DecryptStreamV2(w, src, dek, obj)
		}
		return DecryptStream(w, src, dek)
	})
}

type stagedRewrapFile struct {
	dst string
	tmp string
}

// stageRewrapFileWithDEK re-encrypts one object for Rewrap: it decrypts under
// the old DEK using the OLD generation's committed format (v1 legacy or
// context-bound v2) and stages a context-bound v2 object under the new DEK.
// The staged temp is the roll-forward image; the untouched destination is the
// byte-identical rollback image.
func stageRewrapFileWithDEK(ciphertextPath string, oldDEK, newDEK []byte, oldFormat int, obj ObjectContext) (stagedRewrapFile, error) {
	if err := assertDEKLen(oldDEK); err != nil {
		return stagedRewrapFile{}, err
	}
	if err := assertDEKLen(newDEK); err != nil {
		return stagedRewrapFile{}, err
	}
	if _, err := obj.encode(); err != nil {
		return stagedRewrapFile{}, err
	}
	if err := checkRewrapStagingSpace(ciphertextPath); err != nil {
		return stagedRewrapFile{}, err
	}
	src, err := os.Open(ciphertextPath)
	if err != nil {
		return stagedRewrapFile{}, err
	}
	defer src.Close()
	tmp, err := stagedAtomicWrite(ciphertextPath, func(dst io.Writer) error {
		pr, pw := io.Pipe()
		done := make(chan error, 1)
		go func() {
			done <- EncryptStreamV2(dst, pr, newDEK, obj)
		}()
		var decErr error
		if oldFormat == atRestFormatV2 {
			decErr = DecryptStreamV2(pw, src, oldDEK, obj)
		} else {
			decErr = DecryptStream(pw, src, oldDEK)
		}
		if decErr != nil {
			_ = pw.CloseWithError(decErr)
			<-done
			return decErr
		}
		if err := pw.Close(); err != nil {
			<-done
			return err
		}
		return <-done
	})
	if err != nil {
		return stagedRewrapFile{}, err
	}
	return stagedRewrapFile{dst: ciphertextPath, tmp: tmp}, nil
}

// checkRewrapStagingSpace fails fast when the filesystem backing ciphertextPath
// lacks room for the staged re-encrypted copy (~the source size, since the same
// plaintext is re-framed under the new DEK). Each staged temp is written before
// the next file is checked, so a per-file statfs already reflects prior temps'
// consumption — this enforces the cumulative requirement too. An unsupported
// platform (ok==false) skips the check rather than failing the re-wrap.
func checkRewrapStagingSpace(ciphertextPath string) error {
	info, err := os.Stat(ciphertextPath)
	if err != nil {
		return err
	}
	free, ok, err := freeBytesFn(filepath.Dir(ciphertextPath))
	if err != nil {
		return err
	}
	if ok && free < uint64(info.Size()) {
		return fmt.Errorf("%w: %s needs %d bytes, %d free", ErrRewrapInsufficientSpace, ciphertextPath, info.Size(), free)
	}
	return nil
}

// atomicWrite writes to a sibling temp file (0600) then renames it over dst, so a
// reader never sees a partial file and a crash never corrupts an existing dst. On
// any write error the temp file is removed.
func atomicWrite(dst string, write func(io.Writer) error) error {
	tmpName, err := stagedAtomicWrite(dst, write)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func stagedAtomicWrite(dst string, write func(io.Writer) error) (string, error) {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".libenc-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	return tmpName, nil
}

func cleanupStagedRewrapFiles(staged []stagedRewrapFile) {
	for _, f := range staged {
		_ = os.Remove(f.tmp)
	}
}

func swapStagedRewrapFiles(staged []stagedRewrapFile) ([]stagedRewrapFile, error) {
	swapped := []stagedRewrapFile{}
	for _, f := range staged {
		backup := f.tmp + ".old"
		if err := renameRewrapFile(f.dst, backup); err != nil {
			rollbackStagedRewrapFiles(swapped)
			return nil, err
		}
		if err := renameRewrapFile(f.tmp, f.dst); err != nil {
			_ = os.Rename(backup, f.dst)
			rollbackStagedRewrapFiles(swapped)
			return nil, err
		}
		swapped = append(swapped, stagedRewrapFile{dst: f.dst, tmp: backup})
	}
	if err := syncStagedRewrapDirs(swapped); err != nil {
		rollbackStagedRewrapFiles(swapped)
		return nil, err
	}
	return swapped, nil
}

func syncStagedRewrapDirs(staged []stagedRewrapFile) error {
	dirs := make([]string, 0, len(staged))
	for _, f := range staged {
		dirs = append(dirs, filepath.Dir(f.dst))
	}
	return syncDirs(dirs)
}

func syncRewrapJournalDirs(files []rewrapJournalFile) error {
	dirs := make([]string, 0, len(files))
	for _, f := range files {
		dirs = append(dirs, filepath.Dir(f.Dst))
	}
	return syncDirs(dirs)
}

func syncDirs(dirs []string) error {
	synced := map[string]bool{}
	for _, dir := range dirs {
		if synced[dir] {
			continue
		}
		d, err := os.Open(dir)
		if err != nil {
			return err
		}
		if err := d.Sync(); err != nil {
			_ = d.Close()
			return err
		}
		if err := d.Close(); err != nil {
			return err
		}
		synced[dir] = true
	}
	return nil
}

func rollbackStagedRewrapFiles(swapped []stagedRewrapFile) {
	for i := len(swapped) - 1; i >= 0; i-- {
		_ = os.Remove(swapped[i].dst)
		_ = os.Rename(swapped[i].tmp, swapped[i].dst)
	}
}

// wipeFile overwrites a file's bytes with zeroes then removes it. Best-effort
// hygiene (see SealDBFile). A missing file is not an error — the working copy may
// already be gone.
func wipeFile(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zeros := make([]byte, 32*1024)
	remaining := info.Size()
	for remaining > 0 {
		n := int64(len(zeros))
		if n > remaining {
			n = remaining
		}
		if _, err := f.Write(zeros[:n]); err != nil {
			_ = f.Close()
			return err
		}
		remaining -= n
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("libencrypt: remove working copy: %w", err)
	}
	return nil
}
