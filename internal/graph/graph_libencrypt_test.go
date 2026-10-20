package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/libencrypt/secretstoretest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// memSecretStore is an in-memory libencrypt.SecretStore for the graph tests,
// recording every persisted value so the invariant-#5 negative assertions can
// inspect what reached "at rest".
type memSecretStore struct {
	values      map[string]string
	locus       libencrypt.KeyLocusInfo
	setCalls    int
	failSetCall int
	failSetErr  error
}

func newMemSecretStore() *memSecretStore { return &memSecretStore{values: map[string]string{}} }

func TestSecretStoreAbsenceContract(t *testing.T) {
	secretstoretest.AssertAbsence(t, newMemSecretStore())
}

func (s *memSecretStore) Secret(ref string) (string, error) {
	v, ok := s.values[ref]
	if !ok {
		return "", errNoSecret
	}
	return v, nil
}

func (s *memSecretStore) SetSecret(ref, value string) error {
	s.setCalls++
	if s.failSetCall != 0 && s.setCalls == s.failSetCall {
		return s.failSetErr
	}
	if value == "" {
		delete(s.values, ref)
		return nil
	}
	s.values[ref] = value
	return nil
}

func (s *memSecretStore) SecretLocus(ref string) libencrypt.KeyLocusInfo {
	if s.locus.Class == "" {
		return libencrypt.KeyLocusInfo{Class: libencrypt.KeyLocusUnknown}
	}
	return s.locus
}

var errNoSecret = &noSecretErr{}

type noSecretErr struct{}

func (*noSecretErr) Error() string { return "no secret" }

// Unwrap satisfies libencrypt.SecretStore: only fs.ErrNotExist means the ref is
// absent, so an unreadable entry is never mistaken for one that was never set.
func (*noSecretErr) Unwrap() error { return fs.ErrNotExist }

