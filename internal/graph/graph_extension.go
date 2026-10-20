package graph

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/push"
	"github.com/beamfall/core/internal/store"
)

// Parental extension request (ADR-0039 §2.1–2.4 / PUSH-2), graph half. The
// "more time" flow: a managed Profile blocked mid-playback raises a pending
// request (requestExtension), it is pushed to every adult Profile's devices and
// dropped into their notification inbox, an adult approves/denies it
// (respondExtension, first-responder-wins), and the outcome is delivered to the
// managed Profile's session over the /api/events SSE. A 5-minute window
// auto-expires an unanswered request; on approve the managed Profile's block is
// lifted for the granted window (auth.Resolve consults the store grant).
//
// SUPERVISOR MODEL (ADR-0039 §2.3 + rejected-alternatives): there is NO stored
// supervisor→managed link — the profile table is id/name/kind only. The ADR
// resolves this explicitly: the request fans out to "every adult Profile on the
// server" and "the first available parent answers, not a designated primary".
// So authorization is by Profile KIND: any adult Profile may respond; a managed
// Profile may not (it cannot self-approve — rejected-alternatives "there is no
// self-approval path"). The push fan-out target set is every adult Profile.
//
// Invariant #5: the push payload carries only {type, eventId} (push.Payload has
// no content field by construction); the SSE outcome event carries only the
// request id, the managed Profile, and the resolved status — no media title,
// position, or Profile name. The device fetches details over the authenticated
// API (the extensionRequest query) on receipt.

// ExtensionPusher fans an extension-request notification out to every adult
// Profile's enrolled devices. Defined at the consumer (the graph
// layer, ADR-0016 §seams); the composition root wires an adapter that lists
// adult Profiles, reads their device push tokens, and dispatches the minimal
// {type, eventId} payload through the configured push.Dispatcher. Nil (the
// default) or a dormant dispatcher makes Push a no-op — the request, the inbox
// notification, and the SSE outcome still happen; only the real-time push
// accelerator is absent (clients fall back to the polled inbox, ADR-0039 §2.5).
type ExtensionPusher interface {
	// Push dispatches the extension-request notification (payload type
	// "extension_request", event id = notification ID) to every adult Profile's
	// devices. It is best-effort: a transport failure to one device must not
	// fail the request.
	Push(ctx context.Context, notifications []PushNotificationTarget) error
}

// SetExtensionPusher wires the adult-device push fan-out for extension requests.
// Nil (the default) leaves push a no-op — the request,
// inbox notification, and SSE outcome are unaffected; only real-time push to
// adult devices is skipped (clients fall back to the polled inbox).
func (h *Handler) SetExtensionPusher(p ExtensionPusher) { h.extensionPusher = p }

// extensionGrantMinutes resolves the ADR-0039 §2.2 approval option vocabulary
// into a concrete grant length in minutes. The closed, domain-neutral set is
// "finish-item" (the request's captured remaining runtime — the domain-neutral
// wire token for the ADR's "finish this item" client label, since the core wire
// entity is Media, invariant #1/#2), "+15", "+30", "+60". An unknown option is a
// client error so a typo never silently grants zero. finishMinutes is the
// request's remaining-runtime captured at request time.
func extensionGrantMinutes(option string, finishMinutes int) (int, error) {
	switch option {
	case "", "finish-item":
		return finishMinutes, nil
	case "+15":
		return 15, nil
	case "+30":
		return 30, nil
	case "+60":
		return 60, nil
	default:
		return 0, clientError("grant must be finish-item, +15, +30, or +60")
	}
}

// extensionTriggerSources is the closed §2.1 trigger vocabulary: the three block
// mechanisms ("all three, single request shape"). The label is reported in the
// notification ("schedule ended" vs "daily limit"); Core never branches on it
// (invariant #1) but validates it so a client cannot inject arbitrary text.
var extensionTriggerSources = map[string]bool{
	"schedule": true, // access-schedule expiry (ADR-0007)
	"cap":      true, // stream cap A1/A2 (ADR-0004 §per-profile-ceilings)
	"budget":   true, // viewing-budget exhaustion (ideas backlog)
}

