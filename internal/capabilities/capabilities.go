package capabilities

import (
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

type Report struct {
	Version                     string   `json:"version,omitempty"`
	Commit                      string   `json:"commit"`
	BuildDate                   string   `json:"buildDate,omitempty"`
	BuildTags                   []string `json:"buildTags"`
	CorrespondingSourceOfferURL string   `json:"correspondingSourceOfferURL,omitempty"`
	Capabilities                []string `json:"capabilities,omitempty"`
	// CapabilityStates is the per-capability runtime state: each
	// compiled-in capability with its kind, runtime-enabled flag, and (for a
	// probe-absent capability) its degraded reason. Capabilities (above) is the
	// flat enabled-ID convenience list derived from these.
	CapabilityStates []State `json:"capabilityStates,omitempty"`
	// CapabilityManifest is the canonical flat ID registry consumed by the CLI
	// and Companion activation. Runtime state never changes these identities.
	CapabilityManifest *Manifest `json:"capabilityManifest,omitempty"`
	manifestError      error
}

// Build identity can be set at link time by the release pipeline, Dockerfile,
// and Makefile:
//
//	-X github.com/beamfall/core/internal/capabilities.ReleaseVersion=<version>
//	-X github.com/beamfall/core/internal/capabilities.ReleaseCommit=<git commit>
//	-X github.com/beamfall/core/internal/capabilities.ReleaseBuildDate=<RFC3339 UTC timestamp>
//
// ReleaseCommit exists for builds without VCS metadata (a Docker build context
// excludes .git, so debug.ReadBuildInfo has no vcs.revision); an empty value
// falls back to that revision, then to "development".
// ReleaseBuildDate falls back to vcs.time and is omitted when neither is valid.
var ReleaseVersion = "development"
var ReleaseCommit string
var ReleaseBuildDate string
var SourceOfferURL string

// Current reports the build identity plus the *default* runtime capability state
// (no operator Workflow overrides applied). Callers that hold the persisted
// Console overrides (the server, and `beamfall capabilities` reading the store)
// use CurrentWithOverrides so a flipped capability is immediately reflected.
func Current() Report {
	return CurrentWithOverrides(nil)
}

// CurrentWithOverrides reports the build identity plus the runtime capability
// state with the operator's Workflow toggle overrides applied (ADR-0124 §5:
// `beamfall capabilities` reflects the current runtime state). Probe capabilities
// ignore overrides — they are auto-detected and never operator-flipped.
func CurrentWithOverrides(overrides map[string]bool) Report {
	states := Resolve(overrides)
	caps := make([]string, 0, len(states))
	for _, s := range states {
		if s.Enabled {
			caps = append(caps, s.ID)
		}
	}
	sort.Strings(caps)
	buildTags := detectBuildTags()
	entries := make([]ManifestEntry, 0, len(states))
	for _, state := range states {
		entries = append(entries, ManifestEntry{ID: state.ID, Version: state.Version})
	}
	manifest, manifestError := NewManifest(entries, buildTags)
	return Report{
		Version:                     ReleaseVersion,
		Commit:                      detectCommit(),
		BuildDate:                   resolveBuildDate(ReleaseBuildDate, debug.ReadBuildInfo),
		BuildTags:                   buildTags,
		CorrespondingSourceOfferURL: SourceOfferURL,
		Capabilities:                caps,
		CapabilityStates:            states,
		CapabilityManifest:          &manifest,
		manifestError:               manifestError,
	}
}

// Validate asserts the report carries the canonical capability manifest.
func (r Report) Validate() error {
	if r.manifestError != nil {
		return r.manifestError
	}
	if r.CapabilityManifest == nil {
		return nil
	}
	return ValidateManifest(*r.CapabilityManifest)
}

func detectCommit() string {
	if commit := strings.TrimSpace(ReleaseCommit); commit != "" {
		return commit
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "development"
}

func resolveBuildDate(stamped string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if date := normalizeBuildDate(stamped); date != "" {
		return date
	}
	if info, ok := readBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.time" {
				return normalizeBuildDate(setting.Value)
			}
		}
	}
	return ""
}

func normalizeBuildDate(value string) string {
	date, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return date.UTC().Format(time.RFC3339)
}

func detectBuildTags() []string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-tags" && setting.Value != "" {
				return strings.FieldsFunc(setting.Value, func(r rune) bool {
					return r == ',' || r == ' '
				})
			}
		}
	}
	return nil
}
