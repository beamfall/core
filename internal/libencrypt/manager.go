package libencrypt

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// SecretStore is the secure-store seam (ADR-0016 consumer-defined interface): the
// composition root wires the platform secure store / 0600 secretFiles into it
// (the same Secret/SetSecret shape the scrape engine and qBittorrent ingest use).
// libencrypt persists ALL its at-rest artifacts through here, never in any DB
// column (invariant #5). A ref is a flat secret name (no path separators).
type SecretStore interface {
	// Secret reads the value previously stored under ref. A missing ref is an
	// error (the caller distinguishes "not provisioned" via the typed errors),
	// and that error MUST satisfy errors.Is(err, fs.ErrNotExist): a store that
	// reports an unreadable entry as an indistinguishable generic error would let
	// a fresh provision overwrite key material it could not read.
	Secret(ref string) (string, error)
	// SetSecret writes value under ref (0600). An empty value clears it.
	SetSecret(ref, value string) error
}

// KeyLocusClass is non-secret metadata describing where auto-unseal key material
// lives relative to the encrypted data it unlocks.
type KeyLocusClass string

const (
	KeyLocusUnknown             KeyLocusClass = "unknown"
	KeyLocusPlatformSecureStore KeyLocusClass = "platform-secure-store"
	KeyLocusFileSeparateVolume  KeyLocusClass = "file-separate-volume"
	KeyLocusFileSameVolume      KeyLocusClass = "file-same-volume"
)

const (
	KeyLocusPlatformDisclosure   = "Hardware-bound secure stores protect data-dir or backup theft."
	KeyLocusSameVolumeDisclosure = "0600 fallback file is stored on the same volume as the data directory; " +
		"disk theft is effectively whole-box theft because ciphertext and KEK material travel together."
	KeyLocusSeparateVolumeDisclosure = "0600 fallback file is stored outside the data directory volume; " +
		"data-dir or backup theft without the secret file does not include the KEK material."
)

// KeyLocusInfo is safe to expose in operator status: it carries no key material,
// only the storage class and its threat-boundary disclosure.
type KeyLocusInfo struct {
	Class      KeyLocusClass
	Disclosure string
}

// SecretLocusReporter is optionally implemented by SecretStore backends that can
// describe the locus of one secret ref. Stores that do not implement it report
// unknown locus instead of weakening the key model silently.
type SecretLocusReporter interface {
	SecretLocus(ref string) KeyLocusInfo
}

// Errors the Manager returns. They never carry key material.
var (
	// ErrAlreadyProvisioned is returned when provisioning a library that already
	// has a key bundle — re-provisioning would orphan the old DEK (and thus the
	// data ENC-3 will encrypt under it). The caller must not silently clobber it.
	ErrAlreadyProvisioned = errors.New("libencrypt: library already provisioned")
	// ErrNotProvisioned is returned for an unseal/operation on a library with no
	// key bundle.
	ErrNotProvisioned = errors.New("libencrypt: library not provisioned")
	// ErrSealed is returned when an operation needs the DEK but the library is
	// Sealed (DEK not in memory).
	ErrSealed = errors.New("libencrypt: library sealed")
	// ErrRecoveryKeyConsumed is returned if the once-only Recovery Key reveal is
	// requested a second time — it is shown exactly once at provisioning and is
	// never recoverable from the server afterward.
	ErrRecoveryKeyConsumed = errors.New("libencrypt: recovery key already revealed (never escrowed)")
)

// UnsealMode is the per-library unlock mode. It is NON-SECRET
// metadata — which path unseals the DEK — and is stored by the caller (the app
// keys it in server_config), never a key itself.
type UnsealMode string

const (
	// UnsealModeAuto (the default) seals the DEK under the KEK; the server reads
	// the KEK from the secure store and auto-unseals at boot.
	UnsealModeAuto UnsealMode = "auto"
	// UnsealModePassphrase seals the DEK under an operator passphrase (argon2id);
	// the library stays Sealed at boot until the operator Unseals it. The KEK wrap
	// is still minted (so the operator can switch back / a future rotation has a
	// path), but auto-unseal does not use it in this mode.
	UnsealModePassphrase UnsealMode = "passphrase"
)

