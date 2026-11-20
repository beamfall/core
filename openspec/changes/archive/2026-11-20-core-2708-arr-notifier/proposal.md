# Compatibility notifier routes for the *arr tools (CORE-2708)

## Why

Radarr, Sonarr and Lidarr ship an Emby/Jellyfin connection that tests a token and then tells the
server which paths changed. Today their connection test fails against Beamfall because the routes
they call do not exist. ADR-0171 limits this surface to automation: no streaming, playback or
filesystem disclosure.

## What changes

- Accept the integration bearer in `X-MediaBrowser-Token`, which the *arr tools send.
- Add GET `System/Configuration` (read scope) returning an empty object.
- Add POST `Notifications/Admin` (read scope) returning 404 after authentication, so the tools
  report that admin notifications are unsupported instead of a silent false success.
- Add GET `Library/MediaFolders` and the `Items` path lookup (read scope), each returning an empty
  item list so no server path is disclosed.
- Add POST `Library/Media/Updated` (refresh scope): map each changed path to the eligible Library
  root that contains it and queue one scan per matched root through the existing refresh limiter
  and per-root scan queue. Always return 204.
- Serve the notifier routes under the `mediabrowser/` alias Lidarr 3.x uses.
