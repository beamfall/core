package store

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// CORE-1832: ImportManagedFile holds s.mu (read) across its catalog admission.
// sync.RWMutex forbids recursive read locking: a writer queued on s.mu blocks
// every new reader, so a second RLock taken by the same goroutine waits behind
// the writer while the writer waits behind the first RLock.
//
// The catalog step hook queues a writer at exactly that point. TryRLock reports
// false once a writer is pending, which lets the test wait for the queued
// writer without sleeping. Before the fix the admission re-acquired s.mu
// through ImportMediaFile and the import never returned.
func TestImportManagedFileCatalogAdmissionUnderPendingWriter(t *testing.T) {
	ctx := context.Background()
	fx := newManagedScanRaceFixture(t)
	token := fx.token(t, "pending.mkv", "pending-writer-bytes")

	writerDone := make(chan struct{})
	fx.st.managedImportStepHook = func(step string) {
		if step != "catalog" {
			return
		}
		go func() {
			fx.st.mu.Lock()
			close(writerDone)
			fx.st.mu.Unlock()
		}()
		for fx.st.mu.TryRLock() {
			fx.st.mu.RUnlock()
			runtime.Gosched()
		}
	}

	imported := make(chan error, 1)
	go func() {
		_, err := fx.importManaged(ctx, token)
		imported <- err
	}()
	select {
	case err := <-imported:
		if err != nil {
			t.Fatalf("ImportManagedFile: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ImportManagedFile deadlocked: its catalog admission re-acquired s.mu behind a pending writer")
	}
	select {
	case <-writerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("writer queued during the managed admission never acquired s.mu")
	}
	fx.st.managedImportStepHook = nil
	if _, ok := fx.liveRows(t)[filepath.Join(fx.root, "pending.mkv")]; !ok {
		t.Fatal("managed admission did not catalog pending.mkv")
	}
}
