package capabilities

import (
	"encoding/base64"
	"strings"
	"testing"
)

const firstPartyTestHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

const firstPartyTestManifest = `{
  "schema": "beamfall.first-party-builds/v1",
  "builds": [
    {"buildID": "ios-viewer-1.0.0-100", "surface": "ios-viewer", "artifactHash": "` + firstPartyTestHash + `", "sequence": 100, "distributions": ["appStore"], "roles": ["viewer"]},
    {"buildID": "macos-manager-1.0.0-100", "surface": "macos-manager", "artifactHash": "` + firstPartyTestHash + `", "sequence": 100, "distributions": ["direct", "appStore"], "roles": ["manager"]}
  ],
  "minimumSequence": {"ios-viewer": 90}
}`

func TestParseFirstPartyBuildManifestDecodesRecordsAndFloors(t *testing.T) {
	catalog, err := ParseFirstPartyBuildManifest([]byte(firstPartyTestManifest))
	if err != nil {
		t.Fatalf("ParseFirstPartyBuildManifest: %v", err)
	}
	if len(catalog.Builds) != 2 {
		t.Fatalf("builds = %d, want 2", len(catalog.Builds))
	}
	ios := catalog.Builds[0]
	if ios.BuildID != "ios-viewer-1.0.0-100" || ios.Surface != SurfaceIOSViewer || ios.Sequence != 100 ||
		ios.ArtifactHash != firstPartyTestHash || len(ios.Distributions) != 1 || ios.Distributions[0] != DistributionAppStore ||
		len(ios.Roles) != 1 || ios.Roles[0] != EnrollmentRoleViewer {
		t.Fatalf("ios record = %+v", ios)
	}
	if catalog.MinimumSequence[SurfaceIOSViewer] != 90 {
		t.Fatalf("minimumSequence = %v", catalog.MinimumSequence)
	}
	if len(catalog.EnrollmentBindings) != 0 {
		t.Fatalf("a release manifest must never carry enrollment mappings: %v", catalog.EnrollmentBindings)
	}
}

func TestParseFirstPartyBuildManifestRefusesWhatItCannotVouchFor(t *testing.T) {
	build := func(fields string) string {
		return `{"schema":"beamfall.first-party-builds/v1","builds":[` + fields + `]}`
	}
	valid := `"buildID":"b","surface":"ios-viewer","artifactHash":"` + firstPartyTestHash + `","sequence":1,"distributions":["direct"],"roles":["viewer"]`
	cases := map[string]string{
		"wrong schema":         `{"schema":"beamfall.first-party-builds/v2","builds":[]}`,
		"unknown field":        build(`{` + valid + `,"enrollmentID":"session:x"}`),
		"top-level bindings":   `{"schema":"beamfall.first-party-builds/v1","builds":[],"enrollmentBindings":[]}`,
		"trailing data":        build(`{`+valid+`}`) + `{}`,
		"trailing bracket":     build(`{`+valid+`}`) + `]`,
		"trailing brace":       build(`{`+valid+`}`) + ` }`,
		"empty build id":       build(`{` + strings.Replace(valid, `"buildID":"b"`, `"buildID":""`, 1) + `}`),
		"padded build id":      build(`{` + strings.Replace(valid, `"buildID":"b"`, `"buildID":" b"`, 1) + `}`),
		"overlong build id":    build(`{` + strings.Replace(valid, `"buildID":"b"`, `"buildID":"`+strings.Repeat("b", 257)+`"`, 1) + `}`),
		"web ui surface":       build(`{` + strings.Replace(valid, `"ios-viewer"`, `"core-web-ui"`, 1) + `}`),
		"developer surface":    build(`{` + strings.Replace(valid, `"ios-viewer"`, `"developer-api-client"`, 1) + `}`),
		"uppercase digest":     build(`{` + strings.Replace(valid, firstPartyTestHash, strings.ToUpper(firstPartyTestHash), 1) + `}`),
		"short digest":         build(`{` + strings.Replace(valid, firstPartyTestHash, firstPartyTestHash[:63], 1) + `}`),
		"zero sequence":        build(`{` + strings.Replace(valid, `"sequence":1`, `"sequence":0`, 1) + `}`),
		"no distributions":     build(`{` + strings.Replace(valid, `["direct"]`, `[]`, 1) + `}`),
		"unknown distribution": build(`{` + strings.Replace(valid, `["direct"]`, `["sideload"]`, 1) + `}`),
		"no roles":             build(`{` + strings.Replace(valid, `["viewer"]`, `[]`, 1) + `}`),
		"unknown role":         build(`{` + strings.Replace(valid, `["viewer"]`, `["owner"]`, 1) + `}`),
		"duplicate build id":   build(`{` + valid + `},{` + valid + `}`),
		"non-native floor":     `{"schema":"beamfall.first-party-builds/v1","builds":[],"minimumSequence":{"core-web-ui":1}}`,
		"negative floor":       `{"schema":"beamfall.first-party-builds/v1","builds":[],"minimumSequence":{"ios-viewer":-1}}`,
		"not json":             `schema: v1`,
	}
	for name, manifest := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFirstPartyBuildManifest([]byte(manifest)); err == nil {
				t.Fatalf("manifest accepted, want refusal:\n%s", manifest)
			}
			if _, err := EncodeFirstPartyBuildManifest([]byte(manifest)); err == nil {
				t.Fatal("encode accepted a manifest the parser refuses")
			}
		})
	}
}

