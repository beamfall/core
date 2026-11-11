package scrape

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/beamfall/core/internal/plugin"
)

// ErrCredentialNotConfigured reports that a scraper's AuthRef has no secret in
// the secure store at all — the operator never added the key. It is distinct
// from a present-but-broken secret (bad permissions, unreadable file), which
// stays an ordinary error. Callers match it with errors.Is.
var ErrCredentialNotConfigured = errors.New("credential not configured")

// CredentialNotConfiguredError carries the missing AuthRef so callers can
// report it once (per scan, per credential) with an actionable hint instead of
// a raw secure-store lookup error per request.
type CredentialNotConfiguredError struct {
	Ref string
	Err error // underlying secure-store error, kept for debugging
}

func (e *CredentialNotConfiguredError) Error() string {
	return fmt.Sprintf("credential %q not configured", e.Ref)
}

// Unwrap exposes both the sentinel and the underlying store error.
func (e *CredentialNotConfiguredError) Unwrap() []error {
	return []error{ErrCredentialNotConfigured, e.Err}
}

// MissingCredentialRef returns the unconfigured AuthRef. Consumers that must
// not import scrape (scan) match on this method through a local interface.
func (e *CredentialNotConfiguredError) MissingCredentialRef() string { return e.Ref }

// ConfigureHint is an operator-facing sentence naming the plugin, the setting
// and where to add it. It never contains secret material.
func (e *CredentialNotConfiguredError) ConfigureHint() string {
	pluginID, name, err := plugin.ParseScopedCredentialRef(e.Ref)
	if err != nil {
		return fmt.Sprintf("credential %q is not configured; add it in Console > Plugins (Credential column)", e.Ref)
	}
	return fmt.Sprintf("plugin %q credential %q is not configured; add it in Console > Plugins > %s (Credential column) to enable metadata lookups", pluginID, name, pluginID)
}

// classifyCredentialErr converts a secure-store "no such secret" failure into
// a *CredentialNotConfiguredError; every other error passes through unchanged.
func classifyCredentialErr(ref string, err error) error {
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return &CredentialNotConfiguredError{Ref: ref, Err: err}
}
