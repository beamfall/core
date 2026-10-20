package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// V1Store is the /v1/ stable-contract surface's slice of the store (interface
// at the consumer, ADR-0016). Every read takes a LibraryFilter so the Reveal
// contract and Profile-binding hold exactly as they do for the GraphQL surface
// — the v1 handlers never bypass a filtered read path.
type V1Store interface {
	Libraries(ctx context.Context, filter store.LibraryFilter) ([]store.Library, error)
	SourceItems(ctx context.Context, filter store.LibraryFilter, query store.SourceQuery) ([]store.SourceItem, error)
	Media(ctx context.Context, id store.MediaID, filter store.LibraryFilter) (store.Media, error)
	ActiveSessions(ctx context.Context, now time.Time, filter store.LibraryFilter) ([]store.ActiveSession, error)
	LibraryRoots(ctx context.Context, libraryID store.LibraryID) ([]string, error)
	AppendSessionControlEventWithActivity(ctx context.Context, target store.SessionID, payload store.SessionControlPayload, activity store.ActivityEntry, now time.Time) error
	BackupIngest(ctx context.Context, req store.BackupIngestRequest) (store.BackupIngestResult, error)
	BackupIngestStatus(ctx context.Context, profileID store.ProfileID, libraryID store.LibraryID) (store.BackupIngestPolicy, error)
}

// V1ScanEnqueuer enqueues a durable scan job for one (library, root) pair and
// returns the job ID. Scan-job construction lives at the composition root
// (server cannot import internal/scan, ARCHITECTURE.md §4).
type V1ScanEnqueuer func(ctx context.Context, libraryID store.LibraryID, root string) (store.JobID, error)

// mountV1 registers the ADR-0040 /v1/ stable-contract routes. Each route is
// per-route scope-gated against the closed five-scope vocabulary: a scoped
// developer key (Bearer bfk_…) must carry the named scope, mirroring the seam
// the SSE stream already uses. Session- and admin-token-authenticated
// callers carry no API scopes, so a route gated on an API scope rejects them —
// the /v1/ surface is the developer-key surface (HasAPIScope is false for any
// non-scoped-key Scope). The routes are always mounted; without a wired V1Store
// each read handler reports the surface unavailable (503) rather than serving
// an unfiltered read, exactly like the SSE stream's nil-store posture.
func (s *Server) mountV1() {
	for _, route := range v1RouteContracts {
		s.handle(route.Method, route.Pattern, route.RevealClass, s.v1Handler(route.OperationID))
	}
}

func (s *Server) v1Handler(operationID string) http.HandlerFunc {
	switch operationID {
	case "listLibraries":
		return s.v1Libraries
	case "listLibraryMedia":
		return s.v1LibraryMedia
	case "getMedia":
		return s.v1Media
	case "listSessions":
		return s.v1Sessions
	case "playbackControl":
		return s.v1PlaybackControl
	case "scanLibrary":
		return s.v1Scan
	case "getBackupStatus":
		return s.v1BackupStatus
	case "backupMedia":
		return s.v1BackupMedia
	case "subscribeEvents":
		return s.v1EventStream
	default:
		panic("unknown /v1 operation: " + operationID)
	}
}

func (s *Server) v1EventStream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.v1Scope(w, r, store.ScopeEventsSubscribe); !ok {
		return
	}
	s.eventStream(w, r, nil, "no-cache")
}

// SetV1Store wires the /v1/ surface's store. Like SetEvents, this is a
// composition-root setter so New keeps its signature (the conformance harness
// calls New directly); an unwired surface degrades honestly to 503.
func (s *Server) SetV1Store(v1 V1Store) {
	s.v1 = v1
}

// SetV1ScanEnqueuer wires /v1/scans separately from the store reads so the
// server dependency shape matches what each handler uses.
func (s *Server) SetV1ScanEnqueuer(enqueue V1ScanEnqueuer) {
	s.v1ScanEnqueue = enqueue
}

