# Beamfall cross-surface accessibility contract

Status: binding spec v1.0
Date: 2026-06-20
Realizes: ADR-0100

This is the **binding written spec** ADR-0100 §4 places in `docs/design/`. It operationalizes the
accepted contract: a named conformance target, the surface-agnostic required-behavior set, and the
cross-renderer parity ledger that turns parity into a release gate. Every surface ADR (ADR-0014
Viewer, ADR-0060/0061 Core Web UI, ADR-0023 Apple, ADR-0022/0102 Android, ADR-0096 tvOS) references
this contract; per-surface ADRs may add stricter behavior but may not fall below this floor.

It sits **above** the component-level UX detail in
[`accessibility-privacy.md`](accessibility-privacy.md) (per-component required fields, the minimum
evidence packet, and the Restrictions negative-test matrix), which remains the implementation
checklist this contract is enforced against.

## 1. Conformance target

Every Beamfall client surface — web Viewer/Manage/Console, tvOS, Android TV + mobile, iPhone, iPad
Manager, macOS — targets **WCAG 2.2 Level AA** as the floor, realized through each platform's native
accessibility API. "AA realized natively" means the WCAG success criteria are the *intent* and the
platform's idiomatic mechanism is the *implementation*:

| Platform | a11y mechanism |
|---|---|
| Web (Core Web UI, ADR-0007/0060) | semantic HTML + ARIA, resolved by the browser AT |
| Apple (iOS/iPadOS/tvOS/macOS) | UIAccessibility / VoiceOver, Dynamic Type, Reduce Motion |
| Android (mobile + TV) | Android accessibility / TalkBack, font scale, D-pad focus |

Beyond-AA enhancements (e.g. AAA contrast modes) are post-launch and never gate.

## 2. The required-behavior set (the parity contract)

A fixed, surface-agnostic checklist every interactive surface must satisfy:

1. **Reachable + labelled.** Every actionable element is reachable by the platform AT with a
   meaningful accessibility label resolved through the **ADR-0034
   locale map** — a11y labels are localized strings, never hardcoded English.
2. **Deterministic focus + guaranteed initial focus.** Focus order is logical and predictable (no
   positive tab order / focus traps); a guaranteed initial focus exists (ADR-0014 R3 focus rules,
   ADR-0096 focus memory).
3. **Text scaling + reduced motion.** Dynamic-type / text-scaling and reduced-motion are honored;
   layouts reflow rather than clip at the largest supported text size (ADR-0034 text-expansion,
   ADR-0101 state rendering).
4. **Contrast + non-color signalling.** Color is never the sole information channel, and interactive
   elements meet the AA contrast ratio against their rendered background.
5. **Captions/SDH + audio description.** Where present in the stream inventory, they are selectable
   on every playback surface (promotes the tvOS-only caption-styling parity of ADR-0096 to
   cross-surface).
6. **Reveal/privacy is never spoken.** AT must never announce a Reveal-filtered or restricted item's
   existence or title — accessibility readout is *inside* the Reveal contract, not an oracle around
   it (ADR-0004). The negative-test surfaces are enumerated in `accessibility-privacy.md`.

## 3. Per-renderer a11y parity ledger

This is the **`a11y` conformance ledger** ADR-0100 §3 calls for, mirroring the ADR-0014
[rendering-contract conformance](../architecture/RENDERING-CONFORMANCE.md) and the ADR-0017 §8 parity
ledger. Each renderer earns a per-behavior verdict (`gated` = asserted by an automated check in CI /
`spec` = specified per-component, audited / `pending` = owed) for the shared surface set
(**browse · detail · player · setup · Console**). A renderer that regresses a `gated` behavior fails
the release gate the way a vocabulary-firewall or rendering-contract regression does.

