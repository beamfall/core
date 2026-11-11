package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeMissingCredential mirrors scrape.CredentialNotConfiguredError's method
// set, which scan matches through missingCredentialError.
type fakeMissingCredential struct{ ref string }

func (e fakeMissingCredential) Error() string                { return "credential " + e.ref + " not configured" }
func (e fakeMissingCredential) MissingCredentialRef() string { return e.ref }
func (e fakeMissingCredential) ConfigureHint() string {
	return "add it in Console > Plugins (" + e.ref + ")"
}

// failingIdentifier fails every call: the first with an ordinary error, every
// later one with a (wrapped) missing credential.
type failingIdentifier struct {
	mu    sync.Mutex
	calls int
}

func (f *failingIdentifier) AutoIdentify(context.Context, store.MediaID, string, string, ParsedMedia) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return errors.New("provider returned 500")
	}
	return fmt.Errorf("scrape: resolving credential: %w", fakeMissingCredential{ref: "bfplugin-tmdb"})
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	var mu sync.Mutex
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestScanReportsMissingCredentialOncePerScan(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.mp4", "b.mp4", "c.mp4", "d.mp4"} {
		writeScanFile(t, root, name, []byte("video bytes "+name))
	}
	st := storetest.Open(t)
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Films", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	identifier := &failingIdentifier{}
	logs := captureSlog(t)

	res, err := Scan(ctx, st, nil, nil, nil, nil, identifier, Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Files) != 4 || identifier.calls != 4 {
		t.Fatalf("imported %d files with %d identify calls, want 4/4", len(res.Files), identifier.calls)
	}
	out := logs.String()
	if n := strings.Count(out, "credential not configured"); n != 1 {
		t.Fatalf("missing-credential notices = %d, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "Console > Plugins") {
		t.Fatalf("notice lacks the configure hint:\n%s", out)
	}
	if n := strings.Count(out, "auto-identify failed"); n != 1 || !strings.Contains(out, "provider returned 500") {
		t.Fatalf("per-file failures = %d, want only the non-credential one:\n%s", n, out)
	}

	// Dedupe state is per scan: a fresh walk state notices the ref again.
	logs.Reset()
	fresh := &scanWalkState{}
	fresh.reportAutoIdentifyError("x.mp4", "m1", fakeMissingCredential{ref: "bfplugin-tmdb"})
	if strings.Count(logs.String(), "credential not configured") != 1 {
		t.Fatalf("a new scan must report the missing credential again:\n%s", logs.String())
	}
}

func TestCredentialNoticesDedupePerRef(t *testing.T) {
	var n credentialNotices
	if !n.first("a") || n.first("a") || !n.first("b") || n.first("b") {
		t.Fatal("credentialNotices must report each ref exactly once")
	}
}
