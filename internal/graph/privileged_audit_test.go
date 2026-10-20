package graph

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/observability"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type recordingAudit struct {
	records     []observability.AuditRecord
	err         error
	failOutcome observability.AuditOutcome
}

func (r *recordingAudit) RecordAudit(_ context.Context, record observability.AuditRecord) error {
	if r.err != nil && (r.failOutcome == "" || r.failOutcome == record.Outcome) {
		return r.err
	}
	r.records = append(r.records, record)
	return nil
}

func TestPrivilegedGraphQLOperationAuditsSuccess(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	h := newTestHandlerWithStore(st)
	audit := &recordingAudit{}
	h.SetAuditRecorder(audit)

	_, errs := post(t, h, `{"query":"query { libraries { id } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("libraries errors = %#v", errs)
	}
	assertPrivilegedAudit(t, audit.records, observability.AuditSuccess)
}

func TestPrivilegedGraphQLAuditIgnoresOperatorLogLevel(t *testing.T) {
	previous := slog.Default()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(
		&output,
		&slog.HandlerOptions{Level: slog.LevelError},
	)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	h := newTestHandlerWithStore(st)

	data, errs := post(t, h, `{"query":"query { libraries { id } }"}`, authorizeManager)
	if len(errs) != 0 || len(data) == 0 {
		t.Fatalf("libraries data=%v errors=%#v under error log level", data, errs)
	}
	data, errs = post(t, h, `{"query":"mutation($name:String!,$kind:String!){ createProfile(name:$name,kind:$kind){ id } }","variables":{"name":"Quiet Audit","kind":"managed"}}`, authorizeManager)
	if len(errs) != 0 || len(data) == 0 {
		t.Fatalf("createProfile data=%v errors=%#v under error log level", data, errs)
	}
	assertProfilePresent(t, st, "Quiet Audit")
	if count := strings.Count(output.String(), `"msg":"privileged operation"`); count < 3 {
		t.Fatalf("audit entries = %d, want read plus mutation attempt/outcome; output=%s", count, output.String())
	}
}

func TestPrivilegedGraphQLOperationAuditsRefusal(t *testing.T) {
	h := newTestHandler(t)
	audit := &recordingAudit{}
	h.SetAuditRecorder(audit)

	_, errs := post(t, h, `{"query":"query { libraries { id } }"}`, authorizeManager)
	if len(errs) != 1 || errs[0].Message != "admin session required" {
		t.Fatalf("libraries errors = %#v, want admin refusal", errs)
	}
	assertPrivilegedAudit(t, audit.records, observability.AuditRefused)
}

func TestPrivilegedGraphQLBatchAuditsEachOutcome(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	h := newTestHandlerWithStore(st)
	audit := &recordingAudit{}
	h.SetAuditRecorder(audit)

	_, errs := post(t, h, `{"query":"query { first: libraries { id } second: job { id } }"}`, authorizeManager)
	if len(errs) != 1 {
		t.Fatalf("batch errors = %#v, want missing-job error", errs)
	}
	if len(audit.records) != 2 {
		t.Fatalf("audit records = %+v, want two", audit.records)
	}
	if audit.records[0].Operation != "graphql:libraries" || audit.records[0].Outcome != observability.AuditSuccess {
		t.Fatalf("first alias audit = %+v, want canonical libraries success", audit.records[0])
	}
	if audit.records[1].Operation != "graphql:job" || audit.records[1].Outcome != observability.AuditError {
		t.Fatalf("second alias audit = %+v, want canonical job error", audit.records[1])
	}
}

func TestPrivilegedGraphQLReadFailsClosedWhenAuditUnavailable(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	h := newTestHandlerWithStore(st)
	audit := &recordingAudit{err: errors.New("injected audit failure")}
	h.SetAuditRecorder(audit)

	data, errs := post(t, h, `{"query":"query { libraries { id } }"}`, authorizeManager)
	if len(data) != 0 {
		t.Fatalf("libraries data = %v, want none", data)
	}
	if len(errs) != 1 || errs[0].Message != "privileged audit unavailable" {
		t.Fatalf("libraries errors = %#v, want audit-unavailable refusal", errs)
	}
}

func TestPrivilegedGraphQLMutationDoesNotRunWhenAttemptAuditUnavailable(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	h := newTestHandlerWithStore(st)
	h.SetAuditRecorder(&recordingAudit{err: errors.New("injected audit failure")})

	data, errs := post(t, h, `{"query":"mutation($name:String!,$kind:String!){ createProfile(name:$name,kind:$kind){ id } }","variables":{"name":"No Audit","kind":"managed"}}`, authorizeManager)
	if len(data) != 0 {
		t.Fatalf("createProfile data = %v, want none", data)
	}
	if len(errs) != 1 || errs[0].Message != "privileged audit unavailable" {
		t.Fatalf("createProfile errors = %#v, want audit-unavailable refusal", errs)
	}
	assertProfileAbsent(t, st, "No Audit")
}

func TestPrivilegedGraphQLMutationFinalAuditFailurePreservesCommittedResponse(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	h := newTestHandlerWithStore(st)
	audit := &recordingAudit{
		err:         errors.New("injected final audit failure"),
		failOutcome: observability.AuditSuccess,
	}
	h.SetAuditRecorder(audit)

	data, errs := post(t, h, `{"query":"mutation($name:String!,$kind:String!){ createProfile(name:$name,kind:$kind){ id } }","variables":{"name":"Uncertain Audit","kind":"managed"}}`, authorizeManager)
	if len(errs) != 0 || len(data) == 0 {
		t.Fatalf("createProfile data=%v errors=%#v, want truthful committed response", data, errs)
	}
	assertProfilePresent(t, st, "Uncertain Audit")
	if len(audit.records) != 1 || audit.records[0].Operation != "graphql:createprofile" || audit.records[0].Outcome != observability.AuditAttempt {
		t.Fatalf("audit records = %+v, want accepted per-operation attempt", audit.records)
	}
	if audit.records[0].Actor.SessionID != "session-manager" || audit.records[0].Actor.DeviceID != "device-mac" || audit.records[0].Actor.ProfileID != "profile-default" {
		t.Fatalf("attempt actor = %+v, want manager session attribution", audit.records[0].Actor)
	}
	entries, err := st.ActivityEntries(t.Context(), store.ActivityQuery{
		Filter:                   store.LibraryFilter{Revealed: true, AllLibraries: true},
		ProfileID:                "profile-default",
		Limit:                    10,
		IncludeMutationLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var attempt, finished store.ActivityEntry
	for _, entry := range entries {
		switch entry.Action {
		case store.ActivityMutationAttempt:
			attempt = entry
		case store.ActivityMutationFinished:
			finished = entry
		}
	}
	if attempt.TargetID == "" || finished.TargetID != attempt.TargetID ||
		attempt.OperationID != attempt.TargetID+":attempt" ||
		finished.OperationID != attempt.TargetID+":finished" ||
		finished.TargetType != "graphql:success" {
		t.Fatalf("durable lifecycle attempt=%+v finished=%+v", attempt, finished)
	}
}

func TestPrivilegedGraphQLMutationBatchAuditsResolvedOutcomes(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	h := newTestHandlerWithStore(st)
	audit := &recordingAudit{}
	h.SetAuditRecorder(audit)

	_, errs := post(t, h, `{"query":"mutation($name:String!,$kind:String!,$libraryID:String!){ createProfile(name:$name,kind:$kind){ id } deleteLibrary(libraryID:$libraryID) }","variables":{"name":"Audit Batch","kind":"managed","libraryID":"missing"}}`, authorizeManager)
	if len(errs) != 1 || len(errs[0].Path) != 0 {
		t.Fatalf("batch errors = %#v, want one pathless mutation error", errs)
	}
	assertProfilePresent(t, st, "Audit Batch")

	attempted := map[string]bool{}
	final := map[string]observability.AuditOutcome{}
	for _, record := range audit.records {
		if record.Outcome == observability.AuditAttempt {
			attempted[record.Operation] = true
			continue
		}
		final[record.Operation] = record.Outcome
	}
	if len(audit.records) != 4 || !attempted["graphql:createprofile"] || !attempted["graphql:deletelibrary"] {
		t.Fatalf("audit records = %+v, want attempt and final outcome per operation", audit.records)
	}
	if final["graphql:createprofile"] != observability.AuditSuccess {
		t.Fatalf("createProfile outcome = %q, want success; records=%+v", final["graphql:createprofile"], audit.records)
	}
	if final["graphql:deletelibrary"] != observability.AuditError {
		t.Fatalf("deleteLibrary outcome = %q, want error; records=%+v", final["graphql:deletelibrary"], audit.records)
	}
}

func TestPrivilegedGraphQLAuditMapsBearerCredentialActors(t *testing.T) {
	st := storetest.Open(t)
	tokenInfo, token, err := st.CreateAPIToken(t.Context(), "audit-token", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	keyInfo, key, err := st.CreateScopedAPIKey(t.Context(), "audit-key", []store.APIScope{store.ScopeReadCatalog}, "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("admin API token", func(t *testing.T) {
		h := newTestHandlerWithStore(st)
		audit := &recordingAudit{}
		h.SetAuditRecorder(audit)
		authorize := func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		_, errs := post(t, h, `{"query":"query { libraries { id } }"}`, authorize)
		if len(errs) != 1 || errs[0].Message != "admin session required" {
			t.Fatalf("libraries errors = %#v, want admin refusal", errs)
		}
		wantActor := observability.AuditActor{Authenticated: true, APITokenID: string(tokenInfo.ID)}
		if len(audit.records) != 1 || audit.records[0].Actor != wantActor {
			t.Fatalf("audit records = %+v, want actor %+v", audit.records, wantActor)
		}
	})

	// A scoped key is refused at the GraphQL surface itself
	//, before the admin-tier gate can
	// answer — and the privileged refusal audit still attributes the key.
	t.Run("scoped API key", func(t *testing.T) {
		h := newTestHandlerWithStore(st)
		audit := &recordingAudit{}
		h.SetAuditRecorder(audit)

		rec := postScopedKeyGraph(t, h, `{"query":"query { libraries { id } }"}`, key)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), http.StatusForbidden)
		}
		wantActor := observability.AuditActor{Authenticated: true, APIKeyID: string(keyInfo.ID)}
		if len(audit.records) != 1 || audit.records[0].Actor != wantActor {
			t.Fatalf("audit records = %+v, want actor %+v", audit.records, wantActor)
		}
		if audit.records[0].Operation != "graphql:libraries" || audit.records[0].Outcome != observability.AuditRefused {
			t.Fatalf("audit record = %+v, want graphql:libraries refusal", audit.records[0])
		}
	})
}

func assertProfileAbsent(t *testing.T, st *store.Store, name string) {
	t.Helper()
	profiles, err := st.Profiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		if profile.Name == name {
			t.Fatalf("profile %q was committed", name)
		}
	}
}

func assertProfilePresent(t *testing.T, st *store.Store, name string) {
	t.Helper()
	profiles, err := st.Profiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		if profile.Name == name {
			return
		}
	}
	t.Fatalf("profile %q was not committed", name)
}

func assertPrivilegedAudit(t *testing.T, records []observability.AuditRecord, outcome observability.AuditOutcome) {
	t.Helper()
	if len(records) != 1 {
		t.Fatalf("audit records = %+v, want one", records)
	}
	record := records[0]
	if record.Operation != "graphql:libraries" || record.Outcome != outcome {
		t.Fatalf("audit record = %+v, want libraries/%s", record, outcome)
	}
	wantActor := observability.AuditActor{
		Authenticated: true,
		SessionID:     "session-manager",
		DeviceID:      "device-mac",
		ProfileID:     "profile-default",
	}
	if record.Actor != wantActor {
		t.Fatalf("audit actor = %+v, want %+v", record.Actor, wantActor)
	}
}

var _ observability.AuditRecorder = (*recordingAudit)(nil)
