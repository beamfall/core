//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/beamfall/core/internal/integrationhost"
)

// applyConfinement restricts this process's filesystem reach to exactly the
// directories an ffmpeg/ffprobe job needs, using Linux landlock:
// roPaths read-only, rwPaths read-write, plus the read+execute paths every
// process needs to actually run ffmpeg (the binary's own directory, the shared
// library and dynamic-loader directories) and a write grant for /dev/null (the
// null muxer's sink). After landlock_restrict_self, any open() outside the
// granted tree is denied by the kernel — a malicious media file exploiting
// ffmpeg cannot read or write elsewhere.
//
// Confinement is best-effort with respect to kernel support: when landlock is
// unavailable (kernel < 5.13, or disabled at boot) the ruleset-creation syscall
// returns ENOSYS/EOPNOTSUPP and confinement is skipped rather than failing the
// transcode — the rlimits still apply, and the operator gets defense-in-depth
// where the kernel offers it, never a broken job where it does not. The handled
// access set is masked to the kernel's reported landlock ABI so newer access
// bits never make create_ruleset reject the request on an older kernel.
func applyConfinement(roPaths, rwPaths []string, binary string) error {
	abi := landlockABI()
	if abi < 1 {
		// Landlock not supported on this kernel: rlimits carry the weight.
		return nil
	}

	handled := handledAccess(abi)
	readAccess := readAccess()
	writeAccess := writeAccessFor(abi)

	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	rulesetFD, err := landlockCreateRuleset(&attr)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
			return nil
		}
		return fmt.Errorf("landlock_create_ruleset: %w", err)
	}
	defer unix.Close(rulesetFD)

	// Read-only grants: the job's input directories.
	for _, p := range roPaths {
		if err := addPathRule(rulesetFD, p, readAccess); err != nil {
			return err
		}
	}
	// Read-write grants: the job's output directories.
	for _, p := range rwPaths {
		if err := addPathRule(rulesetFD, p, readAccess|writeAccess); err != nil {
			return err
		}
	}
	// Read+execute grants for the runtime: the binary's directory and the
	// system library/loader directories, so ffmpeg and its shared objects load.
	for _, p := range runtimePaths(binary) {
		if err := addPathRuleOptional(rulesetFD, p, readAccess|(handled&unix.LANDLOCK_ACCESS_FS_EXECUTE)); err != nil {
			return err
		}
	}
	// Runtime configuration is granted file-by-file (or by the smallest known
	// data directory), never by exposing the whole /etc tree.
	for _, p := range runtimeConfigPaths() {
		if err := addPathRuleOptional(rulesetFD, p, unix.LANDLOCK_ACCESS_FS_READ_FILE); err != nil {
			return err
		}
	}
	for _, p := range runtimeDataPaths() {
		if err := addPathRuleOptional(rulesetFD, p, readAccess); err != nil {
			return err
		}
	}
	// /dev/null is the null muxer's only write sink (waveform analysis).
	if err := addPathRuleOptional(rulesetFD, "/dev/null", nullDeviceAccess()); err != nil {
		return err
	}

	// PR_SET_NO_NEW_PRIVS is a precondition of landlock_restrict_self for an
	// unprivileged process: it stops a later setuid binary from regaining
	// privileges the ruleset would otherwise be bypassed by.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}
	if err := landlockRestrictSelf(rulesetFD); err != nil {
		return fmt.Errorf("landlock_restrict_self: %w", err)
	}
	return nil
}

// localInferenceLandlockABI is the kernel-support probe, held as a seam so the
// refusal branch below can be exercised on a kernel that does support landlock.
var localInferenceLandlockABI = landlockABI

