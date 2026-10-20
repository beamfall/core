package mediahttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/streamtoken"
)

type mutableCompatResolver struct {
	decisionID string
}

func (r *mutableCompatResolver) Resolve(_ context.Context, _ string, _ streamtoken.DecisionWant) (string, bool, error) {
	return r.decisionID, r.decisionID != "", nil
}

func TestDirectRejectsAlreadyIssuedTokenAfterCompatInvalidation(t *testing.T) {
	handler, _ := newTestHandlerWithBytes(t, "session-default", "media-alpha", []byte("beamfall file bytes"))
	resolver := &mutableCompatResolver{decisionID: "decision-1"}
	handler.SetCompatDecisionResolver(resolver)
	scope, err := handler.auth.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	token, err := handler.tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: time.Unix(100, 0).UTC().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	valid := httptest.NewRecorder()
	handler.Direct(valid, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))
	if valid.Code != http.StatusOK {
		t.Fatalf("unchanged decision status = %d, want 200: %s", valid.Code, valid.Body.String())
	}

	resolver.decisionID = ""
	invalidated := httptest.NewRecorder()
	handler.Direct(invalidated, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))
	if invalidated.Code != http.StatusUnauthorized {
		t.Fatalf("invalidated decision status = %d, want 401", invalidated.Code)
	}
}
