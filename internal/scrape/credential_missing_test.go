package scrape

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

// fileCredentials mimics the server's secret-file source: an absent secret
// wraps the lstat error, which names the secrets path.
type fileCredentials map[string]error

func (f fileCredentials) Secret(ref string) (string, error) {
	if err, ok := f[ref]; ok {
		return "", err
	}
	return "s3cret", nil
}

// TestMissingCredentialIsTypedAndPathFree (CORE-1101): a scraper whose AuthRef
// has no configured secret fails with CredentialMissingError naming only the
// ref, before any request, so a scan can report it once as a setup hint. The
// raw filesystem error stays reachable through Unwrap but out of the message;
// a present-but-broken secret is still an ordinary error.
func TestMissingCredentialIsTypedAndPathFree(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, `{"name": "x"}`)
	}))
	defer server.Close()
	spec := plugin.ScraperSpec{
		ID: "authsource", Kind: "http-json", Endpoint: server.URL,
		LookupTemplate: "/{id}", AuthRef: "authsource-api-key",
		FieldMap: map[string]string{"title": "name"},
	}
	lstat := &fs.PathError{Op: "lstat", Path: "/data/secrets/authsource-api-key", Err: fs.ErrNotExist}

	for name, engine := range map[string]*Engine{
		"absent secret file":   New(WithInsecureLoopback(), WithCredentials(fileCredentials{"authsource-api-key": fmt.Errorf("credential %q unavailable: %w", "authsource-api-key", lstat)})),
		"no credential source": New(WithInsecureLoopback()),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := engine.Lookup(context.Background(), spec, "1")
			var missing *CredentialMissingError
			if !errors.As(err, &missing) {
				t.Fatalf("Lookup err = %v, want CredentialMissingError", err)
			}
			if missing.AuthRef != spec.AuthRef || missing.MissingCredential() != spec.AuthRef {
				t.Fatalf("missing credential = %q, want %q", missing.AuthRef, spec.AuthRef)
			}
			if msg := err.Error(); strings.Contains(msg, "/data/secrets") || strings.Contains(msg, "lstat") {
				t.Fatalf("missing-credential message leaks the filesystem error: %q", msg)
			}
		})
	}
	if _, err := New(WithInsecureLoopback(), WithCredentials(fileCredentials{"authsource-api-key": fmt.Errorf("credential %q unavailable: %w", "authsource-api-key", lstat)})).Lookup(context.Background(), spec, "1"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Unwrap lost the cause: %v", err)
	}

	broken := New(WithInsecureLoopback(), WithCredentials(fileCredentials{"authsource-api-key": errors.New("credential file must be mode 0600")}))
	_, err := broken.Lookup(context.Background(), spec, "1")
	var missing *CredentialMissingError
	if err == nil || errors.As(err, &missing) {
		t.Fatalf("broken secret err = %v, want an ordinary error", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("%d requests sent without a credential", requests.Load())
	}
}

// TestLoginSecretFieldMissingIsTyped: an absent login SecretFields credential
// is reported the same way as the scraper's own AuthRef.
func TestLoginSecretFieldMissingIsTyped(t *testing.T) {
	_, err := resolveCredential(fileCredentials{"pin-ref": fmt.Errorf("unavailable: %w", fs.ErrNotExist)}, "pin-ref", `scraper "x" login field "pin": `)
	var missing *CredentialMissingError
	if !errors.As(err, &missing) || missing.AuthRef != "pin-ref" {
		t.Fatalf("resolveCredential err = %v, want CredentialMissingError{pin-ref}", err)
	}
	_, err = resolveCredential(fileCredentials{"pin-ref": errors.New("empty")}, "pin-ref", `scraper "x" login field "pin": `)
	if err == nil || !strings.Contains(err.Error(), `login field "pin": resolving credential "pin-ref"`) {
		t.Fatalf("ordinary failure lost its context: %v", err)
	}
}
