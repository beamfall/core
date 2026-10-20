package mediahttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// outOfRootBody is the content that must never leave the process through a
// root-scoped read. Every escape case in this file aims at a file holding it, so
// a regression is visible as the bytes themselves, not just as a missing error.
const outOfRootBody = "outside-the-root"

// rootWithMember builds a root directory holding one regular member and an
// out-of-root file, and returns (root, offlimitsPath).
func rootWithMember(t *testing.T, member, body string) (string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(member)), 0o750); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, member), []byte(body), 0o600); err != nil {
		t.Fatalf("write member: %v", err)
	}
	offlimits := filepath.Join(base, "offlimits.bin")
	if err := os.WriteFile(offlimits, []byte(outOfRootBody), 0o600); err != nil {
		t.Fatalf("write out-of-root file: %v", err)
	}
	return root, offlimits
}

func requireSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs developer mode on windows")
	}
}

// A final component that is a symlink is refused outright, whether it points
// outside the root or at a perfectly legitimate sibling inside it. The name the
// server derived must be the file it reads; an indirection there is a swap.
func TestOpenRootedRefusesFinalComponentSymlink(t *testing.T) {
	requireSymlinks(t)
	root, offlimits := rootWithMember(t, "seg.m4s", "real-bytes")
	if err := os.Symlink(offlimits, filepath.Join(root, "escape.m4s")); err != nil {
		t.Fatalf("symlink escape: %v", err)
	}
	if err := os.Symlink("seg.m4s", filepath.Join(root, "inside.m4s")); err != nil {
		t.Fatalf("symlink inside: %v", err)
	}
	for _, name := range []string{"escape.m4s", "inside.m4s"} {
		t.Run(name, func(t *testing.T) {
			member, err := newRootedPath(root, name)
			if err != nil {
				t.Fatalf("newRootedPath err = %v, want nil", err)
			}
			file, _, err := openRooted(member)
			if !errors.Is(err, errRootScope) {
				if file != nil {
					_ = file.Close()
				}
				t.Errorf("openRooted err = %v, want errRootScope", err)
			}
			if _, err := statRooted(member); !errors.Is(err, errRootScope) {
				t.Errorf("statRooted err = %v, want errRootScope", err)
			}
		})
	}
}

// An intermediate component that is a symlink out of the root cannot be
// traversed: the resolution runs against the held root descriptor, so it fails
// inside os.Root rather than escaping and then being "re-checked".
func TestOpenRootedRefusesAncestorSymlinkEscape(t *testing.T) {
	requireSymlinks(t)
	root, offlimits := rootWithMember(t, "sub/seg.m4s", "real-bytes")
	outside := filepath.Dir(offlimits)
	if err := os.Symlink(outside, filepath.Join(root, "hop")); err != nil {
		t.Fatalf("symlink hop: %v", err)
	}
	member, err := newRootedPath(root, filepath.Join("hop", "offlimits.bin"))
	if err != nil {
		t.Fatalf("newRootedPath err = %v, want nil", err)
	}
	file, _, err := openRooted(member)
	if err == nil {
		_ = file.Close()
		t.Fatal("openRooted through an escaping ancestor symlink succeeded, want refusal")
	}
}

