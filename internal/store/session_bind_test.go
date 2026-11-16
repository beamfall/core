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

// A rebind cannot commit while a bound write is between its check and its
// return, so the write never lands after the session moved on (CORE-2177).
func TestRunWhileSessionBoundHoldsRebindsOff(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	for name, rebind := range map[string]func() error{
		"SetSessionProfile": func() error { return st.SetSessionProfile(ctx, "session-default", "profile-managed") },
		"SetSessionProfileWithEntryPIN": func() error {
			return st.SetSessionProfileWithEntryPIN(ctx, "session-default", "profile-managed", "")
		},
	} {
		if err := st.SetSessionProfile(ctx, "session-default", "profile-default"); err != nil {
			t.Fatal(err)
		}
		rebound := make(chan error, 1)
		err := st.RunWhileSessionBound(ctx, "session-default", "profile-default", func() error {
			go func() { rebound <- rebind() }()
			select {
			case err := <-rebound:
				t.Errorf("%s committed inside the bound write: %v", name, err)
			case <-time.After(100 * time.Millisecond):
			}
			session, err := st.Session(ctx, "session-default")
			if err != nil {
				return err
			}
			if session.ProfileID != "profile-default" {
				t.Errorf("%s: binding moved to %s inside the bound write", name, session.ProfileID)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-rebound:
			if err != nil {
				t.Fatalf("%s after the bound write: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never ran after the bound write returned", name)
		}
	}
}
