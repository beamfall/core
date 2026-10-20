package libencrypt

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
)

type libraryLease struct{ mu sync.RWMutex }

func (m *Manager) lease(libraryID string) *libraryLease {
	m.leasing.Lock()
	defer m.leasing.Unlock()
	lease := m.leases[libraryID]
	if lease == nil {
		lease = &libraryLease{}
		m.leases[libraryID] = lease
	}
	return lease
}

// acquireLibrary takes the EXCLUSIVE side of the library's ownership lease:
// every operation that mutates its key generation or Sealed state.
func (m *Manager) acquireLibrary(libraryID string) func() {
	lease := m.lease(libraryID)
	lease.mu.Lock()
	return lease.mu.Unlock
}

// acquireLibraryShared takes the SHARED side of the same lease: read-only
// queries and open decrypt streams. Shared holders never serialize against one
// another, so a slow client reading one encrypted object cannot stall status
// queries or a second stream, while every exclusive operation (provision,
// rewrap, unseal, seal) still waits for the open readers to finish. RWMutex
// gives a queued writer priority over later readers: after an exclusive
// operation starts waiting, new shared acquisitions also wait until the
// existing readers drain and the exclusive operation completes.
func (m *Manager) acquireLibraryShared(libraryID string) func() {
	lease := m.lease(libraryID)
	lease.mu.RLock()
	return lease.mu.RUnlock
}

func (m *Manager) Provision(libraryID string, mode UnsealMode, passphrase string) (RecoveryKey, error) {
	return m.ProvisionPrepared(libraryID, mode, passphrase, ProvisionHooks{})
}

func (m *Manager) Rewrap(libraryID string, mode UnsealMode, passphrase string, rewrite func(RewrapCipher) error) (RecoveryKey, error) {
	release := m.acquireLibrary(libraryID)
	defer release()
	return m.rewrapLocked(libraryID, mode, passphrase, rewrite, nil, false)
}

// RewrapPrepared keeps the ownership lease through the graph/store tail. The
// callback must precompute response metadata and complete/durably record every
// fallible external step. sealAfterCommit makes graph's sealed result an
// in-memory-only post-commit publication.
func (m *Manager) RewrapPrepared(
	libraryID string,
	mode UnsealMode,
	passphrase string,
	prepareOld func(OperationCipher) error,
	rollbackOld func(OperationCipher) error,
	rewrite func(RewrapCipher) error,
	beforeCommit func() error,
	sealAfterCommit bool,
) (RecoveryKey, error) {
	release := m.acquireLibrary(libraryID)
	defer release()
	oldDEK, err := m.dekCopyLocked(libraryID)
	if err != nil {
		return RecoveryKey{}, err
	}
	defer zero(oldDEK)
	oldFormat, err := m.atRestFormatLocked(libraryID)
	if err != nil {
		return RecoveryKey{}, err
	}
	oldCipher := OperationCipher{owner: libraryID, dek: oldDEK, format: oldFormat}
	prepared := false
	if prepareOld != nil {
		if err := prepareOld(oldCipher); err != nil {
			if rollbackOld != nil {
				err = errors.Join(err, rollbackOld(oldCipher))
			}
			return RecoveryKey{}, err
		}
		prepared = true
	}
	recovery, err := m.rewrapLocked(libraryID, mode, passphrase, rewrite, beforeCommit, sealAfterCommit)
	if err != nil && prepared && rollbackOld != nil {
		err = errors.Join(err, rollbackOld(oldCipher))
	}
	return recovery, err
}

func (m *Manager) AutoUnseal(libraryID string) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	return m.autoUnsealLocked(libraryID)
}

func (m *Manager) PassphraseUnseal(libraryID, passphrase string) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	return m.passphraseUnsealLocked(libraryID, passphrase)
}

func (m *Manager) RecoveryUnseal(libraryID, recoveryKey string) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	return m.recoveryUnsealLocked(libraryID, recoveryKey)
}

func (m *Manager) PassphraseUnsealPrepared(libraryID, passphrase string, prepare func(OperationCipher) error) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	if err := m.passphraseUnsealLocked(libraryID, passphrase); err != nil {
		return err
	}
	if err := m.runUnsealPrepareLocked(libraryID, prepare); err != nil {
		m.sealLocked(libraryID)
		return err
	}
	return nil
}

