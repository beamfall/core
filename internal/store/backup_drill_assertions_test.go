package store

import (
	"context"
	"strings"
	"testing"
)

// The drill's Layer-1 suite passes on an untouched restore and names the
// table whose rows no longer match the snapshot (ADR-0192 §5 L1).
func TestVerifyRestoredAgainstBackupDetectsRowDrift(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st := seedBackupStore(t, dest)
	result, err := st.backupPlaintextForTest(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	restoreDir := t.TempDir()
	if _, err := RestoreBackupWithOptions(ctx, result.Manifest, restoreDir, RestoreOptions{ReplaceIdentity: true}); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, restoreDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })

	report, err := restored.VerifyRestoredAgainstBackup(ctx, result.Manifest)
	if err != nil {
		t.Fatalf("untouched restore: %v", err)
	}
	if report.Databases != 2 || report.Tables == 0 || len(report.Migrated) != 0 {
		t.Fatalf("report = %+v, want both databases and their tables compared", report)
	}

	if _, err := restored.catalogW.ExecContext(ctx, "UPDATE media SET title = title || ' (drifted)' WHERE id = (SELECT min(id) FROM media)"); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.VerifyRestoredAgainstBackup(ctx, result.Manifest); err == nil || !strings.Contains(err.Error(), "table media") {
		t.Fatalf("drifted media row: err = %v, want a media checksum mismatch", err)
	}
}
