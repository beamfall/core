package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Internal ADR-0174 ownership-succession interleavings: the convertStepHook
// hands the lease over at an EXACT fence point — journal, admit, import,
// trash — which wall-clock scheduling cannot express deterministically. The
// hook runs inside ConvertManagedFile, so each case exercises the precise
// window between two steps of the replacement.

type internalConvertFixture struct {
	st     *Store
	root   string
	src    string
	dst    string
	filter LibraryFilter
}

func newInternalConvertFixture(t *testing.T) internalConvertFixture {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(canonicalRoot, "clip.mkv")
	if err := os.WriteFile(src, []byte("clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               src,
		Title:              "clip",
		Container:          "mp4",
		Codec:              "h264",
		Class:              MediaClassVideo,
		ContentFingerprint: HashMediaFilePath(src),
		OSHash:             "oshash-internal",
	}); err != nil {
		t.Fatal(err)
	}
	return internalConvertFixture{
		st:     st,
		root:   canonicalRoot,
		src:    src,
		dst:    filepath.Join(canonicalRoot, "clip-h264-mp4.mp4"),
		filter: LibraryFilter{AllLibraries: true},
	}
}

func (fx internalConvertFixture) claimLease(t *testing.T, worker string) ConvertOwnerLease {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	id := JobID(t.Name() + "-job")
	if _, err := fx.st.EnqueueJob(ctx, Job{ID: id, Kind: "convert-managed-file", Priority: 1 << 20, CreatedAt: now}); err != nil {
		if !strings.Contains(err.Error(), "active job already exists") {
			t.Fatal(err)
		}
	}
	claimed, err := fx.st.ClaimNextJob(ctx, worker, nil, now.Add(2*JobLeaseTTL))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != id {
		t.Fatalf("claimed %q, want %q", claimed.ID, id)
	}
	return ConvertOwnerLease{JobID: claimed.ID, Owner: claimed.LeaseOwner}
}

