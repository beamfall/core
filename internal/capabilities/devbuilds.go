package capabilities

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// DevBuildIDSuffix ends every build id a runtime dev-build catalog may carry.
// Client debug identities already use it (beamfall-android
// script/client_build_identity.py --dev), and a release identity never does,
// so a dev record cannot stand in for a release build id.
const DevBuildIDSuffix = "-dev"

// ParseDevBuildManifest strictly decodes the operator's runtime dev-build
// catalog (CORE-2396). It is a FirstPartyBuildManifestSchema manifest, parsed
// by ParseFirstPartyBuildManifest, with tighter rules because it is read from
// the server's own disk at runtime rather than linked in by a release:
//
//   - every buildID ends in DevBuildIDSuffix;
//   - distributions is exactly ["direct"]: a dev build is never an App Store build;
//   - roles are only viewer and/or manager, the paired-device classes;
//   - no minimumSequence: rollback floors stay release policy.
//
// An empty manifest (no builds) is valid and admits nothing.
func ParseDevBuildManifest(data []byte) (BuildCatalog, error) {
	catalog, err := ParseFirstPartyBuildManifest(data)
	if err != nil {
		return BuildCatalog{}, fmt.Errorf("dev build catalog: %w", err)
	}
	if len(catalog.MinimumSequence) != 0 {
		return BuildCatalog{}, errors.New("dev build catalog: minimumSequence is release policy and is refused here")
	}
	for i, record := range catalog.Builds {
		if err := validateDevBuild(record); err != nil {
			return BuildCatalog{}, fmt.Errorf("dev build catalog: builds[%d] %q: %w", i, record.BuildID, err)
		}
	}
	catalog.MinimumSequence = nil
	return catalog, nil
}

func validateDevBuild(record BuildRecord) error {
	if !strings.HasSuffix(record.BuildID, DevBuildIDSuffix) || len(record.BuildID) == len(DevBuildIDSuffix) {
		return fmt.Errorf("buildID must end in %q", DevBuildIDSuffix)
	}
	if !slices.Equal(record.Distributions, []Distribution{DistributionDirect}) {
		return errors.New(`distributions must be exactly ["direct"]`)
	}
	for _, role := range record.Roles {
		if role != EnrollmentRoleViewer && role != EnrollmentRoleManager {
			return fmt.Errorf("role %q is not allowed for a dev build (viewer, manager)", role)
		}
	}
	return nil
}

// WithDevBuilds returns the catalog with the runtime dev records added. A dev
// record never replaces or shadows a record already in the catalog: one whose
// build id the catalog already carries is skipped and reported in shadowed, so
// a release build keeps exactly the record its release linked. Rollback floors
// and enrollment bindings are untouched; every dev record still goes through
// the unchanged VerifyBuildProvenance (digest, surface, floor, role,
// distribution and exact enrollment mapping).
func (c BuildCatalog) WithDevBuilds(dev BuildCatalog) (merged BuildCatalog, shadowed []string) {
	merged = c.Clone()
	known := make(map[string]struct{}, len(merged.Builds))
	for _, record := range merged.Builds {
		known[strings.TrimSpace(record.BuildID)] = struct{}{}
	}
	for _, record := range dev.Clone().Builds {
		id := strings.TrimSpace(record.BuildID)
		if _, dup := known[id]; dup {
			shadowed = append(shadowed, id)
			continue
		}
		known[id] = struct{}{}
		merged.Builds = append(merged.Builds, record)
	}
	return merged, shadowed
}
