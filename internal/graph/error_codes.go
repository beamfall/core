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
	// does not provide. The message is generic; details stay in the log.
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

	// "unknown <value>" for a closed vocabulary is a bad argument, not a
	// missing record.
	"unknown artwork slot":                ErrorCodeInvalid,
	"unknown delete scope":                ErrorCodeInvalid,
	"unknown engagement kind":             ErrorCodeInvalid,
	"unknown family scope":                ErrorCodeInvalid,
	"unknown importer":                    ErrorCodeInvalid,
	"unknown lifecycle action":            ErrorCodeInvalid,
	"unknown or non-flippable capability": ErrorCodeInvalid,
	"unknown permission":                  ErrorCodeInvalid,
	"unknown plugin action":               ErrorCodeInvalid,
	"unknown plugin config field":         ErrorCodeInvalid,
	"unknown transport command":           ErrorCodeInvalid,

	"admin password rejected":                                    ErrorCodeForbidden,
	"adult profile or manager role required":                     ErrorCodeForbidden,
	"cannot change the current device role":                      ErrorCodeForbidden,
	"incorrect passphrase or recovery key":                       ErrorCodeForbidden,
	"managed profiles require guardian or operator data delete":  ErrorCodeForbidden,
	"not a member of this group":                                 ErrorCodeForbidden,
	"not a member of this party":                                 ErrorCodeForbidden,
	"not the party host":                                         ErrorCodeForbidden,
	"only a managed profile can request access to a locked item": ErrorCodeForbidden,
	"only a managed profile can request an extension":            ErrorCodeForbidden,
	"only an adult profile can resolve an access request":        ErrorCodeForbidden,
	"only an adult profile can respond to an extension request":  ErrorCodeForbidden,
	"only an adult profile can revoke an access grant":           ErrorCodeForbidden,
	"profile PIN required":                                       ErrorCodeForbidden,
	"profile is disabled":                                        ErrorCodeForbidden,
	"reveal PIN mismatch":                                        ErrorCodeForbidden,
	"reveal PIN must be set before exporting your data":          ErrorCodeForbidden,
	"session not controllable":                                   ErrorCodeForbidden,

	"device revoked": ErrorCodeUnauthenticated,

	"access request already resolved":                    ErrorCodeConflict,
	"encrypted library is already provisioned":           ErrorCodeConflict,
	"extension request already resolved":                 ErrorCodeConflict,
	"library is already provisioned":                     ErrorCodeConflict,
	"library is not provisioned":                         ErrorCodeConflict,
	"library must be unsealed before rewrap":             ErrorCodeConflict,
	"no runnable job":                                    ErrorCodeConflict,
	"queue has items; pass confirmReplace to replace it": ErrorCodeConflict,
	"session has no active playback":                     ErrorCodeConflict,

	"offline pack job did not produce a pack":    ErrorCodeInternal,
	"playback rendition cache is not configured": ErrorCodeInternal,
	"scraper credential could not be disabled":   ErrorCodeInternal,
	"scraper credential could not be saved":      ErrorCodeInternal,

	"a grant cannot bind both a Profile and all Libraries":     ErrorCodeInvalid,
	"display name is ambiguous":                                ErrorCodeInvalid,
	"importer does not support the requested data category":    ErrorCodeInvalid,
	"library has no roots":                                     ErrorCodeInvalid,
	"library is not flagged encrypted":                         ErrorCodeInvalid,
	"media does not belong to the subscription library":        ErrorCodeInvalid,
	"no console config to update":                              ErrorCodeInvalid,
	"no profile settings to update":                            ErrorCodeInvalid,
	"no transcode config to update":                            ErrorCodeInvalid,
	"not a date":                                               ErrorCodeInvalid,
	"not a migration job":                                      ErrorCodeInvalid,
	"not a number":                                             ErrorCodeInvalid,
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
	{code: ErrorCodeNotFound, prefix: "unknown "},
	{code: ErrorCodeForbidden, prefix: "a session "},
	{code: ErrorCodeForbidden, prefix: "a profile session "},
	{code: ErrorCodeForbidden, prefix: "a profile is required"},
	{code: ErrorCodeForbidden, prefix: "an enrolled device session "},
	{code: ErrorCodeForbidden, prefix: "reveal required to "},
	{code: ErrorCodeForbidden, prefix: "reveal PIN required to "},
	{code: ErrorCodeForbidden, prefix: "permission ", suffix: " is not approved"},
	{code: ErrorCodeInternal, contains: "unavailable"},
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
