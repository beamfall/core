package store

import (
	"errors"
	"testing"
	"time"
)

func TestOfflinePackCreateListItemsRevoke(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

	pack, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-default",
		DeviceID:   "device-mac",
		TargetKind: "media",
		TargetRef:  "media-alpha",
		State:      OfflinePackReady,
		Manifest:   `{"label":"Offline Pack","items":[{"mediaID":"media-alpha","title":"Sample Media"}]}`,
	}, []OfflinePackItem{{MediaID: "media-alpha", SortOrder: 1}}, now)
	if err != nil {
		t.Fatalf("CreateOfflinePack: %v", err)
	}
	if pack.ID == "" || pack.State != OfflinePackReady || pack.ItemCount != 1 {
		t.Fatalf("pack = %+v, want ready 1-item pack with id", pack)
	}

	packs, err := st.OfflinePacksForProfile(ctx, "profile-default")
	if err != nil {
		t.Fatalf("OfflinePacksForProfile: %v", err)
	}
	if len(packs) != 1 || packs[0].ID != pack.ID {
		t.Fatalf("packs = %+v, want the one created pack", packs)
	}

	// Cross-Profile isolation: another Profile cannot read it.
	if _, err := st.OfflinePackByID(ctx, pack.ID, "profile-managed"); err != ErrOfflinePackNotFound {
		t.Fatalf("cross-profile read err = %v, want ErrOfflinePackNotFound", err)
	}

	items, err := st.OfflinePackItems(ctx, pack.ID)
	if err != nil || len(items) != 1 || items[0].MediaID != "media-alpha" {
		t.Fatalf("OfflinePackItems = %+v, err %v", items, err)
	}

	revoked, err := st.RevokeOfflinePack(ctx, pack.ID, "profile-default", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("RevokeOfflinePack: %v", err)
	}
	if revoked.State != OfflinePackRevoked || revoked.RevokedAt == nil {
		t.Fatalf("revoked = %+v, want revoked state + timestamp", revoked)
	}
}

func TestOfflinePackRejectsEmpty(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Now().UTC()
	_, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-default",
		TargetKind: "media",
	}, nil, now)
	if err == nil {
		t.Fatal("CreateOfflinePack with no items must error")
	}
}

func TestOfflinePackRejectsUnknownProfile(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	_, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-missing",
		TargetKind: "media",
	}, []OfflinePackItem{{MediaID: "media-alpha"}}, time.Now().UTC())
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("CreateOfflinePack err = %v (%T), want ValidationError", err, err)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM offline_pack WHERE profile_id = 'profile-missing'`); got != 0 {
		t.Fatalf("orphan packs = %d, want 0", got)
	}
}

func TestOfflinePackCreateRollsBackWhenItemInsertFails(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	_, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-default",
		TargetKind: "media",
	}, []OfflinePackItem{{MediaID: "media-missing"}}, time.Now().UTC())
	if err == nil {
		t.Fatal("CreateOfflinePack with missing item media must error")
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM offline_pack`); got != 0 {
		t.Fatalf("packs after failed item insert = %d, want 0", got)
	}
}

func TestOfflinePackRejectsInvalidState(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Now().UTC()
	if _, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-default",
		TargetKind: "media",
		State:      OfflinePackState("wat"),
	}, []OfflinePackItem{{MediaID: "media-alpha"}}, now); err == nil {
		t.Fatal("CreateOfflinePack with invalid state must error")
	}
	pack, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-default",
		TargetKind: "media",
	}, []OfflinePackItem{{MediaID: "media-alpha"}}, now)
	if err != nil {
		t.Fatalf("CreateOfflinePack: %v", err)
	}
	if _, err := st.SetOfflinePackState(ctx, pack.ID, OfflinePackState("wat"), now); err == nil {
		t.Fatal("SetOfflinePackState with invalid state must error")
	}
}

func TestOfflinePackRejectsBytesReadyAboveTotal(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Now().UTC()
	if _, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-default",
		TargetKind: "media",
		BytesTotal: 10,
		BytesReady: 11,
	}, []OfflinePackItem{{MediaID: "media-alpha"}}, now); err == nil {
		t.Fatal("CreateOfflinePack with bytes_ready > bytes_total must error")
	}
	pack, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-default",
		TargetKind: "media",
		BytesTotal: 10,
		BytesReady: 5,
	}, []OfflinePackItem{{MediaID: "media-alpha"}}, now)
	if err != nil {
		t.Fatalf("CreateOfflinePack: %v", err)
	}
	if _, err := st.SetOfflinePackProgress(ctx, pack.ID, 11, 10, now); err == nil {
		t.Fatal("SetOfflinePackProgress with bytes_ready > bytes_total must error")
	}
}

