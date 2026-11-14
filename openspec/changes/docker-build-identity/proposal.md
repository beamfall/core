# Stamp the build identity into the Docker image

Ticket: CORE-1096 (`queue:core:main`).

## Why

An image built from the repo Dockerfile reports version and commit `development`. The Dockerfile
links no version, and the build context excludes `.git`, so the binary has no VCS revision
either. An operator cannot tell which commit a container runs, or confirm that an update or a
rollback took effect.

## What Changes

- The Dockerfile takes `BEAMFALL_VERSION` and `BEAMFALL_COMMIT` build args. They are linked into
  `capabilities.ReleaseVersion` and a new `capabilities.ReleaseCommit`, which `beamfall version`
  reports and which takes precedence over the build-info VCS revision.
- The image carries the OCI `org.opencontainers.image.version` and `.revision` labels, and exports
  the commit as `BEAMFALL_BUILD_SHA`, which `/health` reports as `buildSHA`.
- Without the args the version is `development+src.<digest of the Go sources>` and the commit is
  `development`: an unstamped build is labelled distinctly from a release, and two unstamped
  images built from different trees report different versions.

## Non-goals

- The release workflow's bake target does not pass the args yet, and the graph `serverInfo`
  version is a constant; both are follow-ups outside this ticket's paths.
