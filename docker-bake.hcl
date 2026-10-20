group "default" {
  targets = ["beamfall-core"]
}

target "beamfall-core" {
  dockerfile = "Dockerfile"
  context    = "."
  platforms  = ["linux/amd64", "linux/arm64"]
  tags = [
    "ghcr.io/beamfall/core:${CHANNEL:-stable}",
    "ghcr.io/beamfall/core:${VERSION:-development}-${CHANNEL:-stable}",
  ]

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
