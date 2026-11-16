package graph

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// Rescan preview (CORE-1054): scanPreview walks a Library's roots without
// writing and returns the planned changes with a single-use, expiring,
// Library-bound token and the audit id of the preview's activity row.
// commitScanPreview consumes the token and enqueues the scan, refusing a token
// that is unknown, already used, expired, or stale because the Library's roots
// or catalogued files changed since the preview.

// scanPreviewTTL is how long a preview token stays committable.
const scanPreviewTTL = 10 * time.Minute

// maxScanPreviewTokens bounds the in-memory token table; the soonest-expiring
// entry is evicted when a new preview would exceed it.
const maxScanPreviewTokens = 256

// ScanPreviewInfo is the wire shape of a rescan preview.
type ScanPreviewInfo struct {
	PreviewToken   string                  `json:"previewToken"`
	AuditEventID   string                  `json:"auditEventID"`
	LibraryID      string                  `json:"libraryID"`
	Roots          []string                `json:"roots"`
	ExpiresAt      string                  `json:"expiresAt"`
	Adds           int                     `json:"adds"`
	Updates        int                     `json:"updates"`
	Missing        int                     `json:"missing"`
	Unchanged      int                     `json:"unchanged"`
	Changes        []ScanPreviewChangeInfo `json:"changes"`
	Truncated      bool                    `json:"truncated"`
	MissingSkipped []string                `json:"missingSkipped"`
}

// ScanPreviewChangeInfo is one planned change: kind add, update or missing.
type ScanPreviewChangeInfo struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// scanPreviewTokens is the single-use token table. Tokens are kept only as
// SHA-256 digests; a used token stays recorded until it expires so a reuse
// reads as reused rather than unknown.
type scanPreviewTokens struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*scanPreviewEntry
}

type scanPreviewEntry struct {
	holder    string
	libraryID store.LibraryID
	root      string
	digest    string
	auditID   string
	expiresAt time.Time
	used      bool
}

func (t *scanPreviewTokens) clock() time.Time {
	if t.now != nil {
		return t.now().UTC()
	}
	return time.Now().UTC()
}

// pruneLocked drops expired entries; t.mu must be held.
func (t *scanPreviewTokens) pruneLocked(now time.Time) {
	for key, entry := range t.entries {
		if !now.Before(entry.expiresAt) {
			delete(t.entries, key)
		}
	}
}

