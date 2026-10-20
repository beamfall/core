package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func openIWStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, ctx
}

// TestIntegrationWorkerRoundTrip proves the worker row upserts and reads back.
func TestIntegrationWorkerRoundTrip(t *testing.T) {
	st, ctx := openIWStore(t)
	now := time.Now()
	if _, ok, err := st.IntegrationWorker(ctx, "p"); err != nil || ok {
		t.Fatalf("absent worker: ok=%v err=%v", ok, err)
	}
	w := IntegrationWorkerRow{PluginID: "p", Version: "1.0", ArtifactSHA256: "abc", State: "installed"}
	if err := st.SetIntegrationWorker(ctx, w, now); err != nil {
		t.Fatalf("set worker: %v", err)
	}
	got, ok, err := st.IntegrationWorker(ctx, "p")
	if err != nil || !ok {
		t.Fatalf("read worker: ok=%v err=%v", ok, err)
	}
	if got.Version != "1.0" || got.ArtifactSHA256 != "abc" || got.State != "installed" {
		t.Fatalf("worker row mismatch: %+v", got)
	}
}

// TestPluginGrantRoundTrip proves grant rows upsert/read with their bound scope.
func TestPluginGrantRoundTrip(t *testing.T) {
	st, ctx := openIWStore(t)
	now := time.Now()
	g := PluginGrantRow{PluginID: "p", Kind: "read-catalog", Approved: true, BoundProfileID: ProfileID("prof")}
	if err := st.SetPluginGrant(ctx, g, now); err != nil {
		t.Fatalf("set grant: %v", err)
	}
	rows, err := st.PluginGrants(ctx, "p")
	if err != nil || len(rows) != 1 {
		t.Fatalf("read grants: %d rows err=%v", len(rows), err)
	}
	if !rows[0].Approved || rows[0].BoundProfileID != "prof" || rows[0].AllLibraries {
		t.Fatalf("grant row mismatch: %+v", rows[0])
	}
}

// TestPluginGrantScopeConflictRejected proves the store refuses a grant binding
// both a Profile and all-Libraries — the Go guard before the DB CHECK.
func TestPluginGrantScopeConflictRejected(t *testing.T) {
	st, ctx := openIWStore(t)
	bad := PluginGrantRow{PluginID: "p", Kind: "read-catalog", BoundProfileID: ProfileID("prof"), AllLibraries: true}
	if err := st.SetPluginGrant(ctx, bad, time.Now()); !errors.Is(err, ErrGrantScopeConflict) {
		t.Fatalf("conflicting grant must be rejected, got %v", err)
	}
}

// TestPluginUpdateSourceRoundTrip proves the update-source row upserts/reads.
func TestPluginUpdateSourceRoundTrip(t *testing.T) {
	st, ctx := openIWStore(t)
	u := PluginUpdateSourceRow{PluginID: "p", Kind: "folder", Location: "/srv/plugins", PinKind: "hash", Pin: "abc"}
	if err := st.SetPluginUpdateSource(ctx, u, time.Now()); err != nil {
		t.Fatalf("set update source: %v", err)
	}
	got, ok, err := st.PluginUpdateSource(ctx, "p")
	if err != nil || !ok {
		t.Fatalf("read update source: ok=%v err=%v", ok, err)
	}
	if got.Kind != "folder" || got.PinKind != "hash" || got.Pin != "abc" {
		t.Fatalf("update source mismatch: %+v", got)
	}
	if got.SigningKey != "" || got.Signature != "" {
		t.Fatalf("hash pin must leave signing key/signature empty: %+v", got)
	}

	// A signing-key pin round-trips the trusted public key + detached signature
	// (PLUG-1-SIGNKEY: the launch path verifies against these).
	sk := PluginUpdateSourceRow{
		PluginID: "p", Kind: "url", Location: "https://example/feed", PinKind: "signing-key",
		Pin: "key-1", SigningKey: "abcd", Signature: "ef01",
	}
	if err := st.SetPluginUpdateSource(ctx, sk, time.Now()); err != nil {
		t.Fatalf("set signing-key update source: %v", err)
	}
	got, ok, err = st.PluginUpdateSource(ctx, "p")
	if err != nil || !ok {
		t.Fatalf("read signing-key update source: ok=%v err=%v", ok, err)
	}
	if got.PinKind != "signing-key" || got.SigningKey != "abcd" || got.Signature != "ef01" {
		t.Fatalf("signing-key round-trip mismatch: %+v", got)
	}
}

// TestReadEventOutboxFilteredDropsDeviceAggregates proves a worker's filtered
// event tail never includes per-Profile device aggregates (session/group/
// extension) — those are device-subscriber signals a worker does not receive.
func TestReadEventOutboxFilteredDropsDeviceAggregates(t *testing.T) {
	st, ctx := openIWStore(t)
	now := time.Now()
	// A session-control event (device-subscriber aggregate).
	if err := st.AppendSessionControlEvent(ctx, SessionID("s1"),
		SessionControlPayload{ProfileID: ProfileID("prof"), Command: "pause"}, now); err != nil {
		t.Fatalf("append session-control event: %v", err)
	}
	events, err := st.ReadEventOutboxFiltered(ctx, 0, 100, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("filtered read: %v", err)
	}
	for _, e := range events {
		if e.AggregateType == OutboxAggregateSession {
			t.Fatal("a worker must never receive a session-control device aggregate")
		}
	}
}
