package contract

import (
	"net/http"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/playback"
)

func TestManagementManifestIncludesArtworkUploadRoute(t *testing.T) {
	manifest := buildManagementManifest()
	for _, route := range manifest.ManagementRoutes {
		if route.Method == http.MethodPost && route.Pattern == "/api/media/artwork-upload/{recordID}" {
			if route.Authorization != "manager-admin-session" ||
				route.Request == nil ||
				route.Request.ContentType != "multipart/form-data" ||
				route.Request.MaxBodyBytes == 0 ||
				route.Success == nil ||
				route.Success.Status != http.StatusCreated ||
				len(route.Request.PathFields) != 1 ||
				len(route.Request.MultipartFields) != 4 ||
				len(route.Errors) == 0 {
				t.Fatalf("artwork-upload route contract is incomplete: %+v", route)
			}
			return
		}
	}
	t.Fatal("management manifest omitted live artwork-upload route")
}

// TestPlaybackInfoNotesEnumerateAudioDeliveries keeps the published
// audioQuality.delivery vocabulary in step with the playback package's
// (CORE-1627): reflection publishes it as a plain string.
func TestPlaybackInfoNotesEnumerateAudioDeliveries(t *testing.T) {
	want := "delivery is the closed vocabulary " + strings.Join(playback.AudioDeliveries(), "|")
	for _, note := range playbackInfoNotes() {
		if strings.Contains(note, want) {
			return
		}
	}
	t.Fatalf("playbackInfoNotes does not state %q", want)
}
