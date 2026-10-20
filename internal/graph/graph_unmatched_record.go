package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// METAPARITY-6/7 (ADR-0068 §A) graph half of the Media-INDEPENDENT Unmatched
// Record inbox: read the pending records, create a hand-authored no-match Media
// from one, and manage the three independently-resettable suppression scopes.
// Resolver bodies live here rather than in graph_identify.go, which owns the
// Media-KEYED scan queue — the two inboxes have different identities.

// UnmatchedRecordInfo is one pending Media-independent Unmatched Record on the
// wire. Generic shape (invariant #1): origin and failureReason are shape words;
// importerID carries the species. sourceHandle is display context — a path or an
// external-id summary — never a durable identity.
type UnmatchedRecordInfo struct {
	ID             string  `json:"id"`
	Origin         string  `json:"origin"`
	LibraryID      string  `json:"libraryID,omitempty"`
	MediaID        string  `json:"mediaID,omitempty"`
	ImportRunID    string  `json:"importRunID,omitempty"`
	ImporterID     string  `json:"importerID,omitempty"`
	SourceHandle   string  `json:"sourceHandle,omitempty"`
	CandidateID    string  `json:"candidateID,omitempty"`
	CandidateTitle string  `json:"candidateTitle,omitempty"`
	Confidence     float64 `json:"confidence"`
	FailureReason  string  `json:"failureReason,omitempty"`
	QueuedAt       string  `json:"queuedAt,omitempty"`
}

