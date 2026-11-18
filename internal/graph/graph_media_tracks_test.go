package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// CORE-2442: mediaTracks lists Versions and their tracks before playback,
// with the ids playbackInfo uses, from stored inventory only.
func TestMediaTracksListsVersionsAndTracks(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)

	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/tracks.mkv",
		Title:              "Tracks",
		Container:          "mkv",
		Codec:              "hevc",
		ContentFingerprint: "fp-tracks",
		VideoWidthHint:     3840,
		VideoHeightHint:    1600,
		Streams: []store.MediaFileStream{
			{Index: 0, Type: "video", Codec: "hevc", ColorTransfer: "smpte2084"},
			{Index: 1, Type: "audio", Codec: "flac", Language: "eng", Channels: 2},
			{Index: 2, Type: "audio", Codec: "ac3", Language: "fra", Channels: 6, Default: true},
			{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng", Forced: true},
		},
		Sidecars: []store.MediaFileSidecarCaption{
			{RelPath: "tracks.de.srt", Kind: store.SidecarKindCaption, Format: "srt", Language: "deu", SDH: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaFileVersion(ctx, media.ID, store.MediaFile{
		Path:               "/media/tracks - 720p.mp4",
		ContentFingerprint: "fp-tracks-720",
		VersionLabel:       "720p",
	}, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "h264"},
		{Index: 1, Type: "audio", Codec: "aac", Language: "eng", Channels: 2},
	}); err != nil {
		t.Fatal(err)
	}

	body := graphPost(t, handler, authorizeViewer,
		`{"query":"query($mediaID:String!){ mediaTracks(mediaID:$mediaID){ versions { id label videoCodec hdr resolution audioTracks { streamIndex codec language channels default } captionTracks { id format streamIndex language forced sdh } } } }","variables":{"mediaID":"`+string(media.ID)+`"}}`)
	var resp struct {
		Data struct {
			MediaTracks MediaTracksInfo `json:"mediaTracks"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	versions := resp.Data.MediaTracks.Versions
	if len(versions) != 2 {
		t.Fatalf("versions = %#v", versions)
	}
	first, second := versions[0], versions[1]
	// CORE-2485: only the probed Version's frame size is stored.
	if first.Resolution != "4K" || second.Resolution != "" {
		t.Fatalf("resolutions = %q, %q; want 4K and unknown", first.Resolution, second.Resolution)
	}
	if first.Label != "" || first.VideoCodec != "hevc" || first.HDR != "hdr10" || second.Label != "720p" || second.VideoCodec != "h264" {
		t.Fatalf("version facts = %#v / %#v", first, second)
	}
	if first.ID == "" || first.ID == second.ID || !strings.HasPrefix(first.ID, "v") {
		t.Fatalf("version ids = %q, %q", first.ID, second.ID)
	}
	if len(first.AudioTracks) != 2 ||
		first.AudioTracks[0].StreamIndex != 1 || first.AudioTracks[0].Default ||
		first.AudioTracks[1].StreamIndex != 2 || !first.AudioTracks[1].Default ||
		first.AudioTracks[1].Channels != 6 || first.AudioTracks[1].Language != "fra" {
		t.Fatalf("first audio = %#v", first.AudioTracks)
	}
	for _, track := range first.AudioTracks {
		if track.Selected {
			t.Fatalf("a listed audio track is never selected: %#v", track)
		}
	}
	captions := first.CaptionTracks
	if len(captions) != 2 {
		t.Fatalf("first captions = %#v", captions)
	}
	if captions[0].ID != "muxed-main-3" || captions[0].StreamIndex == nil || *captions[0].StreamIndex != 3 || !captions[0].Forced || captions[0].URL != "" {
		t.Fatalf("muxed caption = %#v", captions[0])
	}
	if captions[1].ID != "sidecar-main-0" || captions[1].StreamIndex != nil || captions[1].Language != "deu" || !captions[1].SDH {
		t.Fatalf("sidecar caption = %#v", captions[1])
	}
	if len(second.AudioTracks) != 1 || !second.AudioTracks[0].Default || len(second.CaptionTracks) != 0 {
		t.Fatalf("second tracks = %#v", second)
	}
	// The ids are the ones playbackInfo reports for the same Versions.
	listed, err := playback.ListTracks(ctx, st, media, mustMediaFiles(t, st, media.ID))
	if err != nil || len(listed) != 2 || listed[1].ID != second.ID {
		t.Fatalf("ListTracks = %#v, %v", listed, err)
	}
}

func TestMediaTracksUnknownMedia(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	body := graphPost(t, handler, authorizeViewer,
		`{"query":"query{ mediaTracks(mediaID:\"media-missing\"){ versions { id } } }"}`)
	var resp struct {
		Data struct {
			MediaTracks MediaTracksInfo `json:"mediaTracks"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "unknown media" || len(resp.Data.MediaTracks.Versions) != 0 {
		t.Fatalf("unknown Media = %s", body)
	}
}

func mustMediaFiles(t *testing.T, st *store.Store, id store.MediaID) []store.MediaFile {
	t.Helper()
	files, err := st.MediaFilesForMedia(context.Background(), id, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
