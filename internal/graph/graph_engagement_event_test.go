package graph

import (
	"net/http"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Per-Profile engagement counter, graph acceptance. These
// wire tests boot the real seeded store + HTTP handler with an activated plugin
// that declares a synthetic engagement-event kind, and exercise increment /
// remove-last correction / count / history / entity aggregate / Profile-scoping
// + the My-Data export-and-clear integration over GraphQL.
//
// The kind here is a synthetic plugin-declared term ("plugin-counter"), never a
// domain word — Core validates a recorded kind against the activated plugins'
// declared kinds (HasEngagementEventKind), so a test plugin declaring the kind
// is exactly how a real plugin's counter rides the generic surface.
const wireEventKind = "plugin-counter"

// newCounterHandler builds a handler over the seeded store and a one-bundle
// registry whose plugin declares the synthetic engagement-event kind.
func newCounterHandler(t *testing.T) *Handler {
	t.Helper()
	st := storetest.Open(t)
	for _, entity := range []store.Entity{
		{ID: "entity-counter-local", LibraryID: "library-local", Kind: "category", Title: "Local"},
		{ID: "entity-counter-hidden", LibraryID: "library-archive", Kind: "category", Title: "Hidden"},
	} {
		if err := st.UpsertEntity(t.Context(), entity); err != nil {
			t.Fatalf("UpsertEntity(%s): %v", entity.ID, err)
		}
	}
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:           "counter-test",
		Version:      "0.0.1",
		DisplayName:  plugin.LocaleString{"en": "Counter Test"},
		MediaLabel:   plugin.LocaleString{"en": "Item"},
		MediaClasses: []string{"video"},
		Engagement:   plugin.Engagement{EventKinds: []string{wireEventKind}},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "all", Kind: "grid", Title: plugin.LocaleString{"en": "All"},
				ArtworkSlot: "thumb", Source: plugin.SourceSpec{Kind: "all-items"}},
		}}},
	}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return newTestHandlerWithPlugins(st, registry)
}

// recordWire appends one increment for the authorizing Profile on a Media target
// and returns the echoed counter.
func recordWire(t *testing.T, h *Handler, authorize func(*http.Request), mediaID string) EngagementCounterInfo {
	t.Helper()
	body := `{"query":"mutation { recordEngagement(kind: \"` + wireEventKind + `\", mediaID: \"` + mediaID + `\") { kind mediaID count events } }",` +
		`"variables":{"kind":"` + wireEventKind + `","mediaID":"` + mediaID + `"}}`
	data, errs := post(t, h, body, authorize)
	if len(errs) != 0 {
		t.Fatalf("recordEngagement errors = %#v", errs)
	}
	var info EngagementCounterInfo
	mustUnmarshal(t, data["recordEngagement"], &info)
	return info
}

// counterWire reads the authorizing Profile's counter (with history) for a Media
// target.
func counterWire(t *testing.T, h *Handler, authorize func(*http.Request), mediaID string) EngagementCounterResult {
	t.Helper()
	body := `{"query":"query { engagementCounter(kind: \"` + wireEventKind + `\", mediaID: \"` + mediaID + `\", history: true) { counter { count events } history { value } } }",` +
		`"variables":{"kind":"` + wireEventKind + `","mediaID":"` + mediaID + `","history":true}}`
	data, errs := post(t, h, body, authorize)
	if len(errs) != 0 {
		t.Fatalf("engagementCounter errors = %#v", errs)
	}
	var result EngagementCounterResult
	mustUnmarshal(t, data["engagementCounter"], &result)
	return result
}

