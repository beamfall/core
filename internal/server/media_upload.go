package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/reveal"
	"github.com/beamfall/core/internal/store"
)

// Media upload from the manager (CORE-3192): an admin stages files in chunks,
// reviews a proposed Entity and destination per file, then commits. The routes
// live here; filename parsing and Entity matching come from the plugins through
// a MediaUploadPlanner the app composes, so this package stays free of domain
// vocabulary and of the scan pipeline.
const (
	mediaUploadsPattern         = "/api/media/uploads"
	mediaUploadPattern          = "/api/media/uploads/{id}"
	mediaUploadProposalsPattern = "/api/media/uploads/proposals"
	mediaUploadTargetsPattern   = "/api/media/uploads/targets"
	mediaUploadCommitPattern    = "/api/media/uploads/commit"

	// MediaUploadChunkBytes is the largest chunk one PUT may carry.
	MediaUploadChunkBytes  = 32 << 20
	maxMediaUploadJSONBody = 1 << 20
	maxMediaUploadBatch    = 200
	// MediaUploadOffsetHeader names the byte offset a chunk starts at.
	MediaUploadOffsetHeader = "Upload-Offset"
)

// MediaUploadParse is what the library's plugin parser read from a path.
type MediaUploadParse struct {
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Sequence string `json:"sequence,omitempty"`
	Index    int    `json:"index,omitempty"`
	IndexEnd int    `json:"indexEnd,omitempty"`
}

// MediaUploadTarget is an Entity an upload can join, with the folder its
// files live in. KindLabel is the plugin's display label for the Entity kind.
type MediaUploadTarget struct {
	EntityID    string `json:"entityID"`
	Title       string `json:"title"`
	Year        int    `json:"year,omitempty"`
	KindLabel   string `json:"kindLabel"`
	Destination string `json:"destination"`
}

// MediaUploadProposal is the proposed assignment for one complete upload.
type MediaUploadProposal struct {
	UploadID    string             `json:"uploadID"`
	FileName    string             `json:"fileName"`
	Parsed      *MediaUploadParse  `json:"parsed,omitempty"`
	Target      *MediaUploadTarget `json:"target,omitempty"`
	Destination string             `json:"destination"`
	// Rescan is what the parser reads at Destination/FileName, so the admin
	// sees that a rescan keeps the file where it lands.
	Rescan   *MediaUploadParse `json:"rescan,omitempty"`
	Conflict bool              `json:"conflict"`
}

// MediaUploadPlanner proposes assignments and searches targets with the
// library's plugins, and starts the scan after a commit.
type MediaUploadPlanner interface {
	ProposeMediaUploads(ctx context.Context, filter store.LibraryFilter, libraryID store.LibraryID, uploads []store.MediaUpload) ([]MediaUploadProposal, error)
	SearchMediaUploadTargets(ctx context.Context, filter store.LibraryFilter, libraryID store.LibraryID, query string) ([]MediaUploadTarget, error)
	MediaUploadCommitted(ctx context.Context, libraryID store.LibraryID, root string) error
}

type mediaUploadStore interface {
	CreateMediaUpload(ctx context.Context, libraryID store.LibraryID, fileName, relativeDir string, size int64, filter store.LibraryFilter, now time.Time) (store.MediaUpload, error)
	MediaUploadStatus(ctx context.Context, id string, filter store.LibraryFilter, now time.Time) (store.MediaUpload, error)
	AppendMediaUploadChunk(ctx context.Context, id string, offset int64, body io.Reader, filter store.LibraryFilter, now time.Time) (store.MediaUpload, error)
	CancelMediaUpload(ctx context.Context, id string, filter store.LibraryFilter, now time.Time) error
	CommitMediaUpload(ctx context.Context, id, destDir string, conflict store.MediaUploadConflict, filter store.LibraryFilter, now time.Time) (store.MediaUploadCommit, error)
	AppendActivity(ctx context.Context, entry store.ActivityEntry) error
}

// WithMediaUploadPlanner enables the media upload routes' proposal, target
// search and post-commit scan.
func WithMediaUploadPlanner(planner MediaUploadPlanner) Option {
	return func(s *Server) { s.mediaUploadPlanner = planner }
}

func (s *Server) registerMediaUploadRoutes() {
	s.handle(http.MethodPost, mediaUploadsPattern, reveal.ContentJSON, s.createMediaUpload)
	s.handle(http.MethodGet, mediaUploadPattern, reveal.ContentJSON, s.mediaUploadStatus)
	s.handle(http.MethodPut, mediaUploadPattern, reveal.ContentJSON, s.appendMediaUpload)
	s.handle(http.MethodDelete, mediaUploadPattern, reveal.ContentJSON, s.cancelMediaUpload)
	s.handle(http.MethodPost, mediaUploadProposalsPattern, reveal.ContentJSON, s.proposeMediaUploads)
	s.handle(http.MethodPost, mediaUploadTargetsPattern, reveal.ContentJSON, s.searchMediaUploadTargets)
	s.handle(http.MethodPost, mediaUploadCommitPattern, reveal.ContentJSON, s.commitMediaUploads)
}

