# CLA Propagation Checklist

Use this checklist for plugin-sdk and every plugin-* repository. Propagation is per-repository follow-up work, not part of the Beamfall Core ticket that introduced this template.

For each target repository:

- Copy Beamfall Core `CLA.md` to the repository root as `CLA.md`.
- Copy `cla/plugin-CONTRIBUTING-template.md` to the repository root as `CONTRIBUTING.md`.
- Ensure `CONTRIBUTING.md` says external contributions require an accepted signed CLA before review.
- Ensure `CONTRIBUTING.md` references `CLA.md`.
- Ensure `CLA.md` references `CONTRIBUTING.md` or the contribution gate.
- Add or adapt a repository-local lint check so `CONTRIBUTING.md` and `CLA.md` both exist and reference each other.
- Run that repository's normal gate.
- Record the evidence path and SHA-256 in the per-repository result.

Initial target set:

- beamfall-plugin-sdk
- beamfall-plugin-audiobook
- beamfall-plugin-enrichment
- beamfall-plugin-familyarchive
- beamfall-plugin-filmtv
- beamfall-plugin-music
- beamfall-plugin-photos
- beamfall-plugin-podcasts
