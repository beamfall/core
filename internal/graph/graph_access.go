package graph

import (
	"context"
	"errors"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/push"
	"github.com/beamfall/core/internal/store"
)

// Per-title access request, graph half. The "Ask a parent"
// flow: a managed Profile viewing a Locked item taps "Ask a parent" →
// requestAccess creates a pending AccessRequest (dedup: one pending per pair),
// notifies every adult Profile's devices and inbox → the first adult to
// respond wins (resolveAccessRequest) → the managed Profile sees "Requested —
// waiting for a parent" via the pendingAccessRequests inbox query.
//
// SUPERVISOR MODEL (same as ADR-0039 §2.3): no stored supervisor link. Any
// adult Profile is an authorized responder; a managed Profile may not
// self-approve. The push payload is {type, eventId} only — invariant #5.

// AccessRequestPusher fans an access-request notification out to every adult
// Profile's enrolled devices (ADR-0039 push reuse + ADR-0113 §2.2). Nil (the
// default) or a dormant dispatcher makes Push a no-op — the inbox and the
// managed Profile's status query still carry the flow.
type AccessRequestPusher interface {
	// Push dispatches the access-request notification (payload type
	// "access_request", event id = notification ID) to every adult Profile's enrolled
	// devices. Best-effort: a transport failure to one device must not fail the
	// request.
	Push(ctx context.Context, notifications []PushNotificationTarget) error
}

// AccessRequestInfo is the wire shape of one access request. Status is
// pending|resolved; Verdict is the sub-state once resolved
// (granted|denied|suppressed). MediaID and ProfileID are the opaque
// identifiers only — no title or name rides this shape (invariant #5).
type AccessRequestInfo struct {
	ID           string `json:"id"`
	ProfileID    string `json:"profileID"`
	MediaID      string `json:"mediaID"`
	Status       string `json:"status"`
	Verdict      string `json:"verdict"`
	CreatedAt    string `json:"createdAt"`
	TTLExpiresAt string `json:"ttlExpiresAt"`
	Responder    string `json:"responder"`
}

func accessRequestInfo(r store.AccessRequest) AccessRequestInfo {
	return AccessRequestInfo{
		ID:           r.ID,
		ProfileID:    string(r.ProfileID),
		MediaID:      string(r.MediaID),
		Status:       string(r.Status),
		Verdict:      string(r.Verdict),
		CreatedAt:    r.CreatedAt.Format(time.RFC3339),
		TTLExpiresAt: r.TTLExpiresAt.Format(time.RFC3339),
		Responder:    string(r.Responder),
	}
}

// requestAccess raises a pending access request for a Locked item (ADR-0113
// §2.2). Only a managed Profile may request — the managed-kind check IS the
// authorization (an adult has the Reveal path, not an access-request gate).
// Dedup: a second tap while a request is pending returns the existing row (the
// store's partial-index idempotent INSERT). On create, every adult Profile's
// inbox is notified and the push fan-out fires (best-effort).
func (h *Handler) requestAccess(ctx context.Context, scope auth.Scope, req request) (AccessRequestInfo, error) {
	if h.store == nil {
		return AccessRequestInfo{}, clientError("access requests unavailable")
	}
	if scope.ProfileID == "" {
		return AccessRequestInfo{}, clientError("a session in a profile is required to request access")
	}
	if scope.Profile != store.ProfileManaged {
		return AccessRequestInfo{}, clientError("only a managed profile can request access to a locked item")
	}
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return AccessRequestInfo{}, clientError("mediaID is required")
	}
	now := time.Now().UTC()
	created, err := h.store.CreateAccessRequest(ctx, store.AccessRequestInput{
		ProfileID: scope.ProfileID,
		MediaID:   store.MediaID(mediaID),
		Now:       now,
	})
	if err != nil {
		return AccessRequestInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityAccessRequest, "access_request", created.ID, ""); err != nil {
		return AccessRequestInfo{}, err
	}
	// Notify adults and fire push. Both are best-effort — the request exists
	// regardless of notification/push delivery.
	if err := h.notifyAdultsOfAccessRequest(ctx, created); err != nil {
		return AccessRequestInfo{}, err
	}
	return accessRequestInfo(created), nil
}

