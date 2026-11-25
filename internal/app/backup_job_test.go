package app

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/store"
)

func TestClassifyBackupError(t *testing.T) {
	cases := []struct {
		err  error
		want BackupFailureClass
	}{
		{&os.PathError{Op: "write", Err: syscall.ENOSPC}, BackupFailureDestinationFull},
		{&os.PathError{Op: "mkdir", Err: syscall.EACCES}, BackupFailureUnwritable},
		{&os.PathError{Op: "mkdir", Err: syscall.EROFS}, BackupFailureUnwritable},
		{errors.New("sqlite busy"), BackupFailureSnapshot},
		{&backupClassedError{class: BackupFailureIntegrity, err: errors.New("x")}, BackupFailureIntegrity},
	}
	for _, tc := range cases {
		if got := classifyBackupError(tc.err); got != tc.want {
			t.Errorf("classify(%v) = %s, want %s", tc.err, got, tc.want)
		}
	}
}

// A full destination fails the Job with the destination-full class and leaves
// every prior set intact.
func TestDatabaseBackupHandlerFullDestinationKeepsPriorBackups(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	dataDir := t.TempDir()
	st, err := store.Open(ctx, dataDir, store.Options{SeedDevelopmentData: true, AllowedRoots: []string{dest}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(dataDir))

	handler := databaseBackupHandler(st, dest, nil)
	if _, err := handler(ctx, store.Job{ID: "j1", Kind: databaseBackupJobKind}); err != nil {
		t.Fatal(err)
	}
	before, err := st.BackupHistory(ctx, dest)
	if err != nil || len(before) != 1 {
		t.Fatalf("history = %v, %v", before, err)
	}

	orig := backupFreeBytes
	backupFreeBytes = func(string) (uint64, bool) { return 0, true }
	defer func() { backupFreeBytes = orig }()

	_, err = handler(ctx, store.Job{ID: "j2", Kind: databaseBackupJobKind})
	if err == nil || classifyBackupError(err) != BackupFailureDestinationFull {
		t.Fatalf("err = %v, want destination-full", err)
	}
	after, err := st.BackupHistory(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Directory != before[0].Directory || !after[0].Valid {
		t.Fatalf("prior backup changed after failed run: before=%v after=%v", before, after)
	}
}
