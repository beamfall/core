package server

import (
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

type recordingStreamObserver struct {
	mu     sync.Mutex
	events []string
}

func (o *recordingStreamObserver) EventStreamOpened(session store.SessionID) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, "open:"+string(session))
}

func (o *recordingStreamObserver) EventStreamClosed(session store.SessionID) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, "close:"+string(session))
}

func (o *recordingStreamObserver) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.events...)
}

// TestEventStreamObserverSeesSessionOpenAndClose: Watch Party presence
// (CORE-1763) hears when a session's event stream opens and when it closes.
//
// feature:sse-events
func TestEventStreamObserverSeesSessionOpenAndClose(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	observer := &recordingStreamObserver{}
	srv.SetEventStreamObserver(observer)

	var during []string
	serveStreamFor(t, srv, 300*time.Millisecond, func() {
		deadline := time.Now().Add(250 * time.Millisecond)
		for len(observer.snapshot()) == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		during = observer.snapshot()
	})
	if len(during) != 1 || during[0] != "open:session-default" {
		t.Fatalf("observer while streaming = %v, want [open:session-default]", during)
	}
	if got := observer.snapshot(); len(got) != 2 || got[1] != "close:session-default" {
		t.Fatalf("observer after the stream ended = %v, want open then close", got)
	}
}
