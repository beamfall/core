//go:build unix

package jobs

import (
	"context"
	"errors"
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
			ctx := context.Background()
			st := storetest.Open(t)
			now := time.Now().UTC()
			if _, err := st.EnqueueJob(ctx, store.Job{ID: "job-cancel", Kind: "asset-sweep", CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			registry := NewRegistry(st, "worker-a")
			if !withRenewal {
				registry.renewInterval = 0
			}
			started := make(chan int, 1)
			registry.Register("asset-sweep", func(handlerCtx context.Context, _ store.Job) (Result, error) {
				// The same lifecycle the ffmpeg supervisor gives a child: its
				// own process group, killed whole when the context ends.
				cmd := exec.CommandContext(handlerCtx, "sh", "-c", "sleep 60 & sleep 60")
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
				if err := cmd.Start(); err != nil {
					return Result{}, err
				}
				started <- cmd.Process.Pid
				return Result{Checkpoint: []byte(`{"cursor":"partial"}`)}, cmd.Wait()
			})

			returned := make(chan error, 1)
			go func() {
				_, err := registry.RunOne(ctx, nil, now)
				returned <- err
			}()
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
