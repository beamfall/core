package store

import (
	"context"
	"errors"
)

// ErrSessionProfileMismatch reports that a session is not bound to the
// Profile a write was made for, so the write did not run.
var ErrSessionProfileMismatch = errors.New("session is bound to another profile")

// ErrSessionRevealMismatch reports that a session's Reveal state changed
// before a guarded write could apply.
var ErrSessionRevealMismatch = errors.New("the session's reveal state changed")

// RunWhileSessionBound is RunWhileSessionMatches without a Reveal precondition.
func (s *Store) RunWhileSessionBound(ctx context.Context, sessionID SessionID, profileID ProfileID, write func() error) error {
	return s.RunWhileSessionMatches(ctx, sessionID, profileID, nil, write)
}

// RunWhileSessionMatches verifies the Profile binding and optional Reveal
// state, then holds them stable until write returns. The
// session row lives in the catalog database and most writes in the events
// database, so one SQL transaction cannot cover both; the lock does.
//
// write must not rebind a session or change its Reveal state.
func (s *Store) RunWhileSessionMatches(ctx context.Context, sessionID SessionID, profileID ProfileID, revealed *bool, write func() error) error {
	if sessionID == "" || profileID == "" {
		return ValidationError("session ID and profile ID are required")
	}
	s.sessionBindMu.RLock()
	defer s.sessionBindMu.RUnlock()
	session, err := s.Session(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.ProfileID != profileID {
		return ErrSessionProfileMismatch
	}
	if revealed != nil && session.Revealed != *revealed {
		return ErrSessionRevealMismatch
	}
	return write()
}