// ExtensionRequestInfo is the wire shape of one extension request through its
// lifecycle. Status is the closed pending|approved|denied|expired vocabulary;
// GrantMinutes is the resolved length once approved; Responder is the adult who
// resolved it. Times are RFC3339. It carries no media content — only the opaque
// trigger source — so it is invariant-#5 clean.
type ExtensionRequestInfo struct {
	ID            string `json:"id"`
	ProfileID     string `json:"profileID"`
	TriggerSource string `json:"triggerSource"`
	Status        string `json:"status"`
	GrantMinutes  int    `json:"grantMinutes"`
	CreatedAt     string `json:"createdAt"`
	ExpiresAt     string `json:"expiresAt"`
	Responder     string `json:"responder"`
}

func extensionRequestInfo(r store.ExtensionRequest) ExtensionRequestInfo {
	return ExtensionRequestInfo{
		ID:            r.ID,
		ProfileID:     string(r.ProfileID),
		TriggerSource: r.TriggerSource,
		Status:        string(r.Status),
		GrantMinutes:  r.GrantMinutes,
		CreatedAt:     r.CreatedAt.Format(time.RFC3339),
		ExpiresAt:     r.ExpiresAt.Format(time.RFC3339),
		Responder:     string(r.Responder),
	}
}

func extensionApprovalToken(r store.ExtensionRequest) string {
	return r.ApprovalToken()
}

func parseExtensionApprovalToken(token string) (string, string) {
	id, nonce, ok := strings.Cut(token, ":")
	if !ok {
		return token, ""
	}
	return id, nonce
}

// requestExtension raises a pending extension request for the calling managed
// Profile's session. The Profile and Session come from the
// authenticated scope, never the request, so a session can only ever request an
// extension for itself. Only a managed Profile may request — an adult Profile has
// the Reveal path, not a time gate, so it is refused (there is nothing to
// extend). The request is pushed to every adult Profile's devices and dropped
// into their inbox; if the managed Profile is pre-authorized AND
// this is the first request in the session, it is auto-approved for the
// finish-item default and the outcome is emitted at once.
func (h *Handler) requestExtension(ctx context.Context, scope auth.Scope, req request) (ExtensionRequestInfo, error) {
	if h.store == nil {
		return ExtensionRequestInfo{}, clientError("extension requests unavailable")
	}
	if scope.ProfileID == "" || scope.SessionID == "" {
		return ExtensionRequestInfo{}, clientError("a session in a profile is required to request an extension")
	}
	if scope.Profile != store.ProfileManaged {
		// Only a managed Profile has a block to extend; an adult requesting one is a
		// client error, not a silent no-op (rejected-alternatives: no self-approval,
		// and an adult has no schedule gate at all).
		return ExtensionRequestInfo{}, clientError("only a managed profile can request an extension")
	}
	triggerSource := trimmedVariable(req, "triggerSource")
	if triggerSource != "" && !extensionTriggerSources[triggerSource] {
		return ExtensionRequestInfo{}, clientError("triggerSource must be schedule, cap, or budget")
	}
	remaining := intVar(req, "remainingMinutes")
	now := time.Now().UTC()
	created, err := h.store.CreateExtensionRequest(ctx, store.ExtensionRequestInput{
		ProfileID:           scope.ProfileID,
		RequestingSessionID: scope.SessionID,
		TriggerSource:       triggerSource,
		RemainingMinutes:    remaining,
		Now:                 now,
	})
	if err != nil {
		return ExtensionRequestInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityExtensionRequest, "extension", created.ID, ""); err != nil {
		return ExtensionRequestInfo{}, err
	}

	// Pre-authorization escape hatch: the FIRST request in a
	// session is auto-approved for the finish-item default when the managed
	// Profile is pre-authorized; subsequent requests in the same session still
	// require an adult. The once-per-session guard checks for a prior approval.
	preauthed, err := h.store.ExtensionPreauthorizationEnabled(ctx, scope.ProfileID)
	if err != nil {
		return ExtensionRequestInfo{}, err
	}
	if preauthed {
		used, err := h.store.SessionHasApprovedExtension(ctx, scope.ProfileID, scope.SessionID)
		if err != nil {
			return ExtensionRequestInfo{}, err
		}
		if !used {
			resolved, err := h.store.RespondExtensionRequest(ctx, created.ID, "", scope.ProfileID, store.ExtensionApproved, remaining, now)
			if err != nil {
				return ExtensionRequestInfo{}, err
			}
			if err := h.emitExtensionOutcome(ctx, resolved); err != nil {
				return ExtensionRequestInfo{}, err
			}
			return extensionRequestInfo(resolved), nil
		}
	}

	// Not auto-approved: notify the adults. The inbox is the durable record
	// (works without push credentials); the push is the real-time accelerator.
	if err := h.notifyAdultsOfExtension(ctx, created); err != nil {
		return ExtensionRequestInfo{}, err
	}
	return extensionRequestInfo(created), nil
}

