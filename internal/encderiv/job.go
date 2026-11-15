package encderiv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/libencrypt"
)

const (
	// StageSuffix names a job's encrypted staging file inside its directory.
	StageSuffix = ".stage"
	// DefaultTTL bounds a token's whole life.
	DefaultTTL = 12 * time.Hour
	// DefaultIdleTimeout ends a token nobody has used for this long.
	DefaultIdleTimeout = 60 * time.Second
	// DefaultPollInterval is how often a running job re-checks authorization.
	DefaultPollInterval = time.Second
)

var (
	// ErrRevoked is returned when authorization ends before the job does: the
	// Library sealed, the Profile lost access, or the Media left its scope.
	ErrRevoked = errors.New("encderiv: authorization revoked")
	// ErrExpired is returned when the token outlives its TTL or idle timeout.
	ErrExpired = errors.New("encderiv: token expired")
)

// Input is what the transcoder is handed. URL and Secret carry the bearer
// token: never log them. Port is the loopback port its sandbox must allow.
type Input struct {
	URL    string
	Secret string
	Port   int
}

// Job describes one derivative build.
type Job struct {
	// Open returns a fresh plaintext view of the source; the endpoint opens one
	// per request.
	Open Opener
	// Size is the plaintext length the endpoint serves.
	Size int64
	// StageDir is the server-owned directory that holds the staging file;
	// StageName is its single-component base name.
	StageDir  string
	StageName string
	// MaxBytes bounds the derivative's final length. Zero means unbounded.
	MaxBytes int64
	// TTL, IdleTimeout and PollInterval default to the package defaults.
	TTL          time.Duration
	IdleTimeout  time.Duration
	PollInterval time.Duration
	// Revoked reports a non-nil error once the job is no longer authorized.
	// It is checked before the job starts, throughout, and once more at the
	// end; any error revokes.
	Revoked func(context.Context) error
	// Transcode runs the transcoder against in and writes its fragmented MP4
	// to stdout. It must stop when ctx ends.
	Transcode func(ctx context.Context, in Input, stdout io.Writer) error

	now  func() time.Time
	peer peerChecker
}

func (j *Job) normalize() error {
	switch {
	case j.Open == nil || j.Transcode == nil || j.Revoked == nil:
		return errors.New("encderiv: job is missing its source, transcoder or revocation check")
	case j.Size < 0:
		return errors.New("encderiv: negative source size")
	case j.StageDir == "" || !filepath.IsAbs(j.StageDir):
		return errors.New("encderiv: staging directory must be absolute")
	case j.StageName == "" || j.StageName != filepath.Base(j.StageName) || strings.HasPrefix(j.StageName, "."):
		return errors.New("encderiv: staging name must be a single path component")
	case j.MaxBytes < 0:
		return errors.New("encderiv: negative size limit")
	}
	if j.TTL <= 0 {
		j.TTL = DefaultTTL
	}
	if j.IdleTimeout <= 0 {
		j.IdleTimeout = DefaultIdleTimeout
	}
	if j.PollInterval <= 0 {
		j.PollInterval = DefaultPollInterval
	}
	if j.now == nil {
		j.now = time.Now
	}
	if j.peer == nil {
		j.peer = checkPeer
	}
	return nil
}

// Result is a finished derivative held encrypted under its job's key in a
// staging file. Close removes the file and zeroes the key.
type Result struct {
	mu       sync.Mutex
	closed   bool
	path     string
	key      []byte
	insertAt int64
	index    []byte
	size     int64
	sha      [sha256.Size]byte
}

// Size is the final layout's length in bytes.
func (r *Result) Size() int64 { return r.size }

// SHA256 is the final layout's digest.
func (r *Result) SHA256() []byte { return append([]byte(nil), r.sha[:]...) }

// Open returns the final layout: the stream with its segment index placed
// before the first fragment. The reader fails if the staging file no longer
// decrypts to exactly Size bytes.
func (r *Result) Open() (io.ReadCloser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("encderiv: result is closed")
	}
	return openFinal(r.path, r.key, r.insertAt, r.index, r.size)
}

// Close removes the staging file and zeroes the key. It is idempotent.
func (r *Result) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	libencrypt.ZeroKey(r.key)
	if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("encderiv: remove staging: %w", err)
	}
	return nil
}

