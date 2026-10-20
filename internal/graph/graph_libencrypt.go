package graph

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/store"
)

// Encrypted-Library key model, graph half. These are
// Admin-Session-gated Console operations (operator-only) that provision a
// per-Library DEK and drive the Sealed/Unsealed lifecycle. They never echo a
// key: provisionEncryptedLibrary returns the user-held Recovery Key EXACTLY ONCE
// (the "you must save this — never escrowed" acknowledgement contract), and no
// resolver here ever returns a DEK, KEK, passphrase, or wrapped blob. The cipher
// that consumes the DEK is ENC-3; this surface only manages keys.

// LibEncryptManager is the consumer-defined seam the app wires the
// libencrypt.Manager into. It is the graph's whole view of the key model — the
// graph never sees raw key material, only these operations.
type LibEncryptManager interface {
	// Provision mints the DEK + KEK/passphrase/Recovery-Key wraps for a newly
	// encrypted library and returns the user-held Recovery Key (shown once).
	Provision(libraryID string, mode libencrypt.UnsealMode, passphrase string) (libencrypt.RecoveryKey, error)
	// PassphraseUnseal unseals a passphrase-mode library with the operator
	// passphrase. RecoveryUnseal unseals via the user-held Recovery Key.
	PassphraseUnseal(libraryID, passphrase string) error
	RecoveryUnseal(libraryID, recoveryKey string) error
	// Seal returns a library to the Sealed state (drops the in-memory DEK).
	Seal(libraryID string)
	// Sealed / Provisioned report the lifecycle state.
	Sealed(libraryID string) bool
	Provisioned(libraryID string) bool
	// Mode reports the persisted unseal mode.
	Mode(libraryID string) (libencrypt.UnsealMode, error)
	// Rewrap rotates the DEK/KEK/Recovery-Key bundle after the caller rewrites
	// all ciphertext classes with the supplied cipher.
	Rewrap(libraryID string, mode libencrypt.UnsealMode, passphrase string, rewrite func(libencrypt.RewrapCipher) error) (libencrypt.RecoveryKey, error)
}

type EncryptedLibraryDBLifecycle interface {
	EncryptedLibraryDBLifecycleAvailable() bool
	UnsealEncryptedLibraryDB(ctx context.Context, libraryID store.LibraryID, cipher store.EncryptedLibraryDBCipher) error
	SealEncryptedLibraryDB(ctx context.Context, libraryID store.LibraryID, cipher store.EncryptedLibraryDBCipher) error
	RewrapEncryptedLibraryDB(ctx context.Context, libraryID store.LibraryID, rewrap func(string) error) error
	EncryptedLibraryDBRegistered(libraryID store.LibraryID) bool
	EncryptedLibraryDBQuarantine(libraryID store.LibraryID) (string, bool)
}

type EncryptedGeneratedAssetRewrapper interface {
	RewrapEncryptedLibraryAssets(ctx context.Context, files []store.MediaFile, rewrap func(path, contentFingerprint, assetName string) error) error
}

type preparedLibEncryptManager interface {
	ProvisionPrepared(string, libencrypt.UnsealMode, string, libencrypt.ProvisionHooks) (libencrypt.RecoveryKey, error)
	RewrapPrepared(string, libencrypt.UnsealMode, string, func(libencrypt.OperationCipher) error, func(libencrypt.OperationCipher) error, func(libencrypt.RewrapCipher) error, func() error, bool) (libencrypt.RecoveryKey, error)
	ProspectiveKeyLocus(string, libencrypt.UnsealMode) libencrypt.KeyLocusInfo
	PassphraseUnsealPrepared(string, string, func(libencrypt.OperationCipher) error) error
	RecoveryUnsealPrepared(string, string, func(libencrypt.OperationCipher) error) error
	SealPrepared(string, func(libencrypt.OperationCipher) error) error
}

type encryptedLibraryProvisionAborter interface {
	AbortEncryptedLibraryDBProvision(context.Context, store.LibraryID) error
}

