package offlinepack

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// builtPackMember builds and grants a one-member pack and returns the handlers,
// grant token and the member's stored ciphertext path.
func builtPackMember(t *testing.T) (*HTTPHandlers, string, string) {
	t.Helper()
	svc, authService, st, _ := newFixture(t)
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
	items, err := st.OfflinePackItems(context.Background(), pack.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("OfflinePackItems = %d items, %v; want one", len(items), err)
	}
	return NewHTTPHandlers(svc, authService), token, items[0].CiphertextPath
}

func TestContentRouteRefusesSymlinkedPackMember(t *testing.T) {
	handlers, token, member := builtPackMember(t)
	secret := []byte("outside-the-pack-root")
	outside := filepath.Join(t.TempDir(), "outside.bfop")
	if err := os.WriteFile(outside, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(member); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, member); err != nil {
		t.Fatal(err)
	}

	rec := serveContent(t, handlers, token, "media-alpha")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("symlinked member status = %d, want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), secret) {
		t.Fatal("content route served bytes through a symlinked pack member")
	}
}

func TestContentRouteRefusesSymlinkedPackDirectory(t *testing.T) {
	handlers, token, member := builtPackMember(t)
	packDir := filepath.Dir(member)
	secret := []byte("outside-pack-directory")
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, filepath.Base(member)), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(packDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, packDir); err != nil {
		t.Fatal(err)
	}

	rec := serveContent(t, handlers, token, "media-alpha")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("symlinked pack directory status = %d, want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), secret) {
		t.Fatal("content route served bytes through a symlinked pack directory")
	}
}

