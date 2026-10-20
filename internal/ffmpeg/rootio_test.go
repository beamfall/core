package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func requireSymlinkSupport(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs developer mode on windows")
	}
}

// The reservation creates a real, uniquely named file inside the destination
// directory, and the publish moves exactly that file onto the destination name.
func TestReserveOutputPublishesTheReservedFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "poster.jpg")
	out, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserveOutput: %v", err)
	}
	if filepath.Dir(out.Path) != dir {
		t.Fatalf("temp reserved outside the destination directory: %q", out.Path)
	}
	if err := os.WriteFile(out.Path, []byte("frame"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := out.publish(dst); err != nil {
		t.Fatalf("publish: %v", err)
	}
	//nolint:gosec // G304: path is built from t.TempDir(), not external input.
	if data, err := os.ReadFile(dst); err != nil || string(data) != "frame" {
		t.Fatalf("published content = %q, err=%v", data, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp residue left behind: %v", entries)
	}
}

// The post-check swap: the reserved temp is unlinked and a symlink is left at the
// same name before the publish. Renaming that name would install a link in the
// media directory pointing wherever the attacker chose. The publish refuses, and
// the destination is never created.
func TestPublishRefusesSwappedTemp(t *testing.T) {
	requireSymlinkSupport(t)
	dir := t.TempDir()
	dst := filepath.Join(dir, "poster.jpg")
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.bin")
	if err := os.WriteFile(elsewhere, []byte("not-our-output"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserveOutput: %v", err)
	}
	if err := os.Remove(out.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, out.Path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := out.publish(dst); !errors.Is(err, ErrPathSwapped) {
		t.Fatalf("publish err = %v, want ErrPathSwapped", err)
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination created despite a refused publish: %v", err)
	}
}

// A temp replaced by a DIFFERENT regular file — same name, new inode — is the
// same swap without a symlink, and is refused on identity alone.
func TestPublishRefusesRecreatedTemp(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "poster.jpg")
	out, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserveOutput: %v", err)
	}
	if err := os.Remove(out.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out.Path, []byte("substituted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := out.publish(dst); !errors.Is(err, ErrPathSwapped) {
		t.Fatalf("publish err = %v, want ErrPathSwapped", err)
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination created despite a refused publish: %v", err)
	}
}

// Root replacement: the destination directory NAME is re-pointed at a different
// directory after the reservation. Publishing would drop the artifact into a tree
// nothing validated, so it is refused.
func TestPublishRefusesReplacedDestinationDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "media")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "poster.jpg")
	out, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserveOutput: %v", err)
	}
	if err := os.Rename(dir, filepath.Join(base, "media-moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := out.publish(dst); !errors.Is(err, ErrPathSwapped) {
		t.Fatalf("publish err = %v, want ErrPathSwapped", err)
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact landed in the substituted directory: %v", err)
	}
}

