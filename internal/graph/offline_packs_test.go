package graph

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type offlinePackTestAttester struct{}

func (offlinePackTestAttester) VerifyDeviceKey(context.Context, string, []byte, []byte) error {
	return nil
}

func newOfflinePackHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	handler, st, _, _ := newOfflinePackHandlerWith(t, nil)
	return handler, st
}

// newOfflinePackHandlerWith is newOfflinePackHandler with the offline pack
// service's store optionally wrapped (wrap may be nil), returning the job
// registry and service too so a test can replace the build job handler.
func newOfflinePackHandlerWith(t *testing.T, wrap func(*store.Store) offlinepack.Store) (*Handler, *store.Store, *jobs.Registry, *offlinepack.Service) {
	t.Helper()
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "media-alpha.bin")
	if err := os.WriteFile(path, []byte("bytes-media-alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(t.Context(), "media-alpha", path); err != nil {
		t.Fatal(err)
	}
	visible, err := st.VisibleMediaBatch(t.Context(), []store.MediaID{"media-alpha"}, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForMedia(t.Context(), "media-alpha", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || len(files) == 0 {
		t.Fatalf("MediaFilesForMedia: %v", err)
	}
	if _, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID: visible["media-alpha"].LibraryID, Path: path, Title: visible["media-alpha"].Title,
		Container: "flac", Codec: "flac", Class: store.MediaClassAudio,
		ContentFingerprint: files[0].ContentFingerprint,
	}); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := offlinepack.NewAuthority("spki-sha256(test)", "offline-pack-test", privateKey, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var svcStore offlinepack.Store = st
	if wrap != nil {
		svcStore = wrap(st)
	}
	svc := offlinepack.NewService(svcStore, authority, time.Now)
	svc.SetPackRoot(t.TempDir())
	svc.SetDeviceKeyAttestationVerifier(offlinePackTestAttester{})
	deviceKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RegisterDeviceKey(t.Context(), "device-tv", deviceKey.PublicKey().Bytes(), []byte("attested")); err != nil {
		t.Fatal(err)
	}
	handler.SetOfflinePacks(svc)
	registry := jobs.NewRegistry(st, "offline-pack-graph-test")
	registry.Register(offlinepack.BuildJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		_, err := svc.HandleBuildJob(ctx, job.Payload)
		return jobs.Result{}, err
	})
	handler.SetJobRunner(registry)
	return handler, st, registry, svc
}

// TestCreateAndRevokeOfflinePackMutation exercises the createOfflinePack and
// revokeOfflinePack mutations and the offlinePacks query end-to-end through the
// GraphQL surface (ADR-0098 area 6).
func TestCreateAndRevokeOfflinePackMutation(t *testing.T) {
	handler, _ := newOfflinePackHandler(t)

	createBody := `{"query":"mutation { createOfflinePack { id targetKind state itemCount manifestToken } }","variables":{"targetKind":"media","targetRef":"media-alpha"}}`
	data, errs := post(t, handler, createBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("createOfflinePack errors = %#v", errs)
	}
	var created struct {
		ID            string `json:"id"`
		TargetKind    string `json:"targetKind"`
		State         string `json:"state"`
		ItemCount     int    `json:"itemCount"`
		ManifestToken string `json:"manifestToken"`
	}
	if err := json.Unmarshal(data["createOfflinePack"], &created); err != nil {
		t.Fatalf("decode createOfflinePack: %v", err)
	}
	if created.State != "ready" || created.ItemCount != 1 || created.ManifestToken == "" {
		t.Fatalf("created = %+v, want ready/1-item/token", created)
	}

	listBody := `{"query":"query { offlinePacks { id state } }"}`
	listData, errs := post(t, handler, listBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("offlinePacks errors = %#v", errs)
	}
	var packs []struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(listData["offlinePacks"], &packs); err != nil {
		t.Fatalf("decode offlinePacks: %v", err)
	}
	if len(packs) != 1 || packs[0].ID != created.ID {
		t.Fatalf("offlinePacks = %+v, want the created pack", packs)
	}

	revokeBody := `{"query":"mutation R($id:String!){ revokeOfflinePack(id:$id){ id state } }","variables":{"id":"` + created.ID + `"}}`
	revData, errs := post(t, handler, revokeBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("revokeOfflinePack errors = %#v", errs)
	}
	var revoked struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(revData["revokeOfflinePack"], &revoked); err != nil {
		t.Fatalf("decode revokeOfflinePack: %v", err)
	}
	if revoked.State != "revoked" {
		t.Fatalf("state = %q, want revoked", revoked.State)
	}
}