// respondExtension approves or denies a pending extension request (ADR-0039
// §2.3). Only an ADULT Profile may respond — the household model (any adult, no
// designated guardian) means the Profile-kind check IS the authorization, and a
// managed Profile (including the requester) is refused, so there is no
// self-approval path. First-responder-wins: a second responder, or one racing
// the 5-minute expiry, gets "already resolved" rather than overwriting the first
// decision. On approve the outcome (and its grant window) is emitted to the
// managed Profile's session over the SSE.
func (h *Handler) respondExtension(ctx context.Context, scope auth.Scope, req request) (ExtensionRequestInfo, error) {
	if h.store == nil {
		return ExtensionRequestInfo{}, clientError("extension requests unavailable")
	}
	if scope.Profile != store.ProfileAdult {
		// Any adult may respond; a managed Profile may not (no self-approval). A
		// non-Profile context (admin token / unbound scoped key) has no Profile to
		// respond as and is refused identically.
		return ExtensionRequestInfo{}, clientError("only an adult profile can respond to an extension request")
	}
	id := trimmedVariable(req, "id")
	if id == "" {
		return ExtensionRequestInfo{}, clientError("id is required")
	}
	id, approvalNonce := parseExtensionApprovalToken(id)
	if scope.DeviceID != "" {
		device, err := h.store.Device(ctx, scope.DeviceID)
		if err != nil {
			return ExtensionRequestInfo{}, err
		}
		if device.Revoked {
			return ExtensionRequestInfo{}, clientError("device revoked")
		}
	}
	approve := boolVar(req, "approve")
	status := store.ExtensionDenied
	grantMinutes := 0
	if approve {
		status = store.ExtensionApproved
		// Resolve the §2.2 approval option against the request's captured
		// remaining runtime (finish-item default).
		existing, err := h.store.ExtensionRequestByID(ctx, id, time.Now().UTC())
		if err != nil {
			return ExtensionRequestInfo{}, mapExtensionError(err)
		}
		minutes, err := extensionGrantMinutes(trimmedVariable(req, "grant"), existing.RemainingMinutes)
		if err != nil {
			return ExtensionRequestInfo{}, err
		}
		grantMinutes = minutes
	}
	now := time.Now().UTC()
	resolved, err := h.store.RespondExtensionRequest(ctx, id, approvalNonce, scope.ProfileID, status, grantMinutes, now)
	if err != nil {
		return ExtensionRequestInfo{}, mapExtensionError(err)
	}
	if err := h.logActivity(ctx, scope, store.ActivityExtensionResponse, "extension", id, ""); err != nil {
		return ExtensionRequestInfo{}, err
	}
	if err := h.emitExtensionOutcome(ctx, resolved); err != nil {
		return ExtensionRequestInfo{}, err
	}
	return extensionRequestInfo(resolved), nil
}

