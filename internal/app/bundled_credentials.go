package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// BundledCredentials carries the default scraper credentials an official
// build injects at link time, as comma-separated ref=value pairs:
//
//	-X github.com/beamfall/core/internal/app.BundledCredentials=ref=value,...
//
// script/release.sh fills it from BEAMFALL_BUNDLED_CREDENTIALS. Source builds
// leave it empty. These are rate-limited project keys that let metadata work
// out of the box, not secrets guarding user data: anything in a binary is
// extractable, and Go build info records the flag.
var BundledCredentials string

// bundledSecrets is the scraper credential source: the owner's saved secret
// files first, then a bundled default for an AuthRef the owner never saved.
// Saving a secret overrides the default; clearing it restores the default.
// Disabling an AuthRef that has a bundled default also records an opt-out
// marker, so the scraper stops instead of falling back to the built-in key;
// saving a new owner secret lifts the opt-out (CORE-1540).
//
// Each save or disable touches two files (the owner's secret and the opt-out
// marker), so mu serializes them: two Console sessions saving and disabling
// the same key at once must leave the last action's state, never a missing
// owner file and a missing marker that re-enables the bundled key
// (CORE-1840). Reads take mu too, so they never see half of an action.
type bundledSecrets struct {
	secretFiles
	bundled map[string]string
	mu      *sync.Mutex
}

func withBundledCredentials(files secretFiles, pairs string) bundledSecrets {
	return bundledSecrets{secretFiles: files, bundled: parseBundledCredentials(pairs), mu: &sync.Mutex{}}
}

// Secret implements scrape.CredentialSource. Only a missing secret file falls
// back to the bundled default; an unreadable, over-permissive or empty file
// stays an error so a broken owner secret is never silently masked. An
// AuthRef the owner disabled reports the bundled default as absent
// (fs.ErrNotExist), so the scrape engine treats it as not configured and
// sends no request.
func (b bundledSecrets) Secret(ref string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	secret, err := b.secretFiles.Secret(ref)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return secret, err
	}
	value, ok := b.bundled[ref]
	if !ok {
		return "", err
	}
	disabled, markerErr := b.optedOut(ref)
	if markerErr != nil {
		return "", markerErr
	}
	if disabled {
		return "", fmt.Errorf("credential %q disabled by the owner: %w", ref, fs.ErrNotExist)
	}
	return value, nil
}

// SetSecret writes the owner's secret. Saving a non-empty secret lifts any
// opt-out, so the owner's key is what a later clear falls back from; an empty
// value only clears the owner's file and leaves an opt-out in place.
func (b bundledSecrets) SetSecret(ref, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.secretFiles.SetSecret(ref, value); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return b.clearOptOut(ref)
}

// DisableSecret implements graph.ScraperCredentialDisabler: it removes the
// owner's secret and, when the build bundles a default for ref, records the
// opt-out first so no read between the two steps sees the built-in key.
func (b bundledSecrets) DisableSecret(ref string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.bundled[ref]; ok {
		if err := b.writeOptOut(ref); err != nil {
			return err
		}
	}
	return b.secretFiles.SetSecret(ref, "")
}

// SecretDisabled implements graph.ScraperCredentialDisabler: true when the
// owner disabled a bundled default and saved no secret of their own since.
func (b bundledSecrets) SecretDisabled(ref string) bool {
	if _, ok := b.bundled[ref]; !ok {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.secretFiles.Secret(ref); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	disabled, err := b.optedOut(ref)
	return disabled && err == nil
}

// optOutPath names ref's opt-out marker. The leading dot keeps it out of the
// AuthRef namespace: Secret and SetSecret refuse dot-prefixed refs.
func (b bundledSecrets) optOutPath(ref string) (string, error) {
	if ref == "" || ref != filepath.Base(ref) || strings.HasPrefix(ref, ".") {
		return "", fmt.Errorf("invalid credential ref %q", ref)
	}
	return filepath.Join(b.dir, ".disabled-"+ref), nil
}

func (b bundledSecrets) optedOut(ref string) (bool, error) {
	path, err := b.optOutPath(ref)
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("credential %q opt-out unreadable: %w", ref, err)
	}
	return true, nil
}

func (b bundledSecrets) writeOptOut(ref string) error {
	path, err := b.optOutPath(ref)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		return err
	}
	marker, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- optOutPath keeps the name a single entry in the secrets directory.
	if err != nil {
		return err
	}
	if err := marker.Close(); err != nil {
		return err
	}
	return syncDir(b.dir)
}

func (b bundledSecrets) clearOptOut(ref string) error {
	path, err := b.optOutPath(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return syncDir(b.dir)
}

// parseBundledCredentials reads ref=value pairs, skipping blank or malformed
// entries and refs that could not name a secret file.
func parseBundledCredentials(pairs string) map[string]string {
	bundled := map[string]string{}
	for _, pair := range strings.Split(pairs, ",") {
		ref, value, ok := strings.Cut(pair, "=")
		ref, value = strings.TrimSpace(ref), strings.TrimSpace(value)
		if !ok || value == "" || ref == "" || strings.ContainsAny(ref, `/\`) || strings.HasPrefix(ref, ".") {
			continue
		}
		bundled[ref] = value
	}
	return bundled
}
