//go:build linux

package integrationhost

import (
	"os/exec"
	"path/filepath"
)

func defaultWorkerSandboxPath() string {
	path, _ := exec.LookPath("bwrap")
	return path
}

func sandboxedWorkerCommand(sandboxPath, shimPath, artifactPath string, shimArgs []string, runtimeDir string, writablePaths []string) (*exec.Cmd, error) {
	if sandboxPath == "" {
		return nil, ErrWorkerSandboxRequired
	}
	const sandboxRoot = "/run/beamfall-worker"
	sandboxShim := filepath.Join(sandboxRoot, "shim")
	sandboxArtifact := filepath.Join(sandboxRoot, "artifact")
	sandboxRuntime := filepath.Join(sandboxRoot, "runtime")
	rewritten := append([]string(nil), shimArgs...)
	for i, value := range rewritten {
		switch value {
		case shimPath:
			rewritten[i] = sandboxShim
		case artifactPath:
			rewritten[i] = sandboxArtifact
		case filepath.Join(runtimeDir, "host.sock"):
			rewritten[i] = filepath.Join(sandboxRuntime, "host.sock")
		}
	}
	args := []string{
		"--die-with-parent", "--new-session", "--unshare-all",
		"--dir", "/run",
		"--dir", sandboxRoot,
		"--ro-bind", shimPath, sandboxShim,
		"--ro-bind", artifactPath, sandboxArtifact,
		"--ro-bind-try", "/bin", "/bin",
		"--ro-bind-try", "/usr", "/usr",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--bind", runtimeDir, sandboxRuntime,
		"--dev", "/dev", "--proc", "/proc",
	}
	for _, path := range writablePaths {
		args = append(args, "--bind", path, path)
	}
	args = append(args, "--", sandboxShim)
	args = append(args, rewritten...)
	// #nosec G204 -- sandboxPath and shimPath are trusted server configuration; artifactPath is a verified content-addressed object.
	return exec.Command(sandboxPath, args...), nil
}
