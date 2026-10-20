<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Beamfall Export Format v1.0.0

Beamfall Export Format v1 is a public, machine-readable package for moving a
library and its accumulated state without relying on a running Beamfall server.
The normative terms **MUST**, **MUST NOT**, **SHOULD**, and **MAY** are interpreted
as described by RFC 2119 and RFC 8174.

## Package layout

An export is a directory with this layout:

```text
manifest.json
schema/manifest.schema.json
schema/record.schema.json
records/<entity-class>.jsonl
media/<library-relative-path>
```

The two immutable 1.0.0 schemas MUST be included at the paths shown, making the
package independently validatable. `manifest.json` MUST validate against
`schema/manifest.schema.json`. Each non-empty line in a declared data file is one
complete UTF-8 JSON object and MUST validate against the in-package
`schema/record.schema.json`. A data file MUST contain records for exactly
the `class` declared for it in the manifest. Lines are separated by LF; a final LF
is RECOMMENDED. Exporters MUST NOT rely on line ordering for referential meaning.

All paths are slash-separated and relative to the package root. Absolute paths,
empty path segments, `.` segments, `..` segments, backslashes, symlinks, and paths
outside the package root are forbidden. Importers MUST verify each declared byte
length and SHA-256 digest before consuming the package.

JSON objects with duplicate member names MUST be rejected before schema
validation. `dataFiles` entries MUST be unique by both `class` and `path`;
`mediaObjects` entries MUST be unique by both `mediaID` and `path`; and
`exclusions` entries MUST be unique by `category`. Records MUST be unique by the
pair (`class`, `id`) across the package. Importers MUST reject any violation rather
than selecting a first or last occurrence.

Exporters MUST emit `dataFiles` in the entity-class order listed below, omitting
absent classes, and MUST emit `mediaObjects` ordered by `path` and then `mediaID`.
`exclusions` MUST be ordered by `category`; records within each JSON Lines file
MUST be ordered by `id`. Importers MUST NOT attach semantic meaning to these
orders, but MUST reject a non-canonical package so identical state has one stable
inventory representation.

Media objects retain their library-relative path below `media/`. Record IDs are
opaque, stable strings. Timestamps are UTC RFC 3339 values. Plugin-defined entity
kinds, field names, relation names, and disposition values remain data; they never
extend the format's entity-class vocabulary or schema types.

## Entity classes

Every v1 package declares one JSON Lines file for each class it contains. Empty
classes MAY be omitted, but an implementation claiming full export coverage MUST
account for every applicable state category below.

| Class | Portable state |
| --- | --- |
| `library` | Libraries and their portable configuration. |
| `profile` | Household Profiles. |
| `profilePolicy` | Profile policy and preference configuration. |
| `media` | Media identity, fields, relations, and package-relative media objects. |
| `entity` | Generic plugin-declared entities, typed fields, and relations. |
| `metadataEdit` | User-authored metadata edits and their current value. |
| `disposition` | User-authored or accumulated dispositions. |
| `tag` | Tags and tag hierarchy. |
| `tagAssignment` | Tag assignments to portable objects. |
| `collection` | Collections and sub-collections. |
| `collectionItem` | Ordered collection membership. |
| `marker` | Timeline points and ranges, including known segment kinds. |
| `watchState` | Per-Profile watch state, resume position, ratings, and play counts. |
| `fieldLock` | Metadata overwrite-protection locks. |
| `objectHistory` | Auditable object-history entries and reversible-change context. |

References use `{ "class": "...", "id": "..." }`. Importers MUST reject a
record whose referenced class is unknown. References MAY point forward to later
lines or files, so referential checks occur after all records are read.

## Completeness and exclusions

The manifest is the completeness index. `dataFiles` states the record count and
digest for each included class; `mediaObjects` states the digest, length, and
relative path of every included media object. `exclusions` states intentional
non-portable categories and reasons. Silent omission is not conforming.

Face embeddings and face-recognition templates are permanently excluded: these
derived biometric representations MUST NOT appear in records or media objects.
Device-bound secrets, authentication credentials, signing material, and
non-exportable key material are excluded. An encrypted-library export instead
uses the separately documented key-export ceremony; v1 records MUST NOT contain
secret key bytes. Operational caches and regenerable derived data MAY be excluded
only when the manifest gives a specific reason.

## Household-data disclosure

An administrator-run export contains other household members' Profiles, policy,
watch state, ratings, play history, and curation. Creating or handing off a package
is therefore a disclosure of their personal data. The domestic-use exemption that
may apply inside a home does not automatically cover a shared, hosted, workplace,
or otherwise non-domestic deployment. Exporters MUST present this warning and set
`householdDataDisclosureAcknowledged` only after the administrator acknowledges it.
The flag records acknowledgement; it is not consent on another person's behalf.

## Versioning

`formatVersion` follows Semantic Versioning, but support is negotiated by exact
published versions. An importer supporting 1.0.0 is not required to accept a later
1.x package. Importers MUST ignore unknown object properties only where that exact
version's schema explicitly permits them; the 1.0.0 schemas otherwise reject
undeclared properties, including undeclared payload fields. A minor release may
only make a backward-compatible change that remains valid under every earlier 1.x
schema; adding a class, field, path, or closed-vocabulary value therefore requires
a major release unless an earlier schema explicitly reserved that extension point.
A major release is also required for any breaking layout or semantic change.
Exporters MUST emit one exact version and MUST NOT mix record versions in a package.

Changes are recorded in `CHANGELOG.md`. Both schemas identify immutable version
1.0.0 in their canonical `$id`; every published version's schemas remain immutable.

## Licensing

This specification text is licensed under CC BY 4.0; see `LICENSE-SPEC.txt`.
Schemas and fixtures are licensed under Apache License 2.0; see
`LICENSE-SCHEMAS-FIXTURES.txt`. Beamfall also makes the explicit patent non-assert
in `PATENT-NON-ASSERT.md` for implementations of this specification.
