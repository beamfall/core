package encderiv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/libencrypt"
)

const (
	fixtureSeconds = 12
	fixtureRate    = 24
)

// ffmpegFixture generates a short H.264/AAC MP4. Without faststart its moov box
// follows its media data, the layout that makes a transcoder seek to the end
// of its input before it can read the start; with it, the moov comes first.
func ffmpegFixture(t *testing.T, faststart bool) []byte {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), "fixture.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=24:duration=12",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=12",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest"}
	if faststart {
		args = append(args, "-movflags", "+faststart")
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, append(args, path)...) //nolint:gosec // fixed test argv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot generate the fixture: %v: %s", err, out)
	}
	data, err := os.ReadFile(path) //nolint:gosec // test temp file
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	moov, mdat := -1, -1
	for i, box := range topLevelBoxes(t, data) {
		switch box.typ {
		case "moov":
			moov = i
		case "mdat":
			mdat = i
		}
	}
	if moov < 0 || mdat < 0 || (moov < mdat) != faststart {
		t.Fatalf("fixture boxes: moov at %d, mdat at %d; faststart %v", moov, mdat, faststart)
	}
	return data
}

// streamedMatroska generates a Matroska written to a pipe. Unable to seek back,
// the muxer never records the segment's duration, so ffprobe reports N/A.
func streamedMatroska(t *testing.T) []byte {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", //nolint:gosec // fixed test argv
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=24:duration=2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-f", "matroska", "pipe:1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		t.Skipf("ffmpeg cannot generate the streamed fixture: %v: %s", err, stderr.Bytes())
	}
	return out
}

// ffprobeDuration is the production duration probe. It stores what it
// measured in measured, when that is not nil.
func ffprobeDuration(t *testing.T, measured *time.Duration) func(context.Context, Input) (time.Duration, error) {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	return func(ctx context.Context, in Input) (time.Duration, error) {
		d, err := ffmpeg.New().ProbePipeDuration(ctx, ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret})
		if measured != nil {
			*measured = d
		}
		return d, err
	}
}

// encryptedSource encrypts plaintext under a fresh key and opens it the way an
// encrypted library does: as a decrypting reader over the ciphertext.
func encryptedSource(t *testing.T, plaintext []byte) func() (io.ReadSeekCloser, error) {
	t.Helper()
	key, err := libencrypt.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	var cipher bytes.Buffer
	if err := libencrypt.EncryptStream(&cipher, bytes.NewReader(plaintext), key); err != nil {
		t.Fatal(err)
	}
	ciphertext := cipher.Bytes()
	return func() (io.ReadSeekCloser, error) {
		r, err := libencrypt.NewDecryptReader(bytes.NewReader(ciphertext), int64(len(ciphertext)), key)
		if err != nil {
			return nil, err
		}
		return struct {
			io.ReadSeeker
			io.Closer
		}{r, io.NopCloser(nil)}, nil
	}
}

// windows returns 32-byte slices of b at even intervals.
func windows(b []byte, count int) [][]byte {
	var out [][]byte
	for i := 1; i <= count && len(b) > 64; i++ {
		at := len(b) * i / (count + 1)
		out = append(out, bytes.Clone(b[at:at+32]))
	}
	return out
}

// teeScanner records the transcoder output in memory and, once it passes
// scanAt bytes, checks that none of it has reached the scanned directories.
type teeScanner struct {
	w      io.Writer
	scanAt int
	dirs   []string
	t      *testing.T

	mu      sync.Mutex
	written bytes.Buffer
	scanned bool
}

func (s *teeScanner) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written.Write(p[:n])
	if !s.scanned && s.written.Len() >= s.scanAt {
		s.scanned = true
		s.scan("mid-transcode")
	}
	return n, err
}

func (s *teeScanner) scan(when string) {
	needles := append(windows(s.written.Bytes(), 16), bytes.Clone(s.written.Bytes()[:16]))
	leaks, _, err := scanForPlaintext(needles, s.dirs...)
	if err != nil || len(leaks) > 0 {
		s.t.Errorf("%s scan: %v %v", when, err, leaks)
	}
}