// applyLocalInferenceConfinement enforces the ADR-0127 §1 deny-all profile on
// the local inference child. It differs from applyConfinement in two ways that
// the audit finding turns on:
//
//   - It FAILS CLOSED. Where the ffmpeg shim treats a kernel without landlock as
//     "rlimits carry the weight", local inference refuses to launch: an
//     unenforceable profile is an unavailable capability, not a degraded one.
//     The refusal threshold is the ABI that can actually enforce what this
//     profile promises, not merely the ABI that accepts a ruleset: below
//     integrationhost.LocalInferenceMinLandlockABI the handled set carries no
//     LANDLOCK_ACCESS_FS_TRUNCATE bit, so the read-only roots would remain
//     emptiable through truncate(2) and open(O_TRUNC).
//   - Its inventory is exactly the three declared roots plus the exec target and
//     the loader directories. The ffmpeg policy's /etc configuration files, TLS
//     and font trees, and /dev/null sink are deliberately absent — the v1 profile
//     is CPU-only with no network and needs none of them, so host configuration
//     and credential paths stay unreachable.
//
// Every grant here is required, never optional: a declared root that cannot be
// opened is a refusal, because a ruleset missing one of its roots is an
// incomplete policy.
func applyLocalInferenceConfinement(roPaths, rwPaths []string, binary string) error {
	abi := localInferenceLandlockABI()
	if abi < 1 {
		return fmt.Errorf("%w: kernel reports no landlock support",
			integrationhost.ErrLocalInferenceConfinementUnavailable)
	}
	if !integrationhost.LocalInferenceLandlockEnforceable(abi) {
		return fmt.Errorf("%w: kernel landlock ABI %d cannot fence truncate on the read-only roots (need >= %d)",
			integrationhost.ErrLocalInferenceConfinementUnavailable, abi, integrationhost.LocalInferenceMinLandlockABI)
	}

	handled := handledAccess(abi)
	readAccess := readAccess()
	writeAccess := writeAccessFor(abi)
	execAccess := handled & unix.LANDLOCK_ACCESS_FS_EXECUTE

	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	rulesetFD, err := landlockCreateRuleset(&attr)
	if err != nil {
		return fmt.Errorf("%w: landlock_create_ruleset: %w",
			integrationhost.ErrLocalInferenceConfinementUnavailable, err)
	}
	defer unix.Close(rulesetFD)

	for _, p := range roPaths {
		if err := addPathRule(rulesetFD, p, readAccess); err != nil {
			return err
		}
	}
	for _, p := range rwPaths {
		if err := addPathRule(rulesetFD, p, readAccess|writeAccess); err != nil {
			return err
		}
	}
	// The exec target is granted on its own inode, not on its directory, so a
	// sibling binary in the same directory stays unreachable.
	if err := addPathRule(rulesetFD, binary, unix.LANDLOCK_ACCESS_FS_READ_FILE|execAccess); err != nil {
		return err
	}
	for _, p := range integrationhost.LocalInferenceLoaderDirs() {
		if err := addPathRule(rulesetFD, p, readAccess|execAccess); err != nil {
			return err
		}
	}

	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}
	if err := landlockRestrictSelf(rulesetFD); err != nil {
		return fmt.Errorf("landlock_restrict_self: %w", err)
	}
	return nil
}

// runtimePaths returns the read+execute directories ffmpeg needs at runtime: its
// own directory plus the standard shared-library and dynamic-loader locations.
// Granting whole directories (not individual files) keeps the rule count small
// and tolerates distro layout differences; a path that does not exist on this
// host is skipped by addPathRuleOptional.
func runtimePaths(binary string) []string {
	dirs := []string{
		filepath.Dir(binary),
		"/usr/bin", "/bin",
		"/usr/lib", "/lib",
		"/usr/lib64", "/lib64",
		"/usr/local/lib", "/usr/local/bin",
	}
	return dirs
}

func runtimeConfigPaths() []string {
	return []string{
		"/etc/ld.so.cache",
		"/etc/ld.so.conf",
		"/etc/nsswitch.conf",
		"/etc/hosts",
		"/etc/resolv.conf",
		"/etc/gai.conf",
		"/etc/services",
		"/etc/localtime",
		"/etc/fonts/fonts.conf",
		"/etc/ssl/openssl.cnf",
		"/etc/ca-certificates.conf",
	}
}

func runtimeDataPaths() []string {
	return []string{
		"/etc/ld.so.conf.d",
		"/etc/ssl/certs",
		"/etc/pki/tls/certs",
		"/etc/fonts",
		"/usr/share/fonts",
		"/usr/local/share/fonts",
		"/usr/share/fontconfig",
	}
}