func TestOpenPackMemberRefusesPathOutsidePackDirectory(t *testing.T) {
	handlers, _, member := builtPackMember(t)
	packID := store.OfflinePackID(filepath.Base(filepath.Dir(member)))

	// A file that really exists at the pack root, one level above the pack's
	// directory: the stored path is not inside the pack directory, so it is
	// refused before any open.
	sibling := filepath.Join(filepath.Dir(filepath.Dir(member)), "stray.bfop")
	if err := os.WriteFile(sibling, []byte("stray"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{sibling, filepath.Join(filepath.Dir(member), "..", "stray.bfop")} {
		if f, _, err := handlers.openPackMember(packID, path); err == nil {
			_ = f.Close()
			t.Fatalf("openPackMember(%q) opened a file outside the pack directory", path)
		}
	}
	f, _, err := handlers.openPackMember(packID, member)
	if err != nil {
		t.Fatalf("openPackMember(member) = %v, want the stored member", err)
	}
	_ = f.Close()
}

func TestManifestRouteDeniesAfterCallerLosesMemberLibraryAccess(t *testing.T) {
	svc, authService, st, clock := newFixture(t)
	ctx := context.Background()
	scope := scopeFor(t, authService, "session-default")

	end := 30.0
	if _, err := st.CreateMarker(ctx, store.Marker{
		MediaID: "media-alpha", Title: "Opening-Canary", StartSeconds: 0, EndSeconds: &end,
	}, store.LibraryFilter{AllLibraries: true, Revealed: true}, clock()); err != nil {
		t.Fatalf("CreateMarker: %v", err)
	}

	secondLib, err := st.CreateLibrary(ctx, "Second", false, "", scope.ProfileID)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	secondPath := filepath.Join(t.TempDir(), "media-second.bin")
	if err := os.WriteFile(secondPath, []byte("bytes-media-second"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondMedia, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: secondLib.ID, Path: secondPath, Title: "Second",
		Container: "flac", Codec: "flac", Class: store.MediaClassAudio,
		ContentFingerprint: "fp-media-second", DurationSecondsHint: 60,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	secondPartPath := filepath.Join(t.TempDir(), "media-second-part1.bin")
	if err := os.WriteFile(secondPartPath, []byte("bytes-media-second-part1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaFileVersion(ctx, secondMedia.ID, store.MediaFile{
		Path: secondPartPath, ContentFingerprint: "fp-media-second-part1", PartOrdinal: 1,
	}, nil); err != nil {
		t.Fatalf("AddMediaFileVersion: %v", err)
	}

	scope = scopeFor(t, authService, "session-default")
	lostPack, err := svc.BuildPack(ctx, scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha", ManifestParts: true,
	})
	if err != nil {
		t.Fatalf("BuildPack(lostPack): %v", err)
	}
	lostToken, err := svc.IssuePackGrant(ctx, scope, lostPack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant(lostPack): %v", err)
	}

	mixedPack, err := svc.BuildPack(ctx, scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha," + string(secondMedia.ID), ManifestParts: true,
	})
	if err != nil {
		t.Fatalf("BuildPack(mixedPack): %v", err)
	}
	mixedToken, err := svc.IssuePackGrant(ctx, scope, mixedPack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant(mixedPack): %v", err)
	}

	visiblePack, err := svc.BuildPack(ctx, scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: string(secondMedia.ID), ManifestParts: true,
	})
	if err != nil {
		t.Fatalf("BuildPack(visiblePack): %v", err)
	}
	visibleToken, err := svc.IssuePackGrant(ctx, scope, visiblePack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant(visiblePack): %v", err)
	}

	handlers := NewHTTPHandlers(svc, authService)

	if rec := serveManifest(t, handlers, lostToken); rec.Code != http.StatusOK || rec.Body.String() != lostPack.Manifest {
		t.Fatalf("pre-loss lostPack manifest = %d, want 200 with manifest", rec.Code)
	}
	if rec := serveManifest(t, handlers, mixedToken); rec.Code != http.StatusOK || rec.Body.String() != mixedPack.Manifest {
		t.Fatalf("pre-loss mixedPack manifest = %d, want 200 with manifest", rec.Code)
	}
	if rec := serveManifest(t, handlers, visibleToken); rec.Code != http.StatusOK || rec.Body.String() != visiblePack.Manifest {
		t.Fatalf("pre-loss visiblePack manifest = %d, want 200 with manifest", rec.Code)
	}

	// Remove the caller's access to library-local while keeping secondLib.
	if _, err := st.SetProfileLibraries(ctx, scope.ProfileID, []store.LibraryID{secondLib.ID}); err != nil {
		t.Fatalf("SetProfileLibraries: %v", err)
	}

	rec := serveManifest(t, handlers, lostToken)
	if rec.Code == http.StatusOK {
		t.Fatalf("post-loss lostPack manifest status = 200, want denial")
	}
	if strings.Contains(rec.Body.String(), lostPack.Manifest) || rec.Header().Get("Content-Type") == "application/jose" {
		t.Fatalf("post-loss lostPack manifest served manifest bytes: content-type=%q body=%q", rec.Header().Get("Content-Type"), rec.Body.String())
	}
	contentRec := serveContent(t, handlers, lostToken, "media-alpha")
	if contentRec.Code != rec.Code || bytes.HasPrefix(contentRec.Body.Bytes(), []byte("BFOP")) {
		t.Fatalf("post-loss lostPack content status = %d (manifest %d), want matching denial without BFOP bytes", contentRec.Code, rec.Code)
	}

	mixedRec := serveManifest(t, handlers, mixedToken)
	if mixedRec.Code == http.StatusOK {
		t.Fatalf("post-loss mixedPack manifest status = 200, want denial when any member is hidden")
	}
	if strings.Contains(mixedRec.Body.String(), mixedPack.Manifest) || mixedRec.Header().Get("Content-Type") == "application/jose" {
		t.Fatalf("post-loss mixedPack manifest served manifest bytes: content-type=%q body=%q", mixedRec.Header().Get("Content-Type"), mixedRec.Body.String())
	}
	mixedContentRec := serveContent(t, handlers, mixedToken, string(secondMedia.ID))
	if mixedContentRec.Code != mixedRec.Code || bytes.HasPrefix(mixedContentRec.Body.Bytes(), []byte("BFOP")) {
		t.Fatalf("post-loss mixedPack content status = %d (manifest %d), want matching denial without BFOP bytes", mixedContentRec.Code, mixedRec.Code)
	}

	visibleRec := serveManifest(t, handlers, visibleToken)
	if visibleRec.Code != http.StatusOK || visibleRec.Body.String() != visiblePack.Manifest {
		t.Fatalf("post-loss visiblePack manifest = %d %q, want 200 with manifest", visibleRec.Code, visibleRec.Body.String())
	}
	visibleContentRec := serveContent(t, handlers, visibleToken, string(secondMedia.ID))
	if visibleContentRec.Code != http.StatusOK || !bytes.HasPrefix(visibleContentRec.Body.Bytes(), []byte("BFOP")) {
		t.Fatalf("post-loss visiblePack content part 0 = %d, want 200 with BFOP bytes", visibleContentRec.Code)
	}
	visiblePart1Rec := serveContent(t, handlers, visibleToken, string(secondMedia.ID)+"/1")
	if visiblePart1Rec.Code != http.StatusOK || !bytes.HasPrefix(visiblePart1Rec.Body.Bytes(), []byte("BFOP")) {
		t.Fatalf("post-loss visiblePack content part 1 = %d, want 200 with BFOP bytes", visiblePart1Rec.Code)
	}
}

func TestPackRoutesDenyAfterPurgingOnePackMember(t *testing.T) {
	svc, authService, st, clock := newFixture(t)
	elevateOfflineSession(t, st, clock)
	ctx := context.Background()
	scope := scopeFor(t, authService, "session-default")

	secondPath := filepath.Join(t.TempDir(), "media-second.bin")
	if err := os.WriteFile(secondPath, []byte("bytes-media-second"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondMedia, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: secondPath, Title: "Second",
		Container: "flac", Codec: "flac", Class: store.MediaClassAudio,
		ContentFingerprint: "fp-media-second", DurationSecondsHint: 60,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	pack, err := svc.BuildPack(ctx, scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha," + string(secondMedia.ID),
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(ctx, scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	handlers := NewHTTPHandlers(svc, authService)

	if rec := serveManifest(t, handlers, token); rec.Code != http.StatusOK {
		t.Fatalf("pre-trash manifest = %d, want 200", rec.Code)
	}
	if rec := serveContent(t, handlers, token, string(secondMedia.ID)); rec.Code != http.StatusOK {
		t.Fatalf("pre-trash content = %d, want 200", rec.Code)
	}
	if rec := serveKey(t, handlers, token); rec.Code != http.StatusOK || rec.Body.Len() != WrappedEnvelopeSize {
		t.Fatalf("pre-trash key = %d/%d, want 200/%d", rec.Code, rec.Body.Len(), WrappedEnvelopeSize)
	}

	if err := st.TrashMedia(ctx, "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true}); err != nil {
		t.Fatalf("TrashMedia: %v", err)
	}
	if rec := serveManifest(t, handlers, token); rec.Code != http.StatusNotFound {
		t.Fatalf("trashed manifest = %d, want 404", rec.Code)
	}

	purged, err := st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || purged != 1 {
		t.Fatalf("PurgeTrashedBefore = %d, %v; want 1, nil", purged, err)
	}
	items, err := st.OfflinePackItems(ctx, pack.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("OfflinePackItems after purge = %d, %v; want 1 surviving row", len(items), err)
	}

	manifestRec := serveManifest(t, handlers, token)
	if manifestRec.Code != http.StatusNotFound {
		t.Fatalf("post-purge manifest = %d, want 404", manifestRec.Code)
	}
	if strings.Contains(manifestRec.Body.String(), pack.Manifest) || manifestRec.Header().Get("Content-Type") == "application/jose" {
		t.Fatalf("post-purge manifest served manifest bytes: content-type=%q body=%q", manifestRec.Header().Get("Content-Type"), manifestRec.Body.String())
	}

	contentRec := serveContent(t, handlers, token, string(secondMedia.ID))
	if contentRec.Code != http.StatusNotFound || bytes.HasPrefix(contentRec.Body.Bytes(), []byte("BFOP")) {
		t.Fatalf("post-purge surviving content = %d, want 404 without BFOP bytes", contentRec.Code)
	}

	keyRec := serveKey(t, handlers, token)
	if keyRec.Code != http.StatusNotFound || keyRec.Body.Len() == WrappedEnvelopeSize {
		t.Fatalf("post-purge key = %d/%d, want 404 without wrapped CEK", keyRec.Code, keyRec.Body.Len())
	}
}

func TestKeyRouteRefusalAfterLosingLibraryAccessAuditsCaller(t *testing.T) {
	svc, authService, st, clock := newFixture(t)
	elevateOfflineSession(t, st, clock)
	ctx := context.Background()
	scope := scopeFor(t, authService, "session-default")

	secondLib, err := st.CreateLibrary(ctx, "Second", false, "", scope.ProfileID)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	pack, err := svc.BuildPack(ctx, scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(ctx, scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}

	if _, err := st.SetProfileLibraries(ctx, scope.ProfileID, []store.LibraryID{secondLib.ID}); err != nil {
		t.Fatalf("SetProfileLibraries: %v", err)
	}

	audit := &recordingAudit{}
	handlers := NewHTTPHandlers(svc, authService)
	handlers.SetAuditRecorder(audit)

	rec := serveKey(t, handlers, token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("post-loss key status = %d, want 404", rec.Code)
	}
	if rec.Body.Len() == WrappedEnvelopeSize {
		t.Fatal("post-loss key route served wrapped CEK bytes")
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
		Outcome:   AuditRefused,
	}
	if audit.records[0] != want {
		t.Fatalf("key audit record = %+v, want %+v", audit.records[0], want)
	}
}
