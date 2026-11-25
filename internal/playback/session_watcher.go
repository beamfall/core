package playback

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/fsnotify/fsnotify"
)

// sessionWatcher shares one fsnotify.Watcher across all concurrent artifact
// waiters of a single hlsSession so N viewers or rung prefetches consume at most
// one OS watcher FD per session.
type sessionWatcher struct {
	mu      sync.Mutex
	watcher *fsnotify.Watcher
	watched map[string]struct{}
	notify  chan struct{}
	closed  bool
	failed  bool
}

func (h *hlsSession) sharedWatcher() *sessionWatcher {
	h.watcherMu.Lock()
	defer h.watcherMu.Unlock()
	if h.watcher == nil {
		h.watcher = &sessionWatcher{watched: make(map[string]struct{})}
	}
	return h.watcher
}

func (h *hlsSession) closeWatcher() {
	h.watcherMu.Lock()
	w := h.watcher
	h.watcherMu.Unlock()
	if w != nil {
		w.close()
	}
}

func (w *sessionWatcher) close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	fw := w.watcher
	w.watcher = nil
	ch := w.notify
	w.notify = nil
	w.mu.Unlock()
	if ch != nil {
		close(ch)
	}
	if fw != nil {
		_ = fw.Close()
	}
}

func (w *sessionWatcher) arm(dir string) (<-chan struct{}, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.failed {
		return nil, false
	}
	if w.watcher == nil {
		fw, err := fsnotify.NewWatcher()
		if err != nil {
			w.failed = true
			return nil, false
		}
		w.watcher = fw
		go w.pump(fw)
	}
	cleanDir := filepath.Clean(dir)
	if _, ok := w.watched[cleanDir]; !ok {
		if err := w.watcher.Add(cleanDir); err != nil {
			return nil, false
		}
		w.watched[cleanDir] = struct{}{}
	}
	if w.notify == nil {
		w.notify = make(chan struct{})
	}
	return w.notify, true
}

func (w *sessionWatcher) subscribe() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.notify == nil {
		w.notify = make(chan struct{})
	}
	return w.notify
}

func (w *sessionWatcher) broadcast() {
	w.mu.Lock()
	ch := w.notify
	w.notify = nil
	w.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (w *sessionWatcher) pump(fw *fsnotify.Watcher) {
	for {
		select {
		case _, ok := <-fw.Events:
			if !ok {
				return
			}
			w.broadcast()
		case _, ok := <-fw.Errors:
			if !ok {
				return
			}
			w.mu.Lock()
			w.failed = true
			w.mu.Unlock()
			w.broadcast()
			return
		}
	}
}

func (w *sessionWatcher) wait(ctx context.Context, path string, exited, cutoff <-chan struct{}, ready func(string) bool) error {
	if ready(path) {
		return nil
	}
	ch, ok := w.arm(filepath.Dir(path))
	if !ok {
		return pollForArtifact(ctx, path, exited, cutoff, ready)
	}
	if ready(path) {
		return nil
	}
	deadline := time.NewTimer(fileWaitTimeout)
	defer deadline.Stop()
	safety := time.NewTicker(safetyPollInterval)
	defer safety.Stop()
	settle := time.NewTimer(fileEventSettleDelay)
	if !settle.Stop() {
		<-settle.C
	}
	defer settle.Stop()
	var settleC <-chan time.Time
	for {
		select {
		case <-cutoff:
			return ErrSessionEvicted
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("stream not ready")
		case <-exited:
			if ready(path) {
				return nil
			}
			return errEncoderExited
		case <-ch:
			if ready(path) {
				return nil
			}
			ch = w.subscribe()
			if ready(path) {
				return nil
			}
			if settleC == nil {
				settle.Reset(fileEventSettleDelay)
				settleC = settle.C
			}
		case <-settleC:
			settleC = nil
			if ready(path) {
				return nil
			}
		case <-safety.C:
			if ready(path) {
				return nil
			}
		}
	}
}

func waitForLiveSession(ctx context.Context, live *encderiv.LiveSession, path string, exited, cutoff <-chan struct{}, ready func(string) bool) error {
	if ready(path) {
		return nil
	}
	ch := live.WaitChan()
	if ready(path) {
		return nil
	}
	deadline := time.NewTimer(fileWaitTimeout)
	defer deadline.Stop()
	safety := time.NewTicker(safetyPollInterval)
	defer safety.Stop()
	for {
		select {
		case <-cutoff:
			return ErrSessionEvicted
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("stream not ready")
		case <-exited:
			if ready(path) {
				return nil
			}
			return errEncoderExited
		case <-ch:
			if ready(path) {
				return nil
			}
			ch = live.WaitChan()
			if ready(path) {
				return nil
			}
		case <-safety.C:
			if ready(path) {
				return nil
			}
		}
	}
}
