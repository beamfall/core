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
	legacy8 := legacyCatalog(t, private, 8, "music")

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
		{name: "legacy response refused keeps cache", cache: index7, response: legacy8, wantID: "filmtv", wantStale: true, wantErr: catalogindex.ErrLegacySchema, wantCached: index7},
		{name: "legacy cache replaced", cache: legacy8, response: index7, wantID: "filmtv", wantCached: index7},
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
			if !errors.Is(err, tc.wantErr) || (errors.Is(tc.wantErr, catalogindex.ErrLegacySchema) && !errors.Is(err, ErrCatalogCorrupt)) {
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

// signedCatalog builds a schema 2 index with TEST FIXTURE legal evidence. It
// uses first-party plugin ids: catalogindex admission is first-party-only and
// rejects any other id as ErrInvalid.
func signedCatalog(t *testing.T, private ed25519.PrivateKey, sequence uint64, pluginID string) []byte {
	t.Helper()
	_, reviewer, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const license = "LicenseRef-Test-Fixture"
	const reviewerID = "Test fixture reviewer (not a real person)"
	policy, err := catalogindex.SignPolicy(catalogindex.Policy{
		PolicyID:              "TEST-FIXTURE-POLICY-not-for-production",
		IssuedAt:              time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Licenses:              []catalogindex.PolicyLicense{{Expression: license}},
		Reviewers:             []catalogindex.PolicyReviewer{{KeyID: "reviewer", Identity: reviewerID, PublicKey: base64.RawURLEncoding.EncodeToString(reviewer.Public().(ed25519.PublicKey))}},
		AttestationMaxAgeDays: 365,
	}, "test", private)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := catalogindex.PolicyDigest(policy.Policy)
	if err != nil {
		t.Fatal(err)
	}
	entry := catalogEntry(pluginID)
	entry.Legal = catalogindex.Legal{
		LicenseExpression: license,
		Notices:           []catalogindex.Notice{{Path: "LICENSE", SHA256: strings.Repeat("b", 64)}},
		SourceRepository:  "https://example.invalid/fixture/" + pluginID,
		SourceCommit:      strings.Repeat("c", 40),
		SourceSHA256:      strings.Repeat("d", 64),
		ProvenanceSHA256:  strings.Repeat("e", 64),
		RightsAttestation: catalogindex.RightsAttestation{
			ReviewerKeyID:    "reviewer",
			ReviewerIdentity: reviewerID,
			ReviewedAt:       time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
			Disposition:      catalogindex.DispositionApproved,
		},
	}
	entry.Legal.RightsAttestation, err = catalogindex.SignRightsAttestation(entry, policyDigest, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := catalogindex.SignIndex(catalogindex.Index{
		SchemaVersion: catalogindex.SchemaVersion,
		Sequence:      sequence,
		IssuedAt:      time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
		Entries:       []catalogindex.Entry{entry},
	}, policy, "test", private)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// legacyCatalog builds a validly signed schema 1 index, the format the registry
// froze at catalog-index.v1.json.
func legacyCatalog(t *testing.T, private ed25519.PrivateKey, sequence uint64, pluginID string) []byte {
	t.Helper()
	index := struct {
		SchemaVersion int                  `json:"schema_version"`
		Sequence      uint64               `json:"sequence"`
		IssuedAt      time.Time            `json:"issued_at"`
		Entries       []catalogindex.Entry `json:"entries"`
	}{1, sequence, time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC), []catalogindex.Entry{catalogEntry(pluginID)}}
	payload, err := json.Marshal(struct {
		Index         any    `json:"index"`
		RegistryKeyID string `json:"registry_key_id"`
	}{index, "test"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"index":           index,
		"registry_key_id": "test",
		"signature":       base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func catalogEntry(pluginID string) catalogindex.Entry {
	return catalogindex.Entry{
		PluginID:       pluginID,
		Version:        "1.0.0",
		SDKFloor:       "0.4.0",
		SDKCeiling:     "0.4.0",
		ArtifactSHA256: strings.Repeat("a", 64),
		DeliveryRef:    "delivery-" + pluginID,
	}
}
