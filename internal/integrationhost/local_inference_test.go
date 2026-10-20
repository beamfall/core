package integrationhost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLocalInferenceCommandUsesThreePathSandbox(t *testing.T) {
	req := localInferenceRequest(t)
	shim := testShimPath(t)
	rt := LocalInferenceRuntime{ShimPath: shim}
	cmd, err := rt.Command(context.Background(), req)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if runtime.GOOS == "darwin" {
		if !strings.HasSuffix(cmd.Path, "sandbox-exec") {
			t.Fatalf("darwin command path = %q, want sandbox-exec", cmd.Path)
		}
		profile := strings.Join(cmd.Args, "\n")
		for _, want := range []string{"(deny default)", "(deny network*)", realTestPath(t, req.InputRoot), realTestPath(t, req.ModelRoot), realTestPath(t, req.WorkRoot)} {
			if !strings.Contains(profile, want) {
				t.Fatalf("sandbox profile missing %q:\n%s", want, profile)
			}
		}
		return
	}
	want := []string{
		shim, localInferenceShimSubcommand,
		"--input-ro", realTestPath(t, req.InputRoot),
		"--model-ro", realTestPath(t, req.ModelRoot),
		"--work-rw", realTestPath(t, req.WorkRoot),
		"--", realTestPath(t, req.Binary), "--json",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("shim args:\n got  %q\nwant %q", cmd.Args, want)
	}
}

// TestLocalInferenceRefusesWithoutEnforcementPath covers the fail-closed half
// of the launch contract: an absent hardening shim is an unavailable
// enforcement path, and the launch is refused rather than run unconfined.
func TestLocalInferenceRefusesWithoutEnforcementPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent-beamfall")
	if _, err := (LocalInferenceRuntime{ShimPath: missing}).confinementShim(); !errors.Is(err, ErrLocalInferenceConfinementUnavailable) {
		t.Fatalf("missing shim error = %v, want ErrLocalInferenceConfinementUnavailable", err)
	}
	if _, err := (LocalInferenceRuntime{ShimPath: testShimPath(t)}).confinementShim(); err != nil {
		t.Fatalf("present shim rejected: %v", err)
	}
}

// TestLocalInferenceLandlockThresholdFencesTruncate pins the fail-closed
// threshold the linux shim decides on. It runs on every host because the
// threshold itself is a pure predicate: ABI 1 and 2 accept a ruleset but carry
// no LANDLOCK_ACCESS_FS_TRUNCATE bit, so the read-only roots would stay
// emptiable and the launch must be refused exactly as it is with no landlock.
func TestLocalInferenceLandlockThresholdFencesTruncate(t *testing.T) {
	for _, abi := range []int{-1, 0, 1, 2} {
		if LocalInferenceLandlockEnforceable(abi) {
			t.Fatalf("landlock ABI %d accepted; it cannot fence truncate on the read-only roots", abi)
		}
	}
	for _, abi := range []int{LocalInferenceMinLandlockABI, 4, 5} {
		if !LocalInferenceLandlockEnforceable(abi) {
			t.Fatalf("landlock ABI %d refused; truncate is in the handled set from ABI %d", abi, LocalInferenceMinLandlockABI)
		}
	}
}

// TestLocalInferenceRunPositiveMinimumPermission is the positive case: a real
// confined child, launched with nothing but the three declared roots, produces
// its label payload and is reaped.
func TestLocalInferenceRunPositiveMinimumPermission(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec confinement is the darwin enforcement path; linux runs the shim under seccomp+landlock")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available on this host")
	}
	req := localInferenceRequest(t)
	req.Binary = "/bin/echo"
	req.Args = []string{`{"media_id":"media-1","labels":[{"label":"outdoor","confidence":0.75}]}`}

	result, err := LocalInferenceRuntime{}.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.MediaID != "media-1" || len(result.Labels) != 1 || result.Labels[0].Label != "outdoor" {
		t.Fatalf("result = %+v", result)
	}
}