// unmatchedRecords lists the pending Media-independent records the caller may
// see. The store read applies the reveal-and-Library filter and fails closed on
// Library-less (operator-global) records, so this resolver adds no visibility
// logic of its own.
func (h *Handler) unmatchedRecords(ctx context.Context, scope auth.Scope, _ request) ([]UnmatchedRecordInfo, error) {
	records, err := h.store.UnmatchedRecords(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	out := make([]UnmatchedRecordInfo, 0, len(records))
	for _, record := range records {
		info := UnmatchedRecordInfo{
			ID:             record.ID,
			Origin:         record.Origin,
			LibraryID:      string(record.LibraryID),
			MediaID:        string(record.MediaID),
			ImportRunID:    record.ImportRunID,
			ImporterID:     record.ImporterID,
			SourceHandle:   record.SourceHandle,
			CandidateID:    record.CandidateID,
			CandidateTitle: record.CandidateTitle,
			Confidence:     record.Confidence,
			FailureReason:  record.FailureReason,
		}
		if !record.QueuedAt.IsZero() {
			info.QueuedAt = record.QueuedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, info)
	}
	return out, nil
}

// createMediaFromUnmatched is METAPARITY-6's associate-vs-CREATE half: the
// operator decided no provider candidate is right and typed the identity
// themselves, so a normal browsable/playable Media is created with hand-entered
// title/year, the file the record pointed at, and NO external ID — Beamfall must
// never imply it found or requires a provider identifier for this Media.
//
// The record is then resolved so it leaves the review inbox. Ordering is
// deliberate: the Media ID is derived, so a retry after a failed resolve lands
// on the SAME Media rather than forking a duplicate, and a failure anywhere
// leaves the record OPEN rather than half-resolved. Manager-gated; the target
// Library must be visible to the caller, which is what rejects a cross-Library
// create.
func (h *Handler) createMediaFromUnmatched(ctx context.Context, scope auth.Scope, req request) (MediaInfo, error) {
	recordID := trimmedVariable(req, "recordID")
	libraryID := trimmedVariable(req, "libraryID")
	title := trimmedVariable(req, "title")
	if recordID == "" || libraryID == "" || title == "" {
		return MediaInfo{}, clientError("recordID, libraryID, and title are required")
	}
	return h.createMediaFromUnmatchedInput(ctx, scope, recordID, libraryID, title, nonNegativeIntVar(req, "year"), nil)
}

// CreateMediaFromUnmatchedArtwork is the multipart HTTP seam for the approved
// create-Media form. The HTTP boundary validates the image bytes and manager
// session first; this method preserves the GraphQL resolver's visibility,
// idempotency, and resolve-last ordering while placing the upload in the normal
// poster candidate/pin pipeline.
func (h *Handler) CreateMediaFromUnmatchedArtwork(
	ctx context.Context,
	scope auth.Scope,
	recordID, libraryID, title string,
	year int,
	data []byte,
) (string, error) {
	if recordID == "" || libraryID == "" || title == "" || len(data) == 0 {
		return "", clientError("recordID, libraryID, title, and artwork are required")
	}
	media, err := h.createMediaFromUnmatchedInput(ctx, scope, recordID, libraryID, title, year, data)
	if err != nil {
		return "", err
	}
	return media.ID, nil
}

func (h *Handler) createMediaFromUnmatchedInput(
	ctx context.Context,
	scope auth.Scope,
	recordID, libraryID, title string,
	year int,
	artworkData []byte,
) (MediaInfo, error) {
	if _, err := h.visibleUnmatchedRecord(ctx, scope, recordID); err != nil {
		return MediaInfo{}, err
	}
	filter := scope.LibraryFilter()
	// The reveal-and-Library visibility check IS the cross-Library guard: a
	// Library the caller cannot see is indistinguishable from one that does not
	// exist, so an unauthorized create can never be told apart from a typo.
	visible, err := h.store.LibraryVisible(ctx, store.LibraryID(libraryID), filter)
	if err != nil || !visible {
		return MediaInfo{}, clientError("unknown library")
	}
	artworkDigest := ""
	if len(artworkData) > 0 {
		sum := sha256.Sum256(artworkData)
		artworkDigest = hex.EncodeToString(sum[:])
	}
	var beforeFinalize []func(store.MediaID) error
	if len(artworkData) > 0 {
		beforeFinalize = append(beforeFinalize, func(mediaID store.MediaID) error {
			return h.pinUploadedArtwork(ctx, scope, mediaID, artworkData)
		})
	}
	mediaID, err := h.store.CreateManualMediaFromUnmatched(
		ctx,
		recordID,
		store.LibraryID(libraryID),
		title,
		year,
		artworkDigest,
		time.Now().UTC(),
		beforeFinalize...,
	)
	if err != nil {
		return MediaInfo{}, err
	}
	media, err := h.store.Media(ctx, mediaID, filter)
	if err != nil {
		return MediaInfo{}, clientError("unknown media")
	}
	return h.mediaInfo(ctx, scope, media)
}

func (h *Handler) pinUploadedArtwork(ctx context.Context, scope auth.Scope, mediaID store.MediaID, data []byte) error {
	if h.candidateImport == nil {
		return clientError("artwork upload unavailable")
	}
	const slot = artwork.SlotPoster
	sum := sha256.Sum256(data)
	sourceURL := "beamfall-upload://sha256/" + hex.EncodeToString(sum[:])
	candidateID := store.ArtworkCandidateID(mediaID, slot, sourceURL)
	candidates, err := h.store.ArtworkCandidates(ctx, mediaID, slot, scope.LibraryFilter())
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if candidate.ID == candidateID && candidate.Applied {
			return nil
		}
	}
	if err := h.candidateImport.ImportArtwork(ctx, mediaID, slot, data); err != nil {
		return err
	}
	if err := h.store.UpsertArtworkCandidate(ctx, mediaID, slot, sourceURL, "", ""); err != nil {
		return err
	}
	if err := h.store.MarkArtworkCandidateApplied(ctx, candidateID); err != nil {
		return err
	}
	media, err := h.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return err
	}
	return h.logActivity(ctx, scope, store.ActivityArtworkPin, "media", string(mediaID), media.LibraryID)
}