// ValidUnsealMode reports whether m is a known mode.
func ValidUnsealMode(m UnsealMode) bool {
	return m == UnsealModeAuto || m == UnsealModePassphrase
}

// keyBundle is the persisted, NON-SECRET per-library key material: the DEK
// wrapped three independent ways plus the non-secret argon2 salt/params. Every
// field is useless without one of the persisted/supplied keys (the KEK in the
// secure store for auto-mode libraries, the operator passphrase, or the user-held
// Recovery Key), so the bundle itself
// is safe to store as a 0600 secret-store entry. The plaintext DEK and the KEK
// are NEVER in here.
type keyBundle struct {
	Version int `json:"version"`
	// Mode is the per-library unseal mode (auto|passphrase). It is NON-SECRET
	// metadata co-located with the wrapped blobs so the boot path knows whether to
	// auto-unseal or stay Sealed pending a passphrase, without a DB column or a
	// server_config vocabulary stretch (invariant #5 keeps the secrets here; the
	// mode rides along as plain metadata in the same 0600 entry).
	Mode UnsealMode `json:"mode"`
	// DEKUnderKEK is the DEK wrapped (AES-GCM) by the KEK held in the secure store.
	// It is present only for auto-unseal libraries; passphrase-mode must not leave
	// disk-resident material sufficient to unwrap the DEK.
	DEKUnderKEK []byte `json:"dekUnderKek"`
	// DEKUnderPassphrase is the DEK wrapped by the argon2id-derived passphrase key.
	DEKUnderPassphrase []byte `json:"dekUnderPassphrase"`
	// DEKUnderRecovery is the DEK wrapped by the user-held Recovery Key.
	DEKUnderRecovery []byte `json:"dekUnderRecovery"`
	// PassphraseSalt and the Argon2* fields are the NON-SECRET KDF parameters
	// needed to re-derive the passphrase key on unseal.
	PassphraseSalt []byte `json:"passphraseSalt"`
	Argon2Time     uint32 `json:"argon2Time"`
	Argon2Memory   uint32 `json:"argon2Memory"`
	Argon2Threads  uint8  `json:"argon2Threads"`
	Argon2KeyLen   uint32 `json:"argon2KeyLen"`
	// AtRestFormat is the key generation's committed at-rest format
	//: every object under this DEK is exactly this
	// format. 0 (absent — a pre-v2 bundle) means atRestFormatV1. There is no
	// same-DEK partial upgrade: a v1 library reaches v2 only through the
	// all-object crash-safe Rewrap, which commits a fresh v2-only DEK; a
	// committed v2 generation permanently rejects restored v1 ciphertext.
	AtRestFormat int `json:"atRestFormat,omitempty"`
}

const keyBundleVersion = 1

// At-rest format generations (the keyBundle.AtRestFormat commitment).
const (
	// atRestFormatV1 is the legacy BFE1 chunked-AEAD format (random per-chunk
	// nonces, no object identity binding). Readable only while the library's
	// key generation is still v1.
	atRestFormatV1 = 1
	// atRestFormatV2 is the context-bound BFE2 format (atrest_v2.go). All new
	// provisions and every completed Rewrap commit to v2.
	atRestFormatV2 = 2
)

// atRestFormat normalizes the persisted commitment: bundles written before the
// field existed are v1 generations.
func (b keyBundle) atRestFormat() int {
	if b.AtRestFormat == 0 {
		return atRestFormatV1
	}
	return b.AtRestFormat
}

// Manager owns the per-library key model: it provisions DEKs + the three wraps,
// seals/unseals, and holds unsealed DEKs in memory keyed by library id. It is
// safe for concurrent use. The in-memory DEK map IS the Sealed/Unsealed state:
// a library whose id is absent is Sealed; present is Unsealed.
type Manager struct {
	secrets   SecretStore
	argon2    Argon2Params
	migration sync.Mutex
	leasing   sync.Mutex
	leases    map[string]*libraryLease

	mu       sync.RWMutex
	unsealed map[string][]byte // library id -> plaintext DEK (in memory only)
}

