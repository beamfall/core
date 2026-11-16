# Name the credential in the Console plugin row and web provider keys, never by ref

Ticket: CORE-2190 (`queue:core:main`).

## Why

CORE-2187 named a missing credential by plugin and label in scan status. Two owner-visible places
still showed the raw `bfplugin-` ref: the plugin Credential status row's detail, and the web
provider key label when a key has no scraper display names.

## What Changes

- The `status` row `credential-<ref>` carries `<plugin display name>: <label>` in `detail` (label
  from the scraper's `CredentialHelp`, else "provider key"). Wire shape unchanged. No native client
  reads `PluginStatusInfo`; the web Console renders `detail` as text.
- The web provider key label is empty when no display name exists, and the UI shows a localized
  generic name (`pluginKeyFieldGeneric`, `pluginKeyProviderGeneric`) in all shipped locales.
