package app

import (
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
)

// The History "Key" column compares each set's fingerprint with the data
// directory's wrap key: empty before one is provisioned, then its fingerprint.
func TestBackupOpsCurrentKeyFingerprint(t *testing.T) {
	dataDir := t.TempDir()
	ops := backupOps{dataDir: dataDir}
	if fp, err := ops.CurrentKeyFingerprint(); err != nil || fp != "" {
		t.Fatalf("unprovisioned fingerprint = %q, %v", fp, err)
	}
	if _, err := backupcore.CreateBootstrapEscrow(dataDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	key, err := backupcore.LoadPublicWrapKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if fp, err := ops.CurrentKeyFingerprint(); err != nil || fp == "" || fp != key.Fingerprint() {
		t.Fatalf("provisioned fingerprint = %q, %v; want %q", fp, err, key.Fingerprint())
	}
}
