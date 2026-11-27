package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const testPipeURL = "http://127.0.0.1:4321/0123abcd/source"

func TestPipeArgsScopeInputAndFragmentOutput(t *testing.T) {
	profile, ok := PackProfileByName("pack-720p")
	if !ok {
		t.Fatal("pack-720p preset missing")
	}
	profile.FastStart = false
	args := pipeArgs(testPipeURL, 10*time.Second, profile)
	i := slices.Index(args, "-i")
	if i < 0 || args[i+1] != testPipeURL {
		t.Fatalf("args lack -i %s: %v", testPipeURL, args)
	}
	input := args[:i]
	if !slices.Contains(input, "-xerror") {
		t.Errorf("pipe args do not make decode errors fatal: %v", args)
	}
	for _, pair := range [][2]string{
		{"-protocol_whitelist", "http,tcp"},
		{"-max_redirects", "0"},
		{"-rw_timeout", "10000000"},
		{"-format_whitelist", pipeFormatAllowlist},
	} {
		j := slices.Index(input, pair[0])
		if j < 0 || input[j+1] != pair[1] {
			t.Errorf("input options lack %s %s: %v", pair[0], pair[1], input)
		}
	}
	for _, demuxer := range []string{"hls", "dash", "concat", "image2"} {
		if slices.Contains(strings.Split(pipeFormatAllowlist, ","), demuxer) {
			t.Errorf("format allowlist admits the %s demuxer", demuxer)
		}
	}
	for _, demuxer := range []string{"mp3", "flac", "wav", "aiff", "ogg", "mov"} {
		if !slices.Contains(strings.Split(pipeFormatAllowlist, ","), demuxer) {
			t.Errorf("format allowlist refuses the %s demuxer an Encrypted music Library needs", demuxer)
		}
	}
	joined := strings.Join(args, " ")
	for _, banned := range []string{"faststart", "global_sidx", "file"} {
		if strings.Contains(joined, banned) {
			t.Errorf("pipe args contain %q: %v", banned, args)
		}
	}
	if !strings.HasSuffix(joined, "-movflags frag_keyframe+empty_moov+default_base_moof -f mp4 pipe:1") {
		t.Errorf("pipe args do not end in fragmented MP4 to pipe:1: %v", args)
	}
}

func TestPipeProbeArgsScopeInputLikeTheTranscode(t *testing.T) {
	profile, _ := PackProfileByName("pack-720p")
	transcode := pipeArgs(testPipeURL, 10*time.Second, profile)
	args := pipeProbeArgs(testPipeURL, 10*time.Second)
	if n := len(args); n < 2 || args[n-2] != "-i" || args[n-1] != testPipeURL || slices.Index(args, "-i") != n-2 {
		t.Fatalf("probe args do not end in their one input -i %s: %v", testPipeURL, args)
	}
	for _, pair := range [][2]string{
		{"-protocol_whitelist", "http,tcp"},
		{"-max_redirects", "0"},
		{"-rw_timeout", "10000000"},
		{"-format_whitelist", pipeFormatAllowlist},
		{"-show_entries", "format=duration"},
		{"-of", "default=noprint_wrappers=1:nokey=1"},
	} {
		j := slices.Index(args, pair[0])
		if j < 0 || args[j+1] != pair[1] {
			t.Errorf("probe args lack %s %s: %v", pair[0], pair[1], args)
		}
		if k := slices.Index(transcode, pair[0]); k >= 0 && transcode[k+1] != args[j+1] {
			t.Errorf("probe and transcode disagree on %s: %q and %q", pair[0], args[j+1], transcode[k+1])
		}
	}
	joined := strings.Join(args, " ")
	for _, banned := range []string{"-nostdin", "file", "pipe:", "-f "} {
		if strings.Contains(joined, banned) {
			t.Errorf("probe args contain %q: %v", banned, args)
		}
	}
}

