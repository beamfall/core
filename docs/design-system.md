# Beamfall design system

Status: v1.0 — binding
Date: 2026-06-11

Ownership: `../beamfall-design-system` owns canonical tokens, mockups, and generated assets; this repo owns the binding written specs.

This document defines the product and interface design system for Beamfall across the Web UI,
tvOS Viewer, Android Viewer, iPhone/iPad Viewer (the iPad doubling as a Manager, ADR-0023),
macOS Manager, Console, and future client packages. It is grounded in
the current glossary and ADR set, especially the generic plugin architecture, Profiles and
Restrictions, the client rendering contract, native client parity, store readiness, and the
Beamfall rename.

## Source constraints

The design system is constrained by these project decisions:

- Beamfall Core is domain-agnostic. Core UI and public App Store surfaces must not use
  plugin-specific vocabulary. Domain language comes from Plugins through display labels.
- The wire entity is always **Media**. Plugin labels such as movie, episode, or scene are labels,
  not schema or base-client identifiers.
- Plugins contribute unbounded data against small fixed vocabularies. For UI, the important
  vocabularies are **Archetype**, **Artwork Slot**, **Section**, **Source**, and **Theme Token**.
- Plugins may set only a Library accent color and Library icon or wordmark. Typography, spacing,
  focus effects, motion, component behavior, and accessibility belong to the clients.
- The Reveal contract, named **Restrictions** in App Store-facing copy, is server-enforced and
  complete across navigation, search, Sources, stats, presence, event streams, tombstones, artwork,
  sidecars, and playback bytes.
- Native clients sell polish, speed, platform fit, and native capabilities. The free Web UI must
  remain complete and credible.

## Design north star

**Quiet cinematic utility.**

Beamfall should feel like a trustworthy living-room appliance and a serious library workbench:
cinematic while watching, precise while managing, plain-spoken while asking for trust. The visual
language draws from the product name: a projector beam falling through a dark room onto a screen.
Use that metaphor as atmosphere and motion, not as decorative clutter.

Beamfall is not Plex-orange, not Jellyfin-purple, and not a developer
dashboard wearing a media grid. It should look consumer-ready on an Apple TV home screen while
remaining legible and dense enough for a 40 TB self-hosted library.

## Principles

### 1. Structure is flexible; behavior is fixed

Plugins can compose screens through Layouts and Sections. They cannot introduce new rendering code,
fonts, spacing, focus behavior, or arbitrary visual systems. A new Domain Plugin should feel native
to Beamfall immediately because the client design system owns the chrome.

### 2. Domain-clean by default

Core and store-facing UI uses Beamfall terms: Media, Library, Profile, Restrictions, Manager,
Console, Collection, Tag, Marker, Sidecar Track, Snapshot. Plugin labels may replace visible nouns
inside plugin-declared content areas, but base navigation, settings, empty states, errors, and
review/demo flows stay generic.

### 3. Native idiom over pixel parity

tvOS, Android TV, web, and macOS share contracts and semantics, not identical pixels. A row, focus
state, track menu, and playback route must mean the same thing everywhere. Their implementation
should use the host platform's best interaction model.

### 4. Cinematic when watching, dense when managing

Viewer surfaces prioritize artwork, motion, focus clarity, and playback confidence. Manager and
Console surfaces prioritize scanning, comparison, preview, batch safety, and auditability. One
design system supports both through register-specific density and theme tokens.

### 5. Trust is shown through state, not theater

Do not lean on vault imagery, spy motifs, lock wallpaper, or ominous secrecy. Beamfall earns trust
through visible provenance, dry runs, restore paths, restrained copy, clear device/session state,
and honest direct-play/transcode/relay explanations.

### 6. Recovery is a first-class visual pattern

Trash is on by default. Bulk operations preview before executing. Batch operations have one undo
unit. The UI should consistently show: what will change, what changed, what can be restored, and
what cannot be undone when Trash is disabled.

### 7. Performance is part of design

The visual system must support <1s Snapshot launch targets, 60fps 10-foot browsing, bounded
sections, fixed artwork derivatives, skeletons that are never focusable, and stable focus during
hydration and relock.

### 8. Accessibility is a component contract

VoiceOver, screen readers, D-pad focus, keyboard focus, captions, reduce-motion, contrast, and
focus order are specified with components, not audited after implementation.

## Surface registers

### Viewer

Surfaces: web Viewer, tvOS Viewer, Android TV Viewer, Android mobile Viewer (phones + tablets,
ADR-0022), viewer mode on macOS.

Tone: cinematic, calm, fast. Artwork leads. Chrome recedes until focused or invoked. The viewer
should feel like a native media client, not an admin app with videos.

Default theme: Cinema Dark.

Key patterns:

