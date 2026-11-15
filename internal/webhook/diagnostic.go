package webhook

import (
	"context"
	"errors"
)

// DiagnosticError is a fixed, non-sensitive failure class safe to cross boot,
// log, retry, and dead-letter boundaries. It deliberately does not unwrap the
// raw error, which may contain a URL, secret ref, filesystem path, or payload.
type DiagnosticError string

func (e DiagnosticError) Error() string { return string(e) }

const (
	ErrConfigDiagnostic     DiagnosticError = "webhook configuration is invalid"
	ErrSecretDiagnostic     DiagnosticError = "webhook secret resolution failed"
	ErrOutboxDiagnostic     DiagnosticError = "webhook outbox read failed"
	ErrRequestDiagnostic    DiagnosticError = "webhook request construction failed"
	ErrTransportDiagnostic  DiagnosticError = "webhook transport failed"
	ErrStatusDiagnostic     DiagnosticError = "webhook endpoint rejected delivery"
	ErrCursorDiagnostic     DiagnosticError = "webhook cursor persistence failed"
	ErrDeadLetterDiagnostic DiagnosticError = "webhook dead-letter persistence failed"
)

// ErrUnknownEndpoint is returned for an endpoint id that is not configured.
const ErrUnknownEndpoint DiagnosticError = "webhook endpoint not found"

// diagnosticText is the fixed text of a delivery failure for the console: a
// DiagnosticError's own text, or a generic class for anything else, so no
// URL, secret or response body ever reaches it.
func diagnosticText(err error) string {
	if err == nil {
		return ""
	}
	var diag DiagnosticError
	if errors.As(err, &diag) {
		return diag.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "webhook delivery timed out"
	}
	return ErrTransportDiagnostic.Error()
}
