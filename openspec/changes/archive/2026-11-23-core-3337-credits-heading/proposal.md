# Credits heading from the plugin's kind label

Ticket: CORE-3337 (`queue:core:main`).

## Why

The media detail page heads every row of person-archetype credits with the fixed string "People".
On a music track the only credit is the artist, so a band such as Oasis appears under "People".
The plugin already declares the label it wants for that kind (the music plugin's `artist` kind has
the plural display name "Artists"), but media credits do not carry it, so clients cannot use it.

## What Changes

- `MediaCredit` gains `kindPluralDisplayName`: the credit kind's declared plural display name,
  omitted when the kind declares none. Additive wire field; needs a client-core-wire minor publish.
- The web media detail page heads the people row with that plural label when every
  person-archetype credit on the item shares one kind that declares it, and with "People"
  otherwise. Film and TV keep "People" because their person kind's plural is "People".
- Core stays domain-agnostic: the label comes from plugin data, never from a core string.

## Impact

- `internal/graph` (`MediaCreditInfo`), wire-contract goldens.
- `internal/web/app` (credit type, media query, `CastRow`).
- Native clients may adopt the field later; they keep their current headings.
