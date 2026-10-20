//go:build io

package ioe2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/beamfall/core/internal/push"
)

// TestEgress_push_apns drives the REAL APNs backend against a loopback double and
// asserts the POST to /3/device/<token> with the provider-JWT bearer + apns-topic.
//
// Note: the production APNs endpoint is HTTP/2, but the backend posts through a
// plain Doer seam, so a default HTTP/1.1 httptest server exercises the real send
// path with zero outbound packets.
//
// io:egress-push-apns
func TestEgress_push_apns(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate APNs key: %v", err)
	}
	var (
		gotPath  string
		gotMeth  string
		gotAuth  string
		gotTopic string
		gotBody  []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMeth = r.Method
		gotAuth = r.Header.Get("authorization")
		gotTopic = r.Header.Get("apns-topic")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := push.NewAPNsBackend(push.APNsConfig{
		SigningKey: key, KeyID: "KEY123", TeamID: "TEAM45", Topic: "com.beamfall.app", BaseURL: srv.URL,
	}, srv.Client())
	if err := b.Send(context.Background(), "device-token-xyz", push.Payload{Type: "extension_request", EventID: "evt-9"}); err != nil {
		t.Fatalf("APNs Send: %v", err)
	}

	if gotMeth != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMeth)
	}
	if gotPath != "/3/device/device-token-xyz" {
		t.Errorf("path = %q, want /3/device/device-token-xyz", gotPath)
	}
	if gotTopic != "com.beamfall.app" {
		t.Errorf("apns-topic = %q", gotTopic)
	}
	if !strings.HasPrefix(gotAuth, "bearer ") {
		t.Errorf("authorization = %q, want a bearer provider JWT", gotAuth)
	}
	assertPushPayload(t, gotBody, "extension_request", "evt-9")
}

// TestEgress_push_fcm drives the REAL FCM backend against a loopback double: it
// asserts the OAuth2 token POST (assertion present) AND the v1 send POST carrying
// the minted bearer token.
//
// io:egress-push-fcm
func TestEgress_push_fcm(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate FCM key: %v", err)
	}
	var (
		tokenHits    int32
		sendHits     int32
		gotAssertion string
		gotSendAuth  string
		gotSendPath  string
		gotSendBody  []byte
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenHits, 1)
		body, _ := io.ReadAll(r.Body)
		for _, kv := range strings.Split(string(body), "&") {
			if strings.HasPrefix(kv, "assertion=") {
				gotAssertion = strings.TrimPrefix(kv, "assertion=")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.fake", "expires_in": 3600})
	})
	mux.HandleFunc("/v1/projects/proj-1/messages:send", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sendHits, 1)
		gotSendAuth = r.Header.Get("authorization")
		gotSendPath = r.URL.Path
		gotSendBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := push.NewFCMBackend(push.FCMConfig{
		ProjectID: "proj-1", ClientEmail: "svc@proj-1.iam", PrivateKey: rsaKey,
		TokenURL: srv.URL + "/token", BaseURL: srv.URL,
	}, srv.Client())
	if err := b.Send(context.Background(), "fcm-reg-token", push.Payload{Type: "watch_party_invite", EventID: "evt-77"}); err != nil {
		t.Fatalf("FCM Send: %v", err)
	}

	if got := atomic.LoadInt32(&tokenHits); got != 1 {
		t.Errorf("OAuth token double hit %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&sendHits); got != 1 {
		t.Errorf("send double hit %d times, want 1", got)
	}
	if gotAssertion == "" {
		t.Error("no OAuth2 assertion presented to the token endpoint")
	}
	if gotSendAuth != "Bearer ya29.fake" {
		t.Errorf("send Authorization = %q, want the minted bearer token", gotSendAuth)
	}
	if gotSendPath != "/v1/projects/proj-1/messages:send" {
		t.Errorf("send path = %q", gotSendPath)
	}
	assertFCMPayload(t, gotSendBody, "watch_party_invite", "evt-77")
}

// assertPushPayload asserts an APNs-style body carries the minimized type +
// eventId custom keys.
func assertPushPayload(t *testing.T, body []byte, wantType, wantEvent string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("push body not JSON: %v (body=%s)", err, body)
	}
	if m["type"] != wantType {
		t.Errorf("type = %v, want %q", m["type"], wantType)
	}
	if m["eventId"] != wantEvent {
		t.Errorf("eventId = %v, want %q", m["eventId"], wantEvent)
	}
}

// assertFCMPayload asserts the FCM v1 message.data carries the minimized type +
// eventId.
func assertFCMPayload(t *testing.T, body []byte, wantType, wantEvent string) {
	t.Helper()
	var m struct {
		Message struct {
			Token string            `json:"token"`
			Data  map[string]string `json:"data"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("FCM body not JSON: %v (body=%s)", err, body)
	}
	if m.Message.Data["type"] != wantType {
		t.Errorf("data.type = %q, want %q", m.Message.Data["type"], wantType)
	}
	if m.Message.Data["eventId"] != wantEvent {
		t.Errorf("data.eventId = %q, want %q", m.Message.Data["eventId"], wantEvent)
	}
}
