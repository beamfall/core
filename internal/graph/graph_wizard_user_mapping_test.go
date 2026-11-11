package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// mappingSource is a per-user source: each source user owns one watched file.
type mappingSource struct {
	users map[string][]migrate.ForeignItem
}

func (mappingSource) Categories() []string { return []string{migrate.CategoryWatchState} }
func (mappingSource) Items(context.Context) ([]migrate.ForeignItem, error) {
	return nil, nil
}
func (s mappingSource) ItemsBySourceUser(context.Context) (map[string][]migrate.ForeignItem, error) {
	return s.users, nil
}

type mappingResolver struct{ source migrate.Source }

func (r mappingResolver) Resolve(string, string, []string, string) (migrate.Source, bool) {
	return r.source, true
}

func newMappingHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "filmtv",
		Version:     "0.1.0",
		DisplayName: plugin.L("Film & TV"),
		MediaLabel:  plugin.L("Item"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		Importers: []plugin.ImporterSpec{{
			ID:          "fixture",
			DisplayName: plugin.L("Plex"),
			Categories:  []string{"watch-state"},
			Input:       "export-file",
			Conflict:    "additive",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithPlugins(st, registry)
	openManagerAdminSession(t, st)
	return handler, st
}

func createMappingProfile(t *testing.T, st *store.Store, name string) store.ProfileID {
	t.Helper()
	profile, err := st.CreateProfile(t.Context(), name, store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	return profile.ID
}

func runMappedImport(t *testing.T, handler *Handler, mappings string) (JobInfo, []graphError) {
	t.Helper()
	body := `{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!,$sourceUserMappings:[SourceUserMappingInput!]){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath,sourceUserMappings:$sourceUserMappings){ id } }","variables":{"pluginID":"filmtv","importerID":"fixture","sourcePath":"/exports/plex.db","sourceUserMappings":` + mappings + `}}`
	data, errs := post(t, handler, body, authorizeManager)
	var job JobInfo
	if len(errs) == 0 {
		mustUnmarshal(t, data["runImport"], &job)
	}
	return job, errs
}

func TestRunImportSourceUserMappingsReachHandlerPerProfile(t *testing.T) {
	handler, st := newMappingHandler(t)
	ctx := t.Context()
	profileA := createMappingProfile(t, st, "Mapped A")
	profileB := createMappingProfile(t, st, "Mapped B")
	seed := func(path, fp string) store.MediaID {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: "library-local", Path: path, Title: path, Container: "mkv",
			Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: fp,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	mediaA := seed("/media/films/A.mkv", "fp-map-a")
	mediaB := seed("/media/films/B.mkv", "fp-map-b")
	mediaC := seed("/media/films/C.mkv", "fp-map-c")
	watched := migrate.WatchState{Present: true, Watched: true, OccurredAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)}
	source := mappingSource{users: map[string][]migrate.ForeignItem{
		"u-a": {{Path: "/media/films/A.mkv", WatchState: watched}},
		"u-b": {{Path: "/media/films/B.mkv", WatchState: watched}},
		"u-c": {{Path: "/media/films/C.mkv", WatchState: watched}},
	}}

	mappings := `[{"sourceUserID":"u-a","profileID":"` + string(profileA) + `"},{"sourceUserID":"u-b","profileID":"` + string(profileB) + `"}]`
	job, errs := runMappedImport(t, handler, mappings)
	if len(errs) != 0 {
		t.Fatalf("runImport errors = %#v", errs)
	}

	run := func(now time.Time) store.Job {
		registry := jobs.NewRegistry(st, "test-worker")
		registry.Register(migrate.JobKind, migrate.Handler(st, mappingResolver{source: source}))
		if _, err := registry.RunOne(ctx, nil, now); err != nil {
			t.Fatalf("RunOne: %v", err)
		}
		done, err := st.Job(ctx, store.JobID(job.ID))
		if err != nil {
			t.Fatal(err)
		}
		return done
	}
	done := run(time.Now().UTC().Add(time.Minute))
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q: %s", done.Status, done.Error)
	}
	for _, want := range []string{"u-a=mapped(items=1)", "u-b=mapped(items=1)", "u-c=unmapped(items=1)", "skipped-unresolved-user=1"} {
		if !strings.Contains(done.Progress.Message, want) {
			t.Fatalf("message = %q, want %q", done.Progress.Message, want)
		}
	}
	expect := func(profile store.ProfileID, media store.MediaID, want bool) {
		t.Helper()
		_, ok, err := st.PlaybackProgressFor(ctx, profile, media)
		if err != nil || ok != want {
			t.Fatalf("progress(%s,%s) ok=%v err=%v, want %v", profile, media, ok, err, want)
		}
	}
	expect(profileA, mediaA, true)
	expect(profileB, mediaB, true)
	expect(profileA, mediaB, false)
	expect(profileB, mediaA, false)
	// The unmapped user never lands on the run-level (operator) Profile or a mapped one.
	for _, profile := range []store.ProfileID{"profile-default", "profile-manager", profileA, profileB} {
		expect(profile, mediaC, false)
	}

	// A rerun is additive and idempotent: the second job writes nothing new.
	again, errs := runMappedImport(t, handler, mappings)
	if len(errs) != 0 {
		t.Fatalf("rerun errors = %#v", errs)
	}
	if again.ID == job.ID {
		// the first job completed, so a rerun is a fresh job
		t.Fatalf("rerun reused completed job %s", job.ID)
	}
	job = again
	rerun := run(time.Now().UTC().Add(time.Hour))
	if rerun.Status != store.JobStatusCompleted {
		t.Fatalf("rerun status = %q: %s", rerun.Status, rerun.Error)
	}
	expect(profileA, mediaA, true)
	expect(profileB, mediaB, true)
	expect(profileA, mediaB, false)
	expect(profileB, mediaA, false)
}

func TestRunImportSourceUserMappingsValidation(t *testing.T) {
	handler, st := newMappingHandler(t)
	valid := createMappingProfile(t, st, "Valid")
	disabledID := createMappingProfile(t, st, "Disabled")
	disabled := true
	if _, err := st.UpdateProfileSettings(t.Context(), disabledID, store.ProfileSettings{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	p := string(valid)
	for name, tc := range map[string]struct{ mappings, want string }{
		"not a list":      {`{"u":"x"}`, "must be a list"},
		"entry not obj":   {`["u"]`, "must be a list"},
		"blank source":    {`[{"sourceUserID":" ","profileID":"` + p + `"}]`, "needs sourceUserID"},
		"missing profile": {`[{"sourceUserID":"u"}]`, "needs sourceUserID"},
		"duplicate":       {`[{"sourceUserID":"u","profileID":"` + p + `"},{"sourceUserID":"u","profileID":"` + p + `"}]`, "duplicate"},
		"unknown profile": {`[{"sourceUserID":"u","profileID":"profile-nope"}]`, "unknown profile"},
		"disabled":        {`[{"sourceUserID":"u","profileID":"` + string(disabledID) + `"}]`, "disabled"},
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := runMappedImport(t, handler, tc.mappings)
			if len(errs) == 0 || !strings.Contains(errs[0].Message, tc.want) {
				t.Fatalf("errors = %#v, want %q", errs, tc.want)
			}
		})
	}
	if jobsQueued, err := st.ListJobs(t.Context()); err != nil || len(jobsQueued) != 0 {
		t.Fatalf("invalid requests enqueued jobs=%d err=%v", len(jobsQueued), err)
	}
}

func TestRunImportSourceUserMappingsRequireAdmin(t *testing.T) {
	handler, st := newMappingHandler(t)
	profile := createMappingProfile(t, st, "Target")
	body := `{"query":"mutation R($pluginID:String!,$importerID:String!,$sourcePath:String!,$sourceUserMappings:[SourceUserMappingInput!]){ runImport(pluginID:$pluginID,importerID:$importerID,sourcePath:$sourcePath,sourceUserMappings:$sourceUserMappings){ id } }","variables":{"pluginID":"filmtv","importerID":"fixture","sourcePath":"/x","sourceUserMappings":[{"sourceUserID":"u","profileID":"` + string(profile) + `"}]}}`
	_, errs := post(t, handler, body, authorizeViewer)
	if len(errs) == 0 {
		t.Fatal("non-admin runImport with mappings succeeded, want refusal")
	}
}

func TestMigrationDedupeKeyIncludesCanonicalMapping(t *testing.T) {
	key := func(m map[string]string) string {
		return migrationDedupeKey("filmtv", "fixture", "", "", "/x", []string{"watch-state"}, false, m)
	}
	ab := map[string]string{"u-a": "p1", "u-b": "p2"}
	ba := map[string]string{"u-b": "p2", "u-a": "p1"}
	if key(ab) != key(ba) {
		t.Fatal("equivalent mappings produced different keys")
	}
	for name, other := range map[string]map[string]string{
		"changed profile": {"u-a": "p1", "u-b": "p3"},
		"swapped":         {"u-a": "p2", "u-b": "p1"},
		"extra pair":      {"u-a": "p1", "u-b": "p2", "u-c": "p1"},
		"fewer":           {"u-a": "p1"},
		"none":            nil,
	} {
		if key(ab) == key(other) {
			t.Fatalf("%s aliased the original mapping", name)
		}
	}
	// Legacy key shape is unchanged when no mapping is supplied.
	if want := strings.Join([]string{"filmtv", "fixture", "", "", "/x", "watch-state", "false"}, "\x1f"); key(nil) != want {
		t.Fatalf("legacy key = %q, want %q", key(nil), want)
	}
}

func TestRunImportSourceUserMappingsDedupeThroughAPI(t *testing.T) {
	handler, st := newMappingHandler(t)
	p1 := string(createMappingProfile(t, st, "One"))
	p2 := string(createMappingProfile(t, st, "Two"))
	pair := func(u, p string) string { return `{"sourceUserID":"` + u + `","profileID":"` + p + `"}` }
	enqueue := func(entries ...string) string {
		t.Helper()
		job, errs := runMappedImport(t, handler, "["+strings.Join(entries, ",")+"]")
		if len(errs) != 0 {
			t.Fatalf("runImport errors = %#v", errs)
		}
		return job.ID
	}
	first := enqueue(pair("u-a", p1), pair("u-b", p2))
	if reordered := enqueue(pair("u-b", p2), pair("u-a", p1)); reordered != first {
		t.Fatalf("reordered mapping job %s != %s", reordered, first)
	}
	if changed := enqueue(pair("u-a", p2), pair("u-b", p2)); changed == first {
		t.Fatal("changed target Profile aliased the active job")
	}
	if fewer := enqueue(pair("u-a", p1)); fewer == first {
		t.Fatal("dropped pair aliased the active job")
	}
}
