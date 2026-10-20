package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestZeroStatusIsNotUpToDate pins the fail-safe default: a Status nobody
// populated reports "unavailable", never the reassuring answer. This is what
// makes every early return in Check safe by construction.
func TestZeroStatusIsNotUpToDate(t *testing.T) {
	var zero Status
	if zero.State != StateUnavailable {
		t.Fatalf("zero Status.State = %v, want StateUnavailable", zero.State)
	}
	if zero.IsUpToDate() {
		t.Fatal("zero Status reported up to date, want false (fail-safe default)")
	}
	if zero.State.IsAnswered() {
		t.Fatal("zero Status.State.IsAnswered() = true, want false")
	}
}

// TestUnreachableManifestIsDistinctFromUpToDate is the acceptance for
// RELEASE-UPDATE-MANIFEST-PUBLISHER-1: when the manifest host cannot be
// reached, the operator gets StateUnavailable — a value that is NOT the
// up-to-date value and cannot be mistaken for it. The httptest server is
// closed before the check runs, so the dial genuinely fails.
func TestUnreachableManifestIsDistinctFromUpToDate(t *testing.T) {
	pub, _ := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close() // nothing listens on base any more

	svc := newTestService(base, pub)
	status, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err == nil {
		t.Fatal("Check against an unreachable host returned no error")
	}
	if status.State != StateUnavailable {
		t.Fatalf("unreachable host: State = %v, want StateUnavailable", status.State)
	}
	if status.IsUpToDate() {
		t.Fatal("unreachable host reported up to date — the silent-success path is reachable")
	}
	if status.State == StateUpToDate {
		t.Fatal("unreachable host produced the up-to-date state")
	}
	if status.UpdateAvailable || status.Verified || status.LatestVersion != "" {
		t.Fatalf("unreachable host leaked a version judgement: %+v", status)
	}
}

// TestAbsentManifestIsDistinctFromUpToDate covers the other half of the
// acceptance: the host answers, but no manifest is published for the channel
// (404). That must also be StateUnavailable, not up to date.
func TestAbsentManifestIsDistinctFromUpToDate(t *testing.T) {
	pub, _ := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	svc := newTestService(srv.URL, pub)
	status, err := svc.Check(context.Background(), true, "stable", "1.0.0")
	if err == nil {
		t.Fatal("absent manifest returned no error")
	}
	if status.State != StateUnavailable {
		t.Fatalf("absent manifest: State = %v, want StateUnavailable", status.State)
	}
	if status.IsUpToDate() {
		t.Fatal("absent manifest reported up to date — the silent-success path is reachable")
	}
}

// TestEveryFailureModeReportsUnavailable sweeps the failure surface: each of
// these responses must land on the same distinct state, and none of them may
// land on the up-to-date state.
func TestEveryFailureModeReportsUnavailable(t *testing.T) {
	pub, seed := testKeypair(t)
	_, otherSeed := testKeypair(t)

	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"absent", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }},
		{"server error", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}},
		{"malformed json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "{not json") }},
		{"empty body", func(w http.ResponseWriter, r *http.Request) {}},
		{"no version", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"channel":"stable"}`) }},
		{"unsigned", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"version":"9.9.9"}`) }},
		{"wrong signer", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, signedManifestJSON(t, otherSeed, "9.9.9", "stable"))
		}},
		{"wrong channel", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, signedManifestJSON(t, seed, "9.9.9", "beta"))
		}},
		{"oversized", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, signedManifestJSON(t, seed, "9.9.9", "stable"))
			fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			svc := newTestService(srv.URL, pub)
			status, err := svc.Check(context.Background(), true, "stable", "1.0.0")
			if err == nil {
				t.Fatalf("%s manifest was accepted, want a failure", tc.name)
			}
			if status.State != StateUnavailable {
				t.Fatalf("%s: State = %v, want StateUnavailable", tc.name, status.State)
			}
			if status.IsUpToDate() {
				t.Fatalf("%s reported up to date — the silent-success path is reachable", tc.name)
			}
			if status.State.IsAnswered() {
				t.Fatalf("%s: State.IsAnswered() = true, want false", tc.name)
			}
		})
	}
}

