//go:build io_e2e

package ioe2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFsStoreDatadirJourney boots the Core Server in-process and asserts the
// server data dir was materialized on disk: catalog.db + events.db (the SQLite
// catalog/events planes store.Open creates under DataDir, internal/store/
// sqlite.go). A plain Boot is the whole journey — standing up the app writes the
// managed data dir, which is exactly what fs-store-datadir credits.
//
// io:fs-store-datadir
func TestFsStoreDatadirJourney(t *testing.T) {
	_, client, dataDir := bootFS(t, "")

	// The app is live and serving: a manifest read proves the wired store is
	// answering, not just that a directory exists.
	resp := client.GET("/api/manifest")
	if resp.Status != 200 {
		t.Fatalf("GET /api/manifest status = %d, want 200\n%s", resp.Status, resp.Body())
	}

	// store.Open lays the SQLite planes under <DataDir>/db (internal/store/
	// sqlite.go: dbDir = filepath.Join(dataDir, "db")).
	for _, name := range []string{"catalog.db", "events.db"} {
		requireNonEmptyFile(t, filepath.Join(dataDir, "db", name))
	}
}

// TestFsTrashMoveJourney routes a delete through the audited server-side Trash
// over HTTP (trashMedia, Manager role) and proves it is recoverable: the media
// leaves the live catalog into the trash view, then restoreMedia brings it back
// (hard invariant 6 — destruction is recoverable by default; ADR-0012). The
// on-disk effect is the catalog.db trash state transition (store.TrashMedia
// stamps trashed_at_ms, internal/store/trash.go — the source file deliberately
// survives until the retention purge, so the catalog row IS the trash record).
//
// io:fs-trash-move
func TestFsTrashMoveJourney(t *testing.T) {
	_, client, dataDir := bootFS(t, "")
	requireNonEmptyFile(t, filepath.Join(dataDir, "db", "catalog.db"))

	const mediaID = "media-alpha" // dev-seed Media in library-local (sqlite.go demo seed)

	trashList := func() []string {
		t.Helper()
		q := `mutation($mediaID:String!){ trashMedia(mediaID:$mediaID){ id } }`
		resp := client.POST("/api/graphql", "application/json",
			graphqlBody(t, q, map[string]any{"mediaID": mediaID}))
		return decodeTrashIDs(t, resp.Body(), "trashMedia")
	}

	// Drive the audited delete: media-alpha lands in the recoverable trash.
	ids := trashList()
	if !contains(ids, mediaID) {
		t.Fatalf("after trashMedia, trash = %v, want it to contain %s", ids, mediaID)
	}

	// Recoverable: restoreMedia pulls it back out of the trash.
	rq := `mutation($mediaID:String!){ restoreMedia(mediaID:$mediaID){ id } }`
	rresp := client.POST("/api/graphql", "application/json",
		graphqlBody(t, rq, map[string]any{"mediaID": mediaID}))
	restored := decodeTrashIDs(t, rresp.Body(), "restoreMedia")
	if contains(restored, mediaID) {
		t.Fatalf("after restoreMedia, trash still contains %s: delete was not recoverable", mediaID)
	}
}

