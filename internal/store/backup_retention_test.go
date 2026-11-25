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

// TestBackupRetentionKeepsWeeklyHistoryUnderDailyBackups simulates a daily backup
// job. Weekly buckets are fixed calendar weeks, so once a set is kept as its
// week's survivor it stays kept as time passes.
func TestBackupRetentionKeepsWeeklyHistoryUnderDailyBackups(t *testing.T) {
	start := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	var sets []string
	for day := 0; day < 120; day++ {
		now := start.Add(time.Duration(day) * 24 * time.Hour)
		sets = append(sets, "beamfall-"+now.Format("20060102T150405.000000000Z"))
		drop := map[string]bool{}
		for _, name := range backupSetsToPrune(sets, now) {
			drop[name] = true
		}
		kept := sets[:0:0]
		for _, name := range sets {
			if !drop[name] {
				kept = append(kept, name)
			}
		}
		sets = kept
		if len(sets) > BackupRetentionCap {
			t.Fatalf("day %d: kept %d sets, cap is %d", day, len(sets), BackupRetentionCap)
		}
	}
	now := start.Add(119 * 24 * time.Hour)
	oldest, _ := backupSetTime(sets[0])
	if age := now.Sub(oldest); age < 28*24*time.Hour {
		t.Fatalf("oldest kept set is %v old after 120 daily runs; want weekly history past 28 days (7 daily plus weekly sets under the cap) (kept %d)", age, len(sets))
	}
}
