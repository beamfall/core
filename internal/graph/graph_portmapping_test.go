package graph

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// stubPortMapping is a test PortMappingController recording what the graph
// asked for.
type stubPortMapping struct {
	mu      sync.Mutex
	status  PortMappingStatus
	report  *ReachabilityReport
	sets    []bool
	checks  int
	verdict ReachabilityReport
}

func (s *stubPortMapping) PortMapping(context.Context) PortMappingStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *stubPortMapping) SetPortMapping(_ context.Context, enabled bool) PortMappingStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sets = append(s.sets, enabled)
	if enabled {
		s.status = PortMappingStatus{Enabled: true, State: "mapped", Protocol: "natpmp", InternalPort: 8443, ExternalPort: 18443, ExternalAddress: "203.0.113.7", LeaseSeconds: 7200}
	} else {
		s.status = PortMappingStatus{State: "disabled", InternalPort: 8443}
	}
	return s.status
}

func (s *stubPortMapping) CheckReachability(context.Context) ReachabilityReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks++
	r := s.verdict
	s.report = &r
	return r
}

func (s *stubPortMapping) LastReachability() (ReachabilityReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.report == nil {
		return ReachabilityReport{}, false
	}
	return *s.report, true
}

const setPortMappingMutation = `{"query":"mutation($enabled: Boolean!){ setRemoteAccessPortMapping(enabled:$enabled){ portMapping { enabled state protocol internalPort externalPort externalAddress leaseSeconds reason } reachability { checkedAt verdicts { path reachable reason } } } }","variables":{"enabled":%s}}`

func portMappingRequest(enabled string) string {
	return strings.Replace(setPortMappingMutation, "%s", enabled, 1)
}

// TestPortMappingMutationsAdminGated proves both CORE-1969 mutations sit
// behind the Admin-Session gate like the rest of remote access.
func TestPortMappingMutationsAdminGated(t *testing.T) {
	handler := newTestHandler(t)
	handler.SetPortMapping(&stubPortMapping{})
	for _, body := range []string{
		portMappingRequest("true"),
		`{"query":"mutation { checkRemoteAccessReachability { portMapping { state } } }"}`,
	} {
		_, errs := post(t, handler, body, authorizeManager)
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
			t.Fatalf("non-admin errors = %#v, want admin-session gate", errs)
		}
	}
}

