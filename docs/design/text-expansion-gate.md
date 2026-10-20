# Beamfall text-expansion layout gate

Status: binding spec v1.0
Date: 2026-06-20
Realizes: ADR-0034 Decision 8 (Amendment §A)

This is the **binding written spec** for the text-expansion layout gate ADR-0034 Decision 8
mandates. German and the Romance launch languages (`de`/`fr`/`es`/`pt`) routinely run **30–50%
longer** than English; a UI laid out to fit English can clip, lossily truncate, or overlap once
translated. Every launch client must **reflow / truncate-gracefully rather than clip or overlap** at
expanded string lengths, and prove it with a pseudo-locale snapshot pass in its release gate.

Expansion and large text-size are the **same layout-robustness property** — this spec is the
localization face of the ADR-0100 dynamic-type
behavior (`accessibility-contract.md` behavior 3) and governs the
ADR-0101 state-family copy slots
([`state-families.md`](state-families.md) §2) under expansion. v1 ships **LTR-only**; directionality
is reserved, not authored (ADR-0034 Decision 7) — this gate does not introduce RTL.

## 1. The pseudo-locale model

The gate does not wait for real translations to regress a layout. It synthesizes a
**longest-plausible** string from the English catalog — the standard pseudo-localization technique
(Xcode "Double-Length Pseudolanguage", Android `en-XA`):

- **Accent** the literal characters (`a`→`á`, `n`→`ñ`, …) to surface font / encoding / glyph-fallback
  gaps. Cosmetic only — same character count.
- **Pad** to a target length = `ceil(len × ratio)`, so the string actually occupies expanded space.
- **Bracket** with `⟦ … ⟧` so a downstream visual clip removes the trailing bracket — a detectable
  signal for the real-browser arm.
- **Preserve `{placeholders}`** (`{count}`, `{performer}`) untouched — they are interpolated at
  render time and are the bidi-isolation surface ADR-0034 Decision 7 reserves.

Two contract ratios (`internal/web/app/src/test/pseudo.ts`):

| Constant | Value | Meaning |
|---|---|---|
| `LAUNCH_EXPANSION` | 1.5 | The documented launch ceiling (German/Romance ≤ +50%). |
| `STRESS` | 2.0 | Longest-plausible pass the gate **asserts against** (headroom over launch). |

## 2. The shared surface set

The gate runs over the same surface set as the a11y contract — **browse · detail · player · setup ·
Console** — so a renderer earns one cross-surface verdict. The web reference implementation renders
one representative surface per register (Viewer `SectionView` = browse, Console `Plugins`, Manage
`TrashPage`), exercising both string resolution paths: the **keyed catalog** (`i18n.tsx`) and the
**manifest locale map** (`display.ts`, ADR-0034 Decision 1).

## 3. Pass / fail rule

A surface **passes** when, at `STRESS` expansion, every localized string:

1. **Survives in full** — the complete value is present in the accessible tree. No component-side
   slice / `maxLength`, no blanking, no hardcoded English that ignores the locale.
2. **Reflows or truncates *gracefully*** — it may wrap or visually ellipsize, but truncation must be
   **lossless to AT**: the full value stays reachable (accessible name / `title`), and the element
   never **overlaps** a neighbor or **clips** content with no recovery affordance.

A surface **fails** on lost content, a blank/placeholder label, hard pixel clipping, or overlap.

## 4. Verification — the two-arm model

Pixel clip / overlap is a *layout* property; jsdom has no layout/paint engine (same limit the a11y
gate documents for contrast). The property is therefore split into a jsdom arm that gates now and a
real-browser arm that measures pixels:

- **jsdom content + ratchet arm — gated in `make gate`** (`internal/web/app/src/areas/a11y/text-expansion.test.tsx`,
  helper `src/test/pseudo.ts`):
  - **Content survival** (§3 rule 1): renders the shared surfaces under the expanded `de`
    pseudo-locale and asserts each expanded keyed + manifest string renders in full.
  - **Clip-surface ratchet**: the app expresses every truncation/clip as a **Tailwind utility**
    (`truncate`, `line-clamp-*`, `whitespace-nowrap` + `overflow-hidden`) — a `global.css` scan would
    be vacuously green. The ratchet counts clip-prone utilities across the app source and pins them
    to a reviewed baseline; a **new** clip site fails the gate until it is confirmed graceful and the
    baseline is bumped. This is a review-forcing ratchet, **not** a layout proof.
- **real-browser overflow arm — measurement complement gated in Playwright**
  (`internal/web/app/e2e/text-expansion.spec.ts`): drives the live Viewer, Manage, and Console
  surfaces in Chromium, scans the reviewed clip-risk utilities, and fails on unrecoverable hard
  clipping. A negative fixture proves the checker fails on an expanded string clipped without an
  accessible full-value fallback. This is the arm that actually proves §3 rule 2 at the pixel level.

### Known clip surfaces (real-browser-arm checklist)

The app already ships these clip-prone utilities; the jsdom arm confirms their **content** survives
but cannot see the visual clip. The real-browser arm must verify each truncates gracefully (full
value reachable, no overlap) at `STRESS`:

| Surface | Utility | Risk |
|---|---|---|
| `components/ui/select.tsx` value slot | `line-clamp-1` | Radix Select trigger clips a long selected value to one line. |
| `components/ui/badge.tsx` | `whitespace-nowrap` + `overflow-hidden` | A long status/label badge clips rather than wraps. |
| `components/ui/table.tsx` head/cell | `whitespace-nowrap` | Wide localized headers/cells force horizontal overflow. |

## 5. Per-renderer expansion parity ledger

Mirrors the [`a11y` parity ledger](accessibility-contract.md#3-per-renderer-a11y-parity-ledger).
Each renderer earns a verdict over the shared surface set (`gated` = asserted by an automated check
in CI / `spec` = specified, audited / `pending` = owed):

| Check | Web (Core Web UI) | iOS / iPadOS | tvOS | Android (TV+mobile) | macOS |
|---|---|---|---|---|---|
| Content survives expansion (§3 rule 1) | **gated** — pseudo-locale render assertions (`internal/web/app/src/areas/a11y/text-expansion.test.tsx`) | spec (String Catalogs) | spec | spec (resource qualifiers) | spec |
| Clip-surface does not grow unreviewed | **gated** — clip-utility ratchet | spec | spec | spec | spec |
| No clip / overlap at expansion (§3 rule 2) | **gated** — real-browser overflow arm (`internal/web/app/e2e/text-expansion.spec.ts`) | spec (Double-Length pseudolanguage) | spec | spec (`en-XA` pseudolocale) | spec |

**Native columns inherit this contract by reference.** Each client repo stands up its own expansion
pass with the platform's pseudolocale (Xcode Double-Length / Accented, Android `en-XA`) over the
shared surface set and flips its column to `gated`. That is the PANEL-1 cross-repo residual.

## 6. Relationship to other contracts

- **ADR-0100 / `accessibility-contract.md` behavior 3 (dynamic type / reflow):** same layout
  robustness property; a surface that reflows for expansion reflows for large text. The real-browser
  overflow arm here and the real-browser reflow pass there should share one Playwright harness.
- **ADR-0101 / `state-families.md` §2 copy slots:** empty/error/no-access copy is localized and must
  obey this gate in its slots — long German error copy may not clip the action affordance.
- **ADR-0034 Decision 4 (no hardcoded user-facing strings):** the content-survival arm also fails a
  surface that renders English while a non-English locale is active, complementing the grep gate.
