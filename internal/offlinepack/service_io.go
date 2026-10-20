package offlinepack

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

type deviceRegistration struct {
	publicKey   []byte
	attestation []byte
}

func (s *Service) SetDeviceKeyAttestationVerifier(verifier DeviceKeyAttestationVerifier) {
	s.attester = verifier
}

func (s *Service) SetPackRoot(root string) {
	s.packRoot = root
}

// SetBuildByteBudget sets the maximum cumulative plaintext bytes in one pack.
// A zero budget restores the fail-closed default.
func (s *Service) SetBuildByteBudget(bytes uint64) {
	if bytes == 0 {
		bytes = DefaultBuildByteBudget
	}
	s.buildByteBudget = bytes
}

func (s *Service) RegisterDeviceKey(ctx context.Context, enrollmentID string, publicKey, attestation []byte) error {
	if s.attester == nil {
		return errors.New("offline pack attestation verifier is required")
	}
	if enrollmentID == "" || len(attestation) == 0 {
		return errors.New("offline pack enrollment and attestation are required")
	}
	if _, err := DeviceKID(publicKey); err != nil {
		return err
	}
	if err := s.attester.VerifyDeviceKey(ctx, enrollmentID, publicKey, attestation); err != nil {
		return err
	}
	s.deviceMu.Lock()
	s.devices[enrollmentID] = deviceRegistration{
		publicKey: append([]byte(nil), publicKey...), attestation: append([]byte(nil), attestation...),
	}
	s.deviceMu.Unlock()
	return nil
}

// RegisterResolver registers a TargetResolver for a target kind. Re-registering
// a kind replaces it. The kind string is the opaque target_kind stored on the
// pack row.
func (s *Service) RegisterResolver(kind string, resolver TargetResolver) {
	s.resolvers[kind] = resolver
}

// SetSealed wires the Sealed-Library predicate (ADR-0098 area 2). With it set,
// every pack build and serve-time re-check stamps the caller's Sealed Library
// IDs onto the store read filter, so a Sealed Library's Media is dropped at
// build time and a member sealed AFTER issue is denied at serve time. nil
// disables it (nothing sealed).
func (s *Service) SetSealed(sealed SealedPredicate) {
	s.sealed = sealed
}

// SetEncryptedContent wires encrypted Offline Pack byte reads to libencrypt's
// shared chunked reader, plus the resolver that binds each open to the file's
// immutable object identity. Sequential readahead remains here, at the pack
// consumer, rather than becoming policy in libencrypt.
func (s *Service) SetEncryptedContent(source EncryptedContentSource, encrypted EncryptedPredicate, resolve ObjectContextResolver) {
	s.content = source
	s.encrypted = encrypted
	s.resolveContext = resolve
}

const contentReadaheadSize = 256 * 1024

func (s *Service) openContent(libraryID store.LibraryID, path string) (io.ReadSeekCloser, error) {
	if s.content == nil || s.encrypted == nil || !s.encrypted(libraryID) {
		return os.Open(path) //nolint:gosec // authenticated store-owned media path
	}
	// Fail closed: an encrypted object without a resolvable immutable identity
	// is never served.
	if s.resolveContext == nil {
		return nil, errors.New("encrypted object context resolver is not wired")
	}
	objectClass, immutableObjectID, err := s.resolveContext(libraryID, path)
	if err != nil {
		return nil, err
	}
	reader, err := s.content.OpenDecryptReader(string(libraryID), path, objectClass, immutableObjectID)
	if err != nil {
		return nil, err
	}
	return newReadaheadReadSeekCloser(reader, contentReadaheadSize), nil
}

type readaheadReadSeekCloser struct {
	src io.ReadSeekCloser
	buf *bufio.Reader
}

func newReadaheadReadSeekCloser(src io.ReadSeekCloser, size int) *readaheadReadSeekCloser {
	return &readaheadReadSeekCloser{src: src, buf: bufio.NewReaderSize(src, size)}
}

func (r *readaheadReadSeekCloser) Read(p []byte) (int, error) { return r.buf.Read(p) }

func (r *readaheadReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekCurrent {
		offset -= int64(r.buf.Buffered())
	}
	position, err := r.src.Seek(offset, whence)
	if err == nil {
		r.buf.Reset(r.src)
	}
	return position, err
}

func (r *readaheadReadSeekCloser) Close() error { return r.src.Close() }

// filter converts a caller scope into the store read policy with the caller's
// currently-Sealed Library IDs stamped on (LibraryFilter.SealedIDs). It
// enumerates the caller's visible Libraries and marks the sealed ones, mirroring
// the Stations chokepoint. When no Sealed predicate is wired the base filter is
// returned unchanged (zero cost). The returned filter feeds every store read so
// the Sealed gate in store.libraryVisible drops sealed Media uniformly.
func (s *Service) filter(ctx context.Context, scope auth.Scope) (store.LibraryFilter, error) {
	base := scope.LibraryFilter()
	if s.sealed == nil {
		return base, nil
	}
	libraries, err := s.store.Libraries(ctx, base)
	if err != nil {
		return store.LibraryFilter{}, err
	}
	var sealedIDs []store.LibraryID
	for _, lib := range libraries {
		if s.sealed(lib.ID) {
			sealedIDs = append(sealedIDs, lib.ID)
		}
	}
	base.SealedIDs = sealedIDs
	return base, nil
}
