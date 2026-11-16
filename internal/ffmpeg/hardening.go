package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
)

// FFmpeg invocation hardening. Every ffmpeg/ffprobe spawn is
// wrapped in a re-exec of the server binary's hidden "ffmpeg-shim" subcommand,
// which lowers the child's resource limits (rlimits, portable) and — on Linux —
// confines its filesystem reach to only the paths the job needs (landlock),
// then execs the real ffmpeg in place (same PID, so the Pdeathsig orphan guard
// still binds). A malicious media file that exploits ffmpeg therefore runs with
// bounded CPU/memory/output and, on Linux, cannot read or write outside the
// input file's directory and the output directory.
//
// The shim binary is the server itself (os.Executable). When the path cannot be
// resolved (e.g. the test binary, or an exotic deployment), wrapping is skipped
// and ffmpeg launches directly — the same un-hardened behavior as before this
// change, never a boot/run failure. Hardening is defense-in-depth layered on
// top of option-injection rejection (checkPath), not a correctness dependency.

// rlimit defaults applied to every ffmpeg/ffprobe child. These
// are deliberately generous so a legitimate 4K transcode never trips them, while
// still capping a runaway exploit. They are consts, not config: the resource
// governor's knobs (niceness, worker count, CPU budget) shape scheduling, while
// these are a hard safety ceiling on a single FFmpeg process.
const (
	// rlimitCPUSeconds caps total CPU time per process (RLIMIT_CPU). A long
	// transcode is wall-clock long but CPU-bounded per process; the kernel sends
	// SIGXCPU at the soft limit. 6 hours covers a slow software transcode of a
	// feature-length title on weak hardware with wide margin.
	rlimitCPUSeconds = 6 * 60 * 60
	// rlimitASBytes caps the process address space (RLIMIT_AS) — virtual memory.
	// FFmpeg's working set for HD/4K decode+encode is well under this; the cap
	// stops an exploit from mapping unbounded memory. 8 GiB.
	rlimitASBytes = 8 << 30
	// rlimitFSizeBytes caps the size of any single file the process may write
	// (RLIMIT_FSIZE). A transcoded rendition or generated asset is far smaller;
	// the cap stops a decompression-bomb / infinite-output write. 32 GiB.
	rlimitFSizeBytes = 32 << 30
	// rlimitNoFile caps open file descriptors (RLIMIT_NOFILE). FFmpeg opens the
	// input, outputs, and a handful of pipes; 512 is ample and bounds an fd
	// exhaustion attempt.
	rlimitNoFile = 512
)

// shimSubcommand is the hidden server subcommand the spawn seam re-execs into.
// It is intentionally undocumented in the top-level command switch's help: it is
// an internal hardening detail, not an operator-facing command.
const shimSubcommand = "ffmpeg-shim"

// hardenedCommand builds the exec.Cmd that actually launches ffmpeg/ffprobe,
// wrapping it in the re-exec shim when this Runner has a shim binary configured.
// roPaths are confined read-only, rwPaths read-write (Linux landlock); rlimits
// apply on every platform. When the Runner has no shim (the default — set only
// by the server entrypoint via SetHardeningShim), the command runs ffmpeg
// directly (un-hardened fallback). The returned command has not yet had
// applySysProcAttr called on it — callers still do that, exactly as before.
//
// Hardening is opt-in by the production server rather than on by default so a
// test binary (or any non-server process using this package) never re-execs
// itself as a "shim" — that would run the host's whole test suite recursively.
// The server knows it is the real beamfall binary and enables the shim
// explicitly; everyone else gets the un-hardened direct exec they had before.
func (r *Runner) hardenedCommand(ctx context.Context, binPath string, roPaths, rwPaths []string, args []string) *exec.Cmd {
	return r.confinedCommand(ctx, binPath, confinement{ReadOnly: roPaths, ReadWrite: rwPaths}, args)
}

