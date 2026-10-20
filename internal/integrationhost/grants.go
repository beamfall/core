package integrationhost

import (
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/beamfall/core/internal/store"
)

// Grant is one plugin permission grant plus its approval and (for the catalog
// read permissions) its bound Reveal scope. A grant is pure data; the store owns
// persistence and the supervisor owns enforcement.
type Grant struct {
	PluginID string
	// Kind is one of the closed Core permission vocabulary (plugin.PermissionKinds).
	Kind string
	// Approved is false until an operator approves a high-risk grant, and is
	// flipped back to false when a permission-expanding update lands (re-approval).
	// Low-risk grants are approved on declaration.
	Approved bool
	// BoundProfileID binds a read-catalog/event-subscribe grant to one Profile's
	// Reveal scope. Mutually exclusive with AllLibraries.
	BoundProfileID store.ProfileID
	// AllLibraries is the explicit operator-level opt-in: the worker reads every
	// Library (hidden-world still hidden — Revealed is never set). Mutually
	// exclusive with BoundProfileID.
	AllLibraries bool
}

// ErrGrantScopeConflict is returned when a grant sets both a bound Profile and
// the all-Libraries opt-in. The two Reveal-scope bindings are mutually exclusive
// — a grant carrying both could bypass Reveal filtering, so it is rejected at
// the validation layer (and a DB CHECK constraint backs this in the migration).
var ErrGrantScopeConflict = errors.New("integrationhost: grant binds both a Profile and all-Libraries")

// validateGrant rejects a grant whose Reveal-scope binding is internally
// inconsistent, mirroring store.validateActivityEntry: the safety invariant is
// enforced in Go before the INSERT, not only by the DB CHECK. A grant may bind
// neither (no catalog read), exactly one (Profile-scoped or operator-level), but
// never both.
func validateGrant(g Grant) error {
	if g.BoundProfileID != "" && g.AllLibraries {
		return fmt.Errorf("%w (plugin %s, kind %s)", ErrGrantScopeConflict, g.PluginID, g.Kind)
	}
	if g.Kind != "" && !IsKnownPermission(g.Kind) {
		return fmt.Errorf("integrationhost: unknown permission %q", g.Kind)
	}
	return nil
}

// libraryFilter resolves the grant's Reveal-scope binding into a
// store.LibraryFilter. This is the single point where a worker's catalog reach
// is decided; the filter then flows into the existing store read paths exactly
// as a Session's filter does (the v1 REST path is the model). The auth.Scope
// resolution in internal/auth is the reference for this shape, not a dependency.
//
// For a Profile-bound grant the caller supplies the Profile's Library set
// (profile.Library, read from the store) so this package does not import auth.
// Revealed is never set: a worker is not a standing Reveal, restricted content
// stays hidden unless its Library is in the bound set AND revealed elsewhere.
func (g Grant) libraryFilter(profileLibraries []store.LibraryID) store.LibraryFilter {
	if g.AllLibraries {
		return store.LibraryFilter{AllLibraries: true, Revealed: false}
	}
	// Profile-bound (or empty): the bound Profile's Libraries, fail-closed on an
	// empty set (a zero-grant Profile sees nothing, like a zero-grant session).
	return store.LibraryFilter{AllowedIDs: profileLibraries, Revealed: false}
}

// expandsPermissions reports whether the desired permission set introduces a
// kind not already approved in the current set — the trigger for re-approval on
// an update (ADR-0058: "updates that expand permissions require re-approval").
// A pure narrowing (or unchanged) set does not require re-approval.
func expandsPermissions(currentApproved map[string]bool, desired []string) bool {
	for _, kind := range desired {
		if !currentApproved[kind] {
			return true
		}
	}
	return false
}

// constantTimeStringEqual compares two strings without leaking length-prefix
// position. Used for artifact-hash comparison (artifact.go).
func constantTimeStringEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
