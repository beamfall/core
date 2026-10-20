package integrationhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const localInferenceShimSubcommand = "local-inference-shim"

const localInferenceOutputLimit = 1 << 20

// LocalInferenceRequest is the CPU-only local inference exec contract. The
// child sees exactly three data roots: read-only media input, read-only model
// artifacts, and a read-write work root.
type LocalInferenceRequest struct {
	MediaID   string
	InputRoot string
	ModelRoot string
	WorkRoot  string
	Binary    string
	Args      []string
}

type LabelConfidence struct {
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
}

type MediaLabelResult struct {
	MediaID string            `json:"media_id"`
	Labels  []LabelConfidence `json:"labels"`
}

type LocalInferenceRuntime struct {
	ShimPath string
}

// ErrLocalInferenceConfinementUnavailable reports a host that cannot enforce
// the ADR-0127 §1 profile. Local inference refuses to launch there rather than
// running the child with a partial policy.
var ErrLocalInferenceConfinementUnavailable = errors.New("integrationhost: local inference confinement unavailable")

// LocalInferenceMinLandlockABI is the lowest landlock ABI that can enforce the
// read-only half of the ADR-0127 §1 profile.
//
// LANDLOCK_ACCESS_FS_TRUNCATE only exists from ABI 3. A ruleset built on ABI 1
// or 2 (kernels 5.13–6.1, which includes Debian 12) therefore leaves truncate(2)
// and open(O_TRUNC) outside the handled set entirely, so they are neither
// granted nor denied by the ruleset — the read-only model and input roots stay
// emptiable and the read-only guarantee does not hold. Landlock cannot be
// narrowed to cover it after the fact, so the launch is refused there.
const LocalInferenceMinLandlockABI = 3

// LocalInferenceSandboxExecPath is the fixed system location of the darwin
// confinement binary.
//
// It is deliberately not resolved through exec.LookPath: for a component whose
// only job is OS-enforced confinement, taking the confining binary from $PATH
// means any $PATH entry ahead of /usr/bin decides what confines the child, and
// a substituted binary would run the payload unconfined while every caller
// still believes the profile was applied. Pinning the absolute path makes the
// enforcement target independent of the server's environment.
const LocalInferenceSandboxExecPath = "/usr/bin/sandbox-exec"

// LocalInferenceLandlockEnforceable reports whether a kernel-reported landlock
// ABI can enforce the profile. It is the platform-independent half of the linux
// confinement decision, kept here so the refusal threshold stays assertable on a
// host that has no landlock at all.
func LocalInferenceLandlockEnforceable(abi int) bool {
	return abi >= LocalInferenceMinLandlockABI
}

// Command builds the confined child command. The launch is refused — never
// narrowed — when the inventory cannot be satisfied or the host has no
// enforcement path: darwin confines through sandbox-exec, linux re-execs the
// hardening shim (seccomp + landlock), and every other platform is refused.
func (r LocalInferenceRuntime) Command(ctx context.Context, req LocalInferenceRequest) (*exec.Cmd, error) {
	policy, err := validatedLocalInferencePolicy(req, rootOwnedByCurrentUser)
	if err != nil {
		return nil, err
	}
	switch runtime.GOOS {
	case "darwin":
		sandboxExec, err := confinementBinary(LocalInferenceSandboxExecPath)
		if err != nil {
			return nil, err
		}
		profile, err := LocalInferenceSandboxProfile(policy.InputRoot, policy.ModelRoot, policy.WorkRoot)
		if err != nil {
			return nil, err
		}
		args := append([]string{"-p", profile, "--", policy.Binary}, req.Args...)
		// #nosec G204 -- sandboxExec is the fixed absolute constant
		// LocalInferenceSandboxExecPath, never a $PATH lookup, and is verified
		// by confinementBinary to be a regular executable file; policy.Binary
		// passed resolveConfinedBinary and the child runs under the profile.
		return exec.CommandContext(ctx, sandboxExec, args...), nil
	case "linux":
		shim, err := r.confinementShim()
		if err != nil {
			return nil, err
		}
		args := []string{
			localInferenceShimSubcommand,
			"--input-ro", policy.InputRoot,
			"--model-ro", policy.ModelRoot,
			"--work-rw", policy.WorkRoot,
			"--", policy.Binary,
		}
		args = append(args, req.Args...)
		// #nosec G204 -- shim is the deployment's own beamfall binary, resolved
		// by confinementShim from the operator-set BEAMFALL_WORKER_SHIM path or
		// os.Executable and verified there to be a regular executable file;
		// argv is this launcher's fixed flag set over already-validated roots.
		return exec.CommandContext(ctx, shim, args...), nil
	default:
		return nil, fmt.Errorf("%w: no enforcement path on %s", ErrLocalInferenceConfinementUnavailable, runtime.GOOS)
	}
}

