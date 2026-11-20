package ffmpeg

import (
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
)

func TestT3PlaybackPermitAtProcessStart(t *testing.T) {
	for _, probe := range []bool{false, true} {
		name := "encoder"
		if probe {
			name = "probe"
		}
		t.Run(name, func(t *testing.T) {
			entered, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(resume) }) }
			defer unblock()
			closed := make(chan struct{})
			permit := func() error {
				close(entered)
				<-resume
				select {
				case <-closed:
					return context.Canceled
				default:
					return nil
				}
			}
			marker := filepath.Join(t.TempDir(), "started")
			ctx := t.Context()
			cmd := exec.CommandContext(ctx, "sh", "-c", `printf start > "$1"`, "test", marker) // #nosec G204 -- fixed shell program and test-owned marker path.
			result := make(chan error, 1)
			go func() {
				if probe {
					ctx := WithPlaybackProbe(ctx, permit, func() bool { t.Error("probe handed off"); return false }, func() {})
					result <- supervise(ctx, cmd, io.Discard, io.Discard)
					return
				}
				ctx := WithPlaybackSpawn(ctx, permit, func(*HLSProcess) { t.Error("encoder handed off") })
				_, err := startHLSProcessContext(ctx, cmd, false)
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("spawn never requested its permit")
			}
			close(closed)
			unblock()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) || cmd.Process != nil {
					t.Fatalf("process = %v, error = %v", cmd.Process, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("denied spawn did not return")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("denied process ran: %v", err)
			}
		})
	}
}

func TestT3SharedResolutionCancelInitiatorAndFollower(t *testing.T) {
	for _, cancelFirst := range []bool{true, false} {
		name := "follower"
		if cancelFirst {
			name = "initiator"
		}
		t.Run(name, func(t *testing.T) {
			var result sharedResolution[string]
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			resolve := func() (string, error) {
				close(entered)
				<-release
				return "resolved", nil
			}
			type outcome struct {
				value string
				err   error
			}
			start := func(ctx context.Context) <-chan outcome {
				done := make(chan outcome, 1)
				go func() {
					value, err := result.wait(ctx, resolve)
					done <- outcome{value, err}
				}()
				return done
			}
			firstCtx, cancelFirstWait := context.WithCancel(t.Context())
			defer cancelFirstWait()
			first := start(firstCtx)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("shared worker did not start")
			}
			nextCtx, cancelNext := context.WithCancel(t.Context())
			defer cancelNext()
			next := start(nextCtx)
			canceled, alive := next, first
			if cancelFirst {
				canceled, alive = first, next
				cancelFirstWait()
			} else {
				cancelNext()
			}
			select {
			case got := <-canceled:
				if !errors.Is(got.err, context.Canceled) {
					t.Fatalf("canceled wait = %+v", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("canceled wait joined the shared worker")
			}
			select {
			case <-alive:
				t.Fatal("the surviving wait completed before the worker")
			default:
			}
			unblock()
			select {
			case got := <-alive:
				if got.value != "resolved" || got.err != nil {
					t.Fatalf("surviving wait = %+v", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("surviving wait missed publication")
			}
		})
	}
}

func TestT3EncryptedChildDiagnosticPolicyPrecedesHandoff(t *testing.T) {
	ctx := t.Context()
	var read atomic.Int32
	ctx = WithPlaybackSpawn(ctx, func() error { return nil }, func(child *HLSProcess) {
		if tail := child.StderrTail(); tail != "stderr withheld (encrypted Library)" {
			t.Errorf("first stderr read = %q", tail)
		}
		read.Add(1)
		if err := child.Wait(); err != nil {
			t.Errorf("encrypted fixture reap: %v", err)
		}
	})
	cmd := exec.CommandContext(ctx, "sh", "-c", "printf hidden >&2")
	child, err := startWithheldHLSProcessContext(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil || read.Load() != 1 {
		t.Fatalf("handoff/reap = %d, %v", read.Load(), err)
	}
}