- Profile picker and PIN entry
- Library home screens from Layouts
- Hero, shelf, grid, list, info, filter-bar Sections
- Search with Snapshot-first and server FTS states
- Unified player transport
- Restrictions reveal/relock UI
- Offline/stale Snapshot banners
- Top Shelf and Engage launcher content under intersection-only rules

### Manager

Surfaces: macOS Manager, web Manage area on promoted MANAGER browsers.

Tone: editorial workbench, precise, reversible. Optimized for comparison and repeated actions.
Native macOS may be richer, but the web manager must carry full curation parity.

Themes: Workbench Light and Workbench Dark. Workbench Light is the default for long curation
sessions, setup, and dense comparison. Workbench Dark is first-class for low-light desktop use and
for operators who keep the Viewer and Manager visually close together.

Key patterns:

- Unmatched queue and fix-match
- Dedupe and keep-as-Version
- Bulk edit
- Batch rename and organize preview
- Artwork candidates and crop-to-slot
- Field locks and source provenance
- Quality policy and upgrade suggestions
- Library health
- Trash review and restore
- Activity log and undo

### Console

Surfaces: Web UI Admin area.

Tone: server-shaped, restrained, explicit. This is operations, setup, policy, devices, jobs, and
plugins. It should feel like an appliance control plane, not enterprise admin software.

Themes: Workbench Light and Workbench Dark. Workbench Light is the default for setup, audits, and
operations because it keeps forms and tables legible. Workbench Dark is first-class for always-on
dashboards, live sessions, logs, and low-light appliance monitoring.

Key patterns:

- First-run wizard
- Library roots, plugin binding, Restrictions flag
- Plugin activation, config, outbound endpoints
- Profiles and access policies
- Device enrollment, revoke, promote
- Admin Session re-auth
- Layout editor
- Job dashboard and live sessions
- Hardware transcode probe
- Backup, migration, version, update, metrics, logs
- Hosted Relay claim-code setup and status

### Store and marketing

Tone: plain, pledge-oriented, consumer-first. Public copy should emphasize native polish over a
user-owned server, honest pricing, real privacy, and the lifetime pledge. Use movies, TV, and family
archive examples in public materials.

Do not imply server-side
licensing, cloud entitlement, telemetry, or relay-only remote access.

### Companion and Integration plugin surfaces

Tone: inherited. Plugin-specific richness lives inside declared Extension Points or config/status
surfaces, but it inherits Beamfall controls, spacing, focus, typography, accessibility, and safety
patterns. Integration plugins may have richer native Mac companion views, but Core and store clients
remain clean.

## Theme system

Use semantic tokens first. Primitive color names are implementation details. All colors are OKLCH.
Do not use pure black or pure white.

### Primitive palette

| Token | OKLCH | Use |
|---|---:|---|
| `color.night.950` | `oklch(13% 0.012 255)` | Cinema dark root background |
| `color.night.900` | `oklch(18% 0.014 255)` | Dark surfaces |
| `color.night.800` | `oklch(24% 0.014 250)` | Raised dark panels |
| `color.paper.50` | `oklch(97% 0.006 102)` | Light root background |
| `color.paper.100` | `oklch(94% 0.008 102)` | Light secondary background |
| `color.paper.200` | `oklch(89% 0.010 98)` | Light borders and disabled fills |
| `color.ink.950` | `oklch(18% 0.018 255)` | Primary text on light |
| `color.ink.700` | `oklch(36% 0.020 255)` | Secondary text on light |
| `color.mist.200` | `oklch(86% 0.018 235)` | Secondary text on dark |
| `color.mist.100` | `oklch(93% 0.012 235)` | Primary text on dark |
| `color.beam.500` | `oklch(82% 0.145 92)` | Brand accent, focus glow, selected hero actions |
| `color.cyan.500` | `oklch(74% 0.120 218)` | Remote, relay, network, stream state |
| `color.green.500` | `oklch(70% 0.140 150)` | Success, direct-play, healthy |
| `color.iris.500` | `oklch(67% 0.130 310)` | Restrictions, reveal session, PIN state |
| `color.amber.500` | `oklch(78% 0.160 78)` | Warning, queued, transcode pressure |
| `color.red.500` | `oklch(64% 0.170 28)` | Destructive, failed, revoked |

The brand accent is beam light, but it must stay below about 10% of most product screens. Viewer
hero art may carry more warmth when the artwork itself justifies it. Console and Manager surfaces
should use the accent mostly for selected state, focus, and primary action.

### Semantic roles

