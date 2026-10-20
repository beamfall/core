package webhook

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