// mediaUploadScope admits only an admin session with the CSRF header; API
// tokens, API keys and integration bearers are refused.
func (s *Server) mediaUploadScope(w http.ResponseWriter, r *http.Request) (auth.Scope, mediaUploadStore, bool) {
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return auth.Scope{}, nil, false
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeScopeError(w, r, err, "invalid session")
		return auth.Scope{}, nil, false
	}
	if scope.APITokenID != "" || scope.APIKeyID != "" || scope.IntegrationKeyID != "" ||
		scope.SessionKind == store.SessionKindIntegration || !scope.Admin {
		writeError(w, r, http.StatusForbidden, "admin session required")
		return auth.Scope{}, nil, false
	}
	uploads := s.mediaUploads
	if uploads == nil {
		writeError(w, r, http.StatusServiceUnavailable, "media upload unavailable")
		return auth.Scope{}, nil, false
	}
	return scope, uploads, true
}

func writeMediaUploadError(w http.ResponseWriter, r *http.Request, err error) {
	var verr store.ValidationError
	switch {
	case errors.As(err, &verr):
		writeError(w, r, http.StatusBadRequest, string(verr))
	case errors.Is(err, store.ErrMediaUploadNotFound):
		writeError(w, r, http.StatusNotFound, "upload not found")
	default:
		writeErrorCause(w, r, http.StatusInternalServerError, "media upload failed", err)
	}
}

type mediaUploadStatus struct {
	UploadID    string `json:"uploadID"`
	FileName    string `json:"fileName"`
	RelativeDir string `json:"relativeDir"`
	Size        int64  `json:"size"`
	Offset      int64  `json:"offset"`
	ChunkSize   int64  `json:"chunkSize"`
}

func mediaUploadStatusOf(u store.MediaUpload) mediaUploadStatus {
	return mediaUploadStatus{
		UploadID: u.ID, FileName: u.FileName, RelativeDir: u.RelativeDir,
		Size: u.Size, Offset: u.Offset, ChunkSize: MediaUploadChunkBytes,
	}
}

func decodeMediaUploadJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxMediaUploadJSONBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeDecodeError(w, r, err)
		return false
	}
	return true
}

func (s *Server) createMediaUpload(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	scope, uploads, ok := s.mediaUploadScope(w, r)
	if !ok {
		return
	}
	var req struct {
		LibraryID   string `json:"libraryID"`
		FileName    string `json:"fileName"`
		RelativeDir string `json:"relativeDir"`
		Size        int64  `json:"size"`
	}
	if !decodeMediaUploadJSON(w, r, &req) {
		return
	}
	upload, err := uploads.CreateMediaUpload(r.Context(), store.LibraryID(strings.TrimSpace(req.LibraryID)),
		req.FileName, req.RelativeDir, req.Size, scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		writeMediaUploadError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mediaUploadStatusOf(upload))
}

func (s *Server) mediaUploadStatus(w http.ResponseWriter, r *http.Request) {
	scope, uploads, ok := s.mediaUploadScope(w, r)
	if !ok {
		return
	}
	upload, err := uploads.MediaUploadStatus(r.Context(), r.PathValue("id"), scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		writeMediaUploadError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mediaUploadStatusOf(upload))
}

func (s *Server) appendMediaUpload(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, MediaUploadChunkBytes)
	scope, uploads, ok := s.mediaUploadScope(w, r)
	if !ok {
		return
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get(MediaUploadOffsetHeader)), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, r, http.StatusBadRequest, "Upload-Offset header is required")
		return
	}
	upload, err := uploads.AppendMediaUploadChunk(r.Context(), r.PathValue("id"), offset, r.Body, scope.LibraryFilter(), time.Now().UTC())
	if errors.Is(err, store.ErrMediaUploadOffset) {
		writeJSON(w, http.StatusConflict, mediaUploadStatusOf(upload))
		return
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "chunk too large")
		return
	}
	if err != nil {
		writeMediaUploadError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mediaUploadStatusOf(upload))
}