| Semantic token | Cinema Dark | Workbench Light | Workbench Dark |
|---|---|---|---|
| `bg.root` | `color.night.950` | `color.paper.50` | `oklch(14% 0.012 255)` |
| `bg.surface` | `color.night.900` | `oklch(99% 0.004 102)` | `color.night.900` |
| `bg.surfaceRaised` | `color.night.800` | `color.paper.100` | `oklch(21% 0.014 252)` |
| `border.subtle` | `oklch(31% 0.014 250)` | `color.paper.200` | `oklch(31% 0.014 250)` |
| `text.primary` | `color.mist.100` | `color.ink.950` | `color.mist.100` |
| `text.secondary` | `color.mist.200` | `color.ink.700` | `color.mist.200` |
| `accent.brand` | `color.beam.500` | `oklch(63% 0.135 92)` | `color.beam.500` |
| `accent.focus` | `color.beam.500` | `oklch(70% 0.135 92)` | `color.beam.500` |
| `state.direct` | `color.green.500` | `oklch(58% 0.135 150)` | `color.green.500` |
| `state.remote` | `color.cyan.500` | `oklch(56% 0.120 218)` | `color.cyan.500` |
| `state.restricted` | `color.iris.500` | `oklch(55% 0.120 310)` | `color.iris.500` |
| `state.warning` | `color.amber.500` | `oklch(62% 0.145 78)` | `color.amber.500` |
| `state.danger` | `color.red.500` | `oklch(56% 0.160 28)` | `color.red.500` |

### Library accents

A Library may provide one accent color and one icon or wordmark. The client must normalize the
accent into accessible foreground, tint, and focus variants. Plugin accent is never allowed to
change type, spacing, focus behavior, card geometry, or motion.

Accent use:

- Library switcher active mark
- Section heading marker, optional and subtle
- Selection state in Library-scoped filters
- Artwork fallback gradient
- Tiny status tint where the status belongs to that Library

Accent non-use:

- Global nav chrome
- Destructive actions
- Admin Session or Device Role state
- Restrictions state
- Player transport controls
- Typography scale

## Typography

Use platform system fonts by default.

- Apple clients: SF Pro / SF Pro Rounded only where system idiomatic.
- Android: Roboto and Material type roles.
- Web: system UI stack first; consider Inter Variable only if it measurably improves cross-platform
  consistency and is self-hosted.

Type should feel precise, not branded for its own sake. Let artwork carry emotion.

### Type roles

| Role | Web desktop | TV | Use |
|---|---:|---:|---|
| Display | 40/48, 650 | 56/64, 650 | Hero title, profile picker title |
| Title 1 | 28/36, 650 | 40/48, 650 | Page title, major detail title |
| Title 2 | 22/30, 620 | 30/38, 620 | Section title, panel title |
| Body | 15/22, 450 | 24/32, 450 | Descriptions, settings copy |
| Body compact | 13/18, 450 | Not used | Tables, metadata rows |
| Label | 12/16, 600 | 20/26, 600 | Buttons, chips, badges |
| Mono / numeric | 13/18 tabular | 20/26 tabular | Bitrate, duration, job progress, versions |

Rules:

- Use tabular numerals for runtimes, counts, percentages, progress, and bitrates.
- Keep body copy to 65-75 characters per line.
- Do not scale type directly with viewport width.
- Do not use negative letter spacing.
- Use weight and size before color for hierarchy.

## Layout, density, and geometry

Base spacing unit: 4px.

Density modes:

| Mode | Spacing sequence | Use |
|---|---|---|
| `dense` | 4, 8, 12, 16, 20 | Console tables, metadata grids, manager sidebars |
| `standard` | 8, 12, 16, 24, 32 | Web Viewer, forms, settings, detail pages |
| `tv` | 16, 24, 32, 48, 64 | tvOS and Android TV 10-foot layouts |

Radii:

- Small controls: 6px
- Cards and artwork frames: 8px maximum
- Full player overlays and sheets: platform default where native, otherwise 8px
- Do not nest cards inside cards

Elevation:

- Prefer contrast, borders, and z-order over heavy shadows.
- Viewer overlays may use a dark scrim for legibility.
- Avoid decorative glassmorphism. Blur is allowed only for native platform chrome or player
  overlays where it improves legibility and performance is proven.

## Motion

Motion should feel like optical settling: quick focus, smooth reveal, no bounce.

Tokens:

- `duration.instant`: 80ms
- `duration.fast`: 140ms
- `duration.normal`: 220ms
- `duration.slow`: 360ms
- `duration.reveal`: 420ms
- `ease.standard`: cubic-bezier(0.22, 1, 0.36, 1)
- `ease.emphasis`: cubic-bezier(0.16, 1, 0.3, 1)

Rules:

- Never animate layout properties.
- TV focus may scale artwork to 1.04-1.08 depending on slot size, with no layout shift.
- Skeletons fade to content; they are never focusable.
- Reveal materializes Sections with focus preservation. Relock dematerializes content without
  yanking focus; move focus to the nearest stable Library or navigation anchor.
- Player chrome appears fast and exits slowly enough to preserve orientation.
- Respect reduce-motion: remove scale, keep opacity and instant state changes.

## Artwork system

Beamfall artwork is slot-based and derivative-based. Clients ask for an Artwork Slot; the server
resolves fallback.