func TestFirstPartyBuildCatalogRoundTripsTheLinkTimeValue(t *testing.T) {
	restore := FirstPartyBuildManifest
	t.Cleanup(func() { FirstPartyBuildManifest = restore })

	FirstPartyBuildManifest = ""
	empty, err := FirstPartyBuildCatalog()
	if err != nil || len(empty.Builds) != 0 {
		t.Fatalf("unset manifest = %+v, %v; want an empty catalog", empty, err)
	}

	encoded, err := EncodeFirstPartyBuildManifest([]byte(firstPartyTestManifest))
	if err != nil {
		t.Fatalf("EncodeFirstPartyBuildManifest: %v", err)
	}
	if strings.ContainsAny(encoded, " \t\n'\"") {
		t.Fatalf("encoded value %q cannot be passed through -ldflags -X unquoted", encoded)
	}
	FirstPartyBuildManifest = encoded
	catalog, err := FirstPartyBuildCatalog()
	if err != nil {
		t.Fatalf("FirstPartyBuildCatalog: %v", err)
	}
	want, _ := ParseFirstPartyBuildManifest([]byte(firstPartyTestManifest))
	if catalog.Digest() != want.Digest() {
		t.Fatalf("round-tripped catalog digest %s, want %s", catalog.Digest(), want.Digest())
	}

	FirstPartyBuildManifest = "not base64!"
	if _, err := FirstPartyBuildCatalog(); err == nil {
		t.Fatal("malformed link-time value accepted")
	}
	FirstPartyBuildManifest = base64.StdEncoding.EncodeToString([]byte(`{"schema":"other"}`))
	if _, err := FirstPartyBuildCatalog(); err == nil {
		t.Fatal("link-time value with a foreign schema accepted")
	}
}

func TestEnrollmentBindingForBindsOnlyAnOtherwiseVerifiedBuild(t *testing.T) {
	catalog, err := ParseFirstPartyBuildManifest([]byte(firstPartyTestManifest))
	if err != nil {
		t.Fatal(err)
	}
	contract := ClientContract{Surface: string(SurfaceIOSViewer), BuildID: "ios-viewer-1.0.0-100", ArtifactHash: firstPartyTestHash}
	enrollment := Enrollment{ID: "session:alpha", Role: EnrollmentRoleViewer, Distribution: DistributionAppStore}

	if got := VerifyBuildProvenance(contract, enrollment, catalog); got != ProvenanceEnrollmentMismatch {
		t.Fatalf("catalog alone = %s, want %s (a catalog record grants no enrollment)", got, ProvenanceEnrollmentMismatch)
	}
	binding, finding := EnrollmentBindingFor(contract, enrollment, catalog)
	if finding != ProvenanceVerified {
		t.Fatalf("finding = %s, want verified", finding)
	}
	want := EnrollmentBuildBinding{EnrollmentID: "session:alpha", Surface: SurfaceIOSViewer, BuildID: "ios-viewer-1.0.0-100"}
	if binding != want {
		t.Fatalf("binding = %+v, want %+v", binding, want)
	}
	if len(catalog.EnrollmentBindings) != 0 {
		t.Fatal("EnrollmentBindingFor mutated the caller's catalog")
	}

	bound := catalog.Clone()
	bound.EnrollmentBindings = append(bound.EnrollmentBindings, binding)
	if got := VerifyBuildProvenance(contract, enrollment, bound); got != ProvenanceVerified {
		t.Fatalf("bound enrollment = %s, want verified", got)
	}
	sibling := enrollment
	sibling.ID = "session:beta"
	if got := VerifyBuildProvenance(contract, sibling, bound); got != ProvenanceEnrollmentMismatch {
		t.Fatalf("same-class sibling = %s, want %s", got, ProvenanceEnrollmentMismatch)
	}

	refusals := map[string]struct {
		contract   ClientContract
		enrollment Enrollment
		want       ProvenanceFinding
	}{
		"unknown build":      {ClientContract{Surface: string(SurfaceIOSViewer), BuildID: "ios-viewer-9", ArtifactHash: firstPartyTestHash}, enrollment, ProvenanceUnknownBuild},
		"digest mismatch":    {ClientContract{Surface: string(SurfaceIOSViewer), BuildID: "ios-viewer-1.0.0-100"}, enrollment, ProvenanceArtifactMismatch},
		"wrong distribution": {contract, Enrollment{ID: "session:alpha", Role: EnrollmentRoleViewer, Distribution: DistributionDirect}, ProvenanceNotEntitled},
		"wrong role":         {contract, Enrollment{ID: "session:alpha", Role: EnrollmentRoleManager, Distribution: DistributionAppStore}, ProvenanceNotEntitled},
		"no enrollment id":   {contract, Enrollment{Role: EnrollmentRoleViewer, Distribution: DistributionAppStore}, ProvenanceUnverifiedEnrollment},
		"web surface":        {EmbeddedUIContract(), enrollment, ProvenanceSurfaceMismatch},
	}
	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			binding, finding := EnrollmentBindingFor(tc.contract, tc.enrollment, catalog)
			if finding != tc.want {
				t.Fatalf("finding = %s, want %s", finding, tc.want)
			}
			if binding != (EnrollmentBuildBinding{}) {
				t.Fatalf("refused enrollment produced binding %+v", binding)
			}
		})
	}
}
