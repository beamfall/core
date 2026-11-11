package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSignInSecurityModeDefaultsToHome(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != SignInSecurityHome {
		t.Fatalf("SignInSecurityMode on a fresh store = (%q, %v), want home", mode, err)
	}
}

func TestBootstrapOwnerStoresSignInSecurityMode(t *testing.T) {
	for _, tc := range []struct {
		in   SignInSecurityMode
		want SignInSecurityMode
	}{{"", SignInSecurityHome}, {SignInSecurityHome, SignInSecurityHome}, {SignInSecurityStrict, SignInSecurityStrict}} {
		t.Run(string(tc.want)+"/"+string(tc.in), func(t *testing.T) {
			ctx := context.Background()
			st, err := Open(ctx, t.TempDir(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			device, session := bootstrapEnrollment("mode")
			if _, err := st.BootstrapOwner(ctx, "Owner", device, session, tc.in); err != nil {
				t.Fatalf("BootstrapOwner: %v", err)
			}
			if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != tc.want {
				t.Fatalf("SignInSecurityMode = (%q, %v), want %q", mode, err, tc.want)
			}
		})
	}
}

func TestBootstrapOwnerRejectsUnknownSignInSecurityMode(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	device, session := bootstrapEnrollment("bad")
	_, err = st.BootstrapOwner(ctx, "Owner", device, session, "paranoid")
	var verr ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("BootstrapOwner(paranoid) = %v, want ValidationError", err)
	}
	if n := countRows(t, st, "profile"); n != 0 {
		t.Fatalf("profile rows after refused setup = %d, want 0", n)
	}
}

func TestBootstrapOwnerFailureStoresNoSignInSecurityMode(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER inject_enroll_failure BEFORE INSERT ON auth_session
		BEGIN SELECT RAISE(ABORT, 'injected enroll failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	device, session := bootstrapEnrollment("fail")
	if _, err := st.BootstrapOwner(ctx, "Owner", device, session, SignInSecurityStrict); err == nil {
		t.Fatal("BootstrapOwner succeeded through an injected session failure")
	}
	var n int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM server_config WHERE key = ?`, configSignInSecurityMode).Scan(&n); err != nil || n != 0 {
		t.Fatalf("mode rows after failed setup = (%d, %v), want 0", n, err)
	}
}

func TestSetSignInSecurityModeReturnsOldMode(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Unix(1_800_000_000, 0)
	old, err := st.SetSignInSecurityMode(ctx, SignInSecurityStrict, now)
	if err != nil || old != SignInSecurityHome {
		t.Fatalf("SetSignInSecurityMode(strict) = (%q, %v), want old home", old, err)
	}
	old, err = st.SetSignInSecurityMode(ctx, SignInSecurityHome, now)
	if err != nil || old != SignInSecurityStrict {
		t.Fatalf("SetSignInSecurityMode(home) = (%q, %v), want old strict", old, err)
	}
	var verr ValidationError
	if _, err := st.SetSignInSecurityMode(ctx, "open", now); !errors.As(err, &verr) {
		t.Fatalf("SetSignInSecurityMode(open) = %v, want ValidationError", err)
	}
	if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != SignInSecurityHome {
		t.Fatalf("mode after refused write = (%q, %v), want home", mode, err)
	}
}
