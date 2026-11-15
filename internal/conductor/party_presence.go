package conductor

import (
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
)

// Watch Party presence (CORE-1763). A browser tab that closes without leaving
// would otherwise stay on the roster for as long as the process runs. The
// server already knows when a session's event stream, the channel every party
// member follows the timeline on, drops: the event surface reports each open
// and close here, and a party member whose last stream stayed closed past a
// grace window is removed as if it had left. Presence is in memory only,
// like every other Conductor state.

// presence counts open event streams per session and remembers when a
// session's last stream closed. It has its own lock, so stream bookkeeping
// never waits on Registry.mu or a Group.mu.
type presence struct {
	mu        sync.Mutex
	streams   map[store.SessionID]int
	droppedAt map[store.SessionID]time.Time
}

// PartyExpiry is one party member removed because its event stream stayed
// closed past the grace window.
type PartyExpiry struct {
	PartyID string
	Session store.SessionID
	Leave   PartyLeave
}

// StreamOpened records that session opened an event stream. A reopen within
// the grace window (a reconnect) cancels a pending expiry.
func (r *Registry) StreamOpened(session store.SessionID) {
	if session == "" {
		return
	}
	r.presence.mu.Lock()
	defer r.presence.mu.Unlock()
	if r.presence.streams == nil {
		r.presence.streams = map[store.SessionID]int{}
	}
	r.presence.streams[session]++
	delete(r.presence.droppedAt, session)
}

// StreamClosed records that session closed an event stream. It reports true
// when that was the session's last open stream, so the caller can schedule an
// expiry sweep.
func (r *Registry) StreamClosed(session store.SessionID) bool {
	if session == "" {
		return false
	}
	now := r.now()
	r.presence.mu.Lock()
	defer r.presence.mu.Unlock()
	n := r.presence.streams[session]
	if n <= 0 {
		return false
	}
	if n > 1 {
		r.presence.streams[session] = n - 1
		return false
	}
	delete(r.presence.streams, session)
	if r.presence.droppedAt == nil {
		r.presence.droppedAt = map[store.SessionID]time.Time{}
	}
	r.presence.droppedAt[session] = now
	return true
}

// SessionInParty reports whether session is a member of any Watch Party.
func (r *Registry) SessionInParty(session store.SessionID) bool {
	return len(r.partiesOf(session)) > 0
}

// partiesOf lists the Watch Parties session is a member of. It snapshots the
// group set under Registry.mu and reads each group after releasing it, so it
// never holds Registry.mu while taking a Group.mu.
func (r *Registry) partiesOf(session store.SessionID) []string {
	r.mu.RLock()
	groups := make([]*Group, 0, len(r.groups))
	for _, g := range r.groups {
		groups = append(groups, g)
	}
	r.mu.RUnlock()
	var ids []string
	for _, g := range groups {
		g.mu.Lock()
		_, member := g.members[session]
		party := g.posture == PostureWatchParty
		g.mu.Unlock()
		if party && member {
			ids = append(ids, g.ID)
		}
	}
	return ids
}

// ExpireDisconnected removes every party member whose last event stream closed
// at least grace ago and has not reopened, handing the host role on or
// dissolving the party exactly as LeaveParty does. Sessions that never opened
// a stream are never expired: presence is a backstop for dropped streams, not
// a requirement to hold one.
func (r *Registry) ExpireDisconnected(grace time.Duration) []PartyExpiry {
	now := r.now()
	r.presence.mu.Lock()
	var due []store.SessionID
	for session, at := range r.presence.droppedAt {
		if now.Sub(at) >= grace {
			due = append(due, session)
			delete(r.presence.droppedAt, session)
		}
	}
	r.presence.mu.Unlock()
	var out []PartyExpiry
	for _, session := range due {
		for _, id := range r.partiesOf(session) {
			leave, err := r.LeaveParty(id, session)
			if err != nil {
				continue // left or dissolved in the meantime
			}
			out = append(out, PartyExpiry{PartyID: id, Session: session, Leave: leave})
		}
	}
	return out
}