// confinedCommand is hardenedCommand for a full confinement, including the
// GPU grant a hardware job needs.
func (r *Runner) confinedCommand(ctx context.Context, binPath string, conf confinement, args []string) *exec.Cmd {
	return shimCommand(ctx, r.shimPath, binPath, conf, args)
}

// shimCommand wraps binPath in the shim at shimPath under conf, or execs it
// directly when shimPath is empty. Transcodes take shimPath from their Runner
// and hardware probes from SetProbeHardeningShim, so a probe runs under the
// same wrapping as the job it certifies.
func shimCommand(ctx context.Context, shimPath, binPath string, conf confinement, args []string) *exec.Cmd {
	if shimPath == "" {
		return exec.CommandContext(ctx, binPath, args...)
	}
	// #nosec G204 -- shimPath is this server binary and the argv is built as a
	// []string by buildShimArgs, never a shell string.
	return exec.CommandContext(ctx, shimPath, buildShimArgs(conf, binPath, args)...)
}

// buildShimArgs assembles the argv for the ffmpeg-shim subcommand:
//
//	ffmpeg-shim [--dri] --ro <dir> [--ro <dir>...] --rw <dir> [--rw <dir>...] -- <ffmpeg> <args...>
//
// The rlimit values are not passed on the command line — they are compile-time
// consts the shim reads directly — keeping the argv tied to the per-job paths
// only. The "--" terminator separates shim flags from the ffmpeg command so a
// path that looks like a flag cannot be misparsed by the shim's own flag set.
func buildShimArgs(conf confinement, binPath string, args []string) []string {
	out := []string{shimSubcommand}
	if conf.DRI {
		out = append(out, "--dri")
	}
	for _, p := range conf.ReadOnly {
		out = append(out, "--ro", p)
	}
	for _, p := range conf.ReadWrite {
		out = append(out, "--rw", p)
	}
	out = append(out, "--")
	out = append(out, binPath)
	out = append(out, args...)
	return out
}

// probeShimPath is the shim the hardware probes run through. The probes are
// package functions, and the encoder probe runs before any Runner exists, so
// the server entrypoint sets it once with SetProbeHardeningShim alongside the
// Runner's own shim.
var probeShimPath atomic.Pointer[string]

// SetProbeHardeningShim makes the encoder, tone-map and device-decode probes
// run FFmpeg through the hardening shim at path, under the confinement their
// transcode gets (CORE-2158), so a verdict never certifies a chain the
// confined job cannot open. An empty path, the default, execs FFmpeg directly,
// for the same reason SetHardeningShim defaults off.
//
//io:ignore — config setter, not an I/O operation (cmd/ioe2e ledger).
func SetProbeHardeningShim(path string) {
	if path == "" {
		probeShimPath.Store(nil)
		return
	}
	probeShimPath.Store(&path)
}

func probeShim() string {
	if path := probeShimPath.Load(); path != nil {
		return *path
	}
	return ""
}

// probeCommand builds one hardware probe's FFmpeg command under conf.
func probeCommand(ctx context.Context, ffmpegPath string, conf confinement, args []string) *exec.Cmd {
	return shimCommand(ctx, probeShim(), ffmpegPath, conf, args)
}

// driAccel reports whether an accelerator's encode or tone-map opens a DRM
// render node and, for the OpenCL tone-map, the OpenCL ICD registry: QSV and
// VA-API. NVENC, VideoToolbox, AMF and the software path need neither.
func driAccel(accel HWAccel) bool {
	return accel == HWAccelQSV || accel == HWAccelVAAPI
}

// ShimEnvVar names an explicit override for the shim binary path. When set, it
// wins over os.Executable in DefaultShimPath — for a deployment where
// os.Executable is unreliable, or a test exercising the real shim.
const ShimEnvVar = "BEAMFALL_FFMPEG_SHIM"

// DefaultShimPath resolves the beamfall binary to re-exec as the hardening shim:
// the BEAMFALL_FFMPEG_SHIM override if set, else os.Executable.
// It returns "" when the path cannot be resolved, in which case SetHardeningShim
// leaves hardening off (un-hardened direct exec — never a boot failure). The
// server entrypoint calls SetHardeningShim(DefaultShimPath()) at startup; the
// split lets a caller substitute a known binary in a test.
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

