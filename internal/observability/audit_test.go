package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestAuditLoggerRecordsActorOperationAndOutcome(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	audit := NewAuditLogger(logger)

	if err := audit.RecordAudit(t.Context(), AuditRecord{
		Actor: AuditActor{
			Authenticated: true,
			SessionID:     "session-manager",
			DeviceID:      "device-mac",
			ProfileID:     "profile-default",
			APITokenID:    "admin-token",
		},
		Operation: "graphql:libraries",
		Outcome:   AuditRefused,
	}); err != nil {
		t.Fatal(err)
	}

	var entry struct {
		Message   string `json:"msg"`
		Operation string `json:"operation"`
		Outcome   string `json:"outcome"`
		Actor     struct {
			Authenticated bool   `json:"authenticated"`
			SessionID     string `json:"session_id"`
			DeviceID      string `json:"device_id"`
			ProfileID     string `json:"profile_id"`
			APITokenID    string `json:"api_token_id"`
			APIKeyID      string `json:"api_key_id"`
		} `json:"actor"`
	}
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Message != "privileged operation" || entry.Operation != "graphql:libraries" || entry.Outcome != string(AuditRefused) {
		t.Fatalf("audit entry = %+v", entry)
	}
	if !entry.Actor.Authenticated ||
		entry.Actor.SessionID != "session-manager" ||
		entry.Actor.DeviceID != "device-mac" ||
		entry.Actor.ProfileID != "profile-default" ||
		entry.Actor.APITokenID != "admin-token" ||
		entry.Actor.APIKeyID != "" {
		t.Fatalf("audit actor = %+v", entry.Actor)
	}
}

func TestAuditLoggerBypassesOperatorLevelFilter(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelError}))
	err := NewAuditLogger(logger).RecordAudit(t.Context(), AuditRecord{
		Operation: "graphql:libraries",
		Outcome:   AuditSuccess,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"operation":"graphql:libraries"`)) {
		t.Fatalf("audit record was filtered by operator log level: %s", output.Bytes())
	}
}
