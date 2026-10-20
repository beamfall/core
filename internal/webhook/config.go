package webhook

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"
)

// SecretSource resolves a per-endpoint secret ref to its bytes. The composition
// root supplies the 0600 secret-file reader (the same secretFiles helper the
// scrape engine and qBittorrent ingest use); the webhook config file on disk
// names a ref, never the secret itself (invariant #5).
type SecretSource interface {
	Secret(ref string) (string, error)
}

// fileEndpoint is the on-disk shape of one configured endpoint. It names the
// HMAC secret by ref (a 0600 secret-store file), never inline — so the config
// file carries no secret.
type fileEndpoint struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	SecretRef  string   `json:"secret_ref"`
	EventKinds []string `json:"event_kinds,omitempty"`
}

// fileConfig is the on-disk webhook configuration (DataDir/webhook-endpoints.json).
// A missing file means the plugin is unconfigured and stays dormant.
type fileConfig struct {
	Endpoints []fileEndpoint `json:"endpoints"`
	// MaxAttempts, BaseBackoffSeconds, PollIntervalMS override the defaults; zero
	// means "use the package default" (handled in New).
	MaxAttempts        int `json:"max_attempts,omitempty"`
	BaseBackoffSeconds int `json:"base_backoff_seconds,omitempty"`
	PollIntervalMS     int `json:"poll_interval_ms,omitempty"`
}

// LoadConfig reads the webhook configuration from path and resolves each
// endpoint's HMAC secret through secrets. A missing config file returns a zero
// Config (dormant — New will return a nil emitter). A present but malformed
// file, or an endpoint whose secret cannot be resolved, is an error so a
// misconfiguration fails at boot rather than silently dropping deliveries.
func LoadConfig(path string, secrets SecretSource) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil // dormant: no config file
	}
	if err != nil {
		slog.Error("webhook configuration read failed", "path", path, "error", err)
		return Config{}, ErrConfigDiagnostic
	}
	var fc fileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		slog.Error("webhook configuration decode failed", "path", path, "error", err)
		return Config{}, ErrConfigDiagnostic
	}
	if len(fc.Endpoints) == 0 {
		return Config{}, nil // dormant: configured with no endpoints
	}
	if secrets == nil {
		return Config{}, ErrSecretDiagnostic
	}
	cfg := Config{
		MaxAttempts:  fc.MaxAttempts,
		BaseBackoff:  time.Duration(fc.BaseBackoffSeconds) * time.Second,
		PollInterval: time.Duration(fc.PollIntervalMS) * time.Millisecond,
	}
	for _, fe := range fc.Endpoints {
		if strings.TrimSpace(fe.SecretRef) == "" {
			return Config{}, ErrConfigDiagnostic
		}
		secret, err := secrets.Secret(fe.SecretRef)
		if err != nil {
			return Config{}, ErrSecretDiagnostic
		}
		cfg.Endpoints = append(cfg.Endpoints, Endpoint{
			ID:         fe.ID,
			URL:        fe.URL,
			Secret:     []byte(secret),
			EventKinds: fe.EventKinds,
		})
	}
	return cfg, nil
}
