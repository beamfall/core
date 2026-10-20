package graph

import (
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestMySessionsOmitsRestrictedTokenRowsAfterRelock(t *testing.T) {
	st := storetest.Open(t)
	now := time.Now().UTC()
	if err := st.RecordStreamToken(t.Context(), "session-revealed", "profile-default", "device-tv", "media", "media-archive", true, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPackToken(t.Context(), "session-revealed", "profile-default", "device-tv", "offline-pack", store.OfflinePackID("pack-restricted"), true, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	h := newTestHandlerWithStore(st)
	query := `{"query":"query { mySessions { streamTokens { targetKind expiresAt } packTokens { targetKind expiresAt } } }"}`
	data, errs := post(t, h, query, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("relocked errors = %#v", errs)
	}
	var relocked MySessionsInfo
	mustUnmarshal(t, data["mySessions"], &relocked)
	if len(relocked.StreamTokens) != 0 || len(relocked.PackTokens) != 0 {
		t.Fatalf("relocked tokens = %+v / %+v", relocked.StreamTokens, relocked.PackTokens)
	}
	data, errs = post(t, h, query, authorizeRevealed)
	if len(errs) != 0 {
		t.Fatalf("revealed errors = %#v", errs)
	}
	var revealed MySessionsInfo
	mustUnmarshal(t, data["mySessions"], &revealed)
	if len(revealed.StreamTokens) != 1 || len(revealed.PackTokens) != 1 {
		t.Fatalf("revealed tokens = %+v / %+v", revealed.StreamTokens, revealed.PackTokens)
	}
}
