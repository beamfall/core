package auth

import "errors"

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