Slots:

- `poster`: 2:3, primary vertical media/entity art
- `backdrop`: 16:9, wide text-safe art
- `thumb`: 16:9 still
- `logo`: transparent wordmark
- `portrait`: 2:3 person/entity headshot

Components:

- `ArtworkFrame`: fixed aspect ratio, derivative-aware image loading, shimmer/skeleton, fallback
  frame, unavailable state.
- `ArtworkPicker`: candidates per slot, upload, URL fetch, crop-to-aspect, lock state, source
  provenance.
- `ArtworkFallback`: generated from title, Library accent, and slot geometry. Never use generic
  grey boxes on final surfaces.

Rules:

- Layout must not shift as images load.
- Fallbacks must be visually acceptable at TV scale.
- Missing artwork is a curation state, not a broken UI state.
- Derivative buckets are fixed; no per-request resizing assumptions in client components.

## Section renderers

These are the core browse primitives. Each renderer must implement the ADR-0014 defensive Layout
rules: item caps, empty collapse, bounded sections, guaranteed initial focus, unknown vocabulary
skip, hydration policy, and accessibility semantics.

| Section | Purpose | Key behavior |
|---|---|---|
| `hero` | Prominent featured Media, Entity, or Library state | One focusable primary action, optional secondary action, backdrop or generated art, no autoplay video by default |
| `shelf` | Horizontal rail for bounded browse | Max visible source set before See All, focus-restoring, fast inertial movement on TV |
| `grid` | Paginated or virtualized browse | Stable item geometry, filter/sort aware, keyboard and D-pad pagination |
| `list` | Dense or metadata-heavy rows | Good for Manager and Console, supports multi-select where authorized |
| `info` | Detail metadata and explanatory state | Renders provenance, field locks, file/version facts, restrictions context |
| `filter-bar` | Source/filter controls | Chips, saved filters, sort menus, no raw query language exposed |

## Core components

### Controls

- `Button`: primary, secondary, quiet, danger. Icon plus label when useful.
- `IconButton`: transport, toolbar, table actions. Always has tooltip or accessible label.
- `SegmentedControl`: playback quality, view density, match/display modes.
- `Toggle`: binary settings.
- `Checkbox`: multi-select and batch inclusion.
- `Slider`: playback position, subtitle offset, resource budget.
- `Stepper/Input`: numeric policy values.
- `Menu`: track selection, sort, Library picker, quality options.
- `Tabs`: Console sections and detail subviews.
- `CommandBar`: Manager bulk actions and Console page actions.
- `StatusBadge`: direct-play, direct-stream, transcode, remote, queued, restricted, stale,
  revoked, missing, healthy.

Use icons from the platform set:

- Apple: SF Symbols
- Android: Material Symbols
- Web: Lucide, unless shadcn/ui or platform guidance points elsewhere

### Feedback

- `Banner`: persistent page-level state, such as offline Snapshot, migration available, remote
  HTTP warning, setup required.
- `Toast`: short completion feedback, never critical authorization or destructive state.
- `InlineError`: form and row-level error.
- `EmptyState`: no content, all sections collapsed, no search results, no jobs, no devices.
- `ProgressRow`: job progress, scan, generate, copy, backup, transcode probe.
- `DryRunReview`: preview-first change-set with per-item result and batch summary.
- `UndoNotice`: activity-log-backed reversible action with clear expiry or permanence.

### Data display

- `DataTable`: Console and Manager tables. Supports column density, stable row height, status
  badges, row actions, keyboard navigation, and reveal-filtered empty states.
- `MetadataGrid`: key/value facts on detail screens.
- `ProvenanceCell`: source name, lock state, imported/backdated marker, scraper confidence.
- `HealthCard`: library health issue summary, never the hero-metric cliche.

## Content components

### Cards

Cards are for repeated items only: Media, Entity, Library, Profile, job, device, plugin. Do not use
cards as page sections.

Card variants:

- `MediaCardPoster`
- `MediaCardThumb`
- `MediaCardBackdrop`
- `EntityCardPortrait`
- `EntityCardPoster`
- `LibraryCard`
- `ProfileCard`
- `DeviceCard`
- `PluginCard`
- `JobCard`

Every card has:

- Stable dimensions
- Known artwork slot
- Title and optional subtitle
- Optional progress/resume indicator
- Optional status badge
- Focus, hover, selected, disabled, loading, stale, and missing states

### Detail screens

Detail screens should be information-rich without feeling like admin pages.

Required modules:

- Artwork and title lockup
- Primary action
- Resume/progress
- Versions and quality affordance
- Editions grouping
- Extras shelf
- Markers/chapters
- Tags and Collections
- Related Entities by Archetype
- Field provenance and locks in Manager mode
- File facts and stream inventory in Manager mode

## Playback transport

Beamfall has one custom transport UI across playback engines.

