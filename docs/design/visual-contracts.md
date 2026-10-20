# Beamfall visual contracts

Status: draft v0.1  
Date: 2026-06-11

This document turns Beamfall's art direction into a release control. The generated
website graphics are a useful north star, but they are not proof that the shipped clients
or web interfaces look that good. Every interface family needs a visual contract, real
screenshots, and an explicit conformance review before marketing or store copy can use it
as product evidence.

Related documents:

- [Design system](../design-system.md) defines principles, tokens, components, and copy.
- Website and account defines public-site, beta-pricing, and
  Beamfall Account boundary states.
- [Playback route and transport](playback-route-and-transport.md) defines player route
  honesty and custom transport evidence.
- macOS Manager, Apple platforms, and
  Android platforms define native platform state boards.
- [Accessibility and privacy](accessibility-privacy.md) defines component accessibility
  requirements, Restrictions red-team cases, and destructive-operation templates.
- [Rendering conformance](../architecture/RENDERING-CONFORMANCE.md) tracks ADR-0014
  behavior such as item caps, focus, hydration, artwork buckets, and accessibility.
- ADR-0006, ADR-0007, ADR-0008, ADR-0017, ADR-0022, ADR-0023, ADR-0024, and ADR-0026
  define the interface surfaces, account boundary, beta posture, and platform release gates.

## Purpose

The visual contract exists to prevent three failures:

1. Marketing art outpacing the actual product.
2. One client shipping with a polished look while another ships as a utilitarian port.
3. Real screenshots leaking copyrighted, private, restricted, or account/server data that
   Beamfall should never expose.

The standard is not pixel parity. It is shared quality: every surface must feel native to
its platform, visibly Beamfall, and as carefully finished as the current website art
direction promises.

## Global visual invariants

- **Quiet cinematic utility.** Viewer surfaces are cinematic and poster-forward; Manager
  and Console surfaces are dense, precise, and calm. Flash belongs in artwork, focus
  states, playback, and hero compositions, not in decorative dashboard clutter.
- **Native idiom over shared chrome.** Web, tvOS, Android TV, Android mobile, iOS/iPadOS,
  and macOS share tokens, behavior, and terminology. They do not share one copied layout.
- **Fictional media in public assets.** Marketing, demos, store screenshots, docs, and
  test fixtures use fictional movies, fictional TV shows, fictional podcasts, fictional
  albums, and family-archive examples unless a licensed or user-supplied asset is being
  inspected privately.
- **No generated art as shipped-UI evidence.** Generated images may express direction.
  They cannot be labeled or treated as real client screenshots, store screenshots, or
  implementation proof.
- **Core stays domain-agnostic.** Public and base-client screens use Media, Library,
  Profile, Restrictions, Manager, Console, Collection, Tag, Marker, Sidecar Track, and
  Snapshot. Plugin labels may appear only inside plugin-declared content areas.
- **The free/open boundary is visible.** Product UI and marketing must distinguish the
  free AGPL Core Platform and self-hosted Web UI from optional proprietary clients and
  Premium client features.
- **Beta is free.** During beta, Beamfall-controlled client features are visually marked
  as active/free during beta where pricing or entitlement appears. No screen should imply
  that Core, the Web UI, or beta client access is paywalled.
- **Beamfall Account is not a cloud dashboard.** Account and `beamfall.com/app` visuals
  show entitlement, devices, payment sources, account deletion, and device-link flows.
  They never show Libraries, playback history, Media metadata, server inventory, or a
  remote-control surface.
- **Restrictions do not leak.** Screenshots must include locked, revealed, and relocked
  states, but unrevealed states must not hint at the existence, title, count, path, job
  error, artwork, sidecar, or byte endpoint for restricted content.
- **Accessibility is visible.** Focus rings, TV focus scale, keyboard focus, VoiceOver/
  TalkBack semantics, Dynamic Type, reduce-motion, and contrast states are screenshot or
  test artifacts, not post-launch audits.
- **Performance is part of the look.** A beautiful surface that flashes, shifts, janks,
  traps focus, or opens with a spinner instead of a Snapshot fails the visual contract.
- **Audio visuals are safety-bound.** Visuals must respect photosensitivity limits, HDR
  ceilings, OLED burn-in constraints, text-safe zones, reduced-motion variants, and
  one-action blackout before they can be treated as product-ready.

## Reference direction

The website graphics under `beamfall-web/assets/` are reference direction only:

