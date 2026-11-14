# Release image build (.github/workflows/release.yml runs this through
# docker/bake-action). Each variable is overridden by the environment variable
# of the same name; an empty value falls back like the shell's ${VAR:-default}.
#
# VERSION   release version (the tag), also linked into `beamfall version`
# CHANNEL   release channel for the tags
# COMMIT    source commit; empty falls back to GITHUB_SHA, which GitHub
#           Actions sets for every step
variable "VERSION" {
  default = ""
}

variable "CHANNEL" {
  default = ""
}

variable "COMMIT" {
  default = ""
}

variable "GITHUB_SHA" {
  default = ""
}

group "default" {
  targets = ["beamfall-core"]
}

target "beamfall-core" {
  dockerfile = "Dockerfile"
  context    = "."
  platforms  = ["linux/amd64", "linux/arm64"]
  tags = [
    "ghcr.io/beamfall/core:${CHANNEL != "" ? CHANNEL : "stable"}",
    "ghcr.io/beamfall/core:${VERSION != "" ? VERSION : "development"}-${CHANNEL != "" ? CHANNEL : "stable"}",
  ]

  # Build identity (CORE-1533, using the Dockerfile args from CORE-1096): the
  # image's `beamfall version`, /health buildSHA and OCI version/revision
  # labels carry the release version and commit. Left empty, the Dockerfile
  # labels the build development+src.<source digest>, never a release.
  args = {
    BEAMFALL_VERSION = VERSION
    BEAMFALL_COMMIT  = COMMIT != "" ? COMMIT : GITHUB_SHA
  }

  # Supply-chain attestations for the server OCI image (PANEL-11, ADR-0013 §A):
  # BuildKit emits an in-toto SBOM (SPDX) and SLSA provenance (mode=max records
  # the full build graph) attached to the image index, so a puller can inspect
  # exactly what the image contains and how it was built. The image is also
  # cosign-signed at push time in .github/workflows/release.yml; the signature is
  # what the blessed unattended-upgrade path (Watchtower-style) verifies.
  attest = [
    "type=sbom",
    "type=provenance,mode=max",
  ]
}