func TestPipeSubtitleArgsScopeInputLikeTheTranscode(t *testing.T) {
	profile, _ := PackProfileByName("pack-720p")
	transcode := pipeArgs(testPipeURL, 10*time.Second, profile)
	args := pipeSubtitleArgs(testPipeURL, 10*time.Second, 3)
	i := slices.Index(args, "-i")
	if i < 0 || args[i+1] != testPipeURL || slices.Index(args[i+1:], "-i") >= 0 {
		t.Fatalf("subtitle args lack their one input -i %s: %v", testPipeURL, args)
	}
	input := args[:i]
	for _, pair := range [][2]string{
		{"-protocol_whitelist", "http,tcp"},
		{"-max_redirects", "0"},
		{"-rw_timeout", "10000000"},
		{"-format_whitelist", pipeFormatAllowlist},
	} {
		j := slices.Index(input, pair[0])
		if j < 0 || input[j+1] != pair[1] {
			t.Errorf("input options lack %s %s: %v", pair[0], pair[1], input)
		}
		if k := slices.Index(transcode, pair[0]); k < 0 || transcode[k+1] != input[j+1] {
			t.Errorf("subtitle extract and transcode disagree on %s", pair[0])
		}
	}
	if !slices.Contains(input, "-nostdin") {
		t.Errorf("subtitle args read stdin: %v", args)
	}
	joined := strings.Join(args, " ")
	if !strings.HasSuffix(joined, "-map 0:3 -c:s webvtt -f webvtt pipe:1") {
		t.Errorf("subtitle args do not map the one stream to WebVTT on pipe:1: %v", args)
	}
	if strings.Contains(joined, "file") || slices.Contains(args, "-y") || slices.Contains(args, "--") {
		t.Errorf("subtitle args name a file output: %v", args)
	}
}

