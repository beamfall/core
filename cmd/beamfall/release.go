package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/release"
)

// releaseCommand is the supply-chain integrity surface (PANEL-11, ADR-0013 §A):
//
//	beamfall release attest --dist DIR --version V --channel C --commit SHA --timestamp TIME --invocation-id ID [--artifact NAME ...]
//	beamfall release verify --dist DIR
//	beamfall release build-catalog --manifest FILE | --catalog VALUE | --assemble DIR
//
// `attest` emits a signed release-manifest.json plus an SPDX SBOM and a SLSA
// provenance attestation; `verify` is the fail-closed upgrade/CI gate that
// rejects an unsigned or tampered dist against the pinned trust anchor.
func releaseCommand(_ context.Context, args []string, stdout io.Writer) error {
	if helpArg(args) {
		_, err := fmt.Fprint(stdout, releaseHelp)
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("release subcommand is required (attest|verify|build-catalog)\n\n%s", releaseHelp)
	}
	switch args[0] {
	case "attest":
		return releaseAttest(args[1:], stdout)
	case "verify":
		return releaseVerify(args[1:], stdout)
	case "build-catalog":
		return releaseBuildCatalog(args[1:], stdout)
	default:
		return fmt.Errorf("unknown release subcommand %q (want attest|verify|build-catalog)", args[0])
	}
}

const releaseHelp = `Usage: beamfall release <command>

Commands:
  attest          write release manifest, SBOM, and provenance
  verify          verify a release dist against the signed manifest
  build-catalog   validate a first-party native build manifest (--manifest) or an encoded
                  link-time value (--catalog) and print the link-time value, or combine
                  per-client build record files (--assemble DIR) into one manifest
`

// releaseBuildCatalog validates the first-party native build manifest a release
// links into the server (ADR-0063 build provenance) and prints the value for
// -X github.com/beamfall/core/internal/capabilities.FirstPartyBuildManifest.
// A malformed manifest fails the release rather than shipping a binary that
// would refuse every native client at boot. With --catalog it instead
// validates an already-encoded value and prints it back, which the image build
// runs on BEAMFALL_FIRST_PARTY_BUILDS before linking it (CORE-1395). With
// --assemble it combines the build record files the client release pipelines
// published for this Core release into the one manifest and prints it as JSON
// (docs/release/first-party-builds.md).
func releaseBuildCatalog(args []string, stdout io.Writer) error {
	fs := commandFlagSet("beamfall release build-catalog", stdout)
	manifestPath := fs.String("manifest", "", "first-party build manifest JSON ("+capabilities.FirstPartyBuildManifestSchema+")")
	catalog := fs.String("catalog", "", "validate an encoded link-time value (the output of --manifest) instead")
	assembleDir := fs.String("assemble", "", "combine the per-client build record files (*.json) in this directory into one manifest, printed as JSON")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	modes := 0
	for _, name := range []string{"manifest", "catalog", "assemble"} {
		if set[name] {
			modes++
		}
	}
	switch {
	case modes > 1:
		return fmt.Errorf("--manifest, --catalog and --assemble are mutually exclusive")
	case set["assemble"]:
		return assembleFirstPartyBuildRecords(*assembleDir, stdout)
	case set["catalog"]:
		value := strings.TrimSpace(*catalog)
		if value == "" {
			return fmt.Errorf("--catalog is empty: an unset first-party build catalog refuses every native client")
		}
		decoded, err := capabilities.DecodeFirstPartyBuildCatalog(value)
		if err != nil {
			return err
		}
		if err := requireFirstPartyBuilds(decoded); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, value)
		return err
	case *manifestPath == "":
		return fmt.Errorf("--manifest, --catalog or --assemble is required")
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	parsed, err := capabilities.ParseFirstPartyBuildManifest(data)
	if err != nil {
		return err
	}
	if err := requireFirstPartyBuilds(parsed); err != nil {
		return err
	}
	encoded, err := capabilities.EncodeFirstPartyBuildManifest(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, encoded)
	return err
}

