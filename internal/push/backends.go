package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The three real dispatch backends. Each signs with the
// operator's credential (passed in by the composition root from the 0600 secure
// store, invariant #5 — never logged, never embedded in the payload) and POSTs
// the minimized Payload to a transport endpoint. Every endpoint is injectable
// (baseURL) so tests run against httptest, never the real APNs/FCM/push service.

// --- APNs HTTP/2 -----------------------------------------------------------

// APNsConfig is the operator's APNs credential set (ADR-0039 §2.5: "APNs key").
// SigningKey is the ECDSA private key parsed from the .p8 file; KeyID and TeamID
// are the Apple identifiers that accompany it; Topic is the app bundle ID the
// push targets. The raw .p8 bytes live in the secure store and are parsed by the
// composition root (ParseAPNsKey) — never logged.
type APNsConfig struct {
	SigningKey *ecdsa.PrivateKey
	KeyID      string
	TeamID     string
	Topic      string
	// BaseURL is the APNs endpoint; empty defaults to the production host. Tests
	// point it at an httptest server.
	BaseURL string
	// Production selects the production vs sandbox default host. Ignored when
	// BaseURL is set.
	Production bool
}

const (
	apnsProdHost    = "https://api.push.apple.com"
	apnsSandboxHost = "https://api.sandbox.push.apple.com"
)

// apnsBackend signs an ES256 provider JWT (cached ~50 min, Apple's <60 min rule)
// and POSTs the payload to /3/device/<token>.
type apnsBackend struct {
	cfg    APNsConfig
	client Doer
	now    func() time.Time

	mu        sync.Mutex
	cachedJWT string
	jwtIssued time.Time
}

// NewAPNsBackend builds the APNs backend. client defaults to http.DefaultClient.
func NewAPNsBackend(cfg APNsConfig, client Doer) *apnsBackend {
	if client == nil {
		client = http.DefaultClient
	}
	return &apnsBackend{cfg: cfg, client: client, now: time.Now}
}

func (b *apnsBackend) host() string {
	if b.cfg.BaseURL != "" {
		return strings.TrimRight(b.cfg.BaseURL, "/")
	}
	if b.cfg.Production {
		return apnsProdHost
	}
	return apnsSandboxHost
}

// providerJWT returns a cached or freshly-signed ES256 provider token. Apple
// rejects a token older than 1 hour and rate-limits frequent re-signing, so it
// is reused for ~50 minutes.
func (b *apnsBackend) providerJWT() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cachedJWT != "" && b.now().Sub(b.jwtIssued) < 50*time.Minute {
		return b.cachedJWT, nil
	}
	header := map[string]string{"alg": "ES256", "kid": b.cfg.KeyID, "typ": "JWT"}
	claims := map[string]any{"iss": b.cfg.TeamID, "iat": b.now().Unix()}
	jwt, err := signES256(b.cfg.SigningKey, header, claims)
	if err != nil {
		return "", err
	}
	b.cachedJWT = jwt
	b.jwtIssued = b.now()
	return jwt, nil
}

// Send POSTs the aps-typed payload to APNs for one device token.
func (b *apnsBackend) Send(ctx context.Context, token string, payload Payload) error {
	jwt, err := b.providerJWT()
	if err != nil {
		return err
	}
	// APNs body: an `aps` envelope with content-available (a silent,
	// data-only push — no alert/title/body, so no content), plus the minimized
	// type + event id as custom keys the app reads to fetch details.
	body, err := json.Marshal(map[string]any{
		"aps":     map[string]any{"content-available": 1},
		"type":    payload.Type,
		"eventId": payload.EventID,
	})
	if err != nil {
		return err
	}
	url := b.host() + "/3/device/" + token
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+jwt)
	req.Header.Set("apns-topic", b.cfg.Topic)
	req.Header.Set("apns-push-type", "background")
	req.Header.Set("apns-priority", "5")
	req.Header.Set("content-type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("apns: status %d", resp.StatusCode)
	}
	return nil
}

// --- FCM v1 ----------------------------------------------------------------