// handOverLease expires the running lease and reclaims it for the successor,
// exactly what a recovery sweep plus a fresh claim produce.
func (fx internalConvertFixture) handOverLease(t *testing.T, id JobID, worker string) ConvertOwnerLease {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if count, err := fx.st.RecoverExpiredJobLeases(ctx, now.Add(3*JobLeaseTTL)); err != nil || count < 1 {
		t.Fatalf("RecoverExpiredJobLeases count=%d err=%v", count, err)
	}
	claimed, err := fx.st.ClaimNextJob(ctx, worker, nil, now.Add(3*JobLeaseTTL+time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != id {
		t.Fatalf("handover claimed %q, want %q", claimed.ID, id)
	}
	return ConvertOwnerLease{JobID: claimed.ID, Owner: claimed.LeaseOwner}
}

type internalConverter struct {
	calls int
}

func (c *internalConverter) ConvertFile(_ context.Context, src, dst, _ string) (string, string, MediaClass, error) {
	c.calls++
	data, err := os.ReadFile(src) // #nosec G304 -- test reads a file this test just created under its own t.TempDir()
	if err != nil {
		return "", "", "", err
	}
	if err := os.WriteFile(dst, append([]byte("converted:"), data...), 0o600); err != nil { // #nosec G703 -- dst is a path this test constructed under its own t.TempDir()
		return "", "", "", err
	}
	return "mp4", "h264", MediaClassVideo, nil
}

// TestConvertSuccessionFenceInterleavings hands the lease to a successor at
// each fence point and asserts the stale owner is rejected with the step's
// exact residue guarantees, then that the successor converges the replacement.
func TestConvertSuccessionFenceInterleavings(t *testing.T) {
	cases := []struct {
		step string
		// verify inspects the state the fenced-out owner left behind.
		verify func(t *testing.T, fx internalConvertFixture)
		// successorReencodes reports whether the successor's convergence run
		// re-encodes (true) or rolls the committed import forward (false).
		successorReencodes bool
	}{
		{
			step: "journal",
			verify: func(t *testing.T, fx internalConvertFixture) {
				if _, err := os.Stat(fx.dst); !os.IsNotExist(err) {
					t.Fatalf("fenced-out owner admitted output before the journal fence: %v", err)
				}
			},
			successorReencodes: true,
		},
		{
			step: "admit",
			verify: func(t *testing.T, fx internalConvertFixture) {
				if _, err := os.Stat(fx.dst); !os.IsNotExist(err) {
					t.Fatalf("fenced-out owner admitted output past the admit fence: %v", err)
				}
			},
			successorReencodes: true,
		},
		{
			step: "import",
			verify: func(t *testing.T, fx internalConvertFixture) {
				// The admission landed, the import fence caught the handover,
				// and the owner removed its own uncataloged residue.
				if _, err := os.Stat(fx.dst); !os.IsNotExist(err) {
					t.Fatalf("fenced-out owner left uncataloged residue: %v", err)
				}
				if _, _, err := fx.st.mediaForManagedFilePath(context.Background(), "library-local", fx.dst); err == nil {
					t.Fatal("fenced-out owner cataloged its output past the import fence")
				}
			},
			successorReencodes: true,
		},
		{
			step: "trash",
			verify: func(t *testing.T, fx internalConvertFixture) {
				// Import committed; the original must be untouched for the
				// successor to roll forward.
				if _, _, err := fx.st.mediaForManagedFilePath(context.Background(), "library-local", fx.dst); err != nil {
					t.Fatalf("import missing before the trash fence: %v", err)
				}
				if _, err := os.Stat(fx.src); err != nil {
					t.Fatalf("original disturbed despite the trash fence: %v", err)
				}
			},
			successorReencodes: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.step, func(t *testing.T) {
			fx := newInternalConvertFixture(t)
			lease := fx.claimLease(t, "worker-a")
			var successor ConvertOwnerLease
			fx.st.convertStepHook = func(step string) {
				if step != tc.step {
					return
				}
				fx.st.convertStepHook = nil
				successor = fx.handOverLease(t, lease.JobID, "worker-b")
			}
			conv := &internalConverter{}
			_, err := fx.st.ConvertManagedFile(context.Background(), "library-local", fx.src, "h264-mp4", conv, fx.filter, lease, time.Now().UTC())
			if !errors.Is(err, ErrJobLeaseLost) {
				t.Fatalf("stale owner at %q err = %v, want ErrJobLeaseLost", tc.step, err)
			}
			if _, err := os.Stat(fx.src); err != nil && tc.step != "trash" {
				t.Fatalf("original lost at %q: %v", tc.step, err)
			}
			tc.verify(t, fx)

			// The successor converges the replacement under its own lease.
			successorConv := &internalConverter{}
			result, err := fx.st.ConvertManagedFile(context.Background(), "library-local", fx.src, "h264-mp4", successorConv, fx.filter, successor, time.Now().UTC())
			if err != nil {
				t.Fatalf("successor convergence at %q: %v", tc.step, err)
			}
			wantCalls := 0
			if tc.successorReencodes {
				wantCalls = 1
			}
			if successorConv.calls != wantCalls {
				t.Fatalf("successor converter calls at %q = %d, want %d", tc.step, successorConv.calls, wantCalls)
			}
			if result.Converted.ID == "" {
				t.Fatalf("successor convergence at %q reports no Media", tc.step)
			}
			if _, err := os.Stat(fx.src); !os.IsNotExist(err) {
				t.Fatalf("original still live after convergence at %q: %v", tc.step, err)
			}
			if _, err := os.Stat(fx.dst); err != nil {
				t.Fatalf("output missing after convergence at %q: %v", tc.step, err)
			}
		})
	}
}

// TestConvertAdmissionCollisionSelfHeals covers the window between the journal
// write and the admission: a stale straggler's file lands at dst first. The
// live generation's no-replace admission collides, recovery proves the file is
// this job's own journal-named, uncataloged residue, removes it, and the
// admission retries once — the live generation completes instead of failing
// terminally to a straggler it already fenced out.
func TestConvertAdmissionCollisionSelfHeals(t *testing.T) {
	fx := newInternalConvertFixture(t)
	lease := fx.claimLease(t, "worker-a")
	fx.st.convertStepHook = func(step string) {
		if step != "admit" {
			return
		}
		fx.st.convertStepHook = nil
		if err := os.WriteFile(fx.dst, []byte("stale-straggler-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	conv := &internalConverter{}
	result, err := fx.st.ConvertManagedFile(context.Background(), "library-local", fx.src, "h264-mp4", conv, fx.filter, lease, time.Now().UTC())
	if err != nil {
		t.Fatalf("live generation failed to a straggler collision: %v", err)
	}
	if conv.calls != 1 {
		t.Fatalf("converter calls = %d, want 1", conv.calls)
	}
	if result.Converted.ID == "" || result.Original.TrashID == "" {
		t.Fatalf("collision self-heal did not complete the replacement: %#v", result)
	}
	data, err := os.ReadFile(fx.dst)
	if err != nil || !strings.HasPrefix(string(data), "converted:") {
		t.Fatalf("final bytes = %q err=%v, want the live generation's output", data, err)
	}
}

// TestAdmitStagedFileNoReplaceLinkFastPath proves the same-filesystem
// admission links the staged inode (no byte copy) and consumes staging.
func TestAdmitStagedFileNoReplaceLinkFastPath(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staged.convert-tmp")
	if err := os.WriteFile(staging, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	stagedInfo, err := os.Stat(staging)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.mp4")
	if err := admitStagedFileNoReplace(staging, dst); err != nil {
		t.Fatalf("admit: %v", err)
	}
	landed, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(stagedInfo, landed) {
		t.Fatal("same-filesystem admission copied instead of linking the staged inode")
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging not consumed: %v", err)
	}
}

// TestAdmitStagedFileNoReplaceRefusesExisting pins the no-replace invariant the
// succession contract rides on: an existing destination is NEVER overwritten.
// Deliberately breaking this (replace-capable admission) makes the assertion
// fail with the destination's bytes swapped.
func TestAdmitStagedFileNoReplaceRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staged.convert-tmp")
	if err := os.WriteFile(staging, []byte("stale-generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(dst, []byte("successor-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := admitStagedFileNoReplace(staging, dst)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("admission over an existing destination err = %v, want EEXIST", err)
	}
	data, readErr := os.ReadFile(dst) // #nosec G304 -- test reads a file this test just created under its own t.TempDir()
	if readErr != nil || string(data) != "successor-bytes" {
		t.Fatalf("destination bytes = %q err=%v, want the successor's bytes intact", data, readErr)
	}
}
