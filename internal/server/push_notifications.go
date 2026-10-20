package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/beamfall/core/internal/store"
)

type PushNotificationStore interface {
	NotificationByID(context.Context, string, store.ProfileID, store.LibraryFilter) (store.Notification, bool, error)
}

func (s *Server) SetPushNotifications(store PushNotificationStore) {
	s.pushNotifications = store
}

type pushNotificationResponse struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Ref       string `json:"ref"`
	CreatedAt string `json:"createdAt"`
	Read      bool   `json:"read"`
}

func (s *Server) pushNotification(w http.ResponseWriter, r *http.Request) {
	if s.pushNotifications == nil {
		writeError(w, r, http.StatusServiceUnavailable, "push notifications unavailable")
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil || scope.SessionID == "" || scope.ProfileID == "" || scope.APITokenID != "" || scope.APIKeyID != "" {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, r, http.StatusNotFound, "notification not found")
		return
	}
	n, ok, err := s.pushNotifications.NotificationByID(r.Context(), id, scope.ProfileID, scope.LibraryFilter())
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "notification unavailable", err)
		return
	}
	if !ok {
		writeError(w, r, http.StatusNotFound, "notification not found")
		return
	}
	writeJSON(w, http.StatusOK, pushNotificationResponse{
		ID:        n.ID,
		Kind:      string(n.Kind),
		Title:     n.Title,
		Body:      n.Body,
		Ref:       n.Ref,
		CreatedAt: n.CreatedAt.Format(time.RFC3339),
		Read:      !n.ReadAt.IsZero(),
	})
}