// SetLibEncrypt wires the key-management seam. Nil (the default) leaves the
// provisioning/unseal mutations reporting the feature unavailable.
func (h *Handler) SetLibEncrypt(mgr LibEncryptManager) {
	h.libEncrypt = mgr
}

func (h *Handler) SetEncryptedLibraryDBLifecycle(lifecycle EncryptedLibraryDBLifecycle) {
	h.encryptedLibraryDBLifecycle = lifecycle
}

func (h *Handler) SetEncryptedGeneratedAssets(rewrapper EncryptedGeneratedAssetRewrapper) {
	h.encryptedGeneratedAssets = rewrapper
}

// librarySealed reports whether a Library is currently Sealed (ADR-0098 area 2).
// Only an ENCRYPTED (provisioned) Library can be Sealed: Manager.Sealed reports
// "DEK not in memory", which is also true for every ordinary unprovisioned
// Library, so gate on Provisioned first or every plain Library would read as
// sealed. Returns false when no libEncrypt seam is wired (nothing is sealed).
func (h *Handler) librarySealed(libraryID store.LibraryID) bool {
	if h.libEncrypt == nil {
		return false
	}
	id := string(libraryID)
	return h.libEncrypt.Provisioned(id) && h.libEncrypt.Sealed(id)
}

// sealedFilter stamps the caller's currently-Sealed Library IDs onto a base
// store read filter (LibraryFilter.SealedIDs), so the store's Sealed gate
// (store.libraryVisible) drops every Sealed Library's Media on the data-plane
// read. It is the generic, domain-agnostic chokepoint for data-plane reads that
// MUST honour the "a Sealed Library serves NOTHING" invariant but are not byte
// routes (offline-pack build, Collection resolve). It enumerates the caller's
// visible Libraries and marks the sealed ones. When no libEncrypt seam is wired
// no Library is Sealed, so the base filter is returned unchanged.
func (h *Handler) sealedFilter(ctx context.Context, base store.LibraryFilter) (store.LibraryFilter, error) {
	if h.libEncrypt == nil {
		return base, nil
	}
	libraries, err := h.store.Libraries(ctx, base)
	if err != nil {
		return store.LibraryFilter{}, err
	}
	var sealedIDs []store.LibraryID
	for _, lib := range libraries {
		if h.librarySealed(lib.ID) {
			sealedIDs = append(sealedIDs, lib.ID)
		}
	}
	base.SealedIDs = sealedIDs
	return base, nil
}

// EncryptedLibraryStatusInfo is the credential-free Console view of one
// Library's key-model state. It carries NO key material — only
// the lifecycle flags and the non-secret unseal mode.
type EncryptedLibraryStatusInfo struct {
	LibraryID   string `json:"libraryID"`
	Provisioned bool   `json:"provisioned"`
	Sealed      bool   `json:"sealed"`
	// UnsealMode is "auto" or "passphrase"; empty when not provisioned.
	UnsealMode string `json:"unsealMode,omitempty"`
	// KeyLocusClass is where the auto-unseal KEK lives; empty when not applicable.
	KeyLocusClass string `json:"keyLocusClass,omitempty"`
	// KeyLocusDisclosure explains the protection case for the reported locus.
	KeyLocusDisclosure string `json:"keyLocusDisclosure,omitempty"`
}

// ProvisionEncryptedLibraryInfo is the once-only provisioning result. RecoveryKey
// is the user-held, License-Key-shaped display string surfaced EXACTLY ONCE: the
// server stores nothing that can reproduce it (ADR-0045 §2.4, never escrowed).
// The acknowledged flag is the contract reminder — the client MUST gate backup
// enablement on the operator confirming they saved this.
type ProvisionEncryptedLibraryInfo struct {
	Status EncryptedLibraryStatusInfo `json:"status"`
	// RecoveryKey is returned exactly once. It is never logged and never
	// re-fetchable.
	RecoveryKey string `json:"recoveryKey"`
	// AcknowledgementRequired is always true: it documents that the client must
	// present the hard "you must save this — it is never escrowed and cannot be
	// recovered" gate before backup enables.
	AcknowledgementRequired bool                         `json:"acknowledgementRequired"`
	BackupRotationArtifacts []BackupRotationArtifactInfo `json:"backupRotationArtifacts,omitempty"`
	ResidualRiskNotice      string                       `json:"residualRiskNotice,omitempty"`
}