func TestExtractSubtitleToPipeRefusesBadArguments(t *testing.T) {
	var out bytes.Buffer
	for name, call := range map[string]func() error{
		"non-loopback host": func() error {
			return New().ExtractSubtitleToPipe(context.Background(), PipeInput{URL: "http://192.168.1.10:8080/token/source"}, 0, 1024, &out)
		},
		"file input": func() error {
			return New().ExtractSubtitleToPipe(context.Background(), PipeInput{URL: "file:///etc/passwd"}, 0, 1024, &out)
		},
		"negative stream": func() error {
			return New().ExtractSubtitleToPipe(context.Background(), PipeInput{URL: testPipeURL}, -1, 1024, &out)
		},
		"no limit": func() error {
			return New().ExtractSubtitleToPipe(context.Background(), PipeInput{URL: testPipeURL}, 0, 0, &out)
		},
		"no writer": func() error {
			return New().ExtractSubtitleToPipe(context.Background(), PipeInput{URL: testPipeURL}, 0, 1024, nil)
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: ExtractSubtitleToPipe accepted it", name)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("refused extraction wrote %d bytes", out.Len())
	}
}

func TestParsePipeDurationAcceptsOnlyAPositiveFiniteLength(t *testing.T) {
	for out, want := range map[string]time.Duration{
		"12.000000\n": 12 * time.Second,
		"0.041667":    41_667 * time.Microsecond,
		" 7200.5 \n":  7200*time.Second + 500*time.Millisecond,
	} {
		if got, err := parsePipeDuration([]byte(out)); err != nil || got != want {
			t.Errorf("parsePipeDuration(%q) = %v, %v; want %v", out, got, err, want)
		}
	}
	for _, out := range []string{
		"", "\n", "N/A", "N/A\n", "0", "0.000000", "-1.5", "nan", "NaN", "inf", "+Inf", "-Inf",
		"1e300", "1e-12", "12.0\n13.0", "12s", "duration=12.0",
	} {
		got, err := parsePipeDuration([]byte(out))
		if !errors.Is(err, errPipeDuration) {
			t.Errorf("parsePipeDuration(%q) = %v, %v; want errPipeDuration", out, got, err)
			continue
		}
		if out != "" && strings.Contains(err.Error(), out) {
			t.Errorf("parsePipeDuration error echoes its input: %v", err)
		}
	}
}

func TestProbePipeDurationRefusesNonLoopbackInputHost(t *testing.T) {
	for _, raw := range []string{"http://192.168.1.10:8080/token/source", "http://169.254.169.254/latest", "file:///etc/passwd"} {
		if d, err := New().ProbePipeDuration(context.Background(), PipeInput{URL: raw}); !errors.Is(err, errPipeInput) || d != 0 {
			t.Fatalf("ProbePipeDuration(%q) = %v, %v; want errPipeInput", raw, d, err)
		}
	}
}

func TestLoopbackPortAcceptsOnlyPlainLoopbackHTTP(t *testing.T) {
	if port, err := loopbackPort(testPipeURL); err != nil || port != 4321 {
		t.Fatalf("loopbackPort(valid) = %d, %v", port, err)
	}
	for _, raw := range []string{
		"",
		"https://127.0.0.1:4321/t/source",
		"http://localhost:4321/t/source",
		"http://127.0.0.2:4321/t/source",
		"http://[::1]:4321/t/source",
		"http://10.0.0.1:4321/t/source",
		"http://127.0.0.1.example.com:4321/t/source",
		"http://user@127.0.0.1:4321/t/source",
		"http://127.0.0.1:4321@example.com/t/source",
		"http://127.0.0.1:4321/t/source?x=1",
		"http://127.0.0.1:4321/t/source?",
		"http://127.0.0.1:4321/t/source#f",
		"http://127.0.0.1:4321/t\\@example.com/",
		"http://127.0.0.1:4321/t/%2e%2e",
		"http://127.0.0.1:0/t/source",
		"http://127.0.0.1:65536/t/source",
		"http://127.0.0.1:04321/t/source",
		"http://127.0.0.1:4321",
		"http://127.0.0.1:/t/source",
		"file:///etc/passwd",
		"concat:http://127.0.0.1:4321/a|http://127.0.0.1:4321/b",
	} {
		port, err := loopbackPort(raw)
		if !errors.Is(err, errPipeInput) {
			t.Errorf("loopbackPort(%q) = %d, %v; want errPipeInput", raw, port, err)
			continue
		}
		if raw != "" && strings.Contains(err.Error(), raw) {
			t.Errorf("loopbackPort error echoes the URL: %v", err)
		}
	}
}

func TestTranscodeToPipeRefusesNonLoopbackInputHost(t *testing.T) {
	profile, _ := PackProfileByName("pack-480p")
	var out bytes.Buffer
	for _, raw := range []string{"http://192.168.1.10:8080/token/source", "http://169.254.169.254/latest"} {
		err := New().TranscodeToPipe(context.Background(), PipeInput{URL: raw}, profile, &out)
		if !errors.Is(err, errPipeInput) {
			t.Fatalf("TranscodeToPipe(%q) = %v, want errPipeInput", raw, err)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("refused transcode wrote %d bytes", out.Len())
	}
}

func TestTranscodeToPipeRefusesNonVideoProfile(t *testing.T) {
	audio, _ := ConvertProfileByName("aac-m4a")
	err := New().TranscodeToPipe(context.Background(), PipeInput{URL: testPipeURL}, audio, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "video MP4") {
		t.Fatalf("TranscodeToPipe(audio profile) = %v, want a refusal", err)
	}
}

func TestPipeConfinementRefusal(t *testing.T) {
	for _, tc := range []struct {
		goos, shim string
		refused    bool
	}{
		{"linux", "", true},
		{"linux", "/srv/beamfall", false},
		{"darwin", "", false},
		{"windows", "/srv/beamfall", true},
		{"freebsd", "/srv/beamfall", true},
	} {
		err := pipeConfinementRefusal(tc.goos, tc.shim)
		if got := errors.Is(err, ErrPipeConfinementUnavailable); got != tc.refused {
			t.Errorf("pipeConfinementRefusal(%s, %q) = %v, refused want %v", tc.goos, tc.shim, err, tc.refused)
		}
	}
}

func TestPipeCommandLinuxRequiresLandlockAndGrantsNoPath(t *testing.T) {
	r := &Runner{shimPath: "/srv/beamfall"}
	args := []string{"-i", testPipeURL, "pipe:1"}
	cmd, err := r.pipeCommand(context.Background(), "linux", 4321, "/usr/bin/ffmpeg", args)
	if err != nil {
		t.Fatalf("pipeCommand(linux): %v", err)
	}
	want := append([]string{"/srv/beamfall", shimSubcommand, "--require-landlock", "--", "/usr/bin/ffmpeg"}, args...)
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("pipeCommand(linux) argv = %q, want %q", cmd.Args, want)
	}
	if _, err := (&Runner{}).pipeCommand(context.Background(), "linux", 4321, "/usr/bin/ffmpeg", args); !errors.Is(err, ErrPipeConfinementUnavailable) {
		t.Fatalf("pipeCommand(linux, no shim) = %v, want ErrPipeConfinementUnavailable", err)
	}
}

func TestPipeCommandDarwinWrapsLoopbackOnlySandbox(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is darwin-only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not on PATH")
	}
	cmd, err := New().pipeCommand(context.Background(), "darwin", 4321, "/opt/homebrew/bin/ffmpeg", []string{"-i", testPipeURL})
	if err != nil {
		t.Fatalf("pipeCommand(darwin): %v", err)
	}
	if len(cmd.Args) < 4 || cmd.Args[1] != "-p" || cmd.Args[3] != "/opt/homebrew/bin/ffmpeg" {
		t.Fatalf("pipeCommand(darwin) argv = %q", cmd.Args)
	}
	profile := cmd.Args[2]
	if !strings.Contains(profile, `(allow network-outbound (remote ip "localhost:4321"))`) {
		t.Fatalf("profile lacks the loopback port grant:\n%s", profile)
	}
	if strings.Contains(profile, "file-write") {
		t.Fatalf("pipe sandbox profile grants a writable path:\n%s", profile)
	}
}

