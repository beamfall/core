//go:build linux

package main

import (
	"errors"
	"flag"
	"fmt"
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

func TestLandlockRuntimeDoesNotGrantEtcTree(t *testing.T) {
	if slices.Contains(runtimePaths("/usr/bin/ffmpeg"), "/etc") {
		t.Fatal("runtime grants expose the whole /etc tree")
	}
}

// unlistedEtcFixture returns a readable regular file directly in /etc that no
// profile lists, or "" when the host has none. A symlink is not a fixture:
// /etc/os-release often points into /usr/lib, which the profiles grant, so
// reading it proves nothing about /etc.
func unlistedEtcFixture() string {
	for _, path := range []string{"/etc/hostname", "/etc/passwd", "/etc/group", "/etc/os-release"} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if _, err := os.ReadFile(path); err == nil { // #nosec G304 -- path comes from the fixed /etc fixture list above
			return path
		}
	}
	return ""
}

func TestLandlockDeniesUnlistedEtcFileAndAllowsInput(t *testing.T) {
	if os.Getenv("BEAMFALL_LANDLOCK_HELPER") == "1" {
		// Landlock binds the calling thread; probe from the confined one.
		runtime.LockOSThread()
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
	denied := unlistedEtcFixture()
	if denied == "" {
		t.Skip("no stable readable /etc fixture")
	}
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
	original := localInferenceLandlockABI
	t.Cleanup(func() { localInferenceLandlockABI = original })
	localInferenceLandlockABI = func() int { return -1 }

	err := applyLocalInferenceConfinement([]string{"/tmp"}, []string{"/tmp"}, os.Args[0])
	if !errors.Is(err, integrationhost.ErrLocalInferenceConfinementUnavailable) {
		t.Fatalf("error = %v, want ErrLocalInferenceConfinementUnavailable", err)
	}
	if onThrowawayThread(func() error { return applyConfinement([]string{"/tmp"}, nil, os.Args[0]) }) != nil {
		t.Fatal("the ffmpeg profile must keep its own best-effort posture")
	}
}

// onThrowawayThread runs f, which may really confine its calling thread, on a
// goroutine locked to its own OS thread and never unlocked, so the runtime
// retires that thread when the goroutine exits. Confining a test goroutine's
// thread directly would leave it confined and silently deny later tests that
// the scheduler places on it.
func onThrowawayThread(f func() error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		done <- f()
	}()
	return <-done
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
	denied := unlistedEtcFixture()
	if denied == "" {
		t.Skip("no stable readable /etc fixture")
	}
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
	// Landlock binds the calling thread; probe from the confined one.
	runtime.LockOSThread()
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
		if _, readErr := os.ReadFile(os.Getenv("BEAMFALL_LOCAL_INFERENCE_ETC")); errors.Is(readErr, os.ErrPermission) { // #nosec G703 -- the parent test sets this path; reading outside the grants is the probe and must be denied
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

// confineShimHelperEnv selects the shim role of
// TestShimExecsConfinedWorkerWhenThreadsMigrate: the helper process runs the
// real shim entry point and execs this test binary as the worker probe.
const confineShimHelperEnv = "BEAMFALL_CONFINE_SHIM_HELPER"

// confinedWorkerProbeArg is the positional argument that marks the exec'd
// worker role. It travels in argv rather than the environment because the
// local inference shim execs its target with an empty environment.
const confinedWorkerProbeArg = "beamfall-confined-worker-probe"

// TestShimExecsConfinedWorkerWhenThreadsMigrate proves the worker a shim execs
// is confined even when the Go scheduler moves the shim's goroutine to another
// OS thread between confinement and exec. no_new_privs, seccomp and landlock
// bind the calling thread only, and execve from any thread replaces the whole
// process with that thread's credentials, so an unlocked goroutine that
// migrated would exec an unconfined worker. Before the real exec the helper
// calls moveOffThread, which deterministically moves an unlocked goroutine to
// another thread, and fails if the goroutine left the thread it confined.
func TestShimExecsConfinedWorkerWhenThreadsMigrate(t *testing.T) {
	if mode := os.Getenv(confineShimHelperEnv); mode != "" {
		confineShimHelper(mode)
		return
	}
	abi := landlockABI()
	if abi < 1 {
		t.Skip("Landlock is unavailable on this kernel")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	for _, mode := range []string{"ffmpeg", "local-inference"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "local-inference" && !integrationhost.LocalInferenceLandlockEnforceable(abi) {
				t.Skipf("kernel landlock ABI %d is below the local inference floor %d",
					abi, integrationhost.LocalInferenceMinLandlockABI)
			}
			outside := t.TempDir()
			denied := filepath.Join(outside, "denied")
			if err := os.WriteFile(denied, []byte("secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			input, model, work := t.TempDir(), t.TempDir(), t.TempDir()
			if err := os.Chmod(model, 0o700); err != nil { // #nosec G302 -- model is a directory; owner-only 0700 is the least that keeps it traversable
				t.Fatal(err)
			}
			cmd := exec.Command(self, "-test.run=^TestShimExecsConfinedWorkerWhenThreadsMigrate$")
			cmd.Env = append(os.Environ(),
				confineShimHelperEnv+"="+mode,
				"BEAMFALL_CONFINE_SHIM_DENIED="+denied,
				"BEAMFALL_CONFINE_SHIM_INPUT="+input,
				"BEAMFALL_CONFINE_SHIM_MODEL="+model,
				"BEAMFALL_CONFINE_SHIM_WORK="+work,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("exec'd worker was not confined: %v\n%s", err, out)
			}
			t.Logf("%s", out)
		})
	}
}

// confineShimHelper runs one shim exactly as production does, except that the
// exec is preceded by moveOffThread, and a goroutine that moved exits non-zero.
func confineShimHelper(mode string) {
	self, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	denied := os.Getenv("BEAMFALL_CONFINE_SHIM_DENIED")
	probe := []string{self, "-test.run=^TestConfinedWorkerProbe$", confinedWorkerProbeArg, mode, denied}
	original := execShimTarget
	execShimTarget = func(path string, argv, env []string) error {
		if before, after, _ := moveOffThread(); after != before {
			fmt.Fprintf(os.Stderr, "shim goroutine moved from OS thread %d to %d before exec: not locked to its confined thread\n", before, after)
			os.Exit(5)
		}
		return original(path, argv, env)
	}
	switch mode {
	case "ffmpeg":
		err = runFFmpegShim(nil, nil, probe)
	case "local-inference":
		err = runLocalInferenceShim(
			os.Getenv("BEAMFALL_CONFINE_SHIM_INPUT"),
			os.Getenv("BEAMFALL_CONFINE_SHIM_MODEL"),
			os.Getenv("BEAMFALL_CONFINE_SHIM_WORK"),
			probe)
	default:
		err = fmt.Errorf("unknown shim mode %q", mode)
	}
	fmt.Fprintf(os.Stderr, "shim returned without exec: %v\n", err)
	os.Exit(3)
}

// moveOffThread parks the calling goroutine, with a single P, while a second
// goroutine takes over the OS thread it parked on and locks that thread to
// itself. An unlocked caller therefore resumes on a different thread; a caller
// locked to its thread is handed back to that thread. It returns the thread ids
// before and after, and a release func that ends the second goroutine (its
// locked thread exits with it).
func moveOffThread() (before, after int, release func()) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	before = unix.Gettid()
	woke, done := make(chan struct{}), make(chan struct{})
	go func() {
		runtime.LockOSThread()
		close(woke)
		<-done
	}()
	<-woke
	return before, unix.Gettid(), func() { close(done) }
}

// TestShimThreadMoveControl proves moveOffThread moves an unlocked goroutine
// and keeps a locked one, so TestShimExecsConfinedWorkerWhenThreadsMigrate
// fails when a shim stops calling lockShimThread instead of passing by chance.
func TestShimThreadMoveControl(t *testing.T) {
	for _, locked := range []bool{false, true} {
		got := make(chan [2]int)
		go func() {
			if locked {
				runtime.LockOSThread() // never unlocked: the thread exits with this goroutine
			}
			before, after, release := moveOffThread()
			release()
			got <- [2]int{before, after}
		}()
		tids := <-got
		if moved := tids[0] != tids[1]; moved == locked {
			t.Errorf("locked=%v: goroutine went from thread %d to %d, moved=%v, want %v",
				locked, tids[0], tids[1], moved, !locked)
		}
	}
}

// TestConfinedWorkerProbe is the exec'd worker. It only does anything when a
// shim helper launched it; it then checks, from inside the new process image,
// every half of the profile its shim promised and exits non-zero on the first
// gap. It reports through the exit status because the landlock domain denies
// the filesystem writes the testing framework could otherwise need.
func TestConfinedWorkerProbe(t *testing.T) {
	args := flag.Args()
	if len(args) != 3 || args[0] != confinedWorkerProbeArg {
		t.Skip("runs only as the worker a shim helper execs")
	}
	mode, denied := args[1], args[2]
	var gaps []string
	if nnp, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0); err != nil || nnp != 1 {
		gaps = append(gaps, fmt.Sprintf("no_new_privs = %d (%v), want 1", nnp, err))
	}
	if _, err := os.ReadFile(denied); !errors.Is(err, os.ErrPermission) { // #nosec G304 -- the shim helper passes a test-owned path; reading it is the probe and must be denied
		gaps = append(gaps, fmt.Sprintf("read outside the landlock grants: err = %v, want permission denied", err))
	}
	if mode == "local-inference" {
		if sc, err := unix.PrctlRetInt(unix.PR_GET_SECCOMP, 0, 0, 0, 0); err != nil || sc != unix.SECCOMP_MODE_FILTER {
			gaps = append(gaps, fmt.Sprintf("seccomp mode = %d (%v), want filter", sc, err))
		}
		fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err == nil {
			_ = unix.Close(fd)
		}
		if !errors.Is(err, unix.EPERM) {
			gaps = append(gaps, fmt.Sprintf("socket(2) err = %v, want EPERM", err))
		}
	}
	if len(gaps) > 0 {
		fmt.Fprintf(os.Stderr, "%s worker is not confined: %s\n", mode, strings.Join(gaps, "; "))
		os.Exit(4)
	}
	fmt.Fprintf(os.Stderr, "%s worker is confined\n", mode)
	os.Exit(0)
}
