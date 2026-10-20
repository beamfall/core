# Rendering-contract conformance checklist (ADR-0014)

Per-renderer conformance status for the client rendering contract
(ADR-0014). This is the checklist ADR-0014's
consequences call for: "every renderer ships a conformance checklist; rendering-semantics
drift joins contract drift as a tested surface."

This document covers rendering behavior. Visual quality, screenshot gates, asset provenance,
and the rule that generated graphics are not real product evidence are governed by
[Beamfall visual contracts](../design/visual-contracts.md).

## Why this exists

**Rendering-semantics drift** is divergence in *behavior* between Section-renderer
implementations while the wire schema still validates: one renderer caps a shelf at 25
items, another materializes 4,000; one collapses empty Sections, another leaves placeholder
holes; one gives skeletons focus, trapping the tvOS remote. Schema/contract tests (golden
manifests, decoding fixtures) cannot catch any of this — the bytes are identical, the
screens are not.

**How to use this document:** every new Section renderer gets a row-by-row pass against the
rule list below *before shipping*, and an honest column in the status table (pass / partial
/ missing / n-a, each with a one-line note). When a renderer's behavior changes, its column
is re-assessed. A `missing` or `partial` cell is allowed to ship only if it's recorded here
— the table is the drift ledger, not a gate that lies.

The shared planning layer
(`beamfall-apple: Sources/BeamfallKit/LayoutRenderPlanner.swift`) implements the
layout-math half of the rules (caps, collapse, bounds, unknown-kind skip, focus target,
skeleton non-focusability) once, with tests in
`Tests/BeamfallKitTests/LayoutRenderPlannerTests.swift`. A renderer earns a `pass` only if
it both consumes the planner's output *and* honors it in the view layer — computing
`initialFocus` and ignoring it is `partial` at best.

## The contract rules

Extracted from ADR-0014 (Decisions 1–4). Rule IDs are stable; reference them in commit
messages and test names.

- **R1 — Item caps per Section.** A shelf renders at most ~25 items with "See All" pushing
  to a paginated grid; grids virtualize and paginate; no Section ever materializes an
  unbounded Source result.
- **R2 — Empty Sections collapse silently.** No placeholder holes; a Layout whose sections
  are all empty renders a defined library-empty state.
- **R3 — Guaranteed initial focus.** First focusable item of the first non-empty Section; a
  Layout can never produce a focus trap or a focus-less screen.
- **R4 — Bounded Layouts.** A maximum Section count per Layout; beyond it, excess sections
  are dropped with a client log.
- **R5 — Unknown vocabulary is skipped silently.** Unknown Section kinds, Sources, artwork
  slots, theme tokens render as if absent (restating ADR-0003), with the Console validating
  symmetrically at save (server-side; tracked outside this table).
- **R6 — Artwork derivative buckets.** Clients request fixed derivative buckets per slot
  (w320/w640/w1280/original), never per-request resizes; URLs are immutable/
  content-addressed so caches hold hard; slot fallback (poster → thumb → frame grab)
  resolves at the endpoint — clients never re-implement fallback.
- **R7 — Structure-first hydration; skeletons never focusable.** The Layout renders
  immediately (from Snapshot or manifest fetch); Sections hydrate independently with
  skeleton placeholders that can never take focus.
- **R8 — Reveal/relock transitions preserve focus.** Restricted Sections/Libraries
  materialize and dematerialize with defined animation; relock never yanks focus from under
  the user — focus moves to a stable anchor if its item vanished.
- **R9 — Offline honesty.** A Snapshot older than a threshold renders with a staleness
  indicator; N consecutive failed syncs surface an offline banner rather than silently
  stale shelves.
- **R10 — Accessibility is specced, not audited after.** Sections carry list/header
  semantics for VoiceOver; players implement the transport VO spec (adjustable scrubber
  with spoken positions, defined focus order, announcing skip actions, overlays never trap
  VO focus). The broader cross-surface a11y floor (WCAG 2.2 AA + the six required behaviors +
  the per-renderer parity ledger) is now governed by ADR-0100 and
  [`docs/design/accessibility-contract.md`](../design/accessibility-contract.md); R10 is the
  rendering-contract slice of it. The Web column's R10 is now automatically asserted by the
  axe-core conformance suite (`internal/web/app/src/areas/a11y/`).

## Per-renderer status