// addPathRule adds a PATH_BENEATH rule granting access under path. The path must
// exist (it is a directory the job depends on); a missing path is a real error.
func addPathRule(rulesetFD int, path string, access uint64) error {
	if err := addPathBeneath(rulesetFD, path, access); err != nil {
		return fmt.Errorf("landlock add rule for %q: %w", path, err)
	}
	return nil
}

// addPathRuleOptional adds a PATH_BENEATH rule but tolerates a path that does
// not exist on this host (ENOENT) — used for the runtime library/loader dirs and
// /dev/null, which vary by distro and are belt-and-suspenders, not job inputs.
func addPathRuleOptional(rulesetFD int, path string, access uint64) error {
	err := addPathBeneath(rulesetFD, path, access)
	if err == nil || errors.Is(err, unix.ENOENT) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("landlock add rule for %q: %w", path, err)
}

// addPathBeneath opens path O_PATH and installs a landlock PATH_BENEATH rule
// granting access beneath it. The access mask must stay within the ruleset's
// handled set and contain only rights valid for the referenced inode type;
// directory-only rights on a file or device node are rejected with EINVAL.
func addPathBeneath(rulesetFD int, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	beneath := unix.LandlockPathBeneathAttr{
		Allowed_access: access,
		Parent_fd:      int32(fd),
	}
	return landlockAddRule(rulesetFD, &beneath)
}

// landlockABI returns the landlock ABI version the running kernel supports, or
// a value < 1 when landlock is unavailable. It queries with the documented
// LANDLOCK_CREATE_RULESET_VERSION flag, which returns the version number rather
// than creating a ruleset.
func landlockABI() int {
	r, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, uintptr(unix.LANDLOCK_CREATE_RULESET_VERSION))
	if errno != 0 {
		return -1
	}
	return int(r)
}

// handledAccess is the full set of filesystem access rights the ruleset governs,
// masked to what the queried ABI supports. Everything in this set that is not
// explicitly granted by a rule is denied. Higher ABI versions add bits
// (TRUNCATE in v3, IOCTL_DEV in v5); including a bit the kernel does not know
// makes create_ruleset fail with EINVAL, so the mask is ABI-gated.
func handledAccess(abi int) uint64 {
	a := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		a |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		a |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		a |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return a
}

// readAccess is the access mask granted on a read-only directory: read files
// and list the directory. Execute and truncate are deliberately not included —
// execute is added separately only for the runtime binary/library dirs, and
// truncate would be a write capability.
func readAccess() uint64 {
	return uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
}

// nullDeviceAccess is the minimal mask FFmpeg needs for its /dev/null sink.
// Directory, execute, truncate, rename, and ioctl rights are intentionally not
// granted: the character device is opened and used only for byte I/O.
func nullDeviceAccess() uint64 {
	return uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE)
}

// writeAccessFor is the access mask added for a read-write directory: create and
// write regular files, make/remove the temp and segment files ffmpeg produces,
// and (ABI >= 3) truncate, which the muxers use to finalize files. Directory
// and special-file creation are included because os.CreateTemp-style temp files
// and ffmpeg's atomic rename land here.
func writeAccessFor(abi int) uint64 {
	w := uint64(unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR)
	if abi >= 2 {
		w |= unix.LANDLOCK_ACCESS_FS_REFER // rename across the granted tree
	}
	if abi >= 3 {
		w |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	return w
}

// landlockCreateRuleset wraps the SYS_LANDLOCK_CREATE_RULESET syscall, returning
// the ruleset file descriptor.
func landlockCreateRuleset(attr *unix.LandlockRulesetAttr) (int, error) {
	fd, _, errno := unix.Syscall(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(attr)),
		unsafe.Sizeof(*attr),
		0,
	)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

// landlockAddRule wraps the SYS_LANDLOCK_ADD_RULE syscall for a PATH_BENEATH rule.
func landlockAddRule(rulesetFD int, beneath *unix.LandlockPathBeneathAttr) error {
	_, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFD),
		uintptr(unix.LANDLOCK_RULE_PATH_BENEATH),
		uintptr(unsafe.Pointer(beneath)),
		0, 0, 0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// landlockRestrictSelf wraps the SYS_LANDLOCK_RESTRICT_SELF syscall, enforcing
// the ruleset on the current thread group from this point forward.
func landlockRestrictSelf(rulesetFD int) error {
	_, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFD), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
