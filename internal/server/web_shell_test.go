package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The SPA shell and the service-worker script are the only un-hashed entry
// points of the embedded web app. A browser-cached shell references chunk
// hashes a newer server no longer serves, so both must carry a Cache-Control
// header that keeps the browser from reusing a previous build's copy.
func TestWebShellCacheControl(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{path: "/web/", want: "no-store"},
		{path: "/web/index.html", want: "no-store"},
		{path: "/web/deep/link", want: "no-store"},
		{path: "/web/sw.js", want: "no-cache"},
	}
	handler := webAppHandler()
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://server.test"+tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.want {
				t.Fatalf("Cache-Control = %q, want %q", got, tc.want)
			}
		})
	}
}