// TestPortMappingUnavailableWithoutController proves a server without the
// seam refuses the toggle and persists nothing.
func TestPortMappingUnavailableWithoutController(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	_, errs := post(t, handler, portMappingRequest("true"), authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unavailable") {
		t.Fatalf("errors = %#v, want unavailable", errs)
	}
	if on, err := st.PortMappingEnabled(context.Background()); err != nil || on {
		t.Fatalf("PortMappingEnabled = %v, %v after a refused toggle", on, err)
	}
	data, errs := post(t, handler, `{"query":"query { remoteAccess { portMapping { state } } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("remoteAccess errors = %#v", errs)
	}
	var info RemoteAccessInfo
	mustUnmarshal(t, data["remoteAccess"], &info)
	if info.PortMapping != nil {
		t.Fatalf("portMapping present without a controller: %#v", info.PortMapping)
	}
}

// TestSetRemoteAccessPortMappingPersistsAppliesAndAudits proves the toggle
// persists the opt-in, drives the controller, runs one self-test, writes an
// audit row, and returns the mapped port, external address and verdicts.
func TestSetRemoteAccessPortMappingPersistsAppliesAndAudits(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	ctrl := &stubPortMapping{verdict: ReachabilityReport{
		CheckedAt: "2026-11-15T10:00:00Z",
		Verdicts: []ReachabilityVerdict{
			{Path: "direct", Reachable: false, Reason: "timeout"},
			{Path: "relay", Reachable: false, Reason: "relay_not_configured"},
		},
	}}
	handler.SetPortMapping(ctrl)

	data, errs := post(t, handler, portMappingRequest("true"), authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setRemoteAccessPortMapping errors = %#v", errs)
	}
	var info RemoteAccessInfo
	mustUnmarshal(t, data["setRemoteAccessPortMapping"], &info)
	if info.PortMapping == nil || info.PortMapping.State != "mapped" || info.PortMapping.ExternalPort != 18443 || info.PortMapping.ExternalAddress != "203.0.113.7" {
		t.Fatalf("portMapping = %#v", info.PortMapping)
	}
	if info.Reachability == nil || len(info.Reachability.Verdicts) != 2 || info.Reachability.Verdicts[0].Reason != "timeout" {
		t.Fatalf("reachability = %#v", info.Reachability)
	}
	if len(ctrl.sets) != 1 || !ctrl.sets[0] || ctrl.checks != 1 {
		t.Fatalf("controller sets=%v checks=%d", ctrl.sets, ctrl.checks)
	}
	if on, err := st.PortMappingEnabled(context.Background()); err != nil || !on {
		t.Fatalf("PortMappingEnabled = %v, %v; want persisted true", on, err)
	}

	if _, errs := post(t, handler, portMappingRequest("false"), authorizeManager); len(errs) != 0 {
		t.Fatalf("disable errors = %#v", errs)
	}
	if on, _ := st.PortMappingEnabled(context.Background()); on {
		t.Fatal("PortMappingEnabled still true after disable")
	}
	if len(ctrl.sets) != 2 || ctrl.sets[1] {
		t.Fatalf("controller sets = %v, want [true false]", ctrl.sets)
	}

	entries, err := st.ActivityEntriesAfter(context.Background(), 0, []string{store.ActivityConfigUpdate}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, entry := range entries {
		if entry.TargetType == "remote-access-port-mapping" {
			targets = append(targets, entry.TargetID)
		}
	}
	if strings.Join(targets, ",") != "enabled,disabled" {
		t.Fatalf("audit rows = %v, want enabled then disabled", targets)
	}
}

// TestSetRemoteAccessPortMappingRequiresEnabled proves the variable is
// mandatory.
func TestSetRemoteAccessPortMappingRequiresEnabled(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	handler.SetPortMapping(&stubPortMapping{})
	_, errs := post(t, handler, `{"query":"mutation { setRemoteAccessPortMapping { portMapping { state } } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "enabled is required") {
		t.Fatalf("errors = %#v, want enabled is required", errs)
	}
}

// TestCheckRemoteAccessReachability proves the on-demand self-test runs once
// and its report shows on the remoteAccess read afterwards.
func TestCheckRemoteAccessReachability(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	ctrl := &stubPortMapping{
		status: PortMappingStatus{State: "disabled", InternalPort: 8443},
		verdict: ReachabilityReport{CheckedAt: "2026-11-15T10:00:00Z", Verdicts: []ReachabilityVerdict{
			{Path: "direct", Reason: "not_mapped"},
			{Path: "relay", Reachable: true},
		}},
	}
	handler.SetPortMapping(ctrl)

	data, errs := post(t, handler, `{"query":"query { remoteAccess { portMapping { state } reachability { checkedAt } } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("remoteAccess errors = %#v", errs)
	}
	var before RemoteAccessInfo
	mustUnmarshal(t, data["remoteAccess"], &before)
	if before.PortMapping == nil || before.PortMapping.State != "disabled" || before.Reachability != nil {
		t.Fatalf("before = %#v", before)
	}

	data, errs = post(t, handler, `{"query":"mutation { checkRemoteAccessReachability { reachability { checkedAt verdicts { path reachable reason } } } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("checkRemoteAccessReachability errors = %#v", errs)
	}
	var after RemoteAccessInfo
	mustUnmarshal(t, data["checkRemoteAccessReachability"], &after)
	if ctrl.checks != 1 || after.Reachability == nil || !after.Reachability.Verdicts[1].Reachable || after.Reachability.Verdicts[0].Reason != "not_mapped" {
		t.Fatalf("after = %#v checks=%d", after.Reachability, ctrl.checks)
	}
}