- `beamfall-ecosystem-hero.png`: Beamfall should feel like a premium media ecosystem
  across TV, mobile, tablet, desktop, and web.
- `beamfall-core-visual.png`: Core should look open, self-hosted, powerful, and
  accountless without looking like bare server software.
- `beamfall-account-admin.png`: Beamfall Account should look polished and narrow:
  entitlement, devices, and account administration, not library management.

When the real clients exist, these generated assets should be replaced in high-trust
contexts by real screenshots or composites made from real screenshots. Until then, they
must remain described as visualization or concept art.

`docs/design/design-preview.html` is also a prototype reference only. It may demonstrate visual
language, tokens, and component direction, but it is not a native-client design, a website
prototype, a store screenshot source, or visual conformance evidence.

## Interface contracts

### Marketing website

Source decisions: ADR-0015, ADR-0024, ADR-0025, ADR-0026, ADR-0031, and
[Design system: Store and marketing](../design-system.md#store-and-marketing), with concrete
state requirements in Website and account.

Visual promise:

- Cinematic first impression, with the product ecosystem visible in the first viewport.
- Colorful fictional media; no drab poster wall and no recognizable copyrighted titles.
- The product is the spectacle. Avoid generic SaaS cards, abstract blobs, and stock
  dashboard illustrations.
- Core/Web UI free and open source must be legible before pricing detail.
- Hosted Relay is absent from launch sales copy except as a future roadmap item if shown
  in a low-prominence roadmap context.

Required screenshot states:

- Desktop and mobile home page.
- Open Source Core page.
- Core vs Premium experience page.
- Beta pricing state with post-beta/GA pricing labels.
- Try-demo entry using fictional media.
- Privacy/account boundary section.

### Self-hosted Web UI

Source decisions: ADR-0007, ADR-0014, ADR-0024, and ADR-0026.

Visual promise:

- The free Web UI is credible and complete, not a neglected fallback.
- Viewer is cinematic and responsive; Manage and Admin are work-focused with restrained
  density.
- Browser-as-device role and Admin Session state are always clear without turning the UI
  into enterprise admin software.
- The web app must demonstrate that Core is free and usable without a Beamfall Account.

Required screenshot states:

- Viewer home from a Snapshot or manifest.
- Media detail with colorful fictional artwork, versions, extras, markers, and progress.
- Player transport in direct-play, transcode, token-expired, and relocked states.
- Profile picker, PIN, locked state, revealed state, and relock transition.
- Manage unmatched queue, dedupe/merge dry run, artwork candidates, bulk edit, and trash
  restore.
- Admin first-run wizard, Profiles/access policies, device enrollment, jobs, hardware
  transcode probe, plugin config, and remote-access setup.
- Browser promoted to MANAGER and browser without MANAGER role.

### tvOS Viewer

Source decisions: ADR-0008, ADR-0010, ADR-0014, and ADR-0015, with concrete state
requirements in Apple platforms and
[Playback route and transport](playback-route-and-transport.md).

Visual promise:

- This is a flagship 10-foot client. It must look premium on a living-room screen before
  it asks anyone to pay after beta.
- Artwork is large, colorful, and stable; focus is unmistakable and never shifts layout.
- Custom transport feels as polished as a native player while exposing Beamfall-specific
  truth: direct-play, transcode, track availability, scrub thumbnails, Markers, and session state.
- Top Shelf content follows the intersection-only rule and never hints at restricted
  content.

Required screenshot states:

- 1920x1080 and 3840x2160 home with first focus target.
- Profile picker, PIN entry, Snapshot stale banner, offline banner, and relock.
- Library home with hero, shelf, grid, list, info, and filter-bar Sections.
- Detail screen with versions, editions, extras, markers, track inventory, and fallback
  artwork.
- Player chrome, scrub thumbnails, audio/subtitle menus, quality menu, transcode start,
  admission-blocked state, and relock/token-expired state.
- Top Shelf or launcher equivalent showing intersection-only content.

### Android TV Viewer

Source decisions: ADR-0017, ADR-0010, and ADR-0014, with concrete state requirements in
Android platforms and
[Playback route and transport](playback-route-and-transport.md).

Visual promise:

- Android TV is co-equal with tvOS, not a cheaper port.
- Compose for TV idioms, D-pad focus, focus restoration, launcher row behavior, and
  device-specific playback honesty should feel first-class on Shield, Google TV, and
  mainstream Amlogic-class devices.
- Platform differences may exceed tvOS where Android can do better, such as explicit
  display-mode switching and verified passthrough.

Required screenshot states:

- TV home at 1920x1080 with focused hero and focused shelf item.
- Enrollment, profile selection, biometric-unavailable/PIN fallback where relevant, and
  restricted/relocked flows.
- Playback engine/status ladder: ExoPlayer direct-play, libmpv fallback, server transcode,
  passthrough opt-in, display-mode switch status, and degraded route.
- Engage launcher row under the intersection-only rule.
- Device-quirk warning or capability profile details.

### Android mobile Viewer

Source decisions: ADR-0022, ADR-0017, ADR-0010, and ADR-0014, with concrete state
requirements in Android platforms.

Visual promise:

- A real phone/tablet media app, not a shrunken TV interface.
- Material 3 structure with fixed Beamfall color tokens; no wallpaper-derived dynamic
  color and no expressive bounce that fights the Beamfall motion system.
- Tablets use adaptive layout; stretched phone UI fails the contract.

Required screenshot states:

- Phone home, search, detail, profile switch, and player.
- Tablet navigation rail and list-detail layout.
- Server discovery, QR enrollment, manual address entry, and no-server-found recovery.
- PiP available on the ExoPlayer rung and unavailable-honest state on the libmpv rung.
- Cast flow with Stream Token/direct-play-only limitations.
- Backgrounding drops Reveal after the grace period.

### Ambient Display and Music Visuals

Source decisions: ADR-0035, ADR-0047, ADR-0033, ADR-0041, and ADR-0043.

Visual promise:

- Ambient Display feels like Beamfall optics, not a generic neon VJ pack: projector
  falloff, apertures, black glass, sparse spectral contours, dust, grain, focus, and
  disciplined black negative space.
- Dark Room is the universal safe floor. Every device and reduced-motion user can fall
  back to a calm, OLED-aware posture without losing transport control.
- Visualiser mode may be spectacular, but it is still a living-room surface: no strobe,
  no full-screen white beats, no unreadable overlays, no hidden restricted artwork, and
  no decorative motion when the OS asks for reduced motion.
- Overlay mode is subordinate to media and controls. A Visual is overlay-capable only
  when its luminance, alpha, motion, and text-safe zones keep metadata, captions, focus,
  and transport legible.
- The first catalog is curated, not exhaustive. Shipping six to eight excellent Visuals
  beats shipping sixteen overlapping presets.

Required safety states:

- Normal motion, reduced motion, and Dark Room for every Visual offered by the device.
- HDR and SDR captures with beat-heavy audio, quiet audio, and silence.
- Overlay captures with transport chrome, metadata, captions/subtitles where applicable,
  focus rings, and unavailable/relocked states.
- Restricted/relocked Profile captures proving that artwork, photo input, filenames,
  counts, and Stream Token endpoints do not leak through Visual material.
- OLED idle and bedtime captures: auto-dim, blackout, metadata hold, and long-running
  static element behavior.

Required implementation evidence:

- Frame-1 and accumulated-frame contact sheets for each Visual: frame 1 must already
  show composition, and accumulated frames must preserve black negative space.
- Per-Visual manifest review: minimum Visual Tier, inputs, overlay capability,
  reduced-motion behavior, flash/HDR risk, text-safe zones, artwork dependency, and
  default-enabled surfaces.
- Weakest-target performance captures before flagship sign-off: WebGL2, low-end Android
  TV, Apple TV HD/4K class hardware, and thermal-constrained mobile.

Photosensitivity enforcement (NGAUDIT-M-PHOTOSENSITIVITY):

- The "no strobe / no full-screen white beats" promise above is machine-enforced, not
  review-only. Each Visual's photosensitivity bounds (flash-frequency ceiling,
  luminance-delta ceiling, flash area, strobe ban) are validated at pack load and in CI
  by `ValidatePhotosensitivityManifest` (`tools/visualsync/contract/photosensitivity.go`):
  a manifest declaring bounds looser than the platform hard maxima (flash ≤ 3 Hz,
  luminance delta ≤ 0.8, flash area ≤ 0.25, strobe banned) fails the build.
