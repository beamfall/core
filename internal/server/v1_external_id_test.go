package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

func TestV1MediaDetailProtectsLegacyURLGUID(t *testing.T) {
	const guid = "https://cdn.example.com/track.mp3?signature=test-secret"
	detail := newV1MediaDetail(store.Media{Title: guid}, []store.MediaExternalID{
		{Scheme: "feed-guid", Value: guid},
		{Scheme: store.ProvisionalExternalIDScheme, Value: "sub-1\x00" + guid},
	})
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Title != "Untitled" || len(detail.ExternalIDs) != 2 ||
		strings.Contains(string(data), "https://") || strings.Contains(string(data), "test-secret") {
		t.Fatal("Media detail exposed a legacy feed GUID")
	}
}

func TestV1LookupResolvesOpaqueLegacyFeedGUID(t *testing.T) {
	const guid = "https://cdn.example.com/track.mp3?signature=test-secret"
	srv, st := newTestServerWithStore(t)
	for _, id := range []store.MediaExternalID{
		{Scheme: "feed-guid", Value: guid},
		{Scheme: store.ProvisionalExternalIDScheme, Value: "sub-1\x00" + guid},
	} {
		if err := st.AddMediaExternalID(t.Context(), "media-alpha", id.Scheme, id.Value); err != nil {
			t.Fatal("could not seed legacy identifier")
		}
	}
	key := mintKey(t, st, "lookup", "profile-managed", store.ScopeReadCatalog)
	rec := v1Get(t, srv, "/v1/media/media-alpha", key)
	var detail v1MediaDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || rec.Code != http.StatusOK ||
		len(detail.ExternalIDs) != 2 || strings.Contains(rec.Body.String(), "test-secret") {
		t.Fatal("detail did not return two safe legacy identifiers")
	}
	for _, id := range detail.ExternalIDs {
		if !strings.Contains(id.Value, "urn:beamfall:feed-entry:") {
			t.Fatal("detail identifier is not opaque")
		}
		query := url.Values{"scheme": {id.Scheme}, "value": {id.Value}}
		rec := v1Get(t, srv, "/v1/media?"+query.Encode(), key)
		var found v1MediaLookupResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &found); err != nil || rec.Code != http.StatusOK ||
			len(found.Media) != 1 || found.Media[0].ID != detail.ID || strings.Contains(rec.Body.String(), "test-secret") {
			t.Fatalf("opaque lookup by %s did not return the same Media safely", id.Scheme)
		}
	}
}

// TestV1LookupMediaByExternalID pins CORE-1698: an integration that knows an
// item only by another system's identifier can ask whether it is in the
// library, and only Media the key can see answer.
func TestV1LookupMediaByExternalID(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	// media-alpha lives in library-local; media-archive in the restricted
	// library-archive the managed Profile cannot see. Both carry the identifier.
	for _, id := range []store.MediaID{"media-alpha", "media-archive"} {
		if err := st.AddMediaExternalID(ctx, id, "example.db", "item-7"); err != nil {
			t.Fatalf("AddMediaExternalID(%s): %v", id, err)
		}
	}
	if err := st.AddMediaExternalID(ctx, "media-alpha", "another.db", "a-1"); err != nil {
		t.Fatalf("AddMediaExternalID: %v", err)
	}
	key := mintKey(t, st, "lookup", "profile-managed", store.ScopeReadCatalog)

	rec := v1Get(t, srv, "/v1/media?scheme=example.db&value=item-7", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("lookup status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var found v1MediaLookupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &found); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(found.Media) != 1 || found.Media[0].ID != "media-alpha" || found.Media[0].LibraryID != "library-local" {
		t.Fatalf("lookup = %#v, want only the visible media-alpha", found.Media)
	}
	wantIDs := []v1ExternalID{{Scheme: "another.db", Value: "a-1"}, {Scheme: "example.db", Value: "item-7"}}
	if got := found.Media[0].ExternalIDs; len(got) != 2 || got[0] != wantIDs[0] || got[1] != wantIDs[1] {
		t.Fatalf("externalIDs = %#v, want %#v", got, wantIDs)
	}

	// An enrolled device Session of the managed Profile sees the same filtered answer.
	rec = v1DeviceRequest(t, srv, http.MethodGet, "/v1/media?scheme=example.db&value=item-7", "", "", authorizeManaged)
	var viaDevice v1MediaLookupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &viaDevice); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("device session lookup = %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	if len(viaDevice.Media) != 1 || viaDevice.Media[0].ID != "media-alpha" {
		t.Fatalf("device session lookup = %#v, want only the visible media-alpha", viaDevice.Media)
	}

	// An identifier nobody carries is an empty list, not an error.
	rec = v1Get(t, srv, "/v1/media?scheme=example.db&value=absent", key)
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"media\":[]}\n" {
		t.Fatalf("absent lookup = %d %q, want 200 with an empty list", rec.Code, rec.Body.String())
	}

	// The detail read carries the same identifiers.
	rec = v1Get(t, srv, "/v1/media/media-alpha", key)
	var detail v1MediaDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("detail = %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	if len(detail.ExternalIDs) != 2 || detail.ExternalIDs[1] != wantIDs[1] {
		t.Fatalf("detail externalIDs = %#v, want %#v", detail.ExternalIDs, wantIDs)
	}
}

func TestV1LookupMediaByExternalIDRejectsBadQueriesAndScopes(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	key := mintKey(t, st, "lookup", "profile-managed", store.ScopeReadCatalog)
	for _, path := range []string{
		"/v1/media",
		"/v1/media?scheme=example.db",
		"/v1/media?value=item-7",
		"/v1/media?scheme=Not%20A%20Scheme&value=item-7",
	} {
		if rec := v1Get(t, srv, path, key); rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s = %d, want 400: %s", path, rec.Code, rec.Body.String())
		}
	}
	ingestKey := mintKey(t, st, "ingest", "", store.ScopeIngest)
	if rec := v1Get(t, srv, "/v1/media?scheme=example.db&value=item-7", ingestKey); rec.Code != http.StatusForbidden {
		t.Fatalf("ingest key lookup = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if rec := v1Get(t, srv, "/v1/media?scheme=example.db&value=item-7", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous lookup = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}
