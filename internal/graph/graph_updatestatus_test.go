package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeUpdateChecker records whether Check was called and returns a canned
// result whose state token and error are set per test, so the graph test can
// assert the operator gate, the default-off "no call when disabled" contract,
// and every state the resolver must report — all without any network.
type fakeUpdateChecker struct {
	called  bool
	channel string
	state   string
	err     error
}

func (f *fakeUpdateChecker) Check(_ context.Context, enabled bool, channel, currentVersion string) (UpdateStatusInfo, error) {
	f.called = true
	f.channel = channel
	if f.err != nil {
		return UpdateStatusInfo{}, f.err
	}
	return UpdateStatusInfo{
		State:           f.state,
		Enabled:         enabled,
		Channel:         channel,
		CurrentVersion:  currentVersion,
		LatestVersion:   "9.9.9",
		UpdateAvailable: true,
	}, nil
}

// TestUpdateStatusAdminGate proves the updateStatus field is Admin-Session-gated
// (ADR-0013 §7 Console operation class): a non-admin session is refused.
func TestUpdateStatusAdminGate(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	handler.SetUpdateChecker(&fakeUpdateChecker{}, "1.0.0")

	_, errs := post(t, handler, `{"query":"query { updateStatus { enabled updateAvailable } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("non-admin updateStatus errors = %#v, want admin-session gate", errs)
	}
}

// TestUpdateStatusDefaultOff proves the default-off contract: with no opt-in
// stored, the resolver reports enabled=false and NEVER calls the checker (no
// outbound request; invariant #5).
func TestUpdateStatusDefaultOff(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	checker := &fakeUpdateChecker{}
	handler.SetUpdateChecker(checker, "1.0.0")
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { updateStatus { state enabled channel currentVersion latestVersion updateAvailable } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("updateStatus errors = %#v", errs)
	}
	var status UpdateStatusInfo
	mustUnmarshal(t, data["updateStatus"], &status)
	if status.Enabled {
		t.Fatalf("status.Enabled = true, want false (default-off)")
	}
	if checker.called {
		t.Fatal("checker was called while disabled — default-off must make NO outbound request")
	}
	if status.UpdateAvailable {
		t.Fatalf("status.UpdateAvailable = true, want false when off")
	}
	if status.CurrentVersion != "1.0.0" {
		t.Fatalf("status.CurrentVersion = %q, want 1.0.0", status.CurrentVersion)
	}
	if status.Channel != "stable" {
		t.Fatalf("status.Channel = %q, want default stable", status.Channel)
	}
	if status.State != UpdateStateDisabled {
		t.Fatalf("status.State = %q, want %q when not opted in", status.State, UpdateStateDisabled)
	}
}

// TestUpdateStatusEnabledDelegates proves that once the operator opts in (stored
// in the existing console config — no schema migration), the resolver delegates
// to the checker and surfaces the update-available signal on the chosen channel.
func TestUpdateStatusEnabledDelegates(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	checker := &fakeUpdateChecker{state: UpdateStateUpdateAvailable}
	handler.SetUpdateChecker(checker, "1.0.0")
	openManagerAdminSession(t, st)

	// Opt in via the existing console-config write (beta channel).
	enabled := true
	channel := "beta"
	if _, err := st.SetConsoleConfig(context.Background(),
		store.ConsoleConfigPatch{UpdateCheckEnabled: &enabled, UpdateCheckChannel: &channel},
		time.Unix(1700, 0).UTC()); err != nil {
		t.Fatalf("SetConsoleConfig: %v", err)
	}

	data, errs := post(t, handler,
		`{"query":"query { updateStatus { state enabled channel updateAvailable latestVersion } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("updateStatus errors = %#v", errs)
	}
	var status UpdateStatusInfo
	mustUnmarshal(t, data["updateStatus"], &status)
	if !checker.called {
		t.Fatal("checker was not called while enabled")
	}
	if checker.channel != "beta" {
		t.Fatalf("checker channel = %q, want beta", checker.channel)
	}
	if !status.Enabled || !status.UpdateAvailable || status.LatestVersion != "9.9.9" {
		t.Fatalf("status = %#v, want enabled+updateAvailable on beta", status)
	}
	if status.State != UpdateStateUpdateAvailable {
		t.Fatalf("status.State = %q, want %q", status.State, UpdateStateUpdateAvailable)
	}
}

// optInUpdateCheck stores the operator opt-in on the stable channel so a test
// can reach the delegating branch of the resolver.
func optInUpdateCheck(t *testing.T, st *store.Store) {
	t.Helper()
	enabled := true
	channel := "stable"
	if _, err := st.SetConsoleConfig(context.Background(),
		store.ConsoleConfigPatch{UpdateCheckEnabled: &enabled, UpdateCheckChannel: &channel},
		time.Unix(1700, 0).UTC()); err != nil {
		t.Fatalf("SetConsoleConfig: %v", err)
	}
}

// queryUpdateState runs the state-carrying updateStatus read and returns the
// decoded result.
func queryUpdateState(t *testing.T, handler *Handler) UpdateStatusInfo {
	t.Helper()
	data, errs := post(t, handler,
		`{"query":"query { updateStatus { state enabled currentVersion latestVersion updateAvailable } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("updateStatus errors = %#v", errs)
	}
	var status UpdateStatusInfo
	mustUnmarshal(t, data["updateStatus"], &status)
	return status
}

// TestUpdateStatusFetchFailureIsUnavailable is the regression this ticket
// exists for: an opted-in check whose outbound read failed must report a state
// distinct from the up-to-date one. Before the state field, that failure was
// indistinguishable on the wire from a current server.
func TestUpdateStatusFetchFailureIsUnavailable(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	checker := &fakeUpdateChecker{err: errors.New("manifest host unreachable")}
	handler.SetUpdateChecker(checker, "1.0.0")
	openManagerAdminSession(t, st)
	optInUpdateCheck(t, st)

	status := queryUpdateState(t, handler)
	if !checker.called {
		t.Fatal("checker was not called while enabled")
	}
	if status.State != UpdateStateUnavailable {
		t.Fatalf("status.State = %q, want %q after a failed check", status.State, UpdateStateUnavailable)
	}
	if status.State == UpdateStateUpToDate {
		t.Fatal("a failed check must never report the up-to-date state")
	}
	if !status.Enabled {
		t.Fatal("status.Enabled = false, want true — the operator did opt in")
	}
}

// TestUpdateStatusEmptyStateIsUnavailable proves the resolver's fail-closed
// normalization: a checker that omits the state does not get its result read as
// a reassuring answer.
func TestUpdateStatusEmptyStateIsUnavailable(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	handler.SetUpdateChecker(&fakeUpdateChecker{state: ""}, "1.0.0")
	openManagerAdminSession(t, st)
	optInUpdateCheck(t, st)

	if status := queryUpdateState(t, handler); status.State != UpdateStateUnavailable {
		t.Fatalf("status.State = %q, want %q for an omitted state", status.State, UpdateStateUnavailable)
	}
}

// TestUpdateStatusNoCheckerIsUnavailable proves an opted-in server with no
// checker wired reports the no-answer state rather than a version claim.
func TestUpdateStatusNoCheckerIsUnavailable(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)
	optInUpdateCheck(t, st)

	status := queryUpdateState(t, handler)
	if status.State != UpdateStateUnavailable {
		t.Fatalf("status.State = %q, want %q with no checker wired", status.State, UpdateStateUnavailable)
	}
	if status.UpdateAvailable {
		t.Fatal("status.UpdateAvailable = true with no checker wired")
	}
}

// TestUpdateStatusUpToDateIsDistinct pins the up-to-date token to its own
// value, so the two answers this ticket separates cannot collapse back into one.
func TestUpdateStatusUpToDateIsDistinct(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	handler.SetUpdateChecker(&fakeUpdateChecker{state: UpdateStateUpToDate}, "1.0.0")
	openManagerAdminSession(t, st)
	optInUpdateCheck(t, st)

	if status := queryUpdateState(t, handler); status.State != UpdateStateUpToDate {
		t.Fatalf("status.State = %q, want %q", status.State, UpdateStateUpToDate)
	}
}