Existing renderers assessed against `beamfall-apple` as of 2026-06-12:
the **tvOS list renderer** (`Sources/BeamfallTV/TVHomeModel.swift`,
`BeamfallTVRootView`) and the **macOS Manager detail renderer**
(`Sources/BeamfallManager/ManagerApp.swift`, `ManagerRootView` detail pane). Both consume
`LayoutRenderPlanner`.

The **Web UI** implementation is now the React app under `internal/web/app`, embedded at
`/web/`, with legacy static files parked under `/web-legacy/` for rollback. Historical Web
cells below still cite the 2026-06-12 static-app audit where that is the evidence source;
new Web renderer evidence should cite React app files and tests.

The **iOS Viewer** column was added in the 2026-06-12 renderer audit against
`beamfall-apple: Sources/BeamfallMobile/` (`MobileRootView.swift`,
`MobileConnectionModel.swift`, `MobilePlayerView.swift`, `MobileComponents.swift`). It is a
real, shipping renderer that **consumes `LayoutRenderPlanner`**
(`MobileConnectionModel.swift:253` for the cached path, `:603-604` for live hydration), so
the planner rules (R1–R5/R7) come for free; its open cells are view-layer artwork (R6) and
accessibility (R10) work.

The **Android Compose** column (covering both `beamfall-android: app-tv/` and
`app-mobile/`, sharing the `kit/` module) was added in the same 2026-06-12 audit. Unlike
every other built renderer, **Android does *not* consume the shared planner.** A faithful
`LayoutRenderPlanner.kt` exists in `kit/` with passing `LayoutRenderPlannerTest.kt`, but no
app code calls it — both `MainActivity.kt` files browse through
`kit/.../BrowseRepository.kt` (app-mobile `MainActivity.kt:145`, app-tv
`MainActivity.kt:158`), which re-derives caps/collapse/kind-filtering by hand and omits
several planner guarantees. Its `partial`/`missing` cells are the direct consequence of that
divergence; closing them means either routing browse through the planner or porting its
guarantees into `BrowseRepository`.