// RewrapCipher rewrites ciphertext from the current DEK to the freshly generated
// DEK during Recovery-Key compromise response. It never exposes either key.
// Reads use the OLD generation's committed at-rest format; every staged output
// is a context-bound v2 object under the new DEK — Rewrap is the only path from
// a v1 generation to v2 (no same-DEK partial upgrade).
type RewrapCipher struct {
	oldDEK    []byte
	newDEK    []byte
	oldFormat int
	staged    *[]stagedRewrapFile
}

// RewrapFile stages one libencrypt ciphertext file under the fresh DEK, bound
// to the caller-supplied ObjectContext (required — the caller derives it from
// persisted IDs/fingerprints, never paths). Rewrap commits new key material
// only after every staged file swaps into place. Plaintext only passes through
// memory/pipe buffers.
func (c RewrapCipher) RewrapFile(path string, obj ObjectContext) error {
	if c.staged == nil {
		return errors.New("libencrypt: rewrap file staging unavailable")
	}
	staged, err := stageRewrapFileWithDEK(path, c.oldDEK, c.newDEK, c.oldFormat, obj)
	if err != nil {
		return err
	}
	*c.staged = append(*c.staged, staged)
	return nil
}

// NewManager builds a Manager over the given secure store, using production
// argon2id params. Tests use NewManagerWithParams to swap in TestArgon2Params.
func NewManager(secrets SecretStore) *Manager {
	return NewManagerWithParams(secrets, ProdArgon2Params)
}

// NewManagerWithParams is NewManager with an explicit argon2id profile (tests
// pass TestArgon2Params for speed — never in production).
func NewManagerWithParams(secrets SecretStore, params Argon2Params) *Manager {
	return &Manager{
		secrets:  secrets,
		argon2:   params,
		leases:   map[string]*libraryLease{},
		unsealed: map[string][]byte{},
	}
}

// rewrapJournal is the single durable commit record for a re-wrap.
// Committed is the atomic commit point: while false the old key material in this
// journal is authoritative and recovery rolls BACK to it (backups are still on
// disk); once it is durably flipped to true the new KEK/bundle are already
// committed and recovery rolls FORWARD (keeps the new ciphertext, discards the
// rollback backups). The old Recovery Key is therefore valid iff the journal is
// absent or Committed==false, and is rejected exactly once Committed is durable.
type rewrapJournal struct {
	Version   int                 `json:"version"`
	OldKEK    string              `json:"oldKek"`
	OldBundle keyBundle           `json:"oldBundle"`
	Files     []rewrapJournalFile `json:"files"`
	Committed bool                `json:"committed"`
}

type rewrapJournalFile struct {
	Dst    string `json:"dst"`
	Tmp    string `json:"tmp"`
	Backup string `json:"backup"`
}

const rewrapJournalVersion = 2

// generateRewrapRecoveryKey is the Recovery Key mint seam for Rewrap. Tests
// replace it only to retain the candidate key when a pre-commit fault makes
// Rewrap correctly return no key to its caller.
var generateRewrapRecoveryKey = generateRecoveryKey