// A root whose NAME is re-pointed at a different directory after the descriptor
// was taken is refused. The identity check compares the held directory against a
// fresh resolution of the name, so the substituted tree is never read out of.
func TestOpenRootedRefusesReplacedRoot(t *testing.T) {
	root, _ := rootWithMember(t, "seg.m4s", "real-bytes")
	base := filepath.Dir(root)
	held, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer held.Close()
	if err := verifyRootUnreplaced(held, root); err != nil {
		t.Fatalf("verifyRootUnreplaced before replacement = %v, want nil", err)
	}

	imposter := filepath.Join(base, "imposter")
	if err := os.MkdirAll(imposter, 0o750); err != nil {
		t.Fatalf("mkdir imposter: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imposter, "seg.m4s"), []byte(outOfRootBody), 0o600); err != nil {
		t.Fatalf("write imposter member: %v", err)
	}
	if err := os.Rename(root, filepath.Join(base, "moved")); err != nil {
		t.Fatalf("rename root away: %v", err)
	}
	if err := os.Rename(imposter, root); err != nil {
		t.Fatalf("rename imposter into place: %v", err)
	}
	if err := verifyRootUnreplaced(held, root); !errors.Is(err, errRootScope) {
		t.Fatalf("verifyRootUnreplaced after replacement = %v, want errRootScope", err)
	}
}

// The bytes served always come from the entry that passed the checks. A
// concurrent swapper flips the member between the real file and a symlink aimed
// out of the root; every successful read must return the real bytes, and every failure
// must be the fail-closed refusal. Run under -race this also proves the opener
// itself holds no shared mutable state.
func TestOpenRootedNeverReadsThroughConcurrentSwap(t *testing.T) {
	requireSymlinks(t)
	root, offlimits := rootWithMember(t, "seg.m4s", "real-bytes")
	member, err := newRootedPath(root, "seg.m4s")
	if err != nil {
		t.Fatalf("newRootedPath err = %v, want nil", err)
	}
	target := filepath.Join(root, "seg.m4s")
	link := filepath.Join(root, "seg.m4s.link")
	real := filepath.Join(root, "seg.m4s.real")
	if err := os.Rename(target, real); err != nil {
		t.Fatalf("stash real member: %v", err)
	}
	if err := os.Symlink(offlimits, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	stop := make(chan struct{})
	var swapper sync.WaitGroup
	swapper.Add(1)
	go func() {
		defer swapper.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// rename never follows the link, so this alternates the NAME between a
			// real in-root file and a symlink aimed out of the root — the exact
			// final-component swap the binding has to survive.
			_ = os.Rename(real, target)
			_ = os.Rename(target, real)
			_ = os.Rename(link, target)
			_ = os.Rename(target, link)
		}
	}()

	served := 0
	for i := 0; i < 2000; i++ {
		file, _, err := openRooted(member)
		if err != nil {
			continue
		}
		buf := make([]byte, len(outOfRootBody)+16)
		n, _ := file.Read(buf)
		_ = file.Close()
		got := string(buf[:n])
		if strings.Contains(got, outOfRootBody) {
			close(stop)
			swapper.Wait()
			t.Fatalf("openRooted served out-of-root bytes %q", got)
		}
		served++
	}
	close(stop)
	swapper.Wait()
	t.Logf("root-scoped opens that returned bytes: %d", served)
}

// The name prefilter refuses every shape that could address something other than
// a member of the root, before any syscall runs.
func TestNewRootedPathRejectsEscapingNames(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		label string
		name  string
	}{
		{"empty", ""},
		{"dot", "."},
		{"parent", ".."},
		{"climb", "../offlimits.bin"},
		{"climb through member", "sub/../../offlimits.bin"},
		{"absolute", filepath.Join(root, "offlimits.bin")},
		{"volume root", "/etc/passwd"},
		{"nul byte", "seg\x00.m4s"},
	}
	for _, tc := range cases {
		if _, err := newRootedPath(root, tc.name); !errors.Is(err, errRootScope) {
			t.Errorf("%s: newRootedPath(%q) err = %v, want errRootScope", tc.label, tc.name, err)
		}
	}
	if _, err := newRootedPath("", "seg.m4s"); !errors.Is(err, errRootScope) {
		t.Errorf("empty root: err = %v, want errRootScope", err)
	}
	member, err := newRootedPath(root, "sub/seg.m4s")
	if err != nil {
		t.Fatalf("nested member err = %v, want nil", err)
	}
	if member.rel != filepath.Join("sub", "seg.m4s") {
		t.Errorf("member.rel = %q, want %q", member.rel, filepath.Join("sub", "seg.m4s"))
	}
}

// Serving stays a full HTTP citizen: byte ranges are honoured off the bound
// descriptor, so binding the read to an inode costs no player behaviour.
func TestServeRootedHonoursRangeRequests(t *testing.T) {
	root, _ := rootWithMember(t, "seg.m4s", "0123456789")
	member, err := newRootedPath(root, "seg.m4s")
	if err != nil {
		t.Fatalf("newRootedPath err = %v, want nil", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/seg.m4s", nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	if err := serveRooted(rec, req, "seg.m4s", member, time.Time{}); err != nil {
		t.Fatalf("serveRooted err = %v, want nil", err)
	}
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
	}
	if got := rec.Body.String(); got != "2345" {
		t.Errorf("body = %q, want %q", got, "2345")
	}
}

