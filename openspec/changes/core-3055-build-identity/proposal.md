## Why

CORE-3055 gives operators a way to identify the running Beamfall build from the web top bar
and the CLI.

## What Changes

- Stamp Docker, release and Makefile builds with a UTC date. Use Go's VCS timestamp as the
  fallback for source builds and omit the date when no timestamp is available.
- Expose the build identity in `beamfall version` and in the capabilities object under
  `/health`. The object includes the existing enabled capability IDs.
- Show the version and localized date in the shared desktop and tablet top bar.

## Impact

- Build entry points, Core capability reporting, and the shared web app shell.
- No GraphQL schema or vendored consumer artifact changes.