// visibleUnmatchedRecord resolves one pending record the caller may see. It
// reuses the store's visibility-filtered inbox read rather than adding a second
// filtering rule, so an invisible record is indistinguishable from an unknown
// one — the same fail-closed posture the Media lookups take.
func (h *Handler) visibleUnmatchedRecord(ctx context.Context, scope auth.Scope, recordID string) (store.UnmatchedRecord, error) {
	record, err := h.store.UnmatchedRecordByID(ctx, recordID, scope.LibraryFilter())
	if err != nil {
		if errors.Is(err, store.ErrUnmatchedRecordNotFound) {
			return store.UnmatchedRecord{}, clientError("unknown unmatched record")
		}
		return store.UnmatchedRecord{}, err
	}
	return record, nil
}

// PrepareUploadedArtwork validates a previously-checked upload against its
// visible Media and returns the candidate/file/activity operation for the HTTP
// boundary to execute through store.ApplyArtworkCandidate. The synthetic source
// identity is derived from the bytes, so identical retries converge on the same
// provenance row and are never dereferenced.
//
// Exported so internal/server's multipart upload route can drive it through
// the server.ArtworkImporter structural interface without internal/server
// importing this package — the GraphQL endpoint is deliberately wired into
// internal/server as an opaque http.Handler (docs/architecture/ARCHITECTURE.md
// §4 "no transport leaks").
func (h *Handler) PrepareUploadedArtwork(
	ctx context.Context,
	scope auth.Scope,
	mediaID store.MediaID,
	slot string,
	data []byte,
) (store.ArtworkCandidateApply, error) {
	if mediaID == "" || !artwork.ValidSlot(slot) {
		return store.ArtworkCandidateApply{}, store.ValidationError("mediaID and a known artwork slot are required")
	}
	if len(data) == 0 {
		return store.ArtworkCandidateApply{}, store.ValidationError("artwork data is required")
	}
	if h.candidateImport == nil {
		return store.ArtworkCandidateApply{}, store.ValidationError("artwork upload unavailable")
	}
	media, err := h.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return store.ArtworkCandidateApply{}, store.ValidationError("unknown media")
	}
	return h.prepareUploadedArtwork(ctx, scope, media, slot, data)
}

type uploadedArtworkImporter interface {
	ArtworkCandidateImporter
	ArtworkPath(store.MediaID, string) string
}

func (h *Handler) prepareUploadedArtwork(
	ctx context.Context,
	scope auth.Scope,
	media store.Media,
	slot string,
	data []byte,
) (store.ArtworkCandidateApply, error) {
	importer, ok := h.candidateImport.(uploadedArtworkImporter)
	if !ok {
		return store.ArtworkCandidateApply{}, store.ValidationError("artwork upload unavailable")
	}
	sum := sha256.Sum256(data)
	sourceURL := "beamfall-upload://sha256/" + hex.EncodeToString(sum[:])
	artworkPath := importer.ArtworkPath(media.ID, slot)
	return store.ArtworkCandidateApply{
		MediaID:   media.ID,
		Slot:      slot,
		SourceURL: sourceURL,
		Activity: store.ActivityEntry{
			OccurredAt: time.Now().UTC(),
			SessionID:  scope.SessionID,
			DeviceID:   scope.DeviceID,
			ProfileID:  scope.ProfileID,
			LibraryID:  media.LibraryID,
			TokenID:    scope.APITokenID,
			APIKeyID:   scope.APIKeyID,
		},
		ApplyArtwork: func() (func() error, error) {
			snapshot, err := captureUploadedArtwork(artworkPath)
			if err != nil {
				return nil, err
			}
			return snapshot.restore, importer.ImportArtwork(ctx, media.ID, slot, data)
		},
	}, nil
}

type uploadedArtworkSnapshot struct {
	path  string
	files []uploadedArtworkFileSnapshot
}

type uploadedArtworkFileSnapshot struct {
	path    string
	data    []byte
	existed bool
}

