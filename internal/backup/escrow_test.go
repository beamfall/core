package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
	assertActivePublicKey(t, dataDir, escrow.Key)

	again, err := CreateBootstrapEscrow(dataDir, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != escrow.ID || !bytes.Equal(again.Key.Bytes(), escrow.Key.Bytes()) {
		t.Fatal("bootstrap recreated instead of returning the existing escrow")
	}
}

func TestAcknowledgeBootstrapEscrowConfirmsTheEscrowedKeyAndWipes(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	escrow, err := CreateBootstrapEscrow(dataDir, now)
	if err != nil {
		t.Fatal(err)
	}

	var acknowledged BackupKey
	if err := AcknowledgeBootstrapEscrow(dataDir, confirmationID(t, escrow), func(key BackupKey) error {
		acknowledged = key
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(acknowledged.Bytes(), escrow.Key.Bytes()) {
		t.Fatal("acknowledgement presented a key other than the escrowed one")
	}
	if _, err := os.Stat(bootstrapEscrowPath(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escrow still exists after acknowledgement: %v", err)
	}
	if _, err := os.Stat(ackJournalPath(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("acknowledgement left a journal: %v", err)
	}
	assertActivePublicKey(t, dataDir, escrow.Key)
}

// CORE-2175: a set sealed before the owner confirms the key opens with the
// confirmed key, as does a set sealed after.
func TestAcknowledgeKeepsEarlierSetsRestorable(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	sealer := NewDirectorySealer(dataDir)
	before := sealTestSet(t, sealer, "before")

	pending, err := RevealBootstrapEscrow(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := AcknowledgeBootstrapEscrow(dataDir, pending.ConfirmationID, func(BackupKey) error { return nil }); err != nil {
		t.Fatal(err)
	}
	after := sealTestSet(t, sealer, "after")

	confirmed, err := ParseBackupKey(pending.Key.Display())
	if err != nil {
		t.Fatal(err)
	}
	assertSetOpens(t, confirmed, before, "before")
	assertSetOpens(t, confirmed, after, "after")
}

// CORE-2175: reaching the end of the window keeps the unconfirmed escrow and
// its public wrap key, so sets from either side of it open with the key the
// owner eventually confirms.
func TestBootstrapEscrowIsKeptPastItsWindowUntilConfirmed(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	escrow, err := CreateBootstrapEscrow(dataDir, start)
	if err != nil {
		t.Fatal(err)
	}
	sealer := NewDirectorySealer(dataDir)
	before := sealTestSet(t, sealer, "before")

	late := escrow.ExpiresAt.Add(24 * time.Hour)
	again, err := CreateBootstrapEscrow(dataDir, late)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != escrow.ID || !bytes.Equal(again.Key.Bytes(), escrow.Key.Bytes()) {
		t.Fatal("the end of the window replaced the unconfirmed key")
	}
	assertActivePublicKey(t, dataDir, escrow.Key)
	after := sealTestSet(t, sealer, "after")

	pending, err := RevealBootstrapEscrow(dataDir)
	if err != nil {
		t.Fatalf("reveal past the window: %v", err)
	}
	if !pending.ExpiresAt.Equal(escrow.ExpiresAt) {
		t.Fatalf("reveal save-by = %s, want %s", pending.ExpiresAt, escrow.ExpiresAt)
	}
	if err := AcknowledgeBootstrapEscrow(dataDir, pending.ConfirmationID, func(BackupKey) error { return nil }); err != nil {
		t.Fatalf("acknowledge past the window: %v", err)
	}
	assertSetOpens(t, pending.Key, before, "before")
	assertSetOpens(t, pending.Key, after, "after")
}

func TestAcknowledgeBootstrapEscrowCrashRecovery(t *testing.T) {
	t.Parallel()
	crashErr := errors.New("simulated crash")
	for _, crashPoint := range []string{"after-present", "after-public-key"} {
		t.Run(crashPoint, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			escrow, err := CreateBootstrapEscrow(dataDir, time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			before := sealTestSet(t, NewDirectorySealer(dataDir), "before")
			id := confirmationID(t, escrow)
			err = acknowledgeBootstrapEscrow(dataDir, id, func(BackupKey) error { return nil },
				func(point string) error {
					if point == crashPoint {
						return crashErr
					}
					return nil
				})
			if !errors.Is(err, crashErr) {
				t.Fatalf("acknowledge error = %v, want simulated crash", err)
			}
			// The escrow is wiped last, so it survives every crash point.
			if _, err := ReadBootstrapEscrow(dataDir); err != nil {
				t.Fatalf("crash lost the pending escrow: %v", err)
			}

			var retried BackupKey
			if err := AcknowledgeBootstrapEscrow(dataDir, id, func(key BackupKey) error {
				retried = key
				return nil
			}); err != nil {
				t.Fatalf("retry acknowledgement: %v", err)
			}
			if !bytes.Equal(retried.Bytes(), escrow.Key.Bytes()) {
				t.Fatal("retry presented a key other than the escrowed one")
			}
			if _, err := ReadBootstrapEscrow(dataDir); !errors.Is(err, ErrBootstrapEscrowNotFound) {
				t.Fatalf("retry left the acknowledged escrow: %v", err)
			}
			assertActivePublicKey(t, dataDir, escrow.Key)
			assertSetOpens(t, escrow.Key, before, "before")
		})
	}
}

func TestAcknowledgeReinstallsTheConfirmedPublicKeyBeforeWiping(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	escrow, err := CreateBootstrapEscrow(dataDir, time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	other, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, err := other.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StorePublicWrapKey(dataDir, otherPublic); err != nil {
		t.Fatal(err)
	}
	if err := AcknowledgeBootstrapEscrow(dataDir, confirmationID(t, escrow), func(BackupKey) error { return nil }); err != nil {
		t.Fatal(err)
	}
	assertActivePublicKey(t, dataDir, escrow.Key)
}

func TestAcknowledgeIsANoOpOnceTheKeyIsConfirmed(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	escrow, err := CreateBootstrapEscrow(dataDir, time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	id := confirmationID(t, escrow)
	for range 2 {
		if err := AcknowledgeBootstrapEscrow(dataDir, id, func(BackupKey) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := AcknowledgeBootstrapEscrow(dataDir, escrow.ID, func(BackupKey) error { return nil }); !errors.Is(err, ErrBootstrapEscrowNotFound) {
		t.Fatalf("bare escrow ID after confirming = %v, want ErrBootstrapEscrowNotFound", err)
	}
	assertActivePublicKey(t, dataDir, escrow.Key)
}

// A page that revealed a different key (one opened before CORE-2175 showed the
// derived replacement key) sends the bare escrow ID; it must not confirm.
func TestAcknowledgeRefusesAnIDThatDoesNotNameTheEscrowedKey(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	escrow, err := CreateBootstrapEscrow(dataDir, time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	other, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := confirmationID(t, BootstrapEscrow{ID: escrow.ID, Key: other})
	for _, id := range []string{escrow.ID, wrongKey, "not-the-escrow"} {
		err := AcknowledgeBootstrapEscrow(dataDir, id, func(BackupKey) error {
			t.Fatal("presented a key for a stale confirmation")
			return nil
		})
		if !errors.Is(err, ErrBootstrapEscrowChanged) {
			t.Fatalf("acknowledge %q = %v, want ErrBootstrapEscrowChanged", id, err)
		}
	}
	got, err := ReadBootstrapEscrow(dataDir)
	if err != nil || got.ID != escrow.ID {
		t.Fatalf("stale confirmation changed the escrow: %v", err)
	}
}

func TestAcknowledgePresenterFailureLeavesBootstrapUsable(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	escrow, err := CreateBootstrapEscrow(dataDir, time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	presentErr := errors.New("presentation failed")
	err = AcknowledgeBootstrapEscrow(dataDir, confirmationID(t, escrow), func(BackupKey) error {
		return presentErr
	})
	if !errors.Is(err, presentErr) {
		t.Fatalf("acknowledge error = %v, want presentation error", err)
	}
	got, err := ReadBootstrapEscrow(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != escrow.ID || !bytes.Equal(got.Key.Bytes(), escrow.Key.Bytes()) {
		t.Fatal("presenter failure changed bootstrap escrow")
	}
}

func TestRevealBootstrapEscrowIsTheEscrowedKey(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	escrow, err := CreateBootstrapEscrow(dataDir, time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := RevealBootstrapEscrow(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if pending.ConfirmationID != confirmationID(t, escrow) || !pending.ExpiresAt.Equal(escrow.ExpiresAt) {
		t.Fatalf("pending = %s %s, want %s %s", pending.ConfirmationID, pending.ExpiresAt, confirmationID(t, escrow), escrow.ExpiresAt)
	}
	if !bytes.Equal(pending.Key.Bytes(), escrow.Key.Bytes()) {
		t.Fatal("reveal returned a key other than the escrowed one")
	}
	var presented BackupKey
	if err := AcknowledgeBootstrapEscrow(dataDir, pending.ConfirmationID, func(key BackupKey) error {
		presented = key
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(presented.Bytes(), pending.Key.Bytes()) {
		t.Fatal("revealed key differs from the key acknowledgement confirmed")
	}
	if _, err := RevealBootstrapEscrow(dataDir); !errors.Is(err, ErrBootstrapEscrowNotFound) {
		t.Fatalf("reveal after acknowledgement = %v, want ErrBootstrapEscrowNotFound", err)
	}
}

// A binary from before CORE-2175 journaled a replacement key before
// installing it. Its journal is still recovered: finished when the replacement
// was installed, abandoned (keeping the escrow) when it was not.
func TestLegacyAcknowledgementJournalStillRecovers(t *testing.T) {
	t.Parallel()
	for _, installed := range []bool{true, false} {
		t.Run(map[bool]string{true: "replacement-installed", false: "replacement-not-installed"}[installed], func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			escrow, err := CreateBootstrapEscrow(dataDir, time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			replacement, err := MintBackupKey()
			if err != nil {
				t.Fatal(err)
			}
			replacementPublic, err := replacement.PublicWrapKey()
			if err != nil {
				t.Fatal(err)
			}
			journal := acknowledgementJournal{
				Version:           bootstrapEscrowVersion,
				EscrowID:          escrow.ID,
				ReplacementPublic: replacementPublic.String(),
			}
			if err := writeJSONPrivate(ackJournalPath(dataDir), journal); err != nil {
				t.Fatal(err)
			}
			if installed {
				if _, err := StorePublicWrapKey(dataDir, replacementPublic); err != nil {
					t.Fatal(err)
				}
			}

			got, err := ReadBootstrapEscrow(dataDir)
			if _, statErr := os.Stat(ackJournalPath(dataDir)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("recovery left the journal: %v", statErr)
			}
			if installed {
				if !errors.Is(err, ErrBootstrapEscrowNotFound) {
					t.Fatalf("read after an installed replacement = %v, want ErrBootstrapEscrowNotFound", err)
				}
				assertActivePublicKey(t, dataDir, replacement)
				return
			}
			if err != nil || got.ID != escrow.ID {
				t.Fatalf("read after an abandoned acknowledgement = %v, want the escrow", err)
			}
			assertActivePublicKey(t, dataDir, escrow.Key)
		})
	}
}

func confirmationID(t *testing.T, escrow BootstrapEscrow) string {
	t.Helper()
	id, err := escrow.ConfirmationID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertActivePublicKey(t *testing.T, dataDir string, key BackupKey) {
	t.Helper()
	want, err := key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadPublicWrapKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatal("installed public wrap key is not the expected key's")
	}
}

// sealTestSet seals a one-file directory through the production sealer and
// returns the envelope path.
func sealTestSet(t *testing.T, sealer DirectorySealer, label string) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "payload.txt"), []byte(label), 0o600); err != nil {
		t.Fatal(err)
	}
	envelope := filepath.Join(root, "set.bbk")
	if _, err := sealer.SealDirectory(context.Background(), source, envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func assertSetOpens(t *testing.T, key BackupKey, envelope, label string) {
	t.Helper()
	restore := filepath.Join(t.TempDir(), "restore")
	if err := NewDirectoryOpener(key).OpenDirectory(context.Background(), envelope, restore); err != nil {
		t.Fatalf("set %q does not open with the confirmed key: %v", label, err)
	}
	got, err := os.ReadFile(filepath.Join(restore, "payload.txt")) //nolint:gosec // test path beneath t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != label {
		t.Fatalf("set %q restored %q", label, got)
	}
}
