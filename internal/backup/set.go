package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DirectorySealer owns the concrete Backup Set envelope operations injected
// into store by the app composition root.
type DirectorySealer struct {
	dataDir string
}

// NewDirectorySealer returns a sealer that loads (or bootstraps) the public
// wrap key stored under dataDir. It never retains private key material.
func NewDirectorySealer(dataDir string) DirectorySealer {
	return DirectorySealer{dataDir: dataDir}
}

// SealDirectory encrypts sourceDir and returns the public recipient
// fingerprint recorded in the store-owned receipt.
func (s DirectorySealer) SealDirectory(ctx context.Context, sourceDir, dest string) (string, error) {
	key, err := LoadPublicWrapKey(s.dataDir)
	if errors.Is(err, os.ErrNotExist) {
		if _, createErr := CreateBootstrapEscrow(s.dataDir, time.Now().UTC()); createErr != nil &&
			!errors.Is(createErr, ErrBootstrapEscrowNotFound) {
			return "", fmt.Errorf("backup public wrap key bootstrap: %w", createErr)
		}
		key, err = LoadPublicWrapKey(s.dataDir)
	}
	if err != nil {
		return "", fmt.Errorf("backup public wrap key is not provisioned: %w", err)
	}
	if err := SealDirectory(ctx, key, sourceDir, dest); err != nil {
		return "", err
	}
	return key.Fingerprint(), nil
}

// VerifyEnvelopeStructure validates public envelope framing without a key.
func (DirectorySealer) VerifyEnvelopeStructure(src io.Reader) error {
	_, err := VerifyEnvelopeStructure(src)
	return err
}

// DirectoryOpener owns the user-supplied key for one restore operation.
type DirectoryOpener struct {
	key BackupKey
}

// NewDirectoryOpener returns a keyed Backup Set directory opener.
func NewDirectoryOpener(key BackupKey) DirectoryOpener {
	return DirectoryOpener{key: key}
}

// OpenDirectory decrypts envelopePath into destDir.
func (o DirectoryOpener) OpenDirectory(ctx context.Context, envelopePath, destDir string) error {
	_, err := OpenDirectory(ctx, o.key, envelopePath, destDir)
	return err
}

// VerifyEnvelopeStructure validates the public BBK1 framing without a
// BackupKey. Payload authentication still requires a keyed restore.
func VerifyEnvelopeStructure(src io.Reader) (EnvelopeInfo, error) {
	fixed := make([]byte, bbk1HeaderFixed)
	if _, err := io.ReadFull(src, fixed); err != nil {
		return EnvelopeInfo{}, ErrCorrupt
	}
	if string(fixed[:4]) != bbk1Magic {
		return EnvelopeInfo{}, ErrCorrupt
	}
	version := fixed[4]
	if version != bbk1VersionV1 && version != bbk1VersionV2 && version != bbk1VersionV3 {
		return EnvelopeInfo{}, ErrCorrupt
	}
	wrappedLength := int(binary.BigEndian.Uint16(fixed[5:7]))
	if wrappedLength == 0 || wrappedLength > wrappedDEKMax {
		return EnvelopeInfo{}, ErrCorrupt
	}
	if version == bbk1VersionV1 && wrappedLength > wrappedDEKV1Max {
		return EnvelopeInfo{}, ErrCorrupt
	}
	if version != bbk1VersionV1 && wrappedLength != wrappedDEKV2Len {
		return EnvelopeInfo{}, ErrCorrupt
	}
	wrapped := make([]byte, wrappedLength)
	if _, err := io.ReadFull(src, wrapped); err != nil {
		return EnvelopeInfo{}, ErrCorrupt
	}
	if _, err := io.ReadFull(src, make([]byte, 1)); err != nil {
		return EnvelopeInfo{}, ErrCorrupt
	}
	info := EnvelopeInfo{Version: int(version)}
	if version != bbk1VersionV1 {
		sender, err := ParsePublicWrapKey(wrapped[:x25519KeySize])
		if err != nil {
			return EnvelopeInfo{}, ErrCorrupt
		}
		info.EphemeralSenderPublicKey = sender
	}
	return info, nil
}

