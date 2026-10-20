package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/pairing"
)

func TestReviewDemoEnrollmentOverHTTP(t *testing.T) {
	// feature:store-review-demo-enrollment
	const reviewKey = "review-demo-key-123456"
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "Review Demo"
	cfg.Discovery.Enabled = false
	cfg.StreamTokenKey = demoEnrollmentTestStreamTokenKey()
	cfg.SeedProfile = "demo"
	cfg.ReviewDemoEnrollment.Enabled = true
	cfg.ReviewDemoEnrollment.ProfileID = "profile-demo-default"
	writeReviewDemoSecret(t, cfg.DataDir, reviewKey, 0o600)

	addr := bootTestApp(t, cfg)
	resp := postEnroll(t, addr, reviewKey, "App Review iPhone")
	if resp.DeviceRole != "viewer" {
		t.Fatalf("DeviceRole = %q, want viewer", resp.DeviceRole)
	}
	if resp.ProfileID != "profile-demo-default" {
		t.Fatalf("ProfileID = %q, want profile-demo-default", resp.ProfileID)
	}
	if resp.SessionID == "" || resp.DeviceID == "" || resp.DeviceSecret == "" {
		t.Fatalf("incomplete enrollment response: %+v", resp)
	}
	if resp.SessionExpiresAt == "" {
		t.Fatalf("SessionExpiresAt is empty: %+v", resp)
	}

	assertPlaybackInfoOK(t, addr, resp)
}

func TestReviewDemoEnrollmentAcceptanceResumeCardsPlayableOverHTTP(t *testing.T) {
	const reviewKey = "review-demo-key-123456"
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "Review Demo"
	cfg.Discovery.Enabled = false
	cfg.StreamTokenKey = demoEnrollmentTestStreamTokenKey()
	cfg.SeedProfile = "acceptance"
	cfg.ReviewDemoEnrollment.Enabled = true
	cfg.ReviewDemoEnrollment.ProfileID = "profile-acc-default"
	writeReviewDemoSecret(t, cfg.DataDir, reviewKey, 0o600)

	addr := bootTestApp(t, cfg)
	resp := postEnroll(t, addr, reviewKey, "App Review iPad")
	for _, mediaID := range []string{"media-acc-search-aurora", "media-acc-search-harbor"} {
		assertPlaybackInfoPlayable(t, addr, resp, mediaID)
	}
}

func TestReviewDemoEnrollmentDisabledByDefaultOverHTTP(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "Review Demo"
	cfg.Discovery.Enabled = false
	cfg.StreamTokenKey = demoEnrollmentTestStreamTokenKey()
	cfg.SeedProfile = "demo"
	writeReviewDemoSecret(t, cfg.DataDir, "review-demo-key-123456", 0o600)

	addr := bootTestApp(t, cfg)
	body := bytes.NewBufferString(`{"code":"review-demo-key-123456","deviceName":"App Review iPhone"}`)
	res, err := http.Post("http://"+addr+"/api/enroll", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/enroll: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when review demo enrollment is disabled", res.StatusCode)
	}
}

func TestReviewDemoEnrollmentSecretFileMustBe0600(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.StreamTokenKey = demoEnrollmentTestStreamTokenKey()
	cfg.SeedProfile = "demo"
	cfg.ReviewDemoEnrollment.Enabled = true
	cfg.ReviewDemoEnrollment.ProfileID = "profile-demo-default"
	writeReviewDemoSecret(t, cfg.DataDir, "review-demo-key-123456", 0o644)

	if _, err := New(context.Background(), cfg, discardLogger(), nil); err == nil {
		t.Fatal("app.New accepted group/other-readable review demo enrollment key")
	}
}

func bootTestApp(t *testing.T, cfg config.Config) string {
	t.Helper()
	beamfall, err := New(context.Background(), cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- beamfall.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("Run returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run did not stop within 10s")
		}
	})

	addrCtx, addrCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer addrCancel()
	addr := beamfall.Addr(addrCtx)
	if addr == "" {
		t.Fatal("server did not bind a listener within 5s")
	}
	return addr
}

func postEnroll(t *testing.T, addr string, code string, deviceName string) pairing.EnrollResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"code": code, "deviceName": deviceName})
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post("http://"+addr+"/api/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/enroll: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	var resp pairing.EnrollResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode enrollment response: %v", err)
	}
	return resp
}

func assertPlaybackInfoOK(t *testing.T, addr string, enroll pairing.EnrollResponse) {
	t.Helper()
	body := `{"mediaID":"media-demo-bunny","capabilityProfile":{"client":"store-review","containers":["mp4"],"directCodecs":["h264"]}}`
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/playback-info", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Beamfall-Session", enroll.SessionID)
	req.Header.Set("X-Beamfall-Device", enroll.DeviceID)
	req.Header.Set("X-Beamfall-Device-Secret", enroll.DeviceSecret)
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST /api/playback-info: %v", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("playback-info status = %d, want 200: %s", res.StatusCode, data)
	}
}

func assertPlaybackInfoPlayable(t *testing.T, addr string, enroll pairing.EnrollResponse, mediaID string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"mediaID": mediaID,
		"capabilityProfile": map[string]any{
			"client":       "store-review",
			"containers":   []string{"mp4"},
			"directCodecs": []string{"h264"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/playback-info", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Beamfall-Session", enroll.SessionID)
	req.Header.Set("X-Beamfall-Device", enroll.DeviceID)
	req.Header.Set("X-Beamfall-Device-Secret", enroll.DeviceSecret)
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST /api/playback-info: %v", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("playback-info status = %d, want 200: %s", res.StatusCode, data)
	}
	var got struct {
		Decision string            `json:"decision"`
		Streams  []json.RawMessage `json:"streams"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode playback-info: %v: %s", err, data)
	}
	if got.Decision == "unavailable" || len(got.Streams) == 0 {
		t.Fatalf("%s playback-info = decision %q streams %d, want playable: %s", mediaID, got.Decision, len(got.Streams), data)
	}
}

func writeReviewDemoSecret(t *testing.T, dataDir string, key string, mode os.FileMode) {
	t.Helper()
	dir := filepath.Join(dataDir, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, reviewDemoEnrollmentSecretRef)
	if err := os.WriteFile(path, []byte(key), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func demoEnrollmentTestStreamTokenKey() []byte {
	return []byte("12345678901234567890123456789012")
}
