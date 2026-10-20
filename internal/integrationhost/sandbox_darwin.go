//go:build darwin

package integrationhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func defaultWorkerSandboxPath() string {
	path, _ := exec.LookPath("sandbox-exec")
	return path
}

func sandboxedWorkerCommand(sandboxPath, shimPath, artifactPath string, shimArgs []string, runtimeDir string, writablePaths []string) (*exec.Cmd, error) {
	if sandboxPath == "" {
		return nil, ErrWorkerSandboxRequired
	}
	resolvedShim, err := realSandboxPath(shimPath)
	if err != nil {
		return nil, err
	}
	resolvedArtifact, err := realSandboxPath(artifactPath)
	if err != nil {
		return nil, err
	}
	resolvedRuntime, err := realSandboxPath(runtimeDir)
	if err != nil {
		return nil, err
	}
	profilePath := filepath.Join(runtimeDir, "worker.sb")
	socketPath := filepath.Join(resolvedRuntime, "host.sock")
	rules := []string{
		"(version 1)",
		"(deny default)",
		`(import "system.sb")`,
		"(allow process-fork)",
		"(allow signal (target same-sandbox))",
		`(allow process-exec (literal "` + sbplQuote(resolvedShim) + `") (literal "` + sbplQuote(resolvedArtifact) + `") (literal "/bin/sh"))`,
		`(allow file-read* (literal "` + sbplQuote(resolvedShim) + `") (literal "` + sbplQuote(resolvedArtifact) + `") (subpath "` + sbplQuote(resolvedRuntime) + `"))`,
		`(allow file-write* (subpath "` + sbplQuote(resolvedRuntime) + `"))`,
		`(allow network-outbound (remote unix-socket (path-literal "` + sbplQuote(socketPath) + `")))`,
	}
	for _, path := range writablePaths {
		resolved, err := realSandboxPath(path)
		if err != nil {
			return nil, err
		}
		rules = append(rules,
			`(allow file-read* (subpath "`+sbplQuote(resolved)+`"))`,
			`(allow file-write* (subpath "`+sbplQuote(resolved)+`"))`,
		)
	}
	profile := strings.Join(rules, "\n")
	if err := os.WriteFile(profilePath, []byte(profile), 0o600); err != nil {
		return nil, fmt.Errorf("integrationhost: write worker sandbox profile: %w", err)
	}
	args := append([]string{"-f", profilePath, shimPath}, shimArgs...)
	// #nosec G204 -- sandboxPath and shimPath are trusted server configuration; artifactPath is a verified content-addressed object.
	return exec.Command(sandboxPath, args...), nil
}
