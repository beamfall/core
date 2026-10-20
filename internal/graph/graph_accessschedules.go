package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// AccessScheduleInfo is the wire shape for a managed Profile's server-enforced
// access schedule. Windows is the Profile's allowed
// weekly time windows in stable order; an empty list means no schedule — the
// Profile is always allowed. The Console reads this to render the editor and
// writes it back through setAccessSchedule.
type AccessScheduleInfo struct {
	ProfileID string             `json:"profileID"`
	Windows   []AccessWindowInfo `json:"windows"`
}

// AccessWindowInfo is one allowed weekly time window. Weekday follows Go's
// time.Weekday (0 = Sunday … 6 = Saturday); StartMinute/EndMinute are
// server-local minutes-from-midnight for the half-open interval
// [StartMinute, EndMinute).
type AccessWindowInfo struct {
	Weekday     int `json:"weekday"`
	StartMinute int `json:"startMinute"`
	EndMinute   int `json:"endMinute"`
}

func accessScheduleInfo(profileID string, windows []store.ProfileAccessWindow) AccessScheduleInfo {
	out := AccessScheduleInfo{ProfileID: profileID, Windows: make([]AccessWindowInfo, 0, len(windows))}
	for _, w := range windows {
		out.Windows = append(out.Windows, AccessWindowInfo{
			Weekday:     w.Weekday,
			StartMinute: w.StartMinute,
			EndMinute:   w.EndMinute,
		})
	}
	return out
}

// accessSchedule reads a Profile's allowed time windows (manager-gated in
// execute). The Profile must exist; an adult Profile resolves with an empty
// window list (the store reader does not error on kind — only setting refuses a
// non-managed target).
func (h *Handler) accessSchedule(ctx context.Context, req request) (AccessScheduleInfo, error) {
	profileID := trimmedVariable(req, "profileID")
	if profileID == "" {
		return AccessScheduleInfo{}, clientError("profileID is required")
	}
	if _, err := h.store.Profile(ctx, store.ProfileID(profileID)); err != nil {
		return AccessScheduleInfo{}, err
	}
	windows, err := h.store.ProfileAccessWindows(ctx, store.ProfileID(profileID))
	if err != nil {
		return AccessScheduleInfo{}, err
	}
	return accessScheduleInfo(profileID, windows), nil
}

// setAccessSchedule replaces a managed Profile's allowed time windows
// (manager-gated in execute; full-replace, like setLibraryRoots). An empty or
// absent windows list clears the schedule, restoring the always-allowed
// default. A malformed window object is a client error — silently dropping it
// would quietly widen (or, on a missing entry, narrow) the enforced schedule.
func (h *Handler) setAccessSchedule(ctx context.Context, scope auth.Scope, req request) (AccessScheduleInfo, error) {
	profileID := trimmedVariable(req, "profileID")
	if profileID == "" {
		return AccessScheduleInfo{}, clientError("profileID is required")
	}
	windows, err := accessWindowsFromVariables(req)
	if err != nil {
		return AccessScheduleInfo{}, err
	}
	if err := h.store.SetProfileAccessWindows(ctx, store.ProfileID(profileID), windows); err != nil {
		return AccessScheduleInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityAccessSchedule, "profile", profileID, ""); err != nil {
		return AccessScheduleInfo{}, err
	}
	// Server-initiated cache invalidation: the manager just
	// changed this Profile's access ceiling, so its still-connected sessions purge
	// OS-owned caches that may now surface newly-out-of-policy content.
	if err := h.emitAccessCeilingInvalidation(ctx, store.ProfileID(profileID)); err != nil {
		return AccessScheduleInfo{}, err
	}
	// Echo server-truth so the Console renders the normalized, deduplicated set.
	stored, err := h.store.ProfileAccessWindows(ctx, store.ProfileID(profileID))
	if err != nil {
		return AccessScheduleInfo{}, err
	}
	return accessScheduleInfo(profileID, stored), nil
}

// emitAccessCeilingInvalidation tells a Profile's still-connected sessions that
// their access/Reveal ceiling changed under them — e.g. a
// manager changed their access schedule or certification ceiling — so
// the client purges OS-owned caches (Top Shelf/CarPlay/Cast/Engage) that may now
// surface newly-out-of-policy content. Content-free + Profile-gated (the affected
// Profile, never the acting manager). A nil store or empty Profile is a no-op.
func (h *Handler) emitAccessCeilingInvalidation(ctx context.Context, profileID store.ProfileID) error {
	if h.store == nil || profileID == "" {
		return nil
	}
	return h.store.AppendInvalidationEvent(ctx, store.InvalidationPayload{
		ProfileID: profileID,
		Reason:    store.InvalidationReasonAccessChanged,
	}, time.Now().UTC())
}

// accessWindowsFromVariables parses the optional `windows` list variable into
// store windows. Each element must be an object with numeric weekday,
// startMinute, and endMinute. A non-object element or a missing/non-numeric
// field is a stable client error (the store re-validates ranges). An absent
// list yields nil — the clear-the-schedule case.
func accessWindowsFromVariables(req request) ([]store.ProfileAccessWindow, error) {
	raw, ok := req.Variables["windows"].([]any)
	if !ok {
		return nil, nil
	}
	windows := make([]store.ProfileAccessWindow, 0, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, clientError("each access window must be an object")
		}
		weekday, ok := numberField(obj, "weekday")
		if !ok {
			return nil, clientError("each access window requires a numeric weekday")
		}
		start, ok := numberField(obj, "startMinute")
		if !ok {
			return nil, clientError("each access window requires a numeric startMinute")
		}
		end, ok := numberField(obj, "endMinute")
		if !ok {
			return nil, clientError("each access window requires a numeric endMinute")
		}
		windows = append(windows, store.ProfileAccessWindow{
			Weekday:     int(weekday),
			StartMinute: int(start),
			EndMinute:   int(end),
		})
	}
	return windows, nil
}