func (m *Manager) RecoveryUnsealPrepared(libraryID, recoveryKey string, prepare func(OperationCipher) error) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	if err := m.recoveryUnsealLocked(libraryID, recoveryKey); err != nil {
		return err
	}
	if err := m.runUnsealPrepareLocked(libraryID, prepare); err != nil {
		m.sealLocked(libraryID)
		return err
	}
	return nil
}

func (m *Manager) runUnsealPrepareLocked(libraryID string, prepare func(OperationCipher) error) error {
	if prepare == nil {
		return nil
	}
	dek, err := m.dekCopyLocked(libraryID)
	if err != nil {
		return err
	}
	defer zero(dek)
	format, err := m.atRestFormatLocked(libraryID)
	if err != nil {
		return err
	}
	return prepare(OperationCipher{owner: libraryID, dek: dek, format: format})
}

func (m *Manager) SealPrepared(libraryID string, prepare func(OperationCipher) error) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	if m.sealedLocked(libraryID) {
		return nil
	}
	dek, err := m.dekCopyLocked(libraryID)
	if err != nil {
		return err
	}
	defer zero(dek)
	format, err := m.atRestFormatLocked(libraryID)
	if err != nil {
		return err
	}
	if prepare != nil {
		if err := prepare(OperationCipher{owner: libraryID, dek: dek, format: format}); err != nil {
			return err
		}
	}
	m.sealLocked(libraryID)
	return nil
}

func (m *Manager) BootUnsealPrepared(libraryIDs []string, prepare func(string, OperationCipher) error) (sealed []string) {
	for _, id := range libraryIDs {
		mode, err := m.Mode(id)
		if err != nil {
			continue
		}
		if mode != UnsealModeAuto {
			sealed = append(sealed, id)
			continue
		}
		release := m.acquireLibrary(id)
		err = m.autoUnsealLocked(id)
		if err == nil {
			err = m.runUnsealPrepareLocked(id, func(cipher OperationCipher) error {
				if prepare == nil {
					return nil
				}
				return prepare(id, cipher)
			})
		}
		if err != nil {
			m.sealLocked(id)
			sealed = append(sealed, id)
		}
		release()
	}
	return sealed
}

func (m *Manager) Seal(libraryID string) {
	release := m.acquireLibrary(libraryID)
	defer release()
	m.sealLocked(libraryID)
}

func (m *Manager) Sealed(libraryID string) bool {
	release := m.acquireLibraryShared(libraryID)
	defer release()
	return m.sealedLocked(libraryID)
}

func (m *Manager) DEK(libraryID string) ([]byte, error) {
	release := m.acquireLibraryShared(libraryID)
	defer release()
	return m.dekCopyLocked(libraryID)
}

func (m *Manager) Provisioned(libraryID string) bool {
	release := m.acquireLibraryShared(libraryID)
	defer release()
	return m.provisionedLocked(libraryID)
}

func (m *Manager) Mode(libraryID string) (UnsealMode, error) {
	release := m.acquireLibraryShared(libraryID)
	defer release()
	return m.modeLocked(libraryID)
}

func (m *Manager) atRestFormat(libraryID string) (int, error) {
	release := m.acquireLibraryShared(libraryID)
	defer release()
	return m.atRestFormatLocked(libraryID)
}

func (m *Manager) KeyLocus(libraryID string) KeyLocusInfo {
	release := m.acquireLibraryShared(libraryID)
	defer release()
	return m.keyLocusLocked(libraryID)
}

func (m *Manager) EncryptFile(libraryID, plaintextPath, ciphertextPath string, obj ObjectContext) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	return m.encryptFileLocked(libraryID, plaintextPath, ciphertextPath, obj)
}

func (m *Manager) DecryptFile(libraryID, ciphertextPath, plaintextPath string, obj ObjectContext) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	return m.decryptFileLocked(libraryID, ciphertextPath, plaintextPath, obj)
}

