# Jellyfin compatibility clean-room corpus

These fixtures are the permitted Jellyfin-side implementation input for ADR-0171. They were
captured on 2026-07-10 against the Jellyfin Project's public, real stable demo instance at
`https://demo.jellyfin.org/stable` (`/System/Info/Public` reported Jellyfin Server 10.11.11).
The demo account is passwordless and explicitly provided for testing. Tokens, server/user/item
IDs, timestamps, network addresses, filesystem paths, and image hashes were replaced with
angle-bracket placeholders while preserving JSON value types. Security headers not relevant to payload conformance
were omitted. No Jellyfin server source was consulted.

The consumer requests were derived from these pinned consumer-source revisions, then replayed
against the real demo server. Consumer source is permitted by ADR-0171 §2.5:

- Seerr `5ae70d05e1ee123b3cda43153ed415754fd8e816`, `server/api/jellyfin.ts`
- Radarr `90d0e26bd1d41c88e7a6bf710219edc818577f8e`,
  `src/NzbDrone.Core/Notifications/MediaBrowser/{MediaBrowserProxy,MediaBrowserSettings}.cs`
- Sonarr `97d24acf06e636ddc888babc3df8f689da879304`, same paths as Radarr
- Lidarr `d93342d8d2cdc81b1c29f2135354f9bab9f4bc7b`,
  `src/NzbDrone.Core/Notifications/MediaBrowser/{MediaBrowser,MediaBrowserProxy,MediaBrowserSettings}.cs`

The only API reference consulted was Jellyfin's published hosted stable OpenAPI document,
`https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json`, fetched 2026-07-10 with SHA-256
`91534097236d67af87df81dc2dabd063b9b5ea5de3386f8f322b2817e2ab47e4`. The OpenAPI file is not
vendored here.

## Findings

### Authentication header

Jellyfin 10.11.11 accepted both the current Seerr header and the legacy header for
`POST /Users/AuthenticateByName`:

- current: `Authorization: MediaBrowser Client="Seerr", ...`
- legacy: `X-Emby-Authorization: MediaBrowser Client="Seerr", ...`

Both returned the same response shape and a bearer token. Current Seerr uses `Authorization`,
not `X-Emby-Authorization`; the legacy fixture is retained because pinned/older deployments may
still send it. Subsequent calls may authenticate with `X-Emby-Token`; current Seerr instead adds
`Token="..."` to `Authorization`.

### Base path

Radarr and Sonarr expose an advanced `UrlBase` setting. It is validated as a URL base, included
in `Address`, and used as the base for `/Items`, `/Notifications/Admin`, and
`/Library/Media/Updated`. They can therefore target `/compat/jellyfin` without root aliases.

Lidarr also exposes `UrlBase`, but its proxy unconditionally appends `/mediabrowser` before every
resource. Setting `/compat/jellyfin` produces `/compat/jellyfin/mediabrowser/...`. Lidarr therefore
needs that nested alias (or an upstream connector change); a root alias is not required. Its shim
is **not identical** to Radarr/Sonarr: it reads `/Library/MediaFolders`, posts either
`/Library/Media/Updated` or `/Library/Refresh`, and does not query `/Items` to match an artist.
The demo edge returned 403 for the legacy `/mediabrowser` paths, confirming they are distinct.

### Replay observations

- Seerr authentication, system info, one-item enumeration, current-user lookup, and image fetch
  returned 200; a 1-pixel primary-image request returned `image/jpeg` (288 bytes).
- Radarr/Sonarr-shaped `/Items` reads returned 200 and `/Library/Media/Updated` returned 204.
- `/Notifications/Admin` returned 404, matching current Jellyfin behavior and the consumers'
  existing warning that notifications should be disabled for Jellyfin.

Each JSON fixture records the normalized request and the response fields relevant to the consumer.
Placeholders are part of the fixture contract and must be supplied with deterministic test values
before replay; omitted response fields are not part of the emulation contract.
