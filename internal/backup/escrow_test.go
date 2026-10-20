package backup

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"
)

func TestBootstrapEscrowIsPrivateAndMatchesPublicWrapKey(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	escrow, err := CreateBootstrapEscrow(dataDir, now)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(bootstrapEscrowPath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("escrow mode = %o, want 600", got)
	}
	if got := escrow.ExpiresAt.Sub(escrow.CreatedAt); got != BootstrapEscrowLifetime {
		t.Fatalf("escrow lifetime = %v, want %v", got, BootstrapEscrowLifetime)
	}
	wantPublic, err := escrow.Key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	gotPublic, err := LoadPublicWrapKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPublic.Bytes(), wantPublic.Bytes()) {
		t.Fatal("stored public wrap key does not match escrowed bootstrap key")
	}

	again, err := CreateBootstrapEscrow(dataDir, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != escrow.ID || !bytes.Equal(again.Key.Bytes(), escrow.Key.Bytes()) {
		t.Fatal("unexpired bootstrap recreated instead of returning the existing escrow")
	}
}

func TestAcknowledgeBootstrapEscrowRotatesAndWipes(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	escrow, err := CreateBootstrapEscrow(dataDir, now)
	if err != nil {
		t.Fatal(err)
	}

	var acknowledged BackupKey
	if err := AcknowledgeBootstrapEscrow(dataDir, escrow.ID, func(key BackupKey) error {
		acknowledged = key
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(acknowledged.Bytes(), escrow.Key.Bytes()) {
		t.Fatal("acknowledgement reused the bootstrap key")
	}
	if _, err := os.Stat(bootstrapEscrowPath(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escrow still exists after acknowledgement: %v", err)
	}
	if _, err := os.Stat(ackJournalPath(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("acknowledgement journal still exists: %v", err)
	}
	wantPublic, err := acknowledged.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	gotPublic, err := LoadPublicWrapKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPublic.Bytes(), wantPublic.Bytes()) {
		t.Fatal("public wrap key was not rotated to acknowledged key")
	}
}

func TestAcknowledgeBootstrapEscrowCrashRecovery(t *testing.T) {
	t.Parallel()
	crashErr := errors.New("simulated crash")
	for _, crashPoint := range []string{
		"after-present",
		"after-journal",
		"after-public-key",
		"after-escrow-wipe",
	} {
		crashPoint := crashPoint
		t.Run(crashPoint, func(t *testing.T) {
			dataDir := t.TempDir()
			now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
			escrow, err := CreateBootstrapEscrow(dataDir, now)
			if err != nil {
				t.Fatal(err)
			}
			var presented BackupKey
			err = acknowledgeBootstrapEscrow(
				dataDir,
				escrow.ID,
				func(key BackupKey) error {
					presented = key
					return nil
				},
				func(point string) error {
					if point == crashPoint {
						return crashErr
					}
					return nil
				},
			)
			if !errors.Is(err, crashErr) {
				t.Fatalf("acknowledge error = %v, want simulated crash", err)
			}

			var retried BackupKey
			if err := AcknowledgeBootstrapEscrow(dataDir, escrow.ID, func(key BackupKey) error {
				retried = key
				return nil
			}); err != nil {
				t.Fatalf("retry acknowledgement: %v", err)
			}
			if len(retried.Bytes()) > 0 && !bytes.Equal(retried.Bytes(), presented.Bytes()) {
				t.Fatal("retry presented a different permanent BackupKey")
			}
			_, readErr := ReadBootstrapEscrow(dataDir, now)
			gotPublic, publicErr := LoadPublicWrapKey(dataDir)
			if publicErr != nil {
				t.Fatal(publicErr)
			}
			if !errors.Is(readErr, ErrBootstrapEscrowNotFound) {
				t.Fatalf("retry left acknowledged escrow: %v", readErr)
			}
			wantPublic, err := presented.PublicWrapKey()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gotPublic.Bytes(), wantPublic.Bytes()) {
				t.Fatal("first presented key did not become the active public recipient")
			}
			if _, err := os.Stat(ackJournalPath(dataDir)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovery left journal: %v", err)
			}
		})
	}
}

func TestAcknowledgeBootstrapEscrowAfterPresentRetryUsesSameKey(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	escrow, err := CreateBootstrapEscrow(dataDir, now)
	if err != nil {
		t.Fatal(err)
	}
	crashErr := errors.New("simulated crash")
	var first BackupKey
	err = acknowledgeBootstrapEscrow(
		dataDir,
		escrow.ID,
		func(key BackupKey) error {
			first = key
			return nil
		},
		func(point string) error {
			if point == "after-present" {
				return crashErr
			}
			return nil
		},
	)
	if !errors.Is(err, crashErr) {
		t.Fatalf("acknowledge error = %v, want simulated crash", err)
	}
	var retried BackupKey
	if err := AcknowledgeBootstrapEscrow(dataDir, escrow.ID, func(key BackupKey) error {
		retried = key
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retried.Bytes(), first.Bytes()) {
		t.Fatal("retry presented a different permanent BackupKey")
	}
	active, err := LoadPublicWrapKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := first.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(active.Bytes(), want.Bytes()) {
		t.Fatal("first presented key did not become the active backup recipient")
	}
}

func TestAcknowledgementJournalIsPrivateAndContainsNoBackupKey(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	escrow, err := CreateBootstrapEscrow(dataDir, now)
	if err != nil {
		t.Fatal(err)
	}
	crashErr := errors.New("simulated crash")
	var presented BackupKey
	err = acknowledgeBootstrapEscrow(
		dataDir,
		escrow.ID,
		func(key BackupKey) error {
			presented = key
			return nil
		},
		func(point string) error {
			if point == "after-journal" {
				return crashErr
			}
			return nil
		},
	)
	if !errors.Is(err, crashErr) {
		t.Fatalf("acknowledge error = %v, want simulated crash", err)
	}
	path := ackJournalPath(dataDir)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("journal mode = %o, want 600", got)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed test path beneath t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, presented.Bytes()) || bytes.Contains(raw, []byte(presented.Display())) {
		t.Fatal("acknowledgement journal persisted the fresh BackupKey")
	}
}

func TestBootstrapEscrowExpiryAllowsFreshBootstrap(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	expired, err := CreateBootstrapEscrow(dataDir, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBootstrapEscrow(dataDir, expired.ExpiresAt); !errors.Is(err, ErrBootstrapEscrowExpired) {
		t.Fatalf("read at expiry = %v, want ErrBootstrapEscrowExpired", err)
	}
	if _, err := os.Stat(bootstrapEscrowPath(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired escrow was not wiped: %v", err)
	}
	if _, err := LoadPublicWrapKey(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired public wrap key remained usable: %v", err)
	}

	fresh, err := CreateBootstrapEscrow(dataDir, expired.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == expired.ID || bytes.Equal(fresh.Key.Bytes(), expired.Key.Bytes()) {
		t.Fatal("re-bootstrap reused expired escrow identity or key")
	}
	wantPublic, err := fresh.Key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	gotPublic, err := LoadPublicWrapKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPublic.Bytes(), wantPublic.Bytes()) {
		t.Fatal("re-bootstrap did not rotate public wrap key")
	}
}

func TestAcknowledgePresenterFailureLeavesBootstrapUsable(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	escrow, err := CreateBootstrapEscrow(dataDir, now)
	if err != nil {
		t.Fatal(err)
	}
	presentErr := errors.New("presentation failed")
	err = AcknowledgeBootstrapEscrow(dataDir, escrow.ID, func(BackupKey) error {
		return presentErr
	})
	if !errors.Is(err, presentErr) {
		t.Fatalf("acknowledge error = %v, want presentation error", err)
	}
	got, err := ReadBootstrapEscrow(dataDir, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != escrow.ID || !bytes.Equal(got.Key.Bytes(), escrow.Key.Bytes()) {
		t.Fatal("presenter failure changed bootstrap escrow")
	}
}