type BackupRotationArtifactInfo struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Manifest string `json:"manifest,omitempty"`
	Action   string `json:"action"`
}

// provisionEncryptedLibrary mints the key model for a Library the operator has
// flagged Encrypted and returns the Recovery Key once. Admin-Session-
// gated; the Library must be visible to the caller and actually flagged
// encrypted. Re-provisioning is refused (it would orphan the existing DEK).
func (h *Handler) provisionEncryptedLibrary(ctx context.Context, scope auth.Scope, req request) (ProvisionEncryptedLibraryInfo, error) {
	if h.libEncrypt == nil {
		return ProvisionEncryptedLibraryInfo{}, clientError("encrypted-library key management is unavailable on this server")
	}
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return ProvisionEncryptedLibraryInfo{}, clientError("libraryID is required")
	}
	library, ok, err := h.encryptedLibrary(ctx, scope, libraryID)
	if err != nil {
		return ProvisionEncryptedLibraryInfo{}, err
	}
	if !ok {
		return ProvisionEncryptedLibraryInfo{}, clientError("unknown library")
	}
	if !library.Encrypted {
		return ProvisionEncryptedLibraryInfo{}, clientError("library is not flagged encrypted")
	}

	mode, err := parseUnsealMode(req)
	if err != nil {
		return ProvisionEncryptedLibraryInfo{}, err
	}
	passphrase, _ := newVars(req).String("passphrase")
	if mode == libencrypt.UnsealModePassphrase && strings.TrimSpace(passphrase) == "" {
		return ProvisionEncryptedLibraryInfo{}, clientError("passphrase is required for passphrase-unseal mode")
	}
	if h.encryptedLibraryDBLifecycle != nil && !h.encryptedLibraryDBLifecycle.EncryptedLibraryDBLifecycleAvailable() {
		return ProvisionEncryptedLibraryInfo{}, clientError("encrypted-library DB lifecycle is unavailable on this server")
	}

	prepared, hasPrepared := h.libEncrypt.(preparedLibEncryptManager)
	var preparedStatus EncryptedLibraryStatusInfo
	if hasPrepared {
		preparedStatus = prospectiveEncryptedLibraryStatus(libraryID, mode, true, false, prepared.ProspectiveKeyLocus(libraryID, mode))
	}
	var rk libencrypt.RecoveryKey
	if hasPrepared {
		var aborter encryptedLibraryProvisionAborter
		if h.encryptedLibraryDBLifecycle != nil {
			var abortOK bool
			aborter, abortOK = h.encryptedLibraryDBLifecycle.(encryptedLibraryProvisionAborter)
			if !abortOK {
				return ProvisionEncryptedLibraryInfo{}, errors.New("encrypted-library DB lifecycle cannot compensate staged provision")
			}
		}
		rk, err = prepared.ProvisionPrepared(libraryID, mode, passphrase, libencrypt.ProvisionHooks{
			Prepare: func(cipher libencrypt.OperationCipher) error {
				if h.encryptedLibraryDBLifecycle != nil {
					if err := h.encryptedLibraryDBLifecycle.UnsealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), operationDBCipher{cipher: cipher}); err != nil {
						return err
					}
				}
				return h.appendCryptoActivityRequired(ctx, scope, "encrypted-library-provision", store.LibraryID(libraryID))
			},
			Rollback: func() error {
				if aborter == nil {
					return nil
				}
				return aborter.AbortEncryptedLibraryDBProvision(ctx, store.LibraryID(libraryID))
			},
		})
	} else {
		rk, err = h.libEncrypt.Provision(libraryID, mode, passphrase)
	}
	if err != nil {
		if errors.Is(err, libencrypt.ErrAlreadyProvisioned) {
			return ProvisionEncryptedLibraryInfo{}, clientError("library is already provisioned")
		}
		return ProvisionEncryptedLibraryInfo{}, err
	}
	if !hasPrepared && h.encryptedLibraryDBLifecycle != nil {
		if err := h.encryptedLibraryDBLifecycle.UnsealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), h.libEncryptDBCipher()); err != nil {
			h.libEncrypt.Seal(libraryID)
			return ProvisionEncryptedLibraryInfo{}, err
		}
	}
	if !hasPrepared {
		if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "encrypted-library-provision", "", store.LibraryID(libraryID)); err != nil {
			return ProvisionEncryptedLibraryInfo{}, err
		}
	}
	status := preparedStatus
	if !hasPrepared {
		status = h.encryptedLibraryStatus(libraryID)
	}
	return ProvisionEncryptedLibraryInfo{
		Status:                  status,
		RecoveryKey:             rk.Display,
		AcknowledgementRequired: true,
	}, nil
}