// extensionRequest reads one request by id (ADR-0039 §2.3 step 6: the managed
// client fetches the outcome over the authenticated API after the SSE wakes it).
// It is gated to the calling Profile: only the managed requester or an adult
// (any adult is a supervisor) may read it; another managed Profile cannot. A
// pending-but-expired request reads as expired (the store applies lazy expiry).
func (h *Handler) extensionRequest(ctx context.Context, scope auth.Scope, req request) (ExtensionRequestInfo, error) {
	if h.store == nil {
		return ExtensionRequestInfo{}, clientError("extension requests unavailable")
	}
	id := trimmedVariable(req, "id")
	if id == "" {
		return ExtensionRequestInfo{}, clientError("id is required")
	}
	id, _ = parseExtensionApprovalToken(id)
	r, err := h.store.ExtensionRequestByID(ctx, id, time.Now().UTC())
	if err != nil {
		return ExtensionRequestInfo{}, mapExtensionError(err)
	}
	// Read gate: an adult (supervisor) may read any request; the managed
	// requester may read its own. Any other Profile is told "not found" so the
	// surface is not a cross-Profile existence oracle.
	if scope.Profile != store.ProfileAdult && scope.ProfileID != r.ProfileID {
		return ExtensionRequestInfo{}, clientError("extension request not found")
	}
	return extensionRequestInfo(r), nil
}

// SetExtensionPreauthorizationResult echoes the §2.4 toggle state.
type SetExtensionPreauthorizationResult struct {
	ProfileID string `json:"profileID"`
	Enabled   bool   `json:"enabled"`
}

// setExtensionPreauthorization toggles the §2.4 per-managed-Profile
// pre-authorization (admin-gated in execute, like setAccessSchedule — it is a
// Profile policy an adult configures in the Console/Manager). The target Profile
// must be managed; an adult Profile has no schedule to auto-extend.
func (h *Handler) setExtensionPreauthorization(ctx context.Context, scope auth.Scope, req request) (SetExtensionPreauthorizationResult, error) {
	if h.store == nil {
		return SetExtensionPreauthorizationResult{}, clientError("extension requests unavailable")
	}
	profileID := trimmedVariable(req, "profileID")
	if profileID == "" {
		return SetExtensionPreauthorizationResult{}, clientError("profileID is required")
	}
	profile, err := h.store.Profile(ctx, store.ProfileID(profileID))
	if err != nil {
		return SetExtensionPreauthorizationResult{}, err
	}
	if profile.Kind != store.ProfileManaged {
		return SetExtensionPreauthorizationResult{}, clientError("pre-authorization applies to managed profiles only")
	}
	enabled := boolVar(req, "enabled")
	if err := h.store.SetExtensionPreauthorization(ctx, store.ProfileID(profileID), enabled, time.Now().UTC()); err != nil {
		return SetExtensionPreauthorizationResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityExtensionPreauth, "profile", profileID, ""); err != nil {
		return SetExtensionPreauthorizationResult{}, err
	}
	return SetExtensionPreauthorizationResult{ProfileID: profileID, Enabled: enabled}, nil
}

// notifyAdultsOfExtension drops an inbox notification for every adult Profile
// (the durable record) and fires the real-time push fan-out.
// The inbox is client-independent and always runs; the push is best-effort and a
// no-op when no dispatcher is wired. The notification Ref is the request id the
// adult client round-trips to open the approval sheet.
func (h *Handler) notifyAdultsOfExtension(ctx context.Context, r store.ExtensionRequest) error {
	profiles, err := h.store.Profiles(ctx)
	if err != nil {
		return err
	}
	var pushTargets []PushNotificationTarget
	for _, p := range profiles {
		if p.Kind != store.ProfileAdult {
			continue
		}
		notificationID := "extreq-" + string(p.ID) + "-" + r.ID
		// One inbox notification per adult Profile. The id is the request id scoped
		// by recipient so a re-run is naturally idempotent per adult. No LibraryID
		// (the request names no content) — visible to the adult regardless of
		// reveal state.
		if err := h.store.CreateNotification(ctx, store.Notification{
			ID:                 notificationID,
			RecipientProfileID: p.ID,
			Kind:               store.NotificationExtensionRequest,
			Title:              "More time requested",
			Ref:                extensionApprovalToken(r),
		}); err != nil {
			return err
		}
		pushTargets = append(pushTargets, PushNotificationTarget{ProfileID: p.ID, NotificationID: notificationID})
	}
	// Real-time push to adult devices (best-effort; dormant when push is
	// unconfigured). A push failure must not fail the request — the inbox already
	// holds the durable record.
	if h.extensionPusher != nil {
		_ = h.extensionPusher.Push(ctx, pushTargets)
	}
	return nil
}

