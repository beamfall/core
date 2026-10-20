# Rendering-capability vocabulary v1

`vocabulary.json` is the static core-owned ADR-0138/ADR-0219 Tier-1 artifact.
Its IDs are closed, shape-named, domain-neutral, and mechanically derived from
renderable `Bundle` fields and their closed discriminator values.

Per-client capability profiles are live Client Contract assertions only. They
are not static registry artifacts and are not authoritative outside the running
build whose compatibility decision carries them.
