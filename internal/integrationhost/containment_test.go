package integrationhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type containmentEventReader struct {
	events []store.OutboxEvent
}

func (r containmentEventReader) ReadEventOutboxFiltered(context.Context, int64, int, store.LibraryFilter) ([]store.OutboxEvent, error) {
	return r.events, nil
}

func (containmentEventReader) CausationToken(store.OutboxEvent) causation.Token { return "" }

type containmentIngest struct {
	calls int
}

func (r *containmentIngest) RequestIngest(context.Context, string, store.LibraryID) error {
	r.calls++
	return nil
}

type containmentMutations struct {
	calls int
}

func (r *containmentMutations) EnqueuePluginJob(context.Context, string, string, []byte, time.Time) (store.JobID, error) {
	r.calls++
	return "job-a", nil
}

func (r *containmentMutations) RequestIngest(context.Context, string, store.LibraryID) error {
	r.calls++
	return nil
}

func (r *containmentMutations) RequestFileMutation(context.Context, string, FileMutationRequest) error {
	r.calls++
	return nil
}

func (r *containmentMutations) RequestOutbound(context.Context, string, OutboundRequest) (OutboundResponse, error) {
	r.calls++
	return OutboundResponse{StatusCode: 204}, nil
}

func TestSubscribeEventsIssuesCausationToken(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	cap := causation.NewCausationCap(causation.DefaultLimits(), func() time.Time { return now })
	host := hostWith("worker-a", Deps{
		Events: containmentEventReader{events: []store.OutboxEvent{{
			Seq: 17, OccurredAt: now, Kind: "library.updated", AggregateType: "library", AggregateID: "library-a",
		}}},
		Causation: cap,
	}, Grant{PluginID: "worker-a", Kind: PermEventSubscribe, Approved: true, AllLibraries: true})

	events, err := host.SubscribeEvents(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].CausationToken == "" {
		t.Fatalf("subscribed events = %+v, want one server-issued causation token", events)
	}
	if err := cap.Admit("worker-a", events[0].CausationToken, methodRequestIngest); err != nil {
		t.Fatalf("issued event token was not admissible: %v", err)
	}
}

func TestMutationSeamRefusesMissingContainmentMarker(t *testing.T) {
	ingest := &containmentIngest{}
	host := hostWith("worker-a", Deps{Ingest: ingest},
		Grant{PluginID: "worker-a", Kind: PermRequestIngest, Approved: true, AllLibraries: true})

	if err := host.RequestIngest(context.Background(), "library-a"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("mutation without CausationCap error = %v, want %v", err, ErrPermissionDenied)
	}
	if ingest.calls != 0 {
		t.Fatal("mutation seam ran without the containment marker")
	}
}

func TestEveryMutationMethodAdmitsBeforeItsAdapter(t *testing.T) {
	mutations := &containmentMutations{}
	newHost := func(cap *causation.CausationCap) *Host {
		return hostWith("worker-a", Deps{
			Jobs: mutations, Ingest: mutations, FileMut: mutations, Outbound: mutations,
			Causation: cap, Audit: &recordingAudit{},
		},
			Grant{PluginID: "worker-a", Kind: PermPluginJob, Approved: true},
			Grant{PluginID: "worker-a", Kind: PermRequestIngest, Approved: true, AllLibraries: true},
			Grant{PluginID: "worker-a", Kind: PermRequestFileMutation, Approved: true},
			Grant{PluginID: "worker-a", Kind: PermDeclaredOutbound, Approved: true},
		)
	}
	calls := []struct {
		name string
		run  func(context.Context, *Host) error
	}{
		{name: "Jobs", run: func(ctx context.Context, host *Host) error {
			_, err := host.EnqueueJob(ctx, "kind", nil, time.Time{})
			return err
		}},
		{name: "Ingest", run: func(ctx context.Context, host *Host) error {
			return host.RequestIngest(ctx, "library-a")
		}},
		{name: "FileMut", run: func(ctx context.Context, host *Host) error {
			return host.RequestFileMutation(ctx, FileMutationRequest{Op: "move"})
		}},
		{name: "Outbound", run: func(ctx context.Context, host *Host) error {
			_, err := host.RequestOutbound(ctx, OutboundRequest{Method: "GET", URL: "https://example.test"})
			return err
		}},
	}

	withoutMarker := newHost(nil)
	for _, call := range calls {
		if err := call.run(context.Background(), withoutMarker); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("%s without marker error = %v, want %v", call.name, err, ErrPermissionDenied)
		}
	}
	if mutations.calls != 0 {
		t.Fatalf("mutation adapters ran %d times without the marker", mutations.calls)
	}

	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	cap := causation.NewCausationCap(causation.DefaultLimits(), func() time.Time { return now })
	withMarker := newHost(cap)
	for _, call := range calls {
		if err := call.run(context.Background(), withMarker); !errors.Is(err, causation.ErrInvalidToken) {
			t.Fatalf("%s without token error = %v, want %v", call.name, err, causation.ErrInvalidToken)
		}
	}
	if mutations.calls != 0 {
		t.Fatalf("mutation adapters ran %d times without a valid token", mutations.calls)
	}

	token, err := cap.TokenFor("outbox:all-seams", now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := causation.WithToken(context.Background(), token)
	for _, call := range calls {
		if err := call.run(ctx, withMarker); err != nil {
			t.Fatalf("%s with marker and valid token: %v", call.name, err)
		}
	}
	if mutations.calls != len(calls) {
		t.Fatalf("mutation adapter calls = %d, want %d", mutations.calls, len(calls))
	}
}

