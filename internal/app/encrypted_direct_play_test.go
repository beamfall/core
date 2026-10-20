package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// encryptedDirectPlayFixture wires the encrypted-Library direct-play route the
// way the composition root does: the SAME adapter value app.New passes to
// SetEncryptedBytes, over a real libencrypt.Manager, plus the same
// Provisioned-and-Sealed predicate. Nothing about the encrypted byte path is
// faked, so a regression in the adapter's interface set is visible here as a
// wrong HTTP status rather than as a silently skipped probe.
type encryptedDirectPlayFixture struct {
	handler   *mediahttp.Handler
	token     string
	plaintext []byte
}

func newEncryptedDirectPlayFixture(t *testing.T) encryptedDirectPlayFixture {
	t.Helper()
	const libraryID = store.LibraryID("library-local")
	const mediaID = store.MediaID("media-alpha")
	const sessionID = store.SessionID("session-default")
	ctx := context.Background()

	st := storetest.Open(t)
	// The at-rest regime is active for this Library, which is
	// what routes its media bytes onto the encrypted branch of the byte route.
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}

	manager := libencrypt.NewManagerWithParams(memSecretStore{}, libencrypt.TestArgon2Params)
	if _, err := manager.Provision(string(libraryID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	adapter := appLibEncryptDBCipher{manager: manager}

	// Ingest order, as in production: the plaintext object is cataloged first —
	// the persisted content fingerprint is the PLAINTEXT identity — and the
	// object is then encrypted in place under that identity as its AAD.
	plaintext := []byte("0123456789")
	mediaPath := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(mediaPath, plaintext, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, mediaID, mediaPath); err != nil {
		t.Fatalf("SetPrimaryMediaFile: %v", err)
	}
	fingerprint, err := store.HashMediaFileContent(mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	ciphertextPath := filepath.Join(t.TempDir(), "media.enc")
	if err := adapter.EncryptObjectFile(string(libraryID), mediaPath, ciphertextPath, fingerprint); err != nil {
		t.Fatalf("EncryptObjectFile: %v", err)
	}
	if err := os.Rename(ciphertextPath, mediaPath); err != nil {
		t.Fatal(err)
	}

	authService := auth.NewService(st)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	scope, err := authService.Scope(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   mediaID,
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	// Both wirings are copied verbatim from the composition root (app.New).
	handler.SetSealed(func(id store.LibraryID) bool {
		return manager.Provisioned(string(id)) && manager.Sealed(string(id))
	})
	handler.SetEncryptedBytes(appLibEncryptDBCipher{manager: manager})

	return encryptedDirectPlayFixture{handler: handler, token: token, plaintext: plaintext}
}

// TestEncryptedLibraryDirectPlayServesRangeThroughProductionAdapter is the
// production-wiring proof for COREAPI-AUDIT-0822-2. The direct-play route
// probes its encrypted-bytes dependency for the streaming extension at run
// time; before this ticket only the test fake satisfied it, so the real server
// answered 416 to every Range request on an encrypted Library and no player
// could seek. A 206 with the exact requested plaintext slice can only be
// produced through the streaming branch.
func TestEncryptedLibraryDirectPlayServesRangeThroughProductionAdapter(t *testing.T) {
	fixture := newEncryptedDirectPlayFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+fixture.token, nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()

	fixture.handler.Direct(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d (a Range request on an encrypted Library must stream): %s", rec.Code, http.StatusPartialContent, rec.Body.String())
	}
	if rec.Body.String() != "2345" {
		t.Fatalf("body = %q, want the decrypted range bytes %q", rec.Body.String(), "2345")
	}
	if got, want := rec.Header().Get("Content-Range"), "bytes 2-5/10"; got != want {
		t.Fatalf("Content-Range = %q, want %q (the range is resolved against the PLAINTEXT length)", got, want)
	}
}

// TestEncryptedLibraryDirectPlayServesWholeObjectThroughProductionAdapter
// pins the no-Range arm of the same wiring: it is served by the streaming
// reader too, so the whole-object buffering fallback is unreachable from the
// direct-play path for media bytes.
func TestEncryptedLibraryDirectPlayServesWholeObjectThroughProductionAdapter(t *testing.T) {
	fixture := newEncryptedDirectPlayFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+fixture.token, nil)
	rec := httptest.NewRecorder()

	fixture.handler.Direct(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != string(fixture.plaintext) {
		t.Fatalf("body = %q, want the decrypted object %q", rec.Body.String(), fixture.plaintext)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want %q — a seekable reader must advertise range support", got, "bytes")
	}
}

// TestEncryptedByteAdapterRejectsWrongObjectIdentityOnOpen keeps the streaming
// arm bound to the same fail-closed object identity the buffered arm already
// had: ciphertext presented under another object's identity must not open.
func TestEncryptedByteAdapterRejectsWrongObjectIdentityOnOpen(t *testing.T) {
	const libraryID = store.LibraryID("library-local")
	manager := libencrypt.NewManagerWithParams(memSecretStore{}, libencrypt.TestArgon2Params)
	if _, err := manager.Provision(string(libraryID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	adapter := appLibEncryptDBCipher{manager: manager}

	plaintextPath := filepath.Join(t.TempDir(), "object.plain")
	if err := os.WriteFile(plaintextPath, []byte("plain media bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	ciphertextPath := filepath.Join(t.TempDir(), "object.enc")
	if err := adapter.EncryptObjectFile(string(libraryID), plaintextPath, ciphertextPath, "sha256:open-reader-object"); err != nil {
		t.Fatalf("EncryptObjectFile: %v", err)
	}

	reader, err := adapter.OpenDecryptReader(libraryID, ciphertextPath, string(libencrypt.ObjectClassMediaBytes), "sha256:open-reader-object")
	if err != nil {
		t.Fatalf("OpenDecryptReader: %v", err)
	}
	_ = reader.Close()

	if _, err := adapter.OpenDecryptReader(libraryID, ciphertextPath, string(libencrypt.ObjectClassMediaBytes), "sha256:some-other-object"); err == nil {
		t.Fatal("OpenDecryptReader with a foreign object identity must fail closed")
	}
	if _, err := adapter.OpenDecryptReader(libraryID, ciphertextPath, "made-up-class", "sha256:open-reader-object"); err == nil {
		t.Fatal("OpenDecryptReader with an unknown object class must fail closed")
	}
}
