package encderiv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type captureFixture struct {
	tmpDir string
	source []byte
}

func newCaptureFixture(t *testing.T) (*captureFixture, Capture) {
	t.Helper()
	fx := &captureFixture{tmpDir: t.TempDir(), source: testPayload(200_000)}
	t.Setenv("TMPDIR", fx.tmpDir)
	c := Capture{
		Open:         func() (io.ReadSeekCloser, error) { return memSource{bytes.NewReader(fx.source)}, nil },
		Size:         int64(len(fx.source)),
		MaxBytes:     1 << 20,
		PollInterval: time.Millisecond,
		Revoked:      func(context.Context) error { return nil },
		Transcode: func(ctx context.Context, in Input, w io.Writer) error {
			src, err := fetchAll(ctx, in.URL)
			if err != nil {
				return err
			}
			_, err = w.Write(src[:4096])
			return err
		},
	}
	return fx, c
}

func TestRunCaptureReturnsTheToolOutputWithoutTouchingDisk(t *testing.T) {
	fx, c := newCaptureFixture(t)
	var served Input
	inner := c.Transcode
	c.Transcode = func(ctx context.Context, in Input, w io.Writer) error {
		served = in
		return inner(ctx, in, w)
	}
	out, err := RunCapture(context.Background(), c)
	if err != nil {
		t.Fatalf("RunCapture: %v", err)
	}
	if !bytes.Equal(out, fx.source[:4096]) {
		t.Fatal("RunCapture did not return the tool output")
	}
	if !strings.HasPrefix(served.URL, "http://127.0.0.1:") || len(served.Secret) < 43 || served.Port == 0 {
		t.Fatalf("tool was not handed a loopback, token-guarded endpoint: %+v", served)
	}
	if _, err := fetchAll(context.Background(), served.URL); err == nil {
		t.Fatal("the source was still served after the capture ended")
	}
	assertNoFiles(t, fx.tmpDir)
}

func TestRunCaptureKillsAToolThatOverflowsTheBound(t *testing.T) {
	fx, c := newCaptureFixture(t)
	c.MaxBytes = 1000
	stopped := make(chan struct{})
	c.Transcode = func(ctx context.Context, _ Input, w io.Writer) error {
		defer close(stopped)
		if _, err := w.Write(make([]byte, 600)); err != nil {
			return err
		}
		if _, err := w.Write(make([]byte, 600)); !errors.Is(err, ErrTooLarge) {
			return fmt.Errorf("overflowing write returned %v", err)
		}
		<-ctx.Done() // a real tool is killed by the cancelled context
		return ctx.Err()
	}
	out, err := RunCapture(context.Background(), c)
	if !errors.Is(err, ErrTooLarge) || out != nil {
		t.Fatalf("RunCapture = %d bytes, %v; want ErrTooLarge and no bytes", len(out), err)
	}
	<-stopped
	assertNoFiles(t, fx.tmpDir)
}

func TestRunCaptureRevocationEndsTokenAndTool(t *testing.T) {
	_, c := newCaptureFixture(t)
	var revoked atomic.Bool
	c.Revoked = func(context.Context) error {
		if revoked.Load() {
			return errors.New("library sealed")
		}
		return nil
	}
	fetchAfter := make(chan error, 1)
	c.Transcode = func(ctx context.Context, in Input, w io.Writer) error {
		if _, err := w.Write([]byte("WEBVTT\n")); err != nil {
			return err
		}
		revoked.Store(true)
		<-ctx.Done()
		_, err := fetchAll(context.Background(), in.URL)
		fetchAfter <- err
		return ctx.Err()
	}
	out, err := RunCapture(context.Background(), c)
	if !errors.Is(err, ErrRevoked) || out != nil {
		t.Fatalf("RunCapture = %d bytes, %v; want ErrRevoked and no bytes", len(out), err)
	}
	if err := <-fetchAfter; err == nil {
		t.Fatal("the source was still served after revocation")
	}
}

func TestRunCaptureRefusesWhenRevokedAtTheEnd(t *testing.T) {
	_, c := newCaptureFixture(t)
	var calls atomic.Int32
	c.PollInterval = time.Hour
	c.Revoked = func(context.Context) error {
		if calls.Add(1) > 1 {
			return errors.New("grant ended")
		}
		return nil
	}
	if out, err := RunCapture(context.Background(), c); !errors.Is(err, ErrRevoked) || out != nil {
		t.Fatalf("RunCapture = %d bytes, %v; want ErrRevoked", len(out), err)
	}
	_, c = newCaptureFixture(t)
	c.Revoked = func(context.Context) error { return errors.New("sealed") }
	c.Transcode = func(context.Context, Input, io.Writer) error { panic("tool started while revoked") }
	if _, err := RunCapture(context.Background(), c); !errors.Is(err, ErrRevoked) {
		t.Fatalf("err = %v, want ErrRevoked", err)
	}
}

func TestRunCaptureFailsOnASourceThatWillNotDecrypt(t *testing.T) {
	_, c := newCaptureFixture(t)
	c.Open = func() (io.ReadSeekCloser, error) { return nil, errors.New("decrypt failed") }
	c.Transcode = func(ctx context.Context, in Input, w io.Writer) error {
		_, _ = fetchAll(ctx, in.URL) // a tolerant tool exits 0 on a failed input
		_, err := w.Write([]byte("WEBVTT\n"))
		return err
	}
	if out, err := RunCapture(context.Background(), c); err == nil || out != nil {
		t.Fatalf("RunCapture = %d bytes, %v; want a failure", len(out), err)
	}
}

func TestRunCaptureRedactsTokenFromErrors(t *testing.T) {
	_, c := newCaptureFixture(t)
	errBoom := errors.New("input failed")
	var secret string
	c.Transcode = func(_ context.Context, in Input, _ io.Writer) error {
		secret = in.Secret
		return fmt.Errorf("reading %s: %w", in.URL, errBoom)
	}
	_, err := RunCapture(context.Background(), c)
	if err == nil || secret == "" || strings.Contains(err.Error(), secret) || !errors.Is(err, errBoom) {
		t.Fatalf("err = %v; want the redacted original", err)
	}
}

func TestRunCaptureValidates(t *testing.T) {
	for name, edit := range map[string]func(*Capture){
		"no source":           func(c *Capture) { c.Open = nil },
		"no tool":             func(c *Capture) { c.Transcode = nil },
		"no revocation check": func(c *Capture) { c.Revoked = nil },
		"negative size":       func(c *Capture) { c.Size = -1 },
		"no limit":            func(c *Capture) { c.MaxBytes = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			_, c := newCaptureFixture(t)
			edit(&c)
			if _, err := RunCapture(context.Background(), c); err == nil {
				t.Fatal("RunCapture accepted an invalid capture")
			}
		})
	}
}

func TestCaptureBufferWipeZeroesCapturedBytes(t *testing.T) {
	b := &captureBuffer{limit: 64}
	if _, err := b.Write([]byte("cue text")); err != nil {
		t.Fatal(err)
	}
	backing := b.buf[:cap(b.buf)]
	b.wipe()
	if !bytes.Equal(backing, make([]byte, len(backing))) {
		t.Fatal("wipe left captured bytes behind")
	}
}