- At runtime the Director's always-on flash/luminance guard and the reference
  `ClampFlashSequence` clamp *attenuate* out-of-bounds frames rather than rendering them;
  clients port the reference clamp and compare semantic traces. The validator and clamp
  together are a release gate for Flagship visuals, alongside ADR-0100's cross-surface
  accessibility gate. The gate is exercised by PEAT-style flash-analysis fixtures under
  `internal/conformance/testdata/visualsync/photosensitivity/` (compliant and over-ceiling
  manifests + frame sequences) in `internal/conformance/photosensitivity_contract_test.go`.

### iPhone Viewer

Source decisions: ADR-0023, ADR-0010, ADR-0014, and ADR-0015, with concrete state
requirements in Apple platforms and
[Playback route and transport](playback-route-and-transport.md).

Visual promise:

- Current Apple design system, iOS/iPadOS 26+, with system Liquid Glass chrome over
  Cinema Dark content. Beamfall must not paint custom opaque bars that fight the system.
- Touch player gestures follow platform muscle memory; Beam is a signature interaction,
  not a hidden power-user command.
- Offline Packs, widgets, Live Activities, Siri/Spotlight, SharePlay, and AirPlay obey the
  intersection/hidden-state rules.

Required screenshot states:

- iPhone home, search, detail, profile switch, player, and Beam target picker.
- Liquid Glass navigation/tab bars over colorful content.
- AirPlay direct-play path, PiP, lock-screen/background audio state for eligible playback,
  and libmpv limitations where applicable.
