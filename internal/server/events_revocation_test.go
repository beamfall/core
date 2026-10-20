package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// TestEventStreamTerminatesOnDeviceRevocation is the SEC-4 live-transport proof:
// when a device key is revoked, that device's open event-stream
// subscription stops delivering within one poll cycle (the per-event scope
// re-evaluation in eventStream sees device.Revoked and ends the stream), while a
// different, unrevoked device keeps streaming. This is the subscription /
// deviceBus half of revocation propagation — the transcode-abort half is proven
// in the graph package (TestRevokeDeviceAbortsTranscodesForThatDeviceOnly).
//
// The stream re-derives the Scope every eventPollInterval (250ms); both clients
// must observe the first event before revocation and the second only on the
// unrevoked client after it. A real httptest.Server + streaming HTTP client is
// used so the two long-lived streams run concurrently and partial output is
// observable, which a ResponseRecorder cannot model.
func TestEventStreamTerminatesOnDeviceRevocation(t *testing.T) {
	// feature:sse-events
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	// A second, unrevoked viewer device (distinct device id) is the control: it
	// must keep streaming after the first device is revoked.
	if err := st.EnrollDevice(ctx, store.Device{
		ID: "device-tv2", Name: "TV2", Role: store.DeviceRoleViewer,
		SecretHash: store.HashDeviceSecret("dev-tv2-secret"),
	}, store.Session{
		ID: "session-tv2", DeviceID: "device-tv2", ProfileID: "profile-default",
	}); err != nil {
		t.Fatalf("enroll control device: %v", err)
	}

	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	// open connects an SSE stream for one device and returns a channel of the
	// event: lines it receives, plus a cancel to drop the connection.
	open := func(session, device, secret string) (<-chan string, context.CancelFunc) {
		streamCtx, cancel := context.WithCancel(ctx)
		req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, httpSrv.URL+"/api/events", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("X-Beamfall-Session", session)
		req.Header.Set("X-Beamfall-Device", device)
		req.Header.Set("X-Beamfall-Device-Secret", secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Fatalf("connect %s: %v", device, err)
		}
		if resp.StatusCode != http.StatusOK {
			cancel()
			t.Fatalf("connect %s status = %d", device, resp.StatusCode)
		}
		lines := make(chan string, 64)
		go func() {
			defer resp.Body.Close()
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				if rest, ok := strings.CutPrefix(scanner.Text(), "event: "); ok {
					select {
					case lines <- rest:
					case <-streamCtx.Done():
						return
					}
				}
			}
		}()
		return lines, cancel
	}

	revoked, cancelRevoked := open("session-default", "device-tv", "dev-tv-secret")
	defer cancelRevoked()
	control, cancelControl := open("session-tv2", "device-tv2", "dev-tv2-secret")
	defer cancelControl()

	// Event #1: both unrevoked streams must receive it (proves both are live).
	if err := st.AppendMediaEvent(ctx, "media.updated", "media-alpha", nil, now); err != nil {
		t.Fatalf("append event 1: %v", err)
	}
	waitForEvent(t, revoked, "media.updated", "pre-revoke event on revoked-to-be stream")
	waitForEvent(t, control, "media.updated", "pre-revoke event on control stream")

	// Revoke the first device's key MID-STREAM. The next eventStream poll
	// re-derives the Scope, sees device.Revoked, and ends that stream — without
	// touching any other device's stream.
	if err := st.RevokeDevice(ctx, "device-tv"); err != nil {
		t.Fatalf("revoke device: %v", err)
	}

	// Event #2: only the control device may receive it. Give the revoked stream
	// more than one poll cycle to (not) deliver it.
	if err := st.AppendMediaEvent(ctx, "media.created", "media-alpha", nil, now); err != nil {
		t.Fatalf("append event 2: %v", err)
	}
	waitForEvent(t, control, "media.created", "post-revoke event on control stream")
	assertNoEvent(t, revoked, "media.created", 750*time.Millisecond)
}

// waitForEvent blocks until the named SSE event type arrives on the channel or
// the test deadline elapses.
func waitForEvent(t *testing.T, ch <-chan string, want, ctxLabel string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("%s: stream closed before %q arrived", ctxLabel, want)
			}
			if ev == want {
				return
			}
		case <-deadline:
			t.Fatalf("%s: timed out waiting for %q", ctxLabel, want)
		}
	}
}

// assertNoEvent fails if the named event arrives within the window — the
// revoked stream must not deliver post-revocation events.
func assertNoEvent(t *testing.T, ch <-chan string, unwanted string, window time.Duration) {
	t.Helper()
	deadline := time.After(window)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return // stream ended (revocation terminated it) — the contract
			}
			if ev == unwanted {
				t.Fatalf("revoked stream delivered %q after revocation", unwanted)
			}
		case <-deadline:
			return // no unwanted event within the window
		}
	}
}