// Cancellation and failure take the discard path: the reserved temp is removed
// and nothing else in the directory is touched.
func TestDiscardRemovesOnlyTheReservedTemp(t *testing.T) {
	dir := t.TempDir()
	bystander := filepath.Join(dir, "existing.jpg")
	if err := os.WriteFile(bystander, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := reserveOutput(filepath.Join(dir, "poster.jpg"))
	if err != nil {
		t.Fatalf("reserveOutput: %v", err)
	}
	if err := out.discard(); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if err := out.discard(); err != nil {
		t.Fatalf("second discard: %v", err)
	}
	if _, err := os.Lstat(out.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp survived discard: %v", err)
	}
	//nolint:gosec // G304: path is built from t.TempDir(), not external input.
	if data, err := os.ReadFile(bystander); err != nil || string(data) != "keep" {
		t.Fatalf("discard disturbed an unrelated file: %q err=%v", data, err)
	}
}

func TestDiscardRetriesAfterRemovalFailure(t *testing.T) {
	dir := t.TempDir()
	out, err := reserveOutput(filepath.Join(dir, "poster.jpg"))
	if err != nil {
		t.Fatalf("reserveOutput: %v", err)
	}
	if err := os.Remove(out.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(out.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(out.Path, "blocker")
	if err := os.WriteFile(blocker, []byte("keep removal failing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := out.discard(); err == nil {
		t.Fatal("discard removed a non-empty replacement directory, want failure")
	}
	if out.root == nil {
		t.Fatal("discard closed the directory capability after removal failed")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := out.discard(); err != nil {
		t.Fatalf("retry discard: %v", err)
	}
	if err := out.discard(); err != nil {
		t.Fatalf("discard after successful retry: %v", err)
	}
	if _, err := os.Lstat(out.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp survived retry: %v", err)
	}
}

func TestReserveOutputRecoversOnlyOwnedCrashResidue(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "poster.jpg")
	deadPID := 1 << 30
	crashResidue := dst + outputTempMarker + strconv.Itoa(deadPID) + "-priorincarnation-0123456789abcdef01234567.tmp"
	unowned := dst + ".someone-else.tmp"
	otherDestination := filepath.Join(dir, "other.jpg") + outputTempMarker + strconv.Itoa(deadPID) + "-priorincarnation-0123456789abcdef01234567.tmp"
	for _, path := range []string{crashResidue, unowned, otherDestination} {
		if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserveOutput: %v", err)
	}
	defer func() {
		if err := out.discard(); err != nil {
			t.Errorf("discard: %v", err)
		}
	}()
	if _, err := os.Lstat(crashResidue); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dead-process residue survived recovery: %v", err)
	}
	for _, path := range []string{unowned, otherDestination} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("recovery disturbed %q: %v", path, err)
		}
	}
}

func TestReserveOutputDistinguishesOwnerIncarnations(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "poster.jpg")
	live, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserve live output: %v", err)
	}
	defer func() {
		if err := live.discard(); err != nil {
			t.Errorf("discard live output: %v", err)
		}
	}()
	incarnation, err := ownerProcessStart(os.Getpid())
	if err != nil {
		t.Fatalf("current process start identity: %v", err)
	}
	prefix := dst + outputTempMarker + strconv.Itoa(os.Getpid()) + "-"
	stale := prefix + incarnation + "stale-0123456789abcdef01234567.tmp"
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserveOutput: %v", err)
	}
	defer func() {
		if err := out.discard(); err != nil {
			t.Errorf("discard: %v", err)
		}
	}()
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prior-incarnation residue with reused live PID survived recovery: %v", err)
	}
	if _, err := os.Lstat(live.Path); err != nil {
		t.Fatalf("genuinely live owner's residue was reclaimed: %v", err)
	}
}

func TestOutputTempSignalOwnerHelper(t *testing.T) {
	if os.Getenv("BEAMFALL_FFMPEG_SIGNAL_OWNER_HELPER") != "1" {
		return
	}
	dst := os.Args[len(os.Args)-1]
	out, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserveOutput: %v", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, out.Path); err != nil {
		t.Fatalf("report temp path: %v", err)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestSignalTerminatedOwnerTempIsRecovered(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "poster.jpg")
	//nolint:gosec // G204: os.Args[0] is this test binary; dst comes from t.TempDir().
	cmd := exec.Command(os.Args[0], "-test.run=^TestOutputTempSignalOwnerHelper$", "--", dst)
	cmd.Env = append(os.Environ(), "BEAMFALL_FFMPEG_SIGNAL_OWNER_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if waited {
			return
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("owner helper did not report its temp: %v", scanner.Err())
	}
	tempPath := scanner.Text()
	killErr := cmd.Process.Kill()
	waitErr := cmd.Wait()
	waited = true
	if killErr != nil {
		t.Fatal(killErr)
	}
	if waitErr == nil {
		t.Fatal("signal-terminated owner exited successfully")
	}
	if _, err := os.Lstat(tempPath); err != nil {
		t.Fatalf("owner temp missing before recovery: %v", err)
	}
	out, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserveOutput recovery: %v", err)
	}
	defer func() {
		if err := out.discard(); err != nil {
			t.Errorf("discard: %v", err)
		}
	}()
	if _, err := os.Lstat(tempPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("signal-terminated owner's temp survived recovery: %v", err)
	}
}

