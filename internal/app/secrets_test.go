package app

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/libencrypt/secretstoretest"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/store"
)

func TestSecretFilesReadsMode0600Secret(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmdb-api-key"), []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := secretFiles{dir: dir}
	secret, err := source.Secret("tmdb-api-key")
	if err != nil {
		t.Fatal(err)
	}
	if secret != "s3cret" {
		t.Fatalf("secret = %q, want trimmed s3cret", secret)
	}
}

func TestSecretFilesRefusesBroadPermissions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmdb-api-key"), []byte("s3cret"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := secretFiles{dir: dir}
	if _, err := source.Secret("tmdb-api-key"); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("err = %v, want 0600 refusal", err)
	}
	// Stricter than 0600 is fine.
	if err := os.Chmod(filepath.Join(dir, "tmdb-api-key"), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Secret("tmdb-api-key"); err != nil {
		t.Fatalf("0400 secret refused: %v", err)
	}
}

func TestSecretFilesMissingSecretIsCleanError(t *testing.T) {
	source := secretFiles{dir: t.TempDir()}
	if _, err := source.Secret("tmdb-api-key"); err == nil || !strings.Contains(err.Error(), "unavailable") || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want unavailable", err)
	}
}

func TestSecretStoreAbsenceContract(t *testing.T) {
	t.Run("secret-files", func(t *testing.T) {
		secretstoretest.AssertAbsence(t, secretFiles{dir: t.TempDir()})
	})
	t.Run("app-memory", func(t *testing.T) {
		secretstoretest.AssertAbsence(t, memSecretStore{})
	})
}

func TestSecretFilesRejectsEscapingRefs(t *testing.T) {
	dir := t.TempDir()
	source := secretFiles{dir: dir}
	for _, ref := range []string{"", "../outside", "a/b", ".", "..", ".hidden"} {
		if _, err := source.Secret(ref); err == nil {
			t.Fatalf("ref %q accepted", ref)
		}
	}
}

func TestSecretFilesRefusesEmptySecret(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blank"), []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := secretFiles{dir: dir}
	if _, err := source.Secret("blank"); err == nil {
		t.Fatal("empty secret accepted")
	}
}

