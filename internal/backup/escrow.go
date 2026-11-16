package backup

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// BootstrapEscrowLifetime is the save-by window recorded on a bootstrap
	// escrow. The escrow is kept past it until the owner confirms the key
	// (CORE-2175): wiping it would orphan every set sealed to that key.
	BootstrapEscrowLifetime = 30 * 24 * time.Hour

	bootstrapEscrowFileName = "backup-key-bootstrap-escrow.json"
	ackJournalFileName      = ".backup-key-acknowledgement.json"
	bootstrapEscrowVersion  = 1
	maxEscrowFileSize       = 4096
)

var (
	// ErrBootstrapEscrowNotFound indicates that no bootstrap credential remains.
	ErrBootstrapEscrowNotFound = errors.New("backup: bootstrap escrow not found")
	// ErrBootstrapEscrowChanged rejects a confirmation ID that does not name
	// the active escrow's key.
	ErrBootstrapEscrowChanged = errors.New("backup: bootstrap escrow changed")

	bootstrapEscrowMu sync.Mutex
)

// BootstrapEscrow is the owner-only bootstrap credential kept until the owner
// confirms the key. Every backup set sealed while it is pending is sealed to
// Key's public wrap key, and Key is the key the owner confirms.
type BootstrapEscrow struct {
	ID        string
	Key       BackupKey
	CreatedAt time.Time
	ExpiresAt time.Time
}

// ConfirmationID names this escrow and its key: the escrow ID and the public
// wrap key fingerprint. Acknowledging requires it, so a client that was shown
// a different key (a stale page, or an escrow ID alone) cannot confirm this one.
func (e BootstrapEscrow) ConfirmationID() (string, error) {
	public, err := e.Key.PublicWrapKey()
	if err != nil {
		return "", fmt.Errorf("backup: derive bootstrap public wrap key: %w", err)
	}
	return e.ID + confirmationIDSeparator + public.Fingerprint(), nil
}

// confirmationIDSeparator cannot occur in an escrow ID or a fingerprint, which
// are both unpadded base64url.
const confirmationIDSeparator = "."

