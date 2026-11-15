package encderiv

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// Capture describes one derivative small enough to hold in memory, such as an
// extracted subtitle track. It runs its tool inside the same per-job endpoint,
// token, revocation and expiry as Job, but captures the tool's stdout into a
// bounded buffer instead of staging it on disk: no plaintext is ever written.
type Capture struct {
	// Open returns a fresh plaintext view of the source; the endpoint opens one
	// per request.
	Open Opener
	// Size is the plaintext length the endpoint serves.
	Size int64
	// MaxBytes bounds the captured output and must be positive. A tool that
	// writes more is cancelled, which kills it, and the capture fails with
	// ErrTooLarge.
	MaxBytes int64
	// TTL, IdleTimeout and PollInterval default to the package defaults.
	TTL          time.Duration
	IdleTimeout  time.Duration
	PollInterval time.Duration
	// Revoked reports a non-nil error once the capture is no longer
	// authorized. It is checked before the tool starts, throughout, and once
	// more at the end; any error revokes.
	Revoked func(context.Context) error
	// Transcode runs the tool against in and writes its output to stdout. It
	// must stop when ctx ends.
	Transcode func(ctx context.Context, in Input, stdout io.Writer) error

	now  func() time.Time
	peer peerChecker
}

func (c *Capture) normalize() error {
	switch {
	case c.Open == nil || c.Transcode == nil || c.Revoked == nil:
		return errors.New("encderiv: capture is missing its source, tool or revocation check")
	case c.Size < 0:
		return errors.New("encderiv: negative source size")
	case c.MaxBytes <= 0:
		return errors.New("encderiv: capture needs a positive size limit")
	}
	if c.TTL <= 0 {
		c.TTL = DefaultTTL
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	if c.PollInterval <= 0 {
		c.PollInterval = DefaultPollInterval
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.peer == nil {
		c.peer = checkPeer
	}
	return nil
}

// RunCapture runs one in-memory derivative and returns the tool's complete
// output. On any failure, including cancellation, revocation, expiry, a source
// that fails to decrypt and an output over MaxBytes, it returns no bytes and
// zeroes what it had captured; the token never appears in its error.
func RunCapture(ctx context.Context, c Capture) ([]byte, error) {
	if err := c.normalize(); err != nil {
		return nil, err
	}
	if err := c.Revoked(ctx); err != nil {
		return nil, ErrRevoked
	}
	out := &captureBuffer{limit: c.MaxBytes}
	secret, err := runCapture(ctx, &c, out)
	if err != nil {
		out.wipe()
		return nil, redact(err, secret)
	}
	return out.take(), nil
}

func runCapture(parent context.Context, c *Capture, out *captureBuffer) (string, error) {
	sess, err := startSession(parent, sessionConfig{
		open:    c.Open,
		size:    c.Size,
		ttl:     c.TTL,
		idle:    c.IdleTimeout,
		poll:    c.PollInterval,
		revoked: c.Revoked,
		now:     c.now,
		peer:    c.peer,
	})
	if err != nil {
		return "", err
	}
	defer sess.close()
	secret := sess.ep.token

	out.onOverflow = func() { sess.cancel(ErrTooLarge) }
	transcodeErr := c.Transcode(sess.ctx, sess.input(), out)
	sess.ep.Revoke()
	if failure := firstFailure(sess.ctx, sess.ep, out.err(), transcodeErr); failure != nil {
		return secret, failure
	}
	if err := c.Revoked(parent); err != nil {
		return secret, ErrRevoked
	}
	return secret, nil
}

// captureBuffer is the tool's stdout: a bounded in-memory buffer. The first
// write past the limit fails, records ErrTooLarge and cancels the session, so
// the tool is killed rather than left to fill memory.
type captureBuffer struct {
	mu         sync.Mutex
	limit      int64
	buf        []byte
	overflow   bool
	onOverflow func()
}

func (b *captureBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	if b.overflow {
		b.mu.Unlock()
		return 0, ErrTooLarge
	}
	if int64(len(b.buf))+int64(len(p)) > b.limit {
		b.overflow = true
		onOverflow := b.onOverflow
		b.mu.Unlock()
		if onOverflow != nil {
			onOverflow()
		}
		return 0, ErrTooLarge
	}
	b.buf = append(b.buf, p...)
	b.mu.Unlock()
	return len(p), nil
}

func (b *captureBuffer) err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		return ErrTooLarge
	}
	return nil
}

func (b *captureBuffer) take() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.buf
	b.buf = nil
	if out == nil {
		out = []byte{}
	}
	return out
}

// wipe zeroes the captured bytes, including any capacity append left behind.
func (b *captureBuffer) wipe() {
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.buf[:cap(b.buf)])
	b.buf = nil
}
