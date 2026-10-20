//go:build io

package ioe2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	scrobble "github.com/beamfall/plugin-sdk/scrobble"

	filmtv "github.com/beamfall/plugin-filmtv"
	music "github.com/beamfall/plugin-music"
)

// recordingHandler captures one request's method, path, headers, and raw body.
type recordedRequest struct {
	method string
	path   string
	auth   string
	body   []byte
	query  url.Values
}

func recordOnce(t *testing.T, respond string) (*httptest.Server, <-chan recordedRequest) {
	t.Helper()
	got := make(chan recordedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case got <- recordedRequest{
			method: r.Method,
			path:   r.URL.Path,
			auth:   r.Header.Get("Authorization"),
			body:   body,
			query:  r.URL.Query(),
		}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respond)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func awaitRequest(t *testing.T, got <-chan recordedRequest) recordedRequest {
	t.Helper()
	select {
	case r := <-got:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("double never received a request")
		return recordedRequest{}
	}
}

func sampleScrobble(scheme, value string) scrobble.Scrobble {
	return scrobble.Scrobble{
		Event: scrobble.WatchedEvent{
			Title:     "Test Track",
			WatchedAt: time.Unix(1700000000, 0).UTC(),
			Music: scrobble.MusicMetadata{
				Artist: "Test Artist",
				Album:  "Test Album",
			},
		},
		Identity: scrobble.ExternalID{Scheme: scheme, Value: value},
		Secret:   "profile-token",
	}
}

// TestEgress_scrobble_trakt_history drives the REAL TraktAdapter at a loopback
// double and asserts it POSTed the watched item to /sync/history.
//
// io:egress-scrobble-trakt-history
func TestEgress_scrobble_trakt_history(t *testing.T) {
	srv, got := recordOnce(t, `[]`)
	adapter := filmtv.NewTraktAdapter(srv.Client(), srv.URL, "client-app-id")

	if err := adapter.Submit(context.Background(), sampleScrobble(filmtv.SchemeTMDB, "603")); err != nil {
		t.Fatalf("Trakt Submit: %v", err)
	}
	r := awaitRequest(t, got)
	if r.method != http.MethodPost {
		t.Errorf("method = %q, want POST", r.method)
	}
	if r.path != "/sync/history" {
		t.Errorf("path = %q, want /sync/history", r.path)
	}
	if r.auth != "Bearer profile-token" {
		t.Errorf("Authorization = %q, want the bearer token", r.auth)
	}
	var payload struct {
		Movies []struct {
			IDs map[string]any `json:"ids"`
		} `json:"movies"`
	}
	if err := json.Unmarshal(r.body, &payload); err != nil {
		t.Fatalf("Trakt body not JSON: %v (body=%s)", err, r.body)
	}
	if len(payload.Movies) != 1 || payload.Movies[0].IDs["tmdb"] == nil {
		t.Errorf("Trakt body = %s, want one movie with a tmdb id", r.body)
	}
}

// TestEgress_scrobble_lastfm_submit drives the REAL LastFMAdapter at a loopback
// double and asserts it POSTed a signed track.scrobble form.
//
// io:egress-scrobble-lastfm-submit
func TestEgress_scrobble_lastfm_submit(t *testing.T) {
	srv, got := recordOnce(t, `{"scrobbles":{"@attr":{"accepted":1}}}`)
	adapter := music.NewLastFMAdapter(srv.Client(), srv.URL, "api-key-123", "shared-secret")

	if err := adapter.Submit(context.Background(), sampleScrobble(music.SchemeMusicBrainzRecording, "mbid-xyz")); err != nil {
		t.Fatalf("Last.fm Submit: %v", err)
	}
	r := awaitRequest(t, got)
	if r.method != http.MethodPost {
		t.Errorf("method = %q, want POST", r.method)
	}
	form, err := url.ParseQuery(string(r.body))
	if err != nil {
		t.Fatalf("Last.fm body not a form: %v", err)
	}
	if form.Get("method") != "track.scrobble" {
		t.Errorf("method param = %q, want track.scrobble", form.Get("method"))
	}
	if form.Get("api_key") != "api-key-123" {
		t.Errorf("api_key = %q", form.Get("api_key"))
	}
	if form.Get("api_sig") == "" {
		t.Error("api_sig missing — the request was not signed")
	}
	if form.Get("mbid") != "mbid-xyz" {
		t.Errorf("mbid = %q, want the recording id", form.Get("mbid"))
	}
}

// TestEgress_scrobble_listenbrainz_submit drives the REAL ListenBrainzAdapter at
// a loopback double and asserts it POSTed a single listen to /1/submit-listens.
//
// io:egress-scrobble-listenbrainz-submit
func TestEgress_scrobble_listenbrainz_submit(t *testing.T) {
	srv, got := recordOnce(t, `{"status":"ok"}`)
	adapter := music.NewListenBrainzAdapter(srv.Client(), srv.URL)

	if err := adapter.Submit(context.Background(), sampleScrobble(music.SchemeMusicBrainzRecording, "mbid-rec-1")); err != nil {
		t.Fatalf("ListenBrainz Submit: %v", err)
	}
	r := awaitRequest(t, got)
	if r.method != http.MethodPost {
		t.Errorf("method = %q, want POST", r.method)
	}
	if r.path != "/1/submit-listens" {
		t.Errorf("path = %q, want /1/submit-listens", r.path)
	}
	if r.auth != "Token profile-token" {
		t.Errorf("Authorization = %q, want Token <token>", r.auth)
	}
	var payload struct {
		ListenType string `json:"listen_type"`
		Payload    []struct {
			TrackMetadata struct {
				TrackName      string `json:"track_name"`
				AdditionalInfo struct {
					RecordingMBID string `json:"recording_mbid"`
				} `json:"additional_info"`
			} `json:"track_metadata"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(r.body, &payload); err != nil {
		t.Fatalf("ListenBrainz body not JSON: %v (body=%s)", err, r.body)
	}
	if payload.ListenType != "single" || len(payload.Payload) != 1 {
		t.Fatalf("listen payload = %s", r.body)
	}
	if payload.Payload[0].TrackMetadata.AdditionalInfo.RecordingMBID != "mbid-rec-1" {
		t.Errorf("recording_mbid = %q, want mbid-rec-1", payload.Payload[0].TrackMetadata.AdditionalInfo.RecordingMBID)
	}
}
