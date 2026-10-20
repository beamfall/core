package offlinepack

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type testDecryptSource struct{ key []byte }

type sizedStore struct {
	Store
	sizes map[string]int64
}

func (s sizedStore) MediaFilesForMedia(ctx context.Context, id store.MediaID, filter store.LibraryFilter) ([]store.MediaFile, error) {
	files, err := s.Store.MediaFilesForMedia(ctx, id, filter)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if size, ok := s.sizes[files[i].Path]; ok {
			files[i].SizeBytes = size
		}
	}
	return files, nil
}

type observingDecryptSource struct {
	bodies      map[string][]byte
	opens       int
	active      int
	maxActive   int
	maxReadSize int
}

func (s *observingDecryptSource) OpenDecryptReader(_ string, path string, _, _ string) (io.ReadSeekCloser, error) {
	body, ok := s.bodies[path]
	if !ok {
		return nil, errors.New("unknown observed decrypt path")
	}
	s.opens++
	s.active++
	s.maxActive = max(s.maxActive, s.active)
	return &observingReadSeekCloser{Reader: bytes.NewReader(body), source: s}, nil
}

type observingReadSeekCloser struct {
	*bytes.Reader
	source *observingDecryptSource
	closed bool
}

func (r *observingReadSeekCloser) Read(p []byte) (int, error) {
	r.source.maxReadSize = max(r.source.maxReadSize, len(p))
	return r.Reader.Read(p)
}

func (r *observingReadSeekCloser) Close() error {
	if !r.closed {
		r.source.active--
		r.closed = true
	}
	return nil
}

type countingReadSeekCloser struct {
	*bytes.Reader
	reads int
}

func (r *countingReadSeekCloser) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}

func (r *countingReadSeekCloser) Close() error { return nil }