// TestDeleteMyPackTokensMutation clears the caller's pack-token ledger rows.
func TestDeleteMyPackTokensMutation(t *testing.T) {
	handler, st := newOfflinePackHandler(t)

	// Seed a pack token ledger row for the viewer's profile.
	if err := st.RecordPackToken(t.Context(), "session-default", "profile-default", "device-tv", "offline-pack-manifest", "offline-pack-x", false, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("RecordPackToken: %v", err)
	}

	deleteBody := `{"query":"mutation { deleteMyPackTokens }"}`
	data, errs := post(t, handler, deleteBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("deleteMyPackTokens errors = %#v", errs)
	}
	var ok bool
	if err := json.Unmarshal(data["deleteMyPackTokens"], &ok); err != nil || !ok {
		t.Fatalf("deleteMyPackTokens = %v (err %v), want true", ok, err)
	}
	tokens, err := st.MyPackTokens(t.Context(), "profile-default", time.Now())
	if err != nil || len(tokens) != 0 {
		t.Fatalf("after delete tokens = %+v, err %v; want none", tokens, err)
	}
}

func TestSyncOfflinePackProgressMutation(t *testing.T) {
	handler, _ := newOfflinePackHandler(t)

	createBody := `{"query":"mutation { createOfflinePack { id targetKind state itemCount } }","variables":{"targetKind":"media","targetRef":"media-alpha"}}`
	data, errs := post(t, handler, createBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("createOfflinePack errors = %#v", errs)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["createOfflinePack"], &created); err != nil {
		t.Fatalf("decode createOfflinePack: %v", err)
	}

	syncBody := `{"query":"mutation Sync($id:String!,$bytesReady:Int!,$bytesTotal:Int!){ syncOfflinePackProgress(id:$id,bytesReady:$bytesReady,bytesTotal:$bytesTotal){ id bytesReady bytesTotal manifestToken } }","variables":{"id":"` + created.ID + `","bytesReady":4096,"bytesTotal":8192}}`
	syncData, errs := post(t, handler, syncBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("syncOfflinePackProgress errors = %#v", errs)
	}
	var synced struct {
		ID            string `json:"id"`
		BytesReady    int64  `json:"bytesReady"`
		BytesTotal    int64  `json:"bytesTotal"`
		ManifestToken string `json:"manifestToken"`
	}
	if err := json.Unmarshal(syncData["syncOfflinePackProgress"], &synced); err != nil {
		t.Fatalf("decode syncOfflinePackProgress: %v", err)
	}
	if synced.ID != created.ID || synced.BytesReady != 4096 || synced.BytesTotal != 8192 || synced.ManifestToken == "" {
		t.Fatalf("synced = %+v, want updated progress and renewed token for %s", synced, created.ID)
	}
}