// TestEngagementCounterIncrementCorrectCountHistory is the ADULT-1 core
// acceptance: two increments raise the net count to 2 with 2 history rows; a
// remove-last correction (a compensating event) drops the net to 1 while the
// history GROWS to 3 (append-only — the cancelled increment survives).
func TestEngagementCounterIncrementCorrectCountHistory(t *testing.T) {
	// feature:engagement-counter
	h := newCounterHandler(t)

	if got := counterWire(t, h, authorizeViewer, "media-alpha"); got.Counter.Count != 0 || got.Counter.Events != 0 {
		t.Fatalf("initial counter = %+v, want zero", got.Counter)
	}

	r1 := recordWire(t, h, authorizeViewer, "media-alpha")
	if r1.Count != 1 || r1.Events != 1 || r1.Kind != wireEventKind || r1.MediaID != "media-alpha" {
		t.Fatalf("first increment = %+v, want count 1 events 1", r1)
	}
	r2 := recordWire(t, h, authorizeViewer, "media-alpha")
	if r2.Count != 2 || r2.Events != 2 {
		t.Fatalf("second increment = %+v, want count 2 events 2", r2)
	}

	// Remove-last correction: net falls, history grows (append-only).
	body := `{"query":"mutation { removeLastEngagement(kind: \"` + wireEventKind + `\", mediaID: \"media-alpha\") { count events } }",` +
		`"variables":{"kind":"` + wireEventKind + `","mediaID":"media-alpha"}}`
	data, errs := post(t, h, body, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("removeLastEngagement errors = %#v", errs)
	}
	var corrected EngagementCounterInfo
	mustUnmarshal(t, data["removeLastEngagement"], &corrected)
	if corrected.Count != 1 {
		t.Fatalf("after correction net count = %d, want 1", corrected.Count)
	}
	if corrected.Events != 3 {
		t.Fatalf("after correction history depth = %d, want 3 (correction is a new row)", corrected.Events)
	}

	final := counterWire(t, h, authorizeViewer, "media-alpha")
	if final.Counter.Count != 1 || final.Counter.Events != 3 {
		t.Fatalf("final counter = %+v, want net 1 over 3 events", final.Counter)
	}
	if len(final.History) != 3 {
		t.Fatalf("history len = %d, want 3 preserved rows", len(final.History))
	}
	// All three rows are preserved: two +1 increments and one -1 correction. The
	// three are appended in the same test millisecond, so occurred_at ties and the
	// id-DESC tiebreak makes positional order nondeterministic — assert the value
	// multiset, which pins the append-only behavior rather than a timing artifact.
	var plus, minus int
	for _, ev := range final.History {
		switch ev.Value {
		case 1:
			plus++
		case -1:
			minus++
		default:
			t.Fatalf("unexpected history value %d", ev.Value)
		}
	}
	if plus != 2 || minus != 1 {
		t.Fatalf("history values = %d×(+1), %d×(-1), want 2 and 1", plus, minus)
	}
}

// TestEngagementCounterEntityAggregate is the ADR-0027 entity-aggregate read: a
// single increment can attribute to a Media AND an Entity, and the per-Entity
// roll-up totals the Profile's events for that Entity independently of any Media.
func TestEngagementCounterEntityAggregate(t *testing.T) {
	// feature:engagement-counter
	h := newCounterHandler(t)

	// Two increments naming the same Entity target (the entity roll-up); one also
	// names a Media, one is Entity-only.
	for _, mediaID := range []string{"media-alpha", ""} {
		body := `{"query":"mutation { recordEngagement(kind: \"` + wireEventKind + `\", mediaID: \"` + mediaID + `\", entityID: \"entity-counter-local\") { count } }",` +
			`"variables":{"kind":"` + wireEventKind + `","mediaID":"` + mediaID + `","entityID":"entity-counter-local"}}`
		_, errs := post(t, h, body, authorizeViewer)
		if len(errs) != 0 {
			t.Fatalf("entity increment (media %q) errors = %#v", mediaID, errs)
		}
	}

	body := `{"query":"query { engagementCounter(kind: \"` + wireEventKind + `\", entityID: \"entity-counter-local\") { counter { count events } } }",` +
		`"variables":{"kind":"` + wireEventKind + `","entityID":"entity-counter-local"}}`
	data, errs := post(t, h, body, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("entity counter errors = %#v", errs)
	}
	var result EngagementCounterResult
	mustUnmarshal(t, data["engagementCounter"], &result)
	if result.Counter.Count != 2 || result.Counter.Events != 2 {
		t.Fatalf("entity aggregate = %+v, want count 2 events 2", result.Counter)
	}
}