// requireFirstPartyBuilds refuses a catalog with no builds at the release
// boundary: it parses, but a server linked with it refuses every native client
// just as an unset one does (CORE-1395). The library still accepts an empty
// catalog for development builds.
func requireFirstPartyBuilds(catalog capabilities.BuildCatalog) error {
	if len(catalog.Builds) == 0 {
		return fmt.Errorf("first-party build catalog has no builds: it would refuse every native client")
	}
	return nil
}

// assembleFirstPartyBuildRecords reads every *.json build record file in dir,
// in name order, and prints the assembled first-party build manifest.
func assembleFirstPartyBuildRecords(dir string, stdout io.Writer) error {
	if dir == "" {
		return fmt.Errorf("--assemble needs a directory of build record files")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []capabilities.FirstPartyBuildRecordFile
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := root.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		files = append(files, capabilities.FirstPartyBuildRecordFile{Name: entry.Name(), Data: data})
	}
	if len(files) == 0 {
		return fmt.Errorf("--assemble: no *.json build record files in %s", dir)
	}
	manifest, err := capabilities.AssembleFirstPartyBuildManifest(files)
	if err != nil {
		return err
	}
	_, err = stdout.Write(manifest)
	return err
}

func releaseAttest(args []string, stdout io.Writer) error {
	fs := commandFlagSet("beamfall release attest", stdout)
	var additionalArtifacts stringListFlag
	var (
		dir           = fs.String("dist", "", "dist directory holding SHA256SUMS and artifacts")
		version       = fs.String("version", "", "release version")
		channel       = fs.String("channel", "stable", "release channel (stable|beta)")
		commit        = fs.String("commit", "", "source git commit (provenance material)")
		timestamp     = fs.String("timestamp", "", "release identity timestamp (RFC3339)")
		invocationID  = fs.String("invocation-id", "", "CI workflow-run identity")
		allowUnsigned = fs.Bool("allow-unsigned", false, "DEV ONLY: write an unsigned manifest (release verify will reject it)")
	)
	fs.Var(&additionalArtifacts, "artifact", "additional pre-existing dist artifact to bind (repeatable)")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if *dir == "" || *version == "" {
		return fmt.Errorf("release attest: --dist and --version are required")
	}
	switch *channel {
	case "stable", "beta":
	default:
		return fmt.Errorf("release attest: --channel must be stable or beta")
	}

	seed, err := release.DecodeSeed(os.Getenv("BEAMFALL_RELEASE_SIGNING_KEY"))
	if err != nil {
		return err
	}
	if len(seed) == 0 && !*allowUnsigned {
		return fmt.Errorf("release attest: BEAMFALL_RELEASE_SIGNING_KEY is unset — refusing to produce an unsigned release (pass --allow-unsigned for a dev build)")
	}
	if len(seed) > 0 && (*commit == "" || *timestamp == "" || *invocationID == "") {
		return fmt.Errorf("release attest: signed releases require --commit, --timestamp, and --invocation-id")
	}
	if *timestamp == "" {
		*timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	written, err := release.Attest(release.AttestOptions{
		Dir:                 *dir,
		Version:             *version,
		Channel:             *channel,
		Commit:              *commit,
		Timestamp:           *timestamp,
		InvocationID:        *invocationID,
		AdditionalArtifacts: additionalArtifacts,
		SigningSeed:         seed,
		AllowUnsigned:       *allowUnsigned,
	})
	if err != nil {
		return err
	}
	if len(seed) == 0 {
		fmt.Fprintln(stdout, "WARNING: produced an UNSIGNED release (dev build); release verify will reject it")
	}
	for _, name := range written {
		fmt.Fprintf(stdout, "wrote %s\n", name)
	}
	return nil
}

type stringListFlag []string

func (v *stringListFlag) String() string { return fmt.Sprint([]string(*v)) }

func (v *stringListFlag) Set(value string) error {
	*v = append(*v, value)
	return nil
}

func releaseVerify(args []string, stdout io.Writer) error {
	fs := commandFlagSet("beamfall release verify", stdout)
	dir := fs.String("dist", "", "dist directory to verify")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("release verify: --dist is required")
	}
	if err := release.VerifyDist(*dir); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "release verify: signature OK, all artifact digests match the signed manifest")
	return nil
}