// OpenDecryptReader holds the SHARED ownership lease for the life of the
// returned reader. Concurrent readers of the same library therefore never wait
// on each other, and neither do status queries; an exclusive operation (seal,
// unseal, rewrap) still cannot invalidate the key generation underneath an open
// stream, because it waits for the shared side to drain.
func (m *Manager) OpenDecryptReader(libraryID, ciphertextPath string, obj ObjectContext) (io.ReadSeekCloser, error) {
	release := m.acquireLibraryShared(libraryID)
	reader, err := m.openDecryptReaderLocked(libraryID, ciphertextPath, obj)
	if err != nil {
		release()
		return nil, err
	}
	leased := &leasedReadSeekCloser{ReadSeekCloser: reader, release: release}
	runtime.SetFinalizer(leased, (*leasedReadSeekCloser).finalize)
	return leased, nil
}

type leasedReadSeekCloser struct {
	io.ReadSeekCloser
	closeOnce sync.Once
	closeErr  error
	release   func()
}

func (r *leasedReadSeekCloser) Close() error {
	runtime.SetFinalizer(r, nil)
	return r.close()
}

func (r *leasedReadSeekCloser) finalize() {
	_ = r.close()
}

func (r *leasedReadSeekCloser) close() error {
	r.closeOnce.Do(func() {
		defer r.release()
		r.closeErr = r.ReadSeekCloser.Close()
	})
	return r.closeErr
}

func (m *Manager) UnsealDBFile(libraryID, ciphertextPath, workingPath string, obj ObjectContext) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	return m.unsealDBFileLocked(libraryID, ciphertextPath, workingPath, obj)
}

func (m *Manager) SealDBFile(libraryID, workingPath, ciphertextPath string, obj ObjectContext) error {
	release := m.acquireLibrary(libraryID)
	defer release()
	return m.sealDBFileLocked(libraryID, workingPath, ciphertextPath, obj)
}

// OperationCipher is bound to one owner and one candidate key generation. It
// lets graph/store preparation run while the Manager's ownership lease remains
// held, without re-entering the Manager or publishing the candidate DEK.
type OperationCipher struct {
	owner  string
	dek    []byte
	format int
}

func (c OperationCipher) UnsealDBFile(libraryID, ciphertextPath, workingPath string, obj ObjectContext) error {
	if libraryID != c.owner {
		return ErrSecretOwnership
	}
	if _, err := obj.encode(); err != nil {
		return err
	}
	return decryptFileIfPresent(ciphertextPath, workingPath, c.dek, c.format, obj)
}

func (c OperationCipher) SealDBFile(libraryID, workingPath, ciphertextPath string, obj ObjectContext) error {
	if libraryID != c.owner {
		return ErrSecretOwnership
	}
	if _, err := obj.encode(); err != nil {
		return err
	}
	if err := encryptFileWithDEK(workingPath, ciphertextPath, c.dek, c.format, obj); err != nil {
		return err
	}
	return wipeFile(workingPath)
}