func (t *scanPreviewTokens) issue(entry scanPreviewEntry) (string, time.Time, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(raw[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock()
	if t.entries == nil {
		t.entries = map[string]*scanPreviewEntry{}
	}
	t.pruneLocked(now)
	for len(t.entries) >= maxScanPreviewTokens {
		var oldest string
		for key, existing := range t.entries {
			if oldest == "" || existing.expiresAt.Before(t.entries[oldest].expiresAt) {
				oldest = key
			}
		}
		delete(t.entries, oldest)
	}
	entry.expiresAt = now.Add(scanPreviewTTL)
	t.entries[scanPreviewTokenKey(token)] = &entry
	return token, entry.expiresAt, nil
}

// consume marks the token used and returns its entry. Unknown tokens and
// tokens issued to another credential read the same, so the table is no
// oracle for other sessions' previews.
func (t *scanPreviewTokens) consume(token, holder string) (scanPreviewEntry, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.entries[scanPreviewTokenKey(token)]
	if !ok || entry.holder != holder {
		return scanPreviewEntry{}, clientError("unknown scan preview token")
	}
	if entry.used {
		return scanPreviewEntry{}, clientError("scan preview token was already used")
	}
	if !t.clock().Before(entry.expiresAt) {
		return scanPreviewEntry{}, clientError("scan preview token has expired")
	}
	entry.used = true
	return *entry, nil
}

func scanPreviewTokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// scanPreviewHolder binds a token to the credential that asked for it.
func scanPreviewHolder(scope auth.Scope) string {
	return string(scope.SessionID) + "\x00" + string(scope.ProfileID) + "\x00" + scope.APITokenID + "\x00" + scope.APIKeyID
}

// scanPreview plans a rescan of a Library the manager can see. It writes no
// catalog state; its only write is the audit row named by auditEventID.
func (h *Handler) scanPreview(ctx context.Context, scope auth.Scope, req request) (ScanPreviewInfo, error) {
	libraryID := store.LibraryID(trimmedVariable(req, "libraryID"))
	root := trimmedVariable(req, "root")
	v := newVars(req)
	limit, _ := v.Number("limit")
	if err := v.Err(); err != nil {
		return ScanPreviewInfo{}, err
	}
	roots, err := h.resolveScanRoots(ctx, scope, libraryID, root)
	if err != nil {
		return ScanPreviewInfo{}, err
	}
	digest, err := scan.LibraryStateDigest(ctx, h.store, libraryID, roots)
	if err != nil {
		return ScanPreviewInfo{}, err
	}
	pluginID, err := h.store.LibraryPluginID(ctx, libraryID)
	if err != nil {
		return ScanPreviewInfo{}, err
	}
	plan, err := scan.PreviewRoots(ctx, h.store, libraryID, roots, int(limit), h.pluginHoldsImages(pluginID))
	if err != nil {
		return ScanPreviewInfo{}, err
	}
	auditID, err := newScanPreviewAuditID()
	if err != nil {
		return ScanPreviewInfo{}, err
	}
	entry := mutationAuditEntry(scope, auditID, store.ActivityScanPreview, "library", string(libraryID))
	entry.LibraryID = libraryID
	if err := h.store.AppendActivity(ctx, entry); err != nil {
		return ScanPreviewInfo{}, err
	}
	token, expiresAt, err := h.scanPreviews.issue(scanPreviewEntry{
		holder:    scanPreviewHolder(scope),
		libraryID: libraryID,
		root:      root,
		digest:    digest,
		auditID:   auditID,
	})
	if err != nil {
		return ScanPreviewInfo{}, err
	}
	changes := make([]ScanPreviewChangeInfo, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		changes = append(changes, ScanPreviewChangeInfo{Kind: change.Kind, Path: change.Path})
	}
	return ScanPreviewInfo{
		PreviewToken:   token,
		AuditEventID:   auditID,
		LibraryID:      string(libraryID),
		Roots:          roots,
		ExpiresAt:      expiresAt.Format(time.RFC3339),
		Adds:           plan.Adds,
		Updates:        plan.Updates,
		Missing:        plan.Missing,
		Unchanged:      plan.Unchanged,
		Changes:        changes,
		Truncated:      plan.Truncated,
		MissingSkipped: plan.MissingSkipped,
	}, nil
}

// commitScanPreview consumes a preview token and enqueues the previewed scan.
// The token is spent before the staleness check, so a stale token cannot be
// retried either; the caller previews again.
func (h *Handler) commitScanPreview(ctx context.Context, scope auth.Scope, req request) (JobInfo, error) {
	token := trimmedVariable(req, "previewToken")
	if token == "" {
		return JobInfo{}, clientError("previewToken is required")
	}
	entry, err := h.scanPreviews.consume(token, scanPreviewHolder(scope))
	if err != nil {
		return JobInfo{}, err
	}
	roots, err := h.resolveScanRoots(ctx, scope, entry.libraryID, entry.root)
	var refused clientError
	if errors.As(err, &refused) {
		// The Library was removed, hidden, or lost its roots or folder.
		return JobInfo{}, clientError("scan preview is stale because the library changed")
	}
	if err != nil {
		return JobInfo{}, err
	}
	digest, err := scan.LibraryStateDigest(ctx, h.store, entry.libraryID, roots)
	if err != nil {
		return JobInfo{}, err
	}
	if digest != entry.digest {
		return JobInfo{}, clientError("scan preview is stale because the library changed")
	}
	audit := mutationAuditEntry(scope, entry.auditID+".commit", store.ActivityScanPreviewCommit, "library", string(entry.libraryID))
	audit.LibraryID = entry.libraryID
	if err := h.store.AppendActivity(ctx, audit); err != nil {
		return JobInfo{}, err
	}
	return h.enqueueScanRoots(ctx, scope, entry.libraryID, roots, false)
}

func newScanPreviewAuditID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "scan-preview-" + hex.EncodeToString(raw[:]), nil
}
