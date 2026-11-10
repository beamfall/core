package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// recordingDeadLetter is an in-memory DeadLetterSink.
type recordingDeadLetter struct {
	mu      sync.Mutex
	entries []DeadLetter
}

func (r *recordingDeadLetter) Record(entry DeadLetter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	return nil
}

// capturedRequest is one request a local test server received.
type capturedRequest struct {
	path      string
	signature string
	body      string
}

// captureServer records every request it receives and answers with handle.
type captureServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []capturedRequest
}

func newCaptureServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *captureServer {
	t.Helper()
	c := &captureServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.requests = append(c.requests, capturedRequest{path: r.URL.Path, signature: r.Header.Get(SignatureHeader), body: string(body)})
		c.mu.Unlock()
		handle(w, r)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *captureServer) received() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedRequest(nil), c.requests...)
}

// localhostURL rewrites an httptest URL (http://127.0.0.1:port) to name the
// same listener by hostname, so it must resolve before the dial hook sees it.
func localhostURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = "localhost:" + u.Port()
	return u.String()
}

// TestDefaultClientRefusesRedirectedDelivery is the two-server proof: the
// configured endpoint answers 307/308 pointing at a second local origin (by
// literal and by hostname) or a private/metadata target, and the default
// client never forwards the event body or signature there. Each attempt is a
// failed delivery that exhausts the bounded budget into the dead-letter.
func TestDefaultClientRefusesRedirectedDelivery(t *testing.T) {
	other := newCaptureServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	targets := map[string]string{
		"other origin":          other.URL + "/collect",
		"other origin by name":  localhostURL(t, other.URL) + "/collect",
		"cloud metadata target": "http://169.254.169.254/latest/meta-data/",
		"private target":        "http://10.0.0.1/admin",
	}
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for name, target := range targets {
			t.Run(http.StatusText(status)+"/"+name, func(t *testing.T) {
				configured := newCaptureServer(t, func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, target, status)
				})
				dl := &recordingDeadLetter{}
				em := newEmitter(t, Config{
					Endpoints:   []Endpoint{{ID: "hook", URL: configured.URL + "/hook", Secret: []byte("hmac-secret")}},
					MaxAttempts: 2,
				}, &fixedEvents{events: []store.OutboxEvent{mediaEvent(3, store.MediaEventCreated)}}, nil, dl)
				before := len(other.received())

				if _, err := em.drain(context.Background(), 0); err != nil {
					t.Fatalf("drain: %v", err)
				}

				if got := len(other.received()) - before; got != 0 {
					t.Fatalf("redirect target received %d requests; want none", got)
				}
				hits := configured.received()
				if len(hits) != 2 {
					t.Fatalf("configured endpoint received %d attempts, want 2 (MaxAttempts)", len(hits))
				}
				for _, hit := range hits {
					if hit.path != "/hook" || hit.signature == "" {
						t.Fatalf("configured endpoint got %+v, want signed POST to /hook", hit)
					}
				}
				if len(dl.entries) != 1 || dl.entries[0].LastError != ErrStatusDiagnostic.Error() || dl.entries[0].Attempts != 2 {
					t.Fatalf("dead-letter = %+v, want one status-diagnostic entry after 2 attempts", dl.entries)
				}
			})
		}
	}
}