Required controls:

- Play/pause
- Seek +/-10s
- Scrubber with thumbnail previews
- Runtime and remaining time
- Chapter/Marker strip
- Skip segment actions
- Audio track menu
- Subtitle track menu
- Subtitle style and sync controls where supported
- Quality menu: version, direct/direct-stream/transcode, auto/manual
- Route/engine status: direct-play, direct-stream, transcode, remote/relay
- Up Next and autoplay countdown
- Info overlay

Required states:

- Buffering
- Starting direct play
- Starting transcode
- Admission blocked or queue suggestion
- Manual quality pinned
- Remote path degraded
- Token expired or session relocked
- Device revoked
- Track unavailable on current engine

Accessibility:

- Scrubber is adjustable with spoken current time and duration.
- Track menus have defined focus order.
- Skip actions announce what was skipped.
- Overlays never trap focus.
- Captions honor platform caption preferences where possible.

## Android mobile

Decisions from the 2026-06-11 mobile design grilling. The mobile Viewer is part of the launch
surface (ADR-0022); this section is its design posture.

### Material posture: structure yes, dynamic color no, Expressive no

- **Material structure**: standard Material 3 components, bottom navigation (phone) / navigation
  rail + list-detail (tablet) via window-size classes, predictive back, pull-to-refresh,
  persistent search affordance. The shell is client chrome; content inside each destination is
  Layout-rendered through the six Section renderers (ADR-0022 §5).
- **No Material You dynamic color.** Beamfall's OKLCH tokens map into a fixed Material 3
  `ColorScheme`. Wallpaper-derived color would fight the Library accent system and the brand —
  the Netflix/Plex/Spotify posture.
- **No Material 3 Expressive motion.** The Beamfall motion tokens apply (optical settling, no
  bounce), implemented with Compose animation primitives.
- **Cinema Dark only at launch.** No light Viewer theme; Workbench Light remains a
  Manager/Console concern.
- Type: Roboto via Material type roles, mapped to the Beamfall type roles above (Display →
  displaySmall, Title 1 → headlineMedium, Title 2 → titleLarge, Body → bodyLarge, Label →
  labelLarge); tabular numerals and the line-length/weight rules apply unchanged. Density mode:
  `standard`. Touch targets: 48dp minimum.

### Touch gesture vocabulary (player)

The established gesture set, no inventions. Every gesture is a shortcut — the on-screen
transport remains complete (this is also the accessibility contract) — and behavior is
identical on the ExoPlayer and libmpv rungs (conformance test row).

- Double-tap left/right half: seek ∓/± 10s, repeated taps accumulate
- Vertical swipe right half: volume; left half: screen brightness (player-local, restored on exit)
- Horizontal drag: scrub with thumbnail preview
- Long-press: temporary 2× speed while held
- Pinch: toggle fit/fill for aspect-mismatched content
- Single tap: chrome toggle; swipe down: exit player (predictive-back compatible)

Excluded: swipe-to-mini-player (in-app floating player). System PiP covers that job.

### Performance budgets (floor device: Pixel 4a / Galaxy A51 class; release gates)

1. Cold launch → rendered content: **<1s** (Snapshot-first; never spinner-then-home).
2. Tap-to-first-frame: **<2s** LAN direct-play, **<4s** transcode start — shown honestly in
   route/engine status when slower.
3. Browse scroll: zero dropped-frame budget on Section scroll; no skeleton ever visible for
   Snapshot-resident content (skeletons only for server fetch: search, reveal, deep pagination).
4. Profile switch (incl. PIN/biometric): **<1s** to the new Profile's rendered home.

Anti-budget: **no startup interstitials, ever** — no branding splash beyond the system launch
screen, no update checks, no upsell screens. The brand statement is the absence of waiting.

### First-run: meeting the server

Pairing-codes-only enrollment (ADR-0012), three paths in order of prominence:

1. **LAN auto-discovery** (mDNS/NSD): discovered servers listed on first launch; tap, type the
   Console's pairing code. No addresses or ports.
2. **QR enrollment**: the Console's device-enrollment screen renders a QR encoding server
   address(es) + the pairing code; the phone scans and lands in the Profile picker. Pure
   transport for the existing pairing-code flow — same code, same expiry, no new security
   surface. Also the easy path for remote servers, where mDNS cannot see. (Console addition:
   render existing pairing data as QR.)
3. **Manual address entry**, accepting overlay/ACME remote hostnames (ADR-0012).

Target: box-fresh install → playing content in **under 60 seconds** on the same LAN. Empty
states never dead-end ("no server found" → install pointer + QR/manual paths), mirroring the
first-run wizard's no-dead-ends rule.

## Apple mobile and the iPad Manager (Liquid Glass)

Decisions from the 2026-06-11 Apple device grilling (ADR-0023). iPhone and iPad are part of
the launch surface; the iPad additionally carries the full Manager + Console. This section is
their design posture.