func TestEngagementTargetsAreRevealFilteredBeforeReadOrWrite(t *testing.T) {
	h := newCounterHandler(t)
	tests := []struct {
		name      string
		field     string
		hiddenID  string
		unknownID string
		target    func(string) store.EngagementEventTarget
	}{
		{
			name: "media", field: "mediaID", hiddenID: "media-archive", unknownID: "media-missing",
			target: func(id string) store.EngagementEventTarget {
				return store.EngagementEventTarget{MediaID: store.MediaID(id)}
			},
		},
		{
			name: "entity", field: "entityID", hiddenID: "entity-counter-hidden", unknownID: "entity-missing",
			target: func(id string) store.EngagementEventTarget {
				return store.EngagementEventTarget{EntityID: store.EntityID(id)}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.store.AppendEngagementEvent(t.Context(), store.EngagementEvent{
				ProfileID: "profile-default",
				MediaID:   tc.target(tc.hiddenID).MediaID,
				EntityID:  tc.target(tc.hiddenID).EntityID,
				Kind:      wireEventKind,
				Value:     1,
			}); err != nil {
				t.Fatalf("seed hidden engagement event: %v", err)
			}
			requestError := func(operation, id string) string {
				t.Helper()
				operationType := "query"
				if operation == "recordEngagement" {
					operationType = "mutation"
				}
				body := `{"query":"` + operationType + ` { ` + operation + `(kind: \"` + wireEventKind + `\", ` + tc.field + `: \"` + id + `\") {` +
					func() string {
						if operation == "engagementCounter" {
							return ` counter { count }`
						}
						return ` count`
					}() + ` } }","variables":{"kind":"` + wireEventKind + `","` + tc.field + `":"` + id + `"}}`
				_, errs := post(t, h, body, authorizeViewer)
				if len(errs) != 1 {
					t.Fatalf("%s(%s) errors = %#v, want one", operation, id, errs)
				}
				return errs[0].Message
			}

			for _, operation := range []string{"engagementCounter", "recordEngagement"} {
				hiddenErr := requestError(operation, tc.hiddenID)
				unknownErr := requestError(operation, tc.unknownID)
				if hiddenErr != unknownErr {
					t.Fatalf("%s hidden error %q != unknown error %q", operation, hiddenErr, unknownErr)
				}
			}

			for _, id := range []string{tc.hiddenID, tc.unknownID} {
				agg, err := h.store.EngagementEventAggregateFor(t.Context(), "profile-default", tc.target(id), wireEventKind)
				if err != nil {
					t.Fatalf("aggregate(%s): %v", id, err)
				}
				want := 0
				if id == tc.hiddenID {
					want = 1
				}
				if agg.Count != want {
					t.Fatalf("rejected target %s has %d engagement events, want unchanged %d", id, agg.Count, want)
				}
			}
		})
	}
}

func TestEngagementTargetsAllowRestrictedRowsWhenRevealed(t *testing.T) {
	h := newCounterHandler(t)
	for _, target := range []struct {
		field string
		id    string
	}{
		{field: "mediaID", id: "media-archive"},
		{field: "entityID", id: "entity-counter-hidden"},
	} {
		body := `{"query":"mutation { recordEngagement(kind: \"` + wireEventKind + `\", ` + target.field + `: \"` + target.id + `\") { count } }",` +
			`"variables":{"kind":"` + wireEventKind + `","` + target.field + `":"` + target.id + `"}}`
		if _, errs := post(t, h, body, authorizeRevealed); len(errs) != 0 {
			t.Fatalf("revealed target %s=%s errors = %#v", target.field, target.id, errs)
		}
	}
}

