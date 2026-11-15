package encderiv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/libencrypt"
)

// fetchAll reads the whole source from url the way a transcoder would. On a
// failed read it still returns what arrived.
func fetchAll(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := testClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// synthTranscoder reads the source and writes a fragmented MP4 whose media
// data is that source, calling during between the stream's two halves. A
// tolerant transcoder exits 0 on whatever part of the source it received, as
// FFmpeg does on a short input.
func synthTranscoder(tolerant bool, during func()) func(context.Context, Input, io.Writer) error {
	return func(ctx context.Context, in Input, w io.Writer) error {
		src, err := fetchAll(ctx, in.URL)
		if err != nil && !tolerant {
			return err
		}
		raw := buildSynth(synthFrags(src), true).raw
		half := len(raw) / 2
		if _, err := w.Write(raw[:half]); err != nil {
			return err
		}
		if during != nil {
			during()
		}
		_, err = w.Write(raw[half:])
		return err
	}
}

type jobFixture struct {
	stageDir string
	tmpDir   string
	source   []byte
	stream   synthStream
	needles  [][]byte
}

func newJobFixture(t *testing.T) (*jobFixture, Job) {
	t.Helper()
	fx := &jobFixture{stageDir: t.TempDir(), tmpDir: t.TempDir(), source: testPayload(300_000)}
	t.Setenv("TMPDIR", fx.tmpDir)
	fx.stream = buildSynth(synthFrags(fx.source), true)
	// Plaintext signatures: the source itself and the clear fMP4 box headers.
	for _, at := range []int{1000, len(fx.source) / 2, len(fx.source) - 64} {
		fx.needles = append(fx.needles, fx.source[at:at+32])
	}
	fx.needles = append(fx.needles, fx.stream.raw[:16])
	for _, at := range fx.stream.moofAt {
		fx.needles = append(fx.needles, fx.stream.raw[at:at+16])
	}
	job := Job{
		Open:         func() (io.ReadSeekCloser, error) { return memSource{bytes.NewReader(fx.source)}, nil },
		Size:         int64(len(fx.source)),
		StageDir:     fx.stageDir,
		StageName:    "0001",
		PollInterval: time.Millisecond,
		Revoked:      func(context.Context) error { return nil },
		Transcode:    synthTranscoder(false, nil),
	}
	return fx, job
}

func (fx *jobFixture) stagePath() string { return filepath.Join(fx.stageDir, "0001"+StageSuffix) }

// scanForPlaintext reports every regular file under dirs that contains a
// plaintext signature, and how many files it read.
func scanForPlaintext(needles [][]byte, dirs ...string) (leaks []string, files int, err error) {
	for _, dir := range dirs {
		err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			data, err := os.ReadFile(path) //nolint:gosec // test-owned temp dirs
			if err != nil {
				return err
			}
			files++
			for i, needle := range needles {
				if bytes.Contains(data, needle) {
					leaks = append(leaks, fmt.Sprintf("%s holds plaintext signature %d", path, i))
				}
			}
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return leaks, files, nil
}

func assertNoFiles(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s still holds %d entries, first %q", dir, len(entries), entries[0].Name())
		}
	}
}