const sandboxReadHelperEnv = "BEAMFALL_TEST_SANDBOX_READ_FILE"

func TestHelperSandboxReadFile(t *testing.T) {
	path := os.Getenv(sandboxReadHelperEnv)
	if path == "" {
		return
	}
	data, err := os.ReadFile(path) // #nosec G304 G703 -- test helper reads caller-specified fixture path
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_, _ = os.Stdout.Write(data)
	os.Exit(0)
}

// CORE-3516 #14: custom executable location only grants literal read access to
// the binary (and its non-system libraries), never its sibling files.
func TestSandboxProfileForDeniesExecutableSiblingSentinelOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is darwin-only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not on PATH")
	}
	selfPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	rawSelf, err := os.ReadFile(selfPath) // #nosec G304 -- current test binary path
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}
	toolDir := t.TempDir()
	allowedDir := t.TempDir()
	toolBin := filepath.Join(toolDir, "custom-tool")
	if err := os.WriteFile(toolBin, rawSelf, 0o700); err != nil { // #nosec G306 G703 -- test executable copy
		t.Fatal(err)
	}
	siblingSentinel := filepath.Join(toolDir, "secret-sibling.txt")
	if err := os.WriteFile(siblingSentinel, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	allowedInput := filepath.Join(allowedDir, "allowed.txt")
	if err := os.WriteFile(allowedInput, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	profile := sandboxProfileFor(confinement{ReadOnly: []string{allowedDir}}, toolBin, toolBin)
	if strings.Contains(profile, fmt.Sprintf("(subpath %s)", sandboxQuote(toolDir))) {
		t.Fatalf("sandbox profile still grants subpath on tool dir:\n%s", profile)
	}
	// Reading allowedDir input succeeds under the Seatbelt profile.
	okCmd := exec.Command("/usr/bin/sandbox-exec", "-p", profile, toolBin, "-test.run=^TestHelperSandboxReadFile$") // #nosec G204
	okCmd.Env = append(os.Environ(), sandboxReadHelperEnv+"="+allowedInput)
	okOut, err := okCmd.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(okOut), "ok\n") {
		t.Fatalf("allowed read failed: err=%v out=%q", err, okOut)
	}
	// Reading sibling file beside toolBin is denied by Seatbelt.
	denyCmd := exec.Command("/usr/bin/sandbox-exec", "-p", profile, toolBin, "-test.run=^TestHelperSandboxReadFile$") // #nosec G204
	denyCmd.Env = append(os.Environ(), sandboxReadHelperEnv+"="+siblingSentinel)
	if denyOut, err := denyCmd.CombinedOutput(); err == nil {
		t.Fatalf("sibling sentinel beside binary was readable inside sandbox! out=%q", denyOut)
	}
}

