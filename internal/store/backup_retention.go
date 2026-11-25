package store

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Backup retention policy (ADR-0192 §6): keep the newest BackupDailyKeep sets,
// plus the newest set in each further 7-day age bucket out to BackupWeeklyHorizon,
// hard-capped at BackupRetentionCap sets overall. The cap drops the oldest
// weeklies first, so the newest BackupDailyKeep sets are never pruned.
const (
	BackupDailyKeep     = 7
	BackupWeeklyHorizon = 90 * 24 * time.Hour
	BackupRetentionCap  = 12
	backupWeeklyBucket  = 7 * 24 * time.Hour
)

// backupSetTime parses the UTC creation time embedded in a published set name.
func backupSetTime(name string) (time.Time, bool) {
	if !publishedBackupSetName(name) {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102T150405.000000000Z", name[len("beamfall-"):])
	return t, err == nil
}

// backupSetsToPrune returns the published set names that fall outside the
// retention policy at now. names may be in any order; foreign names are never
// selected.
func backupSetsToPrune(names []string, now time.Time) []string {
	var sets []string
	for _, name := range names {
		if _, ok := backupSetTime(name); ok {
			sets = append(sets, name)
		}
	}
	// The embedded timestamp is lexicographically sortable: descending is newest-first.
	sort.Sort(sort.Reverse(sort.StringSlice(sets)))
	keep := make(map[string]bool, BackupRetentionCap)
	var kept []string
	seenBucket := map[int64]bool{}
	for i, name := range sets {
		created, _ := backupSetTime(name)
		if i < BackupDailyKeep {
			keep[name] = true
			kept = append(kept, name)
			continue
		}
		age := now.Sub(created)
		if age > BackupWeeklyHorizon {
			continue
		}
		bucket := int64(max(age, 0) / backupWeeklyBucket)
		if bucket == 0 {
			continue // the first week is covered by the daily floor
		}
		if !seenBucket[bucket] {
			seenBucket[bucket] = true
			keep[name] = true
			kept = append(kept, name)
		}
	}
	for _, name := range kept[min(BackupRetentionCap, len(kept)):] {
		delete(keep, name)
	}
	var prune []string
	for _, name := range sets {
		if !keep[name] {
			prune = append(prune, name)
		}
	}
	return prune
}

// PruneBackupsByPolicy removes published sets outside the ADR-0192 retention
// policy. It must only run after a backup succeeded, so a failed backup never
// shrinks the recoverable history. destDir is guarded like PruneBackups.
func (s *Store) PruneBackupsByPolicy(ctx context.Context, destDir string, now time.Time) (int, error) {
	canonicalDest, err := s.guardBackupDest(destDir)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(canonicalDest)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	pruned := 0
	for _, name := range backupSetsToPrune(names, now.UTC()) {
		if ctx.Err() != nil {
			return pruned, ctx.Err()
		}
		if err := os.RemoveAll(filepath.Join(canonicalDest, name)); err != nil {
			return pruned, err
		}
		pruned++
	}
	return pruned, nil
}