// TestSecretFilesSetSecretRoundTripsAt0600 is FEAT-10 (invariant #5): the
// Console's setQBittorrentIngest password write lands in a 0600 file and reads
// back, and clearing it removes the file.
func TestSecretFilesSetSecretRoundTripsAt0600(t *testing.T) {
	dir := t.TempDir()
	source := secretFiles{dir: dir}

	if err := source.SetSecret("qbittorrent", "pw-123"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	path := filepath.Join(dir, "qbittorrent")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode = %v, want 0600", info.Mode().Perm())
	}
	got, err := source.Secret("qbittorrent")
	if err != nil || got != "pw-123" {
		t.Fatalf("round-trip = (%q, %v), want pw-123", got, err)
	}

	// Clearing removes the file (an anonymous endpoint keeps no stale secret).
	if err := source.SetSecret("qbittorrent", ""); err != nil {
		t.Fatalf("clear SetSecret: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("secret file still present after clear: %v", err)
	}

	// An escaping ref is refused, like the read path.
	if err := source.SetSecret("../escape", "x"); err == nil {
		t.Fatal("escaping ref accepted")
	}
}

func TestSecretFilesMoveSecretPublishesVerifiesThenDeletes(t *testing.T) {
	dir := t.TempDir()
	store := secretFiles{dir: dir}
	if err := store.SetSecret("libenc-bundle-legacy", "old"); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveSecret("libenc-bundle-legacy", "libenc-v2-owner-bundle", "old", "owner-bound"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Secret("libenc-v2-owner-bundle"); err != nil || got != "owner-bound" {
		t.Fatalf("destination = %q, %v", got, err)
	}
	if _, err := store.Secret("libenc-bundle-legacy"); err == nil {
		t.Fatal("verified source survived move")
	}
	// Crash-resume state: a matching destination plus source converges.
	if err := store.SetSecret("libenc-bundle-legacy", "old"); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveSecret("libenc-bundle-legacy", "libenc-v2-owner-bundle", "old", "owner-bound"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret("libenc-bundle-legacy", "old"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret("libenc-v2-owner-bundle", "mismatch"); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveSecret("libenc-bundle-legacy", "libenc-v2-owner-bundle", "old", "owner-bound"); err == nil {
		t.Fatal("destination mismatch moved source")
	}
	if got, err := store.Secret("libenc-bundle-legacy"); err != nil || got != "old" {
		t.Fatal("destination mismatch deleted recoverable source")
	}
	refs, err := store.SecretRefs("libenc-")
	if err != nil || len(refs) != 2 {
		t.Fatalf("refs = %v, %v", refs, err)
	}
}

func TestSecretFilesMoveSecretRejectsSymlinkSource(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "libenc-bundle-legacy")
	if err := os.Symlink(target, source); err != nil {
		t.Fatal(err)
	}
	store := secretFiles{dir: dir}
	if err := store.MoveSecret("libenc-bundle-legacy", "libenc-v2-owner-bundle", "secret", "owner-bound"); err == nil {
		t.Fatal("symlink source migrated")
	}
	// #nosec G304 -- target is created inside this test's private t.TempDir.
	if got, err := os.ReadFile(target); err != nil || string(got) != "secret" {
		t.Fatalf("target changed to %q, %v", got, err)
	}
}

// TestSecretFilesDurableWriteLeavesNoTempLeak checks the durable write (temp +
// rename) round-trips an overwrite and leaves no staging temp behind.
func TestSecretFilesDurableWriteLeavesNoTempLeak(t *testing.T) {
	dir := t.TempDir()
	source := secretFiles{dir: dir}

	if err := source.SetSecret("libenc-kek-lib", "v1"); err != nil {
		t.Fatalf("SetSecret v1: %v", err)
	}
	if err := source.SetSecret("libenc-kek-lib", "v2"); err != nil {
		t.Fatalf("SetSecret v2: %v", err)
	}
	got, err := source.Secret("libenc-kek-lib")
	if err != nil || got != "v2" {
		t.Fatalf("overwrite round-trip = (%q, %v), want v2", got, err)
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".libsecret-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("durable-write temps leaked: %v", temps)
	}
}

// TestSecretFilesClearMissingDoesNotCreateDir checks the clear path stays
// idempotent for an absent secret and never creates or fsyncs a missing secrets
// directory (Gate A MED — ENCPRIV-6).
func TestSecretFilesClearMissingDoesNotCreateDir(t *testing.T) {
	dir := t.TempDir()
	source := secretFiles{dir: filepath.Join(dir, "never")}

	if err := source.SetSecret("libenc-kek-absent", ""); err != nil {
		t.Fatalf("clear of missing secret must succeed: %v", err)
	}
	if _, err := os.Stat(source.dir); !os.IsNotExist(err) {
		t.Fatalf("clear must not create the secrets dir: %v", err)
	}
}

func TestSecretFilesReportsSameVolumeKeyLocus(t *testing.T) {
	dataDir := t.TempDir()
	source := secretFiles{dir: filepath.Join(dataDir, "secrets"), dataDir: dataDir}
	if err := source.SetSecret("libenc-kek-library-local", "secret"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	locus := source.SecretLocus("libenc-kek-library-local")
	if locus.Class != libencrypt.KeyLocusFileSameVolume {
		t.Fatalf("locus class = %q, want %q", locus.Class, libencrypt.KeyLocusFileSameVolume)
	}
	if !strings.Contains(locus.Disclosure, "disk theft is effectively whole-box theft") {
		t.Fatalf("disclosure = %q", locus.Disclosure)
	}
}

func TestBootstrapSetupTokenInstallsToken(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := pairing.NewService(st, nil)

	token, err := bootstrapSetupToken(context.Background(), st, svc)
	if err != nil {
		t.Fatalf("bootstrapSetupToken: %v", err)
	}
	if len(token) != 48 {
		t.Fatalf("token length = %d, want 48", len(token))
	}
	if !svc.SetupRequired() {
		t.Fatal("bootstrap token was not installed")
	}
}

func TestFirstRunSetupURL(t *testing.T) {
	got := firstRunSetupURL("http", "[::]:0", "a token")
	want := "http://127.0.0.1:0/web/setup?token=a+token"
	if got != want {
		t.Fatalf("setup URL = %q, want %q", got, want)
	}
}

// TestFirstRunSetupLogRedactsToken asserts the structured setup log line carries
// no token value. The tokened URL is for the operator console only;
// the slog record must match what Serve emits — the redacted URL — and must not
// contain the secret anywhere in its serialized form.
func TestFirstRunSetupLogRedactsToken(t *testing.T) {
	const token = "deadbeefcafef00d1234567890abcdef"
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info(
		"=== Beamfall first-run setup ===",
		"setupURL", firstRunSetupURLRedacted("http", "[::]:8800"),
		"headless", "BEAMFALL_ADMIN_PASSWORD",
	)
	line := buf.String()
	if strings.Contains(line, token) {
		t.Fatalf("setup log line leaked the token: %s", line)
	}
	if strings.Contains(line, "token="+token) || !strings.Contains(line, "token=REDACTED") {
		t.Fatalf("setup log line does not carry the redacted token marker: %s", line)
	}
}
