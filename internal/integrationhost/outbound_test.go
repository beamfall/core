package integrationhost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/store"
)

// recordingOutbound records whether the proxy was reached and returns a canned
// response, so tests can prove the audit-before-proxy ordering and the
// per-origin approval gate.
type recordingOutbound struct {
	called bool
	resp   OutboundResponse
	err    error
}

func (r *recordingOutbound) RequestOutbound(_ context.Context, _ string, _ OutboundRequest) (OutboundResponse, error) {
	r.called = true
	return r.resp, r.err
}

// erroringAudit fails the append, exercising the audit-error abort path.
type erroringAudit struct{ err error }

func (e erroringAudit) AppendActivity(context.Context, store.ActivityEntry) error { return e.err }

func outboundGrant(pluginID string) Grant {
	return Grant{PluginID: pluginID, Kind: PermDeclaredOutbound, Approved: true}
}

func hostOutbound(t *testing.T, pluginID string, deps Deps) (*Host, context.Context) {
	t.Helper()
	cap := causation.NewCausationCap(causation.DefaultLimits(), time.Now)
	token, err := cap.TokenFor(t.Name(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	deps.Causation = cap
	return hostWith(pluginID, deps, outboundGrant(pluginID)), causation.WithToken(context.Background(), token)
}

// TestRequestOutboundUngrantedDenied: no grant ⇒ permission denied, proxy never
// reached.
func TestRequestOutboundUngrantedDenied(t *testing.T) {
	ctx := context.Background()
	proxy := &recordingOutbound{}
	h := NewHost("p", Deps{Outbound: proxy, Audit: &recordingAudit{}}, fixedGrants{}, nil)
	if _, err := h.RequestOutbound(ctx, OutboundRequest{Method: "GET", URL: "https://x.test"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ungranted request-outbound: want permission denied, got %v", err)
	}
	if proxy.called {
		t.Fatal("proxy must not be reached without a grant")
	}
}

// TestRequestOutboundDormantSeam: granted but nil Outbound seam ⇒ dormant
// permission-denied, matching every other Core-handoff seam.
func TestRequestOutboundDormantSeam(t *testing.T) {
	h, ctx := hostOutbound(t, "p", Deps{Audit: &recordingAudit{}})
	if _, err := h.RequestOutbound(ctx, OutboundRequest{Method: "GET", URL: "https://x.test"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("nil Outbound seam: want permission denied (dormant), got %v", err)
	}
}

// TestRequestOutboundDoesNotUseProvenanceAsCapability proves the Host's
// authorization inputs are the approved grant and the per-origin policy enforced
// by its proxy, never a first-party/provenance bit.
func TestRequestOutboundDoesNotUseProvenanceAsCapability(t *testing.T) {
	proxy := &recordingOutbound{resp: OutboundResponse{StatusCode: 200}}
	h, ctx := hostOutbound(t, "thirdparty", Deps{Outbound: proxy, Audit: &recordingAudit{}})
	if _, err := h.RequestOutbound(ctx, OutboundRequest{Method: "GET", URL: "https://x.test"}); err != nil {
		t.Fatalf("provenance-neutral outbound: %v", err)
	}
	if !proxy.called {
		t.Fatal("approved request did not reach the policy-enforcing proxy")
	}
}

// TestRequestOutboundSuccess: a plugin with the coarse grant + proxy + audit
// reaches the proxy and returns its response. The audit row carries only the
// plugin id — no URL/host/header/body.
func TestRequestOutboundSuccess(t *testing.T) {
	audit := &recordingAudit{}
	proxy := &recordingOutbound{resp: OutboundResponse{StatusCode: 204, Body: []byte("ok")}}
	h, ctx := hostOutbound(t, "worker", Deps{Outbound: proxy, Audit: audit})

	resp, err := h.RequestOutbound(ctx, OutboundRequest{Method: "POST", URL: "https://api.first.test/x?token=secret", Body: []byte("body")})
	if err != nil {
		t.Fatalf("outbound: %v", err)
	}
	if !proxy.called || resp.StatusCode != 204 {
		t.Fatalf("proxy not reached or wrong response: called=%v resp=%+v", proxy.called, resp)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("want exactly one audit row, got %d", len(audit.entries))
	}
	e := audit.entries[0]
	if e.Action != store.ActivityPluginOutboundRequest || e.TargetType != "plugin" || e.TargetID != "worker" {
		t.Fatalf("audit shape wrong: %+v", e)
	}
	// No secret leakage: the closed ActivityEntry schema has no free-text detail
	// field, and every string field carries only the plugin id — never the URL,
	// query token, or body.
	for _, secret := range []string{"api.first.test", "token=secret", "body"} {
		for _, field := range []string{e.TargetID, e.TargetType, string(e.LibraryID), string(e.ProfileID)} {
			if strings.Contains(field, secret) {
				t.Fatalf("audit entry leaked %q: %+v", secret, e)
			}
		}
	}
}

// TestRequestOutboundNilAuditDenied: outbound fails closed with no ledger — a nil
// Audit seam ⇒ permission-denied and the proxy is never reached (unlike the shared
// auditHighRisk no-op helper).
func TestRequestOutboundNilAuditDenied(t *testing.T) {
	proxy := &recordingOutbound{}
	h, ctx := hostOutbound(t, "worker", Deps{Outbound: proxy})
	if _, err := h.RequestOutbound(ctx, OutboundRequest{Method: "GET", URL: "https://x.test"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("nil audit: want permission denied, got %v", err)
	}
	if proxy.called {
		t.Fatal("proxy must not be reached with a nil audit ledger")
	}
}

// TestRequestOutboundAuditErrorAborts: an AppendActivity error aborts the outbound
// call before the proxy is reached (audit-before-proxy ordering).
func TestRequestOutboundAuditErrorAborts(t *testing.T) {
	proxy := &recordingOutbound{}
	wantErr := errors.New("append failed")
	h, ctx := hostOutbound(t, "worker", Deps{Outbound: proxy, Audit: erroringAudit{err: wantErr}})
	if _, err := h.RequestOutbound(ctx, OutboundRequest{Method: "GET", URL: "https://x.test"}); !errors.Is(err, wantErr) {
		t.Fatalf("audit error: want append error, got %v", err)
	}
	if proxy.called {
		t.Fatal("proxy must not be reached when the audit append fails")
	}
}

// TestRequestOutboundOverCapBodyRejected: a request body over the cap is rejected
// before the ledger/proxy.
func TestRequestOutboundOverCapBodyRejected(t *testing.T) {
	audit := &recordingAudit{}
	proxy := &recordingOutbound{}
	h, ctx := hostOutbound(t, "worker", Deps{Outbound: proxy, Audit: audit})
	big := make([]byte, maxOutboundBodyBytes+1)
	if _, err := h.RequestOutbound(ctx, OutboundRequest{Method: "POST", URL: "https://x.test", Body: big}); err == nil {
		t.Fatal("over-cap request body: want error, got nil")
	}
	if proxy.called || len(audit.entries) != 0 {
		t.Fatal("over-cap request body must be rejected before ledger/proxy")
	}
}

// TestRequestOutboundLargeHeaderResponseRejected: a response whose fully-marshalled
// payload exceeds the frame budget — driven by large *headers* with an in-cap body
// — is rejected, proving headers are counted (not just the body).
func TestRequestOutboundLargeHeaderResponseRejected(t *testing.T) {
	audit := &recordingAudit{}
	hdr := map[string][]string{"X-Big": {strings.Repeat("h", maxOutboundFrameBytes)}}
	proxy := &recordingOutbound{resp: OutboundResponse{StatusCode: 200, Header: hdr, Body: []byte("small")}}
	h, ctx := hostOutbound(t, "worker", Deps{Outbound: proxy, Audit: audit})
	if _, err := h.RequestOutbound(ctx, OutboundRequest{Method: "GET", URL: "https://x.test"}); err == nil {
		t.Fatal("large-header in-cap-body response: want error, got nil")
	}
}

// TestRequestOutboundInCapResponseAccepted: a near-cap-but-in-cap response body is
// accepted.
func TestRequestOutboundInCapResponseAccepted(t *testing.T) {
	body := make([]byte, maxOutboundBodyBytes)
	proxy := &recordingOutbound{resp: OutboundResponse{StatusCode: 200, Body: body}}
	h, ctx := hostOutbound(t, "worker", Deps{Outbound: proxy, Audit: &recordingAudit{}})
	if _, err := h.RequestOutbound(ctx, OutboundRequest{Method: "GET", URL: "https://x.test"}); err != nil {
		t.Fatalf("in-cap response: want success, got %v", err)
	}
}
