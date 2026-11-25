package store

import (
	"testing"
	"time"
)

func retentionSets(now time.Time, agesDays ...int) []string {
	var out []string
	for _, d := range agesDays {
		out = append(out, "beamfall-"+now.Add(-time.Duration(d)*24*time.Hour).UTC().Format("20060102T150405.000000000Z"))
	}
	return out
}

func TestBackupSetsToPruneBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		ages []int
		want int
	}{
		{"empty", nil, 0},
		{"under daily floor keeps all", []int{0, 1, 2, 3, 4, 5, 6}, 0},
		{"eighth set sharing a bucket with ninth: second pruned", []int{0, 1, 2, 3, 4, 5, 6, 8, 9}, 1},
		{"older than 90 days pruned", []int{0, 1, 2, 3, 4, 5, 6, 91, 120}, 2},
		{"exactly 90 days kept", []int{0, 1, 2, 3, 4, 5, 6, 90}, 0},
		{"hard cap 12", []int{0, 1, 2, 3, 4, 5, 6, 10, 17, 24, 31, 38, 45, 52, 59}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := backupSetsToPrune(retentionSets(now, tc.ages...), now)
			if len(got) != tc.want {
				t.Fatalf("pruned %d (%v), want %d", len(got), got, tc.want)
			}
		})
	}
}

func TestBackupSetsToPruneKeepsNewestSevenRegardlessOfAge(t *testing.T) {
	now := time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)
	var ages []int
	for i := range 10 {
		ages = append(ages, 200+i)
	}
	sets := retentionSets(now, ages...)
	pruned := backupSetsToPrune(sets, now)
	if len(pruned) != 3 {
		t.Fatalf("pruned %d, want 3", len(pruned))
	}
	newest := sets[0]
	for _, p := range pruned {
		if p == newest {
			t.Fatal("pruned the newest set")
		}
	}
}

func TestBackupSetsToPruneIgnoresForeignNames(t *testing.T) {
	if got := backupSetsToPrune([]string{"notes", "beamfall-bogus"}, time.Now()); len(got) != 0 {
		t.Fatalf("foreign names selected: %v", got)
	}
}