func (s testDecryptSource) OpenDecryptReader(libraryID string, path string, objectClass, immutableObjectID string) (io.ReadSeekCloser, error) {
	f, err := os.Open(path) //nolint:gosec // test-owned fixture path
	if err != nil {
		return nil, err
	}
	obj := libencrypt.ObjectContext{
		LibraryID:         libraryID,
		ObjectClass:       libencrypt.ObjectClass(objectClass),
		ImmutableObjectID: immutableObjectID,
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	r, err := libencrypt.NewDecryptReaderV2(f, info.Size(), s.key, obj)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return struct {
		*libencrypt.DecryptReaderV2
		io.Closer
	}{DecryptReaderV2: r, Closer: f}, nil
}

type testAttester struct{}

func (testAttester) VerifyDeviceKey(context.Context, string, []byte, []byte) error { return nil }

type rejectingAttester struct{}

func (rejectingAttester) VerifyDeviceKey(context.Context, string, []byte, []byte) error {
	return errors.New("untrusted attestation")
}

type recordingAudit struct {
	records []AuditRecord
	err     error
}

func (a *recordingAudit) RecordAudit(_ context.Context, record AuditRecord) error {
	a.records = append(a.records, record)
	return a.err
}

func newFixture(t *testing.T) (*Service, *auth.Service, *store.Store, func() time.Time) {
	t.Helper()
	st := storetest.Open(t)
	for _, id := range []store.MediaID{"media-alpha", "media-archive"} {
		path := filepath.Join(t.TempDir(), string(id)+".bin")
		if err := os.WriteFile(path, []byte("bytes-"+string(id)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := st.SetPrimaryMediaFile(context.Background(), id, path); err != nil {
			t.Fatal(err)
		}
		mediaRows, err := st.VisibleMediaBatch(context.Background(), []store.MediaID{id}, store.LibraryFilter{AllLibraries: true, Revealed: true})
		if err != nil {
			t.Fatal(err)
		}
		media := mediaRows[id]
		files, err := st.MediaFilesForMedia(context.Background(), id, store.LibraryFilter{AllLibraries: true, Revealed: true})
		if err != nil || len(files) == 0 {
			t.Fatalf("MediaFilesForMedia: %v", err)
		}
		if _, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
			LibraryID: media.LibraryID, Path: path, Title: media.Title, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: files[0].ContentFingerprint,
		}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(1000, 0).UTC()
	clock := func() time.Time { return now }
	authService := auth.NewServiceWithClock(st, clock)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewAuthority("spki-sha256(test)", "offline-pack-test", privateKey, clock)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, authority, clock)
	svc.SetPackRoot(t.TempDir())
	svc.SetDeviceKeyAttestationVerifier(testAttester{})
	deviceKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RegisterDeviceKey(context.Background(), "device-tv", deviceKey.PublicKey().Bytes(), []byte("attested")); err != nil {
		t.Fatal(err)
	}
	return svc, authService, st, clock
}

func elevateOfflineSession(t *testing.T, st *store.Store, clock func() time.Time) {
	t.Helper()
	if _, err := st.SetDeviceRole(context.Background(), "device-tv", store.DeviceRoleManager); err != nil {
		t.Fatalf("SetDeviceRole: %v", err)
	}
	if err := st.SetSessionAdminUntil(context.Background(), "session-default", clock().Add(time.Hour)); err != nil {
		t.Fatalf("SetSessionAdminUntil: %v", err)
	}
}

func scopeFor(t *testing.T, a *auth.Service, sessionID store.SessionID) auth.Scope {
	t.Helper()
	scope, err := a.Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Scope: %v", err)
	}
	return scope
}

func TestBuildExplicitMediaPack(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")

	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if pack.State != store.OfflinePackReady || pack.ItemCount != 1 {
		t.Fatalf("pack = %+v, want ready 1-item", pack)
	}
}

func TestBuildPackRecordsByteProgress(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	wantBytes := int64(len("bytes-media-alpha"))
	scope := scopeFor(t, authService, "session-default")

	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if pack.BytesReady != wantBytes || pack.BytesTotal != wantBytes {
		t.Fatalf("progress ready/total = %d/%d, want %d", pack.BytesReady, pack.BytesTotal, wantBytes)
	}
}

func TestUint64ToInt64RejectsOverflow(t *testing.T) {
	got, err := uint64ToInt64(1<<63 - 1)
	if err != nil || got != 1<<63-1 {
		t.Fatalf("uint64ToInt64(MaxInt64) = %d, %v", got, err)
	}
	if _, err := uint64ToInt64(1 << 63); err == nil {
		t.Fatal("uint64ToInt64(MaxInt64 + 1) succeeded")
	}
}

func TestBuildPackRejectsResourceBudgetsBeforeDecrypt(t *testing.T) {
	t.Run("item count", func(t *testing.T) {
		svc, authService, _, _ := newFixture(t)
		source := &observingDecryptSource{bodies: map[string][]byte{}}
		svc.SetEncryptedContent(source, func(store.LibraryID) bool { return true }, func(store.LibraryID, string) (string, string, error) {
			return "media", "item", nil
		})
		ids := make([]store.MediaID, MaxManifestItems+1)
		for i := range ids {
			ids[i] = store.MediaID(fmt.Sprintf("media-%d", i))
		}
		svc.RegisterResolver("over-count", resolverFunc(func(context.Context, string, store.LibraryFilter) (ResolvedTarget, error) {
			return ResolvedTarget{MediaIDs: ids}, nil
		}))

		_, err := svc.BuildPack(context.Background(), scopeFor(t, authService, "session-default"), BuildPackRequest{TargetKind: "over-count"})
		if err == nil || !strings.Contains(err.Error(), "item bound") {
			t.Fatalf("BuildPack error = %v, want item bound", err)
		}
		if source.opens != 0 {
			t.Fatalf("decrypt opens = %d, want 0", source.opens)
		}
	})

	t.Run("cumulative bytes", func(t *testing.T) {
		svc, authService, st, _ := newFixture(t)
		scope := scopeFor(t, authService, "session-revealed")
		files := fixtureMediaFiles(t, st, scope, "media-alpha", "media-archive")
		sizes := map[string]int64{}
		for _, file := range files {
			sizes[file.Path] = 10
		}
		svc.store = sizedStore{Store: st, sizes: sizes}
		svc.SetBuildByteBudget(19)
		source := &observingDecryptSource{bodies: map[string][]byte{}}
		svc.SetEncryptedContent(source, func(store.LibraryID) bool { return true }, func(store.LibraryID, string) (string, string, error) {
			return "media", "item", nil
		})

		_, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha,media-archive"})
		if err == nil || !strings.Contains(err.Error(), "cumulative byte budget") {
			t.Fatalf("BuildPack error = %v, want cumulative byte budget", err)
		}
		if source.opens != 0 {
			t.Fatalf("decrypt opens = %d, want 0", source.opens)
		}
	})
}

func TestBuildPackStreamsOneBoundedSourceAtATime(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-revealed")
	files := fixtureMediaFiles(t, st, scope, "media-alpha", "media-archive")
	sizes := map[string]int64{}
	bodies := map[string][]byte{}
	for _, file := range files {
		body := bytes.Repeat([]byte(file.MediaID), 100000)
		bodies[file.Path] = body
		sizes[file.Path] = int64(len(body))
	}
	svc.store = sizedStore{Store: st, sizes: sizes}
	source := &observingDecryptSource{bodies: bodies}
	svc.SetEncryptedContent(source, func(store.LibraryID) bool { return true }, func(store.LibraryID, string) (string, string, error) {
		return "media", "item", nil
	})

	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha,media-archive"})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if pack.ItemCount != 2 {
		t.Fatalf("item count = %d, want 2", pack.ItemCount)
	}
	if source.opens != 4 {
		t.Fatalf("decrypt opens = %d, want two bounded passes for each item", source.opens)
	}
	if source.maxActive != 1 {
		t.Fatalf("simultaneous plaintext sources = %d, want 1", source.maxActive)
	}
	if source.maxReadSize > int(BFOPChunkSize) {
		t.Fatalf("largest plaintext read = %d, exceeds BFOP chunk bound %d", source.maxReadSize, BFOPChunkSize)
	}
}

