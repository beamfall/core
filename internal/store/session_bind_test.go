package store

import (
	"errors"
	"testing"
	"time"
)

func TestRunWhileSessionBoundRefusesAnotherProfile(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	if err := st.SetSessionProfile(ctx, "session-default", "profile-managed"); err != nil {
		t.Fatal(err)
	}
	ran := false
	err := st.RunWhileSessionBound(ctx, "session-default", "profile-default", func() error { ran = true; return nil })
	if !errors.Is(err, ErrSessionProfileMismatch) || ran {
		t.Fatalf("err = %v, ran = %v; want ErrSessionProfileMismatch and no write", err, ran)
	}
	if err := st.RunWhileSessionBound(ctx, "session-default", "profile-managed", func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("matching binding err = %v, ran = %v", err, ran)
	}
}

// A Profile or Reveal change cannot commit while a guarded write is between
// its precondition check and its return.
func TestRunWhileSessionMatchesHoldsScopeChangesOff(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	for name, change := range map[string]func() error{
		"SetSessionProfile": func() error { return st.SetSessionProfile(ctx, "session-default", "profile-managed") },
		"SetSessionProfileWithEntryPIN": func() error {
			return st.SetSessionProfileWithEntryPIN(ctx, "session-default", "profile-managed", "")
		},
		"Relock": func() error { return st.SetSessionRevealed(ctx, "session-default", false) },
	} {
		if err := st.SetSessionProfile(ctx, "session-default", "profile-default"); err != nil {
			t.Fatal(err)
		}
		revealed := true
		if err := st.SetSessionRevealed(ctx, "session-default", revealed); err != nil {
			t.Fatal(err)
		}
		changed := make(chan error, 1)
		err := st.RunWhileSessionMatches(ctx, "session-default", "profile-default", &revealed, func() error {
			go func() { changed <- change() }()
			select {
			case err := <-changed:
				t.Fatalf("%s returned inside the guarded write: %v", name, err)
			case <-time.After(100 * time.Millisecond):
			}
			session, err := st.Session(ctx, "session-default")
			if err != nil {
				return err
			}
			if session.ProfileID != "profile-default" || !session.Revealed {
				t.Errorf("%s: session changed to Profile %s, Revealed %v inside the guarded write", name, session.ProfileID, session.Revealed)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-changed:
			if err != nil {
				t.Fatalf("%s after the guarded write: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never ran after the guarded write returned", name)
		}
	}
}