// newEncryptedLibrary creates a Library granted to the seeded manager Profile
// and flags it encrypted (ENC-1's SetLibraryBacking), returning its id.
func newEncryptedLibrary(t *testing.T, st *store.Store) string {
	t.Helper()
	lib, err := st.CreateLibrary(t.Context(), "Photo Backup", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if err := st.SetLibraryBacking(t.Context(), lib.ID, true, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	return string(lib.ID)
}

func postGraphQL(t *testing.T, handler http.Handler, authorize func(*http.Request), query string, vars map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestProvisionEncryptedLibraryAdminGated confirms provisioning is Admin-Session-
// gated and, with an Admin Session, returns the Recovery Key exactly once with
// the acknowledgement contract — and never leaks key material on the wire.
func TestProvisionEncryptedLibraryAdminGated(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	secrets := newMemSecretStore()
	handler.SetLibEncrypt(libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params))
	libID := newEncryptedLibrary(t, st)

	const mutation = `mutation P($libraryID: ID!, $unsealMode: String) { provisionEncryptedLibrary(libraryID: $libraryID, unsealMode: $unsealMode) { recoveryKey acknowledgementRequired status { provisioned sealed unsealMode } } }`

	// A manager WITHOUT an open Admin Session is refused.
	if rec := postGraphQL(t, handler, authorizeManager, mutation, map[string]any{"libraryID": libID}); !bytes.Contains(rec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("non-admin provision = %s", rec.Body.String())
	}

	openManagerAdminSession(t, st)
	rec := postGraphQL(t, handler, authorizeManager, mutation, map[string]any{"libraryID": libID, "unsealMode": "auto"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ProvisionEncryptedLibrary ProvisionEncryptedLibraryInfo `json:"provisionEncryptedLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	got := resp.Data.ProvisionEncryptedLibrary
	if got.RecoveryKey == "" || !got.AcknowledgementRequired {
		t.Fatalf("expected a Recovery Key + acknowledgement: %#v", got)
	}
	if got.Status.Sealed || !got.Status.Provisioned || got.Status.UnsealMode != "auto" {
		t.Fatalf("post-provision status = %#v", got.Status)
	}

	// Invariant #5: no raw DEK/KEK appears on the wire. The bundle/KEK live only
	// in the secret store; the response carries only the Recovery Key display and
	// non-secret status.
	rkBody := normalizeRecoveryForTest(got.RecoveryKey)
	for ref, val := range secrets.values {
		if strings.Contains(val, rkBody) {
			t.Fatalf("Recovery Key body found in persisted secret %q — must be shown once only", ref)
		}
	}

	// A second provision is rejected (would orphan the DEK).
	rec2 := postGraphQL(t, handler, authorizeManager, mutation, map[string]any{"libraryID": libID, "unsealMode": "auto"})
	if !bytes.Contains(rec2.Body.Bytes(), []byte("already provisioned")) {
		t.Fatalf("re-provision = %s", rec2.Body.String())
	}
}

func TestEncryptedLibraryStatusReportsAutoUnsealKeyLocus(t *testing.T) {
	cases := []struct {
		name               string
		locus              libencrypt.KeyLocusInfo
		wantClass          string
		wantDisclosurePart string
	}{
		{
			name: "platform secure store",
			locus: libencrypt.KeyLocusInfo{
				Class:      libencrypt.KeyLocusPlatformSecureStore,
				Disclosure: libencrypt.KeyLocusPlatformDisclosure,
			},
			wantClass:          "platform-secure-store",
			wantDisclosurePart: "protect data-dir or backup theft",
		},
		{
			name: "file fallback same volume",
			locus: libencrypt.KeyLocusInfo{
				Class:      libencrypt.KeyLocusFileSameVolume,
				Disclosure: libencrypt.KeyLocusSameVolumeDisclosure,
			},
			wantClass:          "file-same-volume",
			wantDisclosurePart: "disk theft is effectively whole-box theft",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.Open(t)
			handler := newTestHandlerWithStore(st)
			secrets := newMemSecretStore()
			secrets.locus = tc.locus
			handler.SetLibEncrypt(libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params))
			libID := newEncryptedLibrary(t, st)
			openManagerAdminSession(t, st)

			provision := `mutation P($libraryID: ID!) { provisionEncryptedLibrary(libraryID: $libraryID, unsealMode: "auto") { status { keyLocusClass keyLocusDisclosure } } }`
			prec := postGraphQL(t, handler, authorizeManager, provision, map[string]any{"libraryID": libID})
			var provisionResp struct {
				Data struct {
					ProvisionEncryptedLibrary ProvisionEncryptedLibraryInfo `json:"provisionEncryptedLibrary"`
				} `json:"data"`
				Errors []struct{ Message string } `json:"errors"`
			}
			if err := json.Unmarshal(prec.Body.Bytes(), &provisionResp); err != nil {
				t.Fatalf("decode provision: %v; body = %s", err, prec.Body.String())
			}
			if len(provisionResp.Errors) != 0 {
				t.Fatalf("provision errors = %#v; body = %s", provisionResp.Errors, prec.Body.String())
			}
			if got := provisionResp.Data.ProvisionEncryptedLibrary.Status; got.KeyLocusClass != tc.wantClass || !strings.Contains(got.KeyLocusDisclosure, tc.wantDisclosurePart) {
				t.Fatalf("provision status locus = %#v", got)
			}

			query := `query Q($libraryID: ID!) { encryptedLibraryStatus(libraryID: $libraryID) { keyLocusClass keyLocusDisclosure } }`
			rec := postGraphQL(t, handler, authorizeManager, query, map[string]any{"libraryID": libID})
			var resp struct {
				Data struct {
					EncryptedLibraryStatus EncryptedLibraryStatusInfo `json:"encryptedLibraryStatus"`
				} `json:"data"`
				Errors []struct{ Message string } `json:"errors"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode status: %v; body = %s", err, rec.Body.String())
			}
			if len(resp.Errors) != 0 {
				t.Fatalf("status errors = %#v; body = %s", resp.Errors, rec.Body.String())
			}
			got := resp.Data.EncryptedLibraryStatus
			if got.KeyLocusClass != tc.wantClass {
				t.Fatalf("keyLocusClass = %q, want %q", got.KeyLocusClass, tc.wantClass)
			}
			if !strings.Contains(got.KeyLocusDisclosure, tc.wantDisclosurePart) {
				t.Fatalf("keyLocusDisclosure = %q, want substring %q", got.KeyLocusDisclosure, tc.wantDisclosurePart)
			}
		})
	}
}

