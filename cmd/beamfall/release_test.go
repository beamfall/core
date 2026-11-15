package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/capabilities"
)

const releaseTestManifest = `{
  "schema": "beamfall.first-party-builds/v1",
  "builds": [
    {"buildID": "macos-manager-1.0.0-100", "surface": "macos-manager", "artifactHash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "sequence": 100, "distributions": ["direct"], "roles": ["manager"]}
  ]
}`

// TestReleaseBuildCatalogValidatesManifestAndEncodedValue (CORE-1395): the
// image build validates BEAMFALL_FIRST_PARTY_BUILDS with `build-catalog
// --catalog` before linking it, so a value --manifest produced passes through
// unchanged and a malformed, empty or build-less one fails with an error instead of a
// server that panics at boot.
func TestReleaseBuildCatalogValidatesManifestAndEncodedValue(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "first-party-builds.json")
	if err := os.WriteFile(manifest, []byte(releaseTestManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := releaseBuildCatalog([]string{"--manifest", manifest}, &encoded); err != nil {
		t.Fatalf("--manifest: %v", err)
	}
	value := strings.TrimSpace(encoded.String())
	if _, err := capabilities.DecodeFirstPartyBuildCatalog(value); err != nil {
		t.Fatalf("--manifest printed an undecodable value %q: %v", value, err)
	}

	var echoed bytes.Buffer
	if err := releaseBuildCatalog([]string{"--catalog", value}, &echoed); err != nil {
		t.Fatalf("--catalog on a valid value: %v", err)
	}
	if strings.TrimSpace(echoed.String()) != value {
		t.Fatalf("--catalog printed %q, want the value back unchanged", echoed.String())
	}

	emptyManifest := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(emptyManifest, []byte(`{"schema":"beamfall.first-party-builds/v1","builds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyCatalog := base64.StdEncoding.EncodeToString([]byte(`{"schema":"beamfall.first-party-builds/v1","builds":[]}`))

	for name, args := range map[string][]string{
		"no builds manifest": {"--manifest", emptyManifest},
		"no builds catalog":  {"--catalog", emptyCatalog},
		"not base64":         {"--catalog", "not base64!"},
		"foreign schema":     {"--catalog", base64.StdEncoding.EncodeToString([]byte(`{"schema":"other"}`))},
		"empty catalog":      {"--catalog", ""},
		"both inputs":        {"--manifest", manifest, "--catalog", value},
		"assemble too":       {"--catalog", value, "--assemble", t.TempDir()},
		"neither input":      {},
		"stray argument":     {"--catalog", value, "extra"},
		"missing file":       {"--manifest", filepath.Join(t.TempDir(), "absent.json")},
	} {
		var out bytes.Buffer
		if err := releaseBuildCatalog(args, &out); err == nil {
			t.Errorf("%s: accepted, printed %q", name, out.String())
		}
	}
}

// TestReleaseBuildCatalogAssemblesClientRecordDirectory (CORE-1395): the build
// record files the client release pipelines publish combine into the one
// manifest --manifest then links, and a duplicate build across files, a
// malformed record or an empty directory fails instead of shipping a partial
// catalog.
func TestReleaseBuildCatalogAssemblesClientRecordDirectory(t *testing.T) {
	const tvRecord = `{"schema": "beamfall.first-party-builds/v1", "builds": [
    {"buildID": "tvos-viewer-1.0.0-100", "surface": "tvos-viewer", "artifactHash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "sequence": 100, "distributions": ["appStore"], "roles": ["viewer"]}
  ]}`
	writeRecords := func(t *testing.T, files map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	dir := writeRecords(t, map[string]string{"macos.json": releaseTestManifest, "tvos.json": tvRecord, "README.txt": "not a record"})
	var assembled bytes.Buffer
	if err := releaseBuildCatalog([]string{"--assemble", dir}, &assembled); err != nil {
		t.Fatalf("--assemble: %v", err)
	}
	catalog, err := capabilities.ParseFirstPartyBuildManifest(assembled.Bytes())
	if err != nil || len(catalog.Builds) != 2 || catalog.Builds[0].BuildID != "macos-manager-1.0.0-100" {
		t.Fatalf("--assemble printed %s (%v), want both records in name order", assembled.String(), err)
	}
	manifest := filepath.Join(t.TempDir(), "first-party-builds.json")
	if err := os.WriteFile(manifest, assembled.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := releaseBuildCatalog([]string{"--manifest", manifest}, &bytes.Buffer{}); err != nil {
		t.Fatalf("--manifest rejected the assembled manifest: %v", err)
	}

	for name, files := range map[string]map[string]string{
		"duplicate build": {"macos.json": releaseTestManifest, "macos-again.json": releaseTestManifest},
		"malformed":       {"macos.json": releaseTestManifest, "bad.json": `{"schema":"beamfall.first-party-builds/v1","builds":[{"buildID":"x"}]}`},
		"no records":      {"README.txt": "not a record"},
	} {
		var out bytes.Buffer
		if err := releaseBuildCatalog([]string{"--assemble", writeRecords(t, files)}, &out); err == nil {
			t.Errorf("%s: assembled %q", name, out.String())
		}
	}
	if err := releaseBuildCatalog([]string{"--assemble", filepath.Join(t.TempDir(), "absent")}, &bytes.Buffer{}); err == nil {
		t.Error("missing directory accepted")
	}
}

// TestReleaseScriptRequiresFirstPartyBuilds (CORE-1395): script/release.sh
// fails before any build work when FIRST_PARTY_BUILDS is unset or names a
// missing file, and its local-build opt-out cannot be combined with a signed
// release. It runs a copy of the script in a scratch root, so a regression that
// got past the check would only touch that root.
func TestReleaseScriptRequiresFirstPartyBuilds(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "script", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	script := filepath.Join(root, "script", "release.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, source, 0o600); err != nil { //nolint:gosec // G703: a path under the test's own TempDir
		t.Fatal(err)
	}
	run := func(env ...string) (int, string) {
		cmd := exec.Command(bash, script) //nolint:gosec // G204: the test's own copy of script/release.sh
		cmd.Dir = root
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "VERSION=v0.0.0-test"}, env...)
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return 0, string(out)
		case errors.As(err, &exit):
			return exit.ExitCode(), string(out)
		default:
			t.Fatalf("run release.sh: %v", err)
			return 0, ""
		}
	}

	for name, tc := range map[string]struct {
		env  []string
		want string
	}{
		"unset":               {nil, "FIRST_PARTY_BUILDS is required"},
		"missing manifest":    {[]string{"FIRST_PARTY_BUILDS=" + filepath.Join(root, "absent.json")}, "first-party build manifest not found"},
		"opt-out when signed": {[]string{"BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS=1", "BEAMFALL_RELEASE_SIGNING_KEY=placeholder"}, "signed release cannot opt out"},
	} {
		code, out := run(tc.env...)
		if code != 2 || !strings.Contains(out, tc.want) {
			t.Errorf("%s: exit %d, output %q; want exit 2 mentioning %q", name, code, out, tc.want)
		}
	}

	// The opt-out is a warning for a local build; the copy then fails later
	// for want of the web app, which proves it got past the check.
	if _, out := run("BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS=1"); !strings.Contains(out, "allowed by BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS=1") {
		t.Errorf("opt-out did not warn: %q", out)
	}
}
