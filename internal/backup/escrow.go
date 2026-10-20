package backup

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// BootstrapEscrowLifetime is the maximum time a bootstrap BackupKey remains
	// recoverable from local storage.
	BootstrapEscrowLifetime = 30 * 24 * time.Hour

	bootstrapEscrowFileName = "backup-key-bootstrap-escrow.json"
	ackJournalFileName      = ".backup-key-acknowledgement.json"
	bootstrapEscrowVersion  = 1
	maxEscrowFileSize       = 4096
)

var (
	// ErrBootstrapEscrowNotFound indicates that no bootstrap credential remains.
	ErrBootstrapEscrowNotFound = errors.New("backup: bootstrap escrow not found")
	// ErrBootstrapEscrowExpired indicates that the 30-day bootstrap window ended.
	ErrBootstrapEscrowExpired = errors.New("backup: bootstrap escrow expired")
	// ErrBootstrapEscrowChanged rejects acknowledgement of a stale escrow ID.
	ErrBootstrapEscrowChanged = errors.New("backup: bootstrap escrow changed")

	bootstrapEscrowMu sync.Mutex
)

// BootstrapEscrow is a short-lived, owner-only bootstrap credential. ID must be
// supplied when acknowledging so a stale client cannot erase a newer escrow.
type BootstrapEscrow struct {
	ID        string
	Key       BackupKey
	CreatedAt time.Time
	ExpiresAt time.Time
}