func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Rewrap generates a fresh DEK/KEK/Recovery Key, lets the caller rewrite every
// durable ciphertext class under the fresh DEK, then atomically replaces the key
// bundle. The old Recovery Key remains the only committed key until rewrite
// succeeds; after commit the old bundle is gone and the new Recovery Key is the
// only user-held recovery path.
func (m *Manager) rewrapLocked(libraryID string, mode UnsealMode, passphrase string, rewrite func(RewrapCipher) error, beforeCommit func() error, sealAfterCommit bool) (RecoveryKey, error) {
	if libraryID == "" {
		return RecoveryKey{}, errors.New("libencrypt: library id is required")
	}
	if rewrite == nil {
		return RecoveryKey{}, errors.New("libencrypt: rewrap callback is required")
	}
	if !ValidUnsealMode(mode) {
		return RecoveryKey{}, fmt.Errorf("libencrypt: unknown unseal mode %q", mode)
	}
	if mode == UnsealModePassphrase && passphrase == "" {
		return RecoveryKey{}, errors.New("libencrypt: passphrase required for passphrase-unseal mode")
	}
	if err := m.recoverRewrapJournal(libraryID); err != nil {
		return RecoveryKey{}, err
	}
	oldBundle, err := m.loadBundle(libraryID)
	if err != nil {
		return RecoveryKey{}, err
	}
	var oldKEK string
	if len(oldBundle.DEKUnderKEK) != 0 {
		oldKEK, err = m.readOwnerSecret(libraryID, secretKindKEK)
		if err != nil {
			return RecoveryKey{}, fmt.Errorf("libencrypt: KEK unavailable: %w", err)
		}
	}

	newDEK, err := newDEK()
	if err != nil {
		return RecoveryKey{}, err
	}
	newKEK, err := newKEK()
	if err != nil {
		return RecoveryKey{}, err
	}
	recovery, err := generateRewrapRecoveryKey()
	if err != nil {
		return RecoveryKey{}, err
	}
	bundle, err := m.newBundle(mode, passphrase, newDEK, newKEK, recovery)
	if err != nil {
		return RecoveryKey{}, err
	}

	// m.mu guards only the process-global unsealed map, so it is taken for the
	// snapshot here and again for the in-memory swap below — never across the
	// rewrite/beforeCommit tail. Holding it there would block every OTHER
	// library's DEK/Sealed/Provisioned for the whole re-wrap and self-deadlock a
	// callback that re-enters the Manager. This library's own entry cannot move
	// underneath us: the caller holds its exclusive ownership lease.
	oldCopy, err := m.dekCopyLocked(libraryID)
	if err != nil {
		return RecoveryKey{}, err
	}
	var staged []stagedRewrapFile
	if err := rewrite(RewrapCipher{oldDEK: oldCopy, newDEK: newDEK, oldFormat: oldBundle.atRestFormat(), staged: &staged}); err != nil {
		zero(oldCopy)
		cleanupStagedRewrapFiles(staged)
		return RecoveryKey{}, err
	}
	zero(oldCopy)

	if err := m.storeRewrapJournal(libraryID, oldKEK, oldBundle, staged); err != nil {
		cleanupStagedRewrapFiles(staged)
		return RecoveryKey{}, err
	}
	_, err = swapStagedRewrapFiles(staged)
	if err != nil {
		return RecoveryKey{}, errors.Join(err, m.rollBackRewrapFiles(libraryID, rewrapJournal{
			Version: rewrapJournalVersion, OldKEK: oldKEK, OldBundle: oldBundle,
			Files: journalFiles(staged),
		}))
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return RecoveryKey{}, errors.Join(err, m.rollBackRewrapFiles(libraryID, rewrapJournal{
				Version: rewrapJournalVersion, OldKEK: oldKEK, OldBundle: oldBundle,
				Files: journalFiles(staged),
			}))
		}
	}
	newKEKValue := ""
	if len(bundle.DEKUnderKEK) != 0 {
		newKEKValue = base64.StdEncoding.EncodeToString(newKEK)
	}
	if err := m.writeOwnerSecret(libraryID, secretKindKEK, newKEKValue); err != nil {
		cause := fmt.Errorf("libencrypt: persist KEK: %w", err)
		return RecoveryKey{}, errors.Join(cause, m.rollBackRewrapJournal(libraryID, rewrapJournal{
			Version: rewrapJournalVersion, OldKEK: oldKEK, OldBundle: oldBundle,
			Files: journalFiles(staged),
		}))
	}
	if err := m.storeBundle(libraryID, bundle); err != nil {
		return RecoveryKey{}, errors.Join(err, m.rollBackRewrapJournal(libraryID, rewrapJournal{
			Version: rewrapJournalVersion, OldKEK: oldKEK, OldBundle: oldBundle,
			Files: journalFiles(staged),
		}))
	}
	// Single atomic commit record: durably flip the journal to committed BEFORE
	// removing the rollback backups. A crash before this rolls back to the old key
	// (backups intact); a crash after rolls forward to the new key. If the commit
	// record cannot be written it is not durable, so the re-wrap is still
	// uncommitted — roll back to the old key material and fail.
	if err := m.commitRewrapJournal(libraryID, oldKEK, oldBundle, staged); err != nil {
		cause := fmt.Errorf("libencrypt: commit rewrap journal: %w", err)
		return RecoveryKey{}, errors.Join(cause, m.rollBackRewrapJournal(libraryID, rewrapJournal{
			Version: rewrapJournalVersion, OldKEK: oldKEK, OldBundle: oldBundle,
			Files: journalFiles(staged),
		}))
	}
	// Committed. Leave journal/backups to the idempotent recovery path: after the
	// commit record no fallible filesystem/secure-store tail may delay or turn
	// the one-shot Recovery-Key response into a false failure.
	m.mu.Lock()
	if oldDEK, ok := m.unsealed[libraryID]; ok {
		zero(oldDEK)
	}
	if sealAfterCommit {
		zero(newDEK)
		delete(m.unsealed, libraryID)
	} else {
		m.unsealed[libraryID] = newDEK
	}
	m.mu.Unlock()
	return recovery, nil
}

