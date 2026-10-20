package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/photoexport"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

// TestRetrievalAfterLapseSurvivesNoEntitlementNoAccountNoCloud (ENCPRIV-13,
// ADR-0045 §2.7/§2.9, ADR-0024 §6 carve-out) is the whole-app e2e proof that
// Custodial retrieval — view, per-photo download, and bulk export — is free
// forever and never gated by entitlement, a Beamfall Account, or cloud
// reachability. The AGPL Core never contains entitlement/account/billing code
// (invariant 7 — there is nothing to check server-side): this test instead
// proves the affirmative, that every retrieval path (playbackInfo → direct
// bytes; photoexport job) succeeds end to end using ONLY in-process wiring —
// no network dial ever leaves the process, and no Premium/Beamfall-Account
// concept is threaded through any call.
func TestRetrievalAfterLapseSurvivesNoEntitlementNoAccountNoCloud(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()

	// A dedicated Library (not the shared library-local dev seed, whose seeded
	// media-alpha row carries no on-disk file — bulk export is all-or-nothing
	// per Library, so a foreign fileless row would fail it closed) marked
	// Custodial: Beamfall holds the canonical copy, so it
	// carries the retrieval-free-forever contract of §2.7 regardless of Premium
	// billing state — there is no billing state in Core to check.
	photoLibrary, err := beamfall.store.CreateLibrary(ctx, "Photo Backup", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if err := beamfall.store.SetLibraryBacking(ctx, photoLibrary.ID, true, false); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}

	// Seed one real photo original directly onto disk and register it as a
	// Custodial Media (the fast ImportMediaFile seam already used by
	// TestMigrationWizardNFOImportReachesFilmtvSourceEndToEnd — no ffmpeg/scan
	// needed for a still image).
	photoPath := filepath.Join(t.TempDir(), "photo.jpg")
	photoBytes := []byte("byte-exact original photo bytes")
	if err := os.WriteFile(photoPath, photoBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := beamfall.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          photoLibrary.ID,
		Path:               photoPath,
		Title:              "Custodial Photo",
		Container:          "jpg",
		Class:              store.MediaClassImage,
		ContentFingerprint: "fp-encpriv-13-photo",
	})
	if err != nil {
		t.Fatalf("seed Custodial photo Media: %v", err)
	}

	srv := httptest.NewServer(beamfall.Handler())
	defer srv.Close()
	client := srv.Client()

	viewerHeaders := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-manager")
		req.Header.Set("X-Beamfall-Device", "device-mac")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	}

	// 1. VIEW: playbackInfo resolves the photo with no entitlement/account
	// header of any kind — Core's auth.Scope has no such field to set.
	infoBody := `{"mediaID":"` + string(media.ID) + `","capabilityProfile":{"client":"encpriv-13-test","containers":["jpg"],"directCodecs":[]}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/playback-info", strings.NewReader(infoBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	viewerHeaders(req)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("playback-info request: %v", err)
	}
	var info playback.InfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode playback-info: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("playback-info status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if info.Decision != "direct-play" || len(info.Streams) == 0 {
		t.Fatalf("playback-info decision = %#v, want a direct-play stream (view must succeed)", info)
	}

	// 2. PER-PHOTO DOWNLOAD: fetch the byte-exact original over the minted
	// stream URL — the "new-phone restore" / normal-download path (§2.9 mode 1).
	downloadReq, err := http.NewRequest(http.MethodGet, srv.URL+info.Streams[0].URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	viewerHeaders(downloadReq)
	downloadResp, err := client.Do(downloadReq)
	if err != nil {
		t.Fatalf("download request: %v", err)
	}
	defer downloadResp.Body.Close()
	if downloadResp.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want %d", downloadResp.StatusCode, http.StatusOK)
	}
	got := make([]byte, len(photoBytes))
	if _, err := readFull(downloadResp, got); err != nil {
		t.Fatalf("read downloaded bytes: %v", err)
	}
	if string(got) != string(photoBytes) {
		t.Fatalf("downloaded bytes = %q, want byte-exact %q", got, photoBytes)
	}

	// 3. BULK EXPORT: run the photo-export job substrate directly against the
	// live store (ADR-0045 §2.9 mode 2 — "export my photos"), the same
	// composition app.New wires (photoExport := photoexport.NewService(st, ...)).
	// No decryptor is needed: this Library is Custodial but not Encrypted, so
	// bytes are copied as-is.
	exportDest := t.TempDir()
	exportSvc := photoexport.NewService(beamfall.store, nil)
	manifest, err := exportSvc.Export(ctx, photoLibrary.ID, exportDest)
	if err != nil {
		t.Fatalf("bulk photo export: %v", err)
	}
	var exportedFile *photoexport.ManifestFile
	for _, item := range manifest.Items {
		if item.MediaID != media.ID {
			continue
		}
		for i := range item.Files {
			exportedFile = &item.Files[i]
		}
	}
	if exportedFile == nil {
		t.Fatalf("bulk export manifest missing seeded Media %s: %#v", media.ID, manifest)
	}
	exportedBytes, err := os.ReadFile(filepath.Join(exportDest, exportedFile.RelPath))
	if err != nil {
		t.Fatalf("read exported original: %v", err)
	}
	if string(exportedBytes) != string(photoBytes) {
		t.Fatalf("exported bytes = %q, want byte-exact %q", exportedBytes, photoBytes)
	}
}

// readFull drains resp.Body into buf, returning the count actually read.
func readFull(resp *http.Response, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := resp.Body.Read(buf[n:])
		n += m
		if err != nil {
			if n == len(buf) {
				return n, nil
			}
			return n, err
		}
	}
	return n, nil
}

// TestRestoreToFreshBoxUnsealsWithRecoveryKeyNoAccountNoPremium (ENCPRIV-13,
// ADR-0045 §2.4/§2.9 mode 3) proves operator disaster recovery: seal an
// Encrypted Library's metadata DB (writing at-rest ciphertext, per the already
// -proven TestFsLibencryptAtRestJourney), close the app, boot a SECOND,
// independent App instance over the SAME DataDir — a fresh box in every sense
// that matters (fresh in-memory Manager/App state; the encrypted data dir is
// the only thing carried over, exactly like restoring a Console backup to a
// new machine) — and unseal using ONLY the locally-minted Recovery Key. No
// Beamfall Account, no Premium/entitlement state, and no network call are
// involved anywhere in this path (Core has none to invoke).
func TestRestoreToFreshBoxUnsealsWithRecoveryKeyNoAccountNoPremium(t *testing.T) {
	dataDir := t.TempDir()
	adminPassword := "encpriv-13-restore-admin"

	bootBox := func() *App {
		cfg := testConfig(t)
		cfg.DataDir = dataDir
		cfg.SeedDevelopmentData = true
		t.Setenv("BEAMFALL_ADMIN_PASSWORD", adminPassword)
		a, err := New(context.Background(), cfg, nil, nil)
		if err != nil {
			t.Fatalf("New returned error: %v", err)
		}
		return a
	}
	adminHeaders := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-manager")
		req.Header.Set("X-Beamfall-Device", "device-mac")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	}
	openAdminSession := func(t *testing.T, box *App) {
		t.Helper()
		if err := box.store.SetSessionAdminUntil(context.Background(), "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
			t.Fatalf("open admin session: %v", err)
		}
	}
	graphqlBody := func(query string, vars map[string]any) string {
		body, err := json.Marshal(struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}{Query: query, Variables: vars})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	postGraphQL := func(t *testing.T, box *App, query string, vars map[string]any) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(graphqlBody(query, vars)))
		req.Header.Set("Content-Type", "application/json")
		adminHeaders(req)
		rec := httptest.NewRecorder()
		box.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("graphql status = %d: %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Data   map[string]any `json:"data"`
			Errors []struct{ Message string }
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode graphql response: %v\n%s", err, rec.Body.String())
		}
		if len(out.Errors) != 0 {
			t.Fatalf("graphql errors: %+v", out.Errors)
		}
		return out.Data
	}

	// Box #1: provision + seal an Encrypted Library, capturing the Recovery Key
	// exactly once (the only durable credential besides the data dir bytes).
	box1 := bootBox()
	openAdminSession(t, box1)
	postGraphQL(t, box1,
		`mutation($libraryID:String!,$custodial:Boolean!,$encrypted:Boolean!){ setLibraryBacking(libraryID:$libraryID,custodial:$custodial,encrypted:$encrypted){ id encrypted } }`,
		map[string]any{"libraryID": "library-local", "custodial": true, "encrypted": true})
	provisioned := postGraphQL(t, box1,
		`mutation($libraryID:ID!,$unsealMode:String!,$passphrase:String!){ provisionEncryptedLibrary(libraryID:$libraryID,unsealMode:$unsealMode,passphrase:$passphrase){ status { provisioned sealed } recoveryKey } }`,
		map[string]any{"libraryID": "library-local", "unsealMode": "passphrase", "passphrase": "box-1-only-passphrase"})
	provisionResult, ok := provisioned["provisionEncryptedLibrary"].(map[string]any)
	if !ok {
		t.Fatalf("provisionEncryptedLibrary response shape = %#v", provisioned)
	}
	recoveryKey, _ := provisionResult["recoveryKey"].(string)
	if recoveryKey == "" {
		t.Fatal("provisionEncryptedLibrary returned an empty Recovery Key")
	}
	postGraphQL(t, box1, `mutation($libraryID:ID!){ sealEncryptedLibrary(libraryID:$libraryID){ provisioned sealed } }`,
		map[string]any{"libraryID": "library-local"})

	ciphertextPath := filepath.Join(dataDir, "db", "encrypted-library", "library-local", "encrypted_library.db.libencrypt")
	if info, err := os.Stat(ciphertextPath); err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty encrypted-library ciphertext at %s: %v", ciphertextPath, err)
	}
	if err := box1.Close(); err != nil {
		t.Fatalf("close box1: %v", err)
	}

	// Box #2: a fresh App instance over the SAME DataDir — no passphrase, no
	// Beamfall Account, no Premium/entitlement state, no cloud reachable. The
	// only thing carried from box #1 is the Recovery Key the operator saved out
	// of band. Restoring the encrypted data dir (Console backups, ADR-0045
	// §2.9 mode 3) to a new box is exactly this: same bytes on disk, a new
	// process, unsealed only via Recovery Key.
	box2 := bootBox()
	defer box2.Close()
	openAdminSession(t, box2)

	statusBefore := postGraphQL(t, box2, `query($libraryID:ID!){ encryptedLibraryStatus(libraryID:$libraryID){ provisioned sealed } }`,
		map[string]any{"libraryID": "library-local"})
	before, _ := statusBefore["encryptedLibraryStatus"].(map[string]any)
	if sealed, _ := before["sealed"].(bool); !sealed {
		t.Fatalf("fresh box must start Sealed (no auto-unseal without the passphrase): %#v", before)
	}

	unsealed := postGraphQL(t, box2, `mutation($libraryID:ID!,$recoveryKey:String!){ unsealEncryptedLibrary(libraryID:$libraryID,recoveryKey:$recoveryKey){ provisioned sealed } }`,
		map[string]any{"libraryID": "library-local", "recoveryKey": recoveryKey})
	after, _ := unsealed["unsealEncryptedLibrary"].(map[string]any)
	if sealed, _ := after["sealed"].(bool); sealed {
		t.Fatalf("unsealEncryptedLibrary with the Recovery Key left the Library Sealed: %#v", after)
	}
	if provisioned, _ := after["provisioned"].(bool); !provisioned {
		t.Fatalf("unsealed status lost provisioned=true: %#v", after)
	}
}
