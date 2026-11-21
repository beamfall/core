package graph

import (
	"errors"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// ErrorCode is the closed extensions.code vocabulary every GraphQL error
// carries (ARCHITECTURE §7). Clients branch on the code, never on the message.
// Growing the set is a wire-contract change.
type ErrorCode string

const (
	// ErrorCodeNotFound: the named thing does not exist or the caller cannot
	// see it. The §6 denial-of-existence rule makes the two indistinguishable,
	// so content outside the caller's scope always answers NOT_FOUND.
	ErrorCodeNotFound ErrorCode = "NOT_FOUND"
	// ErrorCodeForbidden: a role, tier, session-kind or Reveal gate refused an
	// authenticated caller. Never used for content the caller cannot see.
	ErrorCodeForbidden ErrorCode = "FORBIDDEN"
	// ErrorCodeUnauthenticated: no valid session or device credential.
	ErrorCodeUnauthenticated ErrorCode = "UNAUTHENTICATED"
	// ErrorCodeThrottled: attempt limiting refused the request; retry later.
	ErrorCodeThrottled ErrorCode = "THROTTLED"
	// ErrorCodeInvalid: the request or one of its arguments is malformed.
	ErrorCodeInvalid ErrorCode = "INVALID"
	// ErrorCodeConflict: the target's current state does not allow the
	// operation (already resolved, already provisioned, needs confirmation).
	ErrorCodeConflict ErrorCode = "CONFLICT"
	// ErrorCodeInternal: a server-side failure or a capability this server
	// does not provide. An unclassified failure carries the generic
	// "<field> unavailable" message and keeps its details in the log; a
	// capability the server lacks keeps its stable client message.
	ErrorCodeInternal ErrorCode = "INTERNAL"
)

// ErrorCodes returns the closed vocabulary in its documented order.
func ErrorCodes() []ErrorCode {
	return []ErrorCode{
		ErrorCodeNotFound,
		ErrorCodeForbidden,
		ErrorCodeUnauthenticated,
		ErrorCodeThrottled,
		ErrorCodeInvalid,
		ErrorCodeConflict,
		ErrorCodeInternal,
	}
}

// GraphQLErrorWire is the published shape of one entry in a GraphQL
// response's errors array; the wire contract reflects it.
type GraphQLErrorWire = graphError

type graphErrorExtensions struct {
	Code ErrorCode `json:"code"`
	// Reason refines Code where clients must act differently on refusals that
	// share one code (CORE-1551). Absent for most errors; see ErrorReasons.
	Reason ErrorReason `json:"reason,omitempty"`
}

// ErrorReason is the closed extensions.reason vocabulary. It refines an
// error's code where a client must tell refusals apart that share one code;
// clients branch on it, never on the message. Growing the set is a
// wire-contract change.
type ErrorReason string

const (
	// ErrorReasonRevealPINNotSet: the step-up needs a reveal PIN and this
	// Profile has none. Entering a PIN cannot help; the person sets one first.
	ErrorReasonRevealPINNotSet ErrorReason = "REVEAL_PIN_NOT_SET"
	// ErrorReasonRevealPINRequired: the step-up needs the Profile's reveal PIN
	// and the request carried none.
	ErrorReasonRevealPINRequired ErrorReason = "REVEAL_PIN_REQUIRED"
	// ErrorReasonRevealPINIncorrect: the request carried a reveal PIN that does
	// not match.
	ErrorReasonRevealPINIncorrect ErrorReason = "REVEAL_PIN_INCORRECT"
	// ErrorReasonProfileMismatch: the write named, in expectedProfileID, a
	// Profile the session is no longer bound to (CORE-2177). Nothing was
	// written; the client drops the write instead of retrying it.
	ErrorReasonProfileMismatch ErrorReason = "PROFILE_MISMATCH"
)

// ErrorReasons returns the closed reason vocabulary in its documented order.
func ErrorReasons() []ErrorReason {
	return []ErrorReason{
		ErrorReasonRevealPINNotSet,
		ErrorReasonRevealPINRequired,
		ErrorReasonRevealPINIncorrect,
		ErrorReasonProfileMismatch,
	}
}

// reasonedClientError is a clientError that also carries an extensions.reason.
// It unwraps to its clientError, so the code and message rules apply as usual.
type reasonedClientError struct {
	message clientError
	reason  ErrorReason
}

func (e reasonedClientError) Error() string { return string(e.message) }

func (e reasonedClientError) Unwrap() error { return e.message }

// errorReason returns err's extensions.reason, or "" when it carries none.
func errorReason(err error) ErrorReason {
	var re reasonedClientError
	if errors.As(err, &re) {
		return re.reason
	}
	return ""
}

// revealPINRefusal reports a reveal PIN mismatch with the reason that tells a
// missing PIN from a wrong one. VerifyRevealPIN answers ErrRevealPINMismatch
// for both, so the empty-PIN check decides.
func revealPINRefusal(message clientError, pin string) error {
	reason := ErrorReasonRevealPINIncorrect
	if pin == "" {
		reason = ErrorReasonRevealPINRequired
	}
	return reasonedClientError{message: message, reason: reason}
}

func newGraphError(code ErrorCode, message string) graphError {
	return graphError{Message: message, Extensions: graphErrorExtensions{Code: code}}
}

// errorCode maps a failed field's error to its code: client-caused errors by
// their stable message, store validation errors to INVALID, everything else
// (logged and surfaced as "<field> unavailable") to INTERNAL.
func errorCode(err error) ErrorCode {
	var ce clientError
	var ve store.ValidationError
	switch {
	case errors.As(err, &ce):
		code, _ := ClientErrorCode(string(ce))
		return code
	case errors.As(err, &ve):
		// Store validation is a bad argument unless its exact message is
		// pinned below, e.g. "unknown library" answering NOT_FOUND (§6).
		if code, ok := clientErrorCodes[string(ve)]; ok {
			return code
		}
		// A record the argument names that is missing (e.g. "media not found").
		if strings.HasSuffix(string(ve), "not found") {
			return ErrorCodeNotFound
		}
		return ErrorCodeInvalid
	default:
		return ErrorCodeInternal
	}
}

// clientErrorCodes pins the client error messages no rule below classifies,
// or that a rule would classify wrongly. A message here wins over every rule.
var clientErrorCodes = map[string]ErrorCode{
	// §6 denial-of-existence: content the caller cannot see answers exactly
	// as content that does not exist.
	"cannot host a party for content you cannot see":                   ErrorCodeNotFound,
	"cannot join: this party's content is not visible to your profile": ErrorCodeNotFound,
	"no profile with that display name":                                ErrorCodeNotFound,
	// Also returned by the store as a ValidationError for a Library the
	// caller's filter hides or that does not exist.
	"unknown library": ErrorCodeNotFound,
	"unknown profile": ErrorCodeNotFound,
	// An uploaded artwork candidate whose original bytes were never kept or
	// were purged (CORE-2937): the client must upload the image again.
	"uploaded artwork is no longer available; upload it again": ErrorCodeNotFound,

	// "unknown <value>" for a closed vocabulary is a bad argument, not a
	// missing record. Resolvers re-wrap store and push validation text into a
	// clientError, so these are pinned here as well as by the store rules.
	"unknown push platform": ErrorCodeInvalid,
	// Two ways to order one relation row at once.
	"give sortOrder or position, not both": ErrorCodeInvalid,
	"media is not queued":                  ErrorCodeInvalid,
	"unknown artwork slot":                 ErrorCodeInvalid,
	"unknown delete scope":                 ErrorCodeInvalid,
	"unknown engagement kind":              ErrorCodeInvalid,
	"unknown family scope":                 ErrorCodeInvalid,
	"unknown importer":                     ErrorCodeInvalid,
	"unknown lifecycle action":             ErrorCodeInvalid,
	"unknown or non-flippable capability":  ErrorCodeInvalid,
	"unknown permission":                   ErrorCodeInvalid,
	"unknown plugin action":                ErrorCodeInvalid,
	"unknown plugin config field":          ErrorCodeInvalid,
	"unknown transport command":            ErrorCodeInvalid,

	"admin password rejected":                                   ErrorCodeForbidden,
	"adult profile or manager role required":                    ErrorCodeForbidden,
	"cannot change the current device role":                     ErrorCodeForbidden,
	"incorrect passphrase or recovery key":                      ErrorCodeForbidden,
	"managed profiles require guardian or operator data delete": ErrorCodeForbidden,
	"This item is locked.":                                      ErrorCodeForbidden,
	"not a member of this group":                                ErrorCodeForbidden,
	"not a member of this party":                                ErrorCodeForbidden,
	"not the party host":                                        ErrorCodeForbidden,
	"this party has no lead":                                    ErrorCodeInvalid,
	"only the lead reports to this party":                       ErrorCodeForbidden,
	// CORE-0531: create and accept are home-network only, and a party moves
	// only to Media every member can see.
	"Watch Party is available on your home network only":               ErrorCodeForbidden,
	"cannot play this in the party: it is not visible to every member": ErrorCodeForbidden,
	"only a managed profile can request access to a locked item":       ErrorCodeForbidden,
	"only a managed profile can request an extension":                  ErrorCodeForbidden,
	"only an adult profile can resolve an access request":              ErrorCodeForbidden,
	"only an adult profile can respond to an extension request":        ErrorCodeForbidden,
	"only an adult profile can revoke an access grant":                 ErrorCodeForbidden,
	// CORE-2725: a compatibility integration key binds an enabled adult Profile.
	"only an enabled adult profile can hold an integration key": ErrorCodeForbidden,
	"profile PIN required":                                           ErrorCodeForbidden,
	"profile has no reveal path":                                     ErrorCodeForbidden,
	"profile is disabled":                                            ErrorCodeForbidden,
	"reveal PIN mismatch":                                            ErrorCodeForbidden,
	"reveal PIN must be set before exporting your data":              ErrorCodeForbidden,
	"session not controllable":                                       ErrorCodeForbidden,
	"retry the offline pack build from the device that requested it": ErrorCodeForbidden,

	"device revoked":  ErrorCodeUnauthenticated,
	"invalid session": ErrorCodeUnauthenticated,

	"access request already resolved":                                                              ErrorCodeConflict,
	"no backup key is waiting to be saved":                                                         ErrorCodeConflict,
	"the backup key changed; reveal it again":                                                      ErrorCodeConflict,
	"encrypted library is already provisioned":                                                     ErrorCodeConflict,
	"this item was merged into another item as a Version; undo the merge to split it back":         ErrorCodeConflict,
	"an item in this batch was later merged into another item as a Version; undo that merge first": ErrorCodeConflict,
	"job is not terminal":                                                                          ErrorCodeConflict,
	"job is terminal":                                                                              ErrorCodeConflict,
	"This workflow is disabled in Console → Optional features.":                                    ErrorCodeConflict,
	"no identification to refresh; use Fix Match first":                                            ErrorCodeConflict,
	"extension request already resolved":                                                           ErrorCodeConflict,
	"library is already provisioned":                                                               ErrorCodeConflict,
	"library is not provisioned":                                                                   ErrorCodeConflict,
	"library must be unsealed before rewrap":                                                       ErrorCodeConflict,
	"no runnable job":                                                                              ErrorCodeConflict,
	"offline pack is still building":                                                               ErrorCodeConflict,
	"another offline pack build for the same selection is running; retry when it finishes":         ErrorCodeConflict,
	"the previous offline pack build is still finishing; retry shortly":                            ErrorCodeConflict,
	"queue has items; pass confirmReplace to replace it":                                           ErrorCodeConflict,
	"the session is bound to another profile":                                                      ErrorCodeConflict,
	"session has no active playback":                                                               ErrorCodeConflict,
	"scan preview token was already used":                                                          ErrorCodeConflict,
	"scan preview token has expired":                                                               ErrorCodeConflict,
	"scan preview is stale because the library changed":                                            ErrorCodeConflict,

	"offline pack job did not produce a pack":    ErrorCodeInternal,
	"playback rendition cache is not configured": ErrorCodeInternal,
	"scraper credential could not be disabled":   ErrorCodeInternal,
	"scraper credential could not be saved":      ErrorCodeInternal,

	"a grant cannot bind both a Profile and all Libraries":     ErrorCodeInvalid,
	"display name is ambiguous":                                ErrorCodeInvalid,
	"importer does not support the requested data category":    ErrorCodeInvalid,
	"libraryIcon cannot be combined with other console config": ErrorCodeInvalid,
	"library has no roots":                                     ErrorCodeInvalid,
	"library is not flagged encrypted":                         ErrorCodeInvalid,
	"media does not belong to the subscription library":        ErrorCodeInvalid,
	"no console config to update":                              ErrorCodeInvalid,
	"no profile settings to update":                            ErrorCodeInvalid,
	"no transcode config to update":                            ErrorCodeInvalid,
	"not a date":                                               ErrorCodeInvalid,
	"not a migration job":                                      ErrorCodeInvalid,
	"original quality is limited to 4 GiB per item; choose high, medium or low quality": ErrorCodeInvalid,
	"not a number": ErrorCodeInvalid,
	"permission is not declared by the active plugin manifest": ErrorCodeInvalid,
	"pre-authorization applies to managed profiles only":       ErrorCodeInvalid,
	"required plugin config field cannot be cleared":           ErrorCodeInvalid,
	"scan root is not a folder":                                ErrorCodeInvalid,
	"unsupported query":                                        ErrorCodeInvalid,
}

type clientErrorRule struct {
	code     ErrorCode
	prefix   string
	suffix   string
	contains string
}

func (r clientErrorRule) matches(message string) bool {
	return strings.HasPrefix(message, r.prefix) &&
		strings.HasSuffix(message, r.suffix) &&
		strings.Contains(message, r.contains)
}

// clientErrorRules classify the regular message families, first match wins.
// Only the INVALID families below match dynamic messages built at runtime.
var clientErrorRules = []clientErrorRule{
	{code: ErrorCodeThrottled, suffix: " throttled"},
	{code: ErrorCodeNotFound, suffix: "not found"},
	// An unknown field in the query text is a malformed request.
	{code: ErrorCodeInvalid, prefix: "unknown top-level field"},
	// The createApiKey resolver re-wraps the store's validation text.
	{code: ErrorCodeInvalid, prefix: "unknown API scope"},
	{code: ErrorCodeNotFound, prefix: "unknown "},
	{code: ErrorCodeForbidden, prefix: "a session "},
	{code: ErrorCodeForbidden, prefix: "a profile session "},
	{code: ErrorCodeForbidden, prefix: "a profile is required"},
	{code: ErrorCodeForbidden, prefix: "an enrolled device session "},
	{code: ErrorCodeForbidden, prefix: "reveal required to "},
	{code: ErrorCodeForbidden, prefix: "reveal PIN required to "},
	{code: ErrorCodeForbidden, prefix: "permission ", suffix: " is not approved"},
	// A Library health dimension the Library's plugin cannot check (CORE-2383).
	{code: ErrorCodeConflict, suffix: " is not applicable to this library"},
	// An installed plugin built for an incompatible plugin SDK (CORE-2837).
	{code: ErrorCodeConflict, prefix: "plugin does not support this server's plugin SDK: "},
	{code: ErrorCodeInvalid, suffix: " required"},
	{code: ErrorCodeInvalid, contains: " is required "},
	{code: ErrorCodeInvalid, contains: " are required "},
	{code: ErrorCodeInvalid, contains: " must "},
	{code: ErrorCodeInvalid, contains: " requires "},
	{code: ErrorCodeInvalid, contains: " needs "},
	{code: ErrorCodeInvalid, contains: " is not a supported "},
	{code: ErrorCodeInvalid, suffix: " is not valid JSON"},
	{code: ErrorCodeInvalid, prefix: "invalid "},
	{code: ErrorCodeInvalid, prefix: "too many "},
	{code: ErrorCodeInvalid, prefix: "duplicate "},
	{code: ErrorCodeInvalid, contains: " exceeds maximum of "},
	{code: ErrorCodeInvalid, suffix: " cannot be empty"},
	{code: ErrorCodeInvalid, contains: " is not on the plugin's certification scale"},
	{code: ErrorCodeInvalid, prefix: "field ", contains: " is not "},
	{code: ErrorCodeInvalid, prefix: "field ", suffix: " has an unknown type"},
	{code: ErrorCodeInvalid, prefix: "conflicting selections for response key "},
	{code: ErrorCodeInvalid, prefix: "filters["},
	// A capability or dependency this server lacks. Anchored on the message's
	// end and checked last, so a request value quoted into an INVALID message
	// (e.g. ceiling "unavailable") never reads as a server failure.
	{code: ErrorCodeInternal, suffix: " unavailable"},
	{code: ErrorCodeInternal, suffix: " unavailable on this server"},
}

// ClientErrorCode is the one mapping from a client error's stable message to
// its code: the exact table first, then the family rules. mapped is false when
// neither covers the message; the code is then INVALID, because a client error
// is by definition caused by the request. The conformance suite fails on any
// unmapped message it can see in source.
func ClientErrorCode(message string) (code ErrorCode, mapped bool) {
	if code, ok := clientErrorCodes[message]; ok {
		return code, true
	}
	for _, rule := range clientErrorRules {
		if rule.matches(message) {
			return rule.code, true
		}
	}
	return ErrorCodeInvalid, false
}
