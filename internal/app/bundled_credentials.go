package app

import (
	"errors"
	"io/fs"
	"strings"
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
// Every write goes to the secret files unchanged.
type bundledSecrets struct {
	secretFiles
	bundled map[string]string
}

func withBundledCredentials(files secretFiles, pairs string) bundledSecrets {
	return bundledSecrets{secretFiles: files, bundled: parseBundledCredentials(pairs)}
}

// Secret implements scrape.CredentialSource. Only a missing secret file falls
// back to the bundled default; an unreadable, over-permissive or empty file
// stays an error so a broken owner secret is never silently masked.
func (b bundledSecrets) Secret(ref string) (string, error) {
	secret, err := b.secretFiles.Secret(ref)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return secret, err
	}
	if value, ok := b.bundled[ref]; ok {
		return value, nil
	}
	return "", err
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