// TestLocalInferenceRunRefusesUnsatisfiablePolicy proves the refusal reaches
// the caller of Run, not just the policy builder.
func TestLocalInferenceRunRefusesUnsatisfiablePolicy(t *testing.T) {
	req := localInferenceRequest(t)
	req.InputRoot = "/etc"
	if _, err := (LocalInferenceRuntime{ShimPath: testShimPath(t)}).Run(context.Background(), req); !errors.Is(err, ErrLocalInferenceRootEscape) {
		t.Fatalf("Run error = %v, want ErrLocalInferenceRootEscape", err)
	}
}

func testShimPath(t *testing.T) string {
	t.Helper()
	shim := filepath.Join(t.TempDir(), "beamfall")
	// #nosec G306 -- confinementShim refuses a shim with no execute bit, so
	// 0700 is the minimum a stand-in shim can carry; it is a private file under
	// this test's own t.TempDir().
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return shim
}

func TestLocalInferenceModelRootPosture(t *testing.T) {
	model := t.TempDir()
	if err := os.Chmod(model, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CheckLocalInferenceModelRoot(model); err != nil {
		t.Fatalf("0700 model root rejected: %v", err)
	}
	if err := os.Chmod(model, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckLocalInferenceModelRoot(model); err == nil {
		t.Fatal("0755 model root accepted")
	}
}

func TestLocalInferenceRejectsOverlappingBroadAndUnownedRoots(t *testing.T) {
	base := localInferenceRequest(t)
	tests := []struct {
		name string
		edit func(*LocalInferenceRequest)
		want string
	}{
		{name: "equal", edit: func(req *LocalInferenceRequest) { req.WorkRoot = req.InputRoot }, want: "root escape"},
		{name: "nested", edit: func(req *LocalInferenceRequest) {
			req.WorkRoot = filepath.Join(req.InputRoot, "work")
			if err := os.Mkdir(req.WorkRoot, 0o700); err != nil {
				t.Fatal(err)
			}
		}, want: "root escape"},
		{name: "broad", edit: func(req *LocalInferenceRequest) { req.InputRoot = string(os.PathSeparator) }, want: "root escape"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.edit(&req)
			if err := ValidateLocalInferenceRequest(req); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validation = %v, want %q refusal", err, tc.want)
			}
		})
	}
	if _, err := validatedLocalInferencePolicy(base, func(string) (bool, error) { return false, nil }); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("unowned roots = %v, want refusal", err)
	}
	ownerErr := errors.New("owner metadata unavailable")
	if _, err := validatedLocalInferencePolicy(base, func(string) (bool, error) { return false, ownerErr }); !errors.Is(err, ownerErr) {
		t.Fatalf("owner metadata error = %v, want %v", err, ownerErr)
	}
}

func TestLocalInferenceCapsStdoutAndStderr(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			output := newBoundedInferenceOutput(stream, cancel)
			payload := make([]byte, localInferenceOutputLimit+1)
			if n, err := output.Write(payload); err != nil || n != len(payload) {
				t.Fatalf("Write = (%d, %v)", n, err)
			}
			if len(output.Bytes()) != localInferenceOutputLimit {
				t.Fatalf("captured bytes = %d, want %d", len(output.Bytes()), localInferenceOutputLimit)
			}
			if err := output.limitError(); err == nil || !strings.Contains(err.Error(), stream) {
				t.Fatalf("limit error = %v, want %s", err, stream)
			}
			if cause := context.Cause(ctx); cause == nil || !strings.Contains(cause.Error(), stream) {
				t.Fatalf("cancellation cause = %v, want %s cap", cause, stream)
			}
		})
	}
}

func localInferenceRequest(t *testing.T) LocalInferenceRequest {
	t.Helper()
	input := t.TempDir()
	model := t.TempDir()
	work := t.TempDir()
	if err := os.Chmod(model, 0o700); err != nil {
		t.Fatal(err)
	}
	return LocalInferenceRequest{
		MediaID:   "media-1",
		InputRoot: input,
		ModelRoot: model,
		WorkRoot:  work,
		Binary:    "/bin/echo",
		Args:      []string{"--json"},
	}
}

func realTestPath(t *testing.T, path string) string {
	t.Helper()
	real, err := realSandboxPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
