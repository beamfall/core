package integrationhost

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/beamfall/core/internal/plugin"
)

// The closed Integration Host permission vocabulary is owned by internal/plugin
// (plugin.PermissionKinds, ADR-0058). This package names the individual kinds it
// gates on so Host API method dispatch and grant logic read clearly, but the
// authority for the set — and the rule that a plugin can never declare a new one
// — stays in plugin.Validate. PermissionsKnown asserts the two stay in sync.
const (
	PermDeclaredOutbound    = "declared-outbound"
	PermReadCatalog         = "read-catalog"
	PermEventSubscribe      = "event-subscribe"
	PermPluginSecret        = "plugin-secret"
	PermStatusWrite         = "status-write"
	PermPluginJob           = "plugin-job"
	PermProfileLink         = "profile-link"
	PermRequestIngest       = "request-ingest"
	PermRequestFileMutation = "request-file-mutation"
)

// highRiskPermissions are the grants that require explicit Console/Manager
// approval and a per-call audit record (ADR-0058 §"high-risk"). A worker may
// hold a low-risk grant the moment it is declared; a high-risk grant is inert
// until approved, and a permission-expanding update flips it back to unapproved.
var highRiskPermissions = map[string]bool{
	PermReadCatalog:         true,
	PermProfileLink:         true,
	PermRequestIngest:       true,
	PermRequestFileMutation: true,
}

// IsHighRisk reports whether a permission kind requires explicit approval and
// per-call audit.
func IsHighRisk(kind string) bool { return highRiskPermissions[kind] }

// knownPermissions is the set this package gates on. It must equal
// plugin.PermissionKinds exactly (verified by a test) so a permission added to
// the Core vocabulary is never silently ungated here.
var knownPermissions = map[string]bool{
	PermDeclaredOutbound:    true,
	PermReadCatalog:         true,
	PermEventSubscribe:      true,
	PermPluginSecret:        true,
	PermStatusWrite:         true,
	PermPluginJob:           true,
	PermProfileLink:         true,
	PermRequestIngest:       true,
	PermRequestFileMutation: true,
}

// IsKnownPermission reports whether kind is in the closed Core vocabulary. A
// plugin can never declare a new auth scope: an unknown kind is rejected at
// bundle validation (plugin.Validate) and, defensively, here.
func IsKnownPermission(kind string) bool { return knownPermissions[kind] }

var ErrPermissionVocabularyDiverged = errors.New("integrationhost: permission vocabulary diverged")

// permissionDispatch binds every declared permission to the Host API wire
// method that gates on it. The dispatch switch (protocol.go) routes that method
// name into the backing Host method, which authorizes on the same permission.
// A permission with no dispatch method here is a silent "unknown method" trap
// for a worker that holds the grant — TestPermissionKindHasDispatchMethod
// asserts this map covers the closed vocabulary exactly, so a declared-but-
// undispatched permission (the CAUDIT-7 defect) fails the gate.
var permissionDispatch = map[string]string{
	PermDeclaredOutbound:    methodRequestOutbound,
	PermReadCatalog:         methodReadCatalog,
	PermEventSubscribe:      methodSubscribeEvents,
	PermPluginSecret:        methodGetSecret, // also methodSetSecret; one method per kind suffices
	PermStatusWrite:         methodWriteStatus,
	PermPluginJob:           methodEnqueueJob,
	PermProfileLink:         methodLinkProfile,
	PermRequestIngest:       methodRequestIngest,
	PermRequestFileMutation: methodRequestFileMutation,
}

// DispatchMethodFor returns the Host API wire method that gates on kind, and
// whether kind has one. A declared permission with no dispatch method is the
// CAUDIT-7 silent-failure trap.
func DispatchMethodFor(kind string) (string, bool) {
	method, ok := permissionDispatch[kind]
	return method, ok
}

// permissionVocabularyInSync reports whether this package's gated set equals
// plugin.PermissionKinds. A test asserts it; if Core adds a permission, this
// fails until the new kind is gated here, so no permission is silently ungated.
func permissionVocabularyInSync() bool {
	return permissionVocabularySyncError(knownPermissions, plugin.PermissionKinds()) == nil
}

func AssertPermissionVocabularyInSync() error {
	return permissionVocabularySyncError(knownPermissions, plugin.PermissionKinds())
}

func permissionVocabularySyncError(host map[string]bool, sdkKinds []string) error {
	sdkSet := make(map[string]bool, len(sdkKinds))
	var missing, extra, duplicate []string
	for _, kind := range sdkKinds {
		if sdkSet[kind] {
			duplicate = append(duplicate, kind)
		}
		sdkSet[kind] = true
		if !host[kind] {
			missing = append(missing, kind)
		}
	}
	for kind := range host {
		if !sdkSet[kind] {
			extra = append(extra, kind)
		}
	}
	if len(missing) == 0 && len(extra) == 0 && len(duplicate) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(duplicate)
	return fmt.Errorf("%w: missing host gates [%s], extra host gates [%s], duplicate SDK kinds [%s]",
		ErrPermissionVocabularyDiverged, strings.Join(missing, ", "), strings.Join(extra, ", "), strings.Join(duplicate, ", "))
}