func fixtureMediaFiles(t *testing.T, st *store.Store, scope auth.Scope, ids ...store.MediaID) []store.MediaFile {
	t.Helper()
	files := make([]store.MediaFile, 0, len(ids))
	for _, id := range ids {
		file, err := st.PrimaryMediaFile(context.Background(), id, scope.PlaybackFilter())
		if err != nil {
			t.Fatalf("PrimaryMediaFile(%s): %v", id, err)
		}
		files = append(files, file)
	}
	return files
}

func TestBuildJobPackagesExplicitMediaPack(t *testing.T) {
	svc, authService, st, clock := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	payload, err := BuildJobPayloadFor(scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildJobPayloadFor: %v", err)
	}
	if _, err := st.EnqueueJob(context.Background(), store.Job{
		ID:        "job-offline-pack-build",
		Kind:      BuildJobKind,
		Class:     store.JobClassBackground,
		Payload:   payload,
		CreatedAt: clock(),
	}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	registry := jobs.NewRegistry(st, "offline-pack-test")
	registry.Register(BuildJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		_, err := svc.HandleBuildJob(ctx, job.Payload)
		return jobs.Result{}, err
	})

	if _, err := registry.RunOne(context.Background(), nil, clock()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	packs, err := st.OfflinePacksForProfile(context.Background(), scope.ProfileID)
	if err != nil {
		t.Fatalf("OfflinePacksForProfile: %v", err)
	}
	if len(packs) != 1 || packs[0].State != store.OfflinePackReady || packs[0].ItemCount != 1 {
		t.Fatalf("packs = %+v, want one ready 1-item pack", packs)
	}
}

func TestBuildExcludesRestrictedMediaWhenUnrevealed(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	// session-default is NOT revealed; media-archive is in the restricted
	// library, so a pack targeting both keeps only the visible one.
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha,media-archive",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if pack.ItemCount != 1 {
		t.Fatalf("itemCount = %d, want 1 (restricted media excluded)", pack.ItemCount)
	}
	// A pack of only-restricted media for an unrevealed session resolves empty.
	if _, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-archive",
	}); err != ErrEmptyTarget {
		t.Fatalf("only-restricted build err = %v, want ErrEmptyTarget", err)
	}
}

func TestNoRestrictedContentSerializedOutsideRevealedPack(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	hidden := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), hidden, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha,media-archive",
	})
	if err != nil {
		t.Fatalf("BuildPack hidden: %v", err)
	}
	hiddenManifest := mustManifestClaims(t, svc, pack.Manifest)
	if len(hiddenManifest.Items) != 1 || hiddenManifest.Items[0].MediaID != "media-alpha" {
		t.Fatalf("hidden manifest items = %+v", hiddenManifest.Items)
	}

	revealed := scopeFor(t, authService, "session-revealed")
	revealedPack, err := svc.BuildPack(context.Background(), revealed, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-archive",
	})
	if err != nil {
		t.Fatalf("BuildPack revealed: %v", err)
	}
	revealedManifest := mustManifestClaims(t, svc, revealedPack.Manifest)
	if len(revealedManifest.Items) != 1 || revealedManifest.Items[0].MediaID != "media-archive" {
		t.Fatalf("revealed manifest items = %+v", revealedManifest.Items)
	}
}

func mustManifestClaims(t *testing.T, svc *Service, token string) ManifestClaims {
	t.Helper()
	var claims ManifestClaims
	if _, err := verifyCompact(token, ManifestType, svc.authority.PublicKeys(), &claims, manifestClaimKeys); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestBuildUnknownTargetKind(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	if _, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: "nope", TargetRef: "x"}); err == nil {
		t.Fatal("unknown target kind must error")
	}
}

func TestPackDirRejectsTraversal(t *testing.T) {
	svc := &Service{packRoot: t.TempDir()}
	for _, id := range []store.OfflinePackID{"", ".", "..", "../outside", "nested/pack"} {
		if _, err := svc.packDir(id); err == nil {
			t.Fatalf("packDir(%q) accepted", id)
		}
	}
}

func TestBuildRejectsMissingOrUntrustedDeviceAttestation(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	svc.devices = nil
	if _, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
	}); err == nil {
		t.Fatal("missing enrolled key and attestation accepted")
	}
	deviceKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetDeviceKeyAttestationVerifier(rejectingAttester{})
	if _, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
		DevicePublicKey: deviceKey.PublicKey().Bytes(), DeviceAttestation: []byte("untrusted"),
	}); err == nil {
		t.Fatal("untrusted attestation accepted")
	}
}