- Offline Pack list, pack detail, storage/expiry, biometric unlock, and relock key drop.
- Widget, Live Activity, Siri/Spotlight, and SharePlay offer sheets using intersection-only
  content.

### iPad Viewer and iPad Manager

Source decisions: ADR-0023, ADR-0007, ADR-0006, and ADR-0014, with concrete state
requirements in Apple platforms.

Visual promise:

- The iPad has two honest registers: immersive Viewer and serious Manager.
- Manager mode is web-Manager parity in iPad idiom: split view, pointer, keyboard, Stage
  Manager resizing, drag and drop, multi-select, audited destructive operations, and
  Admin Session.
- The role gate is visible state, not a hidden or purchase-shaped mode.

Required screenshot states:

- Viewer split/sidebar layouts across compact, regular, and Stage Manager sizes.
- Manager unmatched queue, dedupe/merge, artwork slots, Collection membership, bulk
  operations, trash restore, and field locks.
- Console access through Admin Session.
- Device Role chip for VIEWER and MANAGER.
- Drag/drop, keyboard selection, pointer hover, and destructive dry-run review.

### macOS Manager

Source decisions: ADR-0006, ADR-0007, ADR-0014, and ADR-0024, with concrete state
requirements in macOS Manager.

Visual promise:

- A premium native curation tool: precise, dense, fast, and calmer than the Viewer.
- It should justify proprietary client value without hiding anything needed to stand up a
  working household library behind Premium.
- Mac-only Companion Plugin richness may exceed web Manage, but the shared Manager
  contract and safety choreography remain the same.

Required screenshot states:

- Native Manager shell, library browse from Layouts, and entity detail.
- Unmatched queue, dedupe/merge, organize preview, bulk edit, artwork candidates, field
  locks, and source provenance.
- Trash review/restore and activity-log undo.
- Device enrollment, role state, Premium-active-during-beta state, and account-linked
  entitlement status if shown.
- Companion Plugin extension point, using inherited Beamfall tokens and controls.

### Beamfall Account and app portal

Source decisions: ADR-0024, ADR-0025, ADR-0026, and ADR-0031, with concrete state
requirements in Website and account.

Visual promise:

- Narrow, trustworthy account administration.
- Account pages know contact info, purchase history, payment sources, devices signed into
  the account, Entitlement Token state, account deletion, and device-link approvals.
- Account pages do not know or imply knowledge of local servers, Libraries, Media,
  playback, Profiles, engagement, watch history, or remote control.

Required screenshot states:

- `/login/` passwordless sign-in and passkey setup.
- `/app/` account overview with "Premium active during beta" state.
- Entitlement status, payment sources, lifetime pledge export/download path, device
  list/sign-out, and account deletion.
- `/link/` device-code approval for TV/client sign-in.
- `app.beamfall.com` redirect behavior represented in deployment notes, not as a separate
  app surface.
- Hosted Relay absent from launch account portal; future roadmap placeholder only if the
  feature later graduates from ADR-0025.

## Preview-clip card contract

Source decision: ADR-0033 §5. Source mockup:
`beamfall-design-system/mockups/preview-clip-card/index.html`.

Preview clips are a browse-card enhancement, not a new Media state or surface. They must
never add audio, hide the title/focus affordance, leak restricted content, or replace the
explicit detail/player open.

