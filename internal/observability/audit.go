package observability

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var ErrAuditUnavailable = errors.New("audit logger unavailable")

type AuditOutcome string

const (
	AuditAttempt AuditOutcome = "attempt"
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

type AuditRecorder interface {
	RecordAudit(context.Context, AuditRecord) error
}

type AuditLogger struct {
	handler slog.Handler
}

// auditSink preserves the operator logger's destination and formatting while
// making audit acceptance independent of its verbosity threshold. slog.Logger
// normally applies Enabled before Handle; audit records deliberately bypass
// that presentation filter and still surface real sink write failures.
type auditSink struct {
	handler slog.Handler
}

func (s auditSink) Enabled(context.Context, slog.Level) bool {
	return true
}

func (s auditSink) Handle(ctx context.Context, record slog.Record) error {
	return s.handler.Handle(ctx, record)
}

func (s auditSink) WithAttrs(attrs []slog.Attr) slog.Handler {
	return auditSink{handler: s.handler.WithAttrs(attrs)}
}

func (s auditSink) WithGroup(name string) slog.Handler {
	return auditSink{handler: s.handler.WithGroup(name)}
}

func NewAuditLogger(logger *slog.Logger) *AuditLogger {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuditLogger{handler: auditSink{handler: logger.Handler()}}
}

func (l *AuditLogger) RecordAudit(ctx context.Context, record AuditRecord) error {
	if !l.handler.Enabled(ctx, slog.LevelInfo) {
		return ErrAuditUnavailable
	}
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
	return l.handler.Handle(ctx, entry)
}
