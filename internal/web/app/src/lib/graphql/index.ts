// The typed GraphQL / data layer for the Beamfall web app. Every operation the
// vanilla app performs (internal/web/static/app.js) is re-exported here as a
// typed async function, grouped by area:
//   - viewer.ts   — manifest, session/profile/reveal, catalog, entity, search,
//                   playback, My Data, client prefs
//   - manage.ts   — libraries, plugins, jobs, schedules, trash, markers,
//                   artwork, subscriptions, bulk edit / organize / undo,
//                   health, field locks, importers, watcher, setup state
//   - console.ts  — remote access, server/transcode config, live sessions,
//                   access schedules, enforced filters, qBittorrent, API
//                   tokens, API keys, activity log
//   - auth.ts     — admin session + REST enroll/promote/logout/pairing codes
//
// Query strings either match app.js verbatim or are substrate-only typed
// helpers for upcoming Manage surfaces, with server-side contract coverage.
// Types are derived from the Go source of truth (internal/manifest/manifest.go
// + internal/graph/*.go). Platform-generic by HARD invariant #1 (the wire
// entity is Media); no entitlement/account types (HARD invariant #7).

export * from "./auth";
export * from "./console";
export * from "./manage";
export type * from "./types";
export * from "./viewer";