// SealDirectory writes an envelope-v2 encrypted tar stream. No plaintext
// archive is materialized.
func SealDirectory(ctx context.Context, key PublicWrapKey, sourceDir, dest string) error {
	return sealDirectory(ctx, key, sourceDir, dest, nil)
}

func sealDirectory(ctx context.Context, key PublicWrapKey, sourceDir, dest string, hooks *directoryArchiveHooks) error {
	if strings.TrimSpace(sourceDir) == "" {
		return errors.New("backup: source directory is required")
	}
	if strings.TrimSpace(dest) == "" {
		return errors.New("backup: envelope destination is required")
	}
	reader, writer := io.Pipe()
	tarDone := make(chan error, 1)
	go func() {
		err := writeDirectoryTarWithHooks(ctx, writer, sourceDir, hooks)
		_ = writer.CloseWithError(err)
		tarDone <- err
	}()
	sealErr := NewService(&singleFileBucket{path: dest}).BackupWithPublicKey(ctx, key, "local-set", reader)
	_ = reader.CloseWithError(sealErr)
	tarErr := <-tarDone
	if sealErr != nil {
		return sealErr
	}
	return tarErr
}

// OpenDirectory decrypts an envelope-v1/v2 tar stream into a new owner-only
// directory. Any authentication or extraction failure removes staged output.
func OpenDirectory(ctx context.Context, key BackupKey, envelopePath, destDir string) (EnvelopeInfo, error) {
	if strings.TrimSpace(envelopePath) == "" {
		return EnvelopeInfo{}, errors.New("backup: envelope path is required")
	}
	if strings.TrimSpace(destDir) == "" {
		return EnvelopeInfo{}, errors.New("backup: restore staging directory is required")
	}
	if _, err := os.Stat(destDir); err == nil {
		return EnvelopeInfo{}, errors.New("backup: restore staging directory already exists")
	} else if !os.IsNotExist(err) {
		return EnvelopeInfo{}, err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return EnvelopeInfo{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(destDir)
		}
	}()
	src, err := os.Open(envelopePath) //nolint:gosec // caller-selected backup input
	if err != nil {
		return EnvelopeInfo{}, err
	}
	defer src.Close()
	reader, writer := io.Pipe()
	type decryptResult struct {
		info EnvelopeInfo
		err  error
	}
	decryptDone := make(chan decryptResult, 1)
	go func() {
		info, decryptErr := InspectEnvelope(key, src, writer)
		_ = writer.CloseWithError(decryptErr)
		decryptDone <- decryptResult{info: info, err: decryptErr}
	}()
	extractErr := extractDirectoryTar(ctx, reader, destDir)
	_ = reader.CloseWithError(extractErr)
	decrypted := <-decryptDone
	if decrypted.err != nil {
		return EnvelopeInfo{}, decrypted.err
	}
	if extractErr != nil {
		return EnvelopeInfo{}, extractErr
	}
	if err := syncDirectory(destDir); err != nil {
		return EnvelopeInfo{}, err
	}
	cleanup = false
	return decrypted.info, nil
}

type singleFileBucket struct {
	path string
}

func (b *singleFileBucket) Put(ctx context.Context, _ string, src io.Reader) error {
	parent := filepath.Dir(b.path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".backup-envelope-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := copyContext(ctx, tmp, src); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, b.path); err != nil {
		return err
	}
	return syncDirectory(parent)
}

func (b *singleFileBucket) Get(context.Context, string) (io.ReadCloser, error) {
	return os.Open(b.path) //nolint:gosec // fixed caller-selected backup input
}

func (b *singleFileBucket) Delete(context.Context, string) error {
	return os.Remove(b.path)
}

