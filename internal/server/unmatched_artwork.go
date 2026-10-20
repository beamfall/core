package server

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

const (
	createMediaArtworkRoutePattern       = "/api/media/artwork-upload/{recordID}"
	createMediaArtworkRouteKey           = "POST " + createMediaArtworkRoutePattern
	maxArtworkUploadBytes          int64 = 10 << 20
	maxArtworkUploadBody                 = maxArtworkUploadBytes + 64<<10
	maxArtworkPixels                     = 40_000_000
)

// AuditOutcome is the result of an audited server command.
type AuditOutcome string

const (
	AuditSuccess AuditOutcome = "success"
	AuditError   AuditOutcome = "error"
)

type unmatchedArtworkCreator interface {
	CreateMediaFromUnmatchedArtwork(
		context.Context,
		auth.Scope,
		string,
		string,
		string,
		int,
		[]byte,
	) (string, error)
	BeginAuditedCommand(context.Context, auth.Scope, string) (string, error)
	FinishAuditedCommand(
		context.Context,
		auth.Scope,
		string,
		string,
		AuditOutcome,
	) error
}

type createMediaArtworkResponse struct {
	MediaID string `json:"mediaID"`
}

func (s *Server) createMediaWithArtwork(w http.ResponseWriter, r *http.Request) {
	if s.unmatchedArtwork == nil {
		writeError(w, r, http.StatusServiceUnavailable, "artwork upload unavailable")
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	if scope.DeviceRole != store.DeviceRoleManager {
		writeError(w, r, http.StatusForbidden, "manager role required")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		writeError(w, r, http.StatusUnsupportedMediaType, "Content-Type must be multipart/form-data")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArtworkUploadBody)
	if err := r.ParseMultipartForm(maxArtworkUploadBytes); err != nil {
		writeMultipartArtworkError(w, r, err)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	recordID := strings.TrimSpace(r.PathValue("recordID"))
	libraryID := strings.TrimSpace(r.FormValue("libraryID"))
	title := strings.TrimSpace(r.FormValue("title"))
	year, err := optionalArtworkYear(r.FormValue("year"))
	if err != nil || recordID == "" || libraryID == "" || title == "" {
		writeError(w, r, http.StatusBadRequest, "recordID, libraryID, and title are required")
		return
	}
	file, _, err := r.FormFile("artwork")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "artwork is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxArtworkUploadBytes+1))
	if err != nil {
		writeErrorCause(w, r, http.StatusBadRequest, "could not read artwork", err)
		return
	}
	if int64(len(data)) > maxArtworkUploadBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "artwork is too large")
		return
	}
	if !validUploadedArtwork(data) {
		writeError(w, r, http.StatusUnsupportedMediaType, "artwork must be a valid JPEG or PNG image")
		return
	}
	auditOperation := "http:" + createMediaArtworkRouteKey
	operationID, err := s.unmatchedArtwork.BeginAuditedCommand(r.Context(), scope, auditOperation)
	if err != nil {
		writeErrorCause(w, r, http.StatusServiceUnavailable, "privileged audit unavailable", err)
		return
	}
	mediaID, err := s.unmatchedArtwork.CreateMediaFromUnmatchedArtwork(
		r.Context(), scope, recordID, libraryID, title, year, data,
	)
	if err != nil {
		if auditErr := s.unmatchedArtwork.FinishAuditedCommand(
			r.Context(), scope, auditOperation, operationID, AuditError,
		); auditErr != nil {
			slog.ErrorContext(r.Context(), "unmatched artwork final audit failed",
				"operation", operationID, "error", auditErr)
		}
		if errors.Is(err, store.ErrUnmatchedCreateConflict) {
			writeErrorCause(w, r, http.StatusConflict, "unmatched record was resolved by a different request", err)
			return
		}
		writeErrorCause(w, r, http.StatusInternalServerError, "could not create Media with artwork", err)
		return
	}
	if err := s.unmatchedArtwork.FinishAuditedCommand(
		r.Context(), scope, auditOperation, operationID, AuditSuccess,
	); err != nil {
		slog.ErrorContext(r.Context(), "unmatched artwork final audit failed",
			"operation", operationID, "error", err)
	}
	writeJSON(w, http.StatusCreated, createMediaArtworkResponse{MediaID: mediaID})
}

func optionalArtworkYear(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	year, err := strconv.Atoi(raw)
	if err != nil || year < 0 {
		return 0, errors.New("invalid year")
	}
	return year, nil
}

func validUploadedArtwork(data []byte) bool {
	contentType := http.DetectContentType(data)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return false
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return false
	}
	if format != "jpeg" && format != "png" {
		return false
	}
	if config.Width <= 0 || config.Height <= 0 {
		return false
	}
	return config.Width <= maxArtworkPixels/config.Height
}

func writeMultipartArtworkError(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) || errors.Is(err, multipart.ErrMessageTooLarge) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "artwork is too large")
		return
	}
	writeError(w, r, http.StatusBadRequest, "invalid multipart body")
}
