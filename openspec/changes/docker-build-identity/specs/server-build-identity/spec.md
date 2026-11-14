## ADDED Requirements

### Requirement: A Docker image identifies the build it was made from

An image built from the repo Dockerfile SHALL report the version and commit given as the
`BEAMFALL_VERSION` and `BEAMFALL_COMMIT` build args through `beamfall version`, `/health`'s
`buildSHA` and the OCI version and revision labels. An image built without them SHALL report a
version that is not a release number and that is derived from the source tree.

#### Scenario: Stamped build

- **WHEN** the image is built with `BEAMFALL_VERSION=v1.0.0` and `BEAMFALL_COMMIT=<sha>`
- **THEN** `beamfall version` reports version `v1.0.0` and commit `<sha>`, and `/health` reports
  `buildSHA` `<sha>`

#### Scenario: Unstamped build

- **WHEN** the image is built without the build args
- **THEN** the version is `development+src.<source digest>` and the commit is `development`, and
  the version check never treats it as a release
