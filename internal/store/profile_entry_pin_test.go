package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func waitForProfileEntryPINVerifier(t *testing.T, st *Store, id ProfileID) string {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	retry := time.NewTicker(time.Millisecond)
	defer retry.Stop()

	for {
		var verifier string
		err := st.catalogR.QueryRowContext(t.Context(),
			`SELECT value FROM server_config WHERE key = ?`, profileEntryPINConfigKey(id)).Scan(&verifier)
		if err == nil {
			return verifier
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("read entry PIN verifier: %v", err)
		}
		select {
		case <-retry.C:
		case <-timeout.C:
			t.Fatalf("entry PIN verifier for %s was not committed within 5s", id)
		case <-t.Context().Done():
			t.Fatalf("wait for entry PIN verifier: %v", t.Context().Err())
		}
	}
}

func TestProfileEntryPINIsSaltedAndIndependentFromReveal(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{Revealed: true, AllLibraries: true}

	a, err := st.CreateProfile(ctx, "Entry Alpha", ProfileAdult, all)
	if err != nil {
		t.Fatalf("CreateProfile Alpha: %v", err)
	}
	b, err := st.CreateProfile(ctx, "Entry Bravo", ProfileAdult, all)
	if err != nil {
		t.Fatalf("CreateProfile Bravo: %v", err)
	}
	if err := st.SetProfileRevealPIN(ctx, a.ID, "1111"); err != nil {
		t.Fatalf("SetProfileRevealPIN: %v", err)
	}
	for _, id := range []ProfileID{a.ID, b.ID} {
		if err := st.SetProfileEntryPIN(ctx, id, "2222"); err != nil {
			t.Fatalf("SetProfileEntryPIN(%s): %v", id, err)
		}
	}

	va := waitForProfileEntryPINVerifier(t, st, a.ID)
	vb := waitForProfileEntryPINVerifier(t, st, b.ID)
	if !strings.HasPrefix(va, profileEntryPINArgon2Prefix) {
		t.Fatalf("entry PIN verifier %q is not Argon2-tagged", va)
	}
	if va == vb {
		t.Fatalf("same entry PIN produced identical verifiers %q; want per-Profile salts", va)
	}
	if strings.Contains(va, "2222") {
		t.Fatal("entry PIN appears in plaintext verifier")
	}
	if err := st.VerifyProfileEntryPIN(ctx, a.ID, "2222"); err != nil {
		t.Fatalf("VerifyProfileEntryPIN correct PIN: %v", err)
	}
	if err := st.VerifyProfileEntryPIN(ctx, a.ID, "1111"); !errors.Is(err, ErrProfileEntryPINMismatch) {
		t.Fatalf("Reveal PIN verified as entry PIN: %v", err)
	}
	if err := st.VerifyRevealPIN(ctx, a.ID, "2222"); !errors.Is(err, ErrRevealPINMismatch) {
		t.Fatalf("entry PIN verified as Reveal PIN: %v", err)
	}
}

func TestProfileEntryPINFailsClosedWithoutVerifier(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	adult, err := st.CreateProfile(ctx, "No Entry PIN", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}

	if err := st.VerifyProfileEntryPIN(ctx, "profile-default", "1234"); !errors.Is(err, ErrProfileEntryPINMismatch) {
		t.Fatalf("seed Reveal PIN verified as independent entry PIN: %v", err)
	}
	if err := st.VerifyProfileEntryPIN(ctx, "profile-default", developmentProfileEntryPIN); err != nil {
		t.Fatalf("independent seed entry PIN failed: %v", err)
	}
	if err := st.VerifyProfileEntryPIN(ctx, "profile-managed", "anything"); !errors.Is(err, ErrNoProfileEntryPIN) {
		t.Fatalf("managed entry verification = %v, want ErrNoProfileEntryPIN", err)
	}
	if err := st.SetProfileEntryPIN(ctx, "profile-managed", "2222"); !errors.Is(err, ErrNoProfileEntryPIN) {
		t.Fatalf("managed entry PIN set = %v, want ErrNoProfileEntryPIN", err)
	}
	if err := st.SetSessionProfileWithEntryPIN(ctx, "session-managed", adult.ID, "1234"); !errors.Is(err, ErrNoProfileEntryPIN) {
		t.Fatalf("adult without entry verifier switch = %v", err)
	}
	session, err := st.Session(ctx, "session-managed")
	if err != nil {
		t.Fatalf("Session after rejected no-verifier switch: %v", err)
	}
	if session.ProfileID != "profile-managed" {
		t.Fatalf("no-verifier switch mutated session: %+v", session)
	}
}

func TestSetSessionProfileWithEntryPINIsAtomicAndRelocks(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	target, err := st.CreateProfile(ctx, "Entry Target", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := st.SetProfileEntryPIN(ctx, target.ID, "4242"); err != nil {
		t.Fatalf("SetProfileEntryPIN: %v", err)
	}
	if _, err := st.catalogW.ExecContext(ctx,
		`UPDATE auth_session SET admin_until_ms = ? WHERE id = ?`, time.Now().Add(time.Hour).UnixMilli(), SessionID("session-revealed")); err != nil {
		t.Fatalf("seed admin session: %v", err)
	}

	for _, pin := range []string{"", "0000"} {
		if err := st.SetSessionProfileWithEntryPIN(ctx, "session-revealed", target.ID, pin); !errors.Is(err, ErrProfileEntryPINMismatch) {
			t.Fatalf("rejected entry PIN %q = %v, want ErrProfileEntryPINMismatch", pin, err)
		}
	}
	before, err := st.Session(ctx, "session-revealed")
	if err != nil {
		t.Fatalf("Session after rejected switch: %v", err)
	}
	if before.ProfileID != "profile-default" || !before.Revealed || before.AdminUntil.IsZero() {
		t.Fatalf("rejected switch mutated session: %+v", before)
	}

	if err := st.SetSessionProfileWithEntryPIN(ctx, "session-revealed", target.ID, "4242"); err != nil {
		t.Fatalf("correct entry PIN switch: %v", err)
	}
	after, err := st.Session(ctx, "session-revealed")
	if err != nil {
		t.Fatalf("Session after switch: %v", err)
	}
	if after.ProfileID != target.ID || after.Revealed || !after.AdminUntil.IsZero() {
		t.Fatalf("successful switch did not atomically rebind/relock: %+v", after)
	}
}

func TestSetSessionProfileWithEntryPINAllowsManagedTargetWithoutPIN(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.SetSessionProfileWithEntryPIN(ctx, "session-revealed", "profile-managed", ""); err != nil {
		t.Fatalf("managed target switch: %v", err)
	}
	session, err := st.Session(ctx, "session-revealed")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if session.ProfileID != "profile-managed" || session.Revealed {
		t.Fatalf("managed switch = %+v, want managed + relocked", session)
	}
}
