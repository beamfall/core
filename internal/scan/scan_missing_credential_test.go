package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type missingCredentialErr struct{ ref string }

func (e missingCredentialErr) Error() string             { return "credential " + e.ref + " is not configured" }
func (e missingCredentialErr) MissingCredential() string { return e.ref }

// failingIdentifier fails every AutoIdentify with err, counting calls.
type failingIdentifier struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *failingIdentifier) AutoIdentify(context.Context, store.MediaID, string, string, ParsedMedia) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureScanLogs(t *testing.T) *syncBuffer {
	t.Helper()
	logs := &syncBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

// TestScanReportsMissingCredentialOnce (CORE-1101): a Library scanned with no
// provider credential configured used to WARN "auto-identify failed" with the
// raw lstat error for every file. The missing credential is now one WARN per
// scan naming the credential and where to add it, the files still import,
// and the scan result and completion message carry it.
func TestScanReportsMissingCredentialOnce(t *testing.T) {
	// feature:scan-pipeline
	const ref = "bfplugin-test-core1101-api-key"
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		writeScanFile(t, root, fmt.Sprintf("core1101-title-%d.mp4", i), []byte(fmt.Sprintf("video bytes %d", i)))
	}
	st := storetest.Open(t)
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Films", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	identifier := &failingIdentifier{err: fmt.Errorf("identify: %w", missingCredentialErr{ref: ref})}
	logs := captureScanLogs(t)

	result, err := Scan(ctx, st, nil, nil, nil, nil, identifier, Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Files) != 3 || identifier.calls != 3 {
		t.Fatalf("imported %d files with %d identify calls, want 3 and 3", len(result.Files), identifier.calls)
	}
	if want := []string{ref}; !reflect.DeepEqual(result.MissingCredentials, want) {
		t.Fatalf("MissingCredentials = %v, want %v", result.MissingCredentials, want)
	}
	out := logs.String()
	if n := strings.Count(out, "provider credential not configured"); n != 1 {
		t.Fatalf("missing-credential WARN logged %d times, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "credential="+ref) || !strings.Contains(out, "secrets folder") {
		t.Fatalf("WARN does not name the credential and where to add it:\n%s", out)
	}
	if strings.Contains(out, "core1101-title") && strings.Contains(out, "auto-identify failed") {
		t.Fatalf("per-file auto-identify WARN still logged:\n%s", out)
	}
	msg := missingCredentialsMessage(result.MissingCredentials)
	if !strings.Contains(msg, ref) || !strings.HasPrefix(msg, "Scan complete") {
		t.Fatalf("completion message = %q", msg)
	}
	if got := missingCredentialsMessage(nil); got != "Scan complete" {
		t.Fatalf("clean completion message = %q, want unchanged", got)
	}
}

// TestScanStillLogsOtherIdentifyFailuresPerFile: only the missing-credential
// case is folded; any other identification error stays a per-file WARN.
func TestScanStillLogsOtherIdentifyFailuresPerFile(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "core1101-other-a.mp4", []byte("a bytes"))
	writeScanFile(t, root, "core1101-other-b.mp4", []byte("b bytes"))
	st := storetest.Open(t)
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Films", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	logs := captureScanLogs(t)
	result, err := Scan(ctx, st, nil, nil, nil, nil, &failingIdentifier{err: errors.New("provider timeout")}, Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.MissingCredentials) != 0 {
		t.Fatalf("MissingCredentials = %v, want none", result.MissingCredentials)
	}
	out := logs.String()
	for _, name := range []string{"core1101-other-a.mp4", "core1101-other-b.mp4"} {
		if !strings.Contains(out, name) {
			t.Fatalf("no per-file WARN for %s:\n%s", name, out)
		}
	}
}

// TestMissingCredentialsSurviveCheckpoint: a scan job yields every batch and
// resumes from its checkpoint, so the noted refs ride the checkpoint and a
// resumed run does not WARN again for a ref an earlier run reported.
func TestMissingCredentialsSurviveCheckpoint(t *testing.T) {
	data, err := marshalScanCheckpoint(Result{LastPath: "/m/a.mp4", MissingCredentials: []string{"a-ref", "b-ref"}}.checkpoint("scanning"))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := checkpointFromJob(data)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a-ref", "b-ref"}; !reflect.DeepEqual(checkpoint.MissingCredentials, want) {
		t.Fatalf("checkpoint MissingCredentials = %v, want %v", checkpoint.MissingCredentials, want)
	}
	legacy, err := checkpointFromJob([]byte(`{"version":1,"stage":"scanning","last_path":"/m/a.mp4","files_seen":1,"media_imported":1}`))
	if err != nil || legacy.MissingCredentials != nil {
		t.Fatalf("pre-CORE-1101 checkpoint = (%+v, %v)", legacy, err)
	}

	logs := captureScanLogs(t)
	state := &scanWalkState{result: Result{MissingCredentials: append([]string(nil), checkpoint.MissingCredentials...)}}
	state.noteMissingCredential("b-ref")
	if strings.Contains(logs.String(), "b-ref") {
		t.Fatalf("resumed scan re-reported a known ref:\n%s", logs.String())
	}
	state.noteMissingCredential("ab-ref")
	if want := []string{"a-ref", "ab-ref", "b-ref"}; !reflect.DeepEqual(state.result.MissingCredentials, want) {
		t.Fatalf("MissingCredentials = %v, want sorted %v", state.result.MissingCredentials, want)
	}
	if n := strings.Count(logs.String(), "credential=ab-ref"); n != 1 {
		t.Fatalf("new ref logged %d times, want once:\n%s", n, logs.String())
	}
}
