//go:build linux

package main

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/beamfall/core/internal/integrationhost"
)

// seccompNativeArch maps the compiled target to its AUDIT_ARCH token. A
// syscall-number filter is only meaningful for one ABI: the same number means
// different calls under a foreign personality, so the filter must verify the
// architecture word before it trusts the number. An unlisted target has no
// verifiable token, so the profile is refused rather than installed blind.
var seccompNativeArch = map[string]uint32{
	"amd64":   unix.AUDIT_ARCH_X86_64,
	"arm64":   unix.AUDIT_ARCH_AARCH64,
	"arm":     unix.AUDIT_ARCH_ARM,
	"riscv64": unix.AUDIT_ARCH_RISCV64,
	"ppc64le": unix.AUDIT_ARCH_PPC64LE,
	"s390x":   unix.AUDIT_ARCH_S390X,
}

// noNetworkFilter builds the CPU-only seccomp program: refuse socket creation
// under the native ABI, refuse everything under any other ABI, allow the rest.
func noNetworkFilter(arch uint32) []unix.SockFilter {
	const denyErrno = unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)
	return []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: arch},
		{Code: unix.BPF_RET | unix.BPF_K, K: denyErrno},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: unix.SYS_SOCKET},
		{Code: unix.BPF_RET | unix.BPF_K, K: denyErrno},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: unix.SYS_SOCKETPAIR},
		{Code: unix.BPF_RET | unix.BPF_K, K: denyErrno},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
}

func applyNoNetwork() error {
	arch, ok := seccompNativeArch[runtime.GOARCH]
	if !ok {
		return fmt.Errorf("%w: no seccomp architecture token for linux/%s",
			integrationhost.ErrLocalInferenceConfinementUnavailable, runtime.GOARCH)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	filter := noNetworkFilter(arch)
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	return unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&prog)), 0, 0)
}

// applyLocalInferenceNoNetwork installs the same profile on linux, where it is
// kernel-enforced. It exists as its own entry point so the non-linux build can
// refuse instead of returning a silent no-op.
func applyLocalInferenceNoNetwork() error { return applyNoNetwork() }