func TestPackLifecycleHelpers(t *testing.T) {
	svc, authService, _, clock := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	svc.RegisterResolver("custom", resolverFunc(func(context.Context, string, store.LibraryFilter) (ResolvedTarget, error) {
		return ResolvedTarget{MediaIDs: []store.MediaID{"media-alpha"}, Label: "Custom"}, nil
	}))
	expiredAt := clock().Add(time.Second)

	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: "custom",
		TargetRef:  "ignored",
		ExpiresAt:  &expiredAt,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if _, err := svc.SyncProgress(context.Background(), pack.ID, scope.ProfileID, 10, 5); err != nil {
		t.Fatalf("SyncProgress: %v", err)
	}
	got, err := svc.Pack(context.Background(), pack.ID, scope.ProfileID)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if got.BytesReady != 10 || got.BytesTotal != 10 {
		t.Fatalf("progress = %d/%d, want 10/10", got.BytesReady, got.BytesTotal)
	}
	packs, err := svc.Packs(context.Background(), scope.ProfileID)
	if err != nil {
		t.Fatalf("Packs: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("Packs len = %d, want 1", len(packs))
	}
	svc.now = func() time.Time { return clock().Add(2 * time.Second) }
	if expired, err := svc.HandleExpireJob(context.Background()); err != nil || expired != 1 {
		t.Fatalf("HandleExpireJob = %d, %v; want 1, nil", expired, err)
	}
}

func TestServingDeniesAfterRevoke(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")

	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}

	handlers := NewHTTPHandlers(svc, authService)

	// Before revoke: the token verifies AND the manifest serves (200).
	rec := serveManifest(t, handlers, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-revoke status = %d, want 200", rec.Code)
	}

	if _, err := svc.Revoke(context.Background(), pack.ID, scope.ProfileID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// ...but the serving layer denies it (410 Gone), proving denial is enforced
	// at the serving layer, not just at token verify.
	rec = serveManifest(t, handlers, token)
	if rec.Code != http.StatusGone {
		t.Fatalf("post-revoke status = %d, want 410", rec.Code)
	}
}

func TestManifestRouteValidatesGrantSignatureOnce(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatal(err)
	}
	validations := 0
	svc.authority.verify = func(key ed25519.PublicKey, message, signature []byte) bool {
		validations++
		return ed25519.Verify(key, message, signature)
	}
	if rec := serveManifest(t, NewHTTPHandlers(svc, authService), token); rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200", rec.Code)
	}
	if validations != 1 {
		t.Fatalf("manifest signature validations = %d, want 1", validations)
	}
}

// TestContentRouteDeniesAfterRevoke mirrors TestServingDeniesAfterRevoke for
// the Content route: the byte-serving routes share the same authorize/
// authorizeToken checks as Manifest, but had no direct regression test
// proving the content route itself denies (and serves zero pack bytes) once
// the pack is revoked, rather than relying on Manifest's coverage alone.
func TestContentRouteDeniesAfterRevoke(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")

	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	handlers := NewHTTPHandlers(svc, authService)

	// Before revoke: the content route serves the pack member's BFOP bytes.
	rec := serveContent(t, handlers, token, "media-alpha")
	if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("BFOP")) {
		t.Fatalf("pre-revoke status/body = %d/%q, want 200/BFOP-prefixed", rec.Code, rec.Body.Bytes()[:min(4, rec.Body.Len())])
	}

	if _, err := svc.Revoke(context.Background(), pack.ID, scope.ProfileID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// After revoke: denied, and zero pack bytes are served — the response is an
	// http.Error message, never BFOP-prefixed pack ciphertext.
	rec = serveContent(t, handlers, token, "media-alpha")
	if rec.Code != http.StatusGone {
		t.Fatalf("post-revoke status = %d, want 410", rec.Code)
	}
	if bytes.HasPrefix(rec.Body.Bytes(), []byte("BFOP")) {
		t.Fatalf("post-revoke content route served pack bytes: %q", rec.Body.Bytes())
	}
}