func (b *singleFileBucket) List(context.Context, string) ([]string, error) {
	if _, err := os.Stat(b.path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return []string{filepath.Base(b.path)}, nil
}

func writeDirectoryTar(ctx context.Context, dst io.Writer, sourceDir string) error {
	return writeDirectoryTarWithHooks(ctx, dst, sourceDir, nil)
}

func writeDirectoryTarWithHooks(ctx context.Context, dst io.Writer, sourceDir string, hooks *directoryArchiveHooks) error {
	snapshot, err := discoverDirectoryArchive(ctx, sourceDir)
	if err != nil {
		return err
	}
	defer snapshot.close()
	hooks.reach(archiveStageDiscovered, "")
	if err := snapshot.prepare(ctx, hooks); err != nil {
		return err
	}

	tw := tar.NewWriter(dst)
	for index := range snapshot.entries {
		entry := &snapshot.entries[index]
		if err := ctx.Err(); err != nil {
			_ = tw.Close()
			return err
		}
		if entry.info.Mode().IsRegular() && entry.file == nil {
			if _, err := snapshot.prepareEntry(ctx, entry, hooks, false); err != nil {
				_ = tw.Close()
				return err
			}
		}
		if want, ok := snapshot.expected[entry.name]; ok && entry.digest != want.digest {
			_ = tw.Close()
			return fmt.Errorf("backup: archive entry does not match manifest: %s", entry.name)
		}
		header := &tar.Header{Name: filepath.ToSlash(entry.name), ModTime: entry.info.ModTime()}
		if entry.info.IsDir() {
			header.Typeflag = tar.TypeDir
			header.Mode = 0o700
		} else {
			header.Typeflag = tar.TypeReg
			header.Mode = 0o600
			header.Size = entry.info.Size()
		}
		if entry.info.IsDir() {
			if err := snapshot.validateEntry(*entry); err != nil {
				_ = tw.Close()
				return err
			}
		}
		if err := tw.WriteHeader(header); err != nil {
			_ = tw.Close()
			return err
		}
		if entry.info.IsDir() {
			continue
		}
		hooks.reach(archiveStageArchive, entry.name)
		if _, err := entry.file.Seek(0, io.SeekStart); err != nil {
			_ = tw.Close()
			return err
		}
		hash := sha256.New()
		written, copyErr := copyContext(ctx, io.MultiWriter(tw, hash), io.LimitReader(entry.file, entry.info.Size()+1))
		info, statErr := entry.file.Stat()
		closeErr := entry.file.Close()
		entry.file = nil
		if copyErr != nil {
			_ = tw.Close()
			return copyErr
		}
		if written != entry.info.Size() {
			_ = tw.Close()
			return errors.New("backup: file changed while archiving")
		}
		if statErr != nil {
			_ = tw.Close()
			return statErr
		}
		if !sameArchiveVersion(entry.info, info) {
			_ = tw.Close()
			return fmt.Errorf("backup: file changed while archiving: %s", entry.name)
		}
		if !bytes.Equal(entry.digest[:], hash.Sum(nil)) {
			_ = tw.Close()
			return fmt.Errorf("backup: file content changed while archiving: %s", entry.name)
		}
		if closeErr != nil {
			_ = tw.Close()
			return closeErr
		}
		if err := snapshot.validateEntry(*entry); err != nil {
			_ = tw.Close()
			return err
		}
	}
	return tw.Close()
}

func extractDirectoryTar(ctx context.Context, src io.Reader, destDir string) error {
	destRoot, err := os.OpenRoot(destDir)
	if err != nil {
		return err
	}
	defer func() { _ = destRoot.Close() }()
	tr := tar.NewReader(src)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		path, err := confinedArchivePath(destDir, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := destRoot.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := destRoot.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			file, err := destRoot.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			written, copyErr := copyContext(ctx, file, io.LimitReader(tr, header.Size))
			syncErr := file.Sync()
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if written != header.Size {
				return errors.New("backup: truncated archive entry")
			}
			if syncErr != nil {
				return syncErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("backup: unsupported archive entry type for %q", header.Name)
		}
	}
}

func confinedArchivePath(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", errors.New("backup: invalid archive path")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("backup: archive path escapes restore directory")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := resolveExistingArchivePath(filepath.Join(root, clean))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("backup: archive path escapes restore directory")
	}
	return clean, nil
}

func resolveExistingArchivePath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolvedParent, err := resolveExistingArchivePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 128*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			count, writeErr := dst.Write(buffer[:n])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if count != n {
				return written, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}