// CORE-3516 #14 follow-up: custom Mach-O binaries linking dylibs via inherited
// @rpath chains or LC_REEXPORT_DYLIB execute cleanly under the literal Seatbelt
// profile while sibling files remain denied.
func TestSandboxBinaryRuntimeLibsInheritedRpathAndReexportOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec and Mach-O dylibs are darwin-only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not on PATH")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not on PATH")
	}
	compile := func(args ...string) {
		t.Helper()
		cmd := exec.Command(clang, args...) // #nosec G204 -- test builds local C fixtures with clang
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("clang %v: %v\n%s", args, err, out)
		}
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	rpathDir := filepath.Join(root, "rpaths")
	reexportDir := filepath.Join(root, "reexport")
	for _, d := range []string{binDir, rpathDir, reexportDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	siblingSecret := filepath.Join(rpathDir, "sibling-secret.txt")
	if err := os.WriteFile(siblingSecret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Build leaf <- mid (@rpath/libleaf.dylib, NO LC_RPATH on libmid!) <- app (-Wl,-rpath,@executable_path/../rpaths)
	// plus reexported <- wrap (-Wl,-reexport_library,.../libreexport.dylib) <- app calling reexported_val().
	leafSrc := filepath.Join(root, "leaf.c")
	midSrc := filepath.Join(root, "mid.c")
	reexpSrc := filepath.Join(root, "reexp.c")
	wrapSrc := filepath.Join(root, "wrap.c")
	appSrc := filepath.Join(root, "app.c")
	if err := os.WriteFile(leafSrc, []byte(`int leaf_val(void) { return 21; }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(midSrc, []byte(`int leaf_val(void); int mid_val(void) { return leaf_val() * 2; }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reexpSrc, []byte(`int reexp_val(void) { return 7; }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapSrc, []byte(`int wrap_marker(void) { return 1; }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appSrc, []byte(`
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
int mid_val(void);
int reexp_val(void);
int main(int argc, char **argv) {
	if (argc > 1 && open(argv[1], O_RDONLY) >= 0) return 99;
	printf("%d\n", mid_val() + reexp_val());
	return 0;
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	leafLib := filepath.Join(rpathDir, "libleaf.dylib")
	midLib := filepath.Join(rpathDir, "libmid.dylib")
	reexpLib := filepath.Join(reexportDir, "libreexp.dylib")
	wrapLib := filepath.Join(reexportDir, "libwrap.dylib")
	appBin := filepath.Join(binDir, "rpath-app")

	compile("-dynamiclib", leafSrc, "-install_name", "@rpath/libleaf.dylib", "-o", leafLib)
	compile("-dynamiclib", midSrc, leafLib, "-install_name", "@rpath/libmid.dylib", "-o", midLib)
	compile("-dynamiclib", reexpSrc, "-install_name", reexpLib, "-o", reexpLib)
	compile("-dynamiclib", wrapSrc, "-Wl,-reexport_library,"+reexpLib, "-install_name", wrapLib, "-o", wrapLib)
	compile(appSrc, midLib, wrapLib, "-Wl,-rpath,@executable_path/../rpaths", "-o", appBin)

	profile := sandboxProfileFor(confinement{}, appBin, appBin)
	// Running appBin under sandbox-exec loads @rpath/libmid -> inherited @rpath/libleaf
	// and libwrap -> LC_REEXPORT_DYLIB libreexp, while open(siblingSecret) fails EPERM.
	cmd := exec.Command("/usr/bin/sandbox-exec", "-p", profile, appBin, siblingSecret) // #nosec G204
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rpath/reexport app failed under sandbox: %v\nout=%s\nprofile:\n%s", err, out, profile)
	}
	if strings.TrimSpace(string(out)) != "49" {
		t.Fatalf("unexpected app stdout %q, want 49", out)
	}
}

func TestSandboxProfileNetGrantsOnlyTheLoopbackPort(t *testing.T) {
	profile := sandboxProfileNet(nil, nil, 4321)
	deny := strings.Index(profile, "(deny network*)")
	allow := strings.Index(profile, `(allow network-outbound (remote ip "localhost:4321"))`)
	if deny < 0 || allow < deny {
		t.Fatalf("profile must deny network, then allow only the loopback port:\n%s", profile)
	}
	if strings.Count(profile, "(allow network") != 1 {
		t.Fatalf("profile has more than one network grant:\n%s", profile)
	}
	if strings.Contains(profile, "file-write") {
		t.Fatalf("pipe profile grants a writable path:\n%s", profile)
	}
	if strings.Contains(sandboxProfile([]string{"/in"}, []string{"/out"}), "network-outbound") {
		t.Fatal("the convert profile gained a network grant")
	}
	if strings.Contains(sandboxProfileNet(nil, nil, 70000), "network-outbound") {
		t.Fatal("an out-of-range port produced a network grant")
	}
}

func TestPipeEnvDropsProxies(t *testing.T) {
	env := pipeEnv([]string{"PATH=/usr/bin", "http_proxy=http://p:1", "HTTP_PROXY=http://p:1", "Https_Proxy=x", "ALL_PROXY=y", "NO_PROXY=z", "HOME=/h"})
	if !slices.Equal(env, []string{"PATH=/usr/bin", "NO_PROXY=z", "HOME=/h"}) {
		t.Fatalf("pipeEnv = %q", env)
	}
}

func TestRedactPipeDetailRemovesURLAndSecret(t *testing.T) {
	in := PipeInput{URL: testPipeURL, Secret: "0123abcd"}
	got := redactPipeDetail(testPipeURL+": Server returned 404; token 0123abcd", in)
	if strings.Contains(got, "0123abcd") || strings.Contains(got, "127.0.0.1:4321") {
		t.Fatalf("redactPipeDetail leaked the token: %q", got)
	}
}

// TestPipeRunKillsAToolThatOverflowsBoundedStdout runs /usr/bin/yes, which
// writes past the probe's stdout cap and never exits, through the pipe run
// loop. The overflow must kill it and refuse, rather than discard its output
// until the context expires.
func TestPipeRunKillsAToolThatOverflowsBoundedStdout(t *testing.T) {
	if _, err := os.Stat("/usr/bin/yes"); err != nil {
		t.Skip("/usr/bin/yes not present")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/yes")
	stdout := &boundedBuffer{limit: pipeProbeStdoutLimit}
	start := time.Now()
	err := New().runPipeCmd(ctx, cmd, PipeInput{URL: testPipeURL}, "ffprobe pipe probe", stdout)
	if !errors.Is(err, ErrOutputOverflow) {
		t.Fatalf("runPipeCmd = %v, want ErrOutputOverflow", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("overflowing tool ran %v; the overflow did not kill it", elapsed)
	}
}
