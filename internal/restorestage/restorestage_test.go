package restorestage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/store"
)

// stageFixture is a server data directory with one published encrypted
// Backup Set and the backup key that opens it.
type stageFixture struct {
	dataDir   string
	backupDir string
	envelope  string
	key       string
	store     *store.Store
}

func newStageFixture(t *testing.T) stageFixture {
	t.Helper()
	ctx := context.Background()
	backupDir := t.TempDir()
	dataDir := t.TempDir()
	st, err := store.Open(ctx, dataDir, store.Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(dataDir))
	// The app provisions the backup key at start, before any backup, so the
	// set's identity files match the live ones (internal/app ensureBackupBootstrap).
	if _, err := backupcore.CreateBootstrapEscrow(dataDir, time.Now().UTC()); err != nil {
		t.Fatalf("provision backup key: %v", err)
	}
	result, err := st.Backup(ctx, backupDir)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	escrow, err := backupcore.ReadBootstrapEscrow(dataDir)
	if err != nil {
		t.Fatalf("read backup key: %v", err)
	}
	return stageFixture{dataDir: dataDir, backupDir: backupDir, envelope: result.Manifest, key: escrow.Key.Display(), store: st}
}

func TestInspectReportsContentsAndVersionsWithoutStaging(t *testing.T) {
	f := newStageFixture(t)
	stager := New(f.dataDir, f.store)
	summary, err := stager.Inspect(context.Background(), f.envelope, f.key)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !summary.Compatible || summary.Refusal != "" {
		t.Fatalf("summary = %+v, want compatible", summary)
	}
	if summary.RestoreInstructionsVersion == 0 || summary.SupportedRestoreInstructionsVersion < summary.RestoreInstructionsVersion {
		t.Fatalf("restore instructions versions = %d / %d", summary.RestoreInstructionsVersion, summary.SupportedRestoreInstructionsVersion)
	}
	names := map[string]store.BackupDatabaseSummary{}
	for _, db := range summary.Databases {
		names[db.Name] = db
	}
	for _, want := range []string{"catalog", "events"} {
		db, ok := names[want]
		if !ok || db.SchemaVersion == 0 || db.SupportedSchemaVersion < db.SchemaVersion {
			t.Fatalf("database %s = %+v (present %v)", want, db, ok)
		}
	}
	assertNoScratch(t, f.dataDir)
	if _, err := os.Lstat(filepath.Join(f.dataDir, DirName)); !os.IsNotExist(err) {
		t.Fatalf("inspect staged a restore: %v", err)
	}
}

func TestStageWritesMarkerAndCancelRemovesIt(t *testing.T) {
	f := newStageFixture(t)
	stager := New(f.dataDir, f.store)
	ctx := context.Background()
	marker, err := stager.Stage(ctx, f.envelope, f.key, false)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if marker.Envelope != f.envelope || !marker.Summary.Compatible || marker.StagedAt == "" {
		t.Fatalf("marker = %+v", marker)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, DirName, setName, "manifest.json")); err != nil {
		t.Fatalf("staged set missing: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(f.dataDir, DirName, markerName))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if strings.Contains(string(raw), f.key) {
		t.Fatal("marker stores the backup key")
	}
	pending, ok, err := Pending(f.dataDir)
	if err != nil || !ok || pending.Envelope != f.envelope {
		t.Fatalf("pending = %+v %v %v", pending, ok, err)
	}
	status, err := stager.Status()
	if err != nil || status.Staged == nil || status.LastResult != nil {
		t.Fatalf("status = %+v %v", status, err)
	}
	if _, err := stager.Stage(ctx, f.envelope, f.key, false); !errors.Is(err, ErrAlreadyStaged) {
		t.Fatalf("second stage err = %v, want ErrAlreadyStaged", err)
	}
	removed, err := stager.Cancel()
	if err != nil || !removed {
		t.Fatalf("cancel = %v %v", removed, err)
	}
	if _, ok, _ := Pending(f.dataDir); ok {
		t.Fatal("cancel left the staged restore")
	}
	if removed, err := stager.Cancel(); err != nil || removed {
		t.Fatalf("second cancel = %v %v, want false", removed, err)
	}
	assertNoScratch(t, f.dataDir)
}

func TestStageRefusesAWrongKeyAndAPathOutsideTheBackupRoots(t *testing.T) {
	f := newStageFixture(t)
	stager := New(f.dataDir, f.store)
	ctx := context.Background()
	wrong, err := backupcore.MintBackupKey()
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	var refusal RefusalError
	if _, err := stager.Stage(ctx, f.envelope, wrong.Display(), false); !errors.As(err, &refusal) {
		t.Fatalf("wrong key err = %v, want refusal", err)
	}
	if _, err := stager.Stage(ctx, f.envelope, "not a key", false); !errors.As(err, &refusal) {
		t.Fatalf("malformed key err = %v, want refusal", err)
	}
	outside := filepath.Join(t.TempDir(), "set.bbk")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := stager.Inspect(ctx, outside, f.key); !errors.As(err, &refusal) {
		t.Fatalf("outside path err = %v, want refusal", err)
	}
	if _, err := stager.Inspect(ctx, filepath.Join(filepath.Dir(f.envelope), "manifest.json"), f.key); !errors.As(err, &refusal) {
		t.Fatalf("non-envelope err = %v, want refusal", err)
	}
	if _, ok, _ := Pending(f.dataDir); ok {
		t.Fatal("a refused stage left a staged restore")
	}
	assertNoScratch(t, f.dataDir)
}