### Liquid Glass posture: system materials yes, custom chrome no

- **iOS/iPadOS 26+ exclusively** — one design system, no legacy chrome. Navigation, tab bars,
  toolbars, sheets, and search use the system Liquid Glass components untinted; Beamfall never
  paints opaque custom bars that fight system materials (the same restraint as the Android
  no-dynamic-color rule, inverted: there we refuse the system's color, here we accept the
  system's material).
- **Cinema Dark content under glass chrome.** The Viewer's content canvas keeps the Beamfall
  Cinema Dark tokens; glass surfaces float above it and pick up the canvas naturally. Library
  accents appear in content (cards, accent details), never as glass tint.
- **Shell**: iPhone tab bar (system, glass) / iPad `NavigationSplitView` with sidebar — both
  client chrome, with content inside each destination Layout-rendered through the same six
  Section renderers (defensive rules, ADR-0014). iPad is a full pointer + hardware-keyboard
  citizen (hover effects, menu-key shortcuts, Stage Manager–correct resizing).
- Type: SF Pro via system text styles, mapped to the Beamfall type roles (Display →
  largeTitle, Title 1 → title1, Title 2 → title2, Body → body, Label → headline); tabular
  numerals and the line-length rules apply unchanged. Touch targets: 44pt minimum.

### The iPad Manager register

The Workbench register (Manager patterns above) rendered in iPad idiom, at web-Manager parity
plus the Console via Admin Session (ADR-0023 §2). Idiom, not new capability: list-detail
manager screens in the split view, drag-and-drop for artwork slots and Collection membership,
swipe actions for queue triage, multi-select with the system edit mode for bulk operations,
and the same destructive-ops choreography as every Manager surface (Trash ON, undo toast,
audited mutations). Manager screens use Workbench density and may use Workbench Light;
the role gate is visible state (the Device Role chip), never a hidden mode.

### Touch gesture vocabulary (player)

Identical to the Android vocabulary above, gesture for gesture, on both the AVPlayer and
libmpv rungs (same conformance test row). One addition: the **Beam** affordance lives in the
transport as a first-class control — one tap lists enrolled targets, one tap transfers the
session at the canonical-timeline position; the receiving device's transport shows a
"beamed from" affordance to pull it back. Beam is the signature interaction: it must feel
instant (pre-warmed target session) and reversible.

### System surfaces (intersection-only, hidden-state at rest)

Widgets, Live Activities, Siri/Spotlight App Intents, and SharePlay all ship at launch
(ADR-0023 §7) under one design rule: a system surface only ever shows the intersection world
— content visible to every Profile without Reveal — and persists nothing beyond the Snapshot
at-rest rules. Live Activities cover playback and iPad Manager jobs (import/transcode
progress). SharePlay sessions are composed under the session intersection rule; content that
can't be offered to the group simply doesn't appear in the share sheet (no "locked" teasers —
absence, not tells).

### Offline Packs UI

Download affordances appear only where packing is permitted; pack management is per-Profile
(storage, eviction, expiry shown honestly). Pack *metadata* on system surfaces and in the app
switcher follows the hidden-state rule; restricted pack content is visible only inside an
unlocked, revealed session, and the biometric unlock is the same gesture as Reveal — no
second ceremony.

### Performance budgets (floor device: iPhone 11 class; release gates)

ADR-0023 §9's gates, sharing the Android anti-budget (**no startup interstitials, ever**):

1. Cold launch → interactive Layout-rendered home: **<1.5s** gate, parity with Android's
   <1s as the target (Snapshot-first; never spinner-then-home).
2. Tap-to-first-frame: **<1s** LAN direct-play; seek-to-frame **<500ms**.
3. Browse scroll: zero dropped frames at 100k items — 120Hz on ProMotion, 60Hz floor.
4. Accessibility as gates, not targets: full VoiceOver (ADR-0014 spec in iOS idiom),
   Dynamic Type, Reduce Motion.

### First-run: meeting the server

The Android first-run flow verbatim with platform spelling: Bonjour discovery, QR enrollment
via the same Console QR, manual address entry. Same target: box-fresh install → playing
content in under 60 seconds on the LAN.

## Profiles, Restrictions, and authorization

### Profile picker

Profiles are not accounts. Do not use login language.

Profile cards should show:

- Avatar
- Name
- Managed/adult state only when context needs it
- PIN requirement
- Last active hint only when reveal-safe

PIN entry should be calm, full-screen or sheet-based depending on platform, with attempt throttling
messaging that does not expose security internals.

### Restrictions and reveal

External/store-facing label: **Restrictions**. Internal docs may say Reveal contract. Product UI
should use Restrictions except in developer/admin documentation where the contract itself is being
named.

States:

- Restricted Library hidden
- Restricted Library visible during revealed session
- Relock pending
- Relocked
- Reveal unavailable for managed Profile
- Reveal expired because app backgrounded, idle timeout elapsed, or profile switched

Visual treatment:

- Use `state.restricted` sparingly.
- Never imply restricted content exists to an unrevealed session.
- Relock copy says content was hidden again; it does not name what disappeared.
- Search empty states must not distinguish none found from restricted filtered.

### Device Role and Admin Session

Device Role is the machine's capability. Profile is the person's view. Admin Session is a short
server-shaped privilege. These should be visually distinct.

- Device Role badges: VIEWER, MANAGER
- Admin Session badge: active, expiring, re-auth required
- Promotion/revoke flows: explicit, audited, reversible where possible
- MANAGER actions never appear as merely disabled in a VIEWER device if showing them would teach
  the existence of restricted or unauthorized capabilities in the wrong context

## Manager patterns

### Unmatched queue and fix-match

Layout:

- Left: queue with confidence/status
- Center: candidate comparison
- Right: selected candidate fields, artwork, provenance, actions

Actions:

- Apply
- Search again
- Pin match
- Skip
- Mark as generic

### Dedupe and merge

Show same-content, same-work, and version-suggestion cases differently. The user should never have
to infer whether an item is being trashed, kept as Version, or linked as Edition.

Required UI:

- Fingerprint and phash evidence
- File facts side by side
- Engagement/markers/relations survival summary
- Merge-and-trash
- Keep as Version
- Keep separate
- Dry-run preview
- Trash restore path

### Bulk operations

Every bulk action uses `DryRunReview` before execution.

The review screen shows:

- Count selected
- Count changed
- Count unchanged
- Count blocked
- Per-item planned action
- One batch envelope equals one activity entry and one undo unit

### Trash and restore

Trash is not a scary graveyard. It is a recoverable-delete store.

Show:

- Retention window
- Original path
- Collision handling
- Restore result
- Permanent delete only with clear confirmation, especially if Trash is disabled

## Console patterns

### First-run wizard

Steps:

1. Claim server with setup token
2. Set admin password
3. Create owner Profile
4. Add first Library
5. Pick or activate Plugin
6. Configure scan and resource defaults
7. Optional update check, shown plainly
8. Finish into Viewer with Console available

The wizard must avoid dead ends: demo connection for app review, headless env path for Docker,
and clear next actions for users with no media mounted yet.

### Jobs and operations

Job dashboard should distinguish:

- Running
- Queued
- Yielded/preempted
- Cancelled
- Failed
- Completed

Interactive transcode preemption is a product feature. Explain it plainly when a background job
yields: playback was prioritized.

### Hardware transcode probe

Never show a faith-based dropdown alone. Show detected devices, test result, supported mode, and
what failed.

### Plugin configuration

Show outbound endpoints, credentials storage state, activation state, Library binding, and status
feeds. Third-party data-only bundles should feel constrained and inspectable.

### Hosted Relay

Relay is infrastructure, not entitlement.

UI should show:

- LAN, direct remote, and relay path order
- Claim Code entry
- Connected region
- Fair-use policy summary
- Byte-blind privacy explanation
- What metadata the relay can see
- Self-hosted remote paths remain free

Do not place relay purchase upsells in store binaries.

## Copy rules

| Use | Avoid |
|---|---|
| Media | Scene, video, clip as core nouns |
| Library | Section for top-level content scope |
| Section | View Type, component, template |
| Profile | Account, login, user |
| Restrictions | Vault, hidden mode, private collection |
| Manager | Admin panel, server admin |
| Console | Admin panel |
| Trash | Recycle bin |
| Sidecar Track | Domain-specific sidecar names in Core |
| Claim Code | Pairing code for relay setup |
| Device Role | User role |
| Direct-play, direct-stream, transcode | Magic quality, optimized automatically |

Copy should be short, literal, and non-judgmental. State what the system is doing and why.

Examples:

- "This Library is restricted for this Profile."
- "Relocked. Restricted Libraries are hidden again."
- "Playback is using server transcode because this device cannot play the selected audio track."
- "This batch will move 42 files. Trash is on, so replaced files can be restored."
- "The relay cannot see your media. It can see hostnames, connection metadata, and traffic
  volume."

## Accessibility and focus

Baseline requirements:

- WCAG AA contrast for text and meaningful controls.
- Visible focus on every interactive element.
- TV focus never depends on color alone.
- Focus order follows visual order.
- Loading skeletons are not focusable.
- Empty collapsed Sections do not leave focus traps.
- Overlays and sheets have escape paths.
- Player controls are fully operable by keyboard, D-pad, remote, and screen reader.
- Reduce-motion support removes scale and long fades.
- Captions are user-customizable and honor platform preferences where possible.
- Tables expose row/column semantics and row actions to assistive tech.

TV-specific targets:

- Minimum focused touch/focus target equivalent: 64px visual height.
- Section headings readable at 10 feet.
- Metadata secondary text must not be required to complete the main task.
- Safe-area aware on overscanned displays.

## Brand and assets

### Wordmark

Beamfall should be set as a clean two-syllable wordmark. Avoid sci-fi angular lettering and
streaming-service mimicry. The word should be readable at TV icon scale and in a GitHub README.

### Mark direction

Recommended mark: a simple falling beam or projected trapezoid meeting a screen plane. It should
work as:

- App icon
- Favicon
- Monochrome toolbar glyph
- TV launcher icon
- Loading mark

Avoid:

- Play triangle as the whole mark
- Locks or shields
- Explicit shadow/vault imagery
- Plex-like orange-on-black
- Jellyfin-like purple triangle families

### Imagery

Use real content artwork in product screens. Marketing imagery can use cinematic room/light
photography or generated beam/screen imagery, but should not obscure the product. Public examples
should use movies, TV, and family archive.

## Design governance

> **Informative — planned, except where a subsection is explicitly marked binding.** The artifact
> and conformance inventory below records intended documentation structure; it is not a work queue.

The design system should become three durable artifacts:

1. `docs/design-system.md`: product-level principles, tokens, components, terminology.
2. `docs/architecture/RENDERING-CONFORMANCE.md`: renderer conformance for Sections, hydration,
   focus, artwork, and accessibility under ADR-0014.
3. Client token packages:
   - Web: CSS variables and shadcn/ui mapping
   - Apple: BeamfallKitUI Swift tokens and Section renderers
   - Android: Compose theme and Section renderers

Conformance should cover:

- Section renderer snapshot/focus behavior
- Unknown vocabulary skip
- Empty Section collapse
- Guaranteed initial focus
- Skeleton non-focusability
- Reveal/relock focus preservation
- Artwork fallback and fixed aspect ratio
- VoiceOver/screen-reader labels for transport controls
- TV D-pad completeness
- Copy vocabulary scan for domain-clean base clients

### Cross-surface token governance (binding)

The "Client token packages" above are **derived projections of one source**, not independent
artifacts. This is the binding rule; the normative contract is
ADR-0106 and the operational owner-doc is
`beamfall-design-system/tokens/GOVERNANCE.md`.

- **Single source of truth:** `beamfall-design-system/tokens/beamfall-workbench.tokens.json`. Every
  token value on every surface — Web, Apple (kit + iOS + tvOS), Android (kit + apps) — traces back
  here. No client defines a value that does not. (The OKLCH table in §Theme system above is the human
  spec of that intent; the JSON is its machine source. Reconciling the two color encodings is the
  recorded open decision *"migrate the token source to OKLCH now, or keep deferred?"*.)
- **Export the separate repos consume:** `tokens/generated/beamfall-tokens.json`, a neutral,
  alias-resolved export. A client repo that must hand-maintain a native theme to compile asserts its
  tokens against this file in its own build.
- **A per-client drift/conformance check, not web-only.** `tokens/clients.json` enumerates **every**
  consuming surface (a surface is governed only by appearing there); `tokens/conformance.mjs` (in the
  design-system gate + pre-commit) reports each one's status: `gated` (byte/import drift fails the
  build), `conformance-tracked` (the governed theme can't be silently removed/replaced here; value
  equality runs in the client repo), `pending-wiring`, or `cross-repo-follow-up`. This closes
  expert-panel finding **P8** (governance existed for web only; Apple/Android/tvOS were ungoverned).