func captureUploadedArtwork(path string) (uploadedArtworkSnapshot, error) {
	paths := []string{path, path + ".generated"}
	derivatives, err := uploadedArtworkDerivativePaths(path)
	if err != nil {
		return uploadedArtworkSnapshot{}, err
	}
	paths = append(paths, derivatives...)
	snapshot := uploadedArtworkSnapshot{path: path, files: make([]uploadedArtworkFileSnapshot, 0, len(paths))}
	for _, filePath := range paths {
		data, existed, err := readUploadedArtworkFile(filePath)
		if err != nil {
			return uploadedArtworkSnapshot{}, err
		}
		snapshot.files = append(snapshot.files, uploadedArtworkFileSnapshot{
			path: filePath, data: data, existed: existed,
		})
	}
	return snapshot, nil
}

func readUploadedArtworkFile(path string) ([]byte, bool, error) {
	// path comes from the server-owned ArtworkPath implementation after Media
	// and slot validation; it is not accepted from the upload request.
	//nolint:gosec // G304: server-derived artwork path, not a caller-supplied filesystem path.
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func (snapshot uploadedArtworkSnapshot) restore() error {
	prior := make(map[string]struct{}, len(snapshot.files))
	for _, file := range snapshot.files {
		if file.existed {
			prior[file.path] = struct{}{}
		}
	}
	var restoreErrors []error
	currentDerivatives, err := uploadedArtworkDerivativePaths(snapshot.path)
	if err != nil {
		restoreErrors = append(restoreErrors, err)
	}
	for _, derivative := range currentDerivatives {
		if _, existed := prior[derivative]; existed {
			continue
		}
		if err := os.Remove(derivative); err != nil && !errors.Is(err, os.ErrNotExist) {
			restoreErrors = append(restoreErrors, err)
		}
	}
	for _, file := range snapshot.files {
		restoreErrors = append(restoreErrors, restoreUploadedArtworkFile(file.path, file.data, file.existed))
	}
	return errors.Join(restoreErrors...)
}

func uploadedArtworkDerivativePaths(path string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	original := filepath.Base(path)
	derivatives := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == original || !strings.HasPrefix(name, base+".") || !strings.HasSuffix(name, ".jpg") {
			continue
		}
		derivatives = append(derivatives, filepath.Join(filepath.Dir(path), name))
	}
	return derivatives, nil
}

