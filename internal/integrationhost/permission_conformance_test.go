package integrationhost

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// TestPermissionKindHasDispatchMethod is the conformance guard CAUDIT-7 adds:
// every permission in the closed Core vocabulary must bind to a Host API
// dispatch method. A declared-but-undispatched permission (status-write /
// profile-link before this ticket) is a silent "unknown method" for a worker
// that holds the grant — exactly the trap this test would have caught.
func TestPermissionKindHasDispatchMethod(t *testing.T) {
	for _, kind := range plugin.PermissionKinds() {
		method, ok := DispatchMethodFor(kind)
		if !ok {
			t.Errorf("permission %q has no Host API dispatch method (silent unknown-method trap)", kind)
			continue
		}
		if method == "" {
			t.Errorf("permission %q maps to an empty dispatch method", kind)
		}
	}
	// Reciprocal: no dispatch binding may name an unknown permission, so the map
	// can never drift to gate a scope outside the closed vocabulary.
	for kind := range permissionDispatch {
		if !IsKnownPermission(kind) {
			t.Errorf("dispatch map gates unknown permission %q (not in the closed vocabulary)", kind)
		}
	}
	// The dispatch surface and the gated set must be the same size, so neither a
	// missing nor an extra binding slips through the two loops above.
	if len(permissionDispatch) != len(knownPermissions) {
		t.Errorf("dispatch map has %d entries, vocabulary has %d: they must match exactly",
			len(permissionDispatch), len(knownPermissions))
	}
}

// TestEveryDispatchMethodReachable proves every method named in permissionDispatch
// is actually routed by dispatch() — a worker calling it never hits the
// "unknown method" default. Each call is made without a grant, so a routed
// method fails permission-denied (not "unknown method"); an unrouted method
// would return "unknown method", failing the assertion.
func TestEveryDispatchMethodReachable(t *testing.T) {
	ctx := context.Background()
	s := &Supervisor{host: hostWith("p", Deps{})}
	for kind, method := range permissionDispatch {
		resp := s.dispatch(ctx, hostRequest{Method: method})
		if resp.Err == "unknown method" {
			t.Errorf("permission %q method %q is not routed by dispatch (silent unknown-method)", kind, method)
		}
	}
}

// TestWriteStatusDispatch proves status-write is gated and dormant-when-unwired:
// denied without a grant, denied with a grant but nil Status seam (the
// composition-root posture), and reaches the seam when both are present.
func TestWriteStatusDispatch(t *testing.T) {
	ctx := context.Background()

	denied := hostWith("p", Deps{})
	if err := denied.WriteStatus(ctx, "ok", "all good"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("status-write without grant: want permission denied, got %v", err)
	}

	dormant := hostWith("p", Deps{}, Grant{PluginID: "p", Kind: PermStatusWrite, Approved: true})
	if err := dormant.WriteStatus(ctx, "ok", "all good"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("status-write with grant but nil seam: want permission denied (dormant), got %v", err)
	}

	rec := &recordingStatus{}
	wired := hostWith("p", Deps{Status: rec}, Grant{PluginID: "p", Kind: PermStatusWrite, Approved: true})
	if err := wired.WriteStatus(ctx, "deadletter", "retry later"); err != nil {
		t.Fatalf("status-write wired: %v", err)
	}
	if rec.kind != "deadletter" || rec.detail != "retry later" || rec.pluginID != "p" {
		t.Fatalf("status seam got %+v, want plugin=p kind=deadletter detail='retry later'", rec)
	}
}

// TestLinkProfileDispatch proves profile-link is high-risk (audited) and
// dormant-when-unwired, mirroring the request-ingest high-risk shape.
func TestLinkProfileDispatch(t *testing.T) {
	ctx := context.Background()

	denied := hostWith("p", Deps{})
	if err := denied.LinkProfile(ctx, "prof", "acct"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("profile-link without grant: want permission denied, got %v", err)
	}

	// Declared but unapproved high-risk grant denies (approval is the gate).
	unapproved := hostWith("p", Deps{Linker: &recordingLinker{}},
		Grant{PluginID: "p", Kind: PermProfileLink, Approved: false})
	if err := unapproved.LinkProfile(ctx, "prof", "acct"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("profile-link unapproved high-risk: want permission denied, got %v", err)
	}

	rec := &recordingLinker{}
	audit := &recordingAudit{}
	wired := hostWith("p", Deps{Linker: rec, Audit: audit},
		Grant{PluginID: "p", Kind: PermProfileLink, Approved: true})
	if err := wired.LinkProfile(ctx, "prof", "acct"); err != nil {
		t.Fatalf("profile-link wired: %v", err)
	}
	if rec.profileID != "prof" || rec.ref != "acct" || rec.pluginID != "p" {
		t.Fatalf("linker seam got %+v, want plugin=p profile=prof ref=acct", rec)
	}
	if len(audit.entries) != 1 ||
		audit.entries[0].Action != store.ActivityPluginProfileLink ||
		audit.entries[0].ProfileID != "prof" {
		t.Fatalf("profile-link must write one audit record %q for prof, got %+v", store.ActivityPluginProfileLink, audit.entries)
	}
}

type recordingStatus struct {
	pluginID, kind, detail string
}

func (r *recordingStatus) WriteStatus(_ context.Context, pluginID, kind, detail string) error {
	r.pluginID, r.kind, r.detail = pluginID, kind, detail
	return nil
}

type recordingLinker struct {
	pluginID, ref string
	profileID     store.ProfileID
}

func (r *recordingLinker) LinkProfile(_ context.Context, pluginID string, profileID store.ProfileID, ref string) error {
	r.pluginID, r.profileID, r.ref = pluginID, profileID, ref
	return nil
}
