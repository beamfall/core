//go:build linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/beamfall/core/internal/integrationhost"
)

// deniedEtcFixture returns a readable /etc file that no confinement profile
// grants. Landlock checks the resolved inode, so a candidate that is a symlink
// out of /etc is useless: Debian and Ubuntu ship /etc/os-release as a link to
// /usr/lib/os-release, and /usr/lib is a granted runtime directory.
func deniedEtcFixture(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"/etc/os-release", "/etc/hostname", "/etc/passwd", "/etc/group"} {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil || !strings.HasPrefix(resolved, "/etc/") || slices.Contains(runtimeConfigPaths(), resolved) {
			continue
		}
		if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if _, err := os.ReadFile(resolved); err != nil {
			continue
		}
		return resolved
	}
	t.Skip("no stable readable /etc fixture resolving inside /etc")
	return ""
}

func TestLandlockRuntimeDoesNotGrantEtcTree(t *testing.T) {
	if slices.Contains(runtimePaths("/usr/bin/ffmpeg"), "/etc") {
		t.Fatal("runtime grants expose the whole /etc tree")
	}
}

func TestLandlockDeniesUnlistedEtcFileAndAllowsInput(t *testing.T) {
	if os.Getenv("BEAMFALL_LANDLOCK_HELPER") == "1" {
		path := os.Getenv("BEAMFALL_LANDLOCK_PATH")
		ro := []string(nil)
		if os.Getenv("BEAMFALL_LANDLOCK_ALLOW") == "1" {
			ro = []string{os.Getenv("BEAMFALL_LANDLOCK_GRANT")}
		}
		if err := applyConfinement(ro, nil, os.Args[0]); err != nil {
			os.Exit(3)
		}
		_, err := os.ReadFile(path)
		if os.Getenv("BEAMFALL_LANDLOCK_ALLOW") == "1" && err == nil {
			os.Exit(0)
		}
		if os.Getenv("BEAMFALL_LANDLOCK_ALLOW") != "1" && errors.Is(err, os.ErrPermission) {
			os.Exit(0)
		}
		os.Exit(4)
	}
	if landlockABI() < 1 {
		t.Skip("Landlock is unavailable on this kernel")
	}
	denied := deniedEtcFixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockDeniesUnlistedEtcFileAndAllowsInput")
	cmd.Env = append(os.Environ(), "BEAMFALL_LANDLOCK_HELPER=1", "BEAMFALL_LANDLOCK_PATH="+denied)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unlisted /etc read was not denied: %v %s", err, out)
	}

	inputDir := t.TempDir()
	input := inputDir + "/input"
	if err := os.WriteFile(input, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(os.Args[0], "-test.run=TestLandlockDeniesUnlistedEtcFileAndAllowsInput")
	cmd.Env = append(os.Environ(), "BEAMFALL_LANDLOCK_HELPER=1", "BEAMFALL_LANDLOCK_ALLOW=1", "BEAMFALL_LANDLOCK_GRANT="+inputDir, "BEAMFALL_LANDLOCK_PATH="+input)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("declared read path was denied: %v %s", err, out)
	}
}

func TestLandlockDevNullUsesFileValidAccess(t *testing.T) {
	abi := landlockABI()
	if abi < 1 {
		t.Skip("Landlock is unavailable on this kernel")
	}

	attr := unix.LandlockRulesetAttr{Access_fs: handledAccess(abi)}
	rulesetFD, err := landlockCreateRuleset(&attr)
	if err != nil {
		t.Fatalf("landlockCreateRuleset: %v", err)
	}
	defer unix.Close(rulesetFD)

	if err := addPathBeneath(rulesetFD, "/dev/null", nullDeviceAccess()); err != nil {
		t.Fatalf("file-valid /dev/null access rejected: %v", err)
	}

	directoryAccess := readAccess() | writeAccessFor(abi)
	if err := addPathBeneath(rulesetFD, "/dev/null", directoryAccess); !errors.Is(err, unix.EINVAL) {
		t.Fatalf("directory access on /dev/null error = %v, want EINVAL", err)
	}
}

// TestLocalInferenceConfinementFailsClosedWithoutLandlock is the audit finding
// itself: where the ffmpeg profile treats a kernel without landlock as "rlimits
// carry the weight", local inference must refuse the launch.
func TestLocalInferenceConfinementFailsClosedWithoutLandlock(t *testing.T) {
	if os.Getenv("BEAMFALL_FFMPEG_POSTURE_HELPER") == "1" {
		// applyConfinement really restricts the calling process on a landlock
		// kernel, so it only ever runs in this throwaway helper: in the test
		// binary itself it would deny every later test its TempDir.
		localInferenceLandlockABI = func() int { return -1 }
		if applyConfinement([]string{"/tmp"}, nil, os.Args[0]) != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
	original := localInferenceLandlockABI
	t.Cleanup(func() { localInferenceLandlockABI = original })
	localInferenceLandlockABI = func() int { return -1 }

	err := applyLocalInferenceConfinement([]string{"/tmp"}, []string{"/tmp"}, os.Args[0])
	if !errors.Is(err, integrationhost.ErrLocalInferenceConfinementUnavailable) {
		t.Fatalf("error = %v, want ErrLocalInferenceConfinementUnavailable", err)
	}
	// #nosec G204 G702 -- re-executes this test binary with a fixed -test.run pattern.
	cmd := exec.Command(os.Args[0], "-test.run=^TestLocalInferenceConfinementFailsClosedWithoutLandlock$")
	cmd.Env = append(os.Environ(), "BEAMFALL_FFMPEG_POSTURE_HELPER=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the ffmpeg profile must keep its own best-effort posture: %v %s", err, out)
	}
}

