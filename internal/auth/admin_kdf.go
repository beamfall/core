package auth

import (
	"context"
	"errors"
	"time"

	"github.com/beamfall/core/internal/store"
)

// adminKDFSlotCount bounds the admin-password KDF verifications that run at
// once, process-wide. Each verification allocates the Argon2id memory cost
// (64 MiB), so the bound caps that memory however many requests arrive.
const adminKDFSlotCount = 2

var (
	adminKDFSlots = make(chan struct{}, adminKDFSlotCount)
	// adminKDFSlotWait is how long a request waits for a free slot before it
	// is refused. A variable so tests can change it.
	adminKDFSlotWait = 2 * time.Second
	// verifyAdminPasswordKDF runs the KDF. A variable so tests can observe how
	// many verifications run at once.
	verifyAdminPasswordKDF = func(ctx context.Context, st *store.Store, password string) error {
		return st.VerifyAdminPassword(ctx, password)
	}
)

// ErrAdminBusy means no admin-password verification slot became free in
// time. Callers refuse it exactly as they refuse a wrong password.
var ErrAdminBusy = errors.New("admin password verification is busy")

// AdminPasswordAttempt names the throttle lanes one admin-password check
// counts against: every key in Keys under Action, on the admin re-auth curve.
type AdminPasswordAttempt struct {
	Action string
	Keys   []string
	At     time.Time
}

// CheckAdminPassword verifies password against the server admin password
// inside the process-wide KDF bound. Inside the bound it first reserves the
// attempt, re-checking every lane and recording a failure on each in one
// store transaction, so parallel requests cannot all pass the throttle before
// any failure lands. It returns ErrAdminBusy when no slot frees in time and
// ErrAdminLocked when a lane is locked; neither runs the KDF or counts.
// Otherwise it returns the KDF's result, with the failure already counted: a
// caller whose whole operation then succeeds records a success on the same
// lanes, which resets them.
func CheckAdminPassword(ctx context.Context, st *store.Store, attempt AdminPasswordAttempt, password string) error {
	return withAdminKDFSlot(ctx, func() error {
		locked, err := st.ReserveAuthAttempt(ctx, attempt.Action, attempt.Keys, attempt.At.UTC(), adminBackoff)
		if err != nil {
			return err
		}
		if !locked.IsZero() {
			return ErrAdminLocked
		}
		return verifyAdminPasswordKDF(ctx, st, password)
	})
}

// withAdminKDFSlot runs fn holding one admin KDF slot, or returns
// ErrAdminBusy without running it when no slot frees within adminKDFSlotWait.
func withAdminKDFSlot(ctx context.Context, fn func() error) error {
	timer := time.NewTimer(adminKDFSlotWait)
	defer timer.Stop()
	select {
	case adminKDFSlots <- struct{}{}:
	case <-timer.C:
		return ErrAdminBusy
	case <-ctx.Done():
		return ErrAdminBusy
	}
	defer func() { <-adminKDFSlots }()
	return fn()
}
