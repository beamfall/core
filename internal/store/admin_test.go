package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// CORE-1601: every new admin password has at least MinAdminPasswordLength code
// points, with one client-safe message; verify never applies the rule.
func TestSetAdminPasswordEnforcesMinimumLength(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()

	err := store.SetAdminPassword(ctx, "seven77", time.Now().UTC())
	if !errors.Is(err, ErrAdminPasswordTooShort) {
		t.Fatalf("SetAdminPassword(7 chars) = %v, want ErrAdminPasswordTooShort", err)
	}
	var invalid ValidationError
	if !errors.As(err, &invalid) || invalid.Error() != "Admin password must be at least 8 characters." {
		t.Fatalf("refusal = %q, want the shared ValidationError message", err)
	}

	// Eight code points pass even though they are far more than eight bytes.
	multibyte := "ñéüßøåçö"
	if err := store.SetAdminPassword(ctx, multibyte, time.Now().UTC()); err != nil {
		t.Fatalf("SetAdminPassword(8 runes) returned error: %v", err)
	}
	if err := store.VerifyAdminPassword(ctx, multibyte); err != nil {
		t.Fatalf("VerifyAdminPassword returned error: %v", err)
	}
}

func TestCreateFirstAdminPasswordRefusesShortPassword(t *testing.T) {
	store := openSeeded(t)
	now := time.Now().UTC()
	ok, err := store.CreateFirstAdminPassword(context.Background(), "session-manager", "profile-default", "short", now, now.Add(time.Hour))
	if ok || !errors.Is(err, ErrAdminPasswordTooShort) {
		t.Fatalf("CreateFirstAdminPassword(short) = %v, %v; want false, ErrAdminPasswordTooShort", ok, err)
	}
}

func TestVerifyAdminPasswordAcceptsExistingShortPassword(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	salt := []byte("0123456789abcdef")
	if _, err := store.catalogW.ExecContext(ctx, `
		INSERT INTO admin_credential(id, password_hash, password_salt, updated_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET password_hash = excluded.password_hash, password_salt = excluded.password_salt`,
		adminCredentialID, hashAdminPassword("old", salt), salt, timeToMillis(time.Now())); err != nil {
		t.Fatalf("plant short verifier: %v", err)
	}
	if err := store.VerifyAdminPassword(ctx, "old"); err != nil {
		t.Fatalf("VerifyAdminPassword(existing short) returned error: %v", err)
	}
}
