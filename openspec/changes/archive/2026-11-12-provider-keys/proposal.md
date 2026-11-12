# Provider keys in setup and a shorter Plugins page

Ticket: CORE-1115 (`queue:core:main`).

## Why

Metadata scrapers such as the Film & TV plugin's need an API key before they fetch details and
artwork. Today the only place to enter one is a table at the bottom of Console > Plugins, labelled
with the raw AuthRef. First-run setup never mentions it, so a new owner scans their library and
gets no artwork. The Plugins page is also long: every plugin's full detail is expanded at once,
followed by two separate scraper and credential tables.

## What Changes

- Group scraper credentials by AuthRef into "provider keys", labelled from the plugin's own scraper
  display names (for example "TMDB (Movies)" and "TMDB (TV)" become "TMDB"). Core hardcodes no
  provider names.
- First-run setup: the Plugins step lists the provider keys that the enabled plugins need, each with
  an optional password field. Keys entered are saved through `setScraperCredential` when the step
  completes. Leaving them blank never blocks setup.
- Console > Plugins: each plugin becomes one compact card that shows its state and any missing key
  at a glance. Provider keys sit at the top of the card. Configuration, permissions, status, actions
  and lifecycle controls move into an expandable details area. The two bottom tables are removed;
  their content now lives in the cards.
- Web-only. No GraphQL schema or server change.

## Non-goals

- Adding "get a key" links: plugins do not declare a sign-up URL yet.
- Changing how secrets are stored or sent. Secrets stay write-only (never returned on the wire).