| Behavior | Web (Core Web UI) | iOS / iPadOS | tvOS | Android (TV+mobile) | macOS |
|---|---|---|---|---|---|
| 1 reachable + localized labels | **gated** — axe-core + locale-map assertions (`internal/web/app/src/areas/a11y/`) | spec (ADR-0023 a11y gate) | spec (ADR-0096) | **gated** — AndroidX accessibility checks + TalkBack labels on governed browse surfaces (`app-tv/src/androidTest/.../BrowseFocusOrderTest.kt`, `app-mobile/src/androidTest/.../MobileBrowseA11yInstrumentedTest.kt`) | spec |
| 2 deterministic + initial focus | **gated** — no positive tabindex + heading/list semantics + Reveal-dialog initial-focus; ADR-0014 R3 web column | spec (ADR-0014 R3) | spec (ADR-0096 focus memory) | **gated** — TV D-pad focus traversal/initial card focus plus heading semantics (`BrowseFocusOrderTest`, `BrowseStructureGateTest`, mobile browse a11y test) | spec |
| 3 text scaling / reduced motion | **gated** — viewport-zoom assertion + Chromium large-text reflow (`internal/web/app/e2e/a11y-browser.spec.ts`) | spec (Dynamic Type) | spec | **gated** — Android UI catalog large-text/reduce-motion variants + shell gate (`ui-catalog/manifest.json`) | spec |
| 4 contrast / non-color | **gated** — token-layer verification plus Chromium axe pass (`internal/web/app/e2e/a11y-browser.spec.ts`) | token-layer | token-layer | **gated** — Android high-contrast catalog variants + token drift/shell gates (`ui-catalog/manifest.json`, `tokenDriftGate`, `androidShellGate`) | token-layer |
| 5 captions/SDH + AD | pending (player ticket) | pending | **spec** (ADR-0096 caption styling) | pending | n-a |
| 6 Reveal never spoken | **gated** — restricted-title-absent assertions across surfaces | spec (ADR-0004) | spec | **gated** — locked/revealed catalog semantics plus TalkBack label assertions keep restricted-origin state explicit without leaking locked titles (`UiCatalogScreenshotTest`, `MobileBrowseA11yInstrumentedTest`) | spec |

**Native columns inherit this contract by reference.** The remaining `spec`/`pending` verdicts are owed work in
the **apple / tvOS** client repos; Android row 5 remains the separate captions/SDH + AD player ticket.
Each client must stand up its own per-renderer a11y conformance suite (XCUITest accessibility audit,
Espresso / `accessibility-test`, TalkBack focus checks) asserting behaviors 1-6 and flip its column
to `gated`. That is tracked as the PANEL-2 cross-repo residual.

## 4. Tooling per platform

- **Web (gated in `make gate`):** `axe-core` over rendered Viewer/Manage/Console surfaces in the
  vitest + jsdom harness (`internal/web/app/src/areas/a11y/cross-surface.a11y.test.tsx`,
  helper `src/test/axe.ts`), plus explicit assertions for localized labelling (ADR-0034),
  deterministic focus, viewport zoom, and the Reveal-readout rule.
  - **Browser arm:** `internal/web/app/e2e/a11y-browser.spec.ts` injects the already-installed
    `axe-core` into Chromium and runs the live Viewer/Manage/Console surfaces, then applies the
    large-text style used by the existing Playwright helper and asserts no horizontal document
    overflow.
- **Apple:** XCUITest accessibility audit (`performAccessibilityAudit`) + VoiceOver focus-order
  assertions on the shared surface set.
- **Android:** Espresso `accessibility-test` / `AccessibilityChecks` + TalkBack focus assertions.

## 5. Design-change coordination

This spec binds **existing** design — it references the `beamfall-design-system` focus-ring and
contrast tokens as the source of truth and asserts surfaces meet AA against them; it introduces no
new visual tokens. A token that is found to fail AA is a `beamfall-design-system` change (mockup +
token edit there), surfaced as a follow-up — never silently changed from this contract.