// FCMConfig is the operator's FCM credential set (ADR-0039 §2.5: "FCM service
// account"). ProjectID is the Firebase project; ClientEmail and PrivateKey come
// from the service-account JSON (the RSA key signs the OAuth2 assertion). The
// service-account JSON lives in the secure store and is parsed by the
// composition root (ParseFCMServiceAccount) — never logged.
type FCMConfig struct {
	ProjectID   string
	ClientEmail string
	PrivateKey  *rsa.PrivateKey
	TokenURL    string // OAuth2 token endpoint; empty defaults to Google's.
	BaseURL     string // FCM send endpoint; empty defaults to Google's.
	now         func() time.Time
}

const (
	fcmGoogleTokenURL = "https://oauth2.googleapis.com/token"
	fcmSendBaseURL    = "https://fcm.googleapis.com"
	fcmScope          = "https://www.googleapis.com/auth/firebase.messaging"
)

// fcmBackend mints an OAuth2 access token from a signed service-account JWT
// (cached until ~5 min before expiry) and POSTs the v1 send request.
type fcmBackend struct {
	cfg    FCMConfig
	client Doer
	now    func() time.Time

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

// NewFCMBackend builds the FCM v1 backend. client defaults to http.DefaultClient.
func NewFCMBackend(cfg FCMConfig, client Doer) *fcmBackend {
	if client == nil {
		client = http.DefaultClient
	}
	now := time.Now
	if cfg.now != nil {
		now = cfg.now
	}
	return &fcmBackend{cfg: cfg, client: client, now: now}
}

func (b *fcmBackend) tokenURL() string {
	if b.cfg.TokenURL != "" {
		return b.cfg.TokenURL
	}
	return fcmGoogleTokenURL
}

func (b *fcmBackend) sendURL() string {
	base := b.cfg.BaseURL
	if base == "" {
		base = fcmSendBaseURL
	}
	return strings.TrimRight(base, "/") + "/v1/projects/" + b.cfg.ProjectID + "/messages:send"
}

// accessTokenFor returns a cached or freshly-minted OAuth2 access token. The
// service-account JWT (RS256) is exchanged at the token endpoint for a bearer
// token the send request uses.
func (b *fcmBackend) accessTokenFor(ctx context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.accessToken != "" && b.now().Before(b.tokenExpiry.Add(-5*time.Minute)) {
		return b.accessToken, nil
	}
	now := b.now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   b.cfg.ClientEmail,
		"scope": fcmScope,
		"aud":   b.tokenURL(),
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	assertion, err := signRS256(b.cfg.PrivateKey, header, claims)
	if err != nil {
		return "", err
	}
	form := "grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=" + assertion
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.tokenURL(), strings.NewReader(form))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	resp, err := b.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fcm oauth: status %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("fcm oauth: empty access token")
	}
	b.accessToken = tok.AccessToken
	b.tokenExpiry = now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	return b.accessToken, nil
}

// Send POSTs the FCM v1 data-message to one registration token.
func (b *fcmBackend) Send(ctx context.Context, token string, payload Payload) error {
	access, err := b.accessTokenFor(ctx)
	if err != nil {
		return err
	}
	// FCM v1 data message: a string-only `data` map carrying the minimized type
	// + event id. No `notification` block (that is the user-visible title/body
	// surface) — the app reads `data` and fetches details, so no content leaks.
	body, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"token": token,
			"data": map[string]string{
				"type":    payload.Type,
				"eventId": payload.EventID,
			},
		},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.sendURL(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "Bearer "+access)
	req.Header.Set("content-type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fcm: status %d", resp.StatusCode)
	}
	return nil
}

// --- Web Push (VAPID + aes128gcm) ------------------------------------------

// WebPushConfig is the operator's VAPID credential set (ADR-0039 §2.5: "Web push
// credentials are also operator-configured"). PrivateKey is the VAPID ECDSA
// signing key; Subject is the mailto:/https: contact the VAPID JWT
// requires. The keys live in the secure store and are parsed by the composition
// root — never logged.
type WebPushConfig struct {
	PrivateKey *ecdsa.PrivateKey
	// PublicKey is the uncompressed (65-byte) P-256 point, base64url, advertised
	// to clients and sent in the VAPID Authorization header.
	PublicKey string
	Subject   string
	now       func() time.Time
	// randReader overrides crypto/rand for the ECDH ephemeral key + salt in
	// tests; nil uses crypto/rand.
	randReader interface{ Read([]byte) (int, error) }
}

