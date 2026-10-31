package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/capabilities"
)

func refusalEntry(subject string, now time.Time, ttl time.Duration) capabilities.Invalidation {
	return capabilities.Invalidation{
		Subject:    subject,
		Decision:   capabilities.DecisionClientTooOld,
		ReasonCode: capabilities.ReasonRevokedBuild,
		RecordedAt: now,
		ExpiresAt:  now.Add(ttl),
	}
}

func TestRefusalFileJournalResolvesRelativeDirectory(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	journal := resolveCompatRefusalFileJournal("refusals")
	if journal.openErr != nil {
		t.Fatalf("resolve relative journal: %v", journal.openErr)
	}
	// The temp dir can be reached through a symlink (macOS /var), so compare
	// against the resolved working directory rather than the raw TempDir.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if want := filepath.Join(cwd, "refusals"); journal.dir != want {
		t.Fatalf("journal dir = %q, want %q", journal.dir, want)
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	ctx := context.Background()
	if err := journal.record(ctx, refusalEntry("session:relative", now, time.Hour)); err != nil {
		t.Fatalf("record: %v", err)
	}
	entries, err := journal.load(ctx, now)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 1 || entries[0].Subject != "session:relative" ||
		entries[0].Decision != capabilities.DecisionClientTooOld ||
		entries[0].ReasonCode != capabilities.ReasonRevokedBuild {
		t.Fatalf("entries = %+v, want the recorded refusal", entries)
	}
	info, err := os.Stat(filepath.Join(cwd, "refusals", compatRefusalFileName("session:relative")))
	if err != nil {
		t.Fatalf("stat refusal record: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("refusal record mode = %v, want 0600", perm)
	}
}

func TestRefusalFileJournalRejectsUncleanPathOnEveryOperation(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	for _, dir := range []string{"relative/refusals", "/tmp/../tmp/refusals"} {
		journal := newCompatRefusalFileJournal(dir)
		if journal.openErr == nil {
			t.Fatalf("newCompatRefusalFileJournal(%q) accepted a non-clean path", dir)
		}
		if _, err := journal.load(ctx, now); !errors.Is(err, journal.openErr) {
			t.Fatalf("load(%q) = %v, want openErr", dir, err)
		}
		if err := journal.record(ctx, refusalEntry("s", now, time.Hour)); !errors.Is(err, journal.openErr) {
			t.Fatalf("record(%q) = %v, want openErr", dir, err)
		}
		if err := journal.clear(ctx, "s"); !errors.Is(err, journal.openErr) {
			t.Fatalf("clear(%q) = %v, want openErr", dir, err)
		}
	}
}

func TestRefusalFileJournalHonorsCanceledContext(t *testing.T) {
	journal := newCompatRefusalFileJournal(filepath.Join(t.TempDir(), "refusals"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	now := time.Unix(1_700_000_000, 0).UTC()
	if _, err := journal.load(ctx, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("load = %v, want context.Canceled", err)
	}
	if err := journal.record(ctx, refusalEntry("s", now, time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("record = %v, want context.Canceled", err)
	}
	if err := journal.clear(ctx, "s"); !errors.Is(err, context.Canceled) {
		t.Fatalf("clear = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(journal.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled record created the journal directory: %v", err)
	}
}

func TestRefusalFileJournalLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	dir := filepath.Join(t.TempDir(), "refusals")
	journal := newCompatRefusalFileJournal(dir)

	// A journal that was never written is empty, not an error.
	if entries, err := journal.load(ctx, now); err != nil || len(entries) != 0 {
		t.Fatalf("load of absent journal = (%v, %v), want empty", entries, err)
	}
	if err := journal.record(ctx, capabilities.Invalidation{ExpiresAt: now.Add(time.Hour)}); err == nil {
		t.Fatal("record accepted an entry without a subject")
	}
	if err := journal.record(ctx, capabilities.Invalidation{Subject: "s"}); err == nil {
		t.Fatal("record accepted an entry without an expiry")
	}
	if err := journal.clear(ctx, ""); err == nil {
		t.Fatal("clear accepted an empty subject")
	}

	if err := journal.record(ctx, refusalEntry("session:live", now, time.Hour)); err != nil {
		t.Fatalf("record live: %v", err)
	}
	if err := journal.record(ctx, refusalEntry("session:short", now, time.Minute)); err != nil {
		t.Fatalf("record short: %v", err)
	}
	// Unrelated directory content must be ignored rather than parsed.
	if err := os.Mkdir(filepath.Join(dir, "nested.json"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".refusal-123.tmp"), []byte("{torn"), 0o600); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	later := now.Add(10 * time.Minute)
	entries, err := journal.load(ctx, later)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 1 || entries[0].Subject != "session:live" {
		t.Fatalf("entries = %+v, want only the live refusal", entries)
	}
	// Loading past expiry rotates the expired record out of the journal.
	if _, err := os.Stat(filepath.Join(dir, compatRefusalFileName("session:short"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired record still present after load: %v", err)
	}

	// Re-recording the same subject replaces its single file in place.
	if err := journal.record(ctx, refusalEntry("session:live", later, 2*time.Hour)); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	entries, err = journal.load(ctx, later)
	if err != nil || len(entries) != 1 || !entries[0].ExpiresAt.Equal(later.Add(2*time.Hour)) {
		t.Fatalf("after re-record entries = (%+v, %v), want one refreshed refusal", entries, err)
	}

	if err := journal.clear(ctx, "session:live"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := journal.clear(ctx, "session:live"); err != nil {
		t.Fatalf("clear of already-cleared subject = %v, want nil", err)
	}
	if entries, err := journal.load(ctx, later); err != nil || len(entries) != 0 {
		t.Fatalf("load after clear = (%+v, %v), want empty", entries, err)
	}
}

func TestRefusalFileJournalLoadFailsClosedOnCorruptRecords(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	valid := refusalEntry("session:valid", now, time.Hour)
	tests := []struct {
		name  string
		write func(t *testing.T, journal *compatRefusalFileJournal)
		want  string
	}{
		{
			name: "undecodable",
			write: func(t *testing.T, journal *compatRefusalFileJournal) {
				writeRefusalFile(t, journal.dir, compatRefusalFileName("s"), "{not json")
			},
			want: "decode refusal record",
		},
		{
			name: "wrong version",
			write: func(t *testing.T, journal *compatRefusalFileJournal) {
				writeRefusalFile(t, journal.dir, compatRefusalFileName("s"),
					`{"version":2,"subject":"s","expiresAt":"2030-01-01T00:00:00Z"}`)
			},
			want: "invalid refusal record",
		},
		{
			name: "subject renamed",
			write: func(t *testing.T, journal *compatRefusalFileJournal) {
				if err := journal.record(ctx, valid); err != nil {
					t.Fatalf("record: %v", err)
				}
				if err := os.Rename(
					filepath.Join(journal.dir, compatRefusalFileName(valid.Subject)),
					filepath.Join(journal.dir, compatRefusalFileName("other")),
				); err != nil {
					t.Fatalf("rename: %v", err)
				}
			},
			want: "does not match file",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			journal := newCompatRefusalFileJournal(filepath.Join(t.TempDir(), "refusals"))
			if err := os.MkdirAll(journal.dir, 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			tc.write(t, journal)
			entries, err := journal.load(ctx, now)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("load = (%+v, %v), want error containing %q", entries, err, tc.want)
			}
		})
	}
}

func TestRefusalFileJournalReportsUnusableDirectory(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	writeRefusalFile(t, filepath.Dir(blocker), filepath.Base(blocker), "file")

	// The journal path names a regular file: reads cannot open it as a root.
	asFile := newCompatRefusalFileJournal(blocker)
	if _, err := asFile.load(ctx, now); err == nil || !strings.Contains(err.Error(), "read refusal journal") {
		t.Fatalf("load of file-backed journal = %v, want read refusal journal error", err)
	}
	// The journal path sits under a regular file: the directory cannot be made.
	under := newCompatRefusalFileJournal(filepath.Join(blocker, "refusals"))
	if err := under.record(ctx, refusalEntry("s", now, time.Hour)); err == nil ||
		!strings.Contains(err.Error(), "create refusal journal") {
		t.Fatalf("record under a file = %v, want create refusal journal error", err)
	}
}

func TestWithCompatRefusalJournalDirTrimsConfiguredPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "refusals")
	srv := &Server{}
	WithCompatRefusalJournalDir("  " + dir + "\n")(srv)
	if srv.compatRefusalDir != dir {
		t.Fatalf("compatRefusalDir = %q, want trimmed %q", srv.compatRefusalDir, dir)
	}
	WithCompatRefusalJournalDir("   ")(srv)
	if srv.compatRefusalDir != "" {
		t.Fatalf("blank option kept compatRefusalDir = %q, want empty (journal disabled)", srv.compatRefusalDir)
	}
}

func writeRefusalFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
