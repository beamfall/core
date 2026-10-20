package push

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/hkdf"
)

// --- Registrar (acceptance a + d): persistence and the disabled gate ---------

// fakeTokenStore is an in-memory TokenStore so a test can assert the registrar
// persisted the token without a real DB.
type fakeTokenStore struct {
	mu   sync.Mutex
	last *TokenRecord
}

func (f *fakeTokenStore) UpsertDevicePushToken(_ context.Context, t TokenRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = &t
	return nil
}

func TestRegistrarPersistsToken(t *testing.T) {
	store := &fakeTokenStore{}
	r := NewRegistrar(store, true)
	if !r.Enabled() {
		t.Fatal("registrar should report enabled")
	}
	err := r.Register(context.Background(), Registration{
		ProfileID: "profile-1", DeviceID: "device-1", Platform: PlatformAPNs, Token: "tok-abc",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if store.last == nil {
		t.Fatal("token was not persisted")
	}
	if store.last.DeviceID != "device-1" || store.last.ProfileID != "profile-1" ||
		store.last.Platform != "apns" || store.last.Token != "tok-abc" {
		t.Fatalf("persisted record mismatch: %+v", store.last)
	}
}

func TestRegistrarDisabledRefuses(t *testing.T) {
	store := &fakeTokenStore{}
	r := NewRegistrar(store, false)
	err := r.Register(context.Background(), Registration{
		ProfileID: "p", DeviceID: "d", Platform: PlatformFCM, Token: "t",
	})
	if !errors.As(err, &ErrPushDisabled{}) {
		t.Fatalf("disabled registrar should return ErrPushDisabled, got %v", err)
	}
	if store.last != nil {
		t.Fatal("disabled registrar must not persist")
	}
}

func TestRegistrarRejectsUnknownPlatform(t *testing.T) {
	store := &fakeTokenStore{}
	r := NewRegistrar(store, true)
	err := r.Register(context.Background(), Registration{
		ProfileID: "p", DeviceID: "d", Platform: PushPlatform("sms"), Token: "t",
	})
	if !errors.As(err, &ErrInvalidPlatform{}) {
		t.Fatalf("expected ErrInvalidPlatform, got %v", err)
	}
	if store.last != nil {
		t.Fatal("invalid platform must not persist")
	}
}

// --- Dispatcher dormancy (acceptance d) --------------------------------------

func TestDispatcherDormantWhenUnconfigured(t *testing.T) {
	d := NewDispatcher(nil)
	if d.Configured() {
		t.Fatal("dispatcher with no backends must report unconfigured")
	}
	err := d.SendTo(context.Background(), PlatformAPNs, "tok", Payload{Type: "x", EventID: "e"})
	if !errors.As(err, &ErrPushNotConfigured{}) {
		t.Fatalf("dormant dispatch should refuse with ErrPushNotConfigured, got %v", err)
	}
}

func TestDispatcherRoutesByPlatform(t *testing.T) {
	apns := &recordingBackend{}
	fcm := &recordingBackend{}
	d := NewDispatcher(map[PushPlatform]Backend{PlatformAPNs: apns, PlatformFCM: fcm})
	if !d.Configured() {
		t.Fatal("dispatcher with backends must report configured")
	}
	_ = d.SendTo(context.Background(), PlatformFCM, "tok-fcm", Payload{Type: "t", EventID: "e1"})
	if apns.calls != 0 || fcm.calls != 1 {
		t.Fatalf("expected FCM-only routing, got apns=%d fcm=%d", apns.calls, fcm.calls)
	}
	// A platform with no configured backend is honestly refused.
	err := d.SendTo(context.Background(), PlatformWebPush, "tok", Payload{Type: "t", EventID: "e"})
	if !errors.As(err, &ErrPushNotConfigured{}) {
		t.Fatalf("unconfigured platform should refuse, got %v", err)
	}
}

type recordingBackend struct{ calls int }

func (b *recordingBackend) Send(context.Context, string, Payload) error { b.calls++; return nil }

// --- APNs backend (acceptance b + c + e): signed request, payload, no secret --

func TestAPNsBackendSignsAndPostsMinimizedPayload(t *testing.T) {
	key := mustECDSAKey(t)
	var (
		gotAuth  string
		gotTopic string
		gotPath  string
		gotBody  []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		gotTopic = r.Header.Get("apns-topic")
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := NewAPNsBackend(APNsConfig{
		SigningKey: key, KeyID: "KEY123", TeamID: "TEAM45", Topic: "com.beamfall.app", BaseURL: srv.URL,
	}, srv.Client())
	err := b.Send(context.Background(), "device-token-xyz", Payload{Type: "extension_request", EventID: "evt-9"})
	if err != nil {
		t.Fatalf("APNs Send: %v", err)
	}

	if gotPath != "/3/device/device-token-xyz" {
		t.Fatalf("APNs path: got %q", gotPath)
	}
	if gotTopic != "com.beamfall.app" {
		t.Fatalf("apns-topic: got %q", gotTopic)
	}
	// The Authorization header is a verifiable ES256 JWT with kid=KEY123,
	// iss=TEAM45 — proving it was signed with the operator key, not a literal
	// secret on the wire.
	if !strings.HasPrefix(gotAuth, "bearer ") {
		t.Fatalf("authorization not a bearer token: %q", gotAuth)
	}
	jwt := strings.TrimPrefix(gotAuth, "bearer ")
	assertES256(t, jwt, &key.PublicKey, map[string]string{"kid": "KEY123"}, map[string]any{"iss": "TEAM45"})

	assertMinimizedPayload(t, gotBody, "extension_request", "evt-9")
}

// --- FCM backend (acceptance b + c): OAuth2 exchange then send ---------------

func TestFCMBackendOAuthThenMinimizedSend(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var (
		gotAssertion string
		gotSendAuth  string
		gotSendPath  string
		gotSendBody  []byte
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		for _, kv := range strings.Split(string(body), "&") {
			if strings.HasPrefix(kv, "assertion=") {
				gotAssertion = strings.TrimPrefix(kv, "assertion=")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.fake", "expires_in": 3600})
	})
	mux.HandleFunc("/v1/projects/proj-1/messages:send", func(w http.ResponseWriter, r *http.Request) {
		gotSendAuth = r.Header.Get("authorization")
		gotSendPath = r.URL.Path
		gotSendBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := NewFCMBackend(FCMConfig{
		ProjectID: "proj-1", ClientEmail: "svc@proj-1.iam", PrivateKey: rsaKey,
		TokenURL: srv.URL + "/token", BaseURL: srv.URL,
	}, srv.Client())
	if err := b.Send(context.Background(), "fcm-reg-token", Payload{Type: "watch_party_invite", EventID: "evt-77"}); err != nil {
		t.Fatalf("FCM Send: %v", err)
	}

	if gotAssertion == "" {
		t.Fatal("no OAuth2 assertion presented")
	}
	assertRS256(t, gotAssertion, &rsaKey.PublicKey)
	if gotSendAuth != "Bearer ya29.fake" {
		t.Fatalf("send auth: got %q", gotSendAuth)
	}
	if gotSendPath != "/v1/projects/proj-1/messages:send" {
		t.Fatalf("send path: got %q", gotSendPath)
	}
	// FCM body nests data under message.data; assert it carries only type+eventId.
	var env struct {
		Message struct {
			Token string            `json:"token"`
			Data  map[string]string `json:"data"`
		} `json:"message"`
	}
	if err := json.Unmarshal(gotSendBody, &env); err != nil {
		t.Fatalf("decode FCM body: %v", err)
	}
	if env.Message.Token != "fcm-reg-token" {
		t.Fatalf("FCM token: got %q", env.Message.Token)
	}
	if len(env.Message.Data) != 2 || env.Message.Data["type"] != "watch_party_invite" || env.Message.Data["eventId"] != "evt-77" {
		t.Fatalf("FCM data not minimized: %+v", env.Message.Data)
	}
	if strings.Contains(string(gotSendBody), "notification") {
		t.Fatalf("FCM body contains a notification (content) block: %s", gotSendBody)
	}
}

// --- Web Push backend (acceptance b + c): VAPID + decryptable ciphertext ------

func TestWebPushBackendVAPIDAndDecryptableMinimizedPayload(t *testing.T) {
	vapidKey := mustECDSAKey(t)
	// The advertised VAPID public key rides the Authorization header verbatim;
	// the test only asserts it is present, so any non-empty base64url value works.
	vapidPub := "BFakeVapidPublicKeyForTestOnly"

	// Subscriber (browser) ECDH keypair + auth secret.
	uaPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uaPub := uaPriv.PublicKey().Bytes()
	authSecret := make([]byte, 16)
	_, _ = rand.Read(authSecret)

	var (
		gotEnc  string
		gotAuth string
		gotBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnc = r.Header.Get("content-encoding")
		gotAuth = r.Header.Get("authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sub := webPushSubscription{
		Endpoint: srv.URL + "/push/sub-1",
		P256dh:   base64.RawURLEncoding.EncodeToString(uaPub),
		Auth:     base64.RawURLEncoding.EncodeToString(authSecret),
	}
	token, _ := json.Marshal(sub)

	b := NewWebPushBackend(WebPushConfig{
		PrivateKey: vapidKey, PublicKey: vapidPub, Subject: "mailto:ops@example.com",
	}, srv.Client())
	if err := b.Send(context.Background(), string(token), Payload{Type: "extension_request", EventID: "evt-w"}); err != nil {
		t.Fatalf("WebPush Send: %v", err)
	}

	if gotEnc != "aes128gcm" {
		t.Fatalf("content-encoding: got %q", gotEnc)
	}
	if !strings.HasPrefix(gotAuth, "vapid t=") || !strings.Contains(gotAuth, ", k=") {
		t.Fatalf("not a VAPID auth header: %q", gotAuth)
	}
	// Decrypt the aes128gcm body with the subscriber's private key and assert it
	// is exactly the minimized payload — proving the ciphertext carries type +
	// eventId only and nothing else.
	plain := decryptAES128GCM(t, gotBody, uaPriv, uaPub, authSecret)
	assertMinimizedPayload(t, plain, "extension_request", "evt-w")
}

// --- Secret-never-logged invariant (acceptance e) ----------------------------

// TestPayloadCarriesOnlyTypeAndEventID hardens the load-bearing minimization
// rule at the wire level: the marshaled Payload has exactly two keys.
func TestPayloadCarriesOnlyTypeAndEventID(t *testing.T) {
	body, _ := json.Marshal(Payload{Type: "extension_request", EventID: "evt-1"})
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("payload has %d keys, want exactly 2 (type, eventId): %s", len(m), body)
	}
	if _, ok := m["type"]; !ok {
		t.Fatal("payload missing type")
	}
	if _, ok := m["eventId"]; !ok {
		t.Fatal("payload missing eventId")
	}
}

func TestErrorsNeverEchoToken(t *testing.T) {
	// A backend failure error must name the platform/status, never the token.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	b := NewAPNsBackend(APNsConfig{SigningKey: mustECDSAKey(t), KeyID: "k", TeamID: "team", Topic: "t", BaseURL: srv.URL}, srv.Client())
	err := b.Send(context.Background(), "super-secret-device-token", Payload{Type: "t", EventID: "e"})
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if strings.Contains(err.Error(), "super-secret-device-token") {
		t.Fatalf("error echoed the device token: %v", err)
	}
}

// --- test helpers ------------------------------------------------------------

func mustECDSAKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// assertMinimizedPayload parses body as JSON and asserts it carries the type +
// eventId and NO content field (title/body/media/etc).
func assertMinimizedPayload(t *testing.T, body []byte, wantType, wantEvent string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("payload not JSON: %v (%s)", err, body)
	}
	// APNs wraps in an aps envelope; pull type/eventId from the top level.
	if m["type"] != wantType {
		t.Fatalf("type: got %v want %q", m["type"], wantType)
	}
	if m["eventId"] != wantEvent {
		t.Fatalf("eventId: got %v want %q", m["eventId"], wantEvent)
	}
	for _, banned := range []string{"title", "body", "mediaTitle", "library", "playback", "summary", "name"} {
		if _, ok := m[banned]; ok {
			t.Fatalf("payload leaked content field %q: %s", banned, body)
		}
	}
	// The APNs aps envelope, if present, must be content-free (no alert).
	if aps, ok := m["aps"].(map[string]any); ok {
		if _, bad := aps["alert"]; bad {
			t.Fatalf("aps envelope carries a user-visible alert (content): %s", body)
		}
	}
}

// assertES256 verifies a compact ES256 JWT against pub and checks header/claims.
func assertES256(t *testing.T, jwt string, pub *ecdsa.PublicKey, wantHeader map[string]string, wantClaims map[string]any) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt not 3 parts: %q", jwt)
	}
	hdr := decodeJSON(t, parts[0])
	for k, v := range wantHeader {
		if hdr[k] != v {
			t.Fatalf("jwt header %q: got %v want %q", k, hdr[k], v)
		}
	}
	claims := decodeJSON(t, parts[1])
	for k, v := range wantClaims {
		if claims[k] != v {
			t.Fatalf("jwt claim %q: got %v want %v", k, claims[k], v)
		}
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("bad ES256 signature encoding: %v len=%d", err, len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("ES256 signature does not verify against the configured key")
	}
}

// assertRS256 verifies the FCM service-account assertion signs with the RSA key.
func assertRS256(t *testing.T, jwt string, pub *rsa.PublicKey) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt not 3 parts")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("bad signature: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("RS256 signature does not verify: %v", err)
	}
}

func decodeJSON(t *testing.T, seg string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("b64 decode: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("json: %v", err)
	}
	return m
}

// decryptAES128GCM reverses encryptAES128GCM with the subscriber's private key,
// returning the plaintext payload (the trailing 0x02 delimiter stripped).
func decryptAES128GCM(t *testing.T, body []byte, uaPriv *ecdh.PrivateKey, uaPub, authSecret []byte) []byte {
	t.Helper()
	if len(body) < 16+4+1 {
		t.Fatal("body too short")
	}
	salt := body[:16]
	idlen := int(body[20])
	serverPub := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]

	serverKey, err := ecdh.P256().NewPublicKey(serverPub)
	if err != nil {
		t.Fatalf("server pub: %v", err)
	}
	shared, err := uaPriv.ECDH(serverKey)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	keyInfo := append([]byte("WebPush: info\x00"), uaPub...)
	keyInfo = append(keyInfo, serverPub...)
	ikm := readHKDF(authSecret, shared, keyInfo, 32)
	cek := readHKDF(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := readHKDF(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("gcm open: %v", err)
	}
	// Strip the RFC 8188 padding delimiter (0x02 for the last record).
	for len(plain) > 0 && (plain[len(plain)-1] == 0x02 || plain[len(plain)-1] == 0x00) {
		plain = plain[:len(plain)-1]
	}
	return plain
}

func readHKDF(salt, ikm, info []byte, n int) []byte {
	r := hkdf.New(sha256.New, ikm, salt, info)
	out := make([]byte, n)
	_, _ = io.ReadFull(r, out)
	return out
}
