package updatecheck

import (
	"encoding/json"
	"fmt"
)

// State is the coarse, mutually exclusive outcome of one version check.
//
// It exists because "up to date" and "the check did not work" used to be the
// same wire shape: latestVersion empty + updateAvailable false. An operator who
// opted in and whose manifest host was unreachable read that as green. A
// security signal that fails silently is worse than no signal, so the two cases
// are now distinct values and the up-to-date answer is derived ONLY from the
// value that means it.
//
// The zero value is deliberately StateUnavailable: a Status assembled anywhere
// other than a successful, signature-verified fetch defaults to "the check did
// not produce an answer", never to a reassuring one. There is no code path that
// yields StateUpToDate without a manifest that was fetched, parsed, verified
// against the pinned trust anchor, and matched to the requested channel.
type State int

const (
	// StateUnavailable means the check ran but produced no trustworthy answer:
	// the manifest host was unreachable, the manifest was absent, the response
	// was malformed or oversized, or its signature was refused. It is NOT a
	// statement about the running version. This is the zero value.
	StateUnavailable State = iota

	// StateDisabled means the operator has not opted in, so no outbound request
	// was made (default-off, invariant #5). Also not a statement about the
	// running version.
	StateDisabled

	// StateUpToDate means a verified manifest was read and the running version
	// is at or ahead of the latest published version. This is the ONLY value
	// that may be reported as "you are up to date".
	StateUpToDate

	// StateUpdateAvailable means a verified manifest was read and it names a
	// strictly newer version than the running one.
	StateUpdateAvailable
)

// stateNames maps each State to its stable wire token. Lookup-table dispatch:
// an unmapped value has no token, which is what makes an out-of-range State a
// marshalling error rather than a silently plausible answer.
var stateNames = map[State]string{
	StateUnavailable:     "unavailable",
	StateDisabled:        "disabled",
	StateUpToDate:        "upToDate",
	StateUpdateAvailable: "updateAvailable",
}

// String returns the wire token for s, or an explicit unknown marker.
func (s State) String() string {
	name, ok := stateNames[s]
	if !ok {
		return fmt.Sprintf("state(%d)", int(s))
	}
	return name
}

// MarshalJSON encodes the State as its stable wire token. An out-of-range value
// is an error, not a default: the encoder must never invent a state.
func (s State) MarshalJSON() ([]byte, error) {
	name, ok := stateNames[s]
	if !ok {
		return nil, fmt.Errorf("updatecheck: cannot encode unknown state %d", int(s))
	}
	return json.Marshal(name)
}

// IsUpToDate reports whether s is the single value that means the running
// version is current. Every other value — including the zero value — is false.
//
// Callers MUST route the "you are current" answer through this, never through
// the absence of an update (an empty latest version or updateAvailable=false),
// which is exactly the conflation this type removes.
func (s State) IsUpToDate() bool { return s == StateUpToDate }

// IsAnswered reports whether the check produced a trustworthy statement about
// the running version. False for StateUnavailable and StateDisabled — the two
// values that carry no version judgement at all.
func (s State) IsAnswered() bool { return s == StateUpToDate || s == StateUpdateAvailable }
