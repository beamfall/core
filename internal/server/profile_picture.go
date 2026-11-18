package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Profile picture routes (CORE-1240). The upload is multipart, so its
// pattern is excluded from handle()'s JSON Content-Type check like the
// artwork upload.
const (
	profilePictureUploadPattern = "/api/profiles/picture"
	profilePicturePattern       = "/api/profiles/picture/{id}"
	maxProfilePictureUploadBody = 16 << 20
)

type profilePictureStore interface {
	SetProfilePicture(context.Context, store.ProfileID, []byte, time.Time) (int64, error)
	ClearProfilePicture(context.Context, store.ProfileID) error
	ProfilePicture(context.Context, store.ProfileID) ([]byte, int64, error)
	AppendActivity(context.Context, store.ActivityEntry) error
}

type profilePictureResponse struct {
	PictureURL string `json:"pictureURL"`
}

// profilePictureURL matches the URL the GraphQL ProfileSummary.pictureURL
// field reports, so a client can swap it in without refetching Profiles.
func profilePictureURL(id store.ProfileID, version int64) string {
	if version == 0 {
		return ""
	}
	return "/api/profiles/picture/" + url.PathEscape(string(id)) + "?v=" + strconv.FormatInt(version, 10)
}

// profilePictureWriteScope authorizes a picture change: a session (never an
// API token or key) sending the CSRF header, acting on its own Profile or
// holding an Admin Session.
func (s *Server) profilePictureWriteScope(w http.ResponseWriter, r *http.Request, target store.ProfileID) (auth.Scope, bool) {
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return auth.Scope{}, false
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil || scope.SessionID == "" || scope.APITokenID != "" || scope.APIKeyID != "" {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return auth.Scope{}, false
	}
	if !scope.Admin && scope.ProfileID != target {
		writeError(w, r, http.StatusForbidden, "admin session required")
		return auth.Scope{}, false
	}
	if s.profilePictures == nil {
		writeError(w, r, http.StatusServiceUnavailable, "profile pictures unavailable")
		return auth.Scope{}, false
	}
	return scope, true
}

func (s *Server) uploadProfilePicture(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxProfilePictureUploadBody)
	if err := r.ParseMultipartForm(maxArtworkMultipartMemory); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	target := store.ProfileID(strings.TrimSpace(r.FormValue("profileID")))
	if target == "" {
		writeError(w, r, http.StatusBadRequest, "profileID is required")
		return
	}
	scope, ok := s.profilePictureWriteScope(w, r, target)
	if !ok {
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	picture, err := normalizeProfilePicture(file)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	version, err := s.profilePictures.SetProfilePicture(r.Context(), target, picture, time.Now())
	if err != nil {
		writeProfilePictureStoreError(w, r, err)
		return
	}
	s.logProfilePictureActivity(r.Context(), scope, target)
	writeJSON(w, http.StatusOK, profilePictureResponse{PictureURL: profilePictureURL(target, version)})
}

func (s *Server) deleteProfilePicture(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	target := store.ProfileID(strings.TrimSpace(r.PathValue("id")))
	scope, ok := s.profilePictureWriteScope(w, r, target)
	if !ok {
		return
	}
	if err := s.profilePictures.ClearProfilePicture(r.Context(), target); err != nil {
		writeProfilePictureStoreError(w, r, err)
		return
	}
	s.logProfilePictureActivity(r.Context(), scope, target)
	writeJSON(w, http.StatusOK, profilePictureResponse{})
}

// profilePicture serves a stored picture to any authenticated scope: the
// Profile picker shows every Profile's picture before one is chosen. The
// bytes are the server's own normalized JPEG, never the upload.
func (s *Server) profilePicture(w http.ResponseWriter, r *http.Request) {
	if _, err := s.auth.ScopeFromRequest(r.Context(), r); err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	if s.profilePictures == nil {
		writeError(w, r, http.StatusServiceUnavailable, "profile pictures unavailable")
		return
	}
	target := store.ProfileID(strings.TrimSpace(r.PathValue("id")))
	picture, version, err := s.profilePictures.ProfilePicture(r.Context(), target)
	if errors.Is(err, store.ErrNoProfilePicture) {
		writeError(w, r, http.StatusNotFound, "profile picture not found")
		return
	}
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "profile picture unavailable", err)
		return
	}
	etag := `"` + strconv.FormatInt(version, 10) + `"`
	w.Header().Set("ETag", etag)
	// A versioned URL never changes content; an unversioned one revalidates.
	if r.URL.Query().Get("v") == strconv.FormatInt(version, 10) {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(picture)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(picture)
	}
}

func writeProfilePictureStoreError(w http.ResponseWriter, r *http.Request, err error) {
	var verr store.ValidationError
	if errors.As(err, &verr) {
		writeError(w, r, http.StatusBadRequest, string(verr))
		return
	}
	writeErrorCause(w, r, http.StatusInternalServerError, "profile picture update failed", err)
}

// logProfilePictureActivity records the change after it is durable. The
// picture is already stored, so a failed audit append is logged, not
// surfaced as a failed upload.
func (s *Server) logProfilePictureActivity(ctx context.Context, scope auth.Scope, target store.ProfileID) {
	err := s.profilePictures.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: time.Now().UTC(),
		Action:     store.ActivityProfilePicture,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: "profile",
		TargetID:   string(target),
	})
	if err != nil {
		slog.WarnContext(ctx, "profile picture activity append failed", "error", err)
	}
}