func prospectiveEncryptedLibraryStatus(libraryID string, mode libencrypt.UnsealMode, provisioned, sealed bool, locus libencrypt.KeyLocusInfo) EncryptedLibraryStatusInfo {
	status := EncryptedLibraryStatusInfo{
		LibraryID:   libraryID,
		Provisioned: provisioned,
		Sealed:      sealed,
		UnsealMode:  string(mode),
	}
	if locus.Class != "" && locus.Class != libencrypt.KeyLocusUnknown {
		status.KeyLocusClass = string(locus.Class)
		status.KeyLocusDisclosure = locus.Disclosure
	}
	return status
}

// unsealEncryptedLibrary unseals a Sealed Library via the operator passphrase or
// the user-held Recovery Key. Admin-Session-gated. A wrong
// passphrase/Recovery Key is a client-safe error and the Library stays Sealed.
func (h *Handler) unsealEncryptedLibrary(ctx context.Context, scope auth.Scope, req request) (EncryptedLibraryStatusInfo, error) {
	if h.libEncrypt == nil {
		return EncryptedLibraryStatusInfo{}, clientError("encrypted-library key management is unavailable on this server")
	}
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return EncryptedLibraryStatusInfo{}, clientError("libraryID is required")
	}
	if _, ok, err := h.encryptedLibrary(ctx, scope, libraryID); err != nil {
		return EncryptedLibraryStatusInfo{}, err
	} else if !ok {
		return EncryptedLibraryStatusInfo{}, clientError("unknown library")
	}
	if !h.libEncrypt.Provisioned(libraryID) {
		return EncryptedLibraryStatusInfo{}, clientError("library is not provisioned")
	}

	v := newVars(req)
	passphrase, hasPassphrase := v.String("passphrase")
	recoveryKey, hasRecovery := v.String("recoveryKey")
	if err := v.Err(); err != nil {
		return EncryptedLibraryStatusInfo{}, err
	}
	prepared, isPrepared := h.libEncrypt.(preparedLibEncryptManager)
	var err error
	prepare := func(cipher libencrypt.OperationCipher) error {
		if h.encryptedLibraryDBLifecycle != nil {
			if err := h.encryptedLibraryDBLifecycle.UnsealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), operationDBCipher{cipher: cipher}); err != nil {
				return err
			}
		}
		return h.logActivity(ctx, scope, store.ActivityConfigUpdate, "encrypted-library-unseal", "", store.LibraryID(libraryID))
	}
	switch {
	case hasRecovery && strings.TrimSpace(recoveryKey) != "":
		if isPrepared {
			err = prepared.RecoveryUnsealPrepared(libraryID, recoveryKey, prepare)
		} else {
			err = h.libEncrypt.RecoveryUnseal(libraryID, recoveryKey)
		}
	case hasPassphrase:
		if isPrepared {
			err = prepared.PassphraseUnsealPrepared(libraryID, passphrase, prepare)
		} else {
			err = h.libEncrypt.PassphraseUnseal(libraryID, passphrase)
		}
	default:
		return EncryptedLibraryStatusInfo{}, clientError("passphrase or recoveryKey is required to unseal")
	}
	if err != nil {
		return EncryptedLibraryStatusInfo{}, unsealClientError(err)
	}
	if !isPrepared && h.encryptedLibraryDBLifecycle != nil {
		if err := h.encryptedLibraryDBLifecycle.UnsealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), h.libEncryptDBCipher()); err != nil {
			h.libEncrypt.Seal(libraryID)
			return EncryptedLibraryStatusInfo{}, err
		}
	}
	if !isPrepared {
		if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "encrypted-library-unseal", "", store.LibraryID(libraryID)); err != nil {
			return EncryptedLibraryStatusInfo{}, err
		}
	}
	return h.encryptedLibraryStatus(libraryID), nil
}

