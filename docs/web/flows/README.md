# Web app flows and sitemap

A Corvint flow-documentation record for Core's React web app (`internal/web/app`), served under
`/web/`. It names every user flow, the source declarations that implement it, the backend calls it
makes and the end-to-end specs that cover it. The generated pages are lexical source
documentation: Corvint's claims are bounded observations of the TypeScript source, not accepted
intent or runtime proof.

| File | What it is |
|---|---|
| [`inventory.json`](inventory.json) | Declared flows (`corvint-flow-inventory/1`): symbol-level members and entry points. Hand-curated; edit it to add or move a flow. |
| [`appmap.json`](appmap.json) | Application-map manifest (`application-map-manifest/0`): the React routers and the Playwright spec root. |
| [`map.json`](map.json) | `corvint flows appmap build` output (`application-map/0`): screens, spec joins and unknowns. |
| [`generated/`](generated/) | `corvint docs flows generate` output: eight Markdown pages per generated flow (linked from the table below), `provider.json`, and [`generation.json`](generated/generation.json), the record of which members resolved and which flows generated. |

## Regenerate and check

Corvint reads the inventory and manifest from Git, so commit edits to either before regenerating.

```sh
make flow-docs        # regenerate generated/ and map.json at HEAD
make flow-docs-check  # read-only: exit 1 when the source drifted from the committed record
```

Both run [`script/flow-docs.sh`](../../../script/flow-docs.sh) and need `corvint` (with
`corvint-docs`) on `PATH`. Neither is part of `make gate`: a drifted record is a documentation
follow-up, not a merge blocker. The record was generated at the revision recorded in
`generated/generation.json` (`source.revision`) with Corvint 1.0.0-rc.3.dev (build 176).

Summary at that revision: 88 flows, 85 generated; 2,387 declared members, 750 resolved and
1,637 unresolved; 214 in-scope declarations unassigned (unit tests, e2e helpers and the Vite
service-worker plugin, none of them user flows). The application map has 44 screens and 0
edges.

## Flows