// TestFsSecretsJourney sets an operator secret over HTTP through the Console and
// asserts it lands as a 0600 file under <DataDir>/secrets (hard invariant 5 —
// secrets in 0600 files, never plaintext config). addSubscription with a
// private-feed Authorization header (a credential) routes the header through
// app.secretFiles.SetSecret (internal/app/secrets.go: os.WriteFile + Chmod
// 0o600), writing <DataDir>/secrets/subscription-auth-<id>; the catalog keeps
// only the ref, never the credential. Manager-gated — no admin password needed.
//
// io:fs-secrets-0600
func TestFsSecretsJourney(t *testing.T) {
	_, client, dataDir := bootFS(t, "")

	q := `mutation($libraryID:String!,$feedURL:String!,$root:String!,$authHeader:String!){ addSubscription(libraryID:$libraryID,feedURL:$feedURL,root:$root,authHeader:$authHeader){ id hasAuth } }`
	resp := client.POST("/api/graphql", "application/json",
		graphqlBody(t, q, map[string]any{
			"libraryID":  "library-local",
			"feedURL":    "https://feeds.example.test/private.xml",
			"root":       t.TempDir(),
			"authHeader": "Bearer ioe2e-private-feed-token",
		}))
	var out struct {
		Data struct {
			AddSubscription struct {
				ID      string `json:"id"`
				HasAuth bool   `json:"hasAuth"`
			} `json:"addSubscription"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	resp.JSON(&out)
	if len(out.Errors) != 0 {
		t.Fatalf("addSubscription errors: %+v", out.Errors)
	}
	sub := out.Data.AddSubscription
	if !sub.HasAuth || sub.ID == "" {
		t.Fatalf("addSubscription did not store a credential: %s", resp.Body())
	}

	// The Authorization header lands as a 0600 secret file keyed by the
	// per-Subscription ref (graph.subscriptionAuthRef: "subscription-auth-<id>").
	secretPath := filepath.Join(dataDir, "secrets", "subscription-auth-"+sub.ID)
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("expected secret file %s: %v", secretPath, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("secret file %s mode = %o, want 0600 (invariant 5)", secretPath, got)
	}
	if info.Size() == 0 {
		t.Fatalf("secret file %s is empty", secretPath)
	}
}

// TestFsStoreBackupJourney triggers a synchronous store backup over HTTP
// (backup(dest), Admin Session) and asserts the backup archive is written to
// disk: a beamfall-<timestamp>.db/ dir holding manifest.json + a catalog.db
// snapshot (store.Backup VACUUM INTOs the SQLite planes, internal/store/
// backup.go). The mutation completes synchronously, so no job worker is needed.
//
// io:fs-store-backup
func TestFsStoreBackupJourney(t *testing.T) {
	const adminPassword = "correct horse battery staple"
	_, client, dataDir := bootFS(t, adminPassword)
	openAdminSession(t, client, adminPassword)

	dataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatalf("resolve data dir: %v", err)
	}
	dest := filepath.Join(dataDir, "backups")
	q := `mutation($dest:String!){ backup(dest:$dest){ manifest } }`
	resp := client.POST("/api/graphql", "application/json",
		graphqlBody(t, q, map[string]any{"dest": dest}))
	var out struct {
		Data struct {
			Backup struct {
				Manifest string `json:"manifest"`
			} `json:"backup"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	resp.JSON(&out)
	if len(out.Errors) != 0 {
		t.Fatalf("backup errors: %+v", out.Errors)
	}
	manifest := out.Data.Backup.Manifest
	if manifest == "" {
		t.Fatalf("backup returned empty manifest path: %s", resp.Body())
	}
	// The returned manifest path points at the freshly written archive.
	requireNonEmptyFile(t, manifest)
	if !strings.HasPrefix(manifest, dest) {
		t.Fatalf("backup manifest %s is not under requested dest %s", manifest, dest)
	}
	// A catalog.db snapshot sits alongside the manifest in the archive dir.
	requireNonEmptyFile(t, filepath.Join(filepath.Dir(manifest), "catalog.db"))
}

// TestFsLibencryptAtRestJourney provisions and seals an Encrypted Library
// through the real GraphQL surface, then proves the store wrote ciphertext at
// rest and removed the plaintext working DB.
//
// io:fs-libencrypt-atrest
func TestFsLibencryptAtRestJourney(t *testing.T) {
	const adminPassword = "correct horse battery staple"
	_, client, dataDir := bootFS(t, adminPassword)
	openAdminSession(t, client, adminPassword)

	setBacking := `mutation($libraryID:String!,$custodial:Boolean!,$encrypted:Boolean!){ setLibraryBacking(libraryID:$libraryID,custodial:$custodial,encrypted:$encrypted){ id encrypted } }`
	resp := client.POST("/api/graphql", "application/json",
		graphqlBody(t, setBacking, map[string]any{
			"libraryID": "library-local",
			"custodial": true,
			"encrypted": true,
		}))
	requireNoGraphQLErrors(t, resp)

	provision := `mutation($libraryID:ID!){ provisionEncryptedLibrary(libraryID:$libraryID,unsealMode:"auto"){ status { provisioned sealed } recoveryKey } }`
	resp = client.POST("/api/graphql", "application/json",
		graphqlBody(t, provision, map[string]any{"libraryID": "library-local"}))
	requireNoGraphQLErrors(t, resp)

	seal := `mutation($libraryID:ID!){ sealEncryptedLibrary(libraryID:$libraryID){ provisioned sealed } }`
	resp = client.POST("/api/graphql", "application/json",
		graphqlBody(t, seal, map[string]any{"libraryID": "library-local"}))
	requireNoGraphQLErrors(t, resp)

	ciphertextPath := filepath.Join(dataDir, "db", "encrypted-library", "library-local", "encrypted_library.db.libencrypt")
	requireNonEmptyFile(t, ciphertextPath)
	ciphertext, err := os.ReadFile(ciphertextPath)
	if err != nil {
		t.Fatalf("read encrypted-library ciphertext: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("SQLite format 3")) {
		t.Fatal("encrypted-library DB ciphertext contains plaintext SQLite header")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "db", "encrypted-library-working", "library-local", "working.db")); !os.IsNotExist(err) {
		t.Fatalf("plaintext encrypted-library working DB still present: %v", err)
	}
}

// ── small local helpers (journey-specific) ────────────────────────────────────

func decodeTrashIDs(t *testing.T, body, field string) []string {
	t.Helper()
	var out struct {
		Data map[string][]struct {
			ID string `json:"id"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s response: %v\n%s", field, err, body)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("%s errors: %+v", field, out.Errors)
	}
	ids := make([]string, 0, len(out.Data[field]))
	for _, e := range out.Data[field] {
		ids = append(ids, e.ID)
	}
	return ids
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
