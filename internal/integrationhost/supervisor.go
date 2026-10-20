package integrationhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// SocketFlag is the CLI flag the supervisor passes the launched worker, naming
// the unix-domain socket the worker connects back to for the Integration Host
// API. The worker reads its capability token from fd3 (CapabilityTokenFD), never
// the environment — env is inherited by any child the worker spawns, which would
// be a privilege-escalation path.
const SocketFlag = "--host-api-socket"

// CapabilityTokenFD is the file descriptor the supervisor passes the one-shot
// capability token on. In the launched worker this is the first extra file
// (os.NewFile(3, ...)), because ExtraFiles[0] maps to fd 3.
const CapabilityTokenFD = 3

// WorkerRlimits bundles the compile-time rlimit ceilings the worker-shim applies
// to each integration worker child. Exported so cmd/beamfall's worker-shim reads
// one source of truth rather than duplicating the numbers (mirrors
// internal/ffmpeg.Rlimits / DefaultRlimits for the ffmpeg-shim seam).
type WorkerRlimits struct {
	CPUSeconds uint64
	ASBytes    uint64
	NoFile     uint64
}

// DefaultWorkerRlimits returns the rlimit ceilings applied to every integration
// worker child. The worker-shim calls this to learn what limits to
// set on itself before exec'ing the worker artifact.
//
// A worker is an integration glue process, not a transcoder: far tighter ceilings
// than the ffmpeg defaults. 30 min CPU, 4 GiB address space, 256 fds.
//
// RLIMIT_AS counts every mapping, including PROT_NONE reservations. On 64-bit
// Linux the Go runtime reserves about 1.1 GiB of address space before its first
// allocation (page-summary levels plus the scavenge index for a 48-bit heap), so
// a Go worker's VmSize is ~1.2 GiB at startup. Under a 1 GiB ceiling a Go worker
// died nondeterministically with a runtime fatal error (exit 2 or SIGSEGV)
// depending on its ASLR layout: 2 of 200 starts on linux/arm64, and the
// integrationhost process-tree tests on the amd64 race lane. 4 GiB leaves that
// reservation plus ~2.8 GiB of real growth while still bounding a runaway map.
func DefaultWorkerRlimits() WorkerRlimits {
	return WorkerRlimits{
		CPUSeconds: 30 * 60,
		ASBytes:    4 << 30,
		NoFile:     256,
	}
}

// shimSubcommand is the hidden server subcommand the supervisor re-execs the
// worker artifact through. Intentionally undocumented in the top-level help.
const shimSubcommand = "worker-shim"

// ShimEnvVar names an explicit override for the worker-shim binary path. When
// set, it wins over os.Executable in DefaultShimPath — for deployments where
// os.Executable is unreliable, or a test exercising the real shim.
const ShimEnvVar = "BEAMFALL_WORKER_SHIM"

// DefaultShimPath resolves the beamfall binary to re-exec as the worker-shim
// (ADR-0058 §4, mirroring internal/ffmpeg.DefaultShimPath). Returns "" when the
// path cannot be resolved; Supervisor.Run then fails closed instead of launching
// an uncontained worker. The server entrypoint calls SetHardeningShim at startup.
func DefaultShimPath() string {
	if override := os.Getenv(ShimEnvVar); override != "" {
		return override
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// Supervisor launches and supervises one Integration Worker out-of-process. It
// opens the Host API socket, mints a one-shot capability token, launches the
// worker in a platform-owned process tree (plus rlimits and Linux Pdeathsig),
// runs the token handshake, and serves Host API requests until the worker exits
// or the context is canceled. A worker exit never crashes the server.
type Supervisor struct {
	pluginID    string
	artifact    Artifact
	host        *Host
	logger      *slog.Logger
	now         func() time.Time
	shimPath    string // mandatory re-exec binary for rlimits + parent-loss guardian
	sandboxPath string // mandatory platform sandbox executable

	mu       sync.Mutex
	token    string // one-shot capability token; cleared after handshake
	listener net.Listener
	running  bool

	writeMu          sync.Mutex
	actionMu         sync.Mutex
	actionConn       net.Conn
	actionTimeout    time.Duration
	handshakeTimeout time.Duration
	capabilities     []string
	pendingActions   map[string]chan error
	generation       *GenerationLease
}

// DefaultActionTimeout bounds one worker action independently of the caller's
// context. Actions may involve remote services, so the default is deliberately
// generous while still preventing a silent worker from pinning a request
// goroutine forever.
const DefaultActionTimeout = 5 * time.Minute

// NewSupervisor binds a supervisor to one verified artifact and its Host. The
// artifact MUST already be verified (Artifact.Verify) before this is called —
// the supervisor launches it, it does not re-verify (Install/Lifecycle own that).
func NewSupervisor(pluginID string, artifact Artifact, host *Host, logger *slog.Logger, now func() time.Time) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Supervisor{pluginID: pluginID, artifact: artifact, host: host, logger: logger, now: now, actionTimeout: DefaultActionTimeout, sandboxPath: defaultWorkerSandboxPath()}
}

// SetActionTimeout configures the host-side worker-action deadline. Non-positive
// values restore DefaultActionTimeout. Set it before serving worker requests.
func (s *Supervisor) SetActionTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = DefaultActionTimeout
	}
	s.actionTimeout = timeout
}