// TestVerifiedManifestReportsAnsweredStates proves the distinct states are
// genuinely distinguishable — a verified manifest reaches StateUpToDate or
// StateUpdateAvailable, so the assertions above are not passing merely because
// nothing ever reaches those values.
func TestVerifiedManifestReportsAnsweredStates(t *testing.T) {
	pub, seed := testKeypair(t)
	cases := []struct {
		name    string
		latest  string
		current string
		want    State
	}{
		{"current", "1.5.0", "1.5.0", StateUpToDate},
		{"ahead of channel", "1.4.0", "1.5.0", StateUpToDate},
		{"behind channel", "1.6.0", "1.5.0", StateUpdateAvailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, signedManifestJSON(t, seed, tc.latest, "stable"))
			}))
			defer srv.Close()

			svc := newTestService(srv.URL, pub)
			status, err := svc.Check(context.Background(), true, "stable", tc.current)
			if err != nil {
				t.Fatalf("Check error = %v", err)
			}
			if status.State != tc.want {
				t.Fatalf("State = %v, want %v", status.State, tc.want)
			}
			if got := status.IsUpToDate(); got != (tc.want == StateUpToDate) {
				t.Fatalf("IsUpToDate() = %v for state %v", got, status.State)
			}
			if !status.State.IsAnswered() {
				t.Fatalf("verified manifest: IsAnswered() = false for state %v", status.State)
			}
		})
	}
}

// TestDisabledReportsItsOwnState keeps the default-off projection out of both
// the up-to-date and the unavailable buckets: not opting in is not a failure,
// and it is not a clean bill of health either.
func TestDisabledReportsItsOwnState(t *testing.T) {
	pub, _ := testKeypair(t)
	svc := newTestService("http://127.0.0.1:1", pub)
	status, err := svc.Check(context.Background(), false, "stable", "1.0.0")
	if err != nil {
		t.Fatalf("Check(disabled) error = %v", err)
	}
	if status.State != StateDisabled {
		t.Fatalf("disabled: State = %v, want StateDisabled", status.State)
	}
	if status.IsUpToDate() || status.State.IsAnswered() {
		t.Fatal("disabled check claimed a version judgement")
	}
}

// TestStateEncoding pins the wire tokens so the unavailable case is legible to
// a consumer, and proves an out-of-range value is an encoding error rather
// than a plausible-looking default.
func TestStateEncoding(t *testing.T) {
	want := map[State]string{
		StateUnavailable:     `"unavailable"`,
		StateDisabled:        `"disabled"`,
		StateUpToDate:        `"upToDate"`,
		StateUpdateAvailable: `"updateAvailable"`,
	}
	for state, token := range want {
		b, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("marshal %v: %v", state, err)
		}
		if string(b) != token {
			t.Fatalf("marshal %v = %s, want %s", state, b, token)
		}
	}
	if _, err := json.Marshal(State(99)); err == nil {
		t.Fatal("an out-of-range State encoded without error")
	}
	if s := State(99).String(); !strings.Contains(s, "99") {
		t.Fatalf("State(99).String() = %q, want it to name the unknown value", s)
	}
}

// TestOnlyOneAssignmentOfUpToDate is the structural half of the acceptance
// ("no code path can report as up-to-date"). It parses the package's own
// non-test sources and asserts StateUpToDate is assigned in exactly one
// function, Check — so a later edit that sets the reassuring state on some
// other path fails here instead of in production.
func TestOnlyOneAssignmentOfUpToDate(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	assignedIn := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		// state.go declares the constant itself; every other mention of it in
		// non-test source is a use we care about.
		if name == "state.go" {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, rhs := range assign.Rhs {
				ident, ok := rhs.(*ast.Ident)
				if !ok || ident.Name != "StateUpToDate" {
					continue
				}
				assignedIn = append(assignedIn, fmt.Sprintf("%s:%d", name, fset.Position(assign.Pos()).Line))
			}
			return true
		})
	}

	if len(assignedIn) != 1 {
		t.Fatalf("StateUpToDate is assigned at %v, want exactly one site (in Check, after verification)", assignedIn)
	}
	if !strings.HasPrefix(assignedIn[0], "updatecheck.go:") {
		t.Fatalf("StateUpToDate assigned at %s, want it inside updatecheck.go's Check", assignedIn[0])
	}
}
