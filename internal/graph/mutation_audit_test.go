package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestMutationRequestHasDurableAuditEnvelope(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	data, errs := post(t, h,
		`{"query":"mutation F($mediaID: String!, $favorite: Boolean!){ setFavorite(mediaID:$mediaID, favorite:$favorite){ id favorited } }","variables":{"mediaID":"media-alpha","favorite":true}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("setFavorite errors = %#v, data=%s", errs, data["setFavorite"])
	}
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{
		Filter:                   store.LibraryFilter{Revealed: true, AllLibraries: true},
		ProfileID:                "profile-default",
		Limit:                    10,
		IncludeMutationLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("activity entries = %+v, want completed request lifecycle", entries)
	}
	finished, attempt := entries[0], entries[1]
	if attempt.Action != store.ActivityMutationAttempt || attempt.OperationID == "" || attempt.TargetType != "graphql:setfavorite" {
		t.Fatalf("attempt = %+v", attempt)
	}
	if finished.Action != store.ActivityMutationFinished || finished.OperationID == "" || finished.TargetID != attempt.TargetID || finished.TargetType != "graphql:success" {
		t.Fatalf("finished = %+v, attempt = %+v", finished, attempt)
	}
}

func TestMutationFinishedAuditFailureLeavesTruthfulResponseAndDurableAttempt(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	h.mutationAuditAppend = func(ctx context.Context, entry store.ActivityEntry) error {
		if entry.Action == store.ActivityMutationFinished {
			return errors.New("injected finished-audit failure")
		}
		return st.AppendActivity(ctx, entry)
	}
	data, errs := post(t, h,
		`{"query":"mutation F($mediaID: String!, $favorite: Boolean!){ setFavorite(mediaID:$mediaID, favorite:$favorite){ id favorited } }","variables":{"mediaID":"media-alpha","favorite":true}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("setFavorite errors = %#v, data=%s", errs, data["setFavorite"])
	}
	favorited, err := st.MediaFavorited(context.Background(), "profile-default", "media-alpha")
	if err != nil || !favorited {
		t.Fatalf("favorite committed = %v, err=%v", favorited, err)
	}
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{
		Filter:                   store.LibraryFilter{Revealed: true, AllLibraries: true},
		ProfileID:                "profile-default",
		Limit:                    10,
		IncludeMutationLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != store.ActivityMutationAttempt || entries[0].OperationID == "" {
		t.Fatalf("activity entries = %+v, want durable uncertain-operation attempt", entries)
	}
}

func TestMutationRequestAuditClassifiesResolverFailure(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	_, errs := post(t, h,
		`{"query":"mutation { setFavorite(favorite:true){ id } }"}`,
		authorizeViewer)
	if len(errs) == 0 {
		t.Fatal("setFavorite without mediaID succeeded")
	}
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{
		Filter:                   store.LibraryFilter{Revealed: true, AllLibraries: true},
		ProfileID:                "profile-default",
		Limit:                    10,
		IncludeMutationLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("activity entries = %+v, want failed request lifecycle", entries)
	}
	finished, attempt := entries[0], entries[1]
	if attempt.Action != store.ActivityMutationAttempt || finished.Action != store.ActivityMutationFinished || finished.TargetID != attempt.TargetID || finished.TargetType != "graphql:error" {
		t.Fatalf("finished = %+v, attempt = %+v", finished, attempt)
	}
}

func TestMutationRequestAuditClassifiesAdminRefusal(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	_, errs := post(t, h,
		`{"query":"mutation { createProfile }"}`,
		authorizeManager)
	if len(errs) != 1 || errs[0].Message != "admin session required" {
		t.Fatalf("createProfile errors = %#v, want admin refusal", errs)
	}
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{
		Filter:                   store.LibraryFilter{Revealed: true, AllLibraries: true},
		ProfileID:                "profile-default",
		Limit:                    10,
		IncludeMutationLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("activity entries = %+v, want refused request lifecycle", entries)
	}
	finished, attempt := entries[0], entries[1]
	if attempt.TargetType != "graphql:createprofile" ||
		finished.TargetID != attempt.TargetID ||
		finished.TargetType != "graphql:refused" ||
		finished.SessionID != "session-manager" ||
		finished.DeviceID != "device-mac" ||
		finished.ProfileID != "profile-default" {
		t.Fatalf("finished = %+v, attempt = %+v", finished, attempt)
	}
}