func (s *teeScanner) output() (written []byte, scanned bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.written.Bytes()), s.scanned
}

// TestRunTranscodesAMoovAtEndSourceWithRealFFmpeg drives the production pipe
// probe and transcode end to end: ffprobe measures and FFmpeg range-reads a
// decrypted source from the token endpoint, and the fragmented output is
// indexed and staged encrypted.
func TestRunTranscodesAMoovAtEndSourceWithRealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video")
	}
	fixture := ffmpegFixture(t, false)
	stageDir, tmpDir := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	profile, ok := ffmpeg.PackProfileByName("pack-480p")
	if !ok {
		t.Fatal("no pack-480p profile")
	}
	open := encryptedSource(t, fixture)
	var opens, probeOpens, transcodeOpens atomic.Int32
	var tee *teeScanner
	var measured time.Duration
	probe := ffprobeDuration(t, &measured)
	job := Job{
		Open: func() (io.ReadSeekCloser, error) {
			opens.Add(1)
			return open()
		},
		Size:      int64(len(fixture)),
		StageDir:  stageDir,
		StageName: "0001",
		ProbeDuration: func(ctx context.Context, in Input) (time.Duration, error) {
			d, err := probe(ctx, in)
			probeOpens.Store(opens.Load())
			return d, err
		},
		Revoked: func(context.Context) error { return nil },
		Transcode: func(ctx context.Context, in Input, w io.Writer) error {
			before := opens.Load()
			defer func() { transcodeOpens.Store(opens.Load() - before) }()
			tee = &teeScanner{w: w, scanAt: 64 << 10, dirs: []string{stageDir, tmpDir}, t: t}
			// io:ffmpeg-transcode-to-pipe
			if err := ffmpeg.New().TranscodeToPipe(ctx, ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret}, profile, tee); err != nil {
				return err
			}
			tee.scan("post-transcode")
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := Run(ctx, job)
	if errors.Is(err, ffmpeg.ErrPipeConfinementUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.Close() }()
	if n := probeOpens.Load(); n < 2 {
		t.Fatalf("ffprobe opened the source %d times; a moov-at-end input needs a ranged read", n)
	}
	if n := transcodeOpens.Load(); n < 2 {
		t.Fatalf("FFmpeg opened the source %d times; a moov-at-end input needs a ranged read", n)
	}
	if want := fixtureSeconds * time.Second; measured < want-time.Second/fixtureRate || measured > want+time.Second/fixtureRate {
		t.Fatalf("ffprobe measured %v of a %v source", measured, want)
	}

	reader, err := result.Open()
	if err != nil {
		t.Fatal(err)
	}
	final, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(final)) != result.Size() || !bytes.Equal(sha256Of(final), result.SHA256()) {
		t.Fatal("Size and SHA256 do not describe the final layout")
	}
	info := checkFinalLayout(t, final)
	if len(info.refs) < 2 {
		t.Fatalf("sidx has %d references; the fixture should yield several fragments", len(info.refs))
	}
	var total uint64
	for _, ref := range info.refs {
		total += uint64(ref.duration)
	}
	want := uint64(fixtureSeconds) * uint64(info.timescale)
	frame := uint64(info.timescale) / fixtureRate
	if total+frame < want || total > want+frame {
		t.Fatalf("sidx durations sum to %d at timescale %d, want %d within one frame", total, info.timescale, want)
	}

	written, scanned := tee.output()
	if !scanned {
		t.Fatalf("the transcode wrote %d bytes, under the mid-transcode scan point", len(written))
	}
	needles := append(windows(written, 32), windows(fixture, 32)...)
	needles = append(needles, bytes.Clone(final[:16]))
	leaks, files, err := scanForPlaintext(needles, stageDir, tmpDir)
	if err != nil || len(leaks) > 0 || files == 0 {
		t.Fatalf("post-job scan over %d files: %v %v", files, err, leaks)
	}
	if err := result.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoFiles(t, stageDir, tmpDir)
}