func TestStageRefusesASetFromANewerSchema(t *testing.T) {
	f := newStageFixture(t)
	newer := resealWithNewerSchema(t, f)
	stager := New(f.dataDir, f.store)
	ctx := context.Background()
	summary, err := stager.Inspect(ctx, newer, f.key)
	if err != nil {
		t.Fatalf("inspect newer set: %v", err)
	}
	if summary.Compatible || !strings.Contains(summary.Refusal, "newer than this binary supports") {
		t.Fatalf("summary = %+v, want a newer-schema refusal", summary)
	}
	_, err = stager.Stage(ctx, newer, f.key, false)
	var refusal RefusalError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "newer than this binary supports") {
		t.Fatalf("stage newer set err = %v, want a newer-schema refusal", err)
	}
	if _, ok, _ := Pending(f.dataDir); ok {
		t.Fatal("a refused stage left a staged restore")
	}
	assertNoScratch(t, f.dataDir)
}

func TestStageRefusesADifferentIdentityUnlessReplacing(t *testing.T) {
	f := newStageFixture(t)
	if err := os.WriteFile(filepath.Join(f.dataDir, "secrets", "added-after-backup"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	stager := New(f.dataDir, f.store)
	ctx := context.Background()
	_, err := stager.Stage(ctx, f.envelope, f.key, false)
	var refusal RefusalError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "secrets and certificates") {
		t.Fatalf("stage err = %v, want an identity refusal", err)
	}
	marker, err := stager.Stage(ctx, f.envelope, f.key, true)
	if err != nil || !marker.ReplaceIdentity {
		t.Fatalf("stage replacing identity = %+v %v", marker, err)
	}
}

func TestNewRemovesInterruptedScratchCopies(t *testing.T) {
	dataDir := t.TempDir()
	stale := filepath.Join(dataDir, workPrefix+"left")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	New(dataDir, nil)
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale scratch copy kept: %v", err)
	}
}

func TestClaimMovesTheStagedSetOutOfTheDataDirectory(t *testing.T) {
	f := newStageFixture(t)
	if _, err := New(f.dataDir, f.store).Stage(context.Background(), f.envelope, f.key, false); err != nil {
		t.Fatalf("stage: %v", err)
	}
	claimed, manifest, err := Claim(f.dataDir, time.Now())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(claimed) })
	if filepath.Dir(claimed) != filepath.Dir(filepath.Clean(f.dataDir)) {
		t.Fatalf("claimed %s is not a sibling of the data directory", claimed)
	}
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("claimed manifest missing: %v", err)
	}
	if _, ok, _ := Pending(f.dataDir); ok {
		t.Fatal("claimed restore is still pending")
	}
}

func assertNoScratch(t *testing.T, dataDir string) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(dataDir, workPrefix+"*"))
	if err != nil || len(left) != 0 {
		t.Fatalf("scratch copies left behind: %v %v", left, err)
	}
}

// resealWithNewerSchema publishes a copy of the fixture set whose catalog
// schema version is newer than this binary supports.
func resealWithNewerSchema(t *testing.T, f stageFixture) string {
	t.Helper()
	ctx := context.Background()
	key, err := backupcore.ParseBackupKey(f.key)
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(t.TempDir(), "plain")
	if _, err := backupcore.OpenDirectory(ctx, key, f.envelope, plain); err != nil {
		t.Fatalf("open set: %v", err)
	}
	manifestPath := filepath.Join(plain, "manifest.json")
	raw, err := os.ReadFile(manifestPath) // #nosec G304 -- test-owned temp dir
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	// Sealing requires every payload path inside the sealed directory, so
	// point the stored paths at the decrypted copy before re-reading.
	catalog := manifest["databases"].(map[string]any)["catalog"].(map[string]any)
	sealedFrom := filepath.Dir(catalog["backupPath"].(string))
	raw = []byte(strings.ReplaceAll(string(raw), sealedFrom, plain))
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	catalog = manifest["databases"].(map[string]any)["catalog"].(map[string]any)
	catalog["schemaVersion"] = 1_000_000
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	setDir := filepath.Join(f.backupDir, "newer-set")
	if err := os.Mkdir(setDir, 0o700); err != nil {
		t.Fatal(err)
	}
	wrap, err := backupcore.LoadPublicWrapKey(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	envelope := filepath.Join(setDir, envelopeName)
	if err := backupcore.SealDirectory(ctx, wrap, plain, envelope); err != nil {
		t.Fatalf("seal: %v", err)
	}
	sealed, err := os.ReadFile(envelope) // #nosec G304 -- test-owned temp dir
	if err != nil {
		t.Fatal(err)
	}
	receiptRaw, err := os.ReadFile(filepath.Join(filepath.Dir(f.envelope), "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(sealed)
	receipt["envelopeSizeBytes"] = len(sealed)
	receipt["envelopeSha256"] = hex.EncodeToString(sum[:])
	receiptRaw, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(setDir, "receipt.json"), receiptRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	return envelope
}
