package ffmpeg

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// pinSpawnPlatform makes the spawn seam build goos's confinement for the rest
// of the test, so argv-shape tests hold on any host.
func pinSpawnPlatform(t *testing.T, goos string) {
	t.Helper()
	prev := spawnGOOS
	spawnGOOS = goos
	t.Cleanup(func() { spawnGOOS = prev })
}

// pinSandboxExec points the macOS launcher at path for the rest of the test.
func pinSandboxExec(t *testing.T, path string) {
	t.Helper()
	prev := sandboxExecPath
	sandboxExecPath = path
	t.Cleanup(func() { sandboxExecPath = prev })
}

// fakeSandboxExec is an executable file standing in for /usr/bin/sandbox-exec,
// so the darwin command shape is checkable on a host without it.
func fakeSandboxExec(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sandbox-exec")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fake executable must be executable
		t.Fatal(err)
	}
	return path
}

var spawnTestConf = confinement{ReadOnly: []string{"/media/in"}, ReadWrite: []string{"/var/out"}}

// TestSpawnSeamLinuxRequiresLandlock: on Linux every shimmed spawn tells the
// shim landlock is mandatory, so a kernel that cannot fence the job refuses
// it instead of running FFmpeg on rlimits alone.
func TestSpawnSeamLinuxRequiresLandlock(t *testing.T) {
	pinSpawnPlatform(t, "linux")
	cmd := shimCommand(context.Background(), "/opt/beamfall", "/usr/bin/ffmpeg", spawnTestConf, []string{"-i", "in.mkv"})
	if cmd.Err != nil {
		t.Fatalf("linux spawn refused: %v", cmd.Err)
	}
	want := []string{"/opt/beamfall", shimSubcommand, "--require-landlock",
		"--ro", "/media/in", "--rw", "/var/out", "--", "/usr/bin/ffmpeg", "-i", "in.mkv"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("linux argv:\n got %q\nwant %q", cmd.Args, want)
	}
}

// TestSpawnSeamDarwinRunsShimUnderSeatbelt: on macOS every shimmed spawn runs
// the shim inside sandbox-exec with a deny-default profile that grants the
// job's paths, the shim binary and the tool directory, and nothing writable
// beyond the job's read-write set.
func TestSpawnSeamDarwinRunsShimUnderSeatbelt(t *testing.T) {
	pinSpawnPlatform(t, "darwin")
	launcher := fakeSandboxExec(t)
	pinSandboxExec(t, launcher)
	cmd := shimCommand(context.Background(), "/opt/beamfall/beamfall", "/opt/homebrew/bin/ffmpeg", spawnTestConf, []string{"-i", "in.mkv"})
	if cmd.Err != nil {
		t.Fatalf("darwin spawn refused: %v", cmd.Err)
	}
	if len(cmd.Args) < 6 || cmd.Args[0] != launcher || cmd.Args[1] != "-p" || cmd.Args[3] != "/opt/beamfall/beamfall" || cmd.Args[4] != shimSubcommand {
		t.Fatalf("darwin argv = %q, want sandbox-exec -p <profile> <shim> ffmpeg-shim ...", cmd.Args)
	}
	wantTail := []string{"--ro", "/media/in", "--rw", "/var/out", "--", "/opt/homebrew/bin/ffmpeg", "-i", "in.mkv"}
	if got := cmd.Args[5:]; !reflect.DeepEqual(got, wantTail) {
		t.Fatalf("darwin shim argv tail:\n got %q\nwant %q", got, wantTail)
	}
	profile := cmd.Args[2]
	for _, want := range []string{
		"(deny default)",
		"(deny network*)",
		`(allow file-read* (subpath "/media/in"))`,
		`(allow file-write* (subpath "/var/out"))`,
		`(allow file-read* (subpath "/opt/homebrew/bin"))`,
		`(allow file-read* (literal "/opt/beamfall/beamfall"))`,
		"iokit-user-client-class",
	} {
		if !strings.Contains(profile, want) {
			t.Errorf("darwin profile missing %q:\n%s", want, profile)
		}
	}
	if strings.Count(profile, "file-write*") != 1 {
		t.Errorf("darwin profile grants writes beyond the job's read-write dir:\n%s", profile)
	}
	if strings.Contains(profile, "network-outbound") {
		t.Errorf("a non-loopback job's profile grants network:\n%s", profile)
	}
}

