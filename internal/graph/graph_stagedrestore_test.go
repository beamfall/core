package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/restorestage"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type fakeRestoreStager struct {
	gotKey          string
	replaceIdentity bool
	marker          *restorestage.Marker
}

func (f *fakeRestoreStager) Inspect(_ context.Context, _, key string) (store.BackupManifestSummary, error) {
	f.gotKey = key
	if key != "good-key" {
		return store.BackupManifestSummary{}, restorestage.RefusalError("the backup could not be opened with this key")
	}
	return store.BackupManifestSummary{
		CreatedAt: "2026-11-14T00:00:00Z", Compatible: true, LibraryRootCount: 2,
		Databases: []store.BackupDatabaseSummary{{Name: "catalog", Database: "catalog", SchemaVersion: 110, SupportedSchemaVersion: 110}},
	}, nil
}

func (f *fakeRestoreStager) Stage(ctx context.Context, envelope, key string, replaceIdentity bool) (restorestage.Marker, error) {
	summary, err := f.Inspect(ctx, envelope, key)
	if err != nil {
		return restorestage.Marker{}, err
	}
	f.replaceIdentity = replaceIdentity
	f.marker = &restorestage.Marker{StagedAt: "2026-11-14T01:00:00Z", Envelope: envelope, ReplaceIdentity: replaceIdentity, Summary: summary}
	return *f.marker, nil
}

func (f *fakeRestoreStager) Cancel() (bool, error) {
	had := f.marker != nil
	f.marker = nil
	return had, nil
}

func (f *fakeRestoreStager) Status() (restorestage.Status, error) {
	return restorestage.Status{Staged: f.marker}, nil
}

// The staged-restore fields are Admin-Session gated, report refusals to the
// admin, never echo the backup key, and report an unwired stager as nothing
// staged.
func TestStagedRestoreFieldsStageInspectAndCancel(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	const (
		status  = `{"query":"query { stagedRestore { staged envelope replaceIdentity contents { compatible } } }"}`
		inspect = `{"query":"mutation($e: String!, $k: String!) { inspectBackup(envelope: $e, backupKey: $k) { compatible libraryRootCount databases { name schemaVersion supportedSchemaVersion } } }","variables":{"e":"/backups/a/set.bbk","k":%q}}`
		stage   = `{"query":"mutation($e: String!, $k: String!) { stageRestore(envelope: $e, backupKey: $k, replaceIdentity: true) { staged envelope replaceIdentity contents { createdAt } } }","variables":{"e":"/backups/a/set.bbk","k":"good-key"}}`
		cancel  = `{"query":"mutation { cancelStagedRestore }"}`
	)
	if _, errs := postLibraryTrash(t, handler, status); len(errs) == 0 || errs[0] != "admin session required" {
		t.Fatalf("stagedRestore without an Admin Session: errors = %q", errs)
	}
	openManagerAdminSession(t, st)

	var info StagedRestoreInfo
	data, errs := postLibraryTrash(t, handler, status)
	if len(errs) != 0 || json.Unmarshal(data["stagedRestore"], &info) != nil || info.Staged {
		t.Fatalf("stagedRestore with no stager = %s, errors %q", data["stagedRestore"], errs)
	}

	fake := &fakeRestoreStager{}
	handler.SetRestoreStager(fake)

	_, errs = postLibraryTrash(t, handler, strings.Replace(inspect, "%q", `"wrong-key"`, 1))
	if len(errs) == 0 || errs[0] != "the backup could not be opened with this key" {
		t.Fatalf("inspectBackup with a wrong key: errors = %q", errs)
	}
	if strings.Contains(strings.Join(errs, " "), "wrong-key") {
		t.Fatalf("refusal echoed the backup key: %q", errs)
	}

	var contents BackupContentsInfo
	data, errs = postLibraryTrash(t, handler, strings.Replace(inspect, "%q", `"good-key"`, 1))
	if len(errs) != 0 || json.Unmarshal(data["inspectBackup"], &contents) != nil || !contents.Compatible || contents.LibraryRootCount != 2 || len(contents.Databases) != 1 {
		t.Fatalf("inspectBackup = %s, errors %q", data["inspectBackup"], errs)
	}

	data, errs = postLibraryTrash(t, handler, stage)
	info = StagedRestoreInfo{}
	if len(errs) != 0 || json.Unmarshal(data["stageRestore"], &info) != nil || !info.Staged || !info.ReplaceIdentity || info.Contents == nil || !fake.replaceIdentity {
		t.Fatalf("stageRestore = %s, errors %q", data["stageRestore"], errs)
	}
	if strings.Contains(string(data["stageRestore"]), "good-key") {
		t.Fatalf("stageRestore echoed the backup key: %s", data["stageRestore"])
	}

	data, errs = postLibraryTrash(t, handler, status)
	info = StagedRestoreInfo{}
	if len(errs) != 0 || json.Unmarshal(data["stagedRestore"], &info) != nil || !info.Staged || info.Envelope != "/backups/a/set.bbk" {
		t.Fatalf("stagedRestore after staging = %s, errors %q", data["stagedRestore"], errs)
	}

	data, errs = postLibraryTrash(t, handler, cancel)
	if len(errs) != 0 || string(data["cancelStagedRestore"]) != "true" || fake.marker != nil {
		t.Fatalf("cancelStagedRestore = %s, errors %q", data["cancelStagedRestore"], errs)
	}
}
