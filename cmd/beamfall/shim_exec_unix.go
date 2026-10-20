//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package main

import "syscall"

func replaceProcess(path string, argv, env []string) error {
	// #nosec G204 G702 -- executing the parsed shim target is this helper's purpose;
	// callers apply the required rlimits and confinement before reaching it.
	return syscall.Exec(path, argv, env)
}