// resolveAccessRequest resolves a pending access request.
// Only an adult Profile may resolve — the household model means any adult is a
// valid responder; a managed Profile (including the requester) is refused.
// First-responder-wins: a second resolver gets "already resolved". When
// grant=true, an AccessGrant is upserted atomically.
func (h *Handler) resolveAccessRequest(ctx context.Context, scope auth.Scope, req request) (AccessRequestInfo, error) {
	if h.store == nil {
		return AccessRequestInfo{}, clientError("access requests unavailable")
	}
	if scope.Profile != store.ProfileAdult {
		return AccessRequestInfo{}, clientError("only an adult profile can resolve an access request")
	}
	id := trimmedVariable(req, "id")
	if id == "" {
		return AccessRequestInfo{}, clientError("id is required")
	}
	// Accept a string "verdict" (granted|denied|suppressed) with a backwards-
	// compatible fallback to the legacy bool "grant" field.
	verdictStr := trimmedVariable(req, "verdict")
	var verdict store.AccessVerdict
	switch verdictStr {
	case "granted":
		verdict = store.AccessVerdictGranted
	case "denied":
		verdict = store.AccessVerdictDenied
	case "suppressed":
		verdict = store.AccessVerdictSuppressed
	default:
		// Legacy path: "grant": true → granted, absent/false → denied.
		if boolVar(req, "grant") {
			verdict = store.AccessVerdictGranted
		} else {
			verdict = store.AccessVerdictDenied
		}
	}
	now := time.Now().UTC()
	resolved, err := h.store.ResolveAccessRequest(ctx, id, scope.ProfileID, verdict, now)
	if err != nil {
		return AccessRequestInfo{}, mapAccessError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityAccessResolve, "access_request", id, ""); err != nil {
		return AccessRequestInfo{}, err
	}
	return accessRequestInfo(resolved), nil
}