// TestLocalInferenceConfinementRefusesKernelsWithoutTruncateFence covers the
// half of the fail-closed contract a landlock-capable kernel can still miss:
// ABI 1 and 2 create a ruleset happily, but LANDLOCK_ACCESS_FS_TRUNCATE does
// not exist until ABI 3, so the read-only model and input roots would remain
// emptiable through truncate(2). That is refused like no landlock at all.
func TestLocalInferenceConfinementRefusesKernelsWithoutTruncateFence(t *testing.T) {
	original := localInferenceLandlockABI
	t.Cleanup(func() { localInferenceLandlockABI = original })

	for _, abi := range []int{1, integrationhost.LocalInferenceMinLandlockABI - 1} {
		localInferenceLandlockABI = func() int { return abi }
		err := applyLocalInferenceConfinement([]string{"/tmp"}, []string{"/tmp"}, os.Args[0])
		if !errors.Is(err, integrationhost.ErrLocalInferenceConfinementUnavailable) {
			t.Fatalf("landlock ABI %d error = %v, want ErrLocalInferenceConfinementUnavailable", abi, err)
		}
	}
}

// TestLocalInferenceConfinementDeniesHostTrees runs the real ruleset in a
// helper process: the three declared roots behave as declared, and host
// configuration outside them is denied by the kernel.
func TestLocalInferenceConfinementDeniesHostTrees(t *testing.T) {
	if os.Getenv("BEAMFALL_LOCAL_INFERENCE_HELPER") == "1" {
		localInferenceConfinementHelper()
		return
	}
	if !integrationhost.LocalInferenceLandlockEnforceable(landlockABI()) {
		t.Skipf("kernel landlock ABI %d is below the local inference floor %d",
			landlockABI(), integrationhost.LocalInferenceMinLandlockABI)
	}
	denied := deniedEtcFixture(t)
	input, model, work := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(input, "media.bin"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, probe := range []string{"read-input", "write-work", "read-etc", "write-model"} {
		cmd := exec.Command(os.Args[0], "-test.run=TestLocalInferenceConfinementDeniesHostTrees")
		cmd.Env = append(os.Environ(),
			"BEAMFALL_LOCAL_INFERENCE_HELPER=1",
			"BEAMFALL_LOCAL_INFERENCE_PROBE="+probe,
			"BEAMFALL_LOCAL_INFERENCE_INPUT="+input,
			"BEAMFALL_LOCAL_INFERENCE_MODEL="+model,
			"BEAMFALL_LOCAL_INFERENCE_WORK="+work,
			"BEAMFALL_LOCAL_INFERENCE_ETC="+denied,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("probe %s did not behave as declared: %v\n%s", probe, err, out)
		}
	}
}

// localInferenceConfinementHelper applies the real profile to itself and exits
// 0 only when the probe's declared outcome holds.
func localInferenceConfinementHelper() {
	input := os.Getenv("BEAMFALL_LOCAL_INFERENCE_INPUT")
	model := os.Getenv("BEAMFALL_LOCAL_INFERENCE_MODEL")
	work := os.Getenv("BEAMFALL_LOCAL_INFERENCE_WORK")
	if err := applyLocalInferenceConfinement([]string{input, model}, []string{work}, os.Args[0]); err != nil {
		os.Exit(3)
	}
	var err error
	switch os.Getenv("BEAMFALL_LOCAL_INFERENCE_PROBE") {
	case "read-input":
		_, err = os.ReadFile(filepath.Join(input, "media.bin"))
	case "write-work":
		err = os.WriteFile(filepath.Join(work, "labels.json"), []byte("{}"), 0o600)
	case "read-etc":
		// #nosec G703 -- the parent test sets this probe path; the child only checks it is denied.
		if _, readErr := os.ReadFile(os.Getenv("BEAMFALL_LOCAL_INFERENCE_ETC")); errors.Is(readErr, os.ErrPermission) {
			os.Exit(0)
		}
		os.Exit(4)
	case "write-model":
		if writeErr := os.WriteFile(filepath.Join(model, "escape"), []byte("no"), 0o600); errors.Is(writeErr, os.ErrPermission) {
			os.Exit(0)
		}
		os.Exit(4)
	default:
		os.Exit(5)
	}
	if err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

// TestNoNetworkFilterChecksArchitectureBeforeSyscallNumber pins the ordering a
// syscall-number filter depends on: the architecture word is validated first,
// so a foreign personality cannot reach the number comparisons at all.
func TestNoNetworkFilterChecksArchitectureBeforeSyscallNumber(t *testing.T) {
	arch, ok := seccompNativeArch[runtime.GOARCH]
	if !ok {
		t.Fatalf("no seccomp architecture token for linux/%s", runtime.GOARCH)
	}
	filter := noNetworkFilter(arch)
	const denyErrno = unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)
	if filter[0].Code != unix.BPF_LD|unix.BPF_W|unix.BPF_ABS || filter[0].K != 4 {
		t.Fatalf("first instruction = %+v, want a load of the architecture word", filter[0])
	}
	if filter[1].K != arch || filter[1].Jt != 1 {
		t.Fatalf("second instruction = %+v, want an equality test against %#x", filter[1], arch)
	}
	if filter[2].Code != unix.BPF_RET|unix.BPF_K || filter[2].K != denyErrno {
		t.Fatalf("foreign architecture instruction = %+v, want a deny return", filter[2])
	}
	if filter[3].K != 0 {
		t.Fatalf("fourth instruction = %+v, want a load of the syscall number", filter[3])
	}
	if filter[len(filter)-1].K != unix.SECCOMP_RET_ALLOW {
		t.Fatalf("last instruction = %+v, want the allow return", filter[len(filter)-1])
	}
}