func TestRunKeepsPlaintextOffDiskAndYieldsTheFinalLayout(t *testing.T) {
	fx, job := newJobFixture(t)
	var midJobFiles atomic.Int32
	job.Transcode = synthTranscoder(false, func() {
		info, err := os.Lstat(fx.stagePath())
		if err != nil {
			t.Errorf("no staging file mid-job: %v", err)
			return
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Errorf("staging file mode %v, want a regular 0600 file", info.Mode())
		}
		leaks, files, err := scanForPlaintext(fx.needles, fx.stageDir, fx.tmpDir)
		if err != nil || len(leaks) > 0 {
			t.Errorf("mid-job scan: %v %v", err, leaks)
		}
		midJobFiles.Store(int32(files)) //nolint:gosec // a handful of files
	})

	result, err := Run(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if midJobFiles.Load() == 0 {
		t.Fatal("the mid-job scan read no staging bytes")
	}
	if info, err := os.Stat(fx.stagePath()); err != nil || info.Size() < int64(len(fx.source)) {
		t.Fatalf("staging file after the job: %v", err)
	}
	leaks, _, err := scanForPlaintext(fx.needles, fx.stageDir, fx.tmpDir)
	if err != nil || len(leaks) > 0 {
		t.Fatalf("post-job scan: %v %v", err, leaks)
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
		t.Fatalf("final layout is %d bytes, Size() %d; digest matches %v", len(final), result.Size(),
			bytes.Equal(sha256Of(final), result.SHA256()))
	}
	info := checkFinalLayout(t, final)
	indexLen := int64(sidxHeaderBytes + sidxReferenceBytes*len(info.refs))
	insertAt := fx.stream.insertAt
	body := append(append([]byte(nil), final[:insertAt]...), final[insertAt+indexLen:]...)
	if !bytes.Equal(body, fx.stream.body) {
		t.Fatal("final layout minus its index is not the transcoder output minus its mfra")
	}

	// The staging file is useless under any other key.
	otherKey, err := libencrypt.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	stage, err := os.Open(fx.stagePath())
	if err != nil {
		t.Fatal(err)
	}
	stat, _ := stage.Stat()
	if _, err := libencrypt.NewDecryptReader(stage, stat.Size(), otherKey); err == nil {
		t.Fatal("staging file decrypted under another key")
	}
	_ = stage.Close()

	key := result.key
	if err := result.Close(); err != nil {
		t.Fatal(err)
	}
	if err := result.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !bytes.Equal(key, make([]byte, len(key))) {
		t.Fatal("Close did not zero the staging key")
	}
	if _, err := result.Open(); err == nil {
		t.Fatal("Open succeeded after Close")
	}
	assertNoFiles(t, fx.stageDir, fx.tmpDir)
}

func TestResultOpenFailsOnTamperedStaging(t *testing.T) {
	fx, job := newJobFixture(t)
	result, err := Run(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.Close() }()
	data, err := os.ReadFile(fx.stagePath())
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0x01
	if err := os.WriteFile(fx.stagePath(), data, 0o600); err != nil { // #nosec G703 -- test-owned temp path
		t.Fatal(err)
	}
	reader, err := result.Open()
	if err == nil {
		_, err = io.ReadAll(reader)
		_ = reader.Close()
	}
	if !errors.Is(err, errStageMismatch) {
		t.Fatalf("err = %v, want errStageMismatch", err)
	}
}

func TestVerifyStageDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	key, err := libencrypt.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	s := buildSynth(synthFrags(testPayload(200_000)), false)
	f, body, err := scanAll(s.raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	index, err := f.sidx()
	if err != nil {
		t.Fatal(err)
	}
	var cipher bytes.Buffer
	if err := libencrypt.EncryptStream(&cipher, bytes.NewReader(body), key); err != nil {
		t.Fatal(err)
	}
	bodySum := sha256.Sum256(body)
	path := filepath.Join(dir, "x"+StageSuffix)
	write := func(b []byte) {
		t.Helper()
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(cipher.Bytes())
	sum, err := verifyStage(path, key, f.insertAt, index, int64(len(body)), bodySum)
	if err != nil || sum != sha256.Sum256(finalLayout(body, index, f.insertAt)) {
		t.Fatalf("intact staging: err %v, digest matches %v", err, sum == sha256.Sum256(finalLayout(body, index, f.insertAt)))
	}

	flipped := bytes.Clone(cipher.Bytes())
	flipped[len(flipped)/3] ^= 0x80
	truncated := cipher.Bytes()[:cipher.Len()-1]
	var other bytes.Buffer
	if err := libencrypt.EncryptStream(&other, bytes.NewReader(append(bytes.Clone(body[:len(body)-1]), body[len(body)-1]^1)), key); err != nil {
		t.Fatal(err)
	}
	for name, staged := range map[string][]byte{
		"flipped ciphertext":        flipped,
		"truncated ciphertext":      truncated,
		"different plaintext":       other.Bytes(),
		"empty file":                {},
		"plaintext in place of one": body,
	} {
		write(staged)
		if _, err := verifyStage(path, key, f.insertAt, index, int64(len(body)), bodySum); !errors.Is(err, errStageMismatch) {
			t.Fatalf("%s: err = %v, want errStageMismatch", name, err)
		}
	}
}

func TestRunLeavesNoStagingOnFailure(t *testing.T) {
	errBoom := errors.New("transcoder crashed")
	cases := map[string]struct {
		setup func(fx *jobFixture, job *Job, cancel context.CancelFunc)
		want  error
	}{
		"transcoder error": {func(fx *jobFixture, job *Job, _ context.CancelFunc) {
			job.Transcode = func(_ context.Context, _ Input, w io.Writer) error {
				_, _ = w.Write(fx.stream.raw[:len(fx.stream.raw)/2])
				return errBoom
			}
		}, errBoom},
		"invalid output, clean exit": {func(_ *jobFixture, job *Job, _ context.CancelFunc) {
			job.Transcode = func(_ context.Context, _ Input, w io.Writer) error {
				_, err := w.Write(bytes.Repeat([]byte("not an mp4 "), 10_000))
				return err
			}
		}, ErrInvalidOutput},
		"truncated output, clean exit": {func(fx *jobFixture, job *Job, _ context.CancelFunc) {
			job.Transcode = func(_ context.Context, _ Input, w io.Writer) error {
				_, err := w.Write(fx.stream.body[:len(fx.stream.body)-100])
				return err
			}
		}, ErrInvalidOutput},
		"cancelled": {func(fx *jobFixture, job *Job, cancel context.CancelFunc) {
			job.Transcode = func(ctx context.Context, _ Input, w io.Writer) error {
				_, _ = w.Write(fx.stream.raw[:len(fx.stream.raw)/2])
				cancel()
				<-ctx.Done()
				return ctx.Err()
			}
		}, context.Canceled},
		"expired": {func(fx *jobFixture, job *Job, _ context.CancelFunc) {
			job.TTL = 20 * time.Millisecond
			job.Transcode = func(ctx context.Context, _ Input, w io.Writer) error {
				_, _ = w.Write(fx.stream.raw[:len(fx.stream.raw)/2])
				<-ctx.Done()
				return ctx.Err()
			}
		}, ErrExpired},
		"revoked before start": {func(_ *jobFixture, job *Job, _ context.CancelFunc) {
			job.Revoked = func(context.Context) error { return errors.New("sealed") }
			job.Transcode = func(context.Context, Input, io.Writer) error {
				panic("transcoder started for a revoked job")
			}
		}, ErrRevoked},
		"revoked after the output": {func(_ *jobFixture, job *Job, _ context.CancelFunc) {
			var revoked atomic.Bool
			job.Revoked = func(context.Context) error {
				if revoked.Load() {
					return errors.New("sealed")
				}
				return nil
			}
			inner := synthTranscoder(false, nil)
			job.Transcode = func(ctx context.Context, in Input, w io.Writer) error {
				err := inner(ctx, in, w)
				revoked.Store(true)
				return err
			}
		}, ErrRevoked},
		"source fails authentication, clean exit": {func(fx *jobFixture, job *Job, _ context.CancelFunc) {
			job.Open = func() (io.ReadSeekCloser, error) {
				return &failingSource{Reader: bytes.NewReader(fx.source[:len(fx.source)/2]), err: errors.New("chunk failed authentication")}, nil
			}
			job.Transcode = synthTranscoder(true, nil)
		}, errSourceFailure},
		"source shorter than declared, clean exit": {func(_ *jobFixture, job *Job, _ context.CancelFunc) {
			job.Size += 1000
			job.Transcode = synthTranscoder(true, nil)
		}, errSourceFailure},
		"index pushes past the limit": {func(fx *jobFixture, job *Job, _ context.CancelFunc) {
			job.MaxBytes = int64(len(fx.stream.body))
		}, ErrTooLarge},
		"stream past the limit": {func(_ *jobFixture, job *Job, _ context.CancelFunc) {
			job.MaxBytes = 100_000
		}, ErrTooLarge},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx, job := newJobFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tc.setup(fx, &job, cancel)
			result, err := Run(ctx, job)
			if err == nil {
				_ = result.Close()
				t.Fatal("Run succeeded")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			assertNoFiles(t, fx.stageDir, fx.tmpDir)
		})
	}
}

func TestRunRevocationMidJobEndsTokenAndTranscode(t *testing.T) {
	fx, job := newJobFixture(t)
	var revoked atomic.Bool
	job.Revoked = func(context.Context) error {
		if revoked.Load() {
			return errors.New("library sealed")
		}
		return nil
	}
	fetchAfter := make(chan error, 1)
	job.Transcode = func(ctx context.Context, in Input, w io.Writer) error {
		if _, err := w.Write(fx.stream.raw[:len(fx.stream.raw)/2]); err != nil {
			return err
		}
		revoked.Store(true)
		<-ctx.Done()
		_, err := fetchAll(context.Background(), in.URL)
		fetchAfter <- err
		return ctx.Err()
	}
	result, err := Run(context.Background(), job)
	if err == nil {
		_ = result.Close()
		t.Fatal("Run succeeded after revocation")
	}
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("err = %v, want ErrRevoked", err)
	}
	if err := <-fetchAfter; err == nil {
		t.Fatal("the source was still served after revocation")
	}
	assertNoFiles(t, fx.stageDir, fx.tmpDir)
}

