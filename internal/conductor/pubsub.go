package conductor

import (
	"sync"

	"github.com/beamfall/core/internal/store"
)

const groupSyncSubBuffer = 64

// GroupSyncHub fans out ephemeral group.sync payloads in memory to active SSE
// subscribers without writing SQLite outbox rows for every heartbeat or nudge.
type GroupSyncHub struct {
	mu   sync.Mutex
	subs map[chan store.GroupSyncPayload]struct{}
}

// NewGroupSyncHub returns an initialized GroupSyncHub.
func NewGroupSyncHub() *GroupSyncHub {
	return &GroupSyncHub{
		subs: make(map[chan store.GroupSyncPayload]struct{}),
	}
}

// Subscribe registers a bounded non-blocking subscriber channel and returns an
// unsubscribe function.
func (h *GroupSyncHub) Subscribe() (<-chan store.GroupSyncPayload, func()) {
	ch := make(chan store.GroupSyncPayload, groupSyncSubBuffer)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = make(map[chan store.GroupSyncPayload]struct{})
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// HasSubscribers reports whether any SSE subscriber is currently listening.
func (h *GroupSyncHub) HasSubscribers() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0
}

// Publish delivers payload to all active subscribers without blocking on slow
// clients.
func (h *GroupSyncHub) Publish(payload store.GroupSyncPayload) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- payload:
		default:
		}
	}
}
