package compat

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

type revealFixture []string

// restrictedFixture collects the IDs and titles of restricted libraries and
// their live media from the seeded store while Reveal is active.
func restrictedFixture(t *testing.T, st *store.Store) revealFixture {
	t.Helper()
	ctx := t.Context()
	filter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	libraries, err := st.Libraries(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	var restricted revealFixture
	var itemCount int
	for _, library := range libraries {
		if !library.IsRestricted {
			continue
		}
		restricted = append(restricted, string(library.ID), library.Title)
		ids, err := st.MediaIDsForLibrary(ctx, library.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			item, err := st.Media(ctx, id, filter)
			if err != nil {
				t.Fatal(err)
			}
			restricted = append(restricted, string(item.ID), item.Title)
			itemCount++
		}
	}
	if itemCount == 0 {
		t.Fatal("seed must contain live media in a Reveal-restricted library")
	}
	return restricted
}

func (f revealFixture) assertAbsent(t *testing.T, body []byte) {
	t.Helper()
	check := func(value string) {
		t.Helper()
		for _, restricted := range f {
			if strings.Contains(value, restricted) {
				t.Fatalf("compat response exposes Reveal-restricted fixture value %q", restricted)
			}
		}
	}
	check(string(body))
	// Inspect JSON strings too, so escape sequences cannot conceal a match.
	dec := json.NewDecoder(bytes.NewReader(body))
	for {
		token, err := dec.Token()
		if err != nil {
			return
		}
		if value, ok := token.(string); ok {
			check(value)
		}
	}
}