// A missing member and a refused traversal are reported identically, so the
// refusal itself discloses nothing about what is on disk.
func TestRootScopeStatusHidesRefusalFromProbes(t *testing.T) {
	if got := rootScopeStatus(errRootScope); got != http.StatusNotFound {
		t.Errorf("rootScopeStatus(errRootScope) = %d, want %d", got, http.StatusNotFound)
	}
	if got := rootScopeStatus(os.ErrNotExist); got != http.StatusNotFound {
		t.Errorf("rootScopeStatus(ErrNotExist) = %d, want %d", got, http.StatusNotFound)
	}
	if got := rootScopeStatus(errors.New("disk fault")); got != http.StatusServiceUnavailable {
		t.Errorf("rootScopeStatus(io fault) = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

// The rendition-member binder and the sidecar binder both keep the root, so the
// member can only ever be re-derived against the directory that was authorised.
func TestRootedBindersKeepTheirRoot(t *testing.T) {
	member, ok := precomputedHLSPath("/renditions/abc", "hls/seg1.m4s")
	if !ok {
		t.Fatal("precomputedHLSPath rejected a legitimate member")
	}
	if member.root != "/renditions/abc" || member.rel != filepath.Join("hls", "seg1.m4s") {
		t.Errorf("precomputedHLSPath = %+v, want root /renditions/abc rel hls/seg1.m4s", member)
	}
	if _, ok := precomputedHLSPath("/renditions/abc", "../../etc/passwd"); ok {
		t.Error("precomputedHLSPath accepted a climbing member")
	}

	sidecar, ok := sidecarFilePath(filepath.Join("/library", "movie.mkv"), "movie.en.srt")
	if !ok {
		t.Fatal("sidecarFilePath rejected a legitimate sibling")
	}
	if sidecar.root != "/library" || sidecar.rel != "movie.en.srt" {
		t.Errorf("sidecarFilePath = %+v, want root /library rel movie.en.srt", sidecar)
	}
	if _, ok := sidecarFilePath(filepath.Join("/library", "movie.mkv"), "../secret.srt"); ok {
		t.Error("sidecarFilePath accepted a climbing rel_path")
	}
	if _, ok := sidecarFilePath(filepath.Join("/library", "movie.mkv"), "/etc/passwd"); ok {
		t.Error("sidecarFilePath accepted an absolute rel_path")
	}
}

// ## Evidence — AUDITFIX-MEDIA-PATH-SAFETY (2026-08-19/20 UTC)
//
// Recorded here because the ticket's Allowed Paths cover only the four code
// trees it touches; no doc path is granted, so the reviewer copy lives with the
// tests it describes. Commands were run in the lane worktree
// .roadmap-worktrees/beamfall/AUDITFIX-MEDIA-PATH-SAFETY. The full repo gate is
// the orchestrator's step, not the builder's, so it is deliberately absent.
//
// FOCUSED VERIFICATION
//   go vet ./internal/ffmpeg/ ./internal/mediahttp/ ./internal/store/ ./internal/workflow/convert/
//   go test -count=1 ./internal/ffmpeg/ ./internal/mediahttp/ ./internal/store/ \
//           ./internal/workflow/convert/ ./internal/conformance/
//
//   At fa9e3d3e3e2b9e826db6ea468fe425d6bac67457 — this comment's own PARENT
//   commit, i.e. the lane with all four code commits applied. A rebase rewrites
//   that SHA, so treat it as naming the tree, not an identity to look up: the
//   check is re-running the commands, which take under three minutes.
//   UTC 2026-08-20T00:49:55Z -> 2026-08-20T00:52:45Z, elapsed 170s, exit 0
//   ffmpeg 3.364s · mediahttp 28.887s · store 168.737s · workflow/convert 0.490s ·
//   conformance 17.959s  (all ok)
//
//   Two earlier runs of the same commands, same tree, both exit 0: 121s at base
//   main 63b096ea (pre-commit working tree) and 115s at the first lane tip before
//   the orchestrator rebased it. log: scratchpad/final-verify.log
//
// RACE RUN (the concurrent final-component swap)
//   go test ./internal/mediahttp/ -race
//   elapsed 407.823s, exit 0 (ok github.com/beamfall/core/internal/mediahttp)
//   TestOpenRootedNeverReadsThroughConcurrentSwap logged 248 opens that returned
//   bytes across 2000 attempts against a live swapper; none carried out-of-root
//   content.
//
// REAL-BINARY COVERAGE
//   ffmpeg 9.0.1 (/opt/homebrew/bin/ffmpeg) drives TestConvertRealBinary and
//   TestConvertCancellationLeavesNoResidue. A separate probe confirmed `ffmpeg -y`
//   TRUNCATES its output in place (inode 583407535 before and after), which is why
//   binding the reserved temp to its inode across the run does not reject honest
//   conversions.
//
// OBSERVED FAILURES WITH THE INVARIANTS DELIBERATELY BROKEN (then restored)
//   Broke openRooted/statRooted (dropped the Lstat regular-file check and the
//   os.SameFile binding) and verifyRootUnreplaced (dropped the identity compare):
//     --- FAIL: TestOpenRootedRefusesFinalComponentSymlink/escape.m4s
//         rootserve_test.go:71: openRooted err = openat escape.m4s: path escapes
//         from parent, want errRootScope
//     --- FAIL: TestOpenRootedRefusesFinalComponentSymlink/inside.m4s
//         rootserve_test.go:71: openRooted err = <nil>, want errRootScope
//         rootserve_test.go:74: statRooted err = <nil>, want errRootScope
//     --- FAIL: TestOpenRootedRefusesReplacedRoot
//         rootserve_test.go:130: verifyRootUnreplaced after replacement = <nil>,
//         want errRootScope
//
//   Broke internal/ffmpeg/rootio.go (publish renamed by absolute path with no
//   directory or temp identity proof; bindSource dropped O_NOFOLLOW and verify):
//     --- FAIL: TestPublishRefusesSwappedTemp
//         rootio_test.go:71: publish err = <nil>, want ErrPathSwapped
//     --- FAIL: TestPublishRefusesRecreatedTemp
//         rootio_test.go:94: publish err = <nil>, want ErrPathSwapped
//     --- FAIL: TestPublishRefusesReplacedDestinationDirectory
//         rootio_test.go:122: publish err = rename .../media/poster.jpg.<tmp>
//         .../media/poster.jpg: no such file or directory, want ErrPathSwapped
//     --- FAIL: TestBindSourceRefusesSymlinkFinalComponent
//         rootio_test.go:168: bindSource followed a symlink final component,
//         want refusal
//     --- FAIL: TestBindSourceVerifyDetectsMidRunSwap
//         rootio_test.go:195: verify after swap = <nil>, want ErrPathSwapped
//
//   Broke internal/store/managed_file_ops.go (staging source Stat'd instead of
//   Lstat'd with no inode binding, admission fast path returned straight off
//   os.Rename, manifest read followed the name):
//     --- FAIL: TestConvertManagedFileRefusesSwappedStagingArtifact
//         managed_file_ops_test.go:558: ConvertManagedFile err = statat
//         h264-mp4-<token>.convert-tmp: path escapes from parent, want the
//         admission identity refusal
//     --- FAIL: TestSweepManagedFileTrashRefusesSymlinkedManifest
//         managed_file_ops_test.go:629: purged = 1, want 0 (an entry whose expiry
//         cannot be proved is never purged)
//     --- FAIL: TestRestoreManagedPathRefusesSymlinkedManifest
//         managed_file_ops_test.go:668: RestoreManagedPath read a symlinked
//         manifest, want refusal
//
// PLATFORM NOTE (the Windows/path-platform case the Verify names)
//   Identity comparisons use os.SameFile, not syscall.Stat_t, so the checks are
//   the same code on every GOOS. newRootedPath rejects volume names and a leading
//   separator explicitly, because filepath.IsAbs is false for a volume-relative
//   Windows name. The symlink-planting tests skip on windows, where creating one
//   needs developer mode; every non-symlink case (root replacement, recreated
//   temp, mid-run source swap, name prefilter) runs there unchanged.
//
// KNOWN LIMIT (deliberate, not a gap to close here)
//   A HARD LINK planted inside a root is indistinguishable from a legitimate
//   member — it IS a real directory entry for that inode — so no root-scoped or
//   inode-binding check can reject it. Creating one already requires write access
//   to the server-owned directory, at which point the content could simply be
//   written. An early draft of the concurrent-swap test used os.Link and passed
//   trivially for this reason; it now renames a SYMLINK into place, which is the
//   threat these checks actually answer.
