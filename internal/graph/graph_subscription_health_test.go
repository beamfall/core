package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestSubscriptionHealthAndRefreshOverGraph pins CORE-2955: the Graph carries
// feed health, a follower may refresh a Subscription, a non-follower viewer
// sees it as unknown, and a second refresh inside five minutes is refused.
func TestSubscriptionHealthAndRefreshOverGraph(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/health?token=feed-secret",
		Root:      "/media/health",
		Title:     "Health Feed",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	failingSince := now.Add(-8 * 24 * time.Hour)
	if err := st.RecordSubscriptionPollFailure(ctx, sub.ID, store.FeedErrorHTTP5xx, failingSince, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	var listed []SubscriptionInfo
	execGraph(t, handler, authorizeManager,
		`query { subscriptions(libraryID: "library-local") { id healthStatus lastErrorClass failingSince nextPollAt } }`,
		nil, "subscriptions", &listed)
	if len(listed) != 1 || listed[0].HealthStatus != "warning" || listed[0].LastErrorClass != "http-5xx" ||
		listed[0].FailingSince == "" || listed[0].NextPollAt == "" {
		t.Fatalf("subscriptions = %+v, want a warning http-5xx feed with failingSince and nextPollAt", listed)
	}

	refresh := `mutation Refresh($id: String!) { refreshSubscription(id: $id) { id title healthStatus lastErrorClass nextPollAt } }`
	resp := postGraphJSON(t, handler, authorizeViewer, refresh, map[string]any{"id": string(sub.ID)})
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "unknown subscription") {
		t.Fatalf("non-follower refresh errors = %+v, want unknown subscription", resp.Errors)
	}

	var followed FollowInfo
	execGraph(t, handler, authorizeViewer,
		`mutation Follow($id: String!) { setFollow(subscriptionID: $id, on: true) { id followed } }`,
		map[string]any{"id": string(sub.ID)}, "setFollow", &followed)
	var refreshed SubscriptionRefreshInfo
	execGraph(t, handler, authorizeViewer, refresh, map[string]any{"id": string(sub.ID)}, "refreshSubscription", &refreshed)
	if refreshed.ID != string(sub.ID) || refreshed.HealthStatus != "warning" || refreshed.LastErrorClass != "http-5xx" {
		t.Fatalf("refresh echo = %+v", refreshed)
	}
	stored, err := st.Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Health.Due(time.Now().UTC()) {
		t.Fatalf("refreshed subscription not due: %+v", stored.Health)
	}
	jobs, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if !hasJobKind(jobs, "subscription-poll") {
		t.Fatalf("queued jobs = %+v, want the subscription poll", jobs)
	}

	resp = postGraphJSON(t, handler, authorizeViewer, refresh, map[string]any{"id": string(sub.ID)})
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0].Message, "refreshed recently") {
		t.Fatalf("second refresh errors = %+v, want the rate limit", resp.Errors)
	}

	resp = postGraphJSON(t, handler, authorizeViewer, `mutation { refreshAllSubscriptions { refreshed } }`, nil)
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "manager role required" {
		t.Fatalf("viewer refresh-all errors = %+v", resp.Errors)
	}
	var all SubscriptionRefreshAllInfo
	execGraph(t, handler, authorizeManager, `mutation { refreshAllSubscriptions { refreshed } }`, nil, "refreshAllSubscriptions", &all)
	if all.Refreshed != 0 {
		t.Fatalf("refresh all inside the gap refreshed %d, want 0", all.Refreshed)
	}
}

func hasJobKind(jobs []store.Job, kind store.JobKind) bool {
	for _, job := range jobs {
		if job.Kind == kind {
			return true
		}
	}
	return false
}