func (s *Supervisor) configuredActionTimeout() time.Duration {
	if s.actionTimeout <= 0 {
		return DefaultActionTimeout
	}
	return s.actionTimeout
}

// SetHardeningShim points the Supervisor at the beamfall binary to re-exec as
// the worker-shim (ADR-0058 §4, mirrors internal/ffmpeg.(*Runner).SetHardeningShim).
// Call SetHardeningShim(DefaultShimPath()) before Run. Run fails closed when no
// shim is configured because the shim owns the required parent-loss guardian.
func (s *Supervisor) SetHardeningShim(path string) { s.shimPath = path }

// SetSandboxBinary overrides the platform sandbox executable. It exists for
// deterministic packaging and tests; an empty path makes launch fail closed.
func (s *Supervisor) SetSandboxBinary(path string) { s.sandboxPath = path }

// SetGenerationLease binds this supervisor to one manager-owned launch
// generation. Stale workers may remain alive only during bounded retirement;
// their Host API calls are refused after the lease is retired.
func (s *Supervisor) SetGenerationLease(lease *GenerationLease) { s.generation = lease }

// workerCommand builds the exec.Cmd that launches the worker artifact, wrapping
// it in the mandatory worker-shim re-exec. The shim starts the parent-loss
// guardian, applies rlimits, then execs the artifact in place (same PID), so
// the Linux Pdeathsig guard still binds.
func (s *Supervisor) workerCommand(artifactPath string, artifactArgs, shimOptions []string, runtimeDir string) (*exec.Cmd, error) {
	if s.shimPath == "" {
		return nil, ErrWorkerShimRequired
	}
	// worker-shim -- <artifact> <args...>
	shimArgs := append([]string{shimSubcommand}, shimOptions...)
	shimArgs = append(shimArgs, "--", artifactPath)
	shimArgs = append(shimArgs, artifactArgs...)
	return sandboxedWorkerCommand(s.sandboxPath, s.shimPath, artifactPath, shimArgs, runtimeDir, s.artifact.WritablePaths)
}

// ErrWorkerExited is returned by Run when the worker process exits (cleanly or
// not). The supervisor never propagates a worker crash as a server failure; the
// caller decides whether to restart per the lifecycle policy.
var ErrWorkerExited = errors.New("integrationhost: worker exited")

// ErrWorkerAlreadyRunning rejects overlapping Run calls on one Supervisor.
var ErrWorkerAlreadyRunning = errors.New("integrationhost: worker supervisor already running")

// ErrPluginDeactivated is the cancellation cause reported when an operator
// deactivates a plugin while its Integration Worker is running.
var ErrPluginDeactivated = errors.New("integrationhost: plugin deactivated; worker canceled")

