//go:build beamfall_appstore

package graph

import (
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

// TestAppStoreSuppressedFieldsAreUnknownOverHTTP covers CORE-2683: on an App
// Store binary the suppressed fields are absent from the dispatch registry, so
// a request naming one is refused as an unknown top-level field before any
// authorization or resolver runs, even with an open Admin Session.
func TestAppStoreSuppressedFieldsAreUnknownOverHTTP(t *testing.T) {
	for name := range suppressedDispatchFields {
		if _, ok := fieldHandlerRegistry[name]; ok {
			t.Errorf("fieldHandlerRegistry still dispatches suppressed field %q", name)
		}
	}
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	for _, query := range []string{
		`query { jellyfinCompat { enabled } }`,
		`mutation { setJellyfinCompat(enabled: true) { enabled } }`,
		`mutation { setProfileIntegrationSignIn(profileID: "profile-default", enabled: true) { id } }`,
		`query { qbittorrentIngest { enabled } }`,
		`query { capabilityStates { id } }`,
	} {
		body := postGraphQL(t, handler, authorizeManager, query, nil).Body.String()
		if !strings.Contains(body, "unknown top-level field") {
			t.Errorf("%s => %s; want an unknown top-level field error", query, body)
		}
	}
}
