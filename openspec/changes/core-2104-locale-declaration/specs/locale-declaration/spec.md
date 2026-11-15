## ADDED Requirements

### Requirement: One declaration lists the shipped locales

Core SHALL publish `contracts/locales/locales.json` listing the source locale and every locale
with its BCP-47 tag, English name, native name, text direction and status. Every client SHALL
derive the languages it offers and the locales its l10n check requires from a byte-identical
vendored copy, and SHALL NOT keep a hand-maintained locale list.

#### Scenario: Adding a language

- **WHEN** a locale entry is added to the declaration and vendored into the web app
- **THEN** the web app's locale list and its catalog checks include that locale with no other list edited

### Requirement: Shipped locales are complete and in-progress locales are never offered

A locale with status `shipped` SHALL have every source key translated, and the catalog check SHALL
fail when one is missing. A locale with status `in-progress` SHALL be built and key-parity checked
but SHALL NOT be offered to users, and its missing keys SHALL fall back to the source locale per key.

#### Scenario: In-progress locale

- **WHEN** a locale is declared `in-progress` with some keys untranslated
- **THEN** it is absent from the language picker and an untranslated key renders the source text
