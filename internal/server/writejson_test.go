package server

import (
	"math"
	"net/http/httptest"
	"strings"
	"testing"
)

// feature:server — regression for the committed-header double-write:
// an encode failure must yield a clean 500 with no half-written
// JSON body, never a plaintext trailer appended after WriteHeader.
func TestWriteJSONEncodeFailureProducesCleanError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, math.Inf(1)) // +Inf is unmarshalable → encode failure

	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500 (encode failure must not commit the success status)", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "{") || strings.Contains(body, "+Inf") {
		t.Fatalf("body = %q, want a plain error with no partial JSON", body)
	}
}

func TestWriteJSONSuccessWritesStatusAndBody(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 201, map[string]string{"ok": "yes"})

	if rec.Code != 201 {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if want := "{\"ok\":\"yes\"}\n"; rec.Body.String() != want {
		t.Fatalf("body = %q, want %q", rec.Body.String(), want)
	}
}