type bootstrapEscrowDisk struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Key       string    `json:"backup_key"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type acknowledgementJournal struct {
	Version           int    `json:"version"`
	EscrowID          string `json:"escrow_id"`
	ReplacementPublic string `json:"replacement_public_key"`
}

// CreateBootstrapEscrow returns an existing unexpired bootstrap escrow, or
// creates a fresh one and installs its matching public wrap key. An expired
// escrow is durably wiped before a fresh key is minted.
func CreateBootstrapEscrow(dataDir string, now time.Time) (BootstrapEscrow, error) {
	bootstrapEscrowMu.Lock()
	defer bootstrapEscrowMu.Unlock()

	if err := validateEscrowDataDir(dataDir); err != nil {
		return BootstrapEscrow{}, err
	}
	if _, err := recoverAcknowledgement(dataDir); err != nil {
		return BootstrapEscrow{}, err
	}
	existing, err := readBootstrapEscrow(dataDir)
	switch {
	case err == nil && now.Before(existing.ExpiresAt):
		if err := installEscrowPublicKey(dataDir, existing); err != nil {
			return BootstrapEscrow{}, err
		}
		return existing, nil
	case err == nil:
		if err := wipeExpiredBootstrap(dataDir); err != nil {
			return BootstrapEscrow{}, fmt.Errorf("backup: wipe expired bootstrap escrow: %w", err)
		}
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

// ReadBootstrapEscrow returns the active bootstrap credential. Expired
// credentials are durably wiped and reported as ErrBootstrapEscrowExpired.
func ReadBootstrapEscrow(dataDir string, now time.Time) (BootstrapEscrow, error) {
	bootstrapEscrowMu.Lock()
	defer bootstrapEscrowMu.Unlock()

	if err := validateEscrowDataDir(dataDir); err != nil {
		return BootstrapEscrow{}, err
	}
	if _, err := recoverAcknowledgement(dataDir); err != nil {
		return BootstrapEscrow{}, err
	}
	escrow, err := readBootstrapEscrow(dataDir)
	if err != nil {
		return BootstrapEscrow{}, err
	}
	if !now.Before(escrow.ExpiresAt) {
		if err := wipeExpiredBootstrap(dataDir); err != nil {
			return BootstrapEscrow{}, fmt.Errorf("backup: wipe expired bootstrap escrow: %w", err)
		}
		return BootstrapEscrow{}, ErrBootstrapEscrowExpired
	}
	if err := installEscrowPublicKey(dataDir, escrow); err != nil {
		return BootstrapEscrow{}, err
	}
	return escrow, nil
}

func wipeExpiredBootstrap(dataDir string) error {
	if err := removeAndSync(filepath.Join(dataDir, publicWrapKeyFileName)); err != nil {
		return err
	}
	return removeAndSync(bootstrapEscrowPath(dataDir))
}

// AcknowledgeBootstrapEscrow derives the permanent BackupKey and passes it to
// present before changing durable state. The derivation is domain-separated and
// retry-stable for this escrow ID, so a crash after presentation can never
// invalidate the key already delivered to the operator. present must return nil
// only after the key has been shown or otherwise durably delivered. The key is
// never written to the acknowledgement journal.
//
// After presentation succeeds, acknowledgement rotates the public wrap key and
// durably wipes the bootstrap escrow. Recovery from an interrupted operation
// retains either the old usable escrow or the newly installed public key.
func AcknowledgeBootstrapEscrow(dataDir, escrowID string, present func(BackupKey) error) error {
	return acknowledgeBootstrapEscrow(dataDir, escrowID, present, nil)
}

func acknowledgeBootstrapEscrow(
	dataDir, escrowID string,
	present func(BackupKey) error,
	interrupt func(string) error,
) error {
	bootstrapEscrowMu.Lock()
	defer bootstrapEscrowMu.Unlock()

	if err := validateEscrowDataDir(dataDir); err != nil {
		return err
	}
	if escrowID == "" {
		return errors.New("backup: bootstrap escrow ID is required")
	}
	if present == nil {
		return errors.New("backup: bootstrap key presenter is required")
	}
	recoveredEscrowID, err := recoverAcknowledgement(dataDir)
	if err != nil {
		return err
	}
	if recoveredEscrowID == escrowID {
		return nil
	}
	escrow, err := readBootstrapEscrow(dataDir)
	if err != nil {
		return err
	}
	if escrow.ID != escrowID {
		return ErrBootstrapEscrowChanged
	}

	replacement, err := deriveAcknowledgedBackupKey(escrow.Key, escrow.ID)
	if err != nil {
		return fmt.Errorf("backup: derive acknowledged key: %w", err)
	}
	if err := present(replacement); err != nil {
		return fmt.Errorf("backup: present acknowledged key: %w", err)
	}
	if err := interruptAt(interrupt, "after-present"); err != nil {
		return err
	}
	replacementPublic, err := replacement.PublicWrapKey()
	if err != nil {
		return fmt.Errorf("backup: derive acknowledged public wrap key: %w", err)
	}
	journal := acknowledgementJournal{
		Version:           bootstrapEscrowVersion,
		EscrowID:          escrow.ID,
		ReplacementPublic: replacementPublic.String(),
	}
	if err := writeJSONPrivate(ackJournalPath(dataDir), journal); err != nil {
		return fmt.Errorf("backup: journal acknowledgement: %w", err)
	}
	if err := interruptAt(interrupt, "after-journal"); err != nil {
		return err
	}
	if _, err := StorePublicWrapKey(dataDir, replacementPublic); err != nil {
		return fmt.Errorf("backup: rotate acknowledged public wrap key: %w", err)
	}
	if err := interruptAt(interrupt, "after-public-key"); err != nil {
		return err
	}
	if err := removeAndSync(bootstrapEscrowPath(dataDir)); err != nil {
		return fmt.Errorf("backup: wipe acknowledged bootstrap escrow: %w", err)
	}
	if err := interruptAt(interrupt, "after-escrow-wipe"); err != nil {
		return err
	}
	if err := removeAndSync(ackJournalPath(dataDir)); err != nil {
		return fmt.Errorf("backup: finish acknowledgement: %w", err)
	}
	return nil
}

func recoverAcknowledgement(dataDir string) (string, error) {
	path := ackJournalPath(dataDir)
	raw, err := readPrivateFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("backup: read acknowledgement journal: %w", err)
	}
	var journal acknowledgementJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return "", fmt.Errorf("backup: decode acknowledgement journal: %w", err)
	}
	if journal.Version != bootstrapEscrowVersion || journal.EscrowID == "" {
		return "", errors.New("backup: invalid acknowledgement journal")
	}
	replacementRaw, err := base64.RawURLEncoding.DecodeString(journal.ReplacementPublic)
	if err != nil {
		return "", fmt.Errorf("backup: decode acknowledgement public key: %w", err)
	}
	replacement, err := ParsePublicWrapKey(replacementRaw)
	if err != nil {
		return "", err
	}
	current, err := LoadPublicWrapKey(dataDir)
	rotated := err == nil && current.String() == replacement.String()
	if rotated {
		escrow, escrowErr := readBootstrapEscrow(dataDir)
		if escrowErr == nil && escrow.ID == journal.EscrowID {
			if err := removeAndSync(bootstrapEscrowPath(dataDir)); err != nil {
				return "", fmt.Errorf("backup: recover acknowledgement escrow wipe: %w", err)
			}
		} else if escrowErr != nil && !errors.Is(escrowErr, ErrBootstrapEscrowNotFound) {
			return "", escrowErr
		}
	}
	if err := removeAndSync(path); err != nil {
		return "", fmt.Errorf("backup: recover acknowledgement journal: %w", err)
	}
	if rotated {
		return journal.EscrowID, nil
	}
	return "", nil
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
