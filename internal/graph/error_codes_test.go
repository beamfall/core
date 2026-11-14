package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// graphErrorsOnWire posts body and returns the raw errors array, so the test
// sees the serialized extensions object rather than a decoded struct.
func graphErrorsOnWire(t *testing.T, handler *Handler, body string, authorize func(*http.Request)) (int, []map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	if authorize != nil {
		authorize(req)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp struct {
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	return rec.Code, resp.Errors
}

func wireErrorCode(t *testing.T, entry map[string]any) string {
	t.Helper()
	extensions, ok := entry["extensions"].(map[string]any)
	if !ok {
		t.Fatalf("error %v has no extensions object", entry)
	}
	code, _ := extensions["code"].(string)
	return code
}

// TestGraphQLErrorsCarryExtensionsCode drives one request per code family
// through the handler and asserts the serialized extensions.code (§7).
func TestGraphQLErrorsCarryExtensionsCode(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	badSession := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-does-not-exist")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	for _, tc := range []struct {
		name      string
		body      string
		authorize func(*http.Request)
		status    int
		message   string
		code      ErrorCode
	}{
		{
			name:      "missing session",
			body:      `{"query":"query { apiKeys { id } }"}`,
			authorize: badSession,
			status:    http.StatusUnauthorized,
			message:   "invalid session",
			code:      ErrorCodeUnauthenticated,
		},
		{
			name:      "role gate",
			body:      `{"query":"query { apiKeys { id } }"}`,
			authorize: authorizeManager,
			status:    http.StatusOK,
			message:   "admin session required",
			code:      ErrorCodeForbidden,
		},
		{
			name:      "malformed query",
			body:      `{"query":"query { noSuchField }"}`,
			authorize: authorizeViewer,
			status:    http.StatusOK,
			code:      ErrorCodeInvalid,
		},
		{
			// Parser failures are malformed requests even when the message
			// reads like another family ("unknown operationName").
			name:      "operation name not in document",
			body:      `{"query":"query A { serverInfo { name } }","operationName":"B"}`,
			authorize: authorizeViewer,
			status:    http.StatusOK,
			code:      ErrorCodeInvalid,
		},
		{
			name:      "unsupported directive",
			body:      `{"query":"query { serverInfo @unavailable { name } }"}`,
			authorize: authorizeViewer,
			status:    http.StatusOK,
			code:      ErrorCodeInvalid,
		},
		{
			// media-archive is in a restricted Library the un-revealed viewer
			// cannot see: §6 answers exactly as for a missing Media.
			name:      "hidden content",
			body:      `{"query":"query($mediaID:String!) { mediaFields(mediaID: $mediaID) { name } }","variables":{"mediaID":"media-archive"}}`,
			authorize: authorizeViewer,
			status:    http.StatusOK,
			message:   "unknown media",
			code:      ErrorCodeNotFound,
		},
		{
			name:      "missing media",
			body:      `{"query":"query($mediaID:String!) { mediaFields(mediaID: $mediaID) { name } }","variables":{"mediaID":"media-nope"}}`,
			authorize: authorizeViewer,
			status:    http.StatusOK,
			message:   "unknown media",
			code:      ErrorCodeNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, errs := graphErrorsOnWire(t, handler, tc.body, tc.authorize)
			if status != tc.status {
				t.Fatalf("status = %d, want %d (errors=%v)", status, tc.status, errs)
			}
			if len(errs) == 0 {
				t.Fatal("no errors in response")
			}
			if tc.message != "" && errs[0]["message"] != tc.message {
				t.Fatalf("message = %v, want %q", errs[0]["message"], tc.message)
			}
			if got := wireErrorCode(t, errs[0]); got != string(tc.code) {
				t.Fatalf("extensions.code = %q, want %s (error=%v)", got, tc.code, errs[0])
			}
		})
	}
}

// TestGraphFailCodesByErrorKind covers the per-field failure path: client
// errors keep their mapped code, store validation errors are INVALID, and
// anything else is INTERNAL behind the generic "<field> unavailable" message.
func TestGraphFailCodesByErrorKind(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    ErrorCode
		message string
	}{
		{err: clientError("unknown media"), code: ErrorCodeNotFound, message: "unknown media"},
		{err: fmt.Errorf("wrapped: %w", clientError("reveal PIN attempts throttled")), code: ErrorCodeThrottled},
		{err: store.ValidationError("title is required"), code: ErrorCodeInvalid},
		// A store lookup that hides or misses a Library is NOT_FOUND (§6).
		{err: store.ValidationError("unknown library"), code: ErrorCodeNotFound, message: "unknown library"},
		{err: store.ValidationError("unknown segment kind intro"), code: ErrorCodeInvalid},
		{err: errors.New("disk on fire"), code: ErrorCodeInternal, message: "mediaFields unavailable"},
	} {
		got := newGraphError(errorCode(tc.err), graphFailMessage(context.Background(), "mediaFields", tc.err))
		if got.Extensions.Code != tc.code {
			t.Errorf("errorCode(%v) = %s, want %s", tc.err, got.Extensions.Code, tc.code)
		}
		if tc.message != "" && got.Message != tc.message {
			t.Errorf("graphFailMessage(%v) = %q, want %q", tc.err, got.Message, tc.message)
		}
	}
}

// TestStoreValidationNotFoundAndRewrappedInvalid pins two §7 rules: a store
// validation error naming a missing record is NOT_FOUND, and re-wrapped store
// or push text for a closed vocabulary is INVALID, not NOT_FOUND.
func TestStoreValidationNotFoundAndRewrappedInvalid(t *testing.T) {
	if got := errorCode(store.ValidationError("media not found")); got != ErrorCodeNotFound {
		t.Errorf("media not found = %s, want NOT_FOUND", got)
	}
	for _, msg := range []string{"unknown API scope: bogus", "unknown push platform"} {
		if got := errorCode(clientError(msg)); got != ErrorCodeInvalid {
			t.Errorf("%q = %s, want INVALID", msg, got)
		}
	}
}

// TestRequestValueNeverReadsAsServerFailure: a request value quoted into a
// validation message stays INVALID even when it spells "unavailable", while
// the server's own capability messages stay INTERNAL.
func TestRequestValueNeverReadsAsServerFailure(t *testing.T) {
	for msg, want := range map[string]ErrorCode{
		`ceiling "unavailable" is not on the plugin's certification scale`: ErrorCodeInvalid,
		"offline packs unavailable":                                        ErrorCodeInternal,
		"verified active plugin manifest is unavailable":                   ErrorCodeInternal,
		"plugin actions are unavailable on this server":                    ErrorCodeInternal,
	} {
		if got := errorCode(clientError(msg)); got != want {
			t.Errorf("%q = %s, want %s", msg, got, want)
		}
	}
}
