package server

import "net/http"

// The refusal a request made for another Profile gets (CORE-2923). The code
// and reason are the wire values internal/graph's expectedProfileID refusal
// uses (ErrorCodeConflict, ErrorReasonProfileMismatch); server may not import
// graph, and the profile precondition tests pin both shapes.
const (
	graphQLPath                = "/api/graphql"
	profileMismatchCode        = "CONFLICT"
	profileMismatchReason      = "PROFILE_MISMATCH"
	profileMismatchMessage     = "the session is bound to another profile"
	profileMismatchRESTMessage = "profile mismatch"
)

// profileMismatchResponse is the refusal a non-GraphQL route gives a request
// made for another Profile.
type profileMismatchResponse struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

type profileMismatchGraphQLError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code   string `json:"code"`
		Reason string `json:"reason"`
	} `json:"extensions"`
}

// writeProfileMismatch refuses a request whose X-Beamfall-Profile header names
// a Profile other than the session's, before the route's handler runs, in the
// error shape the route's clients already read.
func writeProfileMismatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == graphQLPath {
		refusal := profileMismatchGraphQLError{Message: profileMismatchMessage}
		refusal.Extensions.Code = profileMismatchCode
		refusal.Extensions.Reason = profileMismatchReason
		writeJSON(w, http.StatusConflict, struct {
			Errors []profileMismatchGraphQLError `json:"errors"`
		}{Errors: []profileMismatchGraphQLError{refusal}})
		return
	}
	writeJSON(w, http.StatusConflict, profileMismatchResponse{
		Error:  profileMismatchRESTMessage,
		Code:   profileMismatchCode,
		Reason: profileMismatchReason,
	})
}
