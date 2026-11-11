package auth

import (
	"net/http"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// Credential classes name the kind of credential a request presents, without
// verifying it. They are a closed vocabulary safe for logs and metric labels:
// a class never carries any part of the credential itself.
const (
	CredentialAnonymous = "anonymous"
	CredentialSession   = "session"
	CredentialAPIToken  = "api_token"
	CredentialScopedKey = "scoped_key"
)

// CredentialClass reports which credential the request presents, following
// the routing order of ScopeFromRequest: a Bearer credential (scoped key or
// admin API token), then the X-Beamfall-Session header, then the browser
// session cookie.
func CredentialClass(r *http.Request) string {
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if strings.HasPrefix(strings.TrimSpace(bearer), store.ScopedKeyPrefix) {
			return CredentialScopedKey
		}
		return CredentialAPIToken
	}
	if r.Header.Get("X-Beamfall-Session") != "" {
		return CredentialSession
	}
	if _, err := r.Cookie(SessionCookie); err == nil {
		return CredentialSession
	}
	return CredentialAnonymous
}