// v1Scope resolves the request credential and enforces the per-route API scope.
// It returns the resolved Scope and true on success; on failure it has already
// written the response (401 for an unresolved credential, 403 for a scoped key
// missing the required scope) and returns false. A non-scoped-key credential —
// a session or admin token — never carries an API scope, so HasAPIScope is
// false and the request is rejected: the /v1/ surface is reachable only by a
// scoped developer key carrying the named scope.
func (s *Server) v1Scope(w http.ResponseWriter, r *http.Request, required store.APIScope) (auth.Scope, bool) {
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid credential")
		return auth.Scope{}, false
	}
	if !scope.HasAPIScope(required) {
		writeError(w, r, http.StatusForbidden, "API key missing required scope: "+string(required))
		return auth.Scope{}, false
	}
	return scope, true
}

// v1LibrarySummary is the wire shape for a Library on the /v1/ catalog surface.
// It is the reveal-filtered Library row reduced to stable, domain-agnostic
// fields (invariant #1): a Profile-bound key only ever sees the Libraries its
// Profile grants, and a restricted Library it has not revealed never appears
// (the store's Libraries read applies libraryVisible).
type v1LibrarySummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Restricted bool   `json:"restricted"`
}

type v1LibrariesResponse struct {
	Libraries  []v1LibrarySummary `json:"libraries"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

func (s *Server) v1Libraries(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.v1Scope(w, r, store.ScopeReadCatalog)
	if !ok {
		return
	}
	if s.v1 == nil {
		writeError(w, r, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	page, err := parseV1Page(r, "listLibraries")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	libraries, err := s.v1.Libraries(r.Context(), scope.LibraryFilter())
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "catalog read failed", err)
		return
	}
	start, end, nextCursor := page.window(len(libraries))
	out := make([]v1LibrarySummary, 0, end-start)
	for _, library := range libraries[start:end] {
		out = append(out, v1LibrarySummary{
			ID:         string(library.ID),
			Title:      library.Title,
			Restricted: library.IsRestricted,
		})
	}
	writeJSON(w, http.StatusOK, v1LibrariesResponse{Libraries: out, NextCursor: nextCursor})
}

// v1MediaItem is the wire shape for a Media row on the /v1/ catalog surface.
// It carries only generic, plugin-agnostic fields (invariant #1/#2): the wire
// entity is Media, never a domain label.
type v1MediaItem struct {
	ID              string `json:"id"`
	LibraryID       string `json:"libraryID"`
	Title           string `json:"title"`
	Summary         string `json:"summary,omitempty"`
	Year            int    `json:"year,omitempty"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
}

