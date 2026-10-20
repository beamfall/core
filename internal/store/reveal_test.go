package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// readRevealHash returns the stored reveal_pin_hash for a Profile.
func readRevealHash(t *testing.T, st *Store, id ProfileID) string {
	t.Helper()
	var hash sql.NullString
	if err := st.catalogR.QueryRowContext(context.Background(),
		`SELECT reveal_pin_hash FROM profile WHERE id = ?`, id).Scan(&hash); err != nil {
		t.Fatalf("read reveal_pin_hash: %v", err)
	}
	return hash.String
}

func TestSetRevealPINSaltsPerProfile(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	a, err := st.CreateProfile(ctx, "Alpha", ProfileAdult, revealed)
	if err != nil {
		t.Fatalf("CreateProfile Alpha: %v", err)
	}
	b, err := st.CreateProfile(ctx, "Bravo", ProfileAdult, revealed)
	if err != nil {
		t.Fatalf("CreateProfile Bravo: %v", err)
	}

	const pin = "1234"
	if err := st.SetProfileRevealPIN(ctx, a.ID, pin); err != nil {
		t.Fatalf("SetProfileRevealPIN Alpha: %v", err)
	}
	if err := st.SetProfileRevealPIN(ctx, b.ID, pin); err != nil {
		t.Fatalf("SetProfileRevealPIN Bravo: %v", err)
	}

	ha, hb := readRevealHash(t, st, a.ID), readRevealHash(t, st, b.ID)
	if !strings.HasPrefix(ha, revealArgon2Prefix) {
		t.Fatalf("stored hash %q is not argon2-tagged", ha)
	}
	if ha == hb {
		t.Fatalf("two Profiles with the same PIN produced identical stored hashes (%q) — salt not applied", ha)
	}
	// The bare-sha256 of the PIN must never appear verbatim in storage.
	if ha == HashDeviceSecret(pin) || hb == HashDeviceSecret(pin) {
		t.Fatal("stored hash equals bare sha256(pin) — PIN is not stretched")
	}
}

func TestVerifyRevealPINCorrectAndWrong(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	p, err := st.CreateProfile(ctx, "Adult", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := st.SetProfileRevealPIN(ctx, p.ID, "4242"); err != nil {
		t.Fatalf("SetProfileRevealPIN: %v", err)
	}

	if err := st.VerifyRevealPIN(ctx, p.ID, "4242"); err != nil {
		t.Fatalf("VerifyRevealPIN correct PIN: got %v, want nil", err)
	}
	if err := st.VerifyRevealPIN(ctx, p.ID, "0000"); !errors.Is(err, ErrRevealPINMismatch) {
		t.Fatalf("VerifyRevealPIN wrong PIN: got %v, want ErrRevealPINMismatch", err)
	}
}

func TestVerifyRevealPINUpgradesLegacySHA256(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	p, err := st.CreateProfile(ctx, "Adult", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// Simulate a pre-existing verifier written by the old bare-sha256 scheme
	// (no per-Profile salt), exactly as stored before this migration.
	const pin = "9876"
	legacy := HashDeviceSecret(pin)
	if _, err := st.catalogW.ExecContext(ctx,
		`UPDATE profile SET reveal_pin_hash = ?, reveal_pin_salt = NULL WHERE id = ?`,
		legacy, p.ID); err != nil {
		t.Fatalf("inject legacy hash: %v", err)
	}
	if got := readRevealHash(t, st, p.ID); got != legacy {
		t.Fatalf("precondition: stored hash = %q, want legacy %q", got, legacy)
	}

	// A correct PIN against the legacy verifier must verify...
	if err := st.VerifyRevealPIN(ctx, p.ID, pin); err != nil {
		t.Fatalf("VerifyRevealPIN legacy correct PIN: got %v, want nil", err)
	}
	// ...and the verifier must be upgraded in place to the argon2id scheme.
	upgraded := readRevealHash(t, st, p.ID)
	if !strings.HasPrefix(upgraded, revealArgon2Prefix) {
		t.Fatalf("legacy hash was not upgraded: stored %q", upgraded)
	}
	if upgraded == legacy {
		t.Fatal("stored hash unchanged after verify — upgrade-on-verify did not run")
	}
	// The upgraded verifier must still accept the same PIN and reject others.
	if err := st.VerifyRevealPIN(ctx, p.ID, pin); err != nil {
		t.Fatalf("VerifyRevealPIN after upgrade: got %v, want nil", err)
	}
	if err := st.VerifyRevealPIN(ctx, p.ID, "0001"); !errors.Is(err, ErrRevealPINMismatch) {
		t.Fatalf("VerifyRevealPIN wrong PIN after upgrade: got %v, want ErrRevealPINMismatch", err)
	}
}