// Run builds one derivative. On any failure, including cancellation and
// revocation, no staging file remains and the key is zeroed.
func Run(ctx context.Context, job Job) (*Result, error) {
	if err := job.normalize(); err != nil {
		return nil, err
	}
	if err := job.Revoked(ctx); err != nil {
		return nil, ErrRevoked
	}
	key, err := libencrypt.NewRandomKey()
	if err != nil {
		return nil, fmt.Errorf("encderiv: staging key: %w", err)
	}
	path := filepath.Join(job.StageDir, job.StageName+StageSuffix)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // server-owned directory and validated single-component name
	if err != nil {
		libencrypt.ZeroKey(key)
		return nil, fmt.Errorf("encderiv: create staging: %w", err)
	}
	result, secret, err := run(ctx, &job, file, path, key)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		libencrypt.ZeroKey(key)
		return nil, redact(err, secret)
	}
	return result, nil
}

func run(parent context.Context, job *Job, file *os.File, path string, key []byte) (*Result, string, error) {
	ctx, cancelTTL := context.WithTimeoutCause(parent, job.TTL, ErrExpired)
	defer cancelTTL()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	ep, err := startEndpoint(endpointConfig{
		open:     job.Open,
		size:     job.Size,
		deadline: job.now().Add(job.TTL),
		idle:     job.IdleTimeout,
		now:      job.now,
		peer:     job.peer,
	})
	if err != nil {
		return nil, "", err
	}
	defer ep.Revoke()
	secret := ep.token

	stopWatch := make(chan struct{})
	var watchers sync.WaitGroup
	watchers.Go(func() { watchRevocation(ctx, job, ep, cancel, stopWatch) })
	defer func() {
		close(stopWatch)
		watchers.Wait()
	}()

	scan, transcodeErr, err := transcodeToStage(ctx, cancel, job, ep, file, key)
	ep.Revoke()
	if failure := firstFailure(ctx, ep, err, transcodeErr); failure != nil {
		return nil, secret, failure
	}
	if err := file.Close(); err != nil {
		return nil, secret, fmt.Errorf("encderiv: close staging: %w", err)
	}
	index, err := scan.sidx()
	if err != nil {
		return nil, secret, err
	}
	finalLen := scan.n + int64(len(index))
	if job.MaxBytes > 0 && finalLen > job.MaxBytes {
		return nil, secret, ErrTooLarge
	}
	var body [sha256.Size]byte
	copy(body[:], scan.sum.Sum(nil))
	sum, err := verifyStage(path, key, scan.insertAt, index, scan.n, body)
	if err != nil {
		return nil, secret, err
	}
	if err := job.Revoked(parent); err != nil {
		return nil, secret, ErrRevoked
	}
	return &Result{path: path, key: key, insertAt: scan.insertAt, index: index, size: finalLen, sha: sum}, secret, nil
}

// transcodeToStage runs the transcoder and encrypts its output into the
// staging file as it arrives. It returns once the transcoder has exited.
func transcodeToStage(ctx context.Context, cancel context.CancelCauseFunc, job *Job, ep *endpoint, file *os.File, key []byte) (*fragmentReader, error, error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := job.Transcode(ctx, Input{URL: ep.URL(), Secret: ep.token, Port: ep.Port()}, pw)
		_ = pw.CloseWithError(err)
		done <- err
	}()
	scan := newFragmentReader(pr, job.MaxBytes)
	err := libencrypt.EncryptStream(file, scan, key)
	if err != nil {
		cancel(err)
		_ = pr.CloseWithError(err)
	}
	transcodeErr := <-done
	_ = pr.Close()
	return scan, transcodeErr, err
}

// firstFailure orders the reasons a job can fail so the most specific wins:
// revocation, then a failure the endpoint saw (a source that would not
// decrypt, a token used after it ended), then cancellation or expiry, then the
// output and the transcoder's own errors.
func firstFailure(ctx context.Context, ep *endpoint, encryptErr, transcodeErr error) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, ErrRevoked) {
		return ErrRevoked
	}
	if err := ep.Err(); err != nil {
		return err
	}
	if ctx.Err() != nil && !errors.Is(cause, encryptErr) {
		return cause
	}
	if encryptErr != nil {
		return encryptErr
	}
	if transcodeErr != nil {
		return fmt.Errorf("encderiv: transcode: %w", transcodeErr)
	}
	return nil
}

// watchRevocation re-checks authorization until the job ends. On revocation it
// ends the token first, so no more plaintext is served, then cancels the job,
// which kills the transcoder.
func watchRevocation(ctx context.Context, job *Job, ep *endpoint, cancel context.CancelCauseFunc, stop <-chan struct{}) {
	ticker := time.NewTicker(job.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if err := job.Revoked(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				ep.Revoke()
				cancel(ErrRevoked)
				return
			}
		}
	}
}

