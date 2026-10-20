package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

const goldenPlaybackPath = "testdata/playback-info.golden.json"

// TestGoldenPlaybackInfo pins the playback-info wire shape the same way
// TestGoldenManifest pins the Navigation Manifest: the committed golden is
// the cross-repo contract fixture. The stack runs on fixed clocks and a
// fixed token key, so the response — tokens and expiry included — is fully
// deterministic and the golden compares byte-for-byte structurally.
// The request declares a next item, so the golden exercises the complete
// shape: direct-play streams, stream tokens, timeline, and `next`.
// Regenerate with BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance -run TestGoldenPlaybackInfo.
func TestGoldenPlaybackInfo(t *testing.T) {
	srv := newPlaybackContractServer(t)

	body := `{"mediaID":"media-alpha","nextMediaID":"media-archive",` +
		`"capabilityProfile":{"client":"conformance","containers":["mp4"],"directCodecs":["h264"]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/playback-info", strings.NewReader(body))
	req.Header.Set("X-Beamfall-Session", "session-revealed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("playback-info status = %d: %s", rec.Code, rec.Body.String())
	}

	live := decodeJSON(t, rec.Body.Bytes())

	if os.Getenv("BEAMFALL_UPDATE_GOLDEN") == "1" {
		pretty, err := json.MarshalIndent(live, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(goldenPlaybackPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPlaybackPath, append(pretty, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden playback-info updated: %s — copy it to the client repos' contract fixtures", goldenPlaybackPath)
		return
	}

	goldenBytes, err := os.ReadFile(goldenPlaybackPath)
	if err != nil {
		t.Fatalf("golden playback-info missing (%v) — generate with BEAMFALL_UPDATE_GOLDEN=1", err)
	}
	golden := decodeJSON(t, goldenBytes)
	if !jsonEqual(live, golden) {
		liveJSON, _ := json.MarshalIndent(live, "", "  ")
		t.Fatalf("playback-info shape drifted from the committed golden.\n"+
			"If intentional: BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance -run TestGoldenPlaybackInfo, "+
			"then copy the golden to the client repos.\nlive:\n%s", liveJSON)
	}
}

// newPlaybackContractServer composes the real HTTP stack on fixed clocks and
// a fixed token key, with both seeded Media backed by real files. It is the
// baseline-quirks case: the shipped database is empty, so every existing
// contract fixture is unchanged.
func newPlaybackContractServer(t *testing.T) *server.Server {
	t.Helper()
	srv, _ := newPlaybackContractServerWithQuirks(t, nil)
	return srv
}

// newPlaybackContractServerWithQuirks is newPlaybackContractServer with a
// device-quirks database injected, returning the playback service too so a test
// can read back what the server was configured with. nil entries keep the
// release-embedded baseline.
func newPlaybackContractServerWithQuirks(t *testing.T, entries []playback.QuirkEntry) (*server.Server, *playback.Service) {
	t.Helper()
	st := storetest.Open(t)
	for i, mediaID := range []store.MediaID{"media-alpha", "media-archive"} {
		path := filepath.Join(t.TempDir(), "media.bin")
		payload := []byte{byte(i)}
		if mediaID == tokenScopeMediaID {
			payload = []byte(tokenScopeBytes)
		}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := st.SetPrimaryMediaFile(context.Background(), mediaID, path); err != nil {
			t.Fatal(err)
		}
	}
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	authService := auth.NewServiceWithClock(st, clock)
	manifests := manifest.NewService(st, clock, nil)
	tokens := streamtoken.NewService([]byte("12345678901234567890123456789012"), clock)
	assetService := genassets.NewService(st, nil, t.TempDir())
	playbackInfo := playback.NewService(st, tokens, clock)
	playbackInfo.SetAssets(playbackLoudnessLocator{assetService})
	if entries != nil {
		if err := playbackInfo.SetQuirks(playback.QuirkDB{
			Version: "conformance.2026-07-21",
			License: "CC0-1.0",
			Entries: entries,
		}); err != nil {
			t.Fatalf("SetQuirks: %v", err)
		}
	}
	return server.New(
		manifests,
		playbackInfo,
		beam.NewService(st, authService, clock),
		pairing.NewService(st, clock),
		graph.NewHandler(manifests, authService, st, nil),
		mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil)),
		authService,
		st,
	), playbackInfo
}

func decodeJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("playback-info is not valid JSON: %v", err)
	}
	return doc
}

func jsonEqual(a, b map[string]any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// playbackLoudnessLocator bridges *genassets.Service into playback's
// AssetsLocator seam for the conformance corpus, mapping genassets'
// LoudnessAnalysis into playback's LoudnessData (the dependency firewall forbids
// genassets importing playback, so the bridge lives at the wiring site — the same
// adapter the app composition root uses). The conformance corpus seeds no loudness
// asset, so LoudnessForFingerprint always reports absent and the golden carries no
// loudness block — exactly the omitempty default the wire contract promises.
type playbackLoudnessLocator struct {
	service *genassets.Service
}

func (l playbackLoudnessLocator) TrickplayAsset(fp string) (string, bool) {
	return l.service.TrickplayAsset(fp)
}
func (l playbackLoudnessLocator) PreviewClipAsset(fp string) (string, bool) {
	return l.service.PreviewClipAsset(fp)
}
func (l playbackLoudnessLocator) WaveformAsset(fp string) (string, bool) {
	return l.service.WaveformAsset(fp)
}
func (l playbackLoudnessLocator) BeatGridAsset(fp string) (string, bool) {
	return l.service.BeatGridAsset(fp)
}
func (l playbackLoudnessLocator) SyncLanesAsset(fp string) (string, bool) {
	return l.service.SyncLanesAsset(fp)
}
func (l playbackLoudnessLocator) SilenceMapAsset(fp string) (string, bool) {
	return l.service.SilenceMapAsset(fp)
}
func (l playbackLoudnessLocator) LoudnessAsset(fp string) (string, bool) {
	return l.service.LoudnessAsset(fp)
}
func (l playbackLoudnessLocator) LoudnessForFingerprint(fp string) (playback.LoudnessData, bool) {
	analysis, ok := l.service.LoudnessForFingerprint(fp)
	if !ok {
		return playback.LoudnessData{}, false
	}
	return playback.LoudnessData{
		IntegratedLUFS:  analysis.IntegratedLUFS,
		LoudnessRangeLU: analysis.LoudnessRangeLU,
		TruePeakDBTP:    analysis.TruePeakDBTP,
		ReferenceLUFS:   analysis.ReferenceLUFS,
		GainDB:          analysis.GainDB,
		Source:          analysis.Source,
	}, true
}
