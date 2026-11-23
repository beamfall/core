## 1. Migration, Outbox, and Store

- [x] 1.1 Add events migration `0032_party_screen_eligibility.sql` (`auth_session_id`, `device_id`, `binding_generation`, `chosen_lead` on `party_screen`) and bump `schemaFreezeEventsMigrations`
- [x] 1.2 Add `OutboxAggregateParty` / `PartyStageEventKind` / `PartyStageEventPayload` in `internal/store/outbox.go` and filter/drop for integration workers
- [x] 1.3 Update `internal/store/party.go`, `player_controllers.go`, `player_binding.go`, `reveal.go`, `profile_entry_pin.go`, `store.go`, and `renderer_session.go` for session-bound screen ownership, per-screen eligibility, player display join/lead, controller re-checks, D4 `ErrDisplayInParty` / `stop` release, rebind/revoke/relock/switchProfile cleanup, and `IsPlayerPartyCurrentMedia`

## 2. SSE Events, Playback Gate, and Graph API

- [x] 2.1 Update `internal/server/events.go` and `internal/server/server.go` for session-bound `party.stage` SSE delivery, `?screenID=` ownership refusal, live controller check on delivery, and walk-up-off party current media admission
- [x] 2.2 Update `internal/graph/graph_party.go`, `graph_sessioncontrol.go`, `graph_player.go`, `graph.go`, `graph_dispatch.go`, `nested_schema.go`, `graph_arguments.go`, and `error_codes.go` for `partyStage`, `partyScreenAddDisplay`, `partyScreenLead`, `PartySessionInfo.availableDisplays`, `PlayerDisplayInfo.inParty`, `relock` grant cancellation, and `DISPLAY_IN_PARTY`
- [x] 2.3 Regenerate wire goldens and management manifests (`make regen-wire-goldens`, `make mgmt-manifest`) without publishing a wire contract or modifying `contracts/registry.json`

## 3. Tests and Verification

- [x] 3.1 Add store, graph, and server tests covering session-bound stage reads (multiple registrations per Session, refusal on another Session), per-screen `PlaybackFilter`/`LibraryFilter` eligibility, `relock`/`switchProfile` metadata and grant cancellation, host controller-list filtering and Library coverage at join, lead eligibility and pop-out fallback, controller-liveness re-check, D4 `in party` refusal vs `stop` release, all release triggers, and guest exclusion