// sealEncryptedLibrary returns a Library to the Sealed state (drops the
// in-memory DEK). Admin-Session-gated. Idempotent.
func (h *Handler) sealEncryptedLibrary(ctx context.Context, scope auth.Scope, req request) (EncryptedLibraryStatusInfo, error) {
	if h.libEncrypt == nil {
		return EncryptedLibraryStatusInfo{}, clientError("encrypted-library key management is unavailable on this server")
	}
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return EncryptedLibraryStatusInfo{}, clientError("libraryID is required")
	}
	if _, ok, err := h.encryptedLibrary(ctx, scope, libraryID); err != nil {
		return EncryptedLibraryStatusInfo{}, err
	} else if !ok {
		return EncryptedLibraryStatusInfo{}, clientError("unknown library")
	}
	if prepared, ok := h.libEncrypt.(preparedLibEncryptManager); ok {
		if err := prepared.SealPrepared(libraryID, func(cipher libencrypt.OperationCipher) error {
			if h.encryptedLibraryDBLifecycle != nil {
				if err := h.encryptedLibraryDBLifecycle.SealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), operationDBCipher{cipher: cipher}); err != nil {
					return err
				}
			}
			return h.logActivity(ctx, scope, store.ActivityConfigUpdate, "encrypted-library-seal", "", store.LibraryID(libraryID))
		}); err != nil {
			return EncryptedLibraryStatusInfo{}, err
		}
	} else {
		if h.encryptedLibraryDBLifecycle != nil {
			if err := h.encryptedLibraryDBLifecycle.SealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), h.libEncryptDBCipher()); err != nil {
				return EncryptedLibraryStatusInfo{}, err
			}
		}
		h.libEncrypt.Seal(libraryID)
		if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "encrypted-library-seal", "", store.LibraryID(libraryID)); err != nil {
			return EncryptedLibraryStatusInfo{}, err
		}
	}
	return h.encryptedLibraryStatus(libraryID), nil
}

