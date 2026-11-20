//go:build unix

package jobs

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestCancelRunningStopsHandlerAndItsChildProcessGroup covers CORE-2394: an
// operator cancel reaches the running handler's context at once, rather than
// at the next lease renewal minutes later, so the handler returns and the
// child process tree it started under that context is killed within seconds.
// The renewal interval stays at its production value, so only CancelRunning
// can stop the handler inside the bound.
func TestCancelRunningStopsHandlerAndItsChildProcessGroup(t *testing.T) {
	for _, withRenewal := range []bool{true, false} {
		t.Run(map[bool]string{true: "renewing store", false: "store without renewal"}[withRenewal], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			st := storetest.Open(t)
			t.Cleanup(cancel)
			now := time.Now().UTC()
			if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-cancel", Kind: "asset-sweep", CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			registry := NewRegistry(st, "worker-a")
			if !withRenewal {
				registry.renewInterval = 0
			}
			// The background subshell blocks reading fd 3 until the test
			// closes this pipe, so a member the cancel misses exits at
			// cleanup instead of outliving the test.
			hold, release, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = hold.Close() })
			started := make(chan int, 1)
			registry.Register("asset-sweep", func(handlerCtx context.Context, _ store.Job) (Result, error) {
				// The same lifecycle the ffmpeg supervisor gives a child: its
				// own process group, killed whole when the context ends.
				// The background subshell reports ready itself, after its fork
				// has completed, and neither shell forks or execs again. Under
				// load on macOS a group SIGKILL sent while a member is still
				// forking or exec'ing can miss it, leaving an orphan that
				// outlives the bound; cancelling earlier tested that race, not
				// the cancel path (CORE-2710).
				cmd := exec.CommandContext(handlerCtx, "sh", "-c", `(echo ready; read -r _ <&3) & wait`)
				cmd.ExtraFiles = []*os.File{hold}
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					return Result{}, err
				}
				if err := cmd.Start(); err != nil {
					return Result{}, err
				}
				if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
					return Result{}, errors.Join(err, cmd.Wait())
				}
				started <- cmd.Process.Pid
				return Result{Checkpoint: []byte(`{"cursor":"partial"}`)}, cmd.Wait()
			})

			returned := make(chan error, 1)
			workerDone := make(chan struct{})
			go func() {
				defer close(workerDone)
				_, err := registry.RunOne(ctx, nil, now)
				returned <- err
			}()
			// Before the store closes, stop the worker, release any member
			// the cancel missed, and wait for its bookkeeping to finish.
			t.Cleanup(func() { cancel(); _ = release.Close(); <-workerDone })
			var pgid int
			select {
			case pgid = <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("handler never started its child")
			}
			if err := st.CancelJob(ctx, "job-cancel", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if !registry.CancelRunning("job-cancel") {
				t.Fatal("CancelRunning found no running handler for the job")
			}

			select {
			case err := <-returned:
				if err != nil {
					t.Fatalf("RunOne after cancel = %v, want nil", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("handler still running 5 s after the job was cancelled")
			}
			// SIGKILL reaches the group at once, but its members are reaped
			// asynchronously (the orphaned sleep by init), so under load the
			// group can still answer kill(0), or EPERM on macOS while its last
			// member is a zombie. Hold it to the same 5 s bound as the handler.
			deadline := time.Now().Add(5 * time.Second)
			for {
				err := syscall.Kill(-pgid, 0)
				if errors.Is(err, syscall.ESRCH) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("child process group %d still present 5 s after cancel: kill(0) = %v, want ESRCH", pgid, err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			job, err := st.Job(ctx, "job-cancel")
			if err != nil {
				t.Fatal(err)
			}
			if job.Status != store.JobStatusCanceled || job.Error != "" || string(job.Checkpoint) != `{}` {
				t.Fatalf("cancelled job after its worker stopped = %#v, want canceled with no error or checkpoint written", job)
			}
			if registry.CancelRunning("job-cancel") {
				t.Fatal("CancelRunning still finds the job after RunOne returned")
			}
		})
	}
}
