package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// In-app notification inbox resolvers (ADR-0039 §2.5, master issue #32). This is
// the polled, client-independent first slice of the Core Platform push
// primitive: a `notifications(unreadOnly)` query and a `markNotificationRead(id)`
// mutation. APNs/FCM dispatch is a deferred layer behind a feature flag (see
// internal/push); the inbox works fully without it.
//
// Reveal-content surface: `notifications` joins `search`, the activity log, and
// semantic search as a per-Profile content surface, so it is covered by the
// same two reveal-conformance machineries those surfaces use: (1) the field
// registry (queryFields/mutationFields + the frozen scopedFields baseline in
// registry_test.go), which forces every new top-level field through the auth
// gate; and (2) a dedicated reveal-filtering conformance test
// (TestNotificationRevealContract in notifications_test.go, mirroring the store
// TestSemanticRevealContract). The store read (store.ListNotifications) is BOTH
// recipient-gated (a Profile sees only its own notifications) AND reveal-filtered
// (a notification referencing restricted content is hidden from an unrevealed
// session) — the resolver passes scope.ProfileID as the recipient and
// scope.LibraryFilter() as the reveal filter, so the gate lives in one place.

// NotificationInfo is the wire shape of one inbox notification. Kind is the
// closed-vocabulary classifier the client maps to a localized label (never a
// domain term). Read is the unread/read flag the badge and list render from.
// Ref is the opaque originating-object id the client round-trips back to the
// server when the user acts on the notification (e.g. opening an extension
// request); Core never interprets it. Times are RFC3339.
type NotificationInfo struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Ref       string `json:"ref"`
	CreatedAt string `json:"createdAt"`
	Read      bool   `json:"read"`
}

type PushNotificationTarget struct {
	ProfileID      store.ProfileID
	NotificationID string
}

// notifications is the inbox read resolver (#32). It is gated to the calling
// session's own Profile — there is no cross-Profile inbox read — and
// reveal-filtered through scope.LibraryFilter(). unreadOnly restricts the page to
// unread notifications. Returns an empty list (never an error) when no store is
// wired so a client renders an empty inbox cleanly.
func (h *Handler) notifications(ctx context.Context, scope auth.Scope, req request) ([]NotificationInfo, error) {
	if h.store == nil {
		return []NotificationInfo{}, nil
	}
	unreadOnly := boolVar(req, "unreadOnly")
	limit := boundedIntVar(req, "limit", 50, 200)
	items, err := h.store.ListNotifications(ctx, scope.ProfileID, scope.LibraryFilter(), unreadOnly, limit)
	if err != nil {
		return nil, err
	}
	out := make([]NotificationInfo, len(items))
	for i, n := range items {
		out[i] = NotificationInfo{
			ID:        n.ID,
			Kind:      string(n.Kind),
			Title:     n.Title,
			Body:      n.Body,
			Ref:       n.Ref,
			CreatedAt: n.CreatedAt.Format(time.RFC3339),
			Read:      !n.ReadAt.IsZero(),
		}
	}
	return out, nil
}

// NotificationReadResult is the markNotificationRead echo: the id acted on and
// whether a matching notification was found-and-read. Found is false when the id
// is unknown, addressed to another Profile, or references content the caller
// cannot see — all reported identically so the mutation is never an existence
// oracle for hidden or other-Profile notifications.
type NotificationReadResult struct {
	ID    string `json:"id"`
	Found bool   `json:"found"`
}

// markNotificationRead marks one of the caller's own notifications read (#32).
// The store enforces the recipient gate and reveal filter before any write, so a
// caller can never mark another Profile's notification — or a restricted
// notification it hasn't revealed — read. Idempotent: re-marking an already-read
// notification still reports found=true.
func (h *Handler) markNotificationRead(ctx context.Context, scope auth.Scope, req request) (NotificationReadResult, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return NotificationReadResult{}, clientError("id is required")
	}
	found, err := h.store.MarkNotificationRead(ctx, id, scope.ProfileID, scope.LibraryFilter())
	if err != nil {
		return NotificationReadResult{}, err
	}
	return NotificationReadResult{ID: id, Found: found}, nil
}

// PushRegistrationResult is the registerDevicePushToken echo: whether the token
// was accepted-and-persisted. Enabled is false when push-token registration is
// gated off (the client then falls back to the polled inbox). It carries no
// token back — a registration echo must never reflect the opaque token.
type PushRegistrationResult struct {
	Registered bool `json:"registered"`
	Enabled    bool `json:"enabled"`
}

// registerDevicePushToken persists the calling session's device push token
// (ADR-0039 §2.5, PUSH-1 — "viewer apps register a push token with the server at
// enrollment time"). The device and Profile are taken from the authenticated
// session scope, NOT from the request, so a session can only ever register a
// token for its own enrolled device — there is no cross-device registration.
// The platform is the closed apns|fcm|webpush vocabulary; the token is the
// opaque APNs/FCM/Web-Push handle. When the registrar is unwired or gated off,
// the mutation reports enabled=false rather than erroring, so a client treats
// "push unavailable" as a clean fallback-to-inbox signal.
func (h *Handler) registerDevicePushToken(ctx context.Context, scope auth.Scope, req request) (PushRegistrationResult, error) {
	if h.pushRegistrar == nil || !h.pushRegistrar.Enabled() {
		return PushRegistrationResult{Registered: false, Enabled: false}, nil
	}
	platform := trimmedVariable(req, "platform")
	token := trimmedVariable(req, "token")
	if platform == "" || token == "" {
		return PushRegistrationResult{}, clientError("platform and token are required")
	}
	if scope.DeviceID == "" {
		return PushRegistrationResult{}, clientError("an enrolled device session is required")
	}
	if err := h.pushRegistrar.Register(ctx, string(scope.DeviceID), string(scope.ProfileID), platform, token); err != nil {
		// An unknown platform is a client error (closed vocabulary); anything else
		// is surfaced as-is. push.ErrPushDisabled is mapped to enabled=false above
		// before any Register call, so it does not reach here.
		return PushRegistrationResult{}, clientError(err.Error())
	}
	return PushRegistrationResult{Registered: true, Enabled: true}, nil
}