// confinementShim resolves the re-exec target that applies the kernel profile.
// A missing shim binary is an unavailable enforcement path, not a degraded one.
func (r LocalInferenceRuntime) confinementShim() (string, error) {
	shim := r.ShimPath
	if shim == "" {
		shim = DefaultShimPath()
	}
	if shim == "" {
		return "", fmt.Errorf("%w: no hardening shim path", ErrLocalInferenceConfinementUnavailable)
	}
	return confinementBinary(shim)
}

// confinementBinary admits an enforcement helper only when the path names a
// real executable file. Presence alone is not enough: a directory or a
// non-executable file at the expected location is a host that cannot enforce
// the profile, and local inference refuses there rather than launching.
func confinementBinary(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%w: confinement binary %q: %w", ErrLocalInferenceConfinementUnavailable, path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: confinement binary %q is not a regular file", ErrLocalInferenceConfinementUnavailable, path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%w: confinement binary %q is not executable", ErrLocalInferenceConfinementUnavailable, path)
	}
	return path, nil
}

func (r LocalInferenceRuntime) Run(ctx context.Context, req LocalInferenceRequest) (MediaLabelResult, error) {
	cmd, err := r.Command(ctx, req)
	if err != nil {
		return MediaLabelResult{}, err
	}
	stdout, err := runConfinedChild(ctx, cmd)
	if err != nil {
		return MediaLabelResult{}, fmt.Errorf("integrationhost: local inference: %w", err)
	}
	var result MediaLabelResult
	if err := json.Unmarshal(stdout, &result); err != nil {
		return MediaLabelResult{}, fmt.Errorf("integrationhost: decode local inference labels: %w", err)
	}
	if result.MediaID == "" {
		result.MediaID = req.MediaID
	}
	if result.MediaID != req.MediaID {
		return MediaLabelResult{}, fmt.Errorf("integrationhost: local inference media mismatch %q != %q", result.MediaID, req.MediaID)
	}
	return result, nil
}

// runConfinedChild bounds both output streams and retires the complete process
// tree on success, failure, cancellation, or an output-limit violation.
func runConfinedChild(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stdout := newBoundedInferenceOutput("stdout", cancel)
	stderr := newBoundedInferenceOutput("stderr", cancel)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := superviseWorker(runCtx, cmd, nil)
	if limitErr := stdout.limitError(); limitErr != nil {
		return nil, limitErr
	}
	if limitErr := stderr.limitError(); limitErr != nil {
		return nil, limitErr
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

var errLocalInferenceMediaID = errors.New("integrationhost: local inference media id required")

// ValidateLocalInferenceRequest accepts a request only when the whole
// confinement inventory is satisfiable, so an unsatisfiable policy is refused
// before anything is launched.
func ValidateLocalInferenceRequest(req LocalInferenceRequest) error {
	_, err := validatedLocalInferencePolicy(req, rootOwnedByCurrentUser)
	return err
}

func validatedLocalInferencePolicy(req LocalInferenceRequest, ownerCheck func(string) (bool, error)) (LocalInferencePolicy, error) {
	if req.MediaID == "" {
		return LocalInferencePolicy{}, errLocalInferenceMediaID
	}
	policy, err := BuildLocalInferencePolicy(req.InputRoot, req.ModelRoot, req.WorkRoot, req.Binary)
	if err != nil {
		return LocalInferencePolicy{}, err
	}
	roots := []struct {
		name string
		path string
	}{
		{name: "input root", path: policy.InputRoot},
		{name: "model root", path: policy.ModelRoot},
		{name: "work root", path: policy.WorkRoot},
	}
	for _, root := range roots {
		owned, err := ownerCheck(root.path)
		if err != nil {
			return LocalInferencePolicy{}, fmt.Errorf("integrationhost: local inference %s owner: %w", root.name, err)
		}
		if !owned {
			return LocalInferencePolicy{}, fmt.Errorf("integrationhost: local inference %s is not owned by the current user", root.name)
		}
	}
	return policy, nil
}

func CheckLocalInferenceModelRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("integrationhost: model root: %w", err)
	}
	if !info.IsDir() {
		return errors.New("integrationhost: model root is not a directory")
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		return fmt.Errorf("integrationhost: model root perm = %04o, want 0700", perm)
	}
	owned, err := rootOwnedByCurrentUser(root)
	if err != nil {
		return fmt.Errorf("integrationhost: model root owner: %w", err)
	}
	if !owned {
		return errors.New("integrationhost: model root is not owned by the current user")
	}
	return nil
}

type boundedInferenceOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	stream   string
	exceeded bool
	cancel   context.CancelCauseFunc
}

func newBoundedInferenceOutput(stream string, cancel context.CancelCauseFunc) *boundedInferenceOutput {
	return &boundedInferenceOutput{stream: stream, cancel: cancel}
}

func (w *boundedInferenceOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	remaining := localInferenceOutputLimit - w.buffer.Len()
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		_, _ = w.buffer.Write(p[:remaining])
	}
	if remaining < len(p) {
		w.exceeded = true
		w.cancel(w.limitErrorLocked())
	}
	w.mu.Unlock()
	return len(p), nil
}

func (w *boundedInferenceOutput) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buffer.Bytes())
}

func (w *boundedInferenceOutput) String() string {
	return string(w.Bytes())
}

func (w *boundedInferenceOutput) limitError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.exceeded {
		return nil
	}
	return w.limitErrorLocked()
}

func (w *boundedInferenceOutput) limitErrorLocked() error {
	return fmt.Errorf("integrationhost: local inference %s exceeds %d-byte limit", w.stream, localInferenceOutputLimit)
}

func LocalInferenceSandboxProfile(inputRoot, modelRoot, workRoot string) (string, error) {
	inputRoot, err := realSandboxPath(inputRoot)
	if err != nil {
		return "", err
	}
	modelRoot, err = realSandboxPath(modelRoot)
	if err != nil {
		return "", err
	}
	workRoot, err = realSandboxPath(workRoot)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"(version 1)",
		"(deny default)",
		`(import "system.sb")`,
		"(allow process-exec*)",
		"(deny network*)",
		`(deny file-read* ` + sbplSubpaths(sandboxDeniedReadTrees()) + `)`,
		`(allow file-read* (subpath "` + sbplQuote(inputRoot) + `") (subpath "` + sbplQuote(modelRoot) + `") (subpath "` + sbplQuote(workRoot) + `"))`,
		`(allow file-write* (subpath "` + sbplQuote(workRoot) + `"))`,
	}, "\n"), nil
}

// sandboxDeniedReadTrees are the host trees the profile denies explicitly,
// after the system.sb import and before the declared-root grants.
//
// (deny default) alone does not make the profile match LocalInferencePolicy.
// system.sb is load-bearing — without it the child cannot exec at all — but it
// re-grants host reads on its own account, and /etc/passwd was observed
// readable through it while Reach reported ReachNone for the same path. These
// rules close that gap, so an executed refusal covers what the model claims.
//
// /dev is deliberately not in this set. The only device nodes system.sb leaves
// reachable are /dev/null and /dev/urandom (block devices are already refused),
// and denying the tree also revokes the child's own stdout when the caller
// redirects it there.
func sandboxDeniedReadTrees() []string {
	trees := make([]string, 0, len(deniedHostRoots)*2)
	for _, tree := range deniedHostTrees() {
		if pathWithin("/dev", tree) {
			continue
		}
		trees = append(trees, tree)
	}
	return trees
}

// sbplSubpaths renders paths as the (subpath "…") terms an SBPL filter takes.
func sbplSubpaths(paths []string) string {
	terms := make([]string, 0, len(paths))
	for _, path := range paths {
		terms = append(terms, `(subpath "`+sbplQuote(path)+`")`)
	}
	return strings.Join(terms, " ")
}

func realSandboxPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("integrationhost: sandbox path %q: %w", path, err)
	}
	return resolved, nil
}

func sbplQuote(path string) string {
	path = strings.ReplaceAll(path, `\`, `\\`)
	return strings.ReplaceAll(path, `"`, `\"`)
}
