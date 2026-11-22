# Design: online subtitle search (CORE-1690)

## Decision: operator-supplied key, off by default (owner, 2026-11-21)

Beamfall ships no OpenSubtitles API key and online subtitle search is off by default. The key
resolves only from the operator's secrets file. The release-injected `BundledCredentials` source
that scrapers use is deliberately not passed to subtitle search: `wireSubtitleSearch` takes the
plain secrets-file source, so a key a release build carried under `opensubtitles-api-key` is never
read. Keys never live in plaintext config.

## Enabling search (operator)

1. Create an OpenSubtitles.com account and an API consumer to get an API key.
2. Write the key to `<data-dir>/secrets/opensubtitles-api-key` with mode `0600`
   (for example `install -m 0600 /dev/null <file>` and then write the key into it).
3. Set `BEAMFALL_SUBTITLE_SEARCH_PROVIDERS=opensubtitles` (or pass
   `--subtitle-search-providers=opensubtitles`) and restart the server.

Until step 3 the server makes no subtitle request. With a provider listed but no key file, the
status reports the provider as needing a key and still makes no request. An explicit empty
`--subtitle-search-providers=` turns search off even when the environment enables it.

## Egress and storage

Requests ride the hardened outbound proxy under a core-owned policy limited to the provider's
API and download origins, public addresses only, with no install identifier or telemetry.
Downloads are written as a new numbered `.srt` beside the media file through a root held on the
containing Library root, never overwriting an existing file, and are recorded as sidecar rows with
an activity entry.