func (h *Handler) rewrapEncryptedLibrary(ctx context.Context, scope auth.Scope, req request) (ProvisionEncryptedLibraryInfo, error) {
	if h.libEncrypt == nil {
		return ProvisionEncryptedLibraryInfo{}, clientError("encrypted-library key management is unavailable on this server")
	}
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return ProvisionEncryptedLibraryInfo{}, clientError("libraryID is required")
	}
	library, ok, err := h.encryptedLibrary(ctx, scope, libraryID)
	if err != nil {
		return ProvisionEncryptedLibraryInfo{}, err
	}
	if !ok {
		return ProvisionEncryptedLibraryInfo{}, clientError("unknown library")
	}
	if !library.Encrypted {
		return ProvisionEncryptedLibraryInfo{}, clientError("library is not flagged encrypted")
	}
	if !h.libEncrypt.Provisioned(libraryID) {
		return ProvisionEncryptedLibraryInfo{}, clientError("library is not provisioned")
	}
	mode, err := parseUnsealMode(req)
	if err != nil {
		return ProvisionEncryptedLibraryInfo{}, err
	}
	passphrase, _ := newVars(req).String("passphrase")
	if mode == libencrypt.UnsealModePassphrase && strings.TrimSpace(passphrase) == "" {
		return ProvisionEncryptedLibraryInfo{}, clientError("passphrase is required for passphrase-unseal mode")
	}
	files, err := h.store.MediaFilesForLibrary(ctx, store.LibraryID(libraryID))
	if err != nil {
		return ProvisionEncryptedLibraryInfo{}, err
	}
	artifacts, err := h.encryptedLibraryBackupRotationArtifacts(ctx, store.LibraryID(libraryID))
	if err != nil {
		return ProvisionEncryptedLibraryInfo{}, err
	}
	if h.encryptedLibraryDBLifecycle != nil {
		if !h.encryptedLibraryDBLifecycle.EncryptedLibraryDBLifecycleAvailable() {
			return ProvisionEncryptedLibraryInfo{}, clientError("encrypted-library DB lifecycle is unavailable on this server")
		}
	}
	rewrite := func(cipher libencrypt.RewrapCipher) error {
		// Every rewritten object carries its immutable, caller-derived identity
		//: the DB is the per-library singleton, media
		// bytes bind to their persisted content fingerprint, and generated
		// assets bind to fingerprint + asset name — never to file paths.
		if h.encryptedLibraryDBLifecycle != nil {
			dbObj := libencrypt.LibraryDBObjectContext(libraryID)
			if err := h.encryptedLibraryDBLifecycle.RewrapEncryptedLibraryDB(ctx, store.LibraryID(libraryID), func(path string) error {
				return cipher.RewrapFile(path, dbObj)
			}); err != nil {
				return err
			}
		}
		for _, file := range files {
			if strings.TrimSpace(file.Path) == "" {
				continue
			}
			if file.ContentFingerprint == "" {
				return errors.New("encrypted media file has no content fingerprint")
			}
			if err := cipher.RewrapFile(file.Path, libencrypt.MediaObjectContext(libraryID, file.ContentFingerprint)); err != nil {
				return err
			}
		}
		if h.encryptedGeneratedAssets != nil {
			if err := h.encryptedGeneratedAssets.RewrapEncryptedLibraryAssets(ctx, files, func(path, contentFingerprint, assetName string) error {
				return cipher.RewrapFile(path, libencrypt.GeneratedAssetObjectContext(libraryID, contentFingerprint, assetName))
			}); err != nil {
				return err
			}
		}
		return nil
	}
	prepared, hasPrepared := h.libEncrypt.(preparedLibEncryptManager)
	var preparedStatus EncryptedLibraryStatusInfo
	if hasPrepared {
		preparedStatus = prospectiveEncryptedLibraryStatus(libraryID, mode, true, true, prepared.ProspectiveKeyLocus(libraryID, mode))
	}
	var rk libencrypt.RecoveryKey
	if hasPrepared {
		rk, err = prepared.RewrapPrepared(
			libraryID,
			mode,
			passphrase,
			func(cipher libencrypt.OperationCipher) error {
				if h.encryptedLibraryDBLifecycle == nil {
					return nil
				}
				return h.encryptedLibraryDBLifecycle.SealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), operationDBCipher{cipher: cipher})
			},
			func(cipher libencrypt.OperationCipher) error {
				if h.encryptedLibraryDBLifecycle == nil {
					return nil
				}
				return h.encryptedLibraryDBLifecycle.UnsealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), operationDBCipher{cipher: cipher})
			},
			rewrite,
			func() error {
				return h.appendCryptoActivityRequired(ctx, scope, "encrypted-library-rewrap", store.LibraryID(libraryID))
			},
			true,
		)
	} else {
		if h.encryptedLibraryDBLifecycle != nil {
			if err := h.encryptedLibraryDBLifecycle.SealEncryptedLibraryDB(ctx, store.LibraryID(libraryID), h.libEncryptDBCipher()); err != nil {
				return ProvisionEncryptedLibraryInfo{}, err
			}
		}
		rk, err = h.libEncrypt.Rewrap(libraryID, mode, passphrase, rewrite)
	}
	if err != nil {
		if errors.Is(err, libencrypt.ErrSealed) {
			return ProvisionEncryptedLibraryInfo{}, clientError("library must be unsealed before rewrap")
		}
		return ProvisionEncryptedLibraryInfo{}, err
	}
	if !hasPrepared {
		h.libEncrypt.Seal(libraryID)
		if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "encrypted-library-rewrap", "", store.LibraryID(libraryID)); err != nil {
			return ProvisionEncryptedLibraryInfo{}, err
		}
	}
	status := preparedStatus
	if !hasPrepared {
		status = h.encryptedLibraryStatus(libraryID)
	}
	return ProvisionEncryptedLibraryInfo{
		Status:                  status,
		RecoveryKey:             rk.Display,
		AcknowledgementRequired: true,
		BackupRotationArtifacts: artifacts,
		ResidualRiskNotice:      "Rotation protects new copies; already-copied ciphertext remains recoverable to anyone who held the old key. Off-site backup copies (ADR-0115) are wrapped under a separate BackupKey and are not rotated by this response — rotate the BackupKey separately to protect them.",
	}, nil
}

