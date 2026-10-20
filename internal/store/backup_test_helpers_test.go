package store

import (
	"context"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
)

// backupPlaintextForTest exercises the legacy/v2 inner manifest contract that
// remains a supported restore input. Production Backup wraps this exact set in
// BBK1; encrypted-default behavior has separate end-to-end tests.
func (s *Store) backupPlaintextForTest(ctx context.Context, dest string) (BackupResult, error) {
	return s.backupPlaintextWithOptions(ctx, dest, BackupOptions{})
}

// installBackupTestSealer keeps store tests coupled only to the store-owned
// interface while preserving real BBK1 integration coverage in one test seam.
func installBackupTestSealer(st *Store, dataDir string) {
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(dataDir))
}

func bootstrapBackupTestCredentials(t *testing.T, dataDir string, now time.Time) (SnapshotOpener, string) {
	t.Helper()
	escrow, err := backupcore.ReadBootstrapEscrow(dataDir, now)
	if err != nil {
		t.Fatal(err)
	}
	return backupcore.NewDirectoryOpener(escrow.Key), escrow.Key.Display()
}

func backupTestOpenerFromDisplay(t *testing.T, display string) SnapshotOpener {
	t.Helper()
	key, err := backupcore.ParseBackupKey(display)
	if err != nil {
		t.Fatal(err)
	}
	return backupcore.NewDirectoryOpener(key)
}
