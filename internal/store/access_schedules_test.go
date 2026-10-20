package store

import (
	"context"
	"testing"
	"time"
)

// TestProfileAccessWindowsRoundTripAndValidate covers the access-schedules
// store config resolver: a managed Profile's windows
// round-trip with dedup + stable order, a non-managed target is refused, and a
// malformed window is rejected by validation. feature:access-schedules
func TestProfileAccessWindowsRoundTripAndValidate(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()

	// Empty by default — no schedule.
	windows, err := st.ProfileAccessWindows(ctx, "profile-managed")
	if err != nil {
		t.Fatalf("ProfileAccessWindows returned error: %v", err)
	}
	if len(windows) != 0 {
		t.Fatalf("default windows = %#v, want none", windows)
	}

	// Set with a duplicate and out-of-order entries; expect dedup + sort.
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []ProfileAccessWindow{
		{Weekday: 6, StartMinute: 0, EndMinute: 1440},
		{Weekday: 1, StartMinute: 540, EndMinute: 1020},
		{Weekday: 1, StartMinute: 540, EndMinute: 1020},
	}); err != nil {
		t.Fatalf("SetProfileAccessWindows returned error: %v", err)
	}
	windows, err = st.ProfileAccessWindows(ctx, "profile-managed")
	if err != nil {
		t.Fatalf("ProfileAccessWindows returned error: %v", err)
	}
	want := []ProfileAccessWindow{
		{Weekday: 1, StartMinute: 540, EndMinute: 1020},
		{Weekday: 6, StartMinute: 0, EndMinute: 1440},
	}
	if len(windows) != len(want) || windows[0] != want[0] || windows[1] != want[1] {
		t.Fatalf("windows = %#v, want %#v", windows, want)
	}

	// Full-replace with an empty slice clears the schedule.
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", nil); err != nil {
		t.Fatalf("clear SetProfileAccessWindows returned error: %v", err)
	}
	windows, err = st.ProfileAccessWindows(ctx, "profile-managed")
	if err != nil {
		t.Fatalf("ProfileAccessWindows returned error: %v", err)
	}
	if len(windows) != 0 {
		t.Fatalf("windows after clear = %#v, want none", windows)
	}

	// A non-managed (adult) target is refused — adult Profiles have no time gate.
	if err := st.SetProfileAccessWindows(ctx, "profile-default", []ProfileAccessWindow{
		{Weekday: 0, StartMinute: 0, EndMinute: 60},
	}); err == nil {
		t.Fatal("SetProfileAccessWindows accepted an adult profile")
	}

	// A malformed window (end <= start) is rejected by validation.
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []ProfileAccessWindow{
		{Weekday: 1, StartMinute: 600, EndMinute: 600},
	}); err == nil {
		t.Fatal("SetProfileAccessWindows accepted a zero-length window")
	}
}

// TestAllowedAtHalfOpenBoundary covers the enforcement predicate's boundary
// semantics: no windows is always-allowed; the interval is half-open
// [start, end); only the matching weekday counts. feature:access-schedules
func TestAllowedAtHalfOpenBoundary(t *testing.T) {
	// Monday 10:00 local (weekday 1, minute 600).
	at := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.Local)

	if !AllowedAt(nil, at) {
		t.Fatal("AllowedAt(nil) = false, want always-allowed default")
	}

	monday := func(start, end int) []ProfileAccessWindow {
		return []ProfileAccessWindow{{Weekday: 1, StartMinute: start, EndMinute: end}}
	}
	cases := []struct {
		name    string
		windows []ProfileAccessWindow
		want    bool
	}{
		{"start boundary is inclusive", monday(600, 660), true},
		{"end boundary is exclusive", monday(540, 600), false},
		{"strictly inside", monday(540, 660), true},
		{"wrong weekday", []ProfileAccessWindow{{Weekday: 2, StartMinute: 0, EndMinute: 1440}}, false},
	}
	for _, tc := range cases {
		if got := AllowedAt(tc.windows, at); got != tc.want {
			t.Fatalf("%s: AllowedAt = %v, want %v", tc.name, got, tc.want)
		}
	}
}
