package offlinepack

import (
	"context"
	"log/slog"
	"time"
)

// The key route's mandatory audit record is declared here as a consumer-side
// seam rather than taken from internal/observability: ARCHITECTURE.md §4 limits
// offlinepack to store, auth, and streamtoken. The composition root may inject
// the server's central audit recorder through SetAuditRecorder with a two-line
// adapter; until it does, the package-local default below emits the same
// record, in the same shape, to the same process logger.

type AuditOutcome string

const (
	AuditSuccess AuditOutcome = "success"
	AuditRefused AuditOutcome = "refused"
	AuditError   AuditOutcome = "error"
)

type AuditActor struct {
	Authenticated bool
	SessionID     string
	DeviceID      string
	ProfileID     string
	APITokenID    string
	APIKeyID      string
}

type AuditRecord struct {
	Actor     AuditActor
	Operation string
	Outcome   AuditOutcome
}

// AuditRecorder is the narrow method set the key route needs: record, or fail.
// A non-nil error is a fail-closed serving failure, never a warning.
type AuditRecorder interface {
	RecordAudit(context.Context, AuditRecord) error
}

// slogAuditRecorder writes audit records straight to a slog.Handler. Acceptance
// is deliberately independent of the operator logger's verbosity threshold —
// audit records bypass that presentation filter and surface real sink write
// failures to the caller.
type slogAuditRecorder struct {
	handler slog.Handler
}

func newSlogAuditRecorder(logger *slog.Logger) slogAuditRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return slogAuditRecorder{handler: logger.Handler()}
}

func (r slogAuditRecorder) RecordAudit(ctx context.Context, record AuditRecord) error {
	entry := slog.NewRecord(time.Now(), slog.LevelInfo, "privileged operation", 0)
	entry.AddAttrs(
		slog.String("operation", record.Operation),
		slog.String("outcome", string(record.Outcome)),
		slog.Group("actor",
			slog.Bool("authenticated", record.Actor.Authenticated),
			slog.String("session_id", record.Actor.SessionID),
			slog.String("device_id", record.Actor.DeviceID),
			slog.String("profile_id", record.Actor.ProfileID),
			slog.String("api_token_id", record.Actor.APITokenID),
			slog.String("api_key_id", record.Actor.APIKeyID),
		),
	)
	return r.handler.Handle(ctx, entry)
}