// TestSpawnSeamRefusesUnconfinableHosts: a platform with no supported kernel
// confinement, a macOS host without sandbox-exec, and a server whose own
// binary could not be resolved all refuse the spawn. Start fails with
// ErrConfinementUnavailable and no process is created.
func TestSpawnSeamRefusesUnconfinableHosts(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-sandbox-exec")
	cases := []struct {
		name, goos, shim, sandboxExec string
	}{
		{"windows", "windows", "/opt/beamfall", ""},
		{"freebsd", "freebsd", "/opt/beamfall", ""},
		{"openbsd", "openbsd", "/opt/beamfall", ""},
		{"darwin without sandbox-exec", "darwin", "/opt/beamfall", missing},
		{"linux unresolved shim", "linux", unresolvedShimPath, ""},
		{"darwin unresolved shim", "darwin", unresolvedShimPath, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinSpawnPlatform(t, tc.goos)
			if tc.sandboxExec != "" {
				pinSandboxExec(t, tc.sandboxExec)
			} else {
				pinSandboxExec(t, fakeSandboxExec(t))
			}
			if _, err := confinedArgv(tc.goos, tc.shim, "/usr/bin/ffmpeg", spawnTestConf, nil); !errors.Is(err, ErrConfinementUnavailable) {
				t.Fatalf("confinedArgv err = %v, want ErrConfinementUnavailable", err)
			}
			cmd := shimCommand(context.Background(), tc.shim, "/usr/bin/true", spawnTestConf, nil)
			err := cmd.Start()
			if !errors.Is(err, ErrConfinementUnavailable) {
				t.Fatalf("Start err = %v, want ErrConfinementUnavailable", err)
			}
			if cmd.Process != nil {
				t.Fatalf("a refused spawn created process %d", cmd.Process.Pid)
			}
		})
	}
}

// TestRunnerMediaSpawnRefusedWhenConfinementUnavailable drives a real Runner
// entrypoint on an unconfinable platform: the refusal surfaces as the
// operation's error (wrapping ErrConfinementUnavailable), never as a verdict
// about the media.
func TestRunnerMediaSpawnRefusedWhenConfinementUnavailable(t *testing.T) {
	pinSpawnPlatform(t, "windows")
	dir := t.TempDir()
	input := filepath.Join(dir, "in.mkv")
	if err := os.WriteFile(input, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fake executable must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	r := New()
	r.SetHardeningShim("/opt/beamfall")
	_, err := r.Probe(context.Background(), input)
	if !errors.Is(err, ErrConfinementUnavailable) {
		t.Fatalf("Probe err = %v, want ErrConfinementUnavailable", err)
	}
}

// TestDefaultShimPathOverride keeps the explicit override working; the
// unresolved fallback is the sentinel the seam refuses (covered above).
func TestDefaultShimPathOverride(t *testing.T) {
	t.Setenv(ShimEnvVar, "/srv/beamfall")
	if got := DefaultShimPath(); got != "/srv/beamfall" {
		t.Fatalf("DefaultShimPath = %q, want the override", got)
	}
	t.Setenv(ShimEnvVar, "")
	if got := DefaultShimPath(); got == "" || got == unresolvedShimPath {
		t.Fatalf("DefaultShimPath = %q, want this executable", got)
	}
}

// TestProductionSpawnsGoThroughTheSeam fails when production code in this
// package builds a command outside the confinement seam. The allowlisted
// direct spawns are the seam itself, the two shim-less macOS fallbacks, and
// capability queries that run FFmpeg with no media input.
func TestProductionSpawnsGoThroughTheSeam(t *testing.T) {
	allowed := map[string]bool{
		"spawn_confinement.go:shimCommand":                  true, // the seam
		"spawn_confinement.go:refusedCommand":               true, // never started
		"pipe.go:loopbackCommand":                           true, // seam argv, or shim-less sandbox-exec
		"ffmpeg.go:convertCommand":                          true, // shim-less sandbox-exec
		"ffmpeg.go:Version":                                 true, // -version, no media
		"tonemap.go:ffmpegVersionBanner":                    true, // -version, no media
		"composed_capability.go:defaultComponentListRunner": true, // component list, no media
		"pipe_redirects.go:defaultHTTPProtocolHelp":         true, // protocol help, no media
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
						if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "exec" && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") {
							found = append(found, name+":"+fn.Name.Name)
						}
					}
				case *ast.CompositeLit:
					if sel, ok := node.Type.(*ast.SelectorExpr); ok {
						if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "exec" && sel.Sel.Name == "Cmd" {
							found = append(found, name+":"+fn.Name.Name)
						}
					}
				}
				return true
			})
		}
	}
	sort.Strings(found)
	for _, site := range found {
		if !allowed[site] {
			t.Errorf("%s builds a command outside the confinement seam; route it through shimCommand", site)
		}
	}
}