func restoreUploadedArtworkFile(path string, data []byte, existed bool) error {
	if existed {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// UnmatchedSuppressionInfo is one active scoped suppression on the wire, with
// the provenance the operator surface renders beside its own reset control.
type UnmatchedSuppressionInfo struct {
	Scope          string `json:"scope"`
	Key            string `json:"key"`
	RecordID       string `json:"recordID,omitempty"`
	CandidateID    string `json:"candidateID,omitempty"`
	SourceHandle   string `json:"sourceHandle,omitempty"`
	ActorProfileID string `json:"actorProfileID,omitempty"`
	CreatedAt      string `json:"createdAt,omitempty"`
}

// unmatchedSuppressions lists the active suppressions, optionally narrowed to
// one record's panel. Manager-gated by the dispatch registry.
func (h *Handler) unmatchedSuppressions(ctx context.Context, _ auth.Scope, req request) ([]UnmatchedSuppressionInfo, error) {
	suppressions, err := h.store.UnmatchedSuppressions(ctx, trimmedVariable(req, "recordID"))
	if err != nil {
		return nil, err
	}
	out := make([]UnmatchedSuppressionInfo, 0, len(suppressions))
	for _, suppression := range suppressions {
		info := UnmatchedSuppressionInfo{
			Scope:          suppression.Scope,
			Key:            suppression.Key,
			RecordID:       suppression.RecordID,
			SourceHandle:   suppression.SourceHandle,
			ActorProfileID: string(suppression.ActorProfileID),
		}
		if suppression.Scope == store.SuppressionScopeCandidate {
			info.CandidateID = candidateIDFromSuppressionKey(suppression.RecordID, suppression.Key)
		}
		if !suppression.CreatedAt.IsZero() {
			info.CreatedAt = suppression.CreatedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, info)
	}
	return out, nil
}

// candidateIDFromSuppressionKey recovers the candidate half of a composite
// candidate key so the Console can name the exact suggestion it is resetting
// without re-deriving the store's key composition.
func candidateIDFromSuppressionKey(recordID, key string) string {
	prefix := store.CandidateSuppressionKey(recordID, "")
	if len(key) > len(prefix) && key[:len(prefix)] == prefix {
		return key[len(prefix):]
	}
	return ""
}

// suppressUnmatched records ONE scoped suppression. The scope is always stated
// by the caller (ADR-0068: never inferred from a generic "Ignore"), and the key
// is derived per scope so a candidate suppression can never be mistaken for a
// whole-record one. Manager-gated; a record-bearing scope must name a record the
// caller can see.
func (h *Handler) suppressUnmatched(ctx context.Context, scope auth.Scope, req request) (bool, error) {
	suppression, err := h.suppressionTarget(ctx, scope, req)
	if err != nil {
		return false, err
	}
	suppression.ActorProfileID = scope.ProfileID
	suppression.CreatedAt = time.Now().UTC()
	if err := h.store.SuppressUnmatched(ctx, suppression); err != nil {
		return false, err
	}
	return true, nil
}

// resetUnmatchedSuppression lifts exactly one scoped suppression and reports
// whether one was active. Resetting one scope leaves both siblings suppressed.
func (h *Handler) resetUnmatchedSuppression(ctx context.Context, scope auth.Scope, req request) (bool, error) {
	suppression, err := h.suppressionTarget(ctx, scope, req)
	if err != nil {
		return false, err
	}
	return h.store.ResetUnmatchedSuppression(ctx, suppression.Scope, suppression.Key)
}

// suppressionTarget resolves the (scope, key) pair plus its provenance from the
// request. Suppress and reset MUST address identities identically or a reset
// could silently miss its own suppression, so both route through this one place.
func (h *Handler) suppressionTarget(ctx context.Context, scope auth.Scope, req request) (store.UnmatchedSuppression, error) {
	suppressionScope := trimmedVariable(req, "scope")
	recordID := trimmedVariable(req, "recordID")
	switch suppressionScope {
	case store.SuppressionScopeSourceRecord:
		record, err := h.visibleUnmatchedRecord(ctx, scope, recordID)
		if err != nil {
			return store.UnmatchedSuppression{}, err
		}
		return store.UnmatchedSuppression{
			Scope: suppressionScope, Key: record.ID,
			RecordID: record.ID, SourceHandle: record.SourceHandle,
		}, nil
	case store.SuppressionScopeFile:
		fingerprint := trimmedVariable(req, "fingerprint")
		if fingerprint == "" {
			return store.UnmatchedSuppression{}, clientError("fingerprint is required for the file scope")
		}
		return store.UnmatchedSuppression{
			Scope: suppressionScope, Key: fingerprint,
			SourceHandle: trimmedVariable(req, "sourceHandle"),
		}, nil
	case store.SuppressionScopeCandidate:
		candidateID := trimmedVariable(req, "candidateID")
		if candidateID == "" {
			return store.UnmatchedSuppression{}, clientError("candidateID is required for the candidate scope")
		}
		record, err := h.visibleUnmatchedRecord(ctx, scope, recordID)
		if err != nil {
			return store.UnmatchedSuppression{}, err
		}
		return store.UnmatchedSuppression{
			Scope: suppressionScope, Key: store.CandidateSuppressionKey(record.ID, candidateID),
			RecordID: record.ID, SourceHandle: record.SourceHandle,
		}, nil
	}
	return store.UnmatchedSuppression{}, clientError("scope must be source-record, file, or candidate")
}