// appendCryptoActivityRequired is part of the staged key transaction: unlike
// the general post-mutation detail logger, failure must abort before the key
// commit so Provision/Rewrap can compensate safely.
func (h *Handler) appendCryptoActivityRequired(ctx context.Context, scope auth.Scope, targetType string, libraryID store.LibraryID) error {
	return h.appendMutationAudit(ctx, store.ActivityEntry{
		OccurredAt: time.Now().UTC(),
		Action:     store.ActivityCryptoLifecyclePrepared,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: targetType,
		LibraryID:  libraryID,
		TokenID:    scope.APITokenID,
		APIKeyID:   scope.APIKeyID,
	})
}

func (h *Handler) encryptedLibraryBackupRotationArtifacts(ctx context.Context, libraryID store.LibraryID) ([]BackupRotationArtifactInfo, error) {
	artifacts, err := h.store.EncryptedLibraryBackupRotationArtifacts(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	infos := make([]BackupRotationArtifactInfo, 0, len(artifacts))
	for _, artifact := range artifacts {
		infos = append(infos, BackupRotationArtifactInfo{
			Kind:     artifact.Kind,
			Path:     artifact.Path,
			Manifest: artifact.Manifest,
			Action:   artifact.Action,
		})
	}
	return infos, nil
}

// encryptedLibraryStatusField is the Admin-Session-gated read of one Library's
// key-model state — credential-free, for the Console seal/unseal UI.
func (h *Handler) encryptedLibraryStatusField(ctx context.Context, scope auth.Scope, req request) (EncryptedLibraryStatusInfo, error) {
	if h.libEncrypt == nil {
		return EncryptedLibraryStatusInfo{}, clientError("encrypted-library key management is unavailable on this server")
	}
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return EncryptedLibraryStatusInfo{}, clientError("libraryID is required")
	}
	if _, ok, err := h.encryptedLibrary(ctx, scope, libraryID); err != nil {
		return EncryptedLibraryStatusInfo{}, err
	} else if !ok {
		return EncryptedLibraryStatusInfo{}, clientError("unknown library")
	}
	return h.encryptedLibraryStatus(libraryID), nil
}

// encryptedLibraryStatus assembles the credential-free status from the seam.
func (h *Handler) encryptedLibraryStatus(libraryID string) EncryptedLibraryStatusInfo {
	sealed := h.libEncrypt.Sealed(libraryID)
	if h.encryptedLibraryDBLifecycle != nil {
		if _, quarantined := h.encryptedLibraryDBLifecycle.EncryptedLibraryDBQuarantine(store.LibraryID(libraryID)); quarantined {
			sealed = true
		} else if !h.encryptedLibraryDBLifecycle.EncryptedLibraryDBRegistered(store.LibraryID(libraryID)) {
			sealed = true
		}
	}
	info := EncryptedLibraryStatusInfo{
		LibraryID:   libraryID,
		Provisioned: h.libEncrypt.Provisioned(libraryID),
		Sealed:      sealed,
	}
	if info.Provisioned {
		if mode, err := h.libEncrypt.Mode(libraryID); err == nil {
			info.UnsealMode = string(mode)
		}
		if reporter, ok := h.libEncrypt.(interface {
			KeyLocus(string) libencrypt.KeyLocusInfo
		}); ok {
			locus := reporter.KeyLocus(libraryID)
			if locus.Class != "" && locus.Class != libencrypt.KeyLocusUnknown {
				info.KeyLocusClass = string(locus.Class)
				info.KeyLocusDisclosure = locus.Disclosure
			}
		}
	}
	return info
}