func (s *Server) cancelMediaUpload(w http.ResponseWriter, r *http.Request) {
	scope, uploads, ok := s.mediaUploadScope(w, r)
	if !ok {
		return
	}
	if err := uploads.CancelMediaUpload(r.Context(), r.PathValue("id"), scope.LibraryFilter(), time.Now().UTC()); err != nil {
		writeMediaUploadError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

func (s *Server) proposeMediaUploads(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	scope, uploads, ok := s.mediaUploadScope(w, r)
	if !ok {
		return
	}
	var req struct {
		LibraryID string   `json:"libraryID"`
		UploadIDs []string `json:"uploadIDs"`
	}
	if !decodeMediaUploadJSON(w, r, &req) {
		return
	}
	if len(req.UploadIDs) == 0 || len(req.UploadIDs) > maxMediaUploadBatch {
		writeError(w, r, http.StatusBadRequest, "uploadIDs must list 1 to 200 uploads")
		return
	}
	if s.mediaUploadPlanner == nil {
		writeError(w, r, http.StatusServiceUnavailable, "media upload planning unavailable")
		return
	}
	libraryID := store.LibraryID(strings.TrimSpace(req.LibraryID))
	filter := scope.LibraryFilter()
	now := time.Now().UTC()
	staged := make([]store.MediaUpload, 0, len(req.UploadIDs))
	for _, id := range req.UploadIDs {
		upload, err := uploads.MediaUploadStatus(r.Context(), id, filter, now)
		if err != nil {
			writeMediaUploadError(w, r, err)
			return
		}
		if upload.LibraryID != libraryID {
			writeError(w, r, http.StatusBadRequest, "upload belongs to another library")
			return
		}
		staged = append(staged, upload)
	}
	proposals, err := s.mediaUploadPlanner.ProposeMediaUploads(r.Context(), filter, libraryID, staged)
	if err != nil {
		writeMediaUploadError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": proposals})
}

func (s *Server) searchMediaUploadTargets(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	scope, _, ok := s.mediaUploadScope(w, r)
	if !ok {
		return
	}
	var req struct {
		LibraryID string `json:"libraryID"`
		Query     string `json:"query"`
	}
	if !decodeMediaUploadJSON(w, r, &req) {
		return
	}
	if s.mediaUploadPlanner == nil {
		writeError(w, r, http.StatusServiceUnavailable, "media upload planning unavailable")
		return
	}
	targets, err := s.mediaUploadPlanner.SearchMediaUploadTargets(r.Context(), scope.LibraryFilter(),
		store.LibraryID(strings.TrimSpace(req.LibraryID)), strings.TrimSpace(req.Query))
	if err != nil {
		writeMediaUploadError(w, r, err)
		return
	}
	if targets == nil {
		targets = []MediaUploadTarget{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": targets})
}

type mediaUploadCommitResult struct {
	UploadID    string `json:"uploadID"`
	Path        string `json:"path,omitempty"`
	TrashedPath string `json:"trashedPath,omitempty"`
	TrashID     string `json:"trashID,omitempty"`
	Error       string `json:"error,omitempty"`
}

// commitMediaUploads places each upload independently, so one refused item
// does not undo the others; every placed file and every replaced file is
// audited, and each touched root is scanned once.
func (s *Server) commitMediaUploads(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	scope, uploads, ok := s.mediaUploadScope(w, r)
	if !ok {
		return
	}
	var req struct {
		Items []struct {
			UploadID    string `json:"uploadID"`
			Destination string `json:"destination"`
			Conflict    string `json:"conflict"`
		} `json:"items"`
	}
	if !decodeMediaUploadJSON(w, r, &req) {
		return
	}
	if len(req.Items) == 0 || len(req.Items) > maxMediaUploadBatch {
		writeError(w, r, http.StatusBadRequest, "items must list 1 to 200 uploads")
		return
	}
	ctx := r.Context()
	filter := scope.LibraryFilter()
	results := make([]mediaUploadCommitResult, 0, len(req.Items))
	type scanKey struct {
		library store.LibraryID
		root    string
	}
	scans := map[scanKey]struct{}{}
	for _, item := range req.Items {
		result := mediaUploadCommitResult{UploadID: item.UploadID}
		now := time.Now().UTC()
		upload, err := uploads.MediaUploadStatus(ctx, item.UploadID, filter, now)
		if err == nil {
			var commit store.MediaUploadCommit
			commit, err = uploads.CommitMediaUpload(ctx, item.UploadID, item.Destination,
				store.MediaUploadConflict(strings.TrimSpace(item.Conflict)), filter, now)
			if err == nil {
				result.Path = commit.Path
				if commit.Trashed != nil {
					result.TrashedPath = commit.Trashed.OriginalPath
					result.TrashID = commit.Trashed.TrashID
					s.appendMediaUploadActivity(ctx, uploads, scope, store.ActivityManagedFileDelete, "managed-trash", commit.Trashed.TrashID, upload.LibraryID)
				}
				s.appendMediaUploadActivity(ctx, uploads, scope, store.ActivityManagedFileCreate, "path", commit.Path, upload.LibraryID)
				scans[scanKey{upload.LibraryID, commit.Root}] = struct{}{}
			}
		}
		if err != nil {
			var verr store.ValidationError
			switch {
			case errors.As(err, &verr):
				result.Error = string(verr)
			case errors.Is(err, store.ErrMediaUploadNotFound):
				result.Error = "upload not found"
			default:
				slog.Error("media upload commit failed", "upload", item.UploadID, "error", err)
				result.Error = "commit failed"
			}
		}
		results = append(results, result)
	}
	if s.mediaUploadPlanner != nil {
		for key := range scans {
			if err := s.mediaUploadPlanner.MediaUploadCommitted(ctx, key.library, key.root); err != nil {
				slog.Error("media upload scan enqueue failed", "library", key.library, "error", err)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) appendMediaUploadActivity(ctx context.Context, uploads mediaUploadStore, scope auth.Scope, action, targetType, targetID string, libraryID store.LibraryID) {
	if err := uploads.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: time.Now().UTC(),
		Action:     action,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: targetType,
		TargetID:   targetID,
		LibraryID:  libraryID,
	}); err != nil {
		slog.Error("media upload audit append failed", "action", action, "error", err)
	}
}
