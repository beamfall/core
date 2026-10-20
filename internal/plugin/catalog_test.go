package plugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/plugin-sdk/catalogindex"
)

type catalogRoundTripper func(*http.Request) (*http.Response, error)

func (f catalogRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCatalogConsumerRecoveryTable(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	roots := PluginTrustRoots{Registries: map[string]RegistryTrustRoot{"test": {PublicKey: public}}}
	index7 := signedCatalog(t, private, 7, "filmtv")
	index8 := signedCatalog(t, private, 8, "music")
	modified8 := bytes.Replace(index8, []byte("music"), []byte("audiobook"), 1)

	tests := []struct {
		name       string
		cache      []byte
		response   []byte
		fetchErr   error
		wantID     string
		wantStale  bool
		wantErr    error
		wantCached []byte
	}{
		{name: "fresh", response: index7, wantID: "filmtv", wantCached: index7},
		{name: "signed equal is no-op", cache: index7, response: index7, wantID: "filmtv", wantCached: index7},
		{name: "unreachable valid cache", cache: index7, fetchErr: errors.New("offline"), wantID: "filmtv", wantStale: true, wantErr: ErrCatalogUnavailable, wantCached: index7},
		{name: "unreachable no cache", fetchErr: errors.New("offline"), wantErr: ErrCatalogUnavailable},
		{name: "corrupt keeps cache", cache: index7, response: modified8, wantID: "filmtv", wantStale: true, wantErr: ErrCatalogCorrupt, wantCached: index7},
		{name: "rollback keeps cache", cache: index8, response: index7, wantID: "music", wantStale: true, wantErr: ErrCatalogRollback, wantCached: index8},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.json")
			if tc.cache != nil {
				if err := writeStateFile(path, tc.cache); err != nil {
					t.Fatal(err)
				}
			}
			consumer := NewCatalogConsumer(path, roots)
			consumer.client = catalogClient(t, tc.response, tc.fetchErr)
			got, err := consumer.Check(context.Background(), []ID{"filmtv", "music", "audiobook"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Check error = %v, want %v", err, tc.wantErr)
			}
			if got.Stale != tc.wantStale {
				t.Fatalf("Stale = %v, want %v", got.Stale, tc.wantStale)
			}
			if tc.wantID == "" {
				if len(got.Entries) != 0 {
					t.Fatalf("Entries = %#v, want none", got.Entries)
				}
			} else if len(got.Entries) != 1 || got.Entries[0].PluginID != tc.wantID {
				t.Fatalf("Entries = %#v, want %s", got.Entries, tc.wantID)
			}
			cached, readErr := os.ReadFile(path) // #nosec G304 -- path is composed beneath this test's own t.TempDir().
			if tc.wantCached == nil {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("cache read error = %v, want not-exist", readErr)
				}
			} else if readErr != nil || !bytes.Equal(cached, tc.wantCached) {
				t.Fatalf("cache = %q, %v; want original valid bytes", cached, readErr)
			}
		})
	}
}

// io:egress-plugin-catalog-fetch
func TestCatalogConsumerRequestIsConstantAndIdentifierFree(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := signedCatalog(t, private, 1, "familyarchive")
	consumer := NewCatalogConsumer(filepath.Join(t.TempDir(), "catalog.json"), PluginTrustRoots{
		Registries: map[string]RegistryTrustRoot{"test": {PublicKey: public}},
	})
	consumer.client = &http.Client{Transport: catalogRoundTripper(func(req *http.Request) (*http.Response, error) {
		var body []byte
		if req.Body != nil {
			body, _ = io.ReadAll(req.Body)
		}
		header, _ := json.Marshal(req.Header)
		request := req.Method + " " + req.URL.String() + " " + string(header) + string(body)
		if req.URL.String() != CatalogURL || req.URL.RawQuery != "" || strings.Contains(request, "familyarchive") {
			t.Fatalf("request disclosed state or changed URL: %q", request)
		}
		return catalogResponse(raw), nil
	})}

	got, err := consumer.Check(context.Background(), []ID{"familyarchive"})
	if err != nil || len(got.Entries) != 1 {
		t.Fatalf("Check = %#v, %v", got, err)
	}
}

func catalogClient(t *testing.T, raw []byte, fetchErr error) *http.Client {
	t.Helper()
	return &http.Client{Transport: catalogRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != CatalogURL {
			t.Fatalf("URL = %q, want constant %q", req.URL, CatalogURL)
		}
		if fetchErr != nil {
			return nil, fetchErr
		}
		return catalogResponse(raw), nil
	})}
}

func catalogResponse(raw []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}

// signedCatalog uses first-party plugin ids: catalogindex admission is first-party-only
// and rejects any other id as ErrInvalid.
func signedCatalog(t *testing.T, private ed25519.PrivateKey, sequence uint64, pluginID string) []byte {
	t.Helper()
	index := catalogindex.Index{
		SchemaVersion: catalogindex.SchemaVersion,
		Sequence:      sequence,
		IssuedAt:      time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		Entries: []catalogindex.Entry{{
			PluginID:       pluginID,
			Version:        "1.0.0",
			SDKFloor:       "0.1.0",
			SDKCeiling:     "1.0.0",
			ArtifactSHA256: strings.Repeat("a", 64),
			DeliveryRef:    "delivery-" + strings.TrimPrefix(pluginID, "plugin."),
		}},
	}
	payload, err := json.Marshal(struct {
		Index         catalogindex.Index `json:"index"`
		RegistryKeyID string             `json:"registry_key_id"`
	}{index, "test"})
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(payload, &generic); err != nil {
		t.Fatal(err)
	}
	payload, err = json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(catalogindex.SignedIndex{
		Index:         index,
		RegistryKeyID: "test",
		Signature:     base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
