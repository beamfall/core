package qbittorrent

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
)

// decodeJSON unmarshals a Web API body into out, with a clear error that never
// echoes the body (which can be large or contain paths).
func decodeJSON(body []byte, out any) error {
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// newCookieJar returns a per-client cookie jar so the session cookie stays
// scoped to this Client and never bleeds into unrelated traffic. cookiejar.New
// with a nil options never returns a non-nil error in the standard library;
// the error is handled defensively anyway.
func newCookieJar() http.CookieJar {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil
	}
	return jar
}
