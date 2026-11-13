package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDeviceBuildBindingIsExactPerDeviceAndSurface(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()
	for _, id := range []DeviceID{"device-bound", "device-sibling"} {
		if err := st.EnrollDevice(ctx,
			Device{ID: id, Name: "Phone", Role: DeviceRoleViewer, DistributionClass: "appStore", SecretHash: HashDeviceSecret("s")},
			Session{ID: SessionID("session-" + id), DeviceID: id, ProfileID: "profile-default", ExpiresAt: now.Add(time.Hour)},
		); err != nil {
			t.Fatal(err)
		}
	}

	if got, err := st.DeviceBuildBindings(ctx, "device-bound"); err != nil || len(got) != 0 {
		t.Fatalf("fresh device bindings = %+v, %v; want none", got, err)
	}
	if err := st.BindDeviceBuild(ctx, "device-bound", DeviceSurfaceIOSViewer, "ios-viewer-1.0.0-100", now); err != nil {
		t.Fatalf("BindDeviceBuild: %v", err)
	}
	if err := st.BindDeviceBuild(ctx, "device-bound", DeviceSurfaceIOSViewer, "ios-viewer-1.0.0-100", now); err != nil {
		t.Fatalf("re-recording the same build: %v", err)
	}
	got, err := st.DeviceBuildBindings(ctx, "device-bound")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].DeviceID != "device-bound" || got[0].Surface != DeviceSurfaceIOSViewer ||
		got[0].BuildID != "ios-viewer-1.0.0-100" || got[0].BoundAt.UnixMilli() != now.UnixMilli() {
		t.Fatalf("bindings = %+v", got)
	}
	if sibling, err := st.DeviceBuildBindings(ctx, "device-sibling"); err != nil || len(sibling) != 0 {
		t.Fatalf("a same-class sibling device inherited %+v, %v", sibling, err)
	}

	later := now.Add(time.Minute)
	if err := st.BindDeviceBuild(ctx, "device-bound", DeviceSurfaceIOSViewer, "ios-viewer-1.0.1-101", later); err != nil {
		t.Fatalf("upgrade binding: %v", err)
	}
	got, _ = st.DeviceBuildBindings(ctx, "device-bound")
	if len(got) != 1 || got[0].BuildID != "ios-viewer-1.0.1-101" {
		t.Fatalf("after upgrade bindings = %+v, want the one replaced row", got)
	}

	refusals := map[string]struct {
		device  DeviceID
		surface DeviceSurface
		build   string
		at      time.Time
	}{
		"no device":       {"", DeviceSurfaceIOSViewer, "b", now},
		"unknown device":  {"device-missing", DeviceSurfaceIOSViewer, "b", now},
		"web surface":     {"device-bound", DeviceSurfaceCoreWebUI, "b", now},
		"empty surface":   {"device-bound", "", "b", now},
		"foreign surface": {"device-bound", "watch-viewer", "b", now},
		"empty build":     {"device-bound", DeviceSurfaceIOSViewer, "", now},
		"padded build":    {"device-bound", DeviceSurfaceIOSViewer, " b", now},
		"overlong build":  {"device-bound", DeviceSurfaceIOSViewer, strings.Repeat("b", 257), now},
		"no time":         {"device-bound", DeviceSurfaceIOSViewer, "b", time.Time{}},
	}
	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			if err := st.BindDeviceBuild(ctx, tc.device, tc.surface, tc.build, tc.at); err == nil {
				t.Fatal("binding accepted, want refusal")
			}
		})
	}

	if err := st.RevokeDevice(ctx, "device-sibling"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindDeviceBuild(ctx, "device-sibling", DeviceSurfaceIOSViewer, "ios-viewer-1.0.0-100", now); err == nil {
		t.Fatal("a revoked device was bound to a build")
	}
}

func TestDeviceBuildBindingSchemaRefusesWhatTheStoreRefuses(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if err := st.EnrollDevice(ctx,
		Device{ID: "device-schema", Name: "TV", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("s")},
		Session{ID: "session-schema", DeviceID: "device-schema", ProfileID: "profile-default", ExpiresAt: time.Now().Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO device_build_binding(device_id, surface, build_id, bound_at_ms) VALUES (?, ?, ?, 1)`
	for name, args := range map[string][]any{
		"web surface":    {"device-schema", "core-web-ui", "b"},
		"empty build":    {"device-schema", "tvos-viewer", ""},
		"padded build":   {"device-schema", "tvos-viewer", "b "},
		"overlong build": {"device-schema", "tvos-viewer", strings.Repeat("b", 257)},
		"unknown device": {"device-missing", "tvos-viewer", "b"},
	} {
		if _, err := st.catalogW.ExecContext(ctx, insert, args...); err == nil {
			t.Fatalf("%s: schema accepted %v", name, args)
		}
	}
}
