package main

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/integrationhost"
)

func TestParseLocalInferenceShimArgs(t *testing.T) {
	input, model, work, argv, err := parseLocalInferenceShimArgs([]string{
		"--input-ro", "/media/input",
		"--model-ro", "/var/models",
		"--work-rw", "/var/work",
		"--", "/usr/bin/infer", "--json",
	})
	if err != nil {
		t.Fatalf("parseLocalInferenceShimArgs: %v", err)
	}
	if input != "/media/input" || model != "/var/models" || work != "/var/work" {
		t.Fatalf("roots = %q %q %q", input, model, work)
	}
	if !reflect.DeepEqual(argv, []string{"/usr/bin/infer", "--json"}) {
		t.Fatalf("argv = %q", argv)
	}
}

func TestParseLocalInferenceShimArgsRejectsBadArgs(t *testing.T) {
	bad := [][]string{
		{"--input-ro", "/x", "--model-ro", "/m", "--work-rw", "/w"},
		{"--input-ro", "/x", "--model-ro", "/m", "--", "/bin/true"},
		{"--input-ro", "relative", "--model-ro", "/m", "--work-rw", "/w", "--", "/bin/true"},
		{"--bogus", "/x", "--", "/bin/true"},
	}
	for _, args := range bad {
		if _, _, _, _, err := parseLocalInferenceShimArgs(args); err == nil {
			t.Fatalf("accepted bad args %q", args)
		}
	}
}

func TestApplyLocalInferenceSandbox(t *testing.T) {
	var gotRO, gotRW []string
	var gotBinary string
	err := applyLocalInferenceSandbox("/input", "/model", "/work", "/bin/infer",
		func() error { return nil },
		func(ro, rw []string, binary string) error {
			gotRO, gotRW, gotBinary = ro, rw, binary
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotRO, []string{"/input", "/model"}) ||
		!reflect.DeepEqual(gotRW, []string{"/work"}) || gotBinary != "/bin/infer" {
		t.Fatalf("sandbox args = ro:%q rw:%q binary:%q", gotRO, gotRW, gotBinary)
	}

	want := errors.New("denied")
	if err := applyLocalInferenceSandbox("/i", "/m", "/w", "/b",
		func() error { return want },
		func(_, _ []string, _ string) error { return nil },
	); !errors.Is(err, want) || !strings.Contains(err.Error(), "no-network") {
		t.Fatalf("no-network error = %v", err)
	}
	if err := applyLocalInferenceSandbox("/i", "/m", "/w", "/b",
		func() error { return nil },
		func(_, _ []string, _ string) error { return want },
	); !errors.Is(err, want) || !strings.Contains(err.Error(), "filesystem confinement") {
		t.Fatalf("confinement error = %v", err)
	}
}

// TestLocalInferenceHardeningFailsClosedOffLinux proves both halves of the
// profile refuse where the kernel cannot enforce them. The ffmpeg shim keeps
// its documented best-effort posture; local inference does not inherit it.
func TestLocalInferenceHardeningFailsClosedOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux enforces the profile; the kernel-side assertions live in confine_linux_test.go")
	}
	if err := applyLocalInferenceNoNetwork(); !errors.Is(err, integrationhost.ErrLocalInferenceConfinementUnavailable) {
		t.Fatalf("no-network error = %v, want ErrLocalInferenceConfinementUnavailable", err)
	}
	if err := applyLocalInferenceConfinement([]string{"/input"}, []string{"/work"}, "/bin/echo"); !errors.Is(err, integrationhost.ErrLocalInferenceConfinementUnavailable) {
		t.Fatalf("confinement error = %v, want ErrLocalInferenceConfinementUnavailable", err)
	}
	if applyNoNetwork() != nil || applyConfinement([]string{"/input"}, []string{"/work"}, "/bin/echo") != nil {
		t.Fatal("the ffmpeg profile must keep its own best-effort posture")
	}
}

// TestRunLocalInferenceShimRefusesBeforeExec proves the refusal happens before
// the process image is replaced: a shim that exec'd would never return here.
func TestRunLocalInferenceShimRefusesBeforeExec(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux has an enforcement path; this asserts the refusal where it does not")
	}
	input, model, work := t.TempDir(), t.TempDir(), t.TempDir()
	// #nosec G302 -- the model root is a directory and CheckLocalInferenceModelRoot
	// requires exactly 0700 on it, so 0600 would both fail the posture check and
	// make the directory untraversable; it is this test's own t.TempDir().
	if err := os.Chmod(model, 0o700); err != nil {
		t.Fatal(err)
	}
	err := runLocalInferenceShim(input, model, work, []string{"/bin/echo", "--json"})
	if !errors.Is(err, integrationhost.ErrLocalInferenceConfinementUnavailable) {
		t.Fatalf("shim error = %v, want ErrLocalInferenceConfinementUnavailable", err)
	}
}

// TestRunLocalInferenceShimRefusesEscapedRoots proves an unsatisfiable
// inventory is refused before any confinement is applied.
func TestRunLocalInferenceShimRefusesEscapedRoots(t *testing.T) {
	model, work := t.TempDir(), t.TempDir()
	// #nosec G302 -- the model root is a directory and CheckLocalInferenceModelRoot
	// requires exactly 0700 on it, so 0600 would both fail the posture check and
	// make the directory untraversable; it is this test's own t.TempDir().
	if err := os.Chmod(model, 0o700); err != nil {
		t.Fatal(err)
	}
	err := runLocalInferenceShim("/etc", model, work, []string{"/bin/echo"})
	if !errors.Is(err, integrationhost.ErrLocalInferenceRootEscape) {
		t.Fatalf("shim error = %v, want ErrLocalInferenceRootEscape", err)
	}
}