// TestKeyRouteDeniesAfterRevoke mirrors TestServingDeniesAfterRevoke for the
// Key route: proves the wrapped-CEK route denies (and serves zero key bytes)
// once the pack is revoked, not just that the manifest route does.
func TestKeyRouteDeniesAfterRevoke(t *testing.T) {
	svc, authService, st, clock := newFixture(t)
	elevateOfflineSession(t, st, clock)
	scope := scopeFor(t, authService, "session-default")

	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	handlers := NewHTTPHandlers(svc, authService)

	// Before revoke: the key route serves the wrapped CEK.
	rec := serveKey(t, handlers, token)
	if rec.Code != http.StatusOK || rec.Body.Len() != WrappedEnvelopeSize {
		t.Fatalf("pre-revoke status/len = %d/%d, want 200/%d", rec.Code, rec.Body.Len(), WrappedEnvelopeSize)
	}

	if _, err := svc.Revoke(context.Background(), pack.ID, scope.ProfileID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// After revoke: denied, and zero key bytes are served — the response is an
	// http.Error message, never a WrappedEnvelopeSize-length key blob.
	rec = serveKey(t, handlers, token)
	if rec.Code != http.StatusGone {
		t.Fatalf("post-revoke status = %d, want 410", rec.Code)
	}
	if rec.Body.Len() == WrappedEnvelopeSize {
		t.Fatalf("post-revoke key route served a full-length wrapped-CEK body")
	}
}

func TestIssueRefreshSequenceManifestAndStatusBindings(t *testing.T) {
	svc, authService, _, clock := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected := GrantExpectations{
		Issuer: svc.authority.Issuer(), PackID: string(pack.ID), ProfileID: string(scope.ProfileID),
		LibraryIDs: []string{"library-local"}, DeviceEnrollmentID: string(scope.DeviceID),
		DeviceKeyThumbprint: pack.DeviceKeyThumbprint, ManifestRevision: pack.ManifestRevision,
		ManifestSHA256: pack.ManifestSHA256, Rev: packRevocations(pack), TrustedNow: clock(),
	}
	firstClaims, _, _, err := VerifyGrant(first, svc.authority.PublicKeys(), expected)
	if err != nil {
		t.Fatal(err)
	}
	secondClaims, _, _, err := VerifyGrant(second, svc.authority.PublicKeys(), expected)
	if err != nil {
		t.Fatal(err)
	}
	if secondClaims.Seq != firstClaims.Seq+1 || secondClaims.JTI == firstClaims.JTI {
		t.Fatalf("refresh seq/jti = %d/%q after %d/%q", secondClaims.Seq, secondClaims.JTI, firstClaims.Seq, firstClaims.JTI)
	}
	manifest, err := VerifyManifest(pack.Manifest, svc.authority.PublicKeys(), secondClaims)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Items) != 1 || manifest.Items[0].MediaID != "media-alpha" ||
		manifest.Items[0].SelectedVersion != "" || manifest.Wrap.DeviceKID != secondClaims.DeviceKeyThumbprint {
		t.Fatalf("manifest bindings = %+v", manifest)
	}
	statusToken, err := svc.IssueStatus(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, decision, err := VerifyStatus(statusToken, svc.authority.PublicKeys(), secondClaims)
	if err != nil || decision.Destroy || status.State != "ready" {
		t.Fatalf("status = %+v decision=%+v err=%v", status, decision, err)
	}
}

// TestManifestSelectedVersionBindingNonEmpty covers the non-empty SelectedVersion
// case of the manifest binding (the only prior coverage was the empty-label
// default in TestIssueRefreshSequenceManifestAndStatusBindings). A pack built
// against an explicit, non-"" Version selection carries that label into the
// manifest item, and a grant is bound to exactly that manifest's digest: it
// verifies against the matching (non-empty-SelectedVersion) manifest.
//
// The negative case isolates the SelectedVersion axis specifically: it takes
// the verified manifest's own claims, changes ONLY the item's SelectedVersion
// (PackID and every other field held byte-identical to the grant), and
// re-signs. An earlier version of this test compared against a manifest from
// a *separately built* sibling pack, which carries its own PackID — that
// confounded the negative case, since VerifyManifest rejects on the PackID
// mismatch before the SelectedVersion binding is ever exercised. Here PackID
// (and everything else) matches the grant in both the accepted and rejected
// cases, so the rejection can only be caused by the SelectedVersion-driven
// ManifestSHA256 change.
func TestManifestSelectedVersionBindingNonEmpty(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")

	// Add a second, explicitly-labelled Version of media-alpha alongside the
	// default ("" label) Version newFixture already created.
	altPath := filepath.Join(t.TempDir(), "media-alpha-hq.bin")
	if err := os.WriteFile(altPath, []byte("hq-bytes-media-alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaFileVersion(context.Background(), "media-alpha", store.MediaFile{
		Path: altPath, ContentFingerprint: store.HashMediaFilePath(altPath), VersionLabel: "hq",
	}, nil); err != nil {
		t.Fatalf("AddMediaFileVersion: %v", err)
	}

	hqPack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
		SelectedVersions: map[store.MediaID]string{"media-alpha": "hq"},
	})
	if err != nil {
		t.Fatalf("BuildPack (hq): %v", err)
	}

	hqToken, err := svc.IssuePackGrant(context.Background(), scope, hqPack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	hqClaims, _, _, err := VerifyGrant(hqToken, svc.authority.PublicKeys(), GrantExpectations{
		Issuer: svc.authority.Issuer(), PackID: string(hqPack.ID), ProfileID: string(scope.ProfileID),
		LibraryIDs: []string{"library-local"}, DeviceEnrollmentID: string(scope.DeviceID),
		DeviceKeyThumbprint: hqPack.DeviceKeyThumbprint, ManifestRevision: hqPack.ManifestRevision,
		ManifestSHA256: hqPack.ManifestSHA256, Rev: packRevocations(hqPack), TrustedNow: svc.now(),
	})
	if err != nil {
		t.Fatalf("VerifyGrant: %v", err)
	}

	// Matching: the grant's bound manifest carries the non-empty SelectedVersion
	// and verifies.
	manifest, err := VerifyManifest(hqPack.Manifest, svc.authority.PublicKeys(), hqClaims)
	if err != nil {
		t.Fatalf("VerifyManifest (matching): %v", err)
	}
	if len(manifest.Items) != 1 || manifest.Items[0].SelectedVersion != "hq" {
		t.Fatalf("manifest = %+v, want one item with SelectedVersion=hq", manifest)
	}
	if manifest.PackID != hqClaims.PackID {
		t.Fatalf("test setup: manifest PackID %q != grant PackID %q", manifest.PackID, hqClaims.PackID)
	}

	// withSelectedVersion recomputes the manifest's ManifestSHA256 binding
	// with the item's SelectedVersion changed and everything else (PackID
	// included) held fixed, then re-signs a manifest carrying that mutated
	// item and digest.
	withSelectedVersion := func(selectedVersion string) (token string, digest string) {
		items := append([]ManifestItem(nil), manifest.Items...)
		items[0].SelectedVersion = selectedVersion
		binding := ManifestBinding{
			V: 1, Iss: manifest.Iss, Aud: manifest.Aud, PackID: manifest.PackID,
			ProfileID: manifest.ProfileID, LibraryIDs: manifest.LibraryIDs,
			DeviceEnrollmentID: manifest.DeviceEnrollmentID, DeviceKeyThumbprint: manifest.DeviceKeyThumbprint,
			ManifestRevision: manifest.ManifestRevision, PackExpiresAt: manifest.PackExpiresAt,
			Items: items, WrapSuite: manifest.Wrap.Suite,
		}
		digest, err := ManifestSHA256(binding)
		if err != nil {
			t.Fatalf("ManifestSHA256: %v", err)
		}
		claims := manifest
		claims.Items = items
		claims.ManifestSHA256 = digest
		token, err = svc.authority.SignManifest(claims)
		if err != nil {
			t.Fatalf("SignManifest: %v", err)
		}
		return token, digest
	}

	// Prove the isolation: reverting the mutation (SelectedVersion back to
	// "hq") reproduces the pack's own digest and verifies cleanly against the
	// same hq grant — the digest/binding check, not the (unchanged) PackID,
	// is what will reject the mutated case below.
	revertedToken, revertedDigest := withSelectedVersion("hq")
	if revertedDigest != manifest.ManifestSHA256 {
		t.Fatalf("reverted digest = %s, want %s (must reproduce the pack's own ManifestSHA256)", revertedDigest, manifest.ManifestSHA256)
	}
	if _, err := VerifyManifest(revertedToken, svc.authority.PublicKeys(), hqClaims); err != nil {
		t.Fatalf("VerifyManifest (reverted, sanity check): %v", err)
	}

	// The SelectedVersion-only mutation: same PackID, same everything else,
	// only the item's SelectedVersion (and the digest it feeds) differs.
	mutatedToken, mutatedDigest := withSelectedVersion("standard")
	if mutatedDigest == manifest.ManifestSHA256 {
		t.Fatal("SelectedVersion-only mutation left ManifestSHA256 unchanged — digest does not bind SelectedVersion")
	}
	if _, err := VerifyManifest(mutatedToken, svc.authority.PublicKeys(), hqClaims); err == nil {
		t.Fatal("accepted manifest whose only difference from the grant-bound manifest is SelectedVersion")
	}
}