// AutoUnseal unseals a library via the KEK held in the secure store (the default
// boot path, ADR-0045 §2.4). It reads the KEK, unwraps the DEK, and holds it in
// memory. Idempotent: an already-unsealed library is a no-op success.
func (m *Manager) autoUnsealLocked(libraryID string) error {
	if !m.sealedLocked(libraryID) {
		return nil
	}
	if err := m.recoverRewrapJournal(libraryID); err != nil {
		return err
	}
	bundle, err := m.loadBundle(libraryID)
	if err != nil {
		return err
	}
	if len(bundle.DEKUnderKEK) == 0 {
		return ErrSealed
	}
	kekB64, err := m.readOwnerSecret(libraryID, secretKindKEK)
	if err != nil {
		return fmt.Errorf("libencrypt: KEK unavailable: %w", err)
	}
	kek, err := base64.StdEncoding.DecodeString(strings.TrimSpace(kekB64))
	if err != nil {
		return ErrUnwrap
	}
	dek, err := aeadUnwrap(kek, bundle.DEKUnderKEK)
	if err != nil {
		return err
	}
	m.storeDEK(libraryID, dek)
	return nil
}

// PassphraseUnseal unseals a library via the operator passphrase (argon2id), the
// opt-in passphrase-unseal path. A wrong passphrase fails with ErrUnwrap and the
// library stays Sealed. The passphrase and derived key are not retained.
func (m *Manager) passphraseUnsealLocked(libraryID, passphrase string) error {
	if err := m.recoverRewrapJournal(libraryID); err != nil {
		return err
	}
	bundle, err := m.loadBundle(libraryID)
	if err != nil {
		return err
	}
	if len(bundle.DEKUnderPassphrase) == 0 || len(bundle.PassphraseSalt) == 0 {
		return errors.New("libencrypt: library has no passphrase wrap")
	}
	params := Argon2Params{
		Time:    bundle.Argon2Time,
		Memory:  bundle.Argon2Memory,
		Threads: bundle.Argon2Threads,
		KeyLen:  bundle.Argon2KeyLen,
	}
	ppKey := derivePassphraseKey(passphrase, bundle.PassphraseSalt, params)
	dek, err := aeadUnwrap(ppKey, bundle.DEKUnderPassphrase)
	if err != nil {
		return err
	}
	m.storeDEK(libraryID, dek)
	return nil
}

