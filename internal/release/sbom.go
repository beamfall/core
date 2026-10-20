package release

import (
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
)

// SPDX is a minimal SPDX 2.3 document (the syft-compatible shape): enough fields
// for an SBOM consumer to enumerate the Go modules compiled into the server
// binary. Generated from the binary's own debug.BuildInfo, so it describes
// exactly what was built — no external scanner needed.
type SPDX struct {
	SPDXVersion       string           `json:"spdxVersion"`
	DataLicense       string           `json:"dataLicense"`
	SPDXID            string           `json:"SPDXID"`
	Name              string           `json:"name"`
	DocumentNamespace string           `json:"documentNamespace"`
	CreationInfo      spdxCreationInfo `json:"creationInfo"`
	Packages          []spdxPackage    `json:"packages"`
}

type spdxCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type spdxPackage struct {
	SPDXID           string `json:"SPDXID"`
	Name             string `json:"name"`
	VersionInfo      string `json:"versionInfo"`
	DownloadLocation string `json:"downloadLocation"`
	FilesAnalyzed    bool   `json:"filesAnalyzed"`
	ExternalRefs     []ref  `json:"externalRefs,omitempty"`
}

type ref struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

// BuildSBOM produces an SPDX SBOM for the given release version from the
// supplied module list. The caller passes debug.BuildInfo.Deps (plus the main
// module) so the document reflects the actual compiled dependency set; created
// is an injected RFC-3339 timestamp (the package never reads the clock).
func BuildSBOM(version, created string, mods []*debug.Module) SPDX {
	pkgs := make([]spdxPackage, 0, len(mods))
	seen := map[string]bool{}
	for _, mod := range mods {
		if mod == nil || mod.Path == "" || seen[mod.Path+mod.Version] {
			continue
		}
		seen[mod.Path+mod.Version] = true
		p := spdxPackage{
			SPDXID:           "SPDXRef-Package-" + sanitizeID(mod.Path) + "-" + sanitizeID(mod.Version),
			Name:             mod.Path,
			VersionInfo:      mod.Version,
			DownloadLocation: downloadLocation(mod),
			FilesAnalyzed:    false,
		}
		if mod.Version != "" && mod.Version != "(devel)" {
			p.ExternalRefs = []ref{{
				ReferenceCategory: "PACKAGE-MANAGER",
				ReferenceType:     "purl",
				ReferenceLocator:  "pkg:golang/" + mod.Path + "@" + mod.Version,
			}}
		}
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })

	return SPDX{
		SPDXVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              "beamfall-core-" + version,
		DocumentNamespace: "https://beamfall.com/sbom/beamfall-core-" + version,
		CreationInfo: spdxCreationInfo{
			Created:  created,
			Creators: []string{"Tool: beamfall-release", "Organization: Beamfall"},
		},
		Packages: pkgs,
	}
}

func downloadLocation(mod *debug.Module) string {
	if mod.Version == "" || mod.Version == "(devel)" {
		return "NOASSERTION"
	}
	return "https://proxy.golang.org/" + mod.Path + "/@v/" + mod.Version + ".zip"
}

// sanitizeID maps a module path/version to the SPDX identifier charset
// (alphanumeric, ".", "-").
func sanitizeID(s string) string {
	if s == "" {
		return "none"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// CurrentModules returns the main module plus its dependencies from the running
// binary's build info, for an SBOM that mirrors what was compiled. ok is false
// when build info is unavailable (e.g. `go test` without module info).
func CurrentModules() (mods []*debug.Module, ok bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, false
	}
	mods = append(mods, &info.Main)
	mods = append(mods, info.Deps...)
	return mods, true
}

// Validate fails if the SBOM is missing the structural fields an SPDX consumer
// requires — the assertion the SBOM test and CI rely on.
func (d SPDX) Validate() error {
	if d.SPDXVersion == "" || d.SPDXID == "" || d.DataLicense == "" || d.DocumentNamespace == "" {
		return fmt.Errorf("release: SBOM missing required SPDX header fields")
	}
	return nil
}
