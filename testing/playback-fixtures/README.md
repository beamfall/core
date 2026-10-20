# Playback fixture manifest policy

`manifest.json` is the workspace-owned pinned playback-fixture matrix. Its initial rows preserve the
ADR-0128 Required Playback Fixture Matrix shape: fixture ID, SKU/host class, file characteristics,
expected engine, expected Core pipeline/decision, automation class, evidence source, and integrity.

## Schema and integrity

`schemaVersion` changes whenever a consumer-visible field or enum changes. Every fixture row must
contain all objects currently present in the manifest. Consumers must reject unknown schema versions,
duplicate IDs, missing required fields, and values outside their supported engine, pipeline,
automation, artifact-status, or source-kind vocabulary.

`artifact.status` is either `required-missing` or `present`:

- `required-missing` requires both `artifact.path` and `artifact.sha256` to be `null`. Such a row
  reserves and specifies a required fixture but cannot contribute a passing profile or release-gate
  result.
- `present` requires a repository-relative path below `testing/playback-fixtures/` and a lowercase
  64-hex-character SHA-256 of the exact artifact bytes. Consumers must hash the file before use and
  fail closed on a missing file, path escape, symlink escape, or mismatch. Derived rows may point at
  the same artifact as their source row but must still verify its hash.

Hashes are updated only when the synthesized fixture intentionally changes. The fixture change,
manifest hash change, expected-decision diff, and any generated Capability Profile diff are reviewed
together. This is the ADR-0121 deterministic pinned-fixture rule; retry never substitutes for a hash
or decision mismatch.

## Corpus growth

Every user-reported unplayable file must produce a minimized, synthesized, non-identifying fixture and
a manifest row before the related fix merges. The row records the reproducing file characteristics,
host class, expected engine and server decision, and automation class. If minimization removes the
failure, preserve a deterministic synthetic reconstruction recipe rather than user media.

Commercial clips and user files are never redistributed. Dolby Vision and HDR fixtures must be built
from generated images/audio: author or inject test metadata (for example a `dovi_tool` RPU or HDR10+
metadata) into a synthesized base stream. If faithful automated validation genuinely requires a real
device, use ADR-0121 §7: name the manual residual, track its roadmap ticket, and require fresh evidence.
A passing fake must never stand in for that residual.

## Engine bumps and evidence

Every mpv, FFmpeg, libplacebo, native-engine, or other playback-engine bump reruns every applicable
`present` row against the exact bundled build and mechanically re-derives its Capability Profile.
Profile deltas are reviewed and never auto-widened. The bump cannot merge when a required row fails,
has invalid integrity, or the generated profile is stale. CI wiring for this policy lands with
PLAYENG-3.

At run time, replace `host.evidenceSku: "record-at-run"` with the exact representative hardware SKU
in the immutable run evidence; do not mutate the pinned host class in the manifest. Automated-plus-
manual rows pass release only when both automated assertions and fresh named device evidence pass.
