# Name a missing provider credential by its plugin and label

Ticket: CORE-2187 (`queue:core:main`).

## Why

A scan that finds a provider credential missing ends with the job message
`provider credential not configured (bfplugin-<hex>-<hex>)`. The id is the internal scoped
credential ref, a hex encoding of the plugin id and the credential's local name. The first-run
setup wizard and the console show that message as scan status, so the owner sees an opaque id
instead of which plugin needs which key, and no hint of where to add it.

## What Changes

- The completed scan job's message names each missing credential as
  `<plugin display name>: <credential label>`, using the label the scraper's `CredentialHelp`
  declares, or `provider key` when it declares none, and says to add it in the console under
  Plugins. Credentials that share a label are named once.
- A ref no registered plugin declares is named generically. The status text never carries the
  internal ref.
- The scan's one missing-credential WARN adds a `provider` field with the same name. It keeps the
  `credential` ref, which the operator needs to name the secret file.
- `internal/scan` gains an optional `CredentialLabeler` capability on its `Identifier` seam; the
  composition root's adapter implements it from the plugin registry.

## Impact

- `internal/scan` and `internal/app`. No schema, migration, wire field or web change: the job's
  progress message text changes.
