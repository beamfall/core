package app

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// uploadTestParser reads "Title (Year) S01E02.ext" names; it stands in for a
// plugin's filename parser.
type uploadTestParser struct{}

var uploadTestName = regexp.MustCompile(`^(.+?) \((\d{4})\)(?: S(\d+)E(\d+))?\.\w+$`)

func (uploadTestParser) Parse(path string, _ scanpkg.ParseProbe) (scanpkg.ParsedMedia, bool) {
	m := uploadTestName.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return scanpkg.ParsedMedia{}, false
	}
	year, _ := strconv.Atoi(m[2])
	index, _ := strconv.Atoi(m[4])
	sequence := ""
	if m[3] != "" {
		sequence = strconv.Itoa(mustAtoi(m[3]))
	}
	return scanpkg.ParsedMedia{Title: m[1], Year: year, Sequence: sequence, Index: index}, true
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func TestMediaUploadPlannerProposesParsedAssignments(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}
	libraries, err := st.Libraries(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	pluginID := ""
	for _, library := range libraries {
		if library.ID == "library-local" && library.PluginID != nil {
			pluginID = *library.PluginID
		}
	}
	if err := st.RegisterEntityKindPolicies(ctx, []store.EntityKindPolicy{{Kind: "widget", FullTextSearchable: true}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{ID: "entity-harbor", LibraryID: "library-local", Kind: "widget", Title: "Harbor Lights"}); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(canonical, "Harbor Lights (2004)", "Harbor Lights (2004).mkv")
	if err := os.MkdirAll(filepath.Dir(existing), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var enqueued []store.Job
	planner := &mediaUploadPlanner{
		st:      st,
		parsers: map[string]scanpkg.FilenameParser{pluginID: uploadTestParser{}},
		enqueue: func(_ context.Context, job store.Job) (store.Job, error) {
			enqueued = append(enqueued, job)
			job.Status = store.JobStatusQueued
			return job, nil
		},
		now: func() time.Time { return time.Unix(0, 0).UTC() },
	}
	proposals, err := planner.ProposeMediaUploads(ctx, filter, "library-local", []store.MediaUpload{
		{ID: "u1", FileName: "Harbor Lights (2004).mkv"},
		{ID: "u2", FileName: "Unknown Thing (1999).mkv"},
		{ID: "u3", FileName: "notes.txt", RelativeDir: "Extras"},
	})
	if err != nil {
		t.Fatalf("ProposeMediaUploads: %v", err)
	}
	if len(proposals) != 3 {
		t.Fatalf("proposals = %+v", proposals)
	}
	matched := proposals[0]
	if matched.Target == nil || matched.Target.EntityID != "entity-harbor" || matched.Target.KindLabel != "widget" {
		t.Fatalf("matched target = %+v", matched.Target)
	}
	if matched.Destination != filepath.Dir(existing) || !matched.Conflict {
		t.Fatalf("matched destination = %q conflict=%v", matched.Destination, matched.Conflict)
	}
	if matched.Rescan == nil || matched.Rescan.Title != "Harbor Lights" {
		t.Fatalf("rescan = %+v", matched.Rescan)
	}
	unmatched := proposals[1]
	if unmatched.Target != nil || unmatched.Parsed == nil || unmatched.Destination != filepath.Join(canonical, "Unknown Thing (1999)") || unmatched.Conflict {
		t.Fatalf("unmatched = %+v", unmatched)
	}
	if proposals[2].Parsed != nil || proposals[2].Destination != filepath.Join(canonical, "Extras") {
		t.Fatalf("unparsed = %+v", proposals[2])
	}

	targets, err := planner.SearchMediaUploadTargets(ctx, filter, "library-local", "harb")
	if err != nil || len(targets) != 1 || targets[0].Destination != filepath.Join(canonical, "Harbor Lights") {
		t.Fatalf("targets = %+v, %v", targets, err)
	}
	if err := planner.MediaUploadCommitted(ctx, "library-local", canonical); err != nil || len(enqueued) != 1 {
		t.Fatalf("committed: %v, jobs %d", err, len(enqueued))
	}
	if _, err := planner.ProposeMediaUploads(ctx, store.LibraryFilter{}, "library-local", nil); err == nil {
		t.Fatal("invisible library proposed")
	}
}

func TestMediaUploadDestinationHelpers(t *testing.T) {
	if got := mediaUploadFolderName(" a/b\\c. "); got != "a-b-c" {
		t.Fatalf("folder name = %q", got)
	}
	sep := string(filepath.Separator)
	dirs := []string{sep + "r" + sep + "Show" + sep + "S1", sep + "r" + sep + "Show" + sep + "S2"}
	if got := commonMediaUploadDir(dirs); got != sep+"r"+sep+"Show" {
		t.Fatalf("common = %q", got)
	}
	got := proposeMediaUploadDestination(sep+"r", "", &server.MediaUploadParse{Title: "T", Year: 2000}, nil)
	if got != filepath.Join(sep+"r", "T (2000)") {
		t.Fatalf("destination = %q", got)
	}
}

func TestMediaUploadPlannerMatchesSameTitleByYear(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	const libraryID = "library-local"
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, libraryID, []string{root}); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterEntityKindPolicies(ctx, []store.EntityKindPolicy{{Kind: "widget", FullTextSearchable: true}}); err != nil {
		t.Fatal(err)
	}
	for _, year := range []int{1999, 2004} {
		id := store.EntityID("entity-harbor-" + strconv.Itoa(year))
		if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: libraryID, Kind: "widget", Title: "Harbor Lights"}); err != nil {
			t.Fatal(err)
		}
		member, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: libraryID, Path: filepath.Join(root, string(id), "part.mkv"),
			Title: "Harbor Lights", Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
			ContentFingerprint: string(id), Year: year,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: id, Name: "holds", ToMediaID: member.ID}); err != nil {
			t.Fatal(err)
		}
	}
	filter := store.LibraryFilter{AllLibraries: true}
	planner := &mediaUploadPlanner{st: st}
	library, err := planner.library(ctx, filter, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	target, err := planner.matchTarget(ctx, library, &server.MediaUploadParse{Title: "Harbor Lights", Year: 2004, Sequence: "1"})
	if err != nil || target == nil || target.EntityID != "entity-harbor-2004" || target.Year != 2004 {
		t.Fatalf("target = %+v, %v", target, err)
	}
}