Columns: **Route / screen** is the URL under `/web` (or the surface for cross-cutting flows).
**Backend calls** are GraphQL operation names (all sent to `POST /api/graphql`) and REST
endpoints, found statically from the flow's members and the data-access functions they call;
calls made through `useSession()` context methods are declared by hand in the inventory curation.
**E2E specs** are files in `internal/web/app/e2e/` without the `.spec.ts` suffix. **Corvint
status** counts the flow's members that resolved and did not resolve (see
[Coverage limits](#coverage-limits)); a flow with no resolved member is reported, not generated.

| Flow | Title | Route / screen | Entry symbol | Backend calls | E2E specs | Corvint status | Pages |
|---|---|---|---|---|---|---|---|
| `shell.routing` | App routing: /web basename, area code-splitting, initial-route preload | `/web/*` | `main.tsx#renderOnce`, `App.tsx#App` | — | smoke, browser-compat | generated (4 resolved, 17 unresolved) | [page](generated/42aaaef56492e1510b51c8caf3e1b3f0/README.md) |
| `shell.session-spine` | Session spine: compat handshake, session renewal, identity epoch, request guard | `(all routes)` | `session.tsx#SessionProvider` | `CurrentSession` `Profiles` `Relock` `Reveal` `SwitchProfile` `/api/compat/handshake` `/api/events` `/api/graphql` `/api/manifest` `/api/setup` `/api/web/admin-signin` `/api/web/logout` `/api/web/promote` `/api/web/session/renew` | auth, auth-matrix, ui-states | generated (25 resolved, 90 unresolved) | [page](generated/cc107bfc4b52832e04e348aec13c10e4/README.md) |
| `shell.event-stream` | Server event stream (/api/events) with reconnect backoff | `(all routes)` | `eventStream.ts#openEventStream` | `/api/events` | none | generated (5 resolved, 4 unresolved) | [page](generated/6ee7e7f83aac834e8d393dc7cbe30caf/README.md) |
| `shell.pwa` | Service worker registration and reload-on-controller-change | `(all routes)` | `pwa.ts#registerServiceWorker` | — | browser-compat | generated (2 resolved, 1 unresolved) | [page](generated/7d42560a36c7dd785dd9793d6310e253/README.md) |
| `shell.native-host-bridge` | Native app-store host bridge: host messages, open in native player | `(embedded hosts)` | `beamfallHost.ts#getBeamfallHost` | — | none | not generated (10 unresolved) | — |
| `shell.app-shell` | App shell chrome: header, library sidebar, account menu, phone layout | `(all areas)` | `AppShell.tsx#AppShell` | `Notifications` | viewer, browser-compat, ui-states | generated (16 resolved, 5 unresolved) | [page](generated/e6c78d6d240525f99af728fe632d5b32/README.md) |
| `shell.error-boundaries` | Shell and per-area error boundaries with reload panel | `(all areas)` | `ErrorBoundary.tsx#AreaErrorBoundary` | — | none | generated (1 resolved, 6 unresolved) | [page](generated/326dfab3aa8af08b1b6eac5719aceba6/README.md) |
| `shell.server-activity` | Server activity indicator (running jobs and rescans) in the shell | `(all areas)` | `ServerActivity.tsx#ServerActivity` | `Jobs` | none | generated (3 resolved, 4 unresolved) | [page](generated/004c1bfa41da6517c08b5e43232d5584/README.md) |
| `action.library-rescan` | Rescan a library or all libraries and follow scan state | `/manage/libraries, /console/libraries, /console` | `LibraryRescan.tsx#RescanButton` | `Jobs` `ScanLibrary` | manage, console | generated (8 resolved, 21 unresolved) | [page](generated/21d4c394c2e983b00ddb45fdec2594fc/README.md) |
| `shared.i18n` | Localization: locale resolution, ICU formatting and string catalogs | `(all routes)` | `i18n.tsx#I18nProvider` | — | viewer, manage, console, text-expansion, ui-states | generated (7 resolved, 52 unresolved) | [page](generated/8ff34043846006c5450fec0ee31d31d5/README.md) |
| `shared.workbench` | Shared workbench widgets: status chips, tables, forms, server path picker | `(manage, console)` | `index.tsx#ManageSection` | `AddPathPickerRoot` `PathPicker` | console, manage | generated (13 resolved, 27 unresolved) | [page](generated/c2dbd86a641b4d403b46dde9ead7bab6/README.md) |
| `shared.ui-primitives` | UI primitives: button, dialog, alert dialog, select, table, tabs, tree and form controls | `(all areas)` | `button.tsx#Button` | — | browser-compat, a11y-browser | generated (29 resolved, 44 unresolved) | [page](generated/49d9d967e94d5d9e0e31611a9ef4330d/README.md) |
| `shared.graphql-types` | GraphQL client wire types shared by every data call | `(all areas)` | `types.ts#SessionInfo` | — | none | not generated (170 unresolved) | — |
| `shared.display-helpers` | Display helpers: activity, job, provider-key and string formatting | `(manage, console)` | `jobDisplay.ts#jobStatusLabel` | — | none | generated (7 resolved, 40 unresolved) | [page](generated/a814bcc8011e07198fcd174f30acbcdc/README.md) |
| `setup.wizard` | First-run setup wizard shell: token intake, step navigation | `/setup` | `SetupWizard.tsx#SetupWizard` | `BrowseServerFolders` `Jobs` `Plugins` `SetScraperCredential` `/api/setup` | auth, ui-states, a11y-browser | generated (16 resolved, 22 unresolved) | [page](generated/5d042754bf5df5a7cb8957d1623ccc68/README.md) |
| `setup.owner-and-security` | Setup: choose sign-in security mode and create the owner Profile | `/setup` | `orchestrate.ts#bootstrapSetup` | `Libraries` `SetupState` `/api/setup` | ui-states | generated (2 resolved, 9 unresolved) | [page](generated/2f5c3ea4990f350bfe1347a7600ed0cd/README.md) |
| `setup.plugins` | Setup: activate plugins and enter provider keys | `/setup` | `orchestrate.ts#activatePlugins` | `ConsoleConfig` `SetPluginActive` `VerifyAdminSession` | ui-states | generated (1 resolved, 13 unresolved) | [page](generated/6c861cacb507b604f2986e0397a2b389/README.md) |
| `setup.first-library` | Setup: pick a library type and folder, create the first Library and scan | `/setup` | `orchestrate.ts#createFirstLibrary` | `CreateLibrary` `ScanLibrary` `SetLibraryRoots` `/api/setup` | auth, ui-states | generated (3 resolved, 17 unresolved) | [page](generated/0e9508167e84dcc4aa79ce4d933e508f/README.md) |
| `setup.update-check` | Setup: configure the update-check channel | `/setup` | `orchestrate.ts#configureUpdateCheck` | `SetConsoleConfig` `VerifyAdminSession` `/api/setup` | ui-states | generated (1 resolved, 10 unresolved) | [page](generated/5465f282d054251c34d2f39ed7ef9a60/README.md) |
| `enroll.pair-device` | Enroll a browser with a pairing code | `/enroll` | `EnrollPage.tsx#EnrollPage` | `/api/web/enroll` `/api/web/promote` | auth, smoke, ui-states, a11y-browser | generated (3 resolved, 6 unresolved) | [page](generated/5e8a562af7af514eb8f5ec046fdb0d99/README.md) |
| `enroll.admin-sign-in` | Home-mode admin password sign-in on the enroll page | `/enroll` | `EnrollPage.tsx#AdminSignInForm` | `/api/web/admin-signin` `/api/web/signin-security` | ui-states | generated (1 resolved, 4 unresolved) | [page](generated/38a2e26f6c45d30b4c0a342ba6cc236f/README.md) |
| `enroll.admin-session-gate` | Admin Session unlock gate (admin password) reused by Console | `/console/*` | `AdminSessionGate.tsx#AdminSessionGate` | `AdminSession` `EndAdminSession` `VerifyAdminSession` `/api/web/enroll` | auth-matrix, console | generated (4 resolved, 10 unresolved) | [page](generated/ae5c4d0cd7b43bd103cb7fb5eeb41d28/README.md) |
| `enroll.promote` | Promote an enrolled browser to a manager device | `/promote` | `PromotePage.tsx#PromotePage` | `/api/web/promote` `/api/web/session/renew` | ui-states, a11y-browser | generated (2 resolved, 1 unresolved) | [page](generated/090d0d70213cb0dcf8808851431a181c/README.md) |
| `enroll.sign-out` | Sign out of the web session | `(account menu)` | `SignOutButton.tsx#SignOutButton` | `/api/pairing-codes` `/api/web/logout` | none | generated (2 resolved, 1 unresolved) | [page](generated/7086235a1aed16e99089f12c4833bcc2/README.md) |
| `console.shell` | Console area shell: route table, section nav, host firewall, session bar | `/console/*` | `index.tsx#ConsoleArea` | — | console, ui-states, a11y-browser | generated (10 resolved, 14 unresolved) | [page](generated/ccbbc5b36c72164a980ec4ace2721c52/README.md) |
| `console.admin-gate` | Console admin gate: locked reasons and unlock | `/console/*` | `AdminGate.tsx#AdminGate` | `AdminSession` `EndAdminSession` `VerifyAdminSession` `/api/web/enroll` | console, auth-matrix, viewer, manage | generated (8 resolved, 13 unresolved) | [page](generated/acae33ecb9a51ac24e01ea18bef86966/README.md) |
| `console.overview` | Console overview: owner tasks, setup checklist, needs-attention queue | `/console` | `index.tsx#Overview` | `ConsoleConfig` `JobRetry` `Jobs` `Libraries` `SetupState` `SourceItems` `UnmatchedQueue` `WatcherStatus` | console, ui-states, a11y-browser | generated (6 resolved, 25 unresolved) | [page](generated/d5034cba0c0b5e8550e612b623ed95f7/README.md) |
| `console.operations` | Console operations and updates: resource governor, job queue and job log | `/console/operations, /console/updates` | `OperationsControls.tsx#OperationsControls` | `ConsoleConfig` `JobCancel` `JobEvents` `JobRetry` `Jobs` `OperationsGovernor` `RunEventsCompaction` `SetConsoleConfig` `/api/events` | console, ui-states, a11y-browser | generated (1 resolved, 27 unresolved) | [page](generated/885f6abe7bb1ef95a751a5407b6eaa1d/README.md) |
| `console.sessions` | Console live sessions: update awareness, jobs, live sessions and host graphs | `/console/sessions` | `Operations.tsx#Operations` | `ConsoleConfig` `HostMetrics` `Jobs` `LiveSessions` `SetConsoleConfig` `TerminateSession` `UpdateStatus` `/api/events` | console, ui-states, a11y-browser | generated (12 resolved, 46 unresolved) | [page](generated/c561bb20ac01b5f30568584d65ffbaca/README.md) |
| `console.play-history` | Console play history: filters, history table and rankings | `/console/history` | `PlayHistory.tsx#PlayHistory` | `Libraries` `PlayHistory` `PlayStats` `Profiles` | ui-states | generated (4 resolved, 14 unresolved) | [page](generated/349614c235c4bddc845fb9b2075ffd06/README.md) |
| `console.health` | Console library health: dimensions, drilldowns, bulk remediation | `/console/health` | `Health.tsx#Health` | `BulkDrillOp` `BulkOp` `DuplicateGroups` `HealthDrillItems` `LibraryHealth` | console, ui-states, a11y-browser | generated (9 resolved, 13 unresolved) | [page](generated/f3c227244d89f121fb0275b6a0484107/README.md) |
| `console.activity` | Console activity log | `/console/activity` | `ActivityLog.tsx#ActivityLog` | `ActivityRecords` `Devices` `Libraries` `Profiles` | console, ui-states, a11y-browser | generated (4 resolved, 15 unresolved) | [page](generated/c2d11ac6b11f10c01dc526c1909a1195/README.md) |
| `console.libraries` | Console libraries: add a library, roots, preview clips, skip detection | `/console/libraries` | `Libraries.tsx#Libraries` | `CreateLibrary` `FolderTree` `Libraries` `Plugins` `ScanLibrary` `SetLibraryPreviewClips` `SetLibraryRoots` `SetSkipDetectionEnabled` `SkipDetectionEnabled` `WatcherStatus` | console, ui-states, a11y-browser | generated (9 resolved, 25 unresolved) | [page](generated/d19e51d3c9444b908e932b44d855d27d/README.md) |
| `console.plugins` | Console plugins: standing, lifecycle, config editor, provider keys | `/console/plugins` | `Plugins.tsx#Plugins` | `ConsoleConfig` `ConsolePluginStanding` `DisableScraperCredential` `RunPluginAction` `SetPluginActive` `SetPluginConfig` `SetScraperCredential` `UninstallPluginData` `ValidateScraperCredential` | console, ui-states, a11y-browser | generated (9 resolved, 44 unresolved) | [page](generated/c7ebc2bb83d1a8f9f1bf2a9513e8f78e/README.md) |
| `console.capabilities` | Console identification and layout capabilities | `/console/identification, /console/layout` | `Capabilities.tsx#Capabilities` | `CapabilityStates` `SetCapabilityEnabled` | ui-states, a11y-browser | generated (2 resolved, 4 unresolved) | [page](generated/eb1e1e74570853618e9ae7606e648ec7/README.md) |
| `console.access.profiles` | Console access: create and manage Profiles | `/console/access, /console/schedules` | `AccessAdmin.tsx#AccessAdmin` | `CreateProfile` `Profiles` `UpdateProfile` | console, ui-states, a11y-browser | generated (4 resolved, 10 unresolved) | [page](generated/1fb8c6a5ce70995ee6d0c1ffc25b0495/README.md) |
| `console.access.devices` | Console access: devices and pairing codes | `/console/access` | `Devices.tsx#Devices` | `ConsoleConfig` `Devices` `RevokeAllDevices` `RevokeDevice` `SetConsoleConfig` `SetDeviceRole` `/api/pairing-codes` | auth, ui-states, a11y-browser | generated (7 resolved, 16 unresolved) | [page](generated/3b11e3e716888d193bc1263fccd0bd3b/README.md) |
| `console.access.profile-policy` | Console access: library grants, rating ceilings, access schedules, managed policy | `/console/access, /console/schedules` | `ProfilesAccess.tsx#ProfilesAccess` | `AccessGrantsForProfile` `AccessSchedule` `EnforcedFilters` `Libraries` `RevokeAccessGrant` `SetAccessSchedule` `SetCertificationCeiling` `SetExtensionPreauthorization` `SetProfileLibraries` `UpdateProfile` | console, ui-states, a11y-browser | generated (15 resolved, 34 unresolved) | [page](generated/6da48620acc1e31c45eb871859f10ef6/README.md) |
| `console.access.sign-in-security` | Console access: sign-in security mode | `/console/access` | `SignInSecurity.tsx#SignInSecurity` | `/api/web/logout` `/api/web/signin-security` | ui-states, a11y-browser | generated (3 resolved, 2 unresolved) | [page](generated/5071ab9e9b6eb0a48f1c2a791179b71e/README.md) |
| `console.remote-access` | Console remote access: certificate, relay, direct connection, watch-away guide | `/console/remote` | `RemoteAccess.tsx#RemoteAccess` | `RemoteAccess` | console, ui-states, a11y-browser | generated (1 resolved, 16 unresolved) | [page](generated/e40bf913ff2ba00e488661ed0764fddb/README.md) |
| `console.qbittorrent-ingest` | Console qBittorrent ingest connection and outcomes | `/console/remote` | `RemoteAccess.tsx#QBittorrentIngest` | `QBittorrentIngest` | console | not generated (5 unresolved) | — |
| `console.migration-import` | Console migration: importer wizard, credentials, mapping, match-rate report | `/console/migrate, /console/remote` | `RemoteAccess.tsx#MigrationWizard` | `ImportStatus` `Importers` `Libraries` `Profiles` `RunImport` `SetScraperCredential` | console, ui-states, a11y-browser | generated (14 resolved, 23 unresolved) | [page](generated/71c7112c2c0177a0ae446208be7881b9/README.md) |
| `console.api-credentials` | Console API tokens and keys: scopes, create, revoke, one-time secret | `/console/api` | `ApiCredentials.tsx#ApiCredentials` | `APIKeys` `APITokens` `CreateAPIKey` `CreateAPIToken` `RevokeAPIKey` `RevokeAPIToken` | console, ui-states, a11y-browser | generated (3 resolved, 18 unresolved) | [page](generated/79231f795366585d78321cfe8ea9004e/README.md) |
| `console.retention` | Console retention rules | `/console/retention` | `Retention.tsx#Retention` | `CreateRetentionRule` `DeleteRetentionRule` `UpdateRetentionRule` | ui-states, a11y-browser | generated (4 resolved, 10 unresolved) | [page](generated/a27521afd9a1aa9212ea439dbec3ef84/README.md) |
| `console.config` | Console server and transcode configuration | `/console/config` | `Config.tsx#Config` | `ConsoleConfig` `SetConsoleConfig` `SetTranscodeConfig` `TranscodeConfig` | console, ui-states, a11y-browser | generated (1 resolved, 12 unresolved) | [page](generated/91a647a6ca10e8235c4e87bffc10a2a4/README.md) |
| `unrouted.modules` | Unreached code: unmounted modules and GraphQL operations no UI calls | `(none)` | `PlaybackCache.tsx#PlaybackCache` | `EvictPlaybackRenditionCache` `IdentifySearch` `Importers` `PlaybackRenditionCache` `PrecomputePlaybackRenditions` `ReportPlayback` `Search` `SetRetentionCandidateHold` | none | generated (9 resolved, 18 unresolved) | [page](generated/d83ade14d7e4a0a6b9fad9805f698916/README.md) |
| `manage.shell` | Manage area shell: route table, nav groups, admin lock | `/manage/*` | `index.tsx#ManageArea` | — | manage, auth-matrix, viewer, ui-states, a11y-browser | generated (1 resolved, 8 unresolved) | [page](generated/6236381b2381a125e7d325200023a2b4/README.md) |
| `manage.libraries` | Manage libraries: library list and folder tree | `/manage/libraries` | `LibrariesPage.tsx#LibrariesPage` | `CreateLibrary` `CreateManagedFolder` `DeleteManagedPath` `FolderTree` `Libraries` `MoveManagedPath` `Plugins` `RenameManagedPath` `RestoreManagedPath` `ScanLibrary` `SetLibraryPreviewClips` `SetLibraryRoots` | manage, ui-states, a11y-browser | generated (18 resolved, 26 unresolved) | [page](generated/3713ebcf3fbafbc5f290f763e9561913/README.md) |
| `manage.operations` | Manage operations: jobs, schedules, folder watcher | `/manage/operations` | `OperationsPage.tsx#OperationsPage` | `Jobs` `Schedules` `UpdateSchedule` `WatcherStatus` | manage, ui-states, a11y-browser | generated (9 resolved, 18 unresolved) | [page](generated/c0e0b5fbe9dbf1708db4cb766307d1e1/README.md) |
| `manage.convert` | Manage convert: enqueue conversion presets and watch the queue | `/manage/convert` | `ConvertPage.tsx#ConvertPage` | `EnqueueConvertJob` `Jobs` `Libraries` | ui-states, a11y-browser | generated (7 resolved, 10 unresolved) | [page](generated/b1dcc1ff013249dae7b626c1259d1650/README.md) |
| `manage.rename` | Manage rename: pattern preview, batch rename managed paths, undo | `/manage/rename` | `RenamePage.tsx#RenamePage` | `BatchRenameManagedPaths` `FolderTree` `Libraries` `UndoBatch` | ui-states, a11y-browser | generated (9 resolved, 14 unresolved) | [page](generated/22599c7f8807f3dd5b813ad23f4c85e0/README.md) |
| `manage.trash` | Manage trash: restore, purge and reversible activity | `/manage/trash` | `TrashPage.tsx#TrashPage` | `ActivityLog` `PurgeTrash` `RestoreMedia` `TrashList` | manage, ui-states, a11y-browser | generated (6 resolved, 11 unresolved) | [page](generated/b0c5440f654fa21d739d5b2b5c585f3b/README.md) |
| `manage.backups` | Backups: history, verify manifest, restore and export | `/manage/backups, /console/backups` | `BackupExportPage.tsx#BackupExportPage` | `Backup` `BackupHistory` `MetadataExport` `VerifyBackup` | ui-states, a11y-browser | generated (17 resolved, 9 unresolved) | [page](generated/74120a49433d1f637be34057b7f429f5/README.md) |
| `manage.subscriptions` | Manage subscriptions and retention kinds | `/manage/subscriptions` | `SubscriptionsPage.tsx#SubscriptionsPage` | `AddSubscription` `RemoveSubscription` `SetSubscriptionRetention` `Subscriptions` | manage, ui-states, a11y-browser | generated (4 resolved, 10 unresolved) | [page](generated/e07cf6403700ca3de20c5f8ee4e8ef12/README.md) |
| `manage.collections` | Manage manual and smart collections | `/manage/collections` | `CollectionsPage.tsx#CollectionsPage` | `AddCollectionMedia` `Collections` `CreateCollection` `CreateSmartCollection` `DeleteCollection` `DeleteSmartCollection` `RemoveCollectionMedia` `ReorderCollectionMedia` `RestoreCollection` `SmartCollections` `UpdateCollection` `UpdateSmartCollection` | manage, ui-states, a11y-browser | generated (15 resolved, 32 unresolved) | [page](generated/01980267f2da1b8a6a45d6a38088bf67/README.md) |
| `manage.bulk-ops` | Manage bulk edit and organize with preview and confirm | `/manage/bulk` | `BulkOpsPage.tsx#BulkOpsPage` | `BulkEditApply` `BulkEditPreview` `Libraries` `OrganizeApply` `OrganizePreview` `UndoBatch` | manage, ui-states, a11y-browser | generated (8 resolved, 13 unresolved) | [page](generated/6dfceb70ed5574fc0dd74165b30a0088/README.md) |
| `manage.tags` | Manage tags: tag tree, assign, bulk, trashed tags | `/manage/tags` | `TagsPage.tsx#TagsPage` | `AssignTag` `BulkAssignTags` `CreateTag` `DeleteTag` `RestoreTag` `TagAssignments` `Tags` `UnassignTag` `UpdateTag` | ui-states, a11y-browser | generated (21 resolved, 26 unresolved) | [page](generated/7904a72ccfa91625534cebea607e6a03/README.md) |
| `manage.curation` | Manage curation: fields, relations and files of a Media | `/manage/curation` | `CurationPage.tsx#CurationPage` | `Entity` `EntityRelations` `LinkMediaRelation` `MediaFields` `MediaFiles` `MediaRelations` `UpsertEntityRelation` | manage, ui-states, a11y-browser | generated (10 resolved, 17 unresolved) | [page](generated/3d8432e8a48564693b1c5b210720b534/README.md) |
| `manage.entities` | Manage entities | `/manage/entities` | `EntityPage.tsx#EntityPage` | `Entity` | ui-states, a11y-browser | generated (2 resolved, 2 unresolved) | [page](generated/c3cb733e08f4f991741d0b840bb4f42e/README.md) |
| `manage.artwork` | Manage artwork slots and candidates | `/manage/artwork` | `ArtworkPage.tsx#ArtworkPage` | `ArtworkCandidates` `PinArtworkCandidate` `/api/media/artwork-candidate` | manage, ui-states, a11y-browser | generated (6 resolved, 6 unresolved) | [page](generated/2ee85e517ea59a2a44c6f1b91291d7fd/README.md) |
| `manage.markers` | Manage markers: segment edit, bulk delete, trashed markers | `/manage/markers` | `MarkersPage.tsx#MarkersPage` | `CreateMarker` `DeleteMarker` `Markers` `RestoreMarker` `TrashedMarkers` `UpdateMarker` | ui-states, a11y-browser | generated (17 resolved, 19 unresolved) | [page](generated/b7d7e23b5dca20ad45fe1f0fbfd7069b/README.md) |
| `manage.field-locks` | Manage field locks | `/manage/field-locks` | `FieldLocksPage.tsx#FieldLocksPage` | `MediaFieldLocks` `SetMediaFieldLock` | manage, ui-states, a11y-browser | generated (4 resolved, 4 unresolved) | [page](generated/0d4e8f99d8c0f2f17e34c9a664bcb1cf/README.md) |
| `manage.navigation` | Manage navigation overrides: nodes, theme, sections, history | `/manage/navigation` | `NavOverridePage.tsx#NavOverridePage` | `NavOverride` `NavOverrideHistory` `ResetNavOverride` `RestoreNavOverride` `SetNavOverride` | ui-states, a11y-browser | generated (10 resolved, 19 unresolved) | [page](generated/48df4df13e249971d7603fc21fe8ed4b/README.md) |
| `manage.unmatched` | Manage unmatched records: inbox, fix match, suppression | `/manage/unmatched` | `UnmatchedPage.tsx#UnmatchedPage` | `BulkOp` `DismissUnmatched` `FixMatch` `FixMatchCandidates` `FixMatchPreview` `IdentifyLookup` `Libraries` `ResetUnmatchedSuppression` `SuppressUnmatched` `UnmatchedQueue` `UnmatchedRecords` `UnmatchedSuppressions` | manage, ui-states, a11y-browser | generated (18 resolved, 28 unresolved) | [page](generated/9ea9caf14ba4eee1f30846a1994a3132/README.md) |
| `manage.unmatched.create-media` | Create a Media from an unmatched record and upload artwork | `/manage/unmatched` | `CreateMediaForm.tsx#CreateMediaForm` | `CreateMediaFromUnmatched` `/api/media/artwork/upload` | create-media-artwork.console | generated (4 resolved, 6 unresolved) | [page](generated/6c5316a98e80fcee0fad200be1034235/README.md) |
| `manage.restrictions` | Manage restrictions | `/manage/restrictions` | `RestrictionsPage.tsx#RestrictionsPage` | `Relock` `Reveal` | manage, ui-states, a11y-browser | generated (3 resolved, 4 unresolved) | [page](generated/06804cd2e2106358c566b86268baca0f/README.md) |
| `manage.profile` | Manage profile page | `/manage/profile` | `ProfilePage.tsx#ProfilePage` | `Profiles` `SwitchProfile` | manage, ui-states, a11y-browser | generated (2 resolved, 4 unresolved) | [page](generated/57894dc67c94738140390052cc0a3567/README.md) |
| `viewer.session-routing` | Viewer area: session pending/error states, setup hand-off, viewer routes | `/*` | `index.tsx#ViewerArea` | `/api/setup` | viewer, ui-states | generated (1 resolved, 3 unresolved) | [page](generated/95fc64bc56350be5ec20a9f5f9e1a850/README.md) |
| `viewer.home-browse` | Viewer home and node browse: manifest layout, sections, hero | `/, /n/:nodeId` | `BrowsePage.tsx#BrowsePage` | `SourceItems` | viewer, ui-states, a11y-browser | generated (20 resolved, 14 unresolved) | [page](generated/e9b164fba48705f4d491078997ee3846/README.md) |
| `viewer.library-browse` | Viewer full library browse: sort, filters, grouping, virtual grid | `/n/:nodeId/all` | `LibraryBrowsePage.tsx#LibraryBrowsePage` | `LibraryItems` | ui-states, a11y-browser | generated (15 resolved, 46 unresolved) | [page](generated/89e5f11cc94f6f133a5558cd0f4edf61/README.md) |
| `viewer.entity` | Viewer entity detail page and not-available state | `/entity/:entityId` | `EntityPage.tsx#EntityPage` | `Entity` `MediaForEntity` | viewer, ui-states, a11y-browser | generated (8 resolved, 6 unresolved) | [page](generated/2dde8c97f28a6263f2ca97e2f2a0477e/README.md) |
| `viewer.see-all` | Viewer See-All: paginate a Source from a shelf | `/seeall/:sourceId` | `SeeAllPage.tsx#SeeAllPage` | `SourceItems` | viewer, ui-states, a11y-browser | generated (6 resolved, 1 unresolved) | [page](generated/ad02b91216ffb4c17a884821718afc6e/README.md) |
| `viewer.search` | Viewer search page: modes (text, semantic, recommend) and grouped results | `/search` | `SearchPage.tsx#SearchPage` | `ClientPrefs` `SearchResult` `SemanticCapability` | viewer, ui-states, a11y-browser | generated (6 resolved, 20 unresolved) | [page](generated/e8ea9e2a52c2324d9d8afd8cdf8b1941/README.md) |
| `viewer.header-search` | Viewer header live search with keyboard shortcut | `(viewer header)` | `HeaderSearch.tsx#HeaderSearch` | `SearchResult` | viewer | generated (10 resolved, 9 unresolved) | [page](generated/219ee2ba822318eb61cc59b0c0f9ce8f/README.md) |
| `viewer.my-data` | Viewer My Data / privacy: summary, event log, sessions, stream tokens, export | `/mydata` | `MyDataPage.tsx#MyDataPage` | `DeleteMyEngagement` `MyDataSummary` `MyEngagement` `MyEngagementLog` `MySessions` | viewer, ui-states, a11y-browser | generated (9 resolved, 21 unresolved) | [page](generated/f8d45946aa49284f1b86ebef0c980b98/README.md) |
| `viewer.notifications` | Viewer notifications inbox | `/notifications` | `NotificationsPage.tsx#NotificationsPage` | `MarkNotificationRead` `Notifications` | viewer, ui-states, a11y-browser | generated (5 resolved, 8 unresolved) | [page](generated/72039e57e11af1f261be32e0dacda164/README.md) |
| `viewer.profile-picker` | Viewer who's-watching Profile picker with PIN prompt | `/profiles` | `ProfilePickerPage.tsx#ProfilePickerPage` | `Profiles` `SwitchProfile` | viewer, ui-states, a11y-browser | generated (11 resolved, 19 unresolved) | [page](generated/83743f6282cf018923668839ed38a17a/README.md) |
| `viewer.profile-settings` | Viewer Profile settings: preferences toggles and avatar | `/settings` | `ProfileSettingsPage.tsx#ProfileSettingsPage` | `ClientPrefs` `SetClientPref` | ui-states, a11y-browser | generated (7 resolved, 25 unresolved) | [page](generated/6d03d343cff1d14f26774f27046a9347/README.md) |
| `viewer.media-card` | Viewer media card: preview dwell, state overlay, card menu, artwork | `(viewer grids)` | `MediaItemCard.tsx#MediaItemCard` | `/api/preview-info` | viewer, a11y-browser | generated (11 resolved, 11 unresolved) | [page](generated/17717802bf0fe3c8d0f8324328ac9a31/README.md) |
| `viewer.item-actions` | Viewer item actions: mark watched, watchlist, rating, engagement sync | `(cards, entity, player)` | `ItemActions.tsx#ItemActions` | `ItemState` `SetRating` `SetWatched` `SetWatchlisted` `/api/events` | none | generated (9 resolved, 17 unresolved) | [page](generated/f94cdbb556a0c2d845053c71bd808b53/README.md) |
| `viewer.reveal` | Viewer reveal and relock of restricted content | `/, /n/:nodeId/all` | `RevealControls.tsx#RevealControls` | `Relock` `Reveal` | none | generated (3 resolved, 4 unresolved) | [page](generated/4c275a3142cb19d221f107631a2a5676/README.md) |
| `player.page` | Player route: lazy load, timestamp links, info overlay, audio stage | `/watch/:mediaId` | `PlayerPage.tsx#PlayerPage` | `ClientPrefs` `Media` `SetClientPref` | player, viewer, ui-states, a11y-browser | generated (9 resolved, 9 unresolved) | [page](generated/d950458d8d21d8cd6527c9e9982e3442/README.md) |
| `player.playback-engine` | Playback engine: playback-info, capability probe, HLS selection and recovery, progress reports | `/watch/:mediaId` | `usePlayer.ts#usePlayer` | `ClientPrefs` `PlaybackMarkers` `ReportPlayback` `/api/media` `/api/playback-info` `/api/preview-info` | player, viewer, browser-compat | generated (53 resolved, 78 unresolved) | [page](generated/46257443cc4779e98ed53945e66575df/README.md) |
| `player.transport` | Player transport: seek bar, trickplay, markers, chapters, renditions, touch gestures | `/watch/:mediaId` | `PlayerTransport.tsx#PlayerTransport` | — | player | generated (49 resolved, 29 unresolved) | [page](generated/a70344430ef38f9301f230a048332cc9/README.md) |
| `player.tracks-subtitles` | Player audio and subtitle tracks, subtitle styling | `/watch/:mediaId` | `useLiveTracks.ts#useLiveTracks` | — | player | generated (9 resolved, 16 unresolved) | [page](generated/76fc5c2449ad0d7ef758f4e866e02adc/README.md) |
| `player.auto-skip` | Player auto-skip of intro/credits segments with skipped toast | `/watch/:mediaId` | `useAutoSkip.ts#useAutoSkip` | — | player | generated (9 resolved, 5 unresolved) | [page](generated/8b6155e6a01c76a0e008a4cabdb52b2d/README.md) |
| `player.up-next-queue` | Player up-next countdown and play queue navigation | `/watch/:mediaId` | `useUpNext.ts#useUpNext` | — | player | generated (16 resolved, 19 unresolved) | [page](generated/fa22fc31bb8d11236cda2f671f818c4a/README.md) |
| `player.media-session` | Player OS media-session integration (lock-screen controls) | `/watch/:mediaId` | `useMediaSession.ts#useMediaSession` | — | none | generated (3 resolved, 3 unresolved) | [page](generated/ab85b00b0ef1be60ae43722b7edab162/README.md) |

## Route tree

React Router routes from `src/App.tsx` and the three area routers. Every path is under the
`/web` basename (`src/main.tsx`).

```text
/web
├── /setup                       setup.wizard (+ setup.owner-and-security, .plugins, .first-library, .update-check)
├── /enroll                      enroll.pair-device, enroll.admin-sign-in
├── /promote                     enroll.promote
├── /manage/*                    manage.shell (admin-gated)
│   ├── (index)                  → /manage/libraries
│   ├── /libraries               manage.libraries
│   ├── /operations              manage.operations
│   ├── /convert                 manage.convert
│   ├── /rename                  manage.rename
│   ├── /trash                   manage.trash
│   ├── /backups                 manage.backups
│   ├── /subscriptions           manage.subscriptions
│   ├── /collections             manage.collections
│   ├── /bulk                    manage.bulk-ops
│   ├── /tags                    manage.tags
│   ├── /curation                manage.curation
│   ├── /entities                manage.entities
│   ├── /artwork                 manage.artwork
│   ├── /markers                 manage.markers
│   ├── /field-locks             manage.field-locks
│   ├── /navigation              manage.navigation
│   ├── /unmatched               manage.unmatched, manage.unmatched.create-media
│   ├── /restrictions            manage.restrictions
│   └── /profile                 manage.profile
├── /console/*                   console.shell, console.admin-gate (routes built from GROUPS)
│   ├── (index)                  console.overview
│   ├── /operations              console.operations
│   ├── /sessions                console.sessions
│   ├── /history                 console.play-history
│   ├── /health                  console.health
│   ├── /activity                console.activity
│   ├── /libraries               console.libraries
│   ├── /plugins                 console.plugins
│   ├── /identification          console.capabilities                 (hidden in the app-store host)
│   ├── /layout                  console.capabilities                 (hidden in the app-store host)
│   ├── /access                  console.access.profiles, .devices, .profile-policy, .sign-in-security
│   ├── /schedules               console.access.profile-policy
│   ├── /remote                  console.remote-access, console.qbittorrent-ingest
│   ├── /api                     console.api-credentials
│   ├── /backups                 manage.backups
│   ├── /retention               console.retention
│   ├── /migrate                 console.migration-import             (hidden in the app-store host)
│   ├── /updates                 console.operations
│   ├── /config                  console.config
│   └── aliases                  /capabilities → /identification; /operations-controls, /playback-cache → /operations
├── /*                           viewer.session-routing (viewer area)
│   ├── /                        viewer.home-browse
│   ├── /n/:nodeId               viewer.home-browse
│   ├── /n/:nodeId/all           viewer.library-browse
│   ├── /entity/:entityId        viewer.entity
│   ├── /seeall/:sourceId        viewer.see-all
│   ├── /search                  viewer.search (header: viewer.header-search)
│   ├── /mydata                  viewer.my-data
│   ├── /notifications           viewer.notifications
│   ├── /profiles                viewer.profile-picker
│   ├── /settings                viewer.profile-settings
│   └── /watch/:mediaId          player.page (+ player.* engine, transport, tracks, auto-skip, up-next, media-session)
└── *                            → /
```

Cross-cutting flows with no route of their own: `shell.*` (routing, session spine, event stream,
service worker, native host bridge, app shell, error boundaries, server activity),
`action.library-rescan`, `shared.*`, `viewer.media-card`, `viewer.item-actions`, `viewer.reveal`.
`unrouted.modules` holds code nothing reaches: `PlaybackCache.tsx` (the `/playback-cache` alias
redirects to Operations), `console/lib/previewMedia.ts`, `setup/MigrationOffer.tsx`, and the
GraphQL functions `setRetentionCandidateHold`, `identifySearch`, `reportPlayback` and `search`,
which no UI calls.

## Coverage limits

- **Go is not supported.** Corvint's flow documentation reads Ruby, JS/TS, React JSX and HTML
  only, so the Go server is outside this record: the GraphQL resolvers and REST handlers behind
  the backend-call column, `internal/web` serving, and every non-web client. The scope is
  `internal/web/app` alone.
- **Only `function` declarations resolve.** Corvint resolves function declarations and
  arrow-function constants. The inventory still declares the other members so that membership is
  complete, and Corvint reports 985 of them as "not a supported declaration": constant values,
  interfaces, type aliases, classes and `let` bindings. `shared.graphql-types` (170 types) is not
  generated for this reason.
- **TypeScript lexer bug: generic type arguments.** In a `.ts`/`.tsx` file, the first generic
  type argument (`Promise<string>`, `useState<string>(`, `Record<K, V>`) makes the lexer drop every
  later declaration in that file. This accounts for 637 unresolved function declarations,
  including most React components and hooks declared below their imports. The source was not
  changed to work around it.
- **TypeScript lexer bug: quotes in regex character classes.** A regular-expression literal whose
  character class contains a quote (`/[?&]token=[^&\s"']+/`) drops the declarations after it
  (7 functions in `console/sections/OperationsControls.tsx`). Another 8 function declarations in
  `PlayHistory.tsx`, `Profiles.tsx`, `ProfilesAccess.tsx`, `Config.tsx` and `AudioStage.tsx` fail
  without either trigger; the cause is not isolated.
- **Not generated:** `shell.native-host-bridge`, `shared.graphql-types` and
  `console.qbittorrent-ingest` have no resolved member, so they have no pages; their rows above
  still list members' backend calls and specs.
- **Application map (experimental AMAP-V0).** Of the 44 screens, 8 are abstract layout or splat
  routes. These limits apply:
  - The console section routes are built by mapping the `GROUPS` array into `<Route>` elements.
    The appmap reports this as `non-literal-react-children` and sees only the three
    `ROUTE_ALIASES` redirects, so no `/console/<section>` screen exists in `map.json`. The e2e
    helper `e2e/lib/ui-states.mjs` keeps its console route list as a literal for the same reason
    (CORE-0932).
  - The area routers are not composed under their parent route: the manage screens appear as
    `/bulk` rather than `/manage/bulk`, and the viewer screens are not tied to the top-level
    `/{*splat}` route that mounts them.
  - Spec joins: 24 of 31 spec files resolve. Every `page.goto` lands on the top-level `/{*splat}`
    screen, so no spec joins to a specific page, and the map has 0 edges and 0 flows. The
    remaining 7 are unresolved imports of `playwright-core` internals and the specs that import
    them.
  - Navigation driven by `navigate()` calls, links built at runtime and the app-store host's
    hidden console routes are not modelled.
- **Backend calls are curated, not Corvint output.** The column comes from a static scan of
  GraphQL operation names and `/api/...` literals in each flow's members and their data-access
  functions. Requests whose path is built at runtime, and calls routed through `useSession()`,
  are listed only where the curation declared them.
- **E2E coverage is by spec file**, not by test or assertion. `ui-states` and `a11y-browser`
  sweep every screen, so they appear on almost every routed flow; a listed spec is not proof the
  flow's behaviour is asserted.
