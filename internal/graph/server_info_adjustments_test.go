package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestServerInfoAdvertisesPlaybackAdjustmentRanges proves an unauthenticated
// client can read the pitch shift and tempo ranges playback info accepts
// before offering the controls.
func TestServerInfoAdvertisesPlaybackAdjustmentRanges(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { serverInfo { playbackAdjustments { pitchShiftSemitonesMin pitchShiftSemitonesMax tempoMin tempoMax } } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data map[string]ServerInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := PlaybackAdjustmentRanges{PitchShiftSemitonesMin: -6, PitchShiftSemitonesMax: 6, TempoMin: 0.75, TempoMax: 1.25}
	if got := resp.Data["serverInfo"].PlaybackAdjustments; got != want {
		t.Fatalf("playbackAdjustments = %+v, want %+v (body %s)", got, want, rec.Body.String())
	}
}
