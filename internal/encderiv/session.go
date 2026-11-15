package encderiv

import (
	"context"
	"sync"
	"time"
)

// sessionConfig is what every derivative mode shares: the source the endpoint
// serves, the token's lifetime, and the authorization it is revoked by.
type sessionConfig struct {
	open    Opener
	size    int64
	ttl     time.Duration
	idle    time.Duration
	poll    time.Duration
	revoked func(context.Context) error
	now     func() time.Time
	peer    peerChecker
}

// session is one authorized window over a decrypted source: a per-job
// loopback endpoint with its own token, a context bounded by the TTL, and a
// watcher that revokes the token and cancels the context the moment
// authorization ends. Every mode (staged fragmented MP4, in-memory capture)
// runs its tools inside one, so the ADR-0235 properties live in one place.
type session struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	ep     *endpoint

	cancelTTL context.CancelFunc
	stopWatch chan struct{}
	watchers  sync.WaitGroup
}

func startSession(parent context.Context, cfg sessionConfig) (*session, error) {
	ttlCtx, cancelTTL := context.WithTimeoutCause(parent, cfg.ttl, ErrExpired)
	ctx, cancel := context.WithCancelCause(ttlCtx)
	ep, err := startEndpoint(endpointConfig{
		open:     cfg.open,
		size:     cfg.size,
		deadline: cfg.now().Add(cfg.ttl),
		idle:     cfg.idle,
		now:      cfg.now,
		peer:     cfg.peer,
	})
	if err != nil {
		cancel(nil)
		cancelTTL()
		return nil, err
	}
	s := &session{ctx: ctx, cancel: cancel, ep: ep, cancelTTL: cancelTTL, stopWatch: make(chan struct{})}
	s.watchers.Go(func() { watchRevocation(ctx, cfg.poll, cfg.revoked, ep, cancel, s.stopWatch) })
	return s, nil
}

// input is what a tool run inside the session is handed.
func (s *session) input() Input {
	return Input{URL: s.ep.URL(), Secret: s.ep.token, Port: s.ep.Port()}
}

// close ends the token, stops the watcher and cancels the session's context.
func (s *session) close() {
	s.ep.Revoke()
	close(s.stopWatch)
	s.watchers.Wait()
	s.cancel(nil)
	s.cancelTTL()
}

// watchRevocation re-checks authorization until the session ends. On
// revocation it ends the token first, so no more plaintext is served, then
// cancels the session, which kills the tool.
func watchRevocation(ctx context.Context, poll time.Duration, revoked func(context.Context) error, ep *endpoint, cancel context.CancelCauseFunc, stop <-chan struct{}) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if err := revoked(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				ep.Revoke()
				cancel(ErrRevoked)
				return
			}
		}
	}
}