func TestContentRouteServesPackMemberBytes(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	handlers := NewHTTPHandlers(svc, authService)
	req := httptest.NewRequest(http.MethodGet, "/api/pack/content/"+token+"/media-alpha", nil)
	authenticateOfflineRequest(req)
	rec := httptest.NewRecorder()
	handlers.Content(rec, req)
	if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("BFOP")) {
		t.Fatalf("content status/header = %d %q, want 200/BFOP", rec.Code, rec.Body.Bytes()[:min(4, rec.Body.Len())])
	}
}

func TestContentRouteStreamsEncryptedPackMemberAcrossChunkBoundary(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	file, err := st.PrimaryMediaFile(context.Background(), "media-alpha", scope.PlaybackFilter())
	if err != nil {
		t.Fatalf("PrimaryMediaFile: %v", err)
	}
	plain := bytes.Repeat([]byte("chunked-offline-pack-"), 9000)
	key, err := libencrypt.NewRandomKey()
	if err != nil {
		t.Fatalf("NewRandomKey: %v", err)
	}
	defer libencrypt.ZeroKey(key)
	// The pack member's immutable identity: its persisted content fingerprint,
	// never its path.
	obj := libencrypt.MediaObjectContext("library-local", "sha256:offline-pack-member")
	var ciphertext bytes.Buffer
	if err := libencrypt.EncryptStreamV2(&ciphertext, bytes.NewReader(plain), key, obj); err != nil {
		t.Fatalf("EncryptStreamV2: %v", err)
	}
	if err := os.WriteFile(file.Path, ciphertext.Bytes(), 0o600); err != nil {
		t.Fatalf("write ciphertext: %v", err)
	}
	contentDir := filepath.Dir(file.Path)
	before, err := os.ReadDir(contentDir)
	if err != nil {
		t.Fatalf("read encrypted content dir before serving: %v", err)
	}
	var resolved []string
	svc.SetEncryptedContent(testDecryptSource{key: key}, func(id store.LibraryID) bool {
		return id == "library-local"
	}, func(id store.LibraryID, path string) (string, string, error) {
		if id != "library-local" || path != file.Path {
			return "", "", errors.New("unknown encrypted object")
		}
		resolved = append(resolved, obj.ImmutableObjectID)
		return string(obj.ObjectClass), obj.ImmutableObjectID, nil
	})
	svc.store = sizedStore{Store: st, sizes: map[string]int64{file.Path: int64(len(plain))}}
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	handlers := NewHTTPHandlers(svc, authService)
	req := httptest.NewRequest(http.MethodGet, "/api/pack/content/"+token+"/media-alpha", nil)
	authenticateOfflineRequest(req)
	const start, end = 65520, 65620
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	rec := httptest.NewRecorder()
	handlers.Content(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != end-start+1 {
		t.Fatalf("encrypted range length = %d, want %d", rec.Body.Len(), end-start+1)
	}
	after, err := os.ReadDir(contentDir)
	if err != nil {
		t.Fatalf("read encrypted content dir after serving: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("content route materialized a file: entries before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if before[i].Name() != after[i].Name() {
			t.Fatalf("content route changed on-disk entries: before=%q after=%q", before[i].Name(), after[i].Name())
		}
		if after[i].IsDir() {
			continue
		}
		onDisk, err := os.ReadFile(filepath.Join(contentDir, after[i].Name())) //nolint:gosec // test-owned fixture path
		if err != nil {
			t.Fatalf("read content-dir file %q: %v", after[i].Name(), err)
		}
		if bytes.Equal(onDisk, plain) {
			t.Fatalf("content route persisted full plaintext as %q", after[i].Name())
		}
	}
}

func TestEncryptedContentReadaheadIsConsumerOwned(t *testing.T) {
	src := &countingReadSeekCloser{Reader: bytes.NewReader([]byte("abcdefghijklmnop"))}
	r := newReadaheadReadSeekCloser(src, 8)
	for i := 0; i < 4; i++ {
		var one [1]byte
		if _, err := io.ReadFull(r, one[:]); err != nil {
			t.Fatalf("ReadFull: %v", err)
		}
	}
	if src.reads != 1 {
		t.Fatalf("underlying reads = %d, want 1 consumer readahead", src.reads)
	}
	if _, err := r.Seek(-2, io.SeekCurrent); err != nil {
		t.Fatalf("SeekCurrent: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "cdefghijklmnop" {
		t.Fatalf("post-seek bytes = %q", got)
	}
}

func TestKeyRouteServesBFKWAfterGrantCheck(t *testing.T) {
	svc, authService, st, clock := newFixture(t)
	elevateOfflineSession(t, st, clock)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	audit := &recordingAudit{}
	handlers := NewHTTPHandlers(svc, authService)
	handlers.SetAuditRecorder(audit)
	req := httptest.NewRequest(http.MethodGet, "/api/pack/key/"+token, nil)
	authenticateOfflineRequest(req)
	rec := httptest.NewRecorder()
	handlers.Key(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != WrappedEnvelopeSize {
		t.Fatalf("key response = %d/%d bytes, want 200/%d", rec.Code, rec.Body.Len(), WrappedEnvelopeSize)
	}
	if len(audit.records) != 1 {
		t.Fatalf("key audit records = %+v, want one", audit.records)
	}
	want := AuditRecord{
		Actor: AuditActor{
			Authenticated: true,
			SessionID:     "session-default",
			DeviceID:      "device-tv",
			ProfileID:     "profile-default",
		},
		Operation: keyAuditOperation,
		Outcome:   AuditSuccess,
	}
	if audit.records[0] != want {
		t.Fatalf("key audit record = %+v, want %+v", audit.records[0], want)
	}
}

func TestKeyRouteRequiresAdminSession(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	audit := &recordingAudit{}
	handlers := NewHTTPHandlers(svc, authService)
	handlers.SetAuditRecorder(audit)

	rec := serveKey(t, handlers, token)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin key response = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == WrappedEnvelopeSize {
		t.Fatal("non-admin key response served a full wrapped CEK")
	}
	if len(audit.records) != 1 || audit.records[0].Outcome != AuditRefused {
		t.Fatalf("non-admin key audit records = %+v, want one refusal", audit.records)
	}
}

func TestKeyRouteFailsClosedWhenAuditUnavailable(t *testing.T) {
	svc, authService, st, clock := newFixture(t)
	elevateOfflineSession(t, st, clock)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	handlers := NewHTTPHandlers(svc, authService)
	handlers.SetAuditRecorder(&recordingAudit{err: errors.New("injected audit failure")})

	rec := serveKey(t, handlers, token)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("audit-failed key response = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == WrappedEnvelopeSize {
		t.Fatal("audit-failed key response served a full wrapped CEK")
	}
}

// TestBuildExcludesSealedMembers pins the MUSIC-6 BLOCKER fix: when a Library is
// Sealed, its Media must never be frozen into a pack manifest — even for a
// SEALED-but-UNRESTRICTED Library and a fully allowed, revealed session.
// library-local is unrestricted; sealing it must make a media-alpha pack resolve
// empty, and must not affect packs in other (unsealed) Libraries.
func TestBuildExcludesSealedMembers(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")

	// Baseline (unsealed): the pack builds with the member present.
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
	})
	if err != nil || pack.ItemCount != 1 {
		t.Fatalf("baseline BuildPack = %+v err=%v, want 1-item", pack, err)
	}

	// Seal library-local; a pack targeting its only member must resolve empty.
	svc.SetSealed(func(id store.LibraryID) bool { return id == "library-local" })
	if _, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
	}); err != ErrEmptyTarget {
		t.Fatalf("sealed build err = %v, want ErrEmptyTarget (sealed member leaked into pack)", err)
	}
}

