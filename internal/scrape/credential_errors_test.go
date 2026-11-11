package scrape

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

type stubSecrets func(ref string) (string, error)

func (f stubSecrets) Secret(ref string) (string, error) { return f(ref) }

func TestMissingCredentialIsTypedSentinel(t *testing.T) {
	ref := plugin.ScopedCredentialRef("filmtv", "tmdb-api-key")
	missing := stubSecrets(func(ref string) (string, error) {
		return "", fmt.Errorf("credential %q unavailable: %w", ref, &fs.PathError{Op: "lstat", Path: "/data/secrets/" + ref, Err: fs.ErrNotExist})
	})
	e := New(WithCredentials(missing))

	_, credErr := e.Credential(ref)
	_, authErr := e.authorizer(context.Background(), plugin.ScraperSpec{ID: "tmdb", AuthRef: ref})
	for name, err := range map[string]error{"Credential": credErr, "authorizer": authErr} {
		if !errors.Is(err, ErrCredentialNotConfigured) {
			t.Fatalf("%s err = %v, want ErrCredentialNotConfigured", name, err)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s err = %v, lost the underlying store error", name, err)
		}
		var typed *CredentialNotConfiguredError
		if !errors.As(err, &typed) || typed.MissingCredentialRef() != ref {
			t.Fatalf("%s err = %v, want *CredentialNotConfiguredError for %q", name, err, ref)
		}
		hint := typed.ConfigureHint()
		for _, want := range []string{`"filmtv"`, `"tmdb-api-key"`, "Console > Plugins"} {
			if !strings.Contains(hint, want) {
				t.Fatalf("%s hint %q missing %q", name, hint, want)
			}
		}
	}
	if e.CredentialAvailable(ref) {
		t.Fatal("CredentialAvailable = true for a missing credential")
	}
}

func TestBrokenCredentialIsNotSentinel(t *testing.T) {
	broken := stubSecrets(func(string) (string, error) {
		return "", errors.New("credential file must be mode 0600")
	})
	e := New(WithCredentials(broken))
	_, err := e.Credential("tmdb-api-key")
	if err == nil || errors.Is(err, ErrCredentialNotConfigured) {
		t.Fatalf("err = %v, want a plain (non-sentinel) error", err)
	}
	if _, err := e.Credential(""); err != nil {
		t.Fatalf("empty ref err = %v", err)
	}
}