// confinement names the filesystem paths a single ffmpeg job is allowed to
// touch: the input file's directory read-only, and the output
// directory read-write. The landlock shim (Linux) enforces exactly this set;
// on other platforms it is advisory (rlimits still apply, filesystem reach is
// not confined). Directories — not files — are granted: ffmpeg opens sidecars,
// temp files, and segment files alongside the named paths, and a directory
// grant is the minimal set that keeps a legitimate job working.
//
// DRI adds the render nodes and the OpenCL ICD registry for a job that encodes
// or tone-maps on QSV or VA-API (CORE-2158); every other job leaves it false.
type confinement struct {
	ReadOnly  []string
	ReadWrite []string
	DRI       bool
}

// readConfinement confines a read-only job (e.g. ffprobe, waveform analysis)
// to the input file's directory only. The input's directory is granted
// read-only; nothing is writable.
func readConfinement(inputPath string) confinement {
	return confinement{ReadOnly: dedupeDirs(dirOf(inputPath))}
}

// transformConfinement confines a job that reads an input and writes outputs:
// the input file's directory read-only, plus each output directory read-write.
// When the input lives in an output directory (rare, but possible for an
// in-place asset), the read-write grant subsumes the read-only one and the
// input dir is dropped from the read-only set to avoid a redundant rule.
func transformConfinement(inputPath string, outputPaths ...string) confinement {
	rw := make([]string, 0, len(outputPaths))
	for _, o := range outputPaths {
		rw = append(rw, dirOf(o))
	}
	rw = dedupeDirs(rw...)
	ro := dedupeDirs(dirOf(inputPath))
	ro = subtract(ro, rw)
	return confinement{ReadOnly: ro, ReadWrite: rw}
}

// dirConfinement confines a job that reads an input file and writes into an
// output directory directly (e.g. HLS — many segments into one dir): the input
// file's directory read-only, the output directory read-write. Unlike
// transformConfinement, outputDir is granted as-is (it is already a directory),
// not via a file path's parent.
func dirConfinement(inputPath, outputDir string) confinement {
	rw := dedupeDirs(outputDir)
	ro := subtract(dedupeDirs(dirOf(inputPath)), rw)
	return confinement{ReadOnly: ro, ReadWrite: rw}
}

// dirOf returns the cleaned directory containing path. A path that is already a
// directory pattern (e.g. an HLS segment template's directory was passed) is
// handled by the caller passing the directory directly; here path is always a
// file path whose parent is the grant.
func dirOf(path string) string {
	return filepath.Dir(filepath.Clean(path))
}

// dedupeDirs cleans and de-duplicates a set of directory paths, dropping empties.
func dedupeDirs(dirs ...string) []string {
	seen := make(map[string]struct{}, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d == "" {
			continue
		}
		c := filepath.Clean(d)
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// subtract returns the elements of a not present in b.
func subtract(a, b []string) []string {
	bset := make(map[string]struct{}, len(b))
	for _, x := range b {
		bset[x] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, x := range a {
		if _, ok := bset[x]; !ok {
			out = append(out, x)
		}
	}
	return out
}

// Rlimits bundles the compile-time rlimit ceilings the shim applies to each
// ffmpeg child. It is exported so the cmd/beamfall shim reads one source of
// truth (the consts above) rather than duplicating the numbers.
type Rlimits struct {
	CPUSeconds uint64
	ASBytes    uint64
	FSizeBytes uint64
	NoFile     uint64
}

// DefaultRlimits returns the rlimit ceilings applied to every ffmpeg child.
// The shim calls this to learn what limits to set on itself
// before exec'ing ffmpeg.
func DefaultRlimits() Rlimits {
	return Rlimits{
		CPUSeconds: rlimitCPUSeconds,
		ASBytes:    rlimitASBytes,
		FSizeBytes: rlimitFSizeBytes,
		NoFile:     rlimitNoFile,
	}
}