// webPushBackend encrypts the payload (RFC 8291 aes128gcm) to the subscription's
// p256dh/auth keys and POSTs it to the subscription endpoint with a VAPID
// (RFC 8292) Authorization header.
type webPushBackend struct {
	cfg    WebPushConfig
	client Doer
	now    func() time.Time
}

// NewWebPushBackend builds the Web Push backend. client defaults to
// http.DefaultClient.
func NewWebPushBackend(cfg WebPushConfig, client Doer) *webPushBackend {
	if client == nil {
		client = http.DefaultClient
	}
	now := time.Now
	if cfg.now != nil {
		now = cfg.now
	}
	return &webPushBackend{cfg: cfg, client: client, now: now}
}

// webPushSubscription is the client-supplied Web Push subscription, JSON-encoded
// into the registration token. endpoint is the push service URL; p256dh and auth
// are the base64url subscription keys (RFC 8291).
type webPushSubscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// Send encrypts and POSTs the minimized payload to one Web Push subscription.
func (b *webPushBackend) Send(ctx context.Context, token string, payload Payload) error {
	var sub webPushSubscription
	if err := json.Unmarshal([]byte(token), &sub); err != nil {
		return fmt.Errorf("webpush: malformed subscription token: %w", err)
	}
	if sub.Endpoint == "" {
		return fmt.Errorf("webpush: subscription has no endpoint")
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	reader := b.cfg.randReader
	if reader == nil {
		reader = rand.Reader
	}
	ciphertext, err := encryptAES128GCM(sub.P256dh, sub.Auth, plaintext, reader)
	if err != nil {
		return err
	}
	auth, err := vapidAuthHeader(b.cfg, sub.Endpoint, b.now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(ciphertext))
	if err != nil {
		return err
	}
	req.Header.Set("content-encoding", "aes128gcm")
	req.Header.Set("content-type", "application/octet-stream")
	req.Header.Set("ttl", "60")
	req.Header.Set("authorization", auth)
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// Push services return 201 Created on accept; some return 200/202.
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("webpush: status %d", resp.StatusCode)
	}
	return nil
}

// vapidAuthHeader builds the RFC 8292 "vapid" Authorization header: a signed
// ES256 JWT (aud = origin of the push endpoint, exp <= 24h, sub = contact) plus
// the server's public key. The header carries no secret — the JWT is signed,
// not the key itself.
func vapidAuthHeader(cfg WebPushConfig, endpoint string, now time.Time) (string, error) {
	origin, err := originOf(endpoint)
	if err != nil {
		return "", err
	}
	header := map[string]string{"alg": "ES256", "typ": "JWT"}
	claims := map[string]any{
		"aud": origin,
		"exp": now.Add(12 * time.Hour).Unix(),
		"sub": cfg.Subject,
	}
	jwt, err := signES256(cfg.PrivateKey, header, claims)
	if err != nil {
		return "", err
	}
	return "vapid t=" + jwt + ", k=" + cfg.PublicKey, nil
}

// --- JWT signing helpers ---------------------------------------------------

var b64url = base64.RawURLEncoding

// signES256 builds and signs a compact ES256 (ECDSA P-256 / SHA-256) JWT. Used
// for APNs provider tokens and VAPID Authorization. The signature is the raw
// r||s big-endian pair (JWS form), not the ASN.1 DER ecdsa.Sign returns.
func signES256(key *ecdsa.PrivateKey, header map[string]string, claims map[string]any) (string, error) {
	if key == nil {
		return "", fmt.Errorf("es256: nil signing key")
	}
	signingInput, err := jwtSigningInput(header, claims)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	// Fixed 32-byte big-endian r and s, left-padded.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signingInput + "." + b64url.EncodeToString(sig), nil
}

// jwtSigningInput encodes "<b64url(header)>.<b64url(claims)>".
func jwtSigningInput(header map[string]string, claims map[string]any) (string, error) {
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return b64url.EncodeToString(h) + "." + b64url.EncodeToString(c), nil
}