// TestEngagementCounterProfileScoped is the own-Profile guarantee (HARD
// CONSTRAINT): a counter is Profile-private. profile-default increments
// media-alpha; profile-managed (a different Profile) reads zero and increments
// independently without touching profile-default's count.
func TestEngagementCounterProfileScoped(t *testing.T) {
	// feature:engagement-counter
	h := newCounterHandler(t)

	recordWire(t, h, authorizeViewer, "media-alpha")
	recordWire(t, h, authorizeViewer, "media-alpha")

	// The OTHER Profile sees nothing of profile-default's counter.
	if got := counterWire(t, h, authorizeManaged, "media-alpha"); got.Counter.Count != 0 {
		t.Fatalf("managed Profile sees default's counter = %+v, want zero", got.Counter)
	}
	// profile-managed increments its own; profile-default is unaffected.
	recordWire(t, h, authorizeManaged, "media-alpha")
	if got := counterWire(t, h, authorizeManaged, "media-alpha"); got.Counter.Count != 1 {
		t.Fatalf("managed own counter = %+v, want 1", got.Counter)
	}
	if got := counterWire(t, h, authorizeViewer, "media-alpha"); got.Counter.Count != 2 {
		t.Fatalf("default counter after managed write = %+v, want still 2", got.Counter)
	}
}

// TestEngagementCounterRejectsUnknownKind: Core never accepts a kind no
// activated plugin declared (invariant #1 — the kind vocabulary is plugin-owned).
func TestEngagementCounterRejectsUnknownKind(t *testing.T) {
	// feature:engagement-counter
	h := newCounterHandler(t)
	body := `{"query":"mutation { recordEngagement(kind: \"not-a-declared-kind\", mediaID: \"media-alpha\") { count } }",` +
		`"variables":{"kind":"not-a-declared-kind","mediaID":"media-alpha"}}`
	_, errs := post(t, h, body, authorizeViewer)
	if len(errs) == 0 {
		t.Fatal("recordEngagement accepted an undeclared kind, want a client error")
	}
}

// TestEngagementCounterMyDataExportAndDelete is the ADR-0036 My-Data integration:
// (a) engagement events appear in the Profile's My-Data export, and (b) the
// hard-delete-all clears them with the rest of the Profile's data.
func TestEngagementCounterMyDataExportAndDelete(t *testing.T) {
	// feature:engagement-counter
	h := newCounterHandler(t)

	recordWire(t, h, authorizeViewer, "media-alpha")
	recordWire(t, h, authorizeViewer, "media-alpha")

	// (a) the event log is a My-Data export citizen.
	export := readExportEvents(t, h, authorizeViewer)
	if len(export.EngagementEvents) != 2 {
		t.Fatalf("export.EngagementEvents = %#v, want 2", export.EngagementEvents)
	}
	for _, ev := range export.EngagementEvents {
		if ev.Kind != wireEventKind || ev.MediaID != "media-alpha" || ev.Value != 1 {
			t.Errorf("exported event = %+v, want kind/media-alpha/+1", ev)
		}
	}

	// (b) hard-delete-all clears them (profile-default's seeded reveal PIN "1234"
	// confirms the delete; mirrors the mydata delete tests).
	delBody := `{"query":"mutation D($scope: String!, $pin: String){ deleteMyEngagement(scope: $scope, pin: $pin){ scope } }","variables":{"scope":"all","pin":"1234"}}`
	if _, errs := post(t, h, delBody, authorizeViewer); len(errs) != 0 {
		t.Fatalf("deleteMyEngagement errors = %#v", errs)
	}
	if export := readExportEvents(t, h, authorizeViewer); len(export.EngagementEvents) != 0 {
		t.Fatalf("export.EngagementEvents after delete = %#v, want empty", export.EngagementEvents)
	}
	if got := counterWire(t, h, authorizeViewer, "media-alpha"); got.Counter.Count != 0 || got.Counter.Events != 0 {
		t.Fatalf("counter after delete = %+v, want zero", got.Counter)
	}
}

// readExportEvents reads the caller's My-Data export, selecting the engagement
// events block.
func readExportEvents(t *testing.T, h *Handler, authorize func(*http.Request)) MyEngagementExportInfo {
	t.Helper()
	data, errs := post(t, h,
		`{"query":"query Q($pin: String){ myEngagement(pin: $pin){ schema engagementEvents { kind mediaID entityID value occurredAt } } }","variables":{"pin":"1234"}}`,
		authorize)
	if len(errs) != 0 {
		t.Fatalf("myEngagement errors = %#v", errs)
	}
	var export MyEngagementExportInfo
	mustUnmarshal(t, data["myEngagement"], &export)
	return export
}