func TestMutationSeamRefusesMissingOrForgedCausationToken(t *testing.T) {
	cap := causation.NewCausationCap(causation.DefaultLimits(), time.Now)
	ingest := &containmentIngest{}
	host := hostWith("worker-a", Deps{Ingest: ingest, Causation: cap},
		Grant{PluginID: "worker-a", Kind: PermRequestIngest, Approved: true, AllLibraries: true})

	for name, ctx := range map[string]context.Context{
		"missing": context.Background(),
		"forged":  causation.WithToken(context.Background(), "forged"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := host.RequestIngest(ctx, "library-a"); !errors.Is(err, causation.ErrInvalidToken) {
				t.Fatalf("mutation error = %v, want %v", err, causation.ErrInvalidToken)
			}
		})
	}
	if ingest.calls != 0 {
		t.Fatal("mutation seam ran with a missing or forged causation token")
	}
}

func TestDispatchCarriesCausationTokenToMutationGuard(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	cap := causation.NewCausationCap(causation.DefaultLimits(), func() time.Time { return now })
	token, err := cap.TokenFor("outbox:dispatch", now)
	if err != nil {
		t.Fatal(err)
	}
	ingest := &containmentIngest{}
	host := hostWith("worker-a", Deps{Ingest: ingest, Causation: cap},
		Grant{PluginID: "worker-a", Kind: PermRequestIngest, Approved: true, AllLibraries: true})
	supervisor := &Supervisor{host: host}

	accepted := supervisor.dispatch(context.Background(), hostRequest{
		Method: methodRequestIngest, LibraryID: "library-a", Causation: token,
	})
	if accepted.Err != "" || ingest.calls != 1 {
		t.Fatalf("token-bearing dispatch = %+v, ingest calls = %d, want success/1", accepted, ingest.calls)
	}
	denied := supervisor.dispatch(context.Background(), hostRequest{
		Method: methodRequestIngest, LibraryID: "library-a",
	})
	if denied.Err == "" || ingest.calls != 1 {
		t.Fatalf("tokenless dispatch = %+v, ingest calls = %d, want denial/still 1", denied, ingest.calls)
	}
}

