package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/store"
)

func TestConformanceQueueAddAfterPairing(t *testing.T) {
	cfg := testConfig(t)
	cfg.AllowConformanceSeed = true
	cfg.SeedProfile = string(store.SeedProfileConformance)
	a, err := New(t.Context(), cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("boot conformance app: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.store.SetSessionAdminUntil(t.Context(), "session-manager", time.Unix(3600, 0).UTC()); err != nil {
		t.Fatalf("open seeded Admin Session: %v", err)
	}

	handler := a.Handler()
	post := func(path string, body any, authorize func(*http.Request), status int, result any) {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, bytes.NewReader(payload))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Content-Type", "application/json")
		if authorize != nil {
			authorize(req)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("POST %s status = %d, want %d", path, rec.Code, status)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), result); err != nil {
			t.Fatalf("decode POST %s response: %v", path, err)
		}
	}

	var code pairing.CodeResponse
	post("/api/pairing-codes", pairing.CodeRequest{DeviceRole: "viewer"}, func(req *http.Request) {
		req.Header.Set(auth.SessionHeader, "session-manager")
		req.Header.Set(auth.DeviceHeader, "device-mac")
		req.Header.Set(auth.DeviceSecretHeader, "dev-manager-secret")
	}, http.StatusCreated, &code)
	var enrolled pairing.EnrollResponse
	post("/api/enroll", pairing.EnrollRequest{Code: code.Code, DeviceName: "Queue test viewer"}, nil, http.StatusCreated, &enrolled)

	var result struct {
		Errors     []json.RawMessage               `json:"errors"`
		Extensions graph.GraphQLResponseExtensions `json:"extensions"`
	}
	post("/api/graphql", map[string]string{
		"query": `mutation { queueAdd(mediaID: "media-alpha", position: 0) { id } }`,
	}, func(req *http.Request) {
		req.Header.Set(auth.SessionHeader, enrolled.SessionID)
		req.Header.Set(auth.DeviceHeader, enrolled.DeviceID)
		req.Header.Set(auth.DeviceSecretHeader, enrolled.DeviceSecret)
	}, http.StatusOK, &result)
	if len(result.Errors) != 0 {
		t.Fatalf("queueAdd returned errors for freshly paired device: %s", result.Errors)
	}
	receipts := result.Extensions.QueueAdds
	if len(receipts) != 1 {
		t.Fatalf("queueAdd returned %d receipts, want 1", len(receipts))
	}
	receipt := receipts[0]
	if receipt.ResponseKey != "queueAdd" || receipt.MediaID != "media-alpha" || receipt.Position == nil || *receipt.Position != 0 {
		t.Fatalf("queueAdd receipt = %+v, want media-alpha at position 0", receipt)
	}
}
