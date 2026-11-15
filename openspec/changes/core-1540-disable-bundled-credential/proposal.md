# Disable stops a scraper that has a built-in key

Ticket: CORE-1540 (`queue:core:main`).

## Why

On an official build, the Console's Disable control for a scraper credential deletes the owner's
secret, and the scraper then falls back to the credential bundled into the build (CORE-1129). The
owner presses Disable and the scraper keeps sending requests with the built-in key. The Console
offers no way to stop it.

## What Changes

- `disableScraperCredential` stops the AuthRef: on a build that bundles a default for it, the
  server records a per-AuthRef opt-out (an empty `0600` marker beside the secret files) and stops
  resolving the bundled default. The scraper treats it as not configured and sends no request.
  The mutation stays audited.
- Saving a new owner key lifts the opt-out. Clearing an owner key some other way still falls back
  to the bundled default, as before.
- The Console reports a disabled AuthRef with `credentialStatus` "disabled" (an existing open
  status string, so the wire schema does not change). The Disable control says it does not fall
  back to a built-in key, and a disabled key is not flagged as needed.
- A build with no bundled default for the AuthRef behaves as before: Disable clears the owner's
  secret and the status is "missing".

## Impact

- Code: `internal/app` (credential source), `internal/graph` (disable mutation and status),
  `internal/web/app` (provider-key row copy and status).
- No GraphQL or wire-contract change. Going back to the built-in key after disabling it would need
  a new mutation, which is out of scope here.