func decryptFileIfPresent(ciphertextPath, workingPath string, dek []byte, format int, obj ObjectContext) error {
	if _, err := os.Stat(ciphertextPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return decryptFileWithDEK(ciphertextPath, workingPath, dek, format, obj)
}

// ProvisionHooks stages every graph/store side effect before the key commit.
// Rollback must undo Prepare if Prepare succeeded and any later key/journal write
// fails. A rollback failure leaves the uncommitted journal for startup recovery.
type ProvisionHooks struct {
	Prepare  func(OperationCipher) error
	Rollback func() error
}

type provisionJournal struct {
	Version   int       `json:"version"`
	KEK       string    `json:"kek"`
	Bundle    keyBundle `json:"bundle"`
	Committed bool      `json:"committed"`
}

const provisionJournalVersion = 1

func (m *Manager) writeProvisionJournal(libraryID, kek string, bundle keyBundle, committed bool) error {
	raw, err := json.Marshal(provisionJournal{Version: provisionJournalVersion, KEK: kek, Bundle: bundle, Committed: committed})
	if err != nil {
		return err
	}
	// The journal carries the base64 KEK: clear this in-memory copy once written
	// so the only remaining at-rest copy is the secure-store entry, which the
	// success path removes as soon as the provision is durably committed.
	defer zero(raw)
	return m.writeOwnerSecret(libraryID, secretKindProvisionJournal, string(raw))
}

func (m *Manager) clearProvisionJournal(libraryID string) error {
	return m.writeOwnerSecret(libraryID, secretKindProvisionJournal, "")
}

// ProvisionPrepared holds the owner lease across key generation, graph/store
// preparation, and the durable commit record. No fallible callback runs after
// the commit record; publication of the in-memory DEK is the only remaining
// step. A crash after commit but before response delivery is inherently
// indistinguishable from a lost single response; the Recovery Key is never
// escrowed to close that window.
func (m *Manager) ProvisionPrepared(libraryID string, mode UnsealMode, passphrase string, hooks ProvisionHooks) (RecoveryKey, error) {
	release := m.acquireLibrary(libraryID)
	defer release()
	return m.provisionLocked(libraryID, mode, passphrase, hooks)
}

func (m *Manager) provisionLocked(libraryID string, mode UnsealMode, passphrase string, hooks ProvisionHooks) (RecoveryKey, error) {
	if libraryID == "" {
		return RecoveryKey{}, errors.New("libencrypt: library id is required")
	}
	if !ValidUnsealMode(mode) {
		return RecoveryKey{}, fmt.Errorf("libencrypt: unknown unseal mode %q", mode)
	}
	if mode == UnsealModePassphrase && passphrase == "" {
		return RecoveryKey{}, errors.New("libencrypt: passphrase required for passphrase-unseal mode")
	}
	if _, err := m.loadBundle(libraryID); err == nil {
		return RecoveryKey{}, ErrAlreadyProvisioned
	} else if !errors.Is(err, ErrNotProvisioned) {
		return RecoveryKey{}, err
	}
	journalExists, err := m.ownerSecretExists(libraryID, secretKindProvisionJournal)
	if err != nil {
		return RecoveryKey{}, fmt.Errorf("libencrypt: inventory provision journal: %w", err)
	}
	if journalExists {
		return RecoveryKey{}, errors.New("libencrypt: provision recovery required before retry")
	}

	dek, err := newDEK()
	if err != nil {
		return RecoveryKey{}, err
	}
	defer func() {
		if dek != nil {
			zero(dek)
		}
	}()
	kek, err := newKEK()
	if err != nil {
		return RecoveryKey{}, err
	}
	defer zero(kek)
	recovery, err := generateRecoveryKey()
	if err != nil {
		return RecoveryKey{}, err
	}
	bundle, err := m.newBundle(mode, passphrase, dek, kek, recovery)
	if err != nil {
		return RecoveryKey{}, err
	}
	kekValue := ""
	if mode == UnsealModeAuto {
		kekValue = base64.StdEncoding.EncodeToString(kek)
	}
	if err := m.writeProvisionJournal(libraryID, kekValue, bundle, false); err != nil {
		return RecoveryKey{}, fmt.Errorf("libencrypt: persist provision journal: %w", err)
	}
	prepared := false
	rollback := func(cause error) (RecoveryKey, error) {
		if prepared && hooks.Rollback != nil {
			if rollbackErr := hooks.Rollback(); rollbackErr != nil {
				return RecoveryKey{}, errors.Join(cause, fmt.Errorf("libencrypt: provision rollback: %w", rollbackErr))
			}
		}
		var cleanupErr error
		cleanupErr = errors.Join(cleanupErr, m.writeOwnerSecret(libraryID, secretKindKEK, ""))
		cleanupErr = errors.Join(cleanupErr, m.writeOwnerSecret(libraryID, secretKindBundle, ""))
		if cleanupErr == nil {
			cleanupErr = m.clearProvisionJournal(libraryID)
		}
		return RecoveryKey{}, errors.Join(cause, cleanupErr)
	}
	if hooks.Prepare != nil {
		if err := hooks.Prepare(OperationCipher{owner: libraryID, dek: dek, format: atRestFormatV2}); err != nil {
			prepared = true // prepare may have reached a fallible tail; always compensate.
			return rollback(err)
		}
		prepared = true
	}
	if err := m.writeOwnerSecret(libraryID, secretKindKEK, kekValue); err != nil {
		return rollback(fmt.Errorf("libencrypt: persist KEK: %w", err))
	}
	if err := m.storeBundle(libraryID, bundle); err != nil {
		return rollback(err)
	}
	if err := m.writeProvisionJournal(libraryID, kekValue, bundle, true); err != nil {
		return rollback(fmt.Errorf("libencrypt: commit provision journal: %w", err))
	}

	m.mu.Lock()
	m.unsealed[libraryID] = dek
	m.mu.Unlock()
	dek = nil
	// The commit record has served its purpose: the KEK and bundle are durable,
	// so nothing is left to recover. Remove it so the secrets dir keeps no second
	// at-rest copy of the KEK, and so a later Rewrap does not leave a superseded
	// journal behind for the boot path. A failure here is not a provision
	// failure — recovery treats a committed journal as roll-forward.
	_ = m.clearProvisionJournal(libraryID)
	return recovery, nil
}

// RecoverProvisionJournals resolves crash states before any boot unseal. An
// uncommitted journal owns no key generation and is rolled back; a committed
// journal proves its exact v2 owner-bound key refs and rolls forward.
func (m *Manager) RecoverProvisionJournals(owners []string, rollbackUncommitted ...func(string) error) error {
	for _, owner := range owners {
		release := m.acquireLibrary(owner)
		exists, err := m.ownerSecretExists(owner, secretKindProvisionJournal)
		if err != nil {
			release()
			return err
		}
		if !exists {
			release()
			continue
		}
		raw, err := m.readOwnerSecret(owner, secretKindProvisionJournal)
		if err != nil {
			release()
			return err
		}
		var j provisionJournal
		if err := json.Unmarshal([]byte(raw), &j); err != nil || j.Version != provisionJournalVersion {
			release()
			return fmt.Errorf("libencrypt: corrupt provision journal for %s", owner)
		}
		if j.Committed {
			// A committed journal proves its KEK and bundle were durable BEFORE the
			// commit record landed, so only a MISSING generation is a genuinely
			// half-committed provision to roll forward. A generation that no longer
			// matches the journal is a later committed state (a Rewrap mints a fresh
			// KEK/bundle) that superseded it: the journal is stale residue and is
			// removed, never a boot refusal.
			_, err := m.loadBundle(owner)
			switch {
			case errors.Is(err, ErrNotProvisioned):
				if err := m.writeOwnerSecret(owner, secretKindKEK, j.KEK); err != nil {
					release()
					return err
				}
				if err := m.storeBundle(owner, j.Bundle); err != nil {
					release()
					return err
				}
			case err != nil:
				release()
				return err
			}
		} else {
			if len(rollbackUncommitted) != 0 && rollbackUncommitted[0] != nil {
				if err := rollbackUncommitted[0](owner); err != nil {
					release()
					return fmt.Errorf("libencrypt: recover provision preparation for %s: %w", owner, err)
				}
			}
			if err := m.writeOwnerSecret(owner, secretKindKEK, ""); err != nil {
				release()
				return err
			}
			if err := m.writeOwnerSecret(owner, secretKindBundle, ""); err != nil {
				release()
				return err
			}
		}
		if err := m.clearProvisionJournal(owner); err != nil {
			release()
			return err
		}
		release()
	}
	return nil
}

func (m *Manager) RecoverRewrapJournals(owners []string) error {
	for _, owner := range owners {
		release := m.acquireLibrary(owner)
		exists, err := m.ownerSecretExists(owner, secretKindRewrapJournal)
		if err == nil && exists {
			err = m.recoverRewrapJournal(owner)
		}
		release()
		if err != nil {
			return err
		}
	}
	return nil
}

// ProspectiveKeyLocus is response metadata computed before a key commit.
func (m *Manager) ProspectiveKeyLocus(libraryID string, mode UnsealMode) KeyLocusInfo {
	if mode != UnsealModeAuto {
		return KeyLocusInfo{Class: KeyLocusUnknown}
	}
	reporter, ok := m.secrets.(SecretLocusReporter)
	if !ok {
		return KeyLocusInfo{Class: KeyLocusUnknown}
	}
	return reporter.SecretLocus(kekRef(libraryID))
}