// RecoveryUnseal unseals a library via the user-held Recovery Key — the path that
// works with no KEK and no passphrase (disaster recovery, ADR-0045 §2.4 / §2.9).
// A wrong Recovery Key fails with ErrUnwrap.
func (m *Manager) recoveryUnsealLocked(libraryID, recoveryKey string) error {
	if err := m.recoverRewrapJournal(libraryID); err != nil {
		return err
	}
	bundle, err := m.loadBundle(libraryID)
	if err != nil {
		return err
	}
	rk, err := ParseRecoveryKey(recoveryKey)
	if err != nil {
		return err
	}
	rkAES, err := recoveryKeyAESKey(rk.Bytes())
	if err != nil {
		return err
	}
	dek, err := aeadUnwrap(rkAES, bundle.DEKUnderRecovery)
	if err != nil {
		return err
	}
	m.storeDEK(libraryID, dek)
	return nil
}

// Seal drops the in-memory DEK, returning the library to the Sealed state. The
// wrapped blobs and KEK are untouched; the library can be unsealed again. Sealing
// an already-sealed library is a no-op.
func (m *Manager) sealLocked(libraryID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if dek, ok := m.unsealed[libraryID]; ok {
		zero(dek)
		delete(m.unsealed, libraryID)
	}
}

// Sealed reports whether the library is currently Sealed (DEK not in memory). An
// unprovisioned library reports sealed.
func (m *Manager) sealedLocked(libraryID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.unsealed[libraryID]
	return !ok
}

// DEK returns a private copy of the in-memory plaintext DEK for an UNSEALED
// library. The caller must not log or persist it. A Sealed or unprovisioned
// library returns ErrSealed. Copying prevents Seal from zeroing key material
// while an active caller is still using it.
func (m *Manager) dekCopyLocked(libraryID string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dek, ok := m.unsealed[libraryID]
	if !ok {
		return nil, ErrSealed
	}
	return append([]byte(nil), dek...), nil
}

// Provisioned reports whether a key bundle exists for the library.
func (m *Manager) provisionedLocked(libraryID string) bool {
	_, err := m.loadBundle(libraryID)
	// An exact-ref ownership mismatch is corruption, not absence. Report the
	// library as provisioned so callers keep its data plane sealed while all
	// lifecycle operations continue to return the ownership error.
	return err == nil || errors.Is(err, ErrSecretOwnership)
}

// Mode reports the library's persisted unseal mode. ErrNotProvisioned for a
// library with no bundle.
func (m *Manager) modeLocked(libraryID string) (UnsealMode, error) {
	bundle, err := m.loadBundle(libraryID)
	if err != nil {
		return "", err
	}
	return bundle.Mode, nil
}

// atRestFormat reports the library's committed at-rest format generation.
// ErrNotProvisioned for a library with no bundle.
func (m *Manager) atRestFormatLocked(libraryID string) (int, error) {
	bundle, err := m.loadBundle(libraryID)
	if err != nil {
		return 0, err
	}
	return bundle.atRestFormat(), nil
}

// KeyLocus reports where the auto-unseal KEK for a library is stored. It returns
// unknown for passphrase-mode or unprovisioned libraries because there is no
// active auto-unseal KEK locus to disclose.
func (m *Manager) keyLocusLocked(libraryID string) KeyLocusInfo {
	bundle, err := m.loadBundle(libraryID)
	if err != nil || bundle.Mode != UnsealModeAuto {
		return KeyLocusInfo{Class: KeyLocusUnknown}
	}
	reporter, ok := m.secrets.(SecretLocusReporter)
	if !ok {
		return KeyLocusInfo{Class: KeyLocusUnknown}
	}
	info := reporter.SecretLocus(kekRef(libraryID))
	if info.Class == "" {
		info.Class = KeyLocusUnknown
	}
	return info
}

// BootUnseal is the auto-unseal-at-boot path: for each
// provisioned, encrypted library id supplied by the caller, an auto-mode library
// is unsealed via its KEK, while a passphrase-mode library is left Sealed
// (absent from search/Sources until the operator Unseals it). It returns the ids
// it left Sealed (passphrase-mode or KEK-unseal failure) so the caller can
// surface "N libraries awaiting unlock". Per-library failures never abort the
// batch — one unreadable KEK must not block the rest of the server from booting.
func (m *Manager) BootUnseal(libraryIDs []string) (sealed []string) {
	for _, id := range libraryIDs {
		mode, err := m.Mode(id)
		if err != nil {
			// Not provisioned (or unreadable) — nothing to unseal; skip silently.
			continue
		}
		if mode != UnsealModeAuto {
			sealed = append(sealed, id)
			continue
		}
		if err := m.AutoUnseal(id); err != nil {
			sealed = append(sealed, id)
		}
	}
	return sealed
}