// TestServingDeniesAfterSealEvenWhenTokenVerifies pins the seal-AFTER-issue
// denial: a pack built + granted while unsealed must be denied at serve time once
// its Library is sealed, even though the signed token still verifies cleanly.
func TestServingDeniesAfterSealEvenWhenTokenVerifies(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")

	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	handlers := NewHTTPHandlers(svc, authService)

	// Pre-seal: serves 200.
	if rec := serveManifest(t, handlers, token); rec.Code != http.StatusOK {
		t.Fatalf("pre-seal status = %d, want 200", rec.Code)
	}

	// Seal the member's Library AFTER the grant was minted.
	svc.SetSealed(func(id store.LibraryID) bool { return id == "library-local" })

	if rec := serveManifest(t, handlers, token); rec.Code == http.StatusOK {
		t.Fatalf("post-seal status = 200, want denial (sealed member served)")
	}
}

func serveManifest(t *testing.T, h *HTTPHandlers, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/pack/manifest/"+token, nil)
	authenticateOfflineRequest(req)
	rec := httptest.NewRecorder()
	h.Manifest(rec, req)
	return rec
}

func serveContent(t *testing.T, h *HTTPHandlers, token string, mediaID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/pack/content/"+token+"/"+mediaID, nil)
	authenticateOfflineRequest(req)
	rec := httptest.NewRecorder()
	h.Content(rec, req)
	return rec
}