func TestHostGlobalBudgetSpansWorkersAndWorkerRestart(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	limits := causation.DefaultLimits()
	limits.GlobalActions = 3
	limits.WorkerActions = 100
	cap := causation.NewCausationCap(limits, func() time.Time { return now })
	token, err := cap.TokenFor("outbox:18", now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := causation.WithToken(context.Background(), token)
	ingest := &containmentIngest{}
	newWorkerHost := func(pluginID string) *Host {
		return hostWith(pluginID, Deps{Ingest: ingest, Causation: cap},
			Grant{PluginID: pluginID, Kind: PermRequestIngest, Approved: true, AllLibraries: true})
	}

	workerA := newWorkerHost("worker-a")
	workerB := newWorkerHost("worker-b")
	if err := workerA.RequestIngest(ctx, "library-a"); err != nil {
		t.Fatal(err)
	}
	if err := workerB.RequestIngest(ctx, "library-a"); err != nil {
		t.Fatal(err)
	}
	workerAAfterRestart := newWorkerHost("worker-a")
	if err := workerAAfterRestart.RequestIngest(ctx, "library-a"); err != nil {
		t.Fatal(err)
	}
	if err := workerB.RequestIngest(ctx, "library-a"); !errors.Is(err, causation.ErrGlobalBudget) {
		t.Fatalf("A→B→restarted-A→B error = %v, want %v", err, causation.ErrGlobalBudget)
	}
	if ingest.calls != 3 {
		t.Fatalf("ingest seam calls = %d, want exactly the three admitted actions", ingest.calls)
	}
}

type reconciliationOutbox struct {
	store     *store.Store
	now       time.Time
	lastSeq   int64
	calls     int
	propagate bool
	lineage   map[int64]causation.Token
}

func (r *reconciliationOutbox) ReadEventOutboxFiltered(ctx context.Context, afterSeq int64, limit int, filter store.LibraryFilter) ([]store.OutboxEvent, error) {
	events, err := r.store.ReadEventOutboxFiltered(ctx, afterSeq, limit, filter)
	if err == nil && len(events) != 0 {
		r.lastSeq = events[len(events)-1].Seq
	}
	return events, err
}

func (r *reconciliationOutbox) RequestIngest(ctx context.Context, _ string, _ store.LibraryID) error {
	r.calls++
	jobID := store.JobID(fmt.Sprintf("reconciliation-%d", r.calls))
	if _, err := r.store.EnqueueJob(ctx, store.Job{
		ID: jobID, Kind: "reconciliation-ingest", CreatedAt: r.now,
	}); err != nil {
		return err
	}
	events, err := r.store.ReadEventOutboxFiltered(ctx, r.lastSeq, 2, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		return err
	}
	if len(events) != 1 {
		return fmt.Errorf("reconciliation mutation emitted %d outbox events, want 1", len(events))
	}
	r.lastSeq = events[0].Seq
	if r.propagate {
		r.lineage[events[0].Seq] = causation.TokenFromContext(ctx)
	}
	return nil
}

func (r *reconciliationOutbox) CausationToken(event store.OutboxEvent) causation.Token {
	return r.lineage[event.Seq]
}

func TestReconciliationTracePropagatesCausationAcrossOutbox(t *testing.T) {
	for _, tc := range []struct {
		name      string
		propagate bool
		wantBound bool
	}{
		{name: "lineage-propagated", propagate: true, wantBound: true},
		{name: "lineage-omitted-control", propagate: false, wantBound: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
			limits := causation.DefaultLimits()
			limits.GlobalActions = 3
			limits.WorkerActions = 100
			cap := causation.NewCausationCap(limits, func() time.Time { return now })
			outbox := &reconciliationOutbox{
				store: storetest.Open(t), now: now, propagate: tc.propagate,
				lineage: make(map[int64]causation.Token),
			}
			baseline := reconciliationLastSeq(t, outbox.store)
			if _, err := outbox.store.EnqueueJob(context.Background(), store.Job{
				ID: "reconciliation-root", Kind: "reconciliation-root", CreatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}

			deps := Deps{Events: outbox, Ingest: outbox, Causation: cap, Audit: &recordingAudit{}}
			newWorker := func(pluginID string) *Supervisor {
				host := hostWith(pluginID, deps,
					Grant{PluginID: pluginID, Kind: PermEventSubscribe, Approved: true, AllLibraries: true},
					Grant{PluginID: pluginID, Kind: PermRequestIngest, Approved: true, AllLibraries: true},
				)
				return &Supervisor{host: host}
			}
			workers := []*Supervisor{newWorker("worker-a"), newWorker("worker-b")}
			event := reconciliationSubscribe(t, workers[0], baseline)
			ancestor := event.CausationToken
			if ancestor == "" {
				t.Fatal("root subscription omitted its causation token")
			}

			steps := limits.GlobalActions
			if !tc.wantBound {
				steps += 2
			}
			for i := 0; i < steps; i++ {
				worker := workers[i%len(workers)]
				response := worker.dispatch(context.Background(), hostRequest{
					Method: methodRequestIngest, LibraryID: "library-a", Causation: event.CausationToken,
				})
				if response.Err != "" {
					t.Fatalf("chain action %d unexpectedly denied: %s", i+1, response.Err)
				}
				nextWorker := workers[(i+1)%len(workers)]
				next := reconciliationSubscribe(t, nextWorker, event.Seq)
				if tc.propagate && next.CausationToken != ancestor {
					t.Fatalf("descendant E%d token = %q, want ancestor E1 token %q", i+2, next.CausationToken, ancestor)
				}
				if !tc.propagate && next.CausationToken == event.CausationToken {
					t.Fatalf("propagation-disabled descendant E%d retained parent token %q", i+2, next.CausationToken)
				}
				event = next
			}

			if !tc.wantBound {
				if outbox.calls <= limits.GlobalActions {
					t.Fatalf("propagation-disabled calls = %d, want more than global budget %d", outbox.calls, limits.GlobalActions)
				}
				return
			}
			denied := workers[steps%len(workers)].dispatch(context.Background(), hostRequest{
				Method: methodRequestIngest, LibraryID: "library-a", Causation: event.CausationToken,
			})
			if denied.Err != causation.ErrGlobalBudget.Error() {
				t.Fatalf("next chain action error = %q, want %q", denied.Err, causation.ErrGlobalBudget)
			}
			if outbox.calls != limits.GlobalActions {
				t.Fatalf("ingest seam calls = %d, want exactly %d admitted actions", outbox.calls, limits.GlobalActions)
			}
		})
	}
}

func reconciliationLastSeq(t *testing.T, st *store.Store) int64 {
	t.Helper()
	events, err := st.ReadEventOutboxFiltered(context.Background(), 0, 1000, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

func reconciliationSubscribe(t *testing.T, worker *Supervisor, afterSeq int64) SubscribedEvent {
	t.Helper()
	response := worker.dispatch(context.Background(), hostRequest{
		Method: methodSubscribeEvents, AfterSeq: afterSeq, Limit: 2,
	})
	if response.Err != "" {
		t.Fatalf("subscribe after %d: %s", afterSeq, response.Err)
	}
	var events []SubscribedEvent
	if err := json.Unmarshal(response.Data, &events); err != nil {
		t.Fatalf("decode subscription after %d: %v", afterSeq, err)
	}
	if len(events) != 1 {
		t.Fatalf("subscription after %d returned %d events, want 1", afterSeq, len(events))
	}
	return events[0]
}