// AccessGrantInfo is the wire shape of one access grant returned by
// accessGrantsForProfile and revokeAccessGrant.
// ProfileID and MediaID are the opaque identifiers only — no title or name
// rides this shape (invariant #5).
type AccessGrantInfo struct {
	ProfileID      string `json:"profileID"`
	MediaID        string `json:"mediaID"`
	RequestID      string `json:"requestID"`
	ActorProfileID string `json:"actorProfileID"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

func accessGrantInfo(g store.AccessGrant) AccessGrantInfo {
	return AccessGrantInfo{
		ProfileID:      string(g.ProfileID),
		MediaID:        string(g.MediaID),
		RequestID:      g.RequestID,
		ActorProfileID: string(g.ActorProfileID),
		CreatedAt:      g.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      g.UpdatedAt.Format(time.RFC3339),
	}
}

// accessGrantsForProfile returns the active access grants for a managed
// Profile. An adult may query any managed Profile's
// grants (Console UI exception list); a managed Profile may query only its
// own. Returns an empty list when no grants exist.
func (h *Handler) accessGrantsForProfile(ctx context.Context, scope auth.Scope, req request) ([]AccessGrantInfo, error) {
	if h.store == nil {
		return nil, clientError("access grants unavailable")
	}
	profileID := store.ProfileID(trimmedVariable(req, "profileID"))
	if profileID == "" {
		return nil, clientError("profileID is required")
	}
	if scope.Profile != store.ProfileAdult && scope.ProfileID != profileID {
		return nil, clientError("access grants not found")
	}
	grants, err := h.store.AccessGrantsForProfile(ctx, profileID)
	if err != nil {
		return nil, err
	}
	out := make([]AccessGrantInfo, len(grants))
	for i, g := range grants {
		out[i] = accessGrantInfo(g)
	}
	return out, nil
}

// revokeAccessGrant removes the active AccessGrant for a (profileID, mediaID)
// pair. Only an adult may revoke. Revoking a
// non-existent grant is a no-op — the response is an empty AccessGrantInfo.
func (h *Handler) revokeAccessGrant(ctx context.Context, scope auth.Scope, req request) (AccessGrantInfo, error) {
	if h.store == nil {
		return AccessGrantInfo{}, clientError("access grants unavailable")
	}
	if scope.Profile != store.ProfileAdult {
		return AccessGrantInfo{}, clientError("only an adult profile can revoke an access grant")
	}
	profileID := store.ProfileID(trimmedVariable(req, "profileID"))
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	if profileID == "" {
		return AccessGrantInfo{}, clientError("profileID is required")
	}
	if mediaID == "" {
		return AccessGrantInfo{}, clientError("mediaID is required")
	}
	if err := h.store.RevokeAccessGrant(ctx, profileID, mediaID); err != nil {
		return AccessGrantInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityAccessResolve, "access_grant_revoke",
		string(profileID)+"/"+string(mediaID), ""); err != nil {
		return AccessGrantInfo{}, err
	}
	return AccessGrantInfo{ProfileID: string(profileID), MediaID: string(mediaID)}, nil
}

// accessRequest reads one request by id. An adult (any adult = supervisor)
// may read any request; the managed requester may read its own. Another
// managed Profile is told "not found" so the surface is not a cross-Profile
// existence oracle.
func (h *Handler) accessRequest(ctx context.Context, scope auth.Scope, req request) (AccessRequestInfo, error) {
	if h.store == nil {
		return AccessRequestInfo{}, clientError("access requests unavailable")
	}
	id := trimmedVariable(req, "id")
	if id == "" {
		return AccessRequestInfo{}, clientError("id is required")
	}
	r, err := h.store.AccessRequestByID(ctx, id, time.Now().UTC())
	if err != nil {
		return AccessRequestInfo{}, mapAccessError(err)
	}
	if scope.Profile != store.ProfileAdult && scope.ProfileID != r.ProfileID {
		return AccessRequestInfo{}, clientError("access request not found")
	}
	return accessRequestInfo(r), nil
}

// pendingAccessRequests returns the pending access requests visible to the
// calling Profile. Adults see all pending requests (the approval inbox);
// managed Profiles see their own pending requests only (the "waiting" status
// surface). TTL-expired pending rows are reported as resolved/suppressed.
func (h *Handler) pendingAccessRequests(ctx context.Context, scope auth.Scope, req request) ([]AccessRequestInfo, error) {
	if h.store == nil {
		return nil, clientError("access requests unavailable")
	}
	if scope.ProfileID == "" {
		return nil, clientError("a profile is required")
	}
	isAdult := scope.Profile == store.ProfileAdult
	list, err := h.store.PendingAccessRequestsForProfile(ctx, scope.ProfileID, isAdult, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	out := make([]AccessRequestInfo, len(list))
	for i, r := range list {
		out[i] = accessRequestInfo(r)
	}
	return out, nil
}

// notifyAdultsOfAccessRequest drops an inbox notification for every adult
// Profile and fires the push fan-out (best-effort). The inbox is the durable
// record; the push is the real-time accelerator.
func (h *Handler) notifyAdultsOfAccessRequest(ctx context.Context, r store.AccessRequest) error {
	profiles, err := h.store.Profiles(ctx)
	if err != nil {
		return err
	}
	var pushTargets []PushNotificationTarget
	for _, p := range profiles {
		if p.Kind != store.ProfileAdult {
			continue
		}
		notificationID := "acreq-" + string(p.ID) + "-" + r.ID
		if err := h.store.CreateNotification(ctx, store.Notification{
			ID:                 notificationID,
			RecipientProfileID: p.ID,
			Kind:               store.NotificationAccessRequest,
			Title:              "Access requested",
			Ref:                r.ID,
		}); err != nil {
			return err
		}
		pushTargets = append(pushTargets, PushNotificationTarget{ProfileID: p.ID, NotificationID: notificationID})
	}
	if h.accessRequestPusher != nil {
		_ = h.accessRequestPusher.Push(ctx, pushTargets)
	}
	return nil
}

// mapAccessError maps store-level access-request errors to client-facing
// messages.
func mapAccessError(err error) error {
	switch {
	case errors.Is(err, store.ErrAccessRequestNotFound):
		return clientError("access request not found")
	case errors.Is(err, store.ErrAccessRequestAlreadyResolved):
		return clientError("access request already resolved")
	default:
		return err
	}
}

// accessRequestStorePusher is the composition-root adapter that satisfies
// AccessRequestPusher: it reads each notified Profile's device push
// tokens, and fans minimal {type:"access_request", eventId:notificationID}
// payload through the push.Dispatcher.
type accessRequestStorePusher struct {
	store      AccessRequestPushStore
	dispatcher *push.Dispatcher
}

// AccessRequestPushStore is the store slice the push adapter needs.
// *store.Store satisfies it.
type AccessRequestPushStore interface {
	DevicePushTokensForProfile(ctx context.Context, profileID store.ProfileID) ([]store.DevicePushToken, error)
}

// NewAccessRequestStorePusher builds the adult-device push fan-out adapter.
// dispatcher may be nil or dormant — Push is then a no-op.
func NewAccessRequestStorePusher(s AccessRequestPushStore, dispatcher *push.Dispatcher) AccessRequestPusher {
	return accessRequestStorePusher{store: s, dispatcher: dispatcher}
}

// Push dispatches per-recipient {type:"access_request", eventId:notificationID}
// payloads to adult Profile enrolled devices. Best-effort: a dead token or dormant
// transport is collected and ignored.
func (p accessRequestStorePusher) Push(ctx context.Context, notifications []PushNotificationTarget) error {
	if p.dispatcher == nil || !p.dispatcher.Configured() {
		return nil
	}
	var errs []error
	for _, notification := range notifications {
		tokens, err := p.store.DevicePushTokensForProfile(ctx, notification.ProfileID)
		if err != nil {
			return err
		}
		payload := push.Payload{Type: "access_request", EventID: notification.NotificationID}
		var targets []push.Target
		for _, t := range tokens {
			targets = append(targets, push.Target{Platform: push.PushPlatform(t.Platform), Token: t.Token})
		}
		if len(targets) > 0 {
			if err := p.dispatcher.Fanout(ctx, targets, payload); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