func TestRunRedactsTokenFromErrors(t *testing.T) {
	fx, job := newJobFixture(t)
	errBoom := errors.New("input failed")
	var secret string
	job.Transcode = func(_ context.Context, in Input, _ io.Writer) error {
		secret = in.Secret
		return fmt.Errorf("reading %s (token %s): %w", in.URL, in.Secret, errBoom)
	}
	_, err := Run(context.Background(), job)
	if err == nil || secret == "" {
		t.Fatal("Run succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("error message carries the token")
	}
	if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("err = %v; want the redacted original", err)
	}
	assertNoFiles(t, fx.stageDir)
}

func TestRunRefusesAnExistingStagingFile(t *testing.T) {
	fx, job := newJobFixture(t)
	if err := os.WriteFile(fx.stagePath(), []byte("another job"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), job); err == nil {
		t.Fatal("Run reused an existing staging file")
	}
	if data, err := os.ReadFile(fx.stagePath()); err != nil || string(data) != "another job" {
		t.Fatalf("the existing staging file was touched: %q %v", data, err)
	}
}

func TestRunValidatesJob(t *testing.T) {
	for name, edit := range map[string]func(*Job){
		"no source":           func(j *Job) { j.Open = nil },
		"no transcoder":       func(j *Job) { j.Transcode = nil },
		"no revocation check": func(j *Job) { j.Revoked = nil },
		"negative size":       func(j *Job) { j.Size = -1 },
		"relative directory":  func(j *Job) { j.StageDir = "packs/1" },
		"empty name":          func(j *Job) { j.StageName = "" },
		"name with separator": func(j *Job) { j.StageName = "a/b" },
		"parent name":         func(j *Job) { j.StageName = ".." },
		"hidden name":         func(j *Job) { j.StageName = ".x" },
		"negative limit":      func(j *Job) { j.MaxBytes = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			fx, job := newJobFixture(t)
			edit(&job)
			if _, err := Run(context.Background(), job); err == nil {
				t.Fatal("Run accepted an invalid job")
			}
			assertNoFiles(t, fx.stageDir)
		})
	}
}

func TestSweepStagingRemovesOnlyStagingFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("pack-a/0001" + StageSuffix)
	mustWrite("pack-a/0002" + StageSuffix)
	mustWrite("pack-a/pack.bfop")
	mustWrite("pack-b/0001" + StageSuffix)
	mustWrite("top" + StageSuffix)
	mustWrite("pack-c/nested/0001" + StageSuffix)
	if err := os.Mkdir(filepath.Join(root, "pack-b", "dir"+StageSuffix), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := SweepStaging(root); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]bool{
		"pack-a/0001" + StageSuffix:        false,
		"pack-a/0002" + StageSuffix:        false,
		"pack-b/0001" + StageSuffix:        false,
		"pack-a/pack.bfop":                 true,
		"top" + StageSuffix:                true,
		"pack-c/nested/0001" + StageSuffix: true,
		"pack-b/dir" + StageSuffix:         true,
	} {
		_, err := os.Lstat(filepath.Join(root, rel))
		if exists := err == nil; exists != want {
			t.Fatalf("%s exists = %v, want %v", rel, exists, want)
		}
	}
	if err := SweepStaging(filepath.Join(root, "missing")); err != nil {
		t.Fatalf("sweeping a missing root: %v", err)
	}
}