func serveKey(t *testing.T, h *HTTPHandlers, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/pack/key/"+token, nil)
	authenticateOfflineRequest(req)
	rec := httptest.NewRecorder()
	h.Key(rec, req)
	return rec
}

func authenticateOfflineRequest(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

type resolverFunc func(context.Context, string, store.LibraryFilter) (ResolvedTarget, error)

func (f resolverFunc) Resolve(ctx context.Context, ref string, filter store.LibraryFilter) (ResolvedTarget, error) {
	return f(ctx, ref, filter)
}

// TestOpenContentFailsClosedWithoutObjectIdentity: an encrypted library with
// no wired resolver, or a resolver that cannot produce a persisted identity,
// never serves bytes (TECHDUR-CORE-CRYPTO-1 fail-closed identity binding).
func TestOpenContentFailsClosedWithoutObjectIdentity(t *testing.T) {
	svc, _, _, _ := newFixture(t)
	encrypted := func(id store.LibraryID) bool { return true }

	// No resolver wired at all.
	svc.SetEncryptedContent(testDecryptSource{}, encrypted, nil)
	if _, err := svc.openContent("library-local", "/nonexistent"); err == nil {
		t.Fatal("encrypted open without a resolver must fail closed")
	}

	// Resolver cannot produce an identity.
	svc.SetEncryptedContent(testDecryptSource{}, encrypted, func(id store.LibraryID, path string) (string, string, error) {
		return "", "", errors.New("no persisted identity")
	})
	if _, err := svc.openContent("library-local", "/nonexistent"); err == nil {
		t.Fatal("encrypted open without a resolvable identity must fail closed")
	}
}