// TestCreateOfflinePackWithQualityQueuesBackgroundBuild covers the quality
// path: the mutation returns a building pack at once, a repeat request returns
// the same pack ID without a second job, and the worker seals it under that ID.
func TestCreateOfflinePackWithQualityQueuesBackgroundBuild(t *testing.T) {
	handler, st := newOfflinePackHandler(t)
	createBody := `{"query":"mutation { createOfflinePack { id targetKind state } }","variables":{"targetKind":"media","targetRef":"media-alpha","quality":"medium"}}`
	create := func() (id, state string) {
		t.Helper()
		data, errs := post(t, handler, createBody, authorizeViewer)
		if len(errs) != 0 {
			t.Fatalf("createOfflinePack errors = %#v", errs)
		}
		var created struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}
		if err := json.Unmarshal(data["createOfflinePack"], &created); err != nil {
			t.Fatalf("decode createOfflinePack: %v", err)
		}
		return created.ID, created.State
	}
	firstID, state := create()
	if state != "building" || !offlinepack.ValidPackID(store.OfflinePackID(firstID)) {
		t.Fatalf("first create = %q/%q, want a building pack with a valid ID", firstID, state)
	}
	secondID, _ := create()
	if secondID != firstID {
		t.Fatalf("repeat create ID = %q, want %q", secondID, firstID)
	}
	jobs, err := st.ListJobs(t.Context(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Class != store.JobClassBackground {
		t.Fatalf("queued jobs = %+v, want one background build", jobs)
	}
	if _, err := handler.jobRunner.RunOne(t.Context(), []store.JobClass{store.JobClassBackground}, time.Now().UTC()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	listData, errs := post(t, handler, `{"query":"query { offlinePacks { id state } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("offlinePacks errors = %#v", errs)
	}
	var packs []struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(listData["offlinePacks"], &packs); err != nil {
		t.Fatalf("decode offlinePacks: %v", err)
	}
	if len(packs) != 1 || packs[0].ID != firstID || packs[0].State != "ready" {
		t.Fatalf("offlinePacks = %+v, want the queued pack ready under ID %q", packs, firstID)
	}
}

func TestCreateOfflinePackBuildsPerSelectedVersion(t *testing.T) {
	handler, st := newOfflinePackHandler(t)
	ids := map[string]bool{}
	for _, version := range []string{"Director's Cut", "Theatrical", "Theatrical"} {
		body, err := json.Marshal(map[string]any{
			"query": "mutation { createOfflinePack { id } }",
			"variables": map[string]any{
				"targetKind": "media", "targetRef": "media-alpha", "quality": "low",
				"selectedVersions": map[string]string{"media-alpha": version},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		data, errs := post(t, handler, string(body), authorizeViewer)
		if len(errs) != 0 {
			t.Fatalf("createOfflinePack errors = %#v", errs)
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data["createOfflinePack"], &created); err != nil {
			t.Fatal(err)
		}
		ids[created.ID] = true
	}
	jobs, err := st.ListJobs(t.Context(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || len(jobs) != 2 {
		t.Fatalf("pack IDs = %v, queued jobs = %d; want one build per selected version", ids, len(jobs))
	}
}

func TestSelectedVersionsKeyIsCanonical(t *testing.T) {
	a := selectedVersionsKey(map[store.MediaID]string{"m1": "A", "m2": "B"})
	b := selectedVersionsKey(map[store.MediaID]string{"m2": "B", "m1": "A"})
	if a != b || a == selectedVersionsKey(map[store.MediaID]string{"m1": "A,m2=B"}) || selectedVersionsKey(nil) != "" {
		t.Fatalf("selectedVersionsKey not canonical or collides: %q %q", a, b)
	}
}

func TestCreateOfflinePackRejectsUnknownQuality(t *testing.T) {
	handler, st := newOfflinePackHandler(t)
	body := `{"query":"mutation { createOfflinePack { id } }","variables":{"targetKind":"media","targetRef":"media-alpha","quality":"ultra"}}`
	_, errs := post(t, handler, body, authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "quality must be") {
		t.Fatalf("errors = %#v, want a quality error", errs)
	}
	jobs, err := st.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("jobs = %+v, want none queued", jobs)
	}
}

// makeOfflinePackFixtureLargeVideo turns the fixture into a video whose file
// reports one byte over the 4 GiB per-item bound. The file is extended
// sparsely, so no 4 GiB of data is written.
func makeOfflinePackFixtureLargeVideo(t *testing.T, st *store.Store) {
	t.Helper()
	filter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	visible, err := st.VisibleMediaBatch(t.Context(), []store.MediaID{"media-alpha"}, filter)
	if err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForMedia(t.Context(), "media-alpha", filter)
	if err != nil || len(files) == 0 {
		t.Fatalf("MediaFilesForMedia: %v", err)
	}
	if _, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID: visible["media-alpha"].LibraryID, Path: files[0].Path, Title: visible["media-alpha"].Title,
		Container: "mkv", Codec: "hevc", Class: store.MediaClassVideo,
		ContentFingerprint: files[0].ContentFingerprint,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(files[0].Path, int64(offlinepack.MaxItemSize)+1); err != nil {
		t.Fatal(err)
	}
}

// TestCreateOfflinePackRefusesOriginalOverTheItemBound covers CORE-1745: an
// Original-quality request for a video item over 4 GiB is refused with an
// INVALID error naming the limit, before any job is queued.
func TestCreateOfflinePackRefusesOriginalOverTheItemBound(t *testing.T) {
	for _, variables := range []string{
		`{"targetKind":"media","targetRef":"media-alpha","quality":"original"}`,
		`{"targetKind":"media","targetRef":"media-alpha"}`,
	} {
		handler, st := newOfflinePackHandler(t)
		makeOfflinePackFixtureLargeVideo(t, st)
		body := `{"query":"mutation { createOfflinePack { id } }","variables":` + variables + `}`
		_, errs := post(t, handler, body, authorizeViewer)
		if len(errs) != 1 || errs[0].Message != "original quality is limited to 4 GiB per item; choose high, medium or low quality" || errs[0].Extensions.Code != ErrorCodeInvalid {
			t.Fatalf("variables %s: errors = %#v, want the INVALID 4 GiB original limit", variables, errs)
		}
		queued, err := st.ListJobs(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(queued) != 0 {
			t.Fatalf("variables %s: jobs = %+v, want none queued", variables, queued)
		}
	}
}

// TestCreateOfflinePackQueuesTranscodedQualityOverTheItemBound: the limit is
// on sealed bytes only, so a transcoded quality is still queued.
func TestCreateOfflinePackQueuesTranscodedQualityOverTheItemBound(t *testing.T) {
	handler, st := newOfflinePackHandler(t)
	makeOfflinePackFixtureLargeVideo(t, st)
	body := `{"query":"mutation { createOfflinePack { id state } }","variables":{"targetKind":"media","targetRef":"media-alpha","quality":"medium"}}`
	if _, errs := post(t, handler, body, authorizeViewer); len(errs) != 0 {
		t.Fatalf("createOfflinePack errors = %#v", errs)
	}
	queued, err := st.ListJobs(t.Context(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued jobs = %d, want 1", len(queued))
	}
}
