package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/pairing"
)

// Operations whose refusals the server itself notes; credential resolution
// and sign-in refusals are noted by internal/auth.
const (
	refusalOperationEnroll  = "enroll"
	refusalOperationSetup   = "setup"
	refusalOperationPromote = "promote"
	// Sign-in recovery (CORE-1106).
	refusalOperationSessionRenew = "session_renew"
	refusalOperationAdminSignIn  = "admin_sign_in"
)

// authRefusalMetrics counts refused requests by reason. Only reasons in the
// closed auth.RefusalReasons vocabulary are counted, so
// beamfall_auth_refusals_total carries a fixed label set.
type authRefusalMetrics struct {
	byReason sync.Map // auth.RefusalReason -> *atomic.Uint64
}

func (m *authRefusalMetrics) observe(reason auth.RefusalReason) bool {
	if !slices.Contains(auth.RefusalReasons, reason) {
		return false
	}
	counter, _ := m.byReason.LoadOrStore(reason, new(atomic.Uint64))
	counter.(*atomic.Uint64).Add(1)
	return true
}

func (m *authRefusalMetrics) writePrometheus(body *strings.Builder) {
	for _, reason := range auth.RefusalReasons {
		var count uint64
		if counter, ok := m.byReason.Load(reason); ok {
			count = counter.(*atomic.Uint64).Load()
		}
		_, _ = fmt.Fprintf(body, "beamfall_auth_refusals_total{reason=%q} %d\n", string(reason), count)
	}
}

// recordAuthRefusal emits the `auth refused` audit record for a request whose
// credential was refused. A scope-resolution refusal is recorded only when the
// response refused the caller (401 or 403); an explicit sign-in refusal is
// always recorded. The record bypasses the log-level filter like the
// privileged-operation audit, and carries only closed-vocabulary fields and the
// request ID — never a session ID, secret, token, key, password or code.
func (s *Server) recordAuthRefusal(r *http.Request, requestID string, status int) {
	refusal, ok := auth.RefusalFromContext(r.Context())
	if !ok {
		return
	}
	if !refusal.Decisive && status != http.StatusUnauthorized && status != http.StatusForbidden {
		return
	}
	if !s.authRefusals.observe(refusal.Reason) {
		return
	}
	route := r.Pattern
	if route == "" {
		route = "unmatched"
	}
	record := slog.NewRecord(time.Now(), slog.LevelWarn, "auth refused", 0)
	record.AddAttrs(
		slog.String("operation", refusal.Operation),
		slog.String("outcome", "refused"),
		slog.String("reason", string(refusal.Reason)),
		slog.String("credential", principalClass(r)),
		slog.String("method", r.Method),
		slog.String("route", route),
		slog.Int("status", status),
		slog.String("requestID", requestID),
	)
	_ = slog.Default().Handler().Handle(r.Context(), record)
}

// notePairingRefusal notes a refused pairing credential (a pairing code or
// setup token, or a sign-in recovery credential) for the request's audit
// record.
func notePairingRefusal(r *http.Request, operation string, err error) {
	var refusal *pairing.Refusal
	switch {
	case errors.As(err, &refusal):
		auth.NoteRefusal(r.Context(), operation, refusal.Reason, true)
	case errors.Is(err, pairing.ErrThrottled):
		auth.NoteRefusal(r.Context(), operation, auth.RefusalPairingThrottled, true)
	case errors.Is(err, pairing.ErrInvalidCode):
		auth.NoteRefusal(r.Context(), operation, auth.RefusalPairingCodeInvalid, true)
	case errors.Is(err, pairing.ErrSetupTokenInvalid):
		auth.NoteRefusal(r.Context(), operation, auth.RefusalSetupTokenInvalid, true)
	}
}