// TestDefaultClientDeliversToApprovedLANReceivers: a loopback/private literal
// endpoint, and a hostname endpoint with AllowPrivateNetwork, still receive a
// correctly signed delivery through the default client.
func TestDefaultClientDeliversToApprovedLANReceivers(t *testing.T) {
	secret := []byte("lan-secret")
	srv := newCaptureServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	cases := map[string]Endpoint{
		"loopback literal":           {ID: "lan", URL: srv.URL + "/api/webhook/x", Secret: secret},
		"hostname with LAN approval": {ID: "lan", URL: localhostURL(t, srv.URL) + "/api/webhook/x", Secret: secret, AllowPrivateNetwork: true},
	}
	for name, ep := range cases {
		t.Run(name, func(t *testing.T) {
			before := len(srv.received())
			dl := &recordingDeadLetter{}
			em := newEmitter(t, Config{Endpoints: []Endpoint{ep}, MaxAttempts: 1},
				&fixedEvents{events: []store.OutboxEvent{mediaEvent(9, store.MediaEventCreated)}}, nil, dl)
			if _, err := em.drain(context.Background(), 0); err != nil {
				t.Fatalf("drain: %v", err)
			}
			hits := srv.received()[before:]
			if len(hits) != 1 || len(dl.entries) != 0 {
				t.Fatalf("deliveries = %d, dead-letters = %+v; want one delivery", len(hits), dl.entries)
			}
			mac := hmac.New(sha256.New, secret)
			mac.Write([]byte(hits[0].body))
			if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); hits[0].signature != want {
				t.Fatalf("signature = %q, want %q", hits[0].signature, want)
			}
		})
	}
}

// TestDefaultClientRefusesUnapprovedLocalDial: a hostname endpoint without the
// LAN opt-in that resolves to loopback is refused at the dial boundary, so a
// public-looking endpoint that re-resolves inward never receives the POST.
func TestDefaultClientRefusesUnapprovedLocalDial(t *testing.T) {
	srv := newCaptureServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	dl := &recordingDeadLetter{}
	em := newEmitter(t, Config{
		Endpoints:   []Endpoint{{ID: "named", URL: localhostURL(t, srv.URL), Secret: []byte("s")}},
		MaxAttempts: 2,
	}, &fixedEvents{events: []store.OutboxEvent{mediaEvent(1, store.MediaEventCreated)}}, nil, dl)
	if _, err := em.drain(context.Background(), 0); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := len(srv.received()); got != 0 {
		t.Fatalf("unapproved local receiver got %d requests, want none", got)
	}
	if len(dl.entries) != 1 || dl.entries[0].LastError != ErrTransportDiagnostic.Error() {
		t.Fatalf("dead-letter = %+v, want one transport-diagnostic entry", dl.entries)
	}
}

func TestCheckDialAddressClasses(t *testing.T) {
	refused := []string{
		"0.0.0.0:80", "0.1.2.3:80", "[::]:80", "169.254.169.254:80", "[fe80::1]:80",
		"[fe80::1%eth0]:80", "224.0.0.251:5353", "[ff02::1]:80", "255.255.255.255:80",
		"[::ffff:169.254.169.254]:80", "not-an-address", "example.com:443",
	}
	local := []string{
		"127.0.0.1:8123", "[::1]:8123", "10.0.0.5:80", "172.16.0.1:80", "192.168.1.10:8123",
		"100.64.0.1:80", "[fd00::1]:80", "[fec0::1]:80", "[::ffff:192.168.1.10]:80",
	}
	public := []string{"93.184.216.34:443", "[2606:4700:4700::1111]:443"}
	for _, allowLocal := range []bool{false, true} {
		for _, address := range refused {
			if err := checkDialAddress(address, allowLocal); err == nil {
				t.Errorf("allowLocal=%v: dial to %s allowed, want refusal", allowLocal, address)
			}
		}
		for _, address := range local {
			err := checkDialAddress(address, allowLocal)
			if allowLocal && err != nil {
				t.Errorf("approved endpoint: dial to %s refused: %v", address, err)
			}
			if !allowLocal && err == nil {
				t.Errorf("unapproved endpoint: dial to local %s allowed", address)
			}
		}
		for _, address := range public {
			if err := checkDialAddress(address, allowLocal); err != nil {
				t.Errorf("allowLocal=%v: dial to public %s refused: %v", allowLocal, address, err)
			}
		}
	}
	if classify(netip.MustParseAddr("8.8.8.8")) != classOther {
		t.Error("ordinary public address not classOther")
	}
}

// countingDoer counts requests; validation failures must never reach it.
type countingDoer struct{ calls int }