func TestOfflinePackSchemaRejectsRawInvalidRows(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	nowMS := time.Now().UTC().UnixMilli()
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO offline_pack(id, profile_id, target_kind, state, bytes_total, bytes_ready, created_at_ms, updated_at_ms)
		VALUES ('raw-invalid-state', 'profile-default', 'media', 'wat', 0, 0, ?, ?)
	`, nowMS, nowMS); err == nil {
		t.Fatal("raw invalid state insert must error")
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO offline_pack(id, profile_id, target_kind, state, bytes_total, bytes_ready, created_at_ms, updated_at_ms)
		VALUES ('raw-overflow', 'profile-default', 'media', 'pending', 10, 11, ?, ?)
	`, nowMS, nowMS); err == nil {
		t.Fatal("raw bytes overflow insert must error")
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO offline_pack(id, profile_id, target_kind, state, bytes_total, bytes_ready, created_at_ms, updated_at_ms)
		VALUES ('raw-orphan', 'profile-missing', 'media', 'pending', 0, 0, ?, ?)
	`, nowMS, nowMS); err == nil {
		t.Fatal("raw missing profile insert must error")
	}
}

func TestOfflinePackExpirySweep(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	created := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	expiry := created.Add(time.Hour)

	pack, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-default",
		TargetKind: "media",
		State:      OfflinePackReady,
		ExpiresAt:  &expiry,
	}, []OfflinePackItem{{MediaID: "media-alpha"}}, created)
	if err != nil {
		t.Fatalf("CreateOfflinePack: %v", err)
	}

	// Before expiry: no change.
	if n, err := st.ExpireOfflinePacks(ctx, created.Add(30*time.Minute)); err != nil || n != 0 {
		t.Fatalf("ExpireOfflinePacks early = %d, %v; want 0", n, err)
	}
	// After expiry: swept to expired.
	if n, err := st.ExpireOfflinePacks(ctx, expiry.Add(time.Minute)); err != nil || n != 1 {
		t.Fatalf("ExpireOfflinePacks due = %d, %v; want 1", n, err)
	}
	got, err := st.OfflinePackByID(ctx, pack.ID, "profile-default")
	if err != nil {
		t.Fatalf("OfflinePackByID: %v", err)
	}
	if got.State != OfflinePackExpired {
		t.Fatalf("state = %q, want expired", got.State)
	}
	// Sweep is idempotent and leaves revoked/expired terminal rows alone.
	if n, _ := st.ExpireOfflinePacks(ctx, expiry.Add(2*time.Minute)); n != 0 {
		t.Fatalf("second sweep changed %d rows, want 0", n)
	}
}

func TestPackTokenLedgerRecordReadDelete(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Hour)

	if err := st.RecordPackToken(ctx, "session-default", "profile-default", "device-mac", "offline-pack-manifest", "offline-pack-1", true, expiry); err != nil {
		t.Fatalf("RecordPackToken: %v", err)
	}
	// Re-record upserts (refreshes expiry) rather than duplicating.
	if err := st.RecordPackToken(ctx, "session-default", "profile-default", "device-mac", "offline-pack-manifest", "offline-pack-1", true, expiry.Add(time.Hour)); err != nil {
		t.Fatalf("RecordPackToken upsert: %v", err)
	}

	tokens, err := st.MyPackTokens(ctx, "profile-default", now)
	if err != nil {
		t.Fatalf("MyPackTokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].TargetKind != "offline-pack-manifest" {
		t.Fatalf("tokens = %+v, want one manifest token (reveal-blind: kind+expiry only)", tokens)
	}

	if err := st.DeleteMyPackTokens(ctx, "profile-default"); err != nil {
		t.Fatalf("DeleteMyPackTokens: %v", err)
	}
	tokens, err = st.MyPackTokens(ctx, "profile-default", now)
	if err != nil || len(tokens) != 0 {
		t.Fatalf("after delete tokens = %+v, err %v; want none", tokens, err)
	}
}
