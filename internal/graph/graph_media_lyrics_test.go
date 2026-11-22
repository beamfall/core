package graph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type mediaLyricsResponse struct {
	Data struct {
		MediaLyrics MediaLyricsInfo `json:"mediaLyrics"`
	} `json:"data"`
	Errors []graphError `json:"errors"`
}

func queryMediaLyrics(t *testing.T, handler *Handler, mediaID string) mediaLyricsResponse {
	t.Helper()
	body := graphPost(t, handler, authorizeViewer,
		`{"query":"query($mediaID:String!){ mediaLyrics(mediaID:$mediaID){ synced source lines { startMs text } } }","variables":{"mediaID":"`+mediaID+`"}}`)
	var resp mediaLyricsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return resp
}

func importLyricsMedia(t *testing.T, st *store.Store, path string, class store.MediaClass) store.Media {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              filepath.Base(path),
		Class:              class,
		Container:          strings.TrimPrefix(filepath.Ext(path), "."),
		Codec:              "flac",
		ContentFingerprint: "fp-" + filepath.Base(path),
	})
	if err != nil {
		t.Fatal(err)
	}
	return media
}

// CORE-1638: mediaLyrics prefers a synced .lrc sidecar, falls back to the
// plugin's lyrics field, and answers no lines for non-audio items.
func TestMediaLyricsResolvesLocalSources(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)
	dir := t.TempDir()

	synced := importLyricsMedia(t, st, filepath.Join(dir, "song.flac"), store.MediaClassAudio)
	if err := os.WriteFile(filepath.Join(dir, "song.lrc"), []byte("[00:01.00]One\n[00:03.50]Two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := queryMediaLyrics(t, handler, string(synced.ID))
	got := resp.Data.MediaLyrics
	if len(resp.Errors) != 0 || !got.Synced || got.Source != "sidecar-lrc" || len(got.Lines) != 2 ||
		got.Lines[1].StartMs == nil || *got.Lines[1].StartMs != 3500 || got.Lines[1].Text != "Two" {
		t.Fatalf("synced sidecar = %+v %v", got, resp.Errors)
	}

	plain := importLyricsMedia(t, st, filepath.Join(dir, "plain.flac"), store.MediaClassAudio)
	if err := st.UpsertMediaFields(ctx, plain.ID, []store.MediaField{{Name: "lyrics", Type: store.EntityFieldText, Text: "Verse\nline"}}); err != nil {
		t.Fatal(err)
	}
	resp = queryMediaLyrics(t, handler, string(plain.ID))
	got = resp.Data.MediaLyrics
	if len(resp.Errors) != 0 || got.Synced || got.Source != "metadata" || len(got.Lines) != 2 || got.Lines[0].StartMs != nil {
		t.Fatalf("field lyrics = %+v %v", got, resp.Errors)
	}

	none := importLyricsMedia(t, st, filepath.Join(dir, "none.flac"), store.MediaClassAudio)
	resp = queryMediaLyrics(t, handler, string(none.ID))
	if got := resp.Data.MediaLyrics; len(resp.Errors) != 0 || got.Source != "" || got.Lines == nil || len(got.Lines) != 0 {
		t.Fatalf("no lyrics = %+v %v", got, resp.Errors)
	}

	video := importLyricsMedia(t, st, filepath.Join(dir, "clip.mkv"), store.MediaClassVideo)
	if err := os.WriteFile(filepath.Join(dir, "clip.lrc"), []byte("[00:01.00]ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp = queryMediaLyrics(t, handler, string(video.ID))
	if got := resp.Data.MediaLyrics; len(resp.Errors) != 0 || got.Source != "" || len(got.Lines) != 0 {
		t.Fatalf("video = %+v %v", got, resp.Errors)
	}
}

func TestMediaLyricsUnknownMedia(t *testing.T) {
	st := storetest.Open(t)
	resp := queryMediaLyrics(t, newTestHandlerWithStore(st), "media-missing")
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "unknown media" || len(resp.Data.MediaLyrics.Lines) != 0 {
		t.Fatalf("unknown media = %+v", resp)
	}
}