type v1LibraryMediaResponse struct {
	Media      []v1MediaItem `json:"media"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

func (s *Server) v1LibraryMedia(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.v1Scope(w, r, store.ScopeReadCatalog)
	if !ok {
		return
	}
	if s.v1 == nil {
		writeError(w, r, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	libraryID := store.LibraryID(r.PathValue("libraryID"))
	if libraryID == "" {
		writeError(w, r, http.StatusBadRequest, "libraryID is required")
		return
	}
	page, err := parseV1Page(r, "listLibraryMedia")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	// SourceItems applies the reveal filter and scopes to the named Library; a
	// Library outside the key's grants (or restricted and unrevealed) yields an
	// empty feed, never an error — hidden and missing stay indistinguishable.
	items, err := s.v1.SourceItems(r.Context(), scope.LibraryFilter(), store.SourceQuery{
		Kind:      "all-items",
		LibraryID: libraryID,
		Limit:     page.limit + 1,
		Offset:    page.offset,
	})
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "catalog read failed", err)
		return
	}
	nextCursor := ""
	if len(items) > page.limit {
		items = items[:page.limit]
		nextCursor = page.nextCursor()
	}
	out := make([]v1MediaItem, 0, len(items))
	for _, item := range items {
		out = append(out, v1MediaItem{
			ID:        string(item.ID),
			LibraryID: string(libraryID),
			Title:     item.Title,
			Summary:   item.Summary,
		})
	}
	writeJSON(w, http.StatusOK, v1LibraryMediaResponse{Media: out, NextCursor: nextCursor})
}

const (
	v1PageDefaultLimit = 50
	v1PageMaxLimit     = 100
)

func (s *Server) v1Media(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.v1Scope(w, r, store.ScopeReadCatalog)
	if !ok {
		return
	}
	if s.v1 == nil {
		writeError(w, r, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	mediaID := store.MediaID(r.PathValue("mediaID"))
	if mediaID == "" {
		writeError(w, r, http.StatusBadRequest, "mediaID is required")
		return
	}
	media, err := s.v1.Media(r.Context(), mediaID, scope.LibraryFilter())
	if err != nil {
		// Media resolves a hidden or unknown id to the same not-found, so the
		// detail read is never an existence oracle for Media the key cannot see.
		writeError(w, r, http.StatusNotFound, "media not found")
		return
	}
	writeJSON(w, http.StatusOK, v1MediaItem{
		ID:              string(media.ID),
		LibraryID:       string(media.LibraryID),
		Title:           media.Title,
		Summary:         media.Summary,
		Year:            media.Year,
		DurationSeconds: media.DurationSeconds,
	})
}

// v1SessionItem is the wire shape for one active playback session on the /v1/
// sessions surface. Media-identifying fields are empty when the
// session is playing Media the key cannot currently see — the store's
// ActiveSessions read suppresses Title/LibraryID under the LibraryFilter, so
// the Reveal contract holds here exactly as on the Console dashboard.
type v1SessionItem struct {
	SessionID       string `json:"sessionID"`
	ProfileID       string `json:"profileID"`
	DeviceName      string `json:"deviceName,omitempty"`
	MediaID         string `json:"mediaID,omitempty"`
	Title           string `json:"title,omitempty"`
	LibraryID       string `json:"libraryID,omitempty"`
	Transcoding     bool   `json:"transcoding"`
	PositionSeconds int    `json:"positionSeconds,omitempty"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
}

