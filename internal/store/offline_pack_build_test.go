package store

import (
	"errors"
	"testing"
	"time"
)

func testOfflinePackBuild(id OfflinePackID) OfflinePackBuild {
	return OfflinePackBuild{
		PackID: id, ProfileID: "profile-default", DeviceID: "device-mac",
		TargetKind: "media", TargetRef: "media-alpha", Quality: "low",
		Payload: []byte(`{"packID":"` + string(id) + `"}`),
	}
}

// TestOfflinePackBuildLifecycle covers the CORE-1743 pending row: tracking is
// idempotent, progress only moves forward, a failure keeps progress and a
// reason, a restart happens once, and sealing the pack removes the row.
func TestOfflinePackBuildLifecycle(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	const id OfflinePackID = "pack-build-alpha"

	build, err := st.TrackOfflinePackBuild(ctx, testOfflinePackBuild(id), now)
	if err != nil {
		t.Fatal(err)
	}
	if build.State != OfflinePackBuildBuilding || build.Progress != 0 || build.FailureReason != "" {
		t.Fatalf("tracked build = %+v, want building at 0", build)
	}
	if err := st.SetOfflinePackBuildProgress(ctx, id, 0.5, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOfflinePackBuildProgress(ctx, id, 0.25, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	again, err := st.TrackOfflinePackBuild(ctx, testOfflinePackBuild(id), now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if again.Progress != 0.5 || !again.CreatedAt.Equal(now) {
		t.Fatalf("re-tracked build = %+v, want the original row at progress 0.5", again)
	}
	if _, err := st.TrackOfflinePackBuild(ctx, OfflinePackBuild{
		PackID: id, ProfileID: "profile-managed", TargetKind: "media", Payload: []byte("{}"),
	}, now); !errors.Is(err, ErrOfflinePackBuildNotFound) {
		t.Fatalf("tracking another Profile's pack ID error = %v, want not found", err)
	}
	if _, err := st.OfflinePackBuildByID(ctx, id, "profile-managed"); !errors.Is(err, ErrOfflinePackBuildNotFound) {
		t.Fatalf("cross-Profile read error = %v, want not found", err)
	}

	if err := st.FailOfflinePackBuild(ctx, id, "transcode_failed", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	failed, err := st.OfflinePackBuildByID(ctx, id, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != OfflinePackBuildFailed || failed.FailureReason != "transcode_failed" || failed.Progress != 0.5 {
		t.Fatalf("failed build = %+v, want failed/transcode_failed at 0.5", failed)
	}
	if err := st.SetOfflinePackBuildProgress(ctx, id, 0.9, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	restarted, err := st.RestartOfflinePackBuild(ctx, id, "profile-default", []byte(`{"retry":true}`), now.Add(6*time.Second))
	if err != nil || !restarted {
		t.Fatalf("first restart = %v, %v; want a transition", restarted, err)
	}
	restarted, err = st.RestartOfflinePackBuild(ctx, id, "profile-default", []byte(`{"retry":true}`), now.Add(7*time.Second))
	if err != nil || restarted {
		t.Fatalf("second restart = %v, %v; want no transition", restarted, err)
	}
	building, err := st.OfflinePackBuildByID(ctx, id, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if building.State != OfflinePackBuildBuilding || building.Progress != 0 || building.FailureReason != "" ||
		string(building.Payload) != `{"retry":true}` {
		t.Fatalf("restarted build = %+v, want building, cleared, new payload", building)
	}
	if err := st.DeleteFailedOfflinePackBuild(ctx, id, "profile-default"); !errors.Is(err, ErrOfflinePackBuildNotFound) {
		t.Fatalf("dismissing a building row error = %v, want not found", err)
	}

	if _, err := st.CreateOfflinePack(ctx, OfflinePack{
		ID: id, ProfileID: "profile-default", DeviceID: "device-mac", TargetKind: "media",
		TargetRef: "media-alpha", State: OfflinePackReady,
	}, []OfflinePackItem{{MediaID: "media-alpha", SortOrder: 1}}, now.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	builds, err := st.OfflinePackBuildsForProfile(ctx, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 0 {
		t.Fatalf("builds after sealing = %+v, want none", builds)
	}
	if _, err := st.TrackOfflinePackBuild(ctx, testOfflinePackBuild(id), now.Add(9*time.Second)); !errors.Is(err, ErrOfflinePackBuildNotFound) {
		t.Fatalf("tracking a sealed pack error = %v, want not found", err)
	}
}

// TestOfflinePackBuildDismissAndSchema covers dismissal of a failed row and
// the table's closed state and progress constraints.
func TestOfflinePackBuildDismissAndSchema(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	const id OfflinePackID = "pack-build-beta"
	if _, err := st.TrackOfflinePackBuild(ctx, testOfflinePackBuild(id), now); err != nil {
		t.Fatal(err)
	}
	if err := st.FailOfflinePackBuild(ctx, id, "build_failed", now); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteFailedOfflinePackBuild(ctx, id, "profile-managed"); !errors.Is(err, ErrOfflinePackBuildNotFound) {
		t.Fatalf("cross-Profile dismiss error = %v, want not found", err)
	}
	if err := st.DeleteFailedOfflinePackBuild(ctx, id, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if builds, err := st.OfflinePackBuildsForProfile(ctx, "profile-default"); err != nil || len(builds) != 0 {
		t.Fatalf("builds after dismissal = %+v, %v; want none", builds, err)
	}
	nowMS := now.UnixMilli()
	for name, query := range map[string]string{
		"state":    `INSERT INTO offline_pack_build(pack_id, profile_id, target_kind, state, job_payload, created_at_ms, updated_at_ms) VALUES ('raw-state', 'profile-default', 'media', 'ready', x'7b7d', ?, ?)`,
		"progress": `INSERT INTO offline_pack_build(pack_id, profile_id, target_kind, state, progress, job_payload, created_at_ms, updated_at_ms) VALUES ('raw-progress', 'profile-default', 'media', 'building', 1.5, x'7b7d', ?, ?)`,
		"profile":  `INSERT INTO offline_pack_build(pack_id, profile_id, target_kind, state, job_payload, created_at_ms, updated_at_ms) VALUES ('raw-profile', 'profile-missing', 'media', 'building', x'7b7d', ?, ?)`,
	} {
		if _, err := st.catalogW.ExecContext(ctx, query, nowMS, nowMS); err == nil {
			t.Errorf("raw invalid %s insert succeeded", name)
		}
	}
}
