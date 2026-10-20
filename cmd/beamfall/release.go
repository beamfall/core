package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/beamfall/core/internal/release"
)

// releaseCommand is the supply-chain integrity surface (PANEL-11, ADR-0013 §A):
//
//	beamfall release attest --dist DIR --version V --channel C --commit SHA --timestamp TIME --invocation-id ID [--artifact NAME ...]
//	beamfall release verify --dist DIR
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
		return fmt.Errorf("release subcommand is required (attest|verify)\n\n%s", releaseHelp)
	}
	switch args[0] {
	case "attest":
		return releaseAttest(args[1:], stdout)
	case "verify":
		return releaseVerify(args[1:], stdout)
	default:
		return fmt.Errorf("unknown release subcommand %q (want attest|verify)", args[0])
	}
}

const releaseHelp = `Usage: beamfall release <command>

Commands:
  attest   write release manifest, SBOM, and provenance
  verify   verify a release dist against the signed manifest
`

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
