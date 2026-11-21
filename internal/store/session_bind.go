package store

import (
	"context"
	"errors"
)

// ErrSessionProfileMismatch reports that a session is not bound to the
// Profile a write was made for, so the write did not run.
var ErrSessionProfileMismatch = errors.New("session is bound to another profile")

// RunWhileSessionBound runs write only while sessionID stays bound to
// profileID. It refuses with ErrSessionProfileMismatch, running nothing, when
// the session is bound to another Profile. No Profile rebind of any session
// (SetSessionProfile, SetSessionProfileWithEntryPIN) can commit between the
// check and write's return, so a write that commits its own transaction
// inside write lands only while the binding it checked still holds. The
// session row lives in the catalog database and most writes in the events
// database, so one SQL transaction cannot cover both; the lock does.
//
// write must not rebind a session.
func (s *Store) RunWhileSessionBound(ctx context.Context, sessionID SessionID, profileID ProfileID, write func() error) error {
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
	return write()
}