// TestRunRefusesATruncatedSourceWithRealFFmpeg cuts a faststart source in half:
// FFmpeg reads the whole moov, then runs out of media data partway. Left to
// itself it exits 0 on what it could encode. -xerror makes the decode error
// fatal, and the duration check refuses the short output on its own: ffprobe
// still measures the full length the intact moov declares.
func TestRunRefusesATruncatedSourceWithRealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video")
	}
	full := ffmpegFixture(t, true)
	truncated := full[:len(full)/2]
	profile, ok := ffmpeg.PackProfileByName("pack-480p")
	if !ok {
		t.Fatal("no pack-480p profile")
	}
	var measured time.Duration
	for name, probe := range map[string]func(context.Context, Input) (time.Duration, error){
		// A source duration the half-length output still covers leaves only
		// -xerror to refuse it.
		"decode error": func(context.Context, Input) (time.Duration, error) { return time.Second, nil },
		"short output": ffprobeDuration(t, &measured),
	} {
		t.Run(name, func(t *testing.T) {
			stageDir, tmpDir := t.TempDir(), t.TempDir()
			t.Setenv("TMPDIR", tmpDir)
			job := Job{
				Open:          encryptedSource(t, truncated),
				Size:          int64(len(truncated)),
				StageDir:      stageDir,
				StageName:     "0001",
				ProbeDuration: probe,
				Revoked:       func(context.Context) error { return nil },
				Transcode: func(ctx context.Context, in Input, w io.Writer) error {
					return ffmpeg.New().TranscodeToPipe(ctx, ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret}, profile, w)
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			result, err := Run(ctx, job)
			if errors.Is(err, ffmpeg.ErrPipeConfinementUnavailable) {
				t.Skip(err)
			}
			if err == nil {
				_ = result.Close()
				t.Fatal("Run accepted a truncated source")
			}
			t.Logf("refused: %v", err)
			if name == "short output" && measured < (fixtureSeconds-1)*time.Second {
				t.Fatalf("ffprobe measured %v of the truncated source; its moov declares %ds", measured, fixtureSeconds)
			}
			assertNoFiles(t, stageDir, tmpDir)
		})
	}
}

// TestRunRefusesAnUnmeasurableSourceWithRealFFprobe feeds the production probe
// sources it cannot name a length for. The job refuses them before FFmpeg
// starts, since a short output could not be told from a complete one.
func TestRunRefusesAnUnmeasurableSourceWithRealFFprobe(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video")
	}
	probe := ffprobeDuration(t, nil)
	for name, source := range map[string][]byte{
		"streamed matroska": streamedMatroska(t),
		"not media":         bytes.Repeat([]byte("not a media file "), 4096),
	} {
		t.Run(name, func(t *testing.T) {
			stageDir, tmpDir := t.TempDir(), t.TempDir()
			t.Setenv("TMPDIR", tmpDir)
			var transcoded atomic.Bool
			job := Job{
				Open:          encryptedSource(t, source),
				Size:          int64(len(source)),
				StageDir:      stageDir,
				StageName:     "0001",
				ProbeDuration: probe,
				Revoked:       func(context.Context) error { return nil },
				Transcode: func(context.Context, Input, io.Writer) error {
					transcoded.Store(true)
					return errors.New("transcoder started without a source duration")
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			result, err := Run(ctx, job)
			if errors.Is(err, ffmpeg.ErrPipeConfinementUnavailable) {
				t.Skip(err)
			}
			if err == nil {
				_ = result.Close()
			}
			if !errors.Is(err, ErrSourceDuration) || transcoded.Load() {
				t.Fatalf("err = %v, transcoder started %v; want ErrSourceDuration before the transcode", err, transcoded.Load())
			}
			t.Logf("refused: %v", err)
			assertNoFiles(t, stageDir, tmpDir)
		})
	}
}
