## 1. Migration and Store

- [x] 1.1 Add events migration `0032_player_playback_attribution.sql` (extend `player_commanded_media` with originator/generation/active columns; add `playback_grant` and `playback_grant_report`) and bump `schemaFreezeEventsMigrations`
- [x] 1.2 Guard engagement writers (`UpsertPlaybackProgressWithProvenance`, `ApplyImportedPlays`, `ApplyOfflineEngagementEvent`, `SetMediaWatched`, `AppendEngagementEvent`, `SetContinueWatchingHidden`, `SetItemWatchlisted`, `SetItemRating`, `SetMediaFavorite`) against `ProfilePlayer`
- [x] 1.3 Record originator + resume position on `ControlRendererSessionForPlayer` (`play-media`), clear active attribution/end grants on `stop`/`endPlayback`/rebind/clear/revoke, and implement `playback_grant` creation + `(grant, seq)` idempotent report resolution in `internal/store`

## 2. Playback Grant Issuance and Auth Check

- [x] 2.1 Add becoming-current originator playback authorization helper in `internal/auth`
- [x] 2.2 Bind `GrantID` in `streamtoken.Grant`, `playback.InfoResponse`, and `server.v1PlaybackInfoResponse`; resolve originator resume position, skip deleted/unauthorized pending entries, and create `playback_grant` rows on `playback.Service.Info`

## 3. Graph, V1, Conformance, and Tests

- [x] 3.1 Add `grantID` and `sequence` arguments to `reportPlayback` and `PUT /v1/media/{mediaID}/progress`, route player display progress/completion through `playback_grant` attribution and originator scrobble consent, and update `GrantedSessions` position lookup
- [x] 3.2 Regenerate wire goldens (`make regen-wire-goldens`) and management manifests (`make mgmt-manifest`) without publishing a wire contract or modifying `contracts/registry.json`
- [x] 3.3 Add store, playback, graph, and server tests covering all D5/D8 requirements and update `TestPlayerDisplaysObservationPrivateItemAndPresence` for Player Profile progress prohibition
