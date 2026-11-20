# Tasks (CORE-2708)

- [x] Accept `X-MediaBrowser-Token` and verify header precedence and the uniform 401.
- [x] Add the read-scope configuration, admin notification, media folder and item lookup routes and verify none returns a server path.
- [x] Add path-scoped `Library/Media/Updated` refresh through the shared limiter and per-root scan queue, with bounded body decoding.
- [x] Add the `mediabrowser/` aliases and update the route inventory test.
- [x] Verify the connection tests of the pinned Radarr, Sonarr and Lidarr containers.
- [x] Record the notifier decisions in ADR-0171 §2.8.
