package capabilities

import (
	"runtime/debug"
	"sort"
	"strings"
)

type Report struct {
	Version                     string   `json:"version,omitempty"`
	Commit                      string   `json:"commit"`
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

var ReleaseVersion = "development"
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
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "development"
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