// verifyStage decrypts the staging file once in its final layout, proving it
// still matches the bytes that were hashed as they streamed, and returns the
// final layout's digest.
func verifyStage(path string, key []byte, insertAt int64, index []byte, bodyLen int64, bodySum [sha256.Size]byte) ([sha256.Size]byte, error) {
	var none [sha256.Size]byte
	reader, err := openFinal(path, key, insertAt, index, bodyLen+int64(len(index)))
	if err != nil {
		return none, err
	}
	defer func() { _ = reader.Close() }()
	final := sha256.New()
	body := sha256.New()
	tee := &splitHasher{final: final, body: body, insertAt: insertAt, indexLen: int64(len(index))}
	if _, err := io.Copy(tee, reader); err != nil {
		return none, err
	}
	if !bytes.Equal(body.Sum(nil), bodySum[:]) {
		return none, errStageMismatch
	}
	var sum [sha256.Size]byte
	copy(sum[:], final.Sum(nil))
	return sum, nil
}

var errStageMismatch = errors.New("encderiv: staging file does not match the transcoder output")

// splitHasher hashes the final layout and, separately, the body without the
// index, so one pass proves both.
type splitHasher struct {
	final, body io.Writer
	insertAt    int64
	indexLen    int64
	pos         int64
}

func (s *splitHasher) Write(p []byte) (int, error) {
	_, _ = s.final.Write(p)
	for b := p; len(b) > 0; {
		switch {
		case s.pos < s.insertAt:
			n := min(int64(len(b)), s.insertAt-s.pos)
			_, _ = s.body.Write(b[:n])
			b, s.pos = b[n:], s.pos+n
		case s.pos < s.insertAt+s.indexLen:
			n := min(int64(len(b)), s.insertAt+s.indexLen-s.pos)
			b, s.pos = b[n:], s.pos+n
		default:
			_, _ = s.body.Write(b)
			s.pos += int64(len(b))
			b = nil
		}
	}
	return len(p), nil
}

// openFinal opens the staging file and returns its final layout, checked to be
// exactly size bytes long.
func openFinal(path string, key []byte, insertAt int64, index []byte, size int64) (io.ReadCloser, error) {
	file, err := os.Open(path) //nolint:gosec // the staging path this package created
	if err != nil {
		return nil, fmt.Errorf("encderiv: open staging: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("encderiv: stat staging: %w", err)
	}
	dr, err := libencrypt.NewDecryptReader(file, info.Size(), key)
	if err != nil {
		_ = file.Close()
		return nil, errStageMismatch
	}
	layout := io.MultiReader(io.LimitReader(dr, insertAt), bytes.NewReader(index), dr)
	return &exactReader{src: layout, remaining: size, closer: file}, nil
}

// exactReader yields exactly remaining bytes or fails.
type exactReader struct {
	src       io.Reader
	remaining int64
	closer    io.Closer
}

func (r *exactReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		var probe [1]byte
		if n, err := r.src.Read(probe[:]); n > 0 || (err != nil && !errors.Is(err, io.EOF)) {
			return 0, errStageMismatch
		}
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.src.Read(p)
	r.remaining -= int64(n)
	if errors.Is(err, io.EOF) {
		if r.remaining > 0 {
			return n, errStageMismatch
		}
		err = nil
	} else if err != nil {
		return n, errStageMismatch
	}
	return n, err
}

func (r *exactReader) Close() error { return r.closer.Close() }

// SweepStaging removes staging files a crashed or killed job left behind in
// the immediate subdirectories of root. Call it at startup, before any job.
func SweepStaging(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("encderiv: sweep staging: %w", err)
	}
	var errs []error
	for _, dir := range entries {
		if !dir.IsDir() {
			continue
		}
		sub := filepath.Join(root, dir.Name())
		files, err := os.ReadDir(sub)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, file := range files {
			if !file.Type().IsRegular() || !strings.HasSuffix(file.Name(), StageSuffix) {
				continue
			}
			if err := os.Remove(filepath.Join(sub, file.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("encderiv: sweep staging: %w", err)
	}
	return nil
}

// redactedError replaces the token in an error's message while still matching
// the sentinels the original wrapped.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string        { return e.msg }
func (e *redactedError) Is(target error) bool { return errors.Is(e.err, target) }

func redact(err error, secret string) error {
	if err == nil || secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return &redactedError{msg: strings.ReplaceAll(err.Error(), secret, "[redacted]"), err: err}
}