| Surface | Trigger | Rendering contract | Fallback contract |
|---|---|---|---|
| Web Viewer browse cards | Pointer hover or keyboard focus after at least 400 ms. Stop on pointer leave, blur, route change, or detail open. | Muted loop inside the existing artwork frame; card geometry, labels, and focus ring stay stable. | Reduced motion, user toggle off, missing clip, failed clip, or still-generating asset keeps the artwork frame. |
| macOS Manager/Viewer cards | Pointer hover or keyboard focus after at least 400 ms. Stop on pointer leave, blur, window deactivation, route change, or detail open. | Muted loop inside the card artwork region; Manager density and selection affordances remain readable. | Reduced motion, user toggle off, missing clip, failed clip, or still-generating asset keeps artwork and selection state. |
| tvOS Viewer cards | Focus dwell after about 1 s. Cancel when focus moves, a shelf scrolls, or detail opens. | Muted loop inside the focused artwork frame; focus scale/ring remains the primary state indicator. | Reduced motion, missing clip, failed clip, still-generating asset, or profile/restriction suppression keeps artwork. |
| Android TV Viewer cards | D-pad focus dwell after about 1 s. Cancel when focus moves, a row scrolls, or detail opens. | Muted loop inside the focused artwork frame using platform TV focus behavior; no layout shift. | Reduced motion, missing clip, failed clip, still-generating asset, or profile/restriction suppression keeps artwork. |
| iPhone, iPad, and Android mobile/tablet browse cards | No autoplay on touch browse cards. | Poster or thumbnail only until the user explicitly opens detail/player. | Reduced motion is already satisfied; missing or generating clips do not add placeholders. |

## Screenshot gates

Every interface implementation PR that changes a user-facing surface must attach a visual
contract packet:

- Surface name and source ADRs.
- Real screenshot or video capture for every required state affected by the change.
- Viewport/device matrix used for capture.
- Contract checklist result: pass, partial, missing, or not-applicable.
- Rendering conformance status for any Layout/Section/player behavior changed.
- Accessibility evidence: focus state, screen-reader label/focus order, reduce-motion,
  contrast, Dynamic Type or font scaling where applicable.
- Privacy evidence: locked, revealed, relocked, and unrevealed search/empty states where
  the surface can expose restricted content.
- Accessibility/privacy matrix result from
  [Accessibility and privacy](accessibility-privacy.md) when the surface touches controls,
  player chrome, destructive workflows, account boundaries, relay copy, or Restrictions.
- Asset provenance: fictional, generated, licensed, or private user-supplied.
- Performance evidence where the source ADR defines a gate.

Minimum capture matrix:

| Surface | Required sizes/devices |
|---|---|
| Marketing website | 1440x1000 desktop, 390x900 mobile |
| Web UI | 1440x1000 desktop, 768x1024 tablet, 390x900 mobile where supported |
| tvOS | 1920x1080 and 3840x2160 |
| Android TV | 1920x1080 on at least one mainstream Google TV/Shield-class target |
| Android mobile | 390x844 phone, 800x1280 tablet, one Pixel-4a/Galaxy-A51-class floor device |
| iPhone | iPhone 11-class floor device and one current-class iPhone |
| iPad | 1024x1366, Stage Manager resize, pointer/keyboard state |
| macOS Manager | 1440x900, 1728x1117, and one narrow split-view/side-by-side width |
| Beamfall Account | 1440x1000 desktop and 390x900 mobile |

## Marketing replacement rule

Generated graphics may launch the website as concept visualization while real clients are
not yet available. They must be replaced or paired with real screenshots before any of
these claims are made:

- "Actual app screenshot"
- "Available now on {platform}"
- App Store or Play Store screenshot submission
- Paid GA pricing page that implies shipped Premium client quality
- Review/demo pack for store submission
- Press kit images representing product UI

Real screenshots must still use fictional or licensed media and must pass the privacy and
Restrictions checks above.

## Definition of done

A surface satisfies its visual contract only when:

- Required screenshots exist for all launch-blocking states.
- The implementation follows the design-system tokens, density, typography, motion, and
  component rules for its register.
- Platform idiom is respected instead of copying another client.
- Rendering conformance is updated for any Section renderer involved.
- Fictional/asset provenance is recorded.
- Restricted, account, and Premium/beta boundaries are visually correct.
- Performance and accessibility gates from the ADRs have evidence.
- Generated art is clearly separated from real product screenshots.

Anything short of this may still be useful prototype work, but it should be labeled as
prototype, concept, or partial conformance.