func TestFrameGrabDiskFullFailureCleansOwnedTemp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires Unix")
	}
	binDir := t.TempDir()
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	script := `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
printf partial > "$last"
printf '%s\n' 'No space left on device' >&2
exit 1
`
	if err := os.WriteFile(ffmpegPath, []byte(script), 0o700); err != nil { // #nosec G306 -- the test writes a stub ffmpeg script it must then execute, so the exec bit is required
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	dir := t.TempDir()
	src := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "frame.jpg")
	err := New().FrameGrab(context.Background(), src, 1, 160, dst)
	if err == nil || !strings.Contains(err.Error(), "No space left on device") {
		t.Fatalf("FrameGrab err = %v, want disk-full detail", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "source.mp4" {
		t.Fatalf("disk-full failure left output residue: %v", entries)
	}
}

// A source whose final component is a symlink is refused before any FFmpeg run:
// the input a generation reads must be the file that was validated, not a
// redirection planted at the name.
func TestBindSourceRefusesSymlinkFinalComponent(t *testing.T) {
	requireSymlinkSupport(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "clip.mkv")
	if err := os.WriteFile(real, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.mkv")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	source, err := bindSource(link)
	if err == nil {
		source.close()
		t.Fatal("bindSource followed a symlink final component, want refusal")
	}
}

// A source replaced during the run is detected afterwards, so an output derived
// from bytes the caller never authorised is never published.
func TestBindSourceVerifyDetectsMidRunSwap(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mkv")
	if err := os.WriteFile(src, []byte("authorised"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := bindSource(src)
	if err != nil {
		t.Fatalf("bindSource: %v", err)
	}
	defer source.close()
	if err := source.verify(); err != nil {
		t.Fatalf("verify before any swap = %v, want nil", err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("substituted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := source.verify(); !errors.Is(err, ErrPathSwapped) {
		t.Fatalf("verify after swap = %v, want ErrPathSwapped", err)
	}
}

// A directory reached through a STABLE symlink is a normal deployment layout,
// not an attack, and must keep working.
func TestVerifyDirUnreplacedAcceptsStableSymlinkedDirectory(t *testing.T) {
	requireSymlinkSupport(t)
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.Mkdir(realDir, 0o750); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(base, "linked")
	if err := os.Symlink(realDir, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dst := filepath.Join(linked, "poster.jpg")
	out, err := reserveOutput(dst)
	if err != nil {
		t.Fatalf("reserveOutput through a symlinked directory: %v", err)
	}
	if err := os.WriteFile(out.Path, []byte("frame"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := out.publish(dst); err != nil {
		t.Fatalf("publish through a symlinked directory: %v", err)
	}
	//nolint:gosec // G304: path is built from t.TempDir(), not external input.
	if data, err := os.ReadFile(filepath.Join(realDir, "poster.jpg")); err != nil || string(data) != "frame" {
		t.Fatalf("published content = %q, err=%v", data, err)
	}
}

// Cancellation of a real FFmpeg convert leaves the destination directory exactly
// as it was: no partial output under the destination name, and no temp residue.
func TestConvertCancellationLeavesNoResidue(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary cancellation test")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	//nolint:gosec // G204: binary from exec.LookPath("ffmpeg"); args are literals plus src from t.TempDir().
	gen := exec.Command(ffmpegPath, "-v", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=20",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-t", "20", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize test clip: %v\n%s", err, out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dst := filepath.Join(dir, "out.mp4")
	preset, _ := ConvertProfileByName("h264-mp4")
	if err := New().Convert(ctx, src, dst, preset); err == nil {
		t.Fatal("Convert on a cancelled context returned nil, want failure")
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled convert left a destination file: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("cancelled convert left residue in the directory: %v", entries)
	}
}