// emitExtensionOutcome publishes the resolved request's outcome to the managed
// Profile's session over the /api/events SSE (ADR-0039 §2.3 step 6). The event
// is Profile-gated and carries only the request id + status + grant minutes
// (invariant #5) — the managed client fetches details via the extensionRequest
// query on receipt.
func (h *Handler) emitExtensionOutcome(ctx context.Context, r store.ExtensionRequest) error {
	return h.store.AppendExtensionEvent(ctx, store.ExtensionEventPayload{
		RequestID:    r.ID,
		ProfileID:    r.ProfileID,
		Status:       string(r.Status),
		GrantMinutes: r.GrantMinutes,
	}, time.Now().UTC())
}

// mapExtensionError maps the store's typed extension errors to client-facing
// errors: a not-found id and the first-responder-wins/expiry collapse to stable
// messages so the surface never leaks request existence across Profiles and the
// losing responder learns it lost.
func mapExtensionError(err error) error {
	switch {
	case errors.Is(err, store.ErrExtensionNotFound):
		return clientError("extension request not found")
	case errors.Is(err, store.ErrExtensionAlreadyResolved):
		return clientError("extension request already resolved")
	default:
		return err
	}
}

// extensionStorePusher is the composition-root adapter that satisfies
// ExtensionPusher: it lists adult Profiles, reads each one's device push tokens,
// and fans the minimal {type, eventId} payload out through the push.Dispatcher.
// It lives here (rather than the app package) so the {type:"extension_request"}
// contract sits next to the flow it serves; app.go constructs it with the store
// and the configured dispatcher. A dormant dispatcher (no operator credentials)
// makes Push a best-effort no-op.
type extensionStorePusher struct {
	store      ExtensionPushStore
	dispatcher *push.Dispatcher
}

// ExtensionPushStore is the store slice the push adapter needs: per-Profile
// device token reads (ADR-0039 §2.3 fan-out). Interface at the consumer;
// *store.Store satisfies it.
type ExtensionPushStore interface {
	DevicePushTokensForProfile(ctx context.Context, profileID store.ProfileID) ([]store.DevicePushToken, error)
}

// NewExtensionStorePusher builds the adult-device push fan-out adapter. dispatcher
// may be nil or dormant — Push is then a no-op (the inbox + SSE still carry the
// flow).
func NewExtensionStorePusher(s ExtensionPushStore, dispatcher *push.Dispatcher) ExtensionPusher {
	return extensionStorePusher{store: s, dispatcher: dispatcher}
}

// Push dispatches per-recipient {type:"extension_request", eventId:notificationID}
// payloads to adult Profile enrolled devices. Best-effort: a dead
// token or a dormant transport is collected and ignored — one stale device must
// not suppress the rest, and a fully dormant dispatcher (no credentials) is a
// clean no-op. The payload carries no content (invariant #5): the device fetches
// details over the authenticated API.
func (p extensionStorePusher) Push(ctx context.Context, notifications []PushNotificationTarget) error {
	if p.dispatcher == nil || !p.dispatcher.Configured() {
		return nil
	}
	var errs []error
	for _, notification := range notifications {
		tokens, err := p.store.DevicePushTokensForProfile(ctx, notification.ProfileID)
		if err != nil {
			return err
		}
		payload := push.Payload{Type: "extension_request", EventID: notification.NotificationID}
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
