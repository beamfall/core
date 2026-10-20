package release

import "fmt"

// SLSA predicate / statement type URIs (SLSA v1, in-toto v1).
const (
	inTotoStatementType = "https://in-toto.io/Statement/v1"
	slsaPredicateType   = "https://slsa.dev/provenance/v1"
	beamfallBuildType   = "https://beamfall.com/buildtypes/release/v1"
	beamfallBuilderID   = "https://github.com/beamfall/core/.github/workflows/release.yml"
)

// Provenance is a SLSA v1 build-provenance attestation as an in-toto Statement.
// Subjects bind the released artifacts (name + sha256) to the build that
// produced them, so a verifier can attest "this exact artifact came from this
// commit via this builder".
type Provenance struct {
	Type          string        `json:"_type"`
	Subject       []subject     `json:"subject"`
	PredicateType string        `json:"predicateType"`
	Predicate     slsaPredicate `json:"predicate"`
}

type subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type slsaPredicate struct {
	BuildDefinition buildDefinition `json:"buildDefinition"`
	RunDetails      runDetails      `json:"runDetails"`
}

type buildDefinition struct {
	BuildType            string            `json:"buildType"`
	ExternalParameters   map[string]string `json:"externalParameters"`
	ResolvedDependencies []resolvedDep     `json:"resolvedDependencies,omitempty"`
}

type resolvedDep struct {
	URI    string            `json:"uri"`
	Digest map[string]string `json:"digest,omitempty"`
}

type runDetails struct {
	Builder  builder  `json:"builder"`
	Metadata metadata `json:"metadata"`
}

type builder struct {
	ID string `json:"id"`
}

type metadata struct {
	InvocationID string `json:"invocationId,omitempty"`
	StartedOn    string `json:"startedOn,omitempty"`
}

// BuildProvenance builds the SLSA statement for a release: version/channel as
// external parameters, the source commit as a resolved dependency, and one
// subject per signed artifact. startedOn is an injected RFC-3339 timestamp.
func BuildProvenance(version, channel, commit, invocationID, startedOn string, arts []Artifact) Provenance {
	subjects := make([]subject, 0, len(arts))
	for _, a := range arts {
		subjects = append(subjects, subject{
			Name:   a.Name,
			Digest: map[string]string{"sha256": a.SHA256},
		})
	}
	var deps []resolvedDep
	if commit != "" {
		deps = append(deps, resolvedDep{
			URI:    "git+https://github.com/beamfall/core@" + commit,
			Digest: map[string]string{"gitCommit": commit},
		})
	}
	return Provenance{
		Type:          inTotoStatementType,
		Subject:       subjects,
		PredicateType: slsaPredicateType,
		Predicate: slsaPredicate{
			BuildDefinition: buildDefinition{
				BuildType: beamfallBuildType,
				ExternalParameters: map[string]string{
					"version": version,
					"channel": channel,
				},
				ResolvedDependencies: deps,
			},
			RunDetails: runDetails{
				Builder:  builder{ID: beamfallBuilderID},
				Metadata: metadata{InvocationID: invocationID, StartedOn: startedOn},
			},
		},
	}
}

// Validate fails if the provenance is missing the structural fields a SLSA
// verifier requires (type, predicateType, at least one subject with a digest).
func (p Provenance) Validate() error {
	if p.Type != inTotoStatementType || p.PredicateType != slsaPredicateType {
		return fmt.Errorf("release: provenance has wrong statement/predicate type")
	}
	if len(p.Subject) == 0 {
		return fmt.Errorf("release: provenance has no subjects")
	}
	for _, s := range p.Subject {
		if s.Name == "" || s.Digest["sha256"] == "" {
			return fmt.Errorf("release: provenance subject %q missing sha256 digest", s.Name)
		}
	}
	return nil
}
