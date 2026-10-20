package moderation

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func tempStore(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "moderation", "feed.json")
}

func TestReportRoundTripAndPersists(t *testing.T) {
	path := tempStore(t)
	svc, err := Open(path, ContactInfo{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	scope := Scope{ProfileID: "p1", DeviceID: "d1"}
	if err := svc.Report(scope, ReportRequest{TargetType: "media", TargetID: "m1", Reason: "spam"}); err != nil {
		t.Fatalf("Report: %v", err)
	}
	// File is written 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm = %v, want 0600", perm)
	}
	// Reopen and confirm the report survived.
	svc2, err := Open(path, ContactInfo{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := len(svc2.state.Reports); got != 1 {
		t.Fatalf("reports after reopen = %d, want 1", got)
	}
	if svc2.state.Reports[0].ReporterProfileID != "p1" {
		t.Fatalf("reporter = %q, want p1", svc2.state.Reports[0].ReporterProfileID)
	}
}

func TestReportRejectsEmptyTarget(t *testing.T) {
	svc, _ := Open(tempStore(t), ContactInfo{})
	if err := svc.Report(Scope{ProfileID: "p1"}, ReportRequest{TargetType: "", TargetID: "m1"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty targetType err = %v, want ErrInvalidRequest", err)
	}
	if err := svc.Report(Scope{ProfileID: "p1"}, ReportRequest{TargetType: "media", TargetID: " "}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blank targetID err = %v, want ErrInvalidRequest", err)
	}
}

func TestBlockAddListIdempotentAndPerProfile(t *testing.T) {
	path := tempStore(t)
	svc, _ := Open(path, ContactInfo{})
	p1 := Scope{ProfileID: "p1", DeviceID: "d1"}
	p2 := Scope{ProfileID: "p2"}

	if err := svc.Block(p1, BlockRequest{TargetType: "media", TargetID: "m1"}); err != nil {
		t.Fatalf("Block: %v", err)
	}
	// Idempotent: same target again is a no-op success, no duplicate.
	if err := svc.Block(p1, BlockRequest{TargetType: "media", TargetID: "m1"}); err != nil {
		t.Fatalf("Block idempotent: %v", err)
	}
	if got := len(svc.Blocks(p1)); got != 1 {
		t.Fatalf("p1 blocks = %d, want 1 (idempotent)", got)
	}
	// p2 sees none of p1's blocks.
	if got := len(svc.Blocks(p2)); got != 0 {
		t.Fatalf("p2 blocks = %d, want 0 (per-Profile)", got)
	}
	// p2's own block is isolated.
	if err := svc.Block(p2, BlockRequest{TargetType: "media", TargetID: "m9"}); err != nil {
		t.Fatalf("Block p2: %v", err)
	}
	if got := len(svc.Blocks(p1)); got != 1 {
		t.Fatalf("p1 blocks after p2 block = %d, want 1", got)
	}
}

func withBounds(t *testing.T, reports, blocks int) {
	t.Helper()
	origReports, origBlocks, origFieldBytes, origStateBytes := maxReports, maxBlocks, maxModerationFieldBytes, maxModerationStateBytes
	maxReports, maxBlocks = reports, blocks
	t.Cleanup(func() {
		maxReports, maxBlocks = origReports, origBlocks
		maxModerationFieldBytes, maxModerationStateBytes = origFieldBytes, origStateBytes
	})
}

func TestBlocksNeverAgedOut(t *testing.T) {
	// Reports are bounded; blocks are not. Fill past maxReports and confirm a
	// pre-existing block still survives (no silent unblock). Bounds are lowered
	// so the file rewrites stay fast under -race.
	withBounds(t, 20, 10000)
	svc, _ := Open(tempStore(t), ContactInfo{})
	scope := Scope{ProfileID: "p1"}
	if err := svc.Block(scope, BlockRequest{TargetType: "media", TargetID: "keepme"}); err != nil {
		t.Fatalf("Block: %v", err)
	}
	for i := 0; i < maxReports+50; i++ {
		if err := svc.Report(scope, ReportRequest{TargetType: "media", TargetID: "m", Reason: "r"}); err != nil {
			t.Fatalf("Report %d: %v", i, err)
		}
	}
	if got := len(svc.state.Reports); got != maxReports {
		t.Fatalf("reports = %d, want bounded at %d", got, maxReports)
	}
	if got := len(svc.Blocks(scope)); got != 1 {
		t.Fatalf("blocks after many reports = %d, want 1 (never aged out)", got)
	}
}

func TestBlockLimitFailsLoudly(t *testing.T) {
	withBounds(t, 1000, 25)
	svc, _ := Open(tempStore(t), ContactInfo{})
	scope := Scope{ProfileID: "p1"}
	// Fill to the cap with distinct targets.
	for i := 0; i < maxBlocks; i++ {
		if err := svc.Block(scope, BlockRequest{TargetType: "media", TargetID: strconv.Itoa(i)}); err != nil {
			t.Fatalf("Block %d: %v", i, err)
		}
	}
	if err := svc.Block(scope, BlockRequest{TargetType: "media", TargetID: "one-too-many"}); !errors.Is(err, ErrBlockLimit) {
		t.Fatalf("over-cap block err = %v, want ErrBlockLimit", err)
	}
}

func TestOversizedFieldsRejected(t *testing.T) {
	withBounds(t, 1000, 10000)
	maxModerationFieldBytes = 8
	svc, _ := Open(tempStore(t), ContactInfo{})
	scope := Scope{ProfileID: "p1"}

	if err := svc.Report(scope, ReportRequest{TargetType: "media", TargetID: "m1", Reason: strings.Repeat("x", 9)}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized report err = %v, want ErrInvalidRequest", err)
	}
	if err := svc.Block(scope, BlockRequest{TargetType: "media", TargetID: strings.Repeat("x", 9)}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized block err = %v, want ErrInvalidRequest", err)
	}
}

func TestPersistedByteBudgetRejected(t *testing.T) {
	withBounds(t, 1000, 10000)
	maxModerationFieldBytes = 1024
	maxModerationStateBytes = 180
	svc, _ := Open(tempStore(t), ContactInfo{})
	scope := Scope{ProfileID: "p1"}

	if err := svc.Report(scope, ReportRequest{TargetType: "media", TargetID: "m1", Reason: strings.Repeat("x", 128)}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("over-budget report err = %v, want ErrInvalidRequest", err)
	}
	if got := len(svc.state.Reports); got != 0 {
		t.Fatalf("reports after rejected persist = %d, want 0", got)
	}
}

func TestCorruptFileIsError(t *testing.T) {
	path := tempStore(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, ContactInfo{}); err == nil {
		t.Fatal("Open(corrupt) = nil error, want error (no silent reset)")
	}
}

func TestContactReturnsConfigured(t *testing.T) {
	c := ContactInfo{Email: "abuse@example.test", URL: "https://example.test/mod", Note: "x"}
	svc, _ := Open(tempStore(t), c)
	if svc.Contact() != c {
		t.Fatalf("Contact = %+v, want %+v", svc.Contact(), c)
	}
}