type v1SessionsResponse struct {
	Sessions   []v1SessionItem `json:"sessions"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

func (s *Server) v1Sessions(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.v1Scope(w, r, store.ScopeReadSessions)
	if !ok {
		return
	}
	if s.v1 == nil {
		writeError(w, r, http.StatusServiceUnavailable, "sessions unavailable")
		return
	}
	page, err := parseV1Page(r, "listSessions")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	sessions, err := s.v1.ActiveSessions(r.Context(), time.Now().UTC(), scope.LibraryFilter())
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "sessions read failed", err)
		return
	}
	out := make([]v1SessionItem, 0, len(sessions))
	for _, session := range sessions {
		// Profile-binding: a Profile-bound key sees only its own
		// Profile's sessions; an admin-scoped key (no Profile binding) sees all.
		// The store filters Media visibility but reports every session's row to
		// the operator dashboard, so the own-Profile narrowing is applied here.
		if scope.ProfileID != "" && session.ProfileID != scope.ProfileID {
			continue
		}
		out = append(out, v1SessionItem{
			SessionID:       string(session.SessionID),
			ProfileID:       string(session.ProfileID),
			DeviceName:      session.DeviceName,
			MediaID:         string(session.MediaID),
			Title:           session.Title,
			LibraryID:       string(session.LibraryID),
			Transcoding:     session.Transcoding,
			PositionSeconds: session.PositionSeconds,
			DurationSeconds: session.DurationSeconds,
		})
	}
	start, end, nextCursor := page.window(len(out))
	writeJSON(w, http.StatusOK, v1SessionsResponse{Sessions: out[start:end], NextCursor: nextCursor})
}

type v1Page struct {
	operation string
	offset    int
	limit     int
}

func parseV1Page(r *http.Request, operation string) (v1Page, error) {
	page := v1Page{operation: operation, limit: v1PageDefaultLimit}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > v1PageMaxLimit {
			return v1Page{}, fmt.Errorf("limit must be between 1 and %d", v1PageMaxLimit)
		}
		page.limit = limit
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			return v1Page{}, errors.New("invalid cursor")
		}
		prefix := operation + ":"
		if !strings.HasPrefix(string(decoded), prefix) {
			return v1Page{}, errors.New("invalid cursor")
		}
		offset, err := strconv.Atoi(strings.TrimPrefix(string(decoded), prefix))
		if err != nil || offset < 0 {
			return v1Page{}, errors.New("invalid cursor")
		}
		page.offset = offset
	}
	return page, nil
}

func (p v1Page) nextCursor() string {
	raw := fmt.Sprintf("%s:%d", p.operation, p.offset+p.limit)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func (p v1Page) window(length int) (start, end int, nextCursor string) {
	start = min(p.offset, length)
	end = min(start+p.limit, length)
	if end < length {
		nextCursor = p.nextCursor()
	}
	return start, end, nextCursor
}

type v1PlaybackControlRequest struct {
	SessionID string `json:"sessionID"`
	Command   string `json:"command"`
}

type v1PlaybackControlResponse struct {
	SessionID string `json:"sessionID"`
	Command   string `json:"command"`
}

func (s *Server) v1PlaybackControl(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	scope, ok := s.v1Scope(w, r, store.ScopePlaybackControl)
	if !ok {
		return
	}
	if s.v1 == nil {
		writeError(w, r, http.StatusServiceUnavailable, "playback control unavailable")
		return
	}
	var req v1PlaybackControlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	sessionID := store.SessionID(strings.TrimSpace(req.SessionID))
	command := strings.TrimSpace(req.Command)
	if sessionID == "" {
		writeError(w, r, http.StatusBadRequest, "sessionID is required")
		return
	}
	if !store.SessionControlCommandValid(command) {
		writeError(w, r, http.StatusBadRequest, "unknown transport command")
		return
	}

	active, err := s.v1.ActiveSessions(r.Context(), time.Now().UTC(), scope.LibraryFilter())
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "sessions read failed", err)
		return
	}
	var target store.ActiveSession
	foundVisible := false
	for _, activeSession := range active {
		if activeSession.SessionID != sessionID {
			continue
		}
		if scope.ProfileID != "" && activeSession.ProfileID != scope.ProfileID {
			continue
		}
		if activeSession.Title == "" {
			continue
		}
		target = activeSession
		foundVisible = true
		break
	}
	if !foundVisible {
		writeError(w, r, http.StatusNotFound, "session not found")
		return
	}

	now := time.Now().UTC()
	if err := s.v1.AppendSessionControlEventWithActivity(r.Context(), sessionID, store.SessionControlPayload{
		Command:    command,
		FromDevice: scope.DeviceID,
		ProfileID:  target.ProfileID,
		MediaID:    target.MediaID,
	}, store.ActivityEntry{
		OccurredAt: now,
		Action:     store.ActivitySessionControl,
		// A scoped API key is the actor (APIKeyID). ProfileID anchors the row to
		// the target Profile so self-scoped activity reads can see that their
		// session was controlled without seeing the operator-only key id.
		ProfileID:  target.ProfileID,
		TargetType: "session",
		TargetID:   string(sessionID),
		APIKeyID:   scope.APIKeyID,
	}, now); err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "playback control failed", err)
		return
	}
	writeJSON(w, http.StatusAccepted, v1PlaybackControlResponse{SessionID: string(sessionID), Command: command})
}

// v1ScanRequest is the POST /v1/scans body: a Library to scan and an optional
// root. An empty root scans the Library's persisted roots, mirroring the
// GraphQL scanLibrary mutation.
type v1ScanRequest struct {
	LibraryID string `json:"libraryID"`
	Root      string `json:"root"`
}

type v1ScanResponse struct {
	JobIDs []string `json:"jobIDs"`
}

const v1ScanJobLimit = 200

func (s *Server) v1Scan(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	scope, ok := s.v1Scope(w, r, store.ScopeIngest)
	if !ok {
		return
	}
	if s.v1 == nil || s.v1ScanEnqueue == nil {
		writeError(w, r, http.StatusServiceUnavailable, "ingest unavailable")
		return
	}
	var req v1ScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	libraryID := store.LibraryID(strings.TrimSpace(req.LibraryID))
	if libraryID == "" {
		writeError(w, r, http.StatusBadRequest, "libraryID is required")
		return
	}
	// Gate on the same Library-visibility check scanLibrary applies: a Library
	// the key cannot currently see is "unknown" (never an existence oracle), so
	// the enqueue is refused before touching Library-scoped state.
	libraries, err := s.v1.Libraries(r.Context(), scope.LibraryFilter())
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "library read failed", err)
		return
	}
	known := false
	for _, library := range libraries {
		if library.ID == libraryID {
			known = true
			break
		}
	}
	if !known {
		writeError(w, r, http.StatusNotFound, "unknown library")
		return
	}
	persisted, err := s.v1.LibraryRoots(r.Context(), libraryID)
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "library roots read failed", err)
		return
	}
	if len(persisted) == 0 {
		writeError(w, r, http.StatusBadRequest, "library has no roots")
		return
	}
	roots := persisted
	if root := strings.TrimSpace(req.Root); root != "" {
		if !stringInSlice(root, persisted) {
			writeError(w, r, http.StatusBadRequest, "root is not configured for library")
			return
		}
		roots = []string{root}
	}
	if len(roots) > v1ScanJobLimit {
		writeError(w, r, http.StatusBadRequest, "too many library roots; specify one root")
		return
	}
	jobIDs := make([]string, 0, len(roots))
	for _, root := range roots {
		jobID, err := s.v1ScanEnqueue(r.Context(), libraryID, root)
		if err != nil {
			writeErrorCause(w, r, http.StatusInternalServerError, "scan enqueue failed", err)
			return
		}
		jobIDs = append(jobIDs, string(jobID))
	}
	writeJSON(w, http.StatusAccepted, v1ScanResponse{JobIDs: jobIDs})
}

func stringInSlice(needle string, haystack []string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

const (
	maxBackupUploadBody      = 64 << 20
	maxBackupMultipartMemory = 1 << 20
	maxBackupImageSniffBytes = 1 << 20
	maxBackupImagePixels     = 100_000_000
	backupSecurePhaseName    = "backup secure phase"

	// backupUploadReadDeadline bounds how long a single backup upload's body
	// read may take, extended past the server's global 60s ReadTimeout
	// (internal/app/app.go) so a maxBackupUploadBody (64 MiB) body still
	// finishes on a slow-uplink floor of ~600 Kbit/s sustained.
	backupUploadReadDeadline = 15 * time.Minute
)

type v1BackupStatusResponse struct {
	LibraryID  string `json:"libraryID"`
	Paused     bool   `json:"paused"`
	QuotaBytes int64  `json:"quotaBytes"`
}

func (s *Server) v1BackupStatus(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.v1Scope(w, r, store.ScopeBackup)
	if !ok {
		return
	}
	if s.v1 == nil {
		writeError(w, r, http.StatusServiceUnavailable, "backup ingest unavailable")
		return
	}
	if scope.ProfileID == "" {
		writeError(w, r, http.StatusForbidden, "backup scope must be profile-bound")
		return
	}
	libraryID := store.LibraryID(strings.TrimSpace(r.PathValue("libraryID")))
	if libraryID == "" {
		writeError(w, r, http.StatusBadRequest, "libraryID is required")
		return
	}
	if !s.v1BackupLibraryAllowed(w, r, scope, libraryID) {
		return
	}
	policy, err := s.v1.BackupIngestStatus(r.Context(), scope.ProfileID, libraryID)
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "backup status read failed", err)
		return
	}
	writeJSON(w, http.StatusOK, v1BackupStatusResponse{
		LibraryID:  string(libraryID),
		Paused:     policy.Paused,
		QuotaBytes: policy.QuotaBytes,
	})
}

type v1BackupMediaResponse struct {
	MediaID     string `json:"mediaID"`
	LibraryID   string `json:"libraryID"`
	Fingerprint string `json:"fingerprint"`
	Paused      bool   `json:"paused"`
}

func (s *Server) v1BackupMedia(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(backupUploadReadDeadline)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeErrorCause(w, r, http.StatusInternalServerError, "backup upload deadline failed", err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupUploadBody)
	scope, ok := s.v1Scope(w, r, store.ScopeBackup)
	if !ok {
		return
	}
	if s.v1 == nil {
		writeError(w, r, http.StatusServiceUnavailable, "backup ingest unavailable")
		return
	}
	if s.backupEncryptor == nil {
		writeError(w, r, http.StatusServiceUnavailable, backupSecurePhaseName+" unavailable")
		return
	}
	if scope.ProfileID == "" {
		writeError(w, r, http.StatusForbidden, "backup scope must be profile-bound")
		return
	}
	if err := r.ParseMultipartForm(maxBackupMultipartMemory); err != nil {
		writeError(w, r, http.StatusBadRequest, "multipart backup upload is required")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	libraryID := store.LibraryID(strings.TrimSpace(r.FormValue("libraryID")))
	if libraryID == "" {
		writeError(w, r, http.StatusBadRequest, "libraryID is required")
		return
	}
	if !s.v1BackupLibraryAllowed(w, r, scope, libraryID) {
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	if header.Size <= 0 {
		writeError(w, r, http.StatusBadRequest, "file is empty")
		return
	}
	class := store.MediaClass(strings.TrimSpace(r.FormValue("class")))
	contentType := header.Header.Get("Content-Type")
	plainReader := io.Reader(file)
	if class == store.MediaClassImage || strings.HasPrefix(contentType, "image/") {
		prefix, err := readBackupPrefix(file, maxBackupImageSniffBytes)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "backup file read failed")
			return
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(prefix))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "image decode rejected upload")
			return
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > maxBackupImagePixels/int64(cfg.Height) {
			writeError(w, r, http.StatusBadRequest, "image dimensions exceed backup limit")
			return
		}
		if class == "" {
			class = store.MediaClassImage
		}
		plainReader = io.MultiReader(bytes.NewReader(prefix), file)
	}
	result, err := s.v1.BackupIngest(r.Context(), store.BackupIngestRequest{
		LibraryID:   libraryID,
		ProfileID:   scope.ProfileID,
		Title:       r.FormValue("title"),
		Container:   firstNonEmpty(strings.TrimSpace(r.FormValue("container")), backupContainerFromFilename(header.Filename)),
		Codec:       strings.TrimSpace(r.FormValue("codec")),
		Class:       class,
		SizeBytes:   header.Size,
		ModTime:     time.Now().UTC(),
		Filter:      scope.LibraryFilter(),
		Encryptor:   s.backupEncryptor,
		PlainReader: plainReader,
	})
	if err != nil {
		var validationErr store.ValidationError
		switch {
		case errors.Is(err, store.ErrBackupIngestPaused):
			writeJSON(w, http.StatusLocked, v1BackupMediaResponse{LibraryID: string(libraryID), Paused: true})
		case errors.Is(err, store.ErrBackupIngestQuotaExceeded):
			writeError(w, r, http.StatusInsufficientStorage, "backup quota exceeded")
		case errors.Is(err, store.ErrBackupIngestHeadroom):
			writeError(w, r, http.StatusInsufficientStorage, "backup disk headroom exhausted")
		case errors.As(err, &validationErr):
			writeError(w, r, http.StatusBadRequest, validationErr.Error())
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "backup ingest failed", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, v1BackupMediaResponse{
		MediaID:     string(result.Media.ID),
		LibraryID:   string(result.Media.LibraryID),
		Fingerprint: result.Fingerprint,
	})
}

func readBackupPrefix(r io.Reader, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.CopyN(&buf, r, limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) v1BackupLibraryAllowed(w http.ResponseWriter, r *http.Request, scope auth.Scope, libraryID store.LibraryID) bool {
	libraries, err := s.v1.Libraries(r.Context(), scope.LibraryFilter())
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "library read failed", err)
		return false
	}
	for _, library := range libraries {
		if library.ID == libraryID {
			return true
		}
	}
	writeError(w, r, http.StatusNotFound, "unknown library")
	return false
}

func backupContainerFromFilename(filename string) string {
	ext := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(filepath.Ext(filename))), ".")
	if ext == "" {
		return "bin"
	}
	return ext
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
