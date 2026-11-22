package mediahttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
)

type fakeSubtitleSearcher struct {
	query    SubtitleSearchQuery
	searched int
	body     []byte
	err      error
}

func (f *fakeSubtitleSearcher) Status(context.Context) []SubtitleSearchProvider {
	return []SubtitleSearchProvider{{ID: "opensubtitles", Ready: true}}
}

func (f *fakeSubtitleSearcher) Search(_ context.Context, q SubtitleSearchQuery) ([]SubtitleSearchResult, error) {
	f.searched++
	f.query = q
	if f.err != nil {
		return nil, f.err
	}
	return []SubtitleSearchResult{{Provider: "opensubtitles", FileID: "42", Language: "en", HashMatch: true}}, nil
}

func (f *fakeSubtitleSearcher) Download(context.Context, string, string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.body, nil
}

const sampleDownloadedSRT = "1\n00:00:01,000 --> 00:00:02,000\nDownloaded\n"

func newSubtitleSearchFixture(t *testing.T) (*subtitleWriteFixture, *fakeSubtitleSearcher) {
	t.Helper()
	f := newSubtitleWriteFixture(t)
	searcher := &fakeSubtitleSearcher{body: []byte(sampleDownloadedSRT)}
	f.handler.SetSubtitleSearch(searcher)
	return f, searcher
}

func TestSubtitleSearchIsRefusedWhenNotEnabled(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	rec := httptest.NewRecorder()
	f.handler.SearchSubtitles(rec, f.post(t, "/api/media/subtitle-search", map[string]any{"mediaId": f.episodes[0]}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body)
	}
	status := httptest.NewRecorder()
	req := f.rawPost("/api/media/subtitle-search", "")
	req.Method = http.MethodGet
	f.handler.SubtitleSearchStatus(status, req)
	var got map[string]any
	if err := json.Unmarshal(status.Body.Bytes(), &got); err != nil || got["enabled"] != false {
		t.Fatalf("status body = %s (%v)", status.Body, err)
	}
}

func TestSubtitleSearchSendsHashAndGenericMetadata(t *testing.T) {
	f, searcher := newSubtitleSearchFixture(t)
	rec := httptest.NewRecorder()
	f.handler.SearchSubtitles(rec, f.post(t, "/api/media/subtitle-search", map[string]any{
		"mediaId": f.episodes[0], "languages": []string{"EN", "pt_br", "<bad>"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	if searcher.query.Title != "episode-one" || len(searcher.query.Languages) != 2 ||
		searcher.query.Languages[0] != "en" || searcher.query.Languages[1] != "pt-BR" {
		t.Fatalf("query = %+v", searcher.query)
	}
	var body struct {
		Results []SubtitleSearchResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Results) != 1 || !body.Results[0].HashMatch {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
}

func TestSubtitleSearchMapsMissingKeyToConflict(t *testing.T) {
	f, searcher := newSubtitleSearchFixture(t)
	searcher.err = ErrSubtitleSearchNoKey
	rec := httptest.NewRecorder()
	f.handler.SearchSubtitles(rec, f.post(t, "/api/media/subtitle-search", map[string]any{"mediaId": f.episodes[0]}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body)
	}
}

func TestSubtitleSearchRefusesManagedProfile(t *testing.T) {
	f, searcher := newSubtitleSearchFixture(t)
	ctx := context.Background()
	kid, err := f.store.CreateProfile(ctx, "Kid", store.ProfileManaged, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.EnrollDevice(ctx, store.Device{
		ID: "device-kid", Name: "Kid", Role: store.DeviceRoleViewer,
		SecretHash: store.HashDeviceSecret("kid-secret"),
	}, store.Session{ID: "session-kid", DeviceID: "device-kid", ProfileID: kid.ID}); err != nil {
		t.Fatal(err)
	}
	req := f.post(t, "/api/media/subtitle-search", map[string]any{"mediaId": f.episodes[0]})
	req.Header.Set("X-Beamfall-Session", "session-kid")
	req.Header.Set("X-Beamfall-Device", "device-kid")
	req.Header.Set("X-Beamfall-Device-Secret", "kid-secret")
	rec := httptest.NewRecorder()
	f.handler.SearchSubtitles(rec, req)
	if rec.Code != http.StatusForbidden || searcher.searched != 0 {
		t.Fatalf("status = %d searched = %d, want 403 and no provider call", rec.Code, searcher.searched)
	}
}

func TestSubtitleDownloadAddsANumberedSidecarWithoutOverwriting(t *testing.T) {
	f, _ := newSubtitleSearchFixture(t)
	ctx := context.Background()
	file, err := f.store.PrimaryMediaFile(ctx, f.episodes[0], store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(filepath.Dir(file.Path), "episode-one.en.srt")
	before, err := os.ReadFile(existing) // #nosec G304 -- fixture sidecar under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	f.handler.DownloadSubtitle(rec, f.post(t, "/api/media/subtitle-download", map[string]any{
		"mediaId": f.episodes[0], "provider": "opensubtitles", "fileId": "42", "language": "en",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	after, _ := os.ReadFile(existing) // #nosec G304 -- as above.
	if string(after) != string(before) {
		t.Fatal("existing sidecar was overwritten")
	}
	written, err := os.ReadFile(filepath.Join(filepath.Dir(file.Path), "episode-one.en.2.srt"))
	if err != nil || string(written) != sampleDownloadedSRT {
		t.Fatalf("downloaded sidecar = %q (%v)", written, err)
	}
	sidecars, err := f.store.MediaFileSidecarCaptions(ctx, file.ContentFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range sidecars {
		found = found || (s.RelPath == "episode-one.en.2.srt" && s.Language == "en")
	}
	if !found {
		t.Fatalf("sidecar rows = %+v", sidecars)
	}
	entries, err := f.store.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{AllLibraries: true}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	logged := false
	for _, e := range entries {
		logged = logged || (e.Action == store.ActivitySubtitleDownload && e.TargetID == string(f.episodes[0]))
	}
	if !logged {
		t.Fatalf("no %s activity row in %+v", store.ActivitySubtitleDownload, entries)
	}
}

func TestSubtitleDownloadRejectsBadInputAndProviderRefusal(t *testing.T) {
	f, searcher := newSubtitleSearchFixture(t)
	for _, body := range []map[string]any{
		{"mediaId": f.episodes[0], "provider": "opensubtitles", "fileId": "42", "language": "../x"},
		{"mediaId": f.episodes[0], "provider": "opensubtitles", "fileId": "4/2", "language": "en"},
		{"mediaId": f.episodes[0], "provider": "", "fileId": "42", "language": "en"},
	} {
		rec := httptest.NewRecorder()
		f.handler.DownloadSubtitle(rec, f.post(t, "/api/media/subtitle-download", body))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %v: status = %d, want 400", body, rec.Code)
		}
	}
	searcher.err = errors.Join(ErrSubtitleSearchNotSubtitle)
	rec := httptest.NewRecorder()
	f.handler.DownloadSubtitle(rec, f.post(t, "/api/media/subtitle-download", map[string]any{
		"mediaId": f.episodes[0], "provider": "opensubtitles", "fileId": "42", "language": "en",
	}))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestSidecarLanguageMatchesScanForm(t *testing.T) {
	for in, want := range map[string]string{"en": "en", "PT_br": "pt-BR", "zh-hant": "", "e": "", "en.srt": ""} {
		if got := sidecarLanguage(in); got != want {
			t.Errorf("sidecarLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
