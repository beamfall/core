package push

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
)

// Payload is the ENTIRE dispatched push body (ADR-0039 §2.5 payload
// minimization, load-bearing). It carries only a notification Type (a
// closed-vocabulary classifier the client maps to a localized label) and a
// server-issued opaque EventID. On receipt the client fetches the event's
// details from the server over the authenticated API — no media title, no
// playback position, no library content, no Profile name ever rides the push.
//
// This struct has no field for content by construction: a reviewer adding a
// "title" or "body" here would be making the minimization violation explicit
// rather than slipping it in. TestPayloadCarriesOnlyTypeAndEventID asserts the
// marshaled wire bytes contain nothing else.
type Payload struct {
	// Type is the notification class, e.g. "extension_request" or
	// "watch_party_invite". Closed vocabulary, no content.
	Type string `json:"type"`
	// EventID is the server-issued opaque handle the client round-trips back to
	// fetch details over the authenticated API. Opaque to the transport.
	EventID string `json:"eventId"`
}

// Backend sends one already-minimized Payload to one device token over a single
// transport (APNs / FCM / Web Push). It is the common seam the Dispatcher routes
// to by platform; each implementation owns its signing and its HTTP endpoint.
// The endpoint is injectable (baseURL on each backend) so tests drive it against
// an httptest server instead of the real APNs/FCM/push service.
type Backend interface {
	// Send delivers payload to token. It returns an error on a transport failure
	// or a non-success response; the Dispatcher logs and continues to the next
	// device (best-effort fan-out — one dead token must not block the rest).
	Send(ctx context.Context, token string, payload Payload) error
}

// Doer is the HTTP client seam (interface at the consumer). *http.Client
// satisfies it; tests inject a stub or point a real client at httptest.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Dispatcher routes a Payload to the right Backend by a token's platform. It is
// built from whatever backends the operator has configured credentials for; an
// operator who configures none gets a dormant dispatcher (Configured() == false)
// that never sends. The token-to-platform mapping comes from the persisted
// device_push_token rows (store.DevicePushTokensForProfile) at the composition
// root — the Dispatcher itself takes (token, platform) pairs so it stays free of
// the store import (invariant: push owns its seams).
type Dispatcher struct {
	backends map[PushPlatform]Backend
}

// NewDispatcher builds a dispatcher from the configured backends. A nil or empty
// map yields a dormant dispatcher (the default-off case): Configured() reports
// false and SendTo refuses with ErrPushNotConfigured. Backends are added only
// for platforms whose operator credentials are present (see the composition
// root), so a server with an APNs key but no FCM service-account can dispatch to
// Apple devices and honestly refuses Android ones.
func NewDispatcher(backends map[PushPlatform]Backend) *Dispatcher {
	return &Dispatcher{backends: backends}
}

// Configured reports whether any backend is wired — the opt-in gate. A dormant
// dispatcher (no credentials configured) reports false so the composition root
// can skip wiring it and clients fall back to the polled inbox.
func (d *Dispatcher) Configured() bool {
	return d != nil && len(d.backends) > 0
}

// ErrPushNotConfigured is returned by SendTo when no backend is configured for
// the requested platform (the dispatcher, or that transport, is dormant). It is
// a stable sentinel so a caller can treat "push off" distinctly from a transport
// failure and fall back to the inbox.
type ErrPushNotConfigured struct{ Platform PushPlatform }

func (e ErrPushNotConfigured) Error() string {
	if e.Platform == "" {
		return "push dispatch not configured"
	}
	return fmt.Sprintf("push dispatch not configured for platform %q", e.Platform)
}

// SendTo dispatches one payload to one device, routing to the backend for the
// device's platform. It returns ErrPushNotConfigured when no backend serves that
// platform (dormant), ErrInvalidPlatform for an unknown platform, or the
// backend's error on a send failure.
func (d *Dispatcher) SendTo(ctx context.Context, platform PushPlatform, token string, payload Payload) error {
	if !platform.valid() {
		return ErrInvalidPlatform{}
	}
	if d == nil {
		return ErrPushNotConfigured{Platform: platform}
	}
	backend, ok := d.backends[platform]
	if !ok {
		return ErrPushNotConfigured{Platform: platform}
	}
	return backend.Send(ctx, token, payload)
}

// Target is one device the dispatcher fans out to — its platform and token. The
// composition root builds these from the persisted device_push_token rows.
type Target struct {
	Platform PushPlatform
	Token    string
}

// Fanout dispatches one payload to every target, best-effort: a failure to one
// device (dead token, dormant transport) is collected but does not stop the
// rest, because ADR-0039 §2.3 fans a single event out to all of a Profile's
// devices and one stale token must not suppress the others. It returns the
// joined errors (nil when every send succeeded), so the caller can log them
// without treating partial delivery as total failure.
func (d *Dispatcher) Fanout(ctx context.Context, targets []Target, payload Payload) error {
	// Sends run concurrently: each is a full APNs/FCM/Web-Push round-trip
	// (up to the backend's 30s timeout), and the events fanned out here are
	// latency-sensitive — one dead endpoint must not serially delay the rest.
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			if err := d.SendTo(ctx, t.Platform, t.Token, payload); err != nil {
				errs[i] = fmt.Errorf("platform %s: %w", t.Platform, err)
			}
		}(i, t)
	}
	wg.Wait()
	return errors.Join(errs...)
}
