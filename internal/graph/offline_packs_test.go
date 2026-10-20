package graph

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
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
	svc := offlinepack.NewService(st, authority, time.Now)
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
	return handler, st
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
