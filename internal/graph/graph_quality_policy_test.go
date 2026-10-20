package graph

import (
	"reflect"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type qualityPolicyWire struct {
	Order  []string `json:"order"`
	Cutoff string   `json:"cutoff"`
}

// TestQualityPolicyAdminGate proves the per-Library quality policy is Console
// operator config: it is invisible until the manager opens an Admin Session
// (feature:quality-policy, ADR-0009 §4).
func TestQualityPolicyAdminGate(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	_, errs := post(t, handler, `{"query":"query Q($libraryID: String!) { qualityPolicy(libraryID: $libraryID) { order cutoff } }","variables":{"libraryID":"library-local"}}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("non-admin qualityPolicy errors = %#v, want admin-session gate", errs)
	}

	openManagerAdminSession(t, st)
	data, errs := post(t, handler, `{"query":"query Q($libraryID: String!) { qualityPolicy(libraryID: $libraryID) { order cutoff } }","variables":{"libraryID":"library-local"}}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("qualityPolicy errors = %#v", errs)
	}
	var got qualityPolicyWire
	mustUnmarshal(t, data["qualityPolicy"], &got)
	want := store.DefaultQualityPolicy()
	if !reflect.DeepEqual(got.Order, want.Order) || got.Cutoff != want.Cutoff {
		t.Fatalf("qualityPolicy = %#v, want default %#v", got, want)
	}
}

// TestSetQualityPolicyPersistsAndValidates proves the GraphQL mutation writes
// through the store policy validator, returns the persisted shape, and rejects
// invalid closed-vocabulary tiers.
func TestSetQualityPolicyPersistsAndValidates(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	body := `{"query":"mutation S($libraryID: String!, $order: [String!]!, $cutoff: String!) { setQualityPolicy(libraryID: $libraryID, order: $order, cutoff: $cutoff) { order cutoff } }","variables":{"libraryID":"library-local","order":["hd1080","hd720","sd"],"cutoff":"hd720"}}`
	data, errs := post(t, handler, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setQualityPolicy errors = %#v", errs)
	}
	var changed qualityPolicyWire
	mustUnmarshal(t, data["setQualityPolicy"], &changed)
	if !reflect.DeepEqual(changed.Order, []string{store.QualityTierHD1080, store.QualityTierHD720, store.QualityTierSD}) || changed.Cutoff != store.QualityTierHD720 {
		t.Fatalf("setQualityPolicy = %#v", changed)
	}

	data, errs = post(t, handler, `{"query":"query Q($libraryID: String!) { qualityPolicy(libraryID: $libraryID) { order cutoff } }","variables":{"libraryID":"library-local"}}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("qualityPolicy reread errors = %#v", errs)
	}
	var reread qualityPolicyWire
	mustUnmarshal(t, data["qualityPolicy"], &reread)
	if !reflect.DeepEqual(reread.Order, changed.Order) || reread.Cutoff != changed.Cutoff {
		t.Fatalf("qualityPolicy reread = %#v, want %#v", reread, changed)
	}

	_, errs = post(t, handler, `{"query":"mutation S($libraryID: String!, $order: [String!]!, $cutoff: String!) { setQualityPolicy(libraryID: $libraryID, order: $order, cutoff: $cutoff) { order } }","variables":{"libraryID":"library-local","order":["4k"],"cutoff":"4k"}}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown quality tier 4k") {
		t.Fatalf("invalid setQualityPolicy errors = %#v, want quality-tier validation", errs)
	}
}
