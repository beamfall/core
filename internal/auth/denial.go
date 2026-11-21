package auth

import (
	"errors"

	"github.com/beamfall/core/internal/store"
)

// DenialReasonHeader names the response header that carries a closed,
// client-readable denial reason on a refused REST playback or media transport
// request (CORE-2950). Clients branch on its value, never on the body text;
// an absent header means the refusal has no finer reason.
const DenialReasonHeader = "X-Beamfall-Denial-Reason"

// DenialReasonAccessScheduleEnded is the denial reason for a request refused
// only because the caller's managed Profile is outside its access schedule
// (and holds no approved extension). The credential itself is valid: the
// client keeps its session and shows that access has ended for now, instead
// of signing out. The reason says nothing about the schedule's windows, and
// it is emitted only after the credential verified, so it reaches nobody but
// the Profile's own session. GraphQL carries the same value in
// extensions.reason.
const DenialReasonAccessScheduleEnded = "ACCESS_SCHEDULE_ENDED"

// AccessScheduleEnded reports whether err is the access-schedule refusal from
// scope resolution, as opposed to any other credential failure.
func AccessScheduleEnded(err error) bool {
	return errors.Is(err, ErrAccessOutsideSchedule)
}

// SessionBinding names the session, Profile and device a credential was
// issued for.
type SessionBinding struct {
	SessionID store.SessionID
	ProfileID store.ProfileID
	DeviceID  store.DeviceID
}

// sessionScheduleRefusal is the access-schedule refusal from an enrolled
// session's scope resolution. It records the one session snapshot the
// schedule was evaluated against, so a caller decides who may see the reason
// from that snapshot and never from a second read that a concurrent Profile
// switch could change.
type sessionScheduleRefusal struct {
	snapshot SessionBinding
}

func (e *sessionScheduleRefusal) Error() string { return ErrAccessOutsideSchedule.Error() }

func (e *sessionScheduleRefusal) Unwrap() error { return ErrAccessOutsideSchedule }

// AccessScheduleReasonVisible reports whether a credential refused with err
// may be told the reason is ACCESS_SCHEDULE_ENDED. Only a refusal evaluated on
// an enrolled session qualifies: a scoped key or token keeps its surface's
// generic refusal. bound is the credential's own binding when it is a
// derived credential such as a Stream Token; the session snapshot the schedule
// was evaluated against must hold exactly that session, Profile and device.
// A nil bound means the credential is the session itself.
func AccessScheduleReasonVisible(err error, bound *SessionBinding) bool {
	var refusal *sessionScheduleRefusal
	if !errors.As(err, &refusal) {
		return false
	}
	return bound == nil || *bound == refusal.snapshot
}