func (m *Manager) storeDEK(libraryID string, dek []byte) {
	m.mu.Lock()
	m.unsealed[libraryID] = dek
	m.mu.Unlock()
}

func (m *Manager) storeBundle(libraryID string, b keyBundle) error {
	encoded, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return m.writeOwnerSecret(libraryID, secretKindBundle, string(encoded))
}

func (m *Manager) writeRewrapJournal(libraryID, oldKEK string, oldBundle keyBundle, staged []stagedRewrapFile, committed bool) error {
	files := journalFiles(staged)
	encoded, err := json.Marshal(rewrapJournal{
		Version:   rewrapJournalVersion,
		OldKEK:    oldKEK,
		OldBundle: oldBundle,
		Files:     files,
		Committed: committed,
	})
	if err != nil {
		return err
	}
	return m.writeOwnerSecret(libraryID, secretKindRewrapJournal, string(encoded))
}

func journalFiles(staged []stagedRewrapFile) []rewrapJournalFile {
	files := make([]rewrapJournalFile, 0, len(staged))
	for _, f := range staged {
		files = append(files, rewrapJournalFile{Dst: f.dst, Tmp: f.tmp, Backup: f.tmp + ".old"})
	}
	return files
}

// storeRewrapJournal writes the pre-swap (uncommitted) journal: old key material
// is authoritative and a crash rolls back to it.
func (m *Manager) storeRewrapJournal(libraryID, oldKEK string, oldBundle keyBundle, staged []stagedRewrapFile) error {
	return m.writeRewrapJournal(libraryID, oldKEK, oldBundle, staged, false)
}

// commitRewrapJournal durably flips the journal to committed — the single atomic
// commit record. It must land (durably) before the rollback backups are removed,
// so a crash after it rolls forward to the new key material rather than back.
func (m *Manager) commitRewrapJournal(libraryID, oldKEK string, oldBundle keyBundle, staged []stagedRewrapFile) error {
	return m.writeRewrapJournal(libraryID, oldKEK, oldBundle, staged, true)
}

func (m *Manager) clearRewrapJournal(libraryID string) error {
	return m.writeOwnerSecret(libraryID, secretKindRewrapJournal, "")
}

// recoverRewrapJournal resolves an interrupted re-wrap idempotently on every
// Rewrap/unseal entry. A committed journal rolls forward to the new key material;
// an uncommitted one rolls back to the old. Absent journal is the normal case.
func (m *Manager) recoverRewrapJournal(libraryID string) error {
	exists, err := m.ownerSecretExists(libraryID, secretKindRewrapJournal)
	if err != nil || !exists {
		return err
	}
	raw, err := m.readOwnerSecret(libraryID, secretKindRewrapJournal)
	if err != nil {
		return err
	}
	var j rewrapJournal
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return fmt.Errorf("libencrypt: corrupt rewrap journal: %w", err)
	}
	if j.Version != rewrapJournalVersion {
		return fmt.Errorf("libencrypt: unsupported rewrap journal version %d", j.Version)
	}
	if j.Committed {
		return m.rollForwardRewrapJournal(libraryID, j)
	}
	return m.rollBackRewrapJournal(libraryID, j)
}

// rollForwardRewrapJournal finishes a committed re-wrap: the new KEK and bundle
// were already persisted before the commit record landed, so the new ciphertext
// in place is authoritative — never restore old key material over it. Discard the
// leftover rollback backups and staged temps, then clear the journal.
func (m *Manager) rollForwardRewrapJournal(libraryID string, j rewrapJournal) error {
	for i := len(j.Files) - 1; i >= 0; i-- {
		f := j.Files[i]
		_ = os.Remove(f.Backup)
		_ = os.Remove(f.Tmp)
	}
	return m.clearRewrapJournal(libraryID)
}

