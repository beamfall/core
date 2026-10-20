# Verification Claims Register

Register version: **1.0.0**
Register date: **2026-10-20**

This register records bounded checks already performed. Each claim is limited to the named revision,
method, and epistemic scope. A passing check is evidence about that check only; it is not a warranty,
or statement about future revisions.

## Scope tags

- `tested-locally`: the named command was executed against the named revision in a local checkout.
- `reviewed`: the named files or configuration were inspected at the named revision.
- `attested-by-operator`: repository origin is stated by the operator; no independent technical
  mechanism establishes that history.

## Claims

### BF-CLAIM-001 — Core architecture and vocabulary checks

- **Checked:** the architecture dependency rules and forbidden core vocabulary encoded by
  `TestArch` and `TestVocab`.
- **Revision/date:** initial public release / 2026-10-20.
- **Method:** `go test
  ./internal/conformance -run '^(TestArch|TestVocab)$' -count=1`.
- **By:** Beamfall maintainers.
- **Scope:** `tested-locally`.
- **Result:** pass. The result covers those two tests at that revision, not every architectural or
  vocabulary property.

### BF-CLAIM-002 — Release provenance data shape

- **Checked:** the release package's SBOM and SLSA provenance structures pass their shape test.
- **Revision/date:** initial public release / 2026-10-20.
- **Method:** `go test ./internal/release
  -run '^TestSBOMAndProvenanceShape$' -count=1`.
- **By:** Beamfall maintainers.
- **Scope:** `tested-locally`.
- **Result:** pass. This tests data construction and validation; it does not establish that a
  published binary was built from the source at this revision.

### BF-CLAIM-003 — CI gate configuration

- **Checked:** `.github/workflows/gate.yml` invokes `make gate` and `make gate-race` on GitHub-hosted
  runners, with a dependent aggregate job.
- **Revision/date:** initial public release / 2026-10-20.
- **Method:** direct review of `.github/workflows/gate.yml` at the named revision.
- **By:** Beamfall maintainers.
- **Scope:** `reviewed`.
- **Result:** configuration present. This is not a claim that either job ran for this revision.

### BF-CLAIM-004 — Repository-origin statement

- **Checked:** the operator-maintained repository statement identifies Beamfall Core as a
  from-scratch implementation with no predecessor-server lineage.
- **Revision/date:** initial public release / 2026-10-20.
- **Method:** review of the provenance statement in `README.md`.
- **By:** the project maintainer for the origin statement; register transcription by Beamfall
  maintainers.
- **Scope:** `attested-by-operator`.
- **Result:** statement present.

## Source-to-binary gap

Source review and source tests do not prove that a distributed binary was produced from the reviewed
source. Release metadata can describe inputs and artifacts, but this register does not independently
reproduce a published binary or verify a source-to-binary match. Version 1 provenance is therefore
operator-attested, not machine-verifiable.