| Rule | tvOS list renderer | macOS Manager detail renderer | iOS Viewer | Web UI | Android Compose |
|---|---|---|---|---|---|
| R1 item caps | **pass** — `prefix(visibleItemCount)` clamp + "See All" remain planner-driven; shelves render with `LazyHStack`, inline grid Sections and See All grids render with `LazyVGrid` (`beamfall-apple: Sources/BeamfallTV/TVHomeModel.swift`, audited 2026-06-12) | **pass** — same clamp + See All planner path; the browse column uses `LazyVStack` and grid Sections/See All use `LazyVGrid` (`beamfall-apple: Sources/BeamfallManager/ManagerApp.swift`, audited 2026-06-12) | **pass** — planner-driven caps + See All (`MobileConnectionModel.swift` consumes `LayoutRenderPlanner` at `:253`/`:603-604`); shelves/grids render in the touch UI bounded to the planner's `visibleItemCount` (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-12) | **pass** — `fetchSectionItems` caps shelves at 20 and grids/lists at 100; inline grids and accumulated See All pages use `useVirtualGrid` spacer-row windowing with a 72-card mount ceiling (`internal/web/app/src/areas/viewer/components/SectionView.tsx`, `pages/SeeAllPage.tsx`, `pages/SeeAllPage.test.tsx`, 2026-07-11) | **pass** — caps are planner-driven and a "See All" trailing card now escalates a capped Section into a virtualized `LazyVerticalGrid` of the full result on both TV and mobile (`beamfall-android` `af19638`, 2026-06-12) |
| R2 empty-collapse | **pass** — planner collapses `loaded(0)` sections (tested); `allSectionsHydratedEmpty` drives a defined library-empty message (2026-06-12) | **pass** — same helper + empty-library message in the browse column; empty navigation shells are suppressed (2026-06-12) | **pass** — planner collapse semantics flow through to the touch renderer; empty Sections do not render placeholder rails and the all-empty Layout falls to the defined empty state (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-12) | **pass** — `HydratedSection` collapses empty slots and reports per-Section state; `LayoutView` waits for all hydratable Sections before rendering the same reveal-safe library-empty state (`internal/web/app/src/areas/viewer/components/{HydratedSection,LayoutView,RenderingContract.test}.tsx`, 2026-07-11) | **pass** — `BrowseRepository.hydrate` now runs `LayoutRenderPlanner` (two-pass: skip+cap before fetch, empty-collapse after real counts), so empty Sections no longer materialize as header-over-empty-rail, and an all-empty Layout falls to `LibraryEmptyState`/`MobileLibraryEmptyState` (`beamfall-android` `af19638`, 2026-06-12) |
| R3 initial focus | **pass** — the planned target is applied via `defaultFocus` with a (navigation, section, index) focus identity (2026-06-11) | **pass** — `ManagerInitialFocusModifier` consumes the planned target and actively sets keyboard focus to the resolved (navigation, section, index) anchor (2026-06-12) | **pass** — the planner's initial-focus target is honored by the touch renderer; touch has no roaming-focus trap risk but the scroll lands on the first non-empty Section per the planned anchor (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-12) | **pass** — after hydration, `LayoutView` applies focus to the first `[data-focus-item]`, including hero actions, with stable navigation/Section/item identity (`internal/web/app/src/areas/viewer/lib/renderFocus.ts`, `components/{LayoutView,RenderingContract.test}.tsx`, 2026-07-11) | **partial** — Android TV relies on Compose's default D-pad focus engine; there is no explicit planner-driven initial-focus anchor on the browse list (no `focusRequester`/`defaultFocus` outside enroll + player, `beamfall-android: app-tv/.../MainActivity.kt`), so focus lands by platform default rather than the contract's guaranteed first-focusable-of-first-non-empty-Section (2026-06-12) |
| R4 bounded layouts | **pass** — planner drops sections beyond `maxSections` (24) with a `sectionLimitExceeded` diagnostic (tested, incl. skipped-sections-don't-consume-budget); the drop now logs via `logRenderDiagnostics` (OSLog `.notice`, reveal-neutral `.public`; `beamfall-apple: Sources/BeamfallTV/TVHomeModel.swift` + `RenderDiagnosticsLog.swift`, 2026-06-13) | **pass** — same shared planner path; same log sink via `logRenderDiagnostics` (`Sources/BeamfallManager/ManagerApp.swift`, 2026-06-13) | **pass** — inherits the planner's `maxSections` (24) bound + `sectionLimitExceeded` diagnostic by consuming `LayoutRenderPlanner` (`MobileConnectionModel.swift:253`/`:603-604`); the drop logs via `logRenderDiagnostics` like the other Apple renderers (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-13) | **pass** — `LayoutView` filters unknown kinds before the 24-Section budget and emits a fixed `SectionLimitExceeded` warning with no ids, titles, counts, or Reveal state (`internal/web/app/src/areas/viewer/components/{LayoutView,RenderingContract.test}.tsx`, 2026-07-11) | **pass** — `BrowseRepository.hydrate` now consumes `LayoutRenderPlanner` (unknown-kind skip + 24-section cap before any fetch); `BrowseScreen` carries the diagnostics and both apps log `SectionLimitExceeded` via `logRenderDiagnostics` (`beamfall-android` `af19638`, 2026-06-12) |
| R5 unknown vocabulary | **pass** — unknown `Section.kind` skipped with diagnostic, no schema failure (tested) | **pass** — same shared planner path | **pass** — inherits the planner's known-kind set `{hero, shelf, grid, list, info, filter-bar}` (`LayoutRenderPlanner.swift:4-9`); unknown kinds/Sources/slots/tokens render as absent via the planner + view fallbacks (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-12) | **pass** — unknown Section kinds are filtered out, unknown Sources collapse through failed/empty hydration, undeclared artwork slots are ignored, and unknown theme tokens have no renderer hook (`internal/web/app/src/areas/viewer/`, 2026-06-12) | **pass** — the divergent `supportedSectionKinds` set is gone; hydration runs the planner's full known-kind set `{hero, shelf, grid, list, info, filter-bar}` and skips only genuinely-unknown kinds with a diagnostic (`beamfall-android` `af19638`, 2026-06-12) |
| R6 artwork derivatives | **partial** — cards request the `w640` bucket by name (`posterArtwork(mediaID:bucket:)`, 2026-06-11); no content-addressed URLs, no endpoint slot fallback; in-memory session cache only; **also ignores the per-slot `artworkSlots` map** — `BeamfallClient.swift` selects only `artworkSlot`/`poster` (`:139`), so per-slot art the server now emits is never requested | **partial** — same path at `w320` for list thumbnails; **same per-slot `artworkSlots` gap** | **partial** — cards request `?bucket=w320` and heroes request `?bucket=w1280` (`MobileConnectionModel.swift`); no content-addressed URLs, no endpoint slot fallback, **poster-only — ignores the per-slot `artworkSlots` map** (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-12) | **partial** — cards request `?bucket=w320` and heroes request `?bucket=w1280`; core serves the fixed bucket vocabulary (`internal/mediahttp/mediahttp.go`), but URLs are not content-addressed, endpoint slot fallback is still poster-only, and the renderer **ignores the per-slot `artworkSlots` map** the server merged (`internal/web/app/src/areas/viewer/` fetches the `/poster` path only) | **partial** — the kit now selects `artworkSlots` and the TV `HeroCard` renders the `backdrop` slot (poster fallback; cards stay poster) (`beamfall-android` `af19638`); still no content-addressed URLs and no endpoint slot fallback — the remaining R6 gap shared by every client (2026-06-12) |
| R7 structure-first + non-focusable skeletons | **partial** — non-focusability holds (planner marks loading sections `isFocusable=false`, tested; "Loading" rows are plain `Text`, not buttons); cached-Snapshot path renders structure before items; but live hydration is one monolithic round-trip, not per-Section | **partial** — same planner guarantees; hydration is a single `sectionHydration` call after connect, no independent per-Section hydration | **partial** — planner non-focusability guarantees hold; structure renders before items, but live hydration is a single `sectionHydrationByNavigationID` round-trip (`MobileConnectionModel.swift:603`), not independent per-Section hydration — same shape as the tvOS/macOS partials (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-12) | **pass** — `renderLayout` creates all Section slots synchronously, each `hydrateSection` fetches independently, and `buildSkeleton` emits `aria-hidden` non-control placeholders (`internal/web/app/src/areas/viewer/`, 2026-06-12) | **partial** — `SkeletonRow` is plain non-focusable `Box`es so skeletons never take focus (`beamfall-android: app-tv/.../MainActivity.kt:396-406`), and `BrowseRepository.skeleton` renders structure first; but `loadHome` fetches the manifest *and* all Section items in one synchronous pass — there is no independent per-Section hydration (2026-06-12) |
| R8 reveal/relock focus preservation | **pass** — `TVConnectionModel` records the current render focus anchor and resolves it after reveal/relock reloads, falling back to the first visible planned target when the anchor vanished (tested, 2026-06-12) | **pass** — `ManagerModel` uses the same render focus anchor resolver across reveal/reload and relock/reload, with keyboard focus re-applied by the view layer (tested, 2026-06-12) | **n-a** — the iOS Viewer is a touch surface with a live connection and no Snapshot at rest; there is no roaming-focus cursor for reveal/relock to yank, so the focus-preservation contract has no touch analogue (this `n-a` is the touch-surface rationale, not a shrug) (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-12) | **pass** — Reveal and relock capture the remembered `(navigation, section, item)` anchor before reload; hydration restores it or falls back to the first visible target when it disappeared (`internal/web/app/src/areas/viewer/lib/renderFocus.ts`, `components/{RevealControls,RevealControls.rendering.test,RenderingContract.test}.tsx`, 2026-07-11) | **pass** (TV) / **n-a** (mobile) — TV captures the focused `(sectionID, itemIndex)` via `BrowseFocusAnchor`; reveal/relock snapshot it and feed `restoreFocus` into the reload (falls back to default focus if the item vanished). Mobile is a touch surface with no roaming cursor (`beamfall-android` `af19638`, 2026-06-12) |
| R9 offline honesty | **pass** — cached hidden Snapshots render with a visible saved-view staleness banner; a refresh failure upgrades the banner to connection-lost/offline without naming or hinting at restricted content (`beamfall-apple: Sources/BeamfallTV/TVHomeModel.swift`, `TVConnectionModel` + `BeamfallTVRootView.snapshotStatusBanner`, 2026-06-12) | **n-a** — the Manager has no Snapshot store or offline mode; it is a live-connection tool (Snapshots are a viewer concern, ADR-0008) | **n-a** — the iOS Viewer at this stage is live-connection only with no Snapshot store at rest, so there is no stale-Snapshot or N-failed-sync state to surface honestly; revisit if/when iOS gains a Snapshot (ADR-0008) (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-12) | **pass** — the Web UI intentionally has no Snapshot cache; failed manifest or Section hydration sync now shows the `connection-banner` stale/connection-lost state instead of falling through to enrollment while a session view exists (`internal/web/app/src/areas/viewer/`, 2026-06-12) | **partial** — `AndroidSnapshotStore` now persists a hidden-state-only Snapshot (libraries/navigation/layouts, no SourceItems — ADR-0008 at-rest rule), and a failed `loadHome` surfaces an honest `Error` / "Connection issue" screen rather than silently failing or falling through to enrollment (`app-tv/.../MainActivity.kt:175`/`:1320`, `app-mobile/.../MainActivity.kt:169`/`:991`); the remaining gap is the tvOS/web shape — the browse path does not yet *render the cached Snapshot while offline with a staleness/connection-lost banner* (`kit/.../AndroidSnapshotStore.kt`, 2026-06-13) |
| R10 accessibility semantics | **pass** — `TVPlayerView` implements the transport VO spec; browse was audited across shelves, section headers, cards, search results, and See All grids: headers carry header traits, cards expose titles, See All controls expose hints, and skeletons remain non-controls (`beamfall-apple: Sources/BeamfallTV/TVHomeModel.swift` + `Sources/BeamfallTV/TVPlayerView.swift`, 2026-06-12) | **pass** — Manager browse/detail section titles are real accessibility headers via `.accessibilityAddTraits(.isHeader)` across browse, See All, entity detail, jobs, results, and trash blocks; no custom Manager transport surface exists (`beamfall-apple: Sources/BeamfallManager/ManagerApp.swift`, 2026-06-12) | **pass** — the transport scrubber is compliant (`MobilePlayerView.swift:693-694` exposes the position with `.accessibilityLabel`/`.accessibilityValue`; player controls/Up Next/info carry labels and values), and the browse surface now exposes structure: `MobileSectionView` section titles carry `.accessibilityAddTraits(.isHeader)` (`MobileRootView.swift:385`) and every card type carries a contract title label — poster/hero/row at `MobileComponents.swift:32`/`:70`/`:107` (`beamfall-apple: Sources/BeamfallMobile/`, 2026-06-13) | **pass** — sections use real `section`/`h2`, lists use `ul`/`li`, skeletons are `aria-hidden`, controls are native buttons; the custom web transport implements the playback-route spec's SR contract: `role="slider"` scrubber with `aria-valuetext`, listbox menus with focus trap, skip announcements via `aria-live`, one reveal-safe terminal message (`internal/web/app/src/areas/viewer/` + `internal/web/app/src/areas/viewer/player/PlayerTransport.tsx`, 2026-06-12) | **pass** — `PlayerProgress` is now a focusable adjustable scrubber with `progressBarRangeInfo` + steppable `setProgress` + `stateDescription` (spoken position) plus a polite live-region for ±10s/scrub seeks, and section titles carry `heading()` semantics (`beamfall-android` `af19638`, both apps, 2026-06-12) |

**Verdict counts** — tvOS: 8 pass, 2 partial, 0 missing. macOS Manager: 7 pass, 2 partial,
0 missing, 1 n-a. Web UI: 7 pass, 3 partial, 0 missing. iOS Viewer: 6 pass, 2 partial,
0 missing, 2 n-a. Android Compose: 6 pass, 4 partial, 0 missing, 0 fail. The shared Apple
rules (R4, R5) pass on tvOS/macOS/iOS *because* they live in `LayoutRenderPlanner`, which
the iOS Viewer consumes; the iOS `partial` rows are view-layer artwork (R6) and per-Section
hydration (R7) the planner cannot do for a renderer (browse-surface accessibility R10 closed
2026-06-13), and its two `n-a` rows (R8/R9) are the touch-surface + no-Snapshot rationale. **Android
Compose** routed browse through `LayoutRenderPlanner` 2026-06-12 (`af19638`), flipping
R1/R2/R4/R5 to pass and R8/R10 to pass; its remaining cells are the cross-client view-layer
gaps — R3 initial-focus anchor, R6 artwork content-addressing, R7 per-Section hydration —
plus R9 offline honesty (Snapshot store + honest connection-error screen landed 2026-06-13;
the stale-Snapshot-render-with-banner shape remains). The Web UI still needs a planner
port or equivalent tests for R1-R5/R7 to stay stable.

### R5 known-section-kind divergence — closed (2026-06-13)

R5 is fail-safe everywhere (no renderer crashes on a kind it doesn't handle). The *known-kind
set each renderer actually renders* previously diverged — silently dropping **known** platform
kinds on some renderers even though the contract test passed — but that drift is now closed:
**every renderer renders the full set `{hero, shelf, grid, list, info, filter-bar}`.**

- **Apple planner** (`LayoutRenderPlanner.swift:4-9`, consumed by tvOS/macOS/iOS) and the
  **Android `kit/` planner** (`LayoutRenderPlanner.kt:4`) render the full set.
- **Android `BrowseRepository`** routes through the `kit/` planner (2026-06-12, `af19638`),
  so it renders the full set — the prior `{hero, shelf, grid, list}` divergence is closed.
- **Web UI** now renders `info` too: `SECTION_KINDS` carries the full set and `buildSection`
  routes an `info` Section through the same SourceItems item path as `list` (a labeled stack
  of metadata rows, not a poster rail — mirroring how the canonical renderers route it),
  tested in `app.test.mjs` (2026-06-13, `internal/web/app/src/areas/viewer/`). The prior
  `{hero, shelf, grid, list, filter-bar}` divergence — the last open gap — is closed.

### R5-semantics — REOPENED for Android mobile + Apple mobile (2026-07-01, ADR-0134)

The 2026-06-13 closure above tested **kind acceptance** (the planner does not drop the
Section), not **kind semantics** (the renderer produces the UI the kind means). ADR-0134
makes semantics the conformance definition, and under it the claim "no known platform
Section kind silently disappears" is false today:

- **Android mobile** (`beamfall-android` `MobileBrowseScreen.kt`) special-cases only `hero`;
  `grid`/`list`/`info`/`filter-bar` all render as horizontal poster shelves. The app-facing
  `BrowseSection` (`beamfall-android-ui` `BrowseRepository.kt`) carries no `filters`, so a
  `filter-bar`'s chips are dropped at the model layer — filmtv's `browse-filter` renders as
  a duplicate poster shelf with no filtering. Additionally the R1 "See All escalates to a
  grid of the full result" cell above is dead by construction on mobile: `hasMoreItems`
  derives from a fetch already capped at the display limit, so the See-All card never
  renders and content past the cap is unreachable (ADR-0134 §3).
- **Apple mobile** (`beamfall-apple` `MobileLibraryView.swift:170-188`) special-cases `hero`
  and `list`; `grid`/`info`/`filterBar` fall to the poster-shelf branch, and the Apple kit
  never decodes `FilterOption` at all.
- **Web** renders all six kinds semantically (`FilterBar.tsx` chips, real grids/lists) —
  it is the only client where a plugin's `filter-bar` works.

Closure requires the RENDPAR roadmap tickets (per-renderer semantic-kind conformance tests,
`BrowseSection.filters` / Apple `FilterOption` decode, reachable See-All paging).

## How to add a renderer

When a new Section renderer starts (iOS, Web, Android Compose — ADR-0017 §2, ADR-0022/0023):

1. **Build on the shared planning layer where one exists.** On Apple platforms, consume
   `LayoutRenderPlanner` (`beamfall-apple: Sources/BeamfallKit/LayoutRenderPlanner.swift`)
   — do not re-derive caps/collapse/bounds/focus math in the view. Web and Android port the
   *planner contract* (same inputs → same `RenderPlan` semantics, same diagnostic codes)
   and mirror `LayoutRenderPlannerTests` against it; that keeps R1–R5/R7 drift impossible
   by construction.
2. **Copy a column, not a row.** Replace the renderer's "not built" column with a real
   verdict per rule: `pass` / `partial` / `missing` / `n-a`, each with a one-line note
   naming the file that implements (or fails to implement) the rule. `n-a` requires a
   rationale (like the Manager's R9 cell), not a shrug.
3. **A `pass` means view-layer behavior, not planner output.** Verify the rendered result:
   focus actually lands (R3), skeletons actually can't be focused on that platform's focus
   engine (R7), the drop actually logs (R4).
4. **Do the pass before shipping**, and re-run it whenever the renderer's Section/focus/
   artwork code changes. Update the verdict counts above.
5. **Track the gaps.** Any `partial`/`missing` cells the new column introduces stay visible
   here and in the roadmap until closed.