func (h *Handler) libEncryptDBCipher() store.EncryptedLibraryDBCipher {
	return libEncryptDBCipher{mgr: h.libEncrypt}
}

type libEncryptDBCipher struct {
	mgr LibEncryptManager
}

type operationDBCipher struct {
	cipher libencrypt.OperationCipher
}

func (c operationDBCipher) UnsealDBFile(_ context.Context, libraryID store.LibraryID, ciphertextPath, workingPath string) error {
	return c.cipher.UnsealDBFile(string(libraryID), ciphertextPath, workingPath, libencrypt.LibraryDBObjectContext(string(libraryID)))
}

func (c operationDBCipher) SealDBFile(_ context.Context, libraryID store.LibraryID, workingPath, ciphertextPath string) error {
	return c.cipher.SealDBFile(string(libraryID), workingPath, ciphertextPath, libencrypt.LibraryDBObjectContext(string(libraryID)))
}

func (c libEncryptDBCipher) UnsealDBFile(ctx context.Context, libraryID store.LibraryID, ciphertextPath, workingPath string) error {
	if mgr, ok := c.mgr.(interface {
		UnsealDBFile(string, string, string, libencrypt.ObjectContext) error
	}); ok {
		return mgr.UnsealDBFile(string(libraryID), ciphertextPath, workingPath, libencrypt.LibraryDBObjectContext(string(libraryID)))
	}
	return errors.New("encrypted-library DB unseal is unavailable")
}

func (c libEncryptDBCipher) SealDBFile(ctx context.Context, libraryID store.LibraryID, workingPath, ciphertextPath string) error {
	if mgr, ok := c.mgr.(interface {
		SealDBFile(string, string, string, libencrypt.ObjectContext) error
	}); ok {
		return mgr.SealDBFile(string(libraryID), workingPath, ciphertextPath, libencrypt.LibraryDBObjectContext(string(libraryID)))
	}
	return errors.New("encrypted-library DB seal is unavailable")
}

// encryptedLibrary resolves a Library by id, reveal-and-Library filtered through
// scope so a hidden Library is "unknown" rather than an existence oracle.
func (h *Handler) encryptedLibrary(ctx context.Context, scope auth.Scope, libraryID string) (store.Library, bool, error) {
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return store.Library{}, false, err
	}
	for _, lib := range libraries {
		if string(lib.ID) == libraryID {
			return lib, true, nil
		}
	}
	return store.Library{}, false, nil
}

// parseUnsealMode reads the unsealMode variable, defaulting to auto (ADR-0045
// §2.4 — auto-unseal is the default).
func parseUnsealMode(req request) (libencrypt.UnsealMode, error) {
	raw := strings.TrimSpace(trimmedVariable(req, "unsealMode"))
	if raw == "" {
		return libencrypt.UnsealModeAuto, nil
	}
	mode := libencrypt.UnsealMode(raw)
	if !libencrypt.ValidUnsealMode(mode) {
		return "", clientError("unsealMode must be one of auto, passphrase")
	}
	return mode, nil
}

// unsealClientError maps a wrong-key unwrap to a client-safe message; any other
// error propagates as-is. It never echoes the bad credential.
func unsealClientError(err error) error {
	if errors.Is(err, libencrypt.ErrUnwrap) {
		return clientError("incorrect passphrase or recovery key")
	}
	return err
}