func (c *countingDoer) Do(*http.Request) (*http.Response, error) {
	c.calls++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestEndpointURLValidation(t *testing.T) {
	unsafe := map[string]string{
		"no scheme":         "hooks.example.com/x",
		"relative":          "/hook",
		"ftp scheme":        "ftp://hooks.example.com/x",
		"file scheme":       "file:///etc/passwd",
		"javascript":        "javascript:alert(1)",
		"opaque":            "http:hooks.example.com",
		"no host":           "http:///hook",
		"empty host port":   "http://:8080/hook",
		"user info":         "https://user:pass@hooks.example.com/x",
		"metadata literal":  "http://169.254.169.254/latest",
		"link-local v6":     "http://[fe80::1]/x",
		"zoned literal":     "http://[fe80::1%25eth0]/x",
		"unspecified":       "http://0.0.0.0:8080/x",
		"unspecified v6":    "http://[::]:8080/x",
		"multicast":         "http://239.1.1.1/x",
		"broadcast":         "http://255.255.255.255/x",
		"malformed escape":  "http://%gh.example.invalid/x",
		"mapped link-local": "http://[::ffff:169.254.169.254]/x",
	}
	for name, raw := range unsafe {
		t.Run("unsafe/"+name, func(t *testing.T) {
			doer := &countingDoer{}
			em, err := New(Config{Endpoints: []Endpoint{{ID: "a", URL: raw, Secret: []byte("s")}}}, &fixedEvents{}, doer, nil, nil)
			if err != ErrConfigDiagnostic || em != nil {
				t.Fatalf("New(%q) = %v, %v; want nil, configuration diagnostic", raw, em, err)
			}
			if doer.calls != 0 {
				t.Fatalf("validation failure still sent %d requests", doer.calls)
			}
		})
	}
	allowed := map[string]bool{ // URL -> approves local classes
		"https://hooks.example.com/x?token=t":    false,
		"HTTPS://hooks.example.com/x":            false,
		"http://homeassistant.local:8123/api/x":  false,
		"http://192.168.1.10:8123/api/webhook/x": true,
		"http://127.0.0.1:8123/x":                true,
		"http://[fd00::1]:8123/x":                true,
		"http://100.64.0.7/x":                    true,
		"https://93.184.216.34/x":                false,
	}
	for raw, wantLocal := range allowed {
		allowLocal, err := endpointAllowsLocal(Endpoint{URL: raw})
		if err != nil || allowLocal != wantLocal {
			t.Errorf("endpointAllowsLocal(%q) = %v, %v; want %v, nil", raw, allowLocal, err, wantLocal)
		}
		if _, err := New(Config{Endpoints: []Endpoint{{ID: "a", URL: raw, Secret: []byte("s")}}}, &fixedEvents{}, nil, nil, nil); err != nil {
			t.Errorf("New rejected safe URL %q: %v", raw, err)
		}
	}
	if allowLocal, err := endpointAllowsLocal(Endpoint{URL: "http://homeassistant.local:8123", AllowPrivateNetwork: true}); err != nil || !allowLocal {
		t.Errorf("AllowPrivateNetwork did not approve a hostname endpoint: %v, %v", allowLocal, err)
	}
}

func TestLoadConfigReadsAllowPrivateNetwork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook.json")
	data := `{"endpoints":[` +
		`{"id":"ha","url":"http://homeassistant.local:8123/api/webhook/x","secret_ref":"ha","allow_private_network":true},` +
		`{"id":"ntfy","url":"https://ntfy.example.com/beamfall","secret_ref":"ntfy"}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, staticSecrets{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Endpoints) != 2 || !cfg.Endpoints[0].AllowPrivateNetwork || cfg.Endpoints[1].AllowPrivateNetwork {
		t.Fatalf("endpoints = %+v, want only ha approved for the private network", cfg.Endpoints)
	}
}

type staticSecrets struct{}

func (staticSecrets) Secret(ref string) (string, error) { return "secret-for-" + ref, nil }