// rollBackRewrapJournal reverts an uncommitted re-wrap: restore each destination
// from its backup (an unswapped file has no backup and is already the old
// ciphertext), then restore the old KEK and bundle. Backups are guaranteed
// on disk because they are only removed after the durable commit record.
func (m *Manager) rollBackRewrapJournal(libraryID string, j rewrapJournal) error {
	if err := m.restoreRewrapFiles(j); err != nil {
		return err
	}
	if err := m.writeOwnerSecret(libraryID, secretKindKEK, j.OldKEK); err != nil {
		return fmt.Errorf("libencrypt: restore KEK: %w", err)
	}
	if err := m.storeBundle(libraryID, j.OldBundle); err != nil {
		return err
	}
	return m.clearRewrapJournal(libraryID)
}

func (m *Manager) rollBackRewrapFiles(libraryID string, j rewrapJournal) error {
	if err := m.restoreRewrapFiles(j); err != nil {
		return err
	}
	return m.clearRewrapJournal(libraryID)
}

func (m *Manager) restoreRewrapFiles(j rewrapJournal) error {
	for i := len(j.Files) - 1; i >= 0; i-- {
		f := j.Files[i]
		if _, err := os.Stat(f.Backup); err == nil {
			_ = os.Remove(f.Dst)
			if err := os.Rename(f.Backup, f.Dst); err != nil {
				return err
			}
		}
		_ = os.Remove(f.Tmp)
	}
	if err := syncRewrapJournalDirs(j.Files); err != nil {
		return err
	}
	return nil
}

func (m *Manager) newBundle(mode UnsealMode, passphrase string, dek, kek []byte, recovery RecoveryKey) (keyBundle, error) {
	var err error
	bundle := keyBundle{
		Version:       keyBundleVersion,
		Mode:          mode,
		Argon2Time:    m.argon2.Time,
		Argon2Memory:  m.argon2.Memory,
		Argon2Threads: m.argon2.Threads,
		Argon2KeyLen:  m.argon2.KeyLen,
		// Rewrap rewrites every object as context-bound v2 under the fresh DEK,
		// so the new generation commits to v2 and permanently rejects v1.
		AtRestFormat: atRestFormatV2,
	}
	if mode == UnsealModeAuto {
		if bundle.DEKUnderKEK, err = aeadWrap(kek, dek); err != nil {
			return keyBundle{}, err
		}
	}
	rkAES, err := recoveryKeyAESKey(recovery.Bytes())
	if err != nil {
		return keyBundle{}, err
	}
	if bundle.DEKUnderRecovery, err = aeadWrap(rkAES, dek); err != nil {
		return keyBundle{}, err
	}
	if passphrase != "" {
		salt, err := randomBytes(saltLen)
		if err != nil {
			return keyBundle{}, err
		}
		bundle.PassphraseSalt = salt
		ppKey := derivePassphraseKey(passphrase, salt, m.argon2)
		if bundle.DEKUnderPassphrase, err = aeadWrap(ppKey, dek); err != nil {
			return keyBundle{}, err
		}
	}
	return bundle, nil
}

// loadBundle reads + parses the key bundle. A missing bundle is ErrNotProvisioned
// so callers can distinguish "never set up" from a real read error.
func (m *Manager) loadBundle(libraryID string) (keyBundle, error) {
	raw, err := m.readOwnerSecret(libraryID, secretKindBundle)
	if err != nil {
		if errors.Is(err, ErrSecretOwnership) {
			return keyBundle{}, err
		}
		return keyBundle{}, ErrNotProvisioned
	}
	var b keyBundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return keyBundle{}, fmt.Errorf("libencrypt: corrupt key bundle: %w", err)
	}
	return b, nil
}

// zero overwrites a key buffer. Best-effort hygiene — Go's GC may have copied it,
// but clearing the live reference still shrinks the window a memory scrape sees a
// DEK after Seal.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