// Run launches the worker and serves the Host API until ctx is canceled or the
// worker exits. The runtime sequence:
//
//	(1) open a unix-domain socket listener in a per-worker dir;
//	(2) mint a one-shot capability token;
//	(3) launch the worker hardened, passing the socket path as a CLI flag and
//	    the token on fd3 (never env);
//	(4) the worker reads fd3, connects, presents the token in a length-prefixed
//	    JSON frame; the supervisor verifies it, atomically clears it (one-shot),
//	    and acks; a leaked token copy is then inert;
//	(5) serve Host API frames until the worker disconnects or ctx ends.
//
// A worker process exit returns ErrWorkerExited (crash containment).
func (s *Supervisor) Run(ctx context.Context) error {
	if !s.claimRun() {
		return ErrWorkerAlreadyRunning
	}
	defer s.releaseRun()
	if err := validateWorkerLaunch(s.shimPath); err != nil {
		return err
	}
	if err := s.artifact.Verify(Pin{Kind: PinHash, Hash: s.artifact.VerifiedSHA256}); err != nil {
		return fmt.Errorf("integrationhost: verify executable object before launch: %w", err)
	}
	sockDir, err := os.MkdirTemp("", "bf-worker-"+s.pluginID+"-")
	if err != nil {
		return fmt.Errorf("integrationhost: socket dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	sockPath := filepath.Join(sockDir, "host.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("integrationhost: listen: %w", err)
	}
	defer func() { _ = listener.Close() }()
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	token, err := mintToken()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.token = token
	s.mu.Unlock()

	tokenR, tokenW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("integrationhost: token pipe: %w", err)
	}
	defer func() { _ = tokenR.Close() }()

	guardianR, guardianW, guardianArgs, err := newWorkerGuardianLifetime()
	if err != nil {
		_ = tokenW.Close()
		return fmt.Errorf("integrationhost: guardian lifetime pipe: %w", err)
	}
	if guardianR != nil {
		defer func() { _ = guardianR.Close() }()
		defer func() { _ = guardianW.Close() }()
	}

	artifactArgs := append(append([]string(nil), s.artifact.Args...), SocketFlag, sockPath)
	cmd, err := s.workerCommand(s.artifact.Path, artifactArgs, guardianArgs, sockDir)
	if err != nil {
		_ = tokenW.Close()
		return err
	}
	cmd.Env = []string{} // clean env: the worker (and shim) inherit nothing
	cmd.Dir = sockDir
	cmd.ExtraFiles = []*os.File{tokenR} // becomes fd3 in the child
	if guardianR != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, guardianR) // fd4: guardian-only supervisor lifetime
	}
	applyWorkerHardening(cmd, DefaultWorkerRlimits())

	serveDone := make(chan struct{})
	started := false
	waitErr := superviseWorker(ctx, cmd, func() {
		started = true
		// Write the token to fd3 then close so the worker reads to EOF.
		go func() {
			_, _ = tokenW.WriteString(token)
			_ = tokenW.Close()
		}()
		// Serve in the background; wait for the worker to exit or ctx to cancel.
		go func() {
			defer close(serveDone)
			s.serve(ctx)
		}()
	})
	if !started {
		_ = tokenW.Close()
		return waitErr
	}
	_ = listener.Close()
	if started {
		<-serveDone
	}
	if cause := context.Cause(ctx); cause != nil {
		if waitErr == nil || errors.Is(waitErr, cause) {
			return cause
		}
		return waitErr
	}
	if waitErr != nil {
		s.logger.Warn("integration worker exited with error", "plugin", s.pluginID, "error", waitErr)
	}
	return ErrWorkerExited
}

func (s *Supervisor) claimRun() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	return true
}

func (s *Supervisor) releaseRun() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

// serve accepts Host API connections until the listener closes. The first frame
// on a connection must be the capability-token handshake; subsequent frames are
// Host API requests. Each connection is bound to this supervisor's single
// pluginID via its Host, so a worker can never act as another plugin.
func (s *Supervisor) serve(ctx context.Context) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed (worker exited / ctx canceled)
		}
		go s.handleConn(ctx, conn)
	}
}

// mintToken returns a fresh 256-bit hex one-shot capability token.
func mintToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("integrationhost: token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// consumeToken atomically verifies and clears the one-shot token. A second
// presentation (a leaked copy) fails because the token is already cleared.
func (s *Supervisor) consumeToken(presented string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == "" || presented == "" {
		return false
	}
	if !constantTimeStringEqual(presented, s.token) {
		return false
	}
	s.token = "" // one-shot: revoke immediately
	return true
}