// TestSealUnsealLifecycle drives the Sealed→Unsealed transitions through the
// graph for both the passphrase and Recovery-Key paths, including wrong-credential
// rejection.
func TestSealUnsealLifecycle(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	secrets := newMemSecretStore()
	mgr := libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	libID := newEncryptedLibrary(t, st)
	openManagerAdminSession(t, st)

	const passphrase = "operator unlock passphrase"
	provision := `mutation P($libraryID: ID!, $unsealMode: String, $passphrase: String) { provisionEncryptedLibrary(libraryID: $libraryID, unsealMode: $unsealMode, passphrase: $passphrase) { recoveryKey } }`
	prec := postGraphQL(t, handler, authorizeManager, provision, map[string]any{"libraryID": libID, "unsealMode": "passphrase", "passphrase": passphrase})
	var presp struct {
		Data struct {
			ProvisionEncryptedLibrary ProvisionEncryptedLibraryInfo `json:"provisionEncryptedLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(prec.Body.Bytes(), &presp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, prec.Body.String())
	}
	if len(presp.Errors) != 0 {
		t.Fatalf("provision errors = %#v", presp.Errors)
	}
	recoveryKey := presp.Data.ProvisionEncryptedLibrary.RecoveryKey

	// Seal it (drops the in-memory DEK).
	sealMut := `mutation S($libraryID: ID!) { sealEncryptedLibrary(libraryID: $libraryID) { sealed } }`
	srec := postGraphQL(t, handler, authorizeManager, sealMut, map[string]any{"libraryID": libID})
	if !sealedStatus(t, srec, "sealEncryptedLibrary") {
		t.Fatalf("seal did not seal: %s", srec.Body.String())
	}

	unsealMut := `mutation U($libraryID: ID!, $passphrase: String, $recoveryKey: String) { unsealEncryptedLibrary(libraryID: $libraryID, passphrase: $passphrase, recoveryKey: $recoveryKey) { sealed } }`

	// Wrong passphrase fails and stays Sealed.
	wrec := postGraphQL(t, handler, authorizeManager, unsealMut, map[string]any{"libraryID": libID, "passphrase": "wrong"})
	if !bytes.Contains(wrec.Body.Bytes(), []byte("incorrect passphrase or recovery key")) {
		t.Fatalf("wrong passphrase = %s", wrec.Body.String())
	}
	if !mgr.Sealed(libID) {
		t.Fatal("library must stay Sealed after a wrong passphrase")
	}

	// Right passphrase unseals.
	urec := postGraphQL(t, handler, authorizeManager, unsealMut, map[string]any{"libraryID": libID, "passphrase": passphrase})
	if sealedStatus(t, urec, "unsealEncryptedLibrary") {
		t.Fatalf("passphrase unseal failed: %s", urec.Body.String())
	}

	// Re-seal, then unseal via the Recovery Key (disaster-recovery path).
	postGraphQL(t, handler, authorizeManager, sealMut, map[string]any{"libraryID": libID})
	rrec := postGraphQL(t, handler, authorizeManager, unsealMut, map[string]any{"libraryID": libID, "recoveryKey": recoveryKey})
	if sealedStatus(t, rrec, "unsealEncryptedLibrary") {
		t.Fatalf("recovery-key unseal failed: %s", rrec.Body.String())
	}
}

func TestSealUnsealWrapsStoreLifecycle(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	secrets := newMemSecretStore()
	mgr := libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	lifecycle := &recordingEncryptedDBLifecycle{}
	handler.SetEncryptedLibraryDBLifecycle(lifecycle)
	libID := newEncryptedLibrary(t, st)
	openManagerAdminSession(t, st)

	const passphrase = "operator unlock passphrase"
	provision := `mutation P($libraryID: ID!, $unsealMode: String, $passphrase: String) { provisionEncryptedLibrary(libraryID: $libraryID, unsealMode: $unsealMode, passphrase: $passphrase) { status { sealed } } }`
	prec := postGraphQL(t, handler, authorizeManager, provision, map[string]any{"libraryID": libID, "unsealMode": "passphrase", "passphrase": passphrase})
	var presp struct {
		Data struct {
			ProvisionEncryptedLibrary ProvisionEncryptedLibraryInfo `json:"provisionEncryptedLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(prec.Body.Bytes(), &presp); err != nil {
		t.Fatalf("decode provision: %v; body = %s", err, prec.Body.String())
	}
	if len(presp.Errors) != 0 {
		t.Fatalf("provision errors = %#v; body = %s", presp.Errors, prec.Body.String())
	}
	if presp.Data.ProvisionEncryptedLibrary.Status.Sealed {
		t.Fatalf("provision did not register DB lifecycle: %s", prec.Body.String())
	}
	if lifecycle.unseals != 1 || !lifecycle.registered[store.LibraryID(libID)] {
		t.Fatalf("provision lifecycle = unseals %d registered %#v", lifecycle.unseals, lifecycle.registered)
	}

	sealMut := `mutation S($libraryID: ID!) { sealEncryptedLibrary(libraryID: $libraryID) { sealed } }`
	srec := postGraphQL(t, handler, authorizeManager, sealMut, map[string]any{"libraryID": libID})
	if !sealedStatus(t, srec, "sealEncryptedLibrary") {
		t.Fatalf("seal did not report sealed after DB lifecycle: %s", srec.Body.String())
	}
	if lifecycle.seals != 1 || !mgr.Sealed(libID) {
		t.Fatalf("seal lifecycle = seals %d key sealed %v", lifecycle.seals, mgr.Sealed(libID))
	}

	unsealMut := `mutation U($libraryID: ID!, $passphrase: String) { unsealEncryptedLibrary(libraryID: $libraryID, passphrase: $passphrase) { sealed } }`
	urec := postGraphQL(t, handler, authorizeManager, unsealMut, map[string]any{"libraryID": libID, "passphrase": passphrase})
	if sealedStatus(t, urec, "unsealEncryptedLibrary") {
		t.Fatalf("unseal did not register DB lifecycle: %s", urec.Body.String())
	}
	if lifecycle.unseals != 2 || !lifecycle.registered[store.LibraryID(libID)] {
		t.Fatalf("unseal lifecycle = unseals %d registered %#v", lifecycle.unseals, lifecycle.registered)
	}
}

func TestRewrapEncryptedLibraryRotatesFilesAndRecoveryKey(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(ctx, dataDir, store.Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	storetest.InstallSnapshotSealer(st)
	t.Cleanup(func() { _ = st.Close() })
	handler := newTestHandlerWithStore(st)
	secrets := newMemSecretStore()
	mgr := libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	handler.SetEncryptedLibraryDBLifecycle(st)
	assets := genassets.NewService(st, nil, t.TempDir())
	handler.SetEncryptedGeneratedAssets(assets)
	libID := newEncryptedLibrary(t, st)
	openManagerAdminSession(t, st)

	provision := `mutation P($libraryID: ID!) { provisionEncryptedLibrary(libraryID: $libraryID) { recoveryKey } }`
	prec := postGraphQL(t, handler, authorizeManager, provision, map[string]any{"libraryID": libID})
	var presp struct {
		Data struct {
			ProvisionEncryptedLibrary ProvisionEncryptedLibraryInfo `json:"provisionEncryptedLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(prec.Body.Bytes(), &presp); err != nil {
		t.Fatalf("decode provision: %v; body = %s", err, prec.Body.String())
	}
	if len(presp.Errors) != 0 {
		t.Fatalf("provision errors = %#v; body = %s", presp.Errors, prec.Body.String())
	}
	oldRK := presp.Data.ProvisionEncryptedLibrary.RecoveryKey

	dir := t.TempDir()
	originalPlain := filepath.Join(dir, "original.plain")
	originalCipher := filepath.Join(dir, "original.libencrypt")
	if err := os.WriteFile(originalPlain, []byte("original bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalObj := libencrypt.MediaObjectContext(libID, "sha256:enc7-original")
	if err := mgr.EncryptFile(libID, originalPlain, originalCipher, originalObj); err != nil {
		t.Fatalf("EncryptFile original: %v", err)
	}
	media, file, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          store.LibraryID(libID),
		Path:               originalCipher,
		ContentFingerprint: "sha256:enc7-original",
		Title:              "Encrypted Original",
		Container:          "matroska",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if file.MediaID != media.ID {
		t.Fatalf("imported file media = %q, want %q", file.MediaID, media.ID)
	}
	assetPlain := filepath.Join(dir, "asset.plain")
	assetCipher := assets.ManifestPath(file.ContentFingerprint)
	if err := os.MkdirAll(filepath.Dir(assetCipher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPlain, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	assetObj := libencrypt.GeneratedAssetObjectContext(libID, file.ContentFingerprint, filepath.Base(assetCipher))
	if err := mgr.EncryptFile(libID, assetPlain, assetCipher, assetObj); err != nil {
		t.Fatalf("EncryptFile asset: %v", err)
	}
	preMigrationDir := filepath.Join(dataDir, "backups", "pre-migration", "encrypted-library", libID)
	if err := os.MkdirAll(preMigrationDir, 0o700); err != nil {
		t.Fatal(err)
	}
	preMigrationSnapshot := filepath.Join(preMigrationDir, "old.encrypted_library.db.libencrypt")
	if err := os.WriteFile(preMigrationSnapshot, []byte("old-DEK ciphertext snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupResult, err := st.BackupWithOptions(ctx, filepath.Join(dataDir, "backups"), store.BackupOptions{EncryptedLibraryDBCipher: handler.libEncryptDBCipher()})
	if err != nil {
		t.Fatalf("BackupWithOptions: %v", err)
	}
	backupSetName := filepath.Base(filepath.Dir(backupResult.Manifest))
	originalBefore, err := os.ReadFile(originalCipher)
	if err != nil {
		t.Fatal(err)
	}
	assetBefore, err := os.ReadFile(assetCipher)
	if err != nil {
		t.Fatal(err)
	}

	rewrap := `mutation R($libraryID: ID!) { rewrapEncryptedLibrary(libraryID: $libraryID) { recoveryKey acknowledgementRequired residualRiskNotice backupRotationArtifacts { kind path manifest action } status { sealed } } }`
	if rec := postGraphQL(t, handler, authorizeViewer, rewrap, map[string]any{"libraryID": libID}); !bytes.Contains(rec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("viewer rewrap = %s", rec.Body.String())
	}
	rrec := postGraphQL(t, handler, authorizeManager, rewrap, map[string]any{"libraryID": libID})
	var rresp struct {
		Data struct {
			RewrapEncryptedLibrary ProvisionEncryptedLibraryInfo `json:"rewrapEncryptedLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rrec.Body.Bytes(), &rresp); err != nil {
		t.Fatalf("decode rewrap: %v; body = %s", err, rrec.Body.String())
	}
	if len(rresp.Errors) != 0 {
		t.Fatalf("rewrap errors = %#v; body = %s", rresp.Errors, rrec.Body.String())
	}
	newRK := rresp.Data.RewrapEncryptedLibrary.RecoveryKey
	if newRK == "" || newRK == oldRK || !rresp.Data.RewrapEncryptedLibrary.AcknowledgementRequired || !rresp.Data.RewrapEncryptedLibrary.Status.Sealed {
		t.Fatalf("rewrap result = %#v", rresp.Data.RewrapEncryptedLibrary)
	}
	if rresp.Data.RewrapEncryptedLibrary.ResidualRiskNotice != "Rotation protects new copies; already-copied ciphertext remains recoverable to anyone who held the old key. Off-site backup copies (ADR-0115) are wrapped under a separate BackupKey and are not rotated by this response — rotate the BackupKey separately to protect them." {
		t.Fatalf("residual notice = %q", rresp.Data.RewrapEncryptedLibrary.ResidualRiskNotice)
	}
	artifacts := rresp.Data.RewrapEncryptedLibrary.BackupRotationArtifacts
	if len(artifacts) != 3 {
		t.Fatalf("backupRotationArtifacts = %#v, want backup manifest entry + off-site destination + pre-migration snapshot", artifacts)
	}
	if artifacts[0].Kind != "backup-manifest-entry" || !strings.Contains(artifacts[0].Manifest, backupSetName) || artifacts[0].Action != "rotate-or-destroy" {
		t.Fatalf("backup manifest artifact = %#v", artifacts[0])
	}
	if artifacts[1].Kind != "offsite-destination" || artifacts[1].Action != "rotate-backup-key-separately" {
		t.Fatalf("off-site destination artifact = %#v", artifacts[1])
	}
	if artifacts[2].Kind != "pre-migration-snapshot" || artifacts[2].Path != preMigrationSnapshot || artifacts[2].Action != "destroy-or-explicitly-retain" {
		t.Fatalf("pre-migration artifact = %#v", artifacts[2])
	}
	originalAfter, err := os.ReadFile(originalCipher)
	if err != nil {
		t.Fatal(err)
	}
	assetAfter, err := os.ReadFile(assetCipher)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(originalBefore, originalAfter) {
		t.Fatal("original ciphertext did not change")
	}
	if bytes.Equal(assetBefore, assetAfter) {
		t.Fatal("generated-asset ciphertext did not change")
	}

	unseal := `mutation U($libraryID: ID!, $recoveryKey: String) { unsealEncryptedLibrary(libraryID: $libraryID, recoveryKey: $recoveryKey) { sealed } }`
	oldRec := postGraphQL(t, handler, authorizeManager, unseal, map[string]any{"libraryID": libID, "recoveryKey": oldRK})
	if !bytes.Contains(oldRec.Body.Bytes(), []byte("incorrect passphrase or recovery key")) {
		t.Fatalf("old Recovery Key after rewrap = %s", oldRec.Body.String())
	}
	newRec := postGraphQL(t, handler, authorizeManager, unseal, map[string]any{"libraryID": libID, "recoveryKey": newRK})
	if sealedStatus(t, newRec, "unsealEncryptedLibrary") {
		t.Fatalf("new Recovery Key did not unseal: %s", newRec.Body.String())
	}
	originalOut := filepath.Join(dir, "original.out")
	if err := mgr.DecryptFile(libID, originalCipher, originalOut, originalObj); err != nil {
		t.Fatalf("DecryptFile original after rewrap: %v", err)
	}
	gotOriginal, err := os.ReadFile(originalOut)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotOriginal) != "original bytes" {
		t.Fatalf("original plaintext = %q", gotOriginal)
	}
	assetOut := filepath.Join(dir, "asset.out")
	if err := mgr.DecryptFile(libID, assetCipher, assetOut, assetObj); err != nil {
		t.Fatalf("DecryptFile asset after rewrap: %v", err)
	}
	gotAsset, err := os.ReadFile(assetOut)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotAsset) != `{"version":1}` {
		t.Fatalf("asset plaintext = %q", gotAsset)
	}
}

// TestProvisionRejectsNonEncryptedLibrary confirms a Library not flagged
// encrypted cannot be provisioned.
func TestProvisionRejectsNonEncryptedLibrary(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	handler.SetLibEncrypt(libencrypt.NewManagerWithParams(newMemSecretStore(), libencrypt.TestArgon2Params))
	lib, err := st.CreateLibrary(t.Context(), "Plain", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	openManagerAdminSession(t, st)
	mutation := `mutation P($libraryID: ID!) { provisionEncryptedLibrary(libraryID: $libraryID) { recoveryKey } }`
	rec := postGraphQL(t, handler, authorizeManager, mutation, map[string]any{"libraryID": string(lib.ID)})
	if !bytes.Contains(rec.Body.Bytes(), []byte("not flagged encrypted")) {
		t.Fatalf("non-encrypted provision = %s", rec.Body.String())
	}
}

func TestProvisionCryptoAuditFailureRollsBackKeyGeneration(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	secrets := newMemSecretStore()
	mgr := libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	lifecycle := &recordingEncryptedDBLifecycle{}
	handler.SetEncryptedLibraryDBLifecycle(lifecycle)
	libID := newEncryptedLibrary(t, st)
	openManagerAdminSession(t, st)
	boom := errors.New("injected crypto detail audit failure")
	injected := false
	handler.mutationAuditAppend = func(ctx context.Context, entry store.ActivityEntry) error {
		if entry.Action == store.ActivityCryptoLifecyclePrepared && entry.TargetType == "encrypted-library-provision" {
			injected = true
			return boom
		}
		return st.AppendActivity(ctx, entry)
	}

	mutation := `mutation P($libraryID: ID!) { provisionEncryptedLibrary(libraryID: $libraryID) { recoveryKey } }`
	rec := postGraphQL(t, handler, authorizeManager, mutation, map[string]any{"libraryID": libID})
	if !injected || !bytes.Contains(rec.Body.Bytes(), []byte("unavailable")) {
		t.Fatalf("audit failure response = %s", rec.Body.String())
	}
	if mgr.Provisioned(libID) || !mgr.Sealed(libID) {
		t.Fatal("failed crypto audit committed a provision generation")
	}
	if len(secrets.values) != 0 {
		t.Fatalf("failed provision left secret refs: %#v", secrets.values)
	}
	if lifecycle.unseals != 1 || lifecycle.registered[store.LibraryID(libID)] {
		t.Fatalf("staged DB compensation = unseals %d registered %#v", lifecycle.unseals, lifecycle.registered)
	}
}

func TestRewrapCryptoAuditCancellationRestoresOldGeneration(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	storetest.InstallSnapshotSealer(st)
	t.Cleanup(func() { _ = st.Close() })
	handler := newTestHandlerWithStore(st)
	mgr := libencrypt.NewManagerWithParams(newMemSecretStore(), libencrypt.TestArgon2Params)
	handler.SetLibEncrypt(mgr)
	handler.SetEncryptedLibraryDBLifecycle(st)
	libID := newEncryptedLibrary(t, st)
	openManagerAdminSession(t, st)
	provision := `mutation P($libraryID: ID!) { provisionEncryptedLibrary(libraryID: $libraryID) { recoveryKey } }`
	prec := postGraphQL(t, handler, authorizeManager, provision, map[string]any{"libraryID": libID})
	var presp struct {
		Data struct {
			ProvisionEncryptedLibrary ProvisionEncryptedLibraryInfo `json:"provisionEncryptedLibrary"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(prec.Body.Bytes(), &presp); err != nil || len(presp.Errors) != 0 {
		t.Fatalf("provision = %s, err=%v", prec.Body.String(), err)
	}
	oldRecovery := presp.Data.ProvisionEncryptedLibrary.RecoveryKey
	injected := false
	handler.mutationAuditAppend = func(ctx context.Context, entry store.ActivityEntry) error {
		if entry.Action == store.ActivityCryptoLifecyclePrepared && entry.TargetType == "encrypted-library-rewrap" {
			injected = true
			return context.Canceled
		}
		return st.AppendActivity(ctx, entry)
	}

	mutation := `mutation R($libraryID: ID!) { rewrapEncryptedLibrary(libraryID: $libraryID) { recoveryKey } }`
	rec := postGraphQL(t, handler, authorizeManager, mutation, map[string]any{"libraryID": libID})
	if !injected || !bytes.Contains(rec.Body.Bytes(), []byte("unavailable")) {
		t.Fatalf("audit cancellation response = %s", rec.Body.String())
	}
	if !st.EncryptedLibraryDBRegistered(store.LibraryID(libID)) {
		t.Fatal("audit cancellation did not reopen the DB under the old generation")
	}
	seal := `mutation S($libraryID: ID!) { sealEncryptedLibrary(libraryID: $libraryID) { sealed } }`
	if rec := postGraphQL(t, handler, authorizeManager, seal, map[string]any{"libraryID": libID}); !sealedStatus(t, rec, "sealEncryptedLibrary") {
		t.Fatalf("seal after rollback = %s", rec.Body.String())
	}
	unseal := `mutation U($libraryID: ID!, $recoveryKey: String) { unsealEncryptedLibrary(libraryID: $libraryID, recoveryKey: $recoveryKey) { sealed } }`
	if rec := postGraphQL(t, handler, authorizeManager, unseal, map[string]any{"libraryID": libID, "recoveryKey": oldRecovery}); sealedStatus(t, rec, "unsealEncryptedLibrary") {
		t.Fatalf("old key generation was not restored: %s", rec.Body.String())
	}
}

func TestProvisionKeyWriteFailureLeavesPreparedIntentNotCompletedChange(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	secrets := newMemSecretStore()
	secrets.failSetCall = 2 // uncommitted journal succeeds; KEK write fails after audit preparation.
	secrets.failSetErr = errors.New("injected KEK write failure")
	handler.SetLibEncrypt(libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params))
	libID := newEncryptedLibrary(t, st)
	openManagerAdminSession(t, st)
	mutation := `mutation P($libraryID: ID!) { provisionEncryptedLibrary(libraryID: $libraryID) { recoveryKey } }`
	if rec := postGraphQL(t, handler, authorizeManager, mutation, map[string]any{"libraryID": libID}); !bytes.Contains(rec.Body.Bytes(), []byte("unavailable")) {
		t.Fatalf("key-write failure response = %s", rec.Body.String())
	}
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{
		Filter:                   store.LibraryFilter{Revealed: true, AllLibraries: true},
		Limit:                    20,
		IncludeMutationLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var prepared bool
	for _, entry := range entries {
		if entry.Action == store.ActivityConfigUpdate && entry.TargetType == "encrypted-library-provision" {
			t.Fatal("rolled-back key write persisted a completed config.update")
		}
		if entry.Action == store.ActivityCryptoLifecyclePrepared && entry.TargetType == "encrypted-library-provision" {
			prepared = true
		}
	}
	if !prepared {
		t.Fatal("key-write failure lost the durable prepared intent")
	}
}

// TestEncryptedLibraryNotInMyDataExport is the My-Data negative test (ADR-0036 /
// invariant #5): a Profile's My-Data engagement export contains no key material
// — the export schema has no field that could carry a DEK/KEK/Recovery Key, and
// provisioning a key for the Profile's Library adds nothing to it.
func TestEncryptedLibraryNotInMyDataExport(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	secrets := newMemSecretStore()
	handler.SetLibEncrypt(libencrypt.NewManagerWithParams(secrets, libencrypt.TestArgon2Params))
	libID := newEncryptedLibrary(t, st)
	openManagerAdminSession(t, st)

	provision := `mutation P($libraryID: ID!, $passphrase: String, $unsealMode: String) { provisionEncryptedLibrary(libraryID: $libraryID, passphrase: $passphrase, unsealMode: $unsealMode) { recoveryKey } }`
	prec := postGraphQL(t, handler, authorizeManager, provision, map[string]any{"libraryID": libID, "unsealMode": "passphrase", "passphrase": "the operator passphrase"})
	var presp struct {
		Data struct {
			ProvisionEncryptedLibrary ProvisionEncryptedLibraryInfo `json:"provisionEncryptedLibrary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(prec.Body.Bytes(), &presp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, prec.Body.String())
	}
	recoveryKey := presp.Data.ProvisionEncryptedLibrary.RecoveryKey
	if recoveryKey == "" {
		t.Fatalf("provision failed: %s", prec.Body.String())
	}

	// Export the manager Profile's My Data (the export operates on the caller's
	// own Profile). It must contain no key material.
	exportQuery := `query Q($pin: String){ myEngagement(pin: $pin){ schema profile { id } } }`
	erec := postGraphQL(t, handler, authorizeManager, exportQuery, map[string]any{"pin": "1234"})
	body := erec.Body.String()
	if strings.Contains(body, normalizeRecoveryForTest(recoveryKey)) {
		t.Fatalf("My-Data export leaked the Recovery Key: %s", body)
	}
	// The KEK/bundle refs are server-side secrets — they must never appear in the
	// export either.
	for ref := range secrets.values {
		if strings.Contains(body, ref) {
			t.Fatalf("My-Data export referenced a key-bundle secret ref %q", ref)
		}
	}
}

func sealedStatus(t *testing.T, rec *httptest.ResponseRecorder, field string) bool {
	t.Helper()
	var resp struct {
		Data   map[string]EncryptedLibraryStatusInfo `json:"data"`
		Errors []struct{ Message string }            `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v; body = %s", field, err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("%s errors = %#v; body = %s", field, resp.Errors, rec.Body.String())
	}
	return resp.Data[field].Sealed
}

// normalizeRecoveryForTest strips hyphens from a Recovery Key display string so a
// substring search catches it even if a store happened to render it ungrouped.
func normalizeRecoveryForTest(display string) string {
	return strings.ReplaceAll(display, "-", "")
}

type recordingEncryptedDBLifecycle struct {
	registered map[store.LibraryID]bool
	unseals    int
	seals      int
	rewraps    int
}

func (l *recordingEncryptedDBLifecycle) UnsealEncryptedLibraryDB(ctx context.Context, libraryID store.LibraryID, cipher store.EncryptedLibraryDBCipher) error {
	if l.registered == nil {
		l.registered = map[store.LibraryID]bool{}
	}
	l.unseals++
	l.registered[libraryID] = true
	return nil
}

func (l *recordingEncryptedDBLifecycle) EncryptedLibraryDBLifecycleAvailable() bool {
	return true
}

func (l *recordingEncryptedDBLifecycle) SealEncryptedLibraryDB(ctx context.Context, libraryID store.LibraryID, cipher store.EncryptedLibraryDBCipher) error {
	l.seals++
	delete(l.registered, libraryID)
	return nil
}

func (l *recordingEncryptedDBLifecycle) AbortEncryptedLibraryDBProvision(ctx context.Context, libraryID store.LibraryID) error {
	delete(l.registered, libraryID)
	return nil
}

func (l *recordingEncryptedDBLifecycle) RewrapEncryptedLibraryDB(ctx context.Context, libraryID store.LibraryID, rewrap func(string) error) error {
	l.rewraps++
	return nil
}

func (l *recordingEncryptedDBLifecycle) EncryptedLibraryDBRegistered(libraryID store.LibraryID) bool {
	return l.registered[libraryID]
}

func (l *recordingEncryptedDBLifecycle) EncryptedLibraryDBQuarantine(libraryID store.LibraryID) (string, bool) {
	return "", false
}