- **Ownership:** `beamfall-design-system` owns the values, the generator, the export, and this gate;
  `beamfall` owns the binding contract (ADR-0106 + this section); each client repo owns its own
  value-equality conformance test.

## Implementation order

> **Informative — non-binding.** This historical sequencing note does not define current work or
> override the roadmap; binding requirements are the sections explicitly identified as binding and
> their governed ADR/spec sources.

1. Lock this binding design-system spec with owner feedback.
2. Establish `docs/architecture/RENDERING-CONFORMANCE.md` before the first renderer implementation.
3. Build static design references for the four registers: Viewer, Manager, Console, player.
4. Implement web tokens and shadcn/ui theme mapping.
5. Implement the six web Section renderers with conformance tests.
6. Extract BeamfallKitUI equivalents for Apple clients.
7. Create Android Compose theme and Section renderers.
8. Add platform-specific player transport specs.
9. Add Manager and Console pattern specs as workflows become implementation work.

## Open design decisions

> **Informative — non-binding.** These unresolved questions are not v1.0 requirements. A decision
> becomes binding only through the governed mockup/spec/ADR process.

- Final app icon and wordmark.
- Whether to use a bundled web typeface or system-only stack.
- Exact TV focus scale per card size after device testing.
- Marketing site visual direction.
- Full player transport visual treatment after the libmpv and ExoPlayer spikes.