type bootstrapEscrowDisk struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Key       string    `json:"backup_key"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// acknowledgementJournal is written only by binaries from before CORE-2175;
// recoverAcknowledgement still reads it.
type acknowledgementJournal struct {
	Version           int    `json:"version"`
	EscrowID          string `json:"escrow_id"`
	ReplacementPublic string `json:"replacement_public_key"`
}

// CreateBootstrapEscrow returns the existing bootstrap escrow, past its save-by
// date or not, or creates a fresh one, and installs its matching public wrap
// key. It mints nothing once a key has been confirmed.
func CreateBootstrapEscrow(dataDir string, now time.Time) (BootstrapEscrow, error) {
	bootstrapEscrowMu.Lock()
	defer bootstrapEscrowMu.Unlock()

	if err := validateEscrowDataDir(dataDir); err != nil {
		return BootstrapEscrow{}, err
	}
	if err := recoverAcknowledgement(dataDir); err != nil {
		return BootstrapEscrow{}, err
	}
	existing, err := readBootstrapEscrow(dataDir)
	switch {
	case err == nil:
		if err := installEscrowPublicKey(dataDir, existing); err != nil {
			return BootstrapEscrow{}, err
		}
		return existing, nil
	case !errors.Is(err, ErrBootstrapEscrowNotFound):
		return BootstrapEscrow{}, err
	default:
		if _, publicErr := LoadPublicWrapKey(dataDir); publicErr == nil {
			return BootstrapEscrow{}, ErrBootstrapEscrowNotFound
		} else if !errors.Is(publicErr, os.ErrNotExist) {
			return BootstrapEscrow{}, publicErr
		}
	}

	key, err := MintBackupKey()
	if err != nil {
		return BootstrapEscrow{}, fmt.Errorf("backup: mint bootstrap key: %w", err)
	}
	id, err := randomEscrowID()
	if err != nil {
		return BootstrapEscrow{}, err
	}
	createdAt := now.UTC()
	escrow := BootstrapEscrow{
		ID:        id,
		Key:       key,
		CreatedAt: createdAt,
		ExpiresAt: createdAt.Add(BootstrapEscrowLifetime),
	}
	if err := writeBootstrapEscrow(dataDir, escrow); err != nil {
		return BootstrapEscrow{}, err
	}
	if err := installEscrowPublicKey(dataDir, escrow); err != nil {
		// The durable escrow is intentionally retained: retrying this operation
		// repairs the public key without losing the only recoverable credential.
		return BootstrapEscrow{}, err
	}
	return escrow, nil
}

// ReadBootstrapEscrow returns the pending bootstrap credential, past its
// save-by date or not, and makes sure its public wrap key is installed.
func ReadBootstrapEscrow(dataDir string) (BootstrapEscrow, error) {
	bootstrapEscrowMu.Lock()
	defer bootstrapEscrowMu.Unlock()

	if err := validateEscrowDataDir(dataDir); err != nil {
		return BootstrapEscrow{}, err
	}
	if err := recoverAcknowledgement(dataDir); err != nil {
		return BootstrapEscrow{}, err
	}
	escrow, err := readBootstrapEscrow(dataDir)
	if err != nil {
		return BootstrapEscrow{}, err
	}
	if err := installEscrowPublicKey(dataDir, escrow); err != nil {
		return BootstrapEscrow{}, err
	}
	return escrow, nil
}

// PendingBackupKey is the backup key the owner must save before confirming
// it, and the ID that confirms it.
type PendingBackupKey struct {
	ConfirmationID string
	Key            BackupKey
	ExpiresAt      time.Time
}

// RevealBootstrapEscrow returns the pending key so the owner can save it
// before confirming (CORE-2032). It is the escrowed key itself: every backup
// set sealed while the escrow is pending, and every set sealed after
// confirming, opens with it (CORE-2175).
func RevealBootstrapEscrow(dataDir string) (PendingBackupKey, error) {
	escrow, err := ReadBootstrapEscrow(dataDir)
	if err != nil {
		return PendingBackupKey{}, err
	}
	confirmationID, err := escrow.ConfirmationID()
	if err != nil {
		return PendingBackupKey{}, err
	}
	return PendingBackupKey{ConfirmationID: confirmationID, Key: escrow.Key, ExpiresAt: escrow.ExpiresAt}, nil
}

// AcknowledgeBootstrapEscrow confirms the escrowed BackupKey named by
// confirmationID (BootstrapEscrow.ConfirmationID). It passes the key to present
// before changing durable state; present must return nil only after the key has
// been shown or otherwise durably delivered.
//
// The confirmed key is the escrowed key, not a replacement, so every set
// already sealed to it stays restorable. Acknowledgement makes sure the
// installed public wrap key is that key's, then durably wipes the escrow. The
// escrow goes last, so a crash at any point leaves either the pending escrow or
// the confirmed key's public wrap key installed, and a retry finishes the job.
// Confirming a key that is already confirmed is a no-op.
func AcknowledgeBootstrapEscrow(dataDir, confirmationID string, present func(BackupKey) error) error {
	return acknowledgeBootstrapEscrow(dataDir, confirmationID, present, nil)
}

func acknowledgeBootstrapEscrow(
	dataDir, confirmationID string,
	present func(BackupKey) error,
	interrupt func(string) error,
) error {
	bootstrapEscrowMu.Lock()
	defer bootstrapEscrowMu.Unlock()

	if err := validateEscrowDataDir(dataDir); err != nil {
		return err
	}
	if confirmationID == "" {
		return errors.New("backup: bootstrap escrow confirmation ID is required")
	}
	if present == nil {
		return errors.New("backup: bootstrap key presenter is required")
	}
	if err := recoverAcknowledgement(dataDir); err != nil {
		return err
	}
	escrow, err := readBootstrapEscrow(dataDir)
	if errors.Is(err, ErrBootstrapEscrowNotFound) && confirmedKeyInstalled(dataDir, confirmationID) {
		return nil
	}
	if err != nil {
		return err
	}
	want, err := escrow.ConfirmationID()
	if err != nil {
		return err
	}
	if confirmationID != want {
		return ErrBootstrapEscrowChanged
	}

	if err := present(escrow.Key); err != nil {
		return fmt.Errorf("backup: present acknowledged key: %w", err)
	}
	if err := interruptAt(interrupt, "after-present"); err != nil {
		return err
	}
	if err := installEscrowPublicKey(dataDir, escrow); err != nil {
		return err
	}
	if err := interruptAt(interrupt, "after-public-key"); err != nil {
		return err
	}
	if err := removeAndSync(bootstrapEscrowPath(dataDir)); err != nil {
		return fmt.Errorf("backup: wipe acknowledged bootstrap escrow: %w", err)
	}
	return nil
}

// confirmedKeyInstalled reports whether the installed public wrap key is the
// one confirmationID names, that is, whether that key was already confirmed.
func confirmedKeyInstalled(dataDir, confirmationID string) bool {
	_, fingerprint, ok := strings.Cut(confirmationID, confirmationIDSeparator)
	if !ok || fingerprint == "" {
		return false
	}
	current, err := LoadPublicWrapKey(dataDir)
	return err == nil && current.Fingerprint() == fingerprint
}

// recoverAcknowledgement finishes or abandons an acknowledgement journal left
// by a binary from before CORE-2175 that crashed mid-acknowledgement while it
// still installed a replacement key. Acknowledgement no longer writes one.
func recoverAcknowledgement(dataDir string) error {
	path := ackJournalPath(dataDir)
	raw, err := readPrivateFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("backup: read acknowledgement journal: %w", err)
	}
	var journal acknowledgementJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return fmt.Errorf("backup: decode acknowledgement journal: %w", err)
	}
	if journal.Version != bootstrapEscrowVersion || journal.EscrowID == "" {
		return errors.New("backup: invalid acknowledgement journal")
	}
	replacementRaw, err := base64.RawURLEncoding.DecodeString(journal.ReplacementPublic)
	if err != nil {
		return fmt.Errorf("backup: decode acknowledgement public key: %w", err)
	}
	replacement, err := ParsePublicWrapKey(replacementRaw)
	if err != nil {
		return err
	}
	current, err := LoadPublicWrapKey(dataDir)
	if err == nil && current.String() == replacement.String() {
		// The replacement key is installed: that acknowledgement had already
		// presented it, so finish it as the old binary would have.
		escrow, escrowErr := readBootstrapEscrow(dataDir)
		if escrowErr == nil && escrow.ID == journal.EscrowID {
			if err := removeAndSync(bootstrapEscrowPath(dataDir)); err != nil {
				return fmt.Errorf("backup: recover acknowledgement escrow wipe: %w", err)
			}
		} else if escrowErr != nil && !errors.Is(escrowErr, ErrBootstrapEscrowNotFound) {
			return escrowErr
		}
	}
	if err := removeAndSync(path); err != nil {
		return fmt.Errorf("backup: recover acknowledgement journal: %w", err)
	}
	return nil
}

func readBootstrapEscrow(dataDir string) (BootstrapEscrow, error) {
	raw, err := readPrivateFile(bootstrapEscrowPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return BootstrapEscrow{}, ErrBootstrapEscrowNotFound
	}
	if err != nil {
		return BootstrapEscrow{}, fmt.Errorf("backup: read bootstrap escrow: %w", err)
	}
	var disk bootstrapEscrowDisk
	if err := json.Unmarshal(raw, &disk); err != nil {
		return BootstrapEscrow{}, fmt.Errorf("backup: decode bootstrap escrow: %w", err)
	}
	if disk.Version != bootstrapEscrowVersion || disk.ID == "" || disk.CreatedAt.IsZero() ||
		disk.ExpiresAt.IsZero() || !disk.ExpiresAt.Equal(disk.CreatedAt.Add(BootstrapEscrowLifetime)) {
		return BootstrapEscrow{}, errors.New("backup: invalid bootstrap escrow")
	}
	key, err := ParseBackupKey(disk.Key)
	if err != nil {
		return BootstrapEscrow{}, fmt.Errorf("backup: decode bootstrap key: %w", err)
	}
	return BootstrapEscrow{
		ID:        disk.ID,
		Key:       key,
		CreatedAt: disk.CreatedAt,
		ExpiresAt: disk.ExpiresAt,
	}, nil
}

func writeBootstrapEscrow(dataDir string, escrow BootstrapEscrow) error {
	disk := bootstrapEscrowDisk{
		Version:   bootstrapEscrowVersion,
		ID:        escrow.ID,
		Key:       escrow.Key.Display(),
		CreatedAt: escrow.CreatedAt,
		ExpiresAt: escrow.ExpiresAt,
	}
	if err := writeJSONPrivate(bootstrapEscrowPath(dataDir), disk); err != nil {
		return fmt.Errorf("backup: store bootstrap escrow: %w", err)
	}
	return nil
}

func installEscrowPublicKey(dataDir string, escrow BootstrapEscrow) error {
	public, err := escrow.Key.PublicWrapKey()
	if err != nil {
		return fmt.Errorf("backup: derive bootstrap public wrap key: %w", err)
	}
	current, loadErr := LoadPublicWrapKey(dataDir)
	if loadErr == nil && current.String() == public.String() {
		return nil
	}
	if _, err := StorePublicWrapKey(dataDir, public); err != nil {
		return fmt.Errorf("backup: install bootstrap public wrap key: %w", err)
	}
	return nil
}

func randomEscrowID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("backup: generate bootstrap escrow ID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validateEscrowDataDir(dataDir string) error {
	if dataDir == "" {
		return errors.New("backup: data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("backup: create escrow directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil { //nolint:gosec // owner-only directory
		return fmt.Errorf("backup: secure escrow directory: %w", err)
	}
	secretsDir := filepath.Join(dataDir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		return fmt.Errorf("backup: create escrow secrets directory: %w", err)
	}
	if err := os.Chmod(secretsDir, 0o700); err != nil { //nolint:gosec // owner-only secret directory
		return fmt.Errorf("backup: secure escrow secrets directory: %w", err)
	}
	return nil
}

func bootstrapEscrowPath(dataDir string) string {
	return filepath.Join(dataDir, "secrets", bootstrapEscrowFileName)
}

func ackJournalPath(dataDir string) string {
	return filepath.Join(dataDir, "secrets", ackJournalFileName)
}

func writeJSONPrivate(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func readPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("backup: private state is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("backup: private state permissions %o, want 600", info.Mode().Perm())
	}
	if info.Size() > maxEscrowFileSize {
		return nil, errors.New("backup: private state exceeds size limit")
	}
	return os.ReadFile(path) //nolint:gosec // caller validated a fixed private-state path
}

func removeAndSync(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	dir, err := os.Open(path) //nolint:gosec // operator-selected data directory
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func interruptAt(interrupt func(string) error, point string) error {
	if interrupt == nil {
		return nil
	}
	return interrupt(point)
}
