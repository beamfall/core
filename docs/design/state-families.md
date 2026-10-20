# Beamfall state-families board

Status: binding v1.0
Date: 2026-06-20

This is the **binding state board** that ADR-0101
delegates to `docs/design/` (CLAUDE.md design ownership). It defines the four non-content state
families every Beamfall surface renders, the content slots each carries, the canonical copy
guidance, the accessibility floor (tied to ADR-0100),
localization (the ADR-0034 locale map), and the
load-bearing **No-access ≡ Empty** indistinguishability rule from the Reveal contract
(ADR-0004).

Every surface ADR references this board instead of re-deciding non-happy-path states. New surfaces
map their non-content states onto these four families — they do not invent a fifth.

Related documents:

- ADR-0101 — the contract this board realizes.
- ADR-0014 — empty-section collapse, skeleton/hydration,
  initial focus; the rendering substrate Loading/Empty sit on.
- ADR-0080 — first-run/post-scan, where
  Empty is an **activation** surface, not inert.
- [Accessibility and privacy UX contract](accessibility-privacy.md) — the unrevealed negative-test
  matrix and allowed/forbidden Reveal language this board's No-access rule depends on.
- [Design system](../design-system.md) — tokens, copy register ("trust through state, not theater"),
  and the domain-clean rule for base chrome (empty/error copy is generic, never domain vocabulary).

## 1. The four families

Every surface renders content through exactly these non-content states. There is no fifth family;
in particular there is **no distinct "you don't have permission" state** for Reveal-filtered content
(that state *is* the existence oracle the Reveal contract exists to prevent).

| Family | Means | Must communicate | Must NOT |
|---|---|---|---|
| **Loading** | Data in flight / hydrating | A progress affordance; skeleton sized per ADR-0014 so resolve causes no layout jump | Block focus (skeletons are never focusable); spin forever — has a timeout → Error |
| **Empty** | Resolved, legitimately nothing here | Why it's empty, plus an **action** where one exists (the activation/onboarding path — ADR-0080) | Imply failure; use alarming copy |
| **Error** | A request/operation failed | What failed (in honest, non-alarming terms) + a recovery affordance (retry/back) | Expose stack/internal/filesystem detail; offer an action this Profile can't perform |
| **No-access** | Exists, but this Profile / Reveal state / Library scope may not see it | **Nothing distinguishing.** Renders identically to genuine **Empty** or is **absent** entirely | Ever reveal the item exists; differ observably from genuine-empty; become an existence oracle |

### Family selection rule

A surface picks its family by what the **server** returned for the **current Profile/Reveal scope**,
never by raw existence:

1. Request in flight, not yet resolved → **Loading**.
2. Resolved, request failed (transport/operation error) → **Error**.
3. Resolved successfully, the authorized result set is empty → **Empty**.
4. Content exists but is outside this Profile's authorized scope → the server has already filtered it
   to **absent**; the surface therefore renders the *same* path as case 3 (**Empty**) or omits the
   entry entirely. The surface never learns case 4 happened — that is the contract.

Cases 3 and 4 are **indistinguishable by construction** (§4). A surface that can branch differently
between "empty because nothing is here" and "empty because you're not allowed" has already leaked.

## 2. Required content slots

A state instance is not free-form. Each family declares slots; the slot set is fixed so conformance
is testable and copy is reviewed like any product string. Slot copy is localized (ADR-0034) and must
survive the [text-expansion gate](text-expansion-gate.md): long German/Romance copy in a `headline`
or `body` slot may not clip or overlap the `action` affordance.

| Slot | Loading | Empty | Error | No-access |
|---|---|---|---|---|
| **headline** | optional | **required** | **required** | inherits Empty's |
| **body** | optional | optional | **required** (what failed, non-alarming) | inherits Empty's |
| **action** | — | optional (required at activation surfaces, §5) | **required** (retry/back, scope-permitted only) | inherits Empty's (never a No-access-specific action) |
| **illustration / icon** | skeleton only | optional (§3 slot contract) | optional (§3) | inherits Empty's |

Rules:

- **No-access carries no slots of its own.** Whatever it renders, it renders the genuine-**Empty**
  (or absent) slot values for that surface. It must not add, remove, or reword a slot relative to
  genuine-empty, because any delta is observable.
- **Empty's `action` is the activation lever (§5).** Where a Profile can do something about the
  emptiness (first-run setup, post-scan, add a Library), the action is required and primary. Where
  there is genuinely nothing to do, `action` is omitted — but its presence/absence must be driven by
  *capability*, not by whether content is Reveal-filtered.
- **Error's `action` is scope-bounded.** Offer only recovery the current Profile can actually perform.
  An Error must never suggest an owner/admin action to a managed Profile (both useless and a
  capability oracle).
- **Loading sizes its skeleton** to the resolved layout (ADR-0014) so there is no reflow on resolve.

## 3. Illustration / icon slot contract

This board specifies the **slot**, not the canonical artwork. The canonical per-family
illustration/iconography is an ADR-0101 open item owned by the design system and PANEL-3 token
governance; until it lands, surfaces use the platform-idiomatic placeholder for the slot.

The slot, wherever filled, must:

- Be **decorative** for AT — the meaning lives in `headline`/`body` (§4), never in the image alone
  (ADR-0100 §2.4: color/imagery is never the sole information channel).
- Respect **reduced-motion** — no looping/auto-playing motion in Empty/Error illustrations; Loading
  affordances honor reduced-motion (a static or minimal indicator).
- Stay within the **"trust through state, not theater"** register (design-system §5): no vault/lock/
  spy/ominous imagery, especially for Error or anything adjacent to Reveal. Error reads as calm and
  recoverable, not as a breach.
- Be **identical between genuine-Empty and No-access** (a distinct illustration would be an oracle).

## 4. Canonical copy guidance

Copy is generic Beamfall chrome (design-system §2 domain-clean rule) — **never** domain vocabulary
(no "Scene"/"Performer"/"Studio" in a base empty/error). Plugin-supplied labels may fill a slot only
inside a plugin-declared content area, through the locale map, and may not change which *family* renders.

**Loading** — usually copy-free (skeleton). If text is shown, name what is loading neutrally
("Loading your library…"), never a fake percentage.

**Empty** — calm, specific about *why*, and oriented to the next step when one exists:

- Activation (ADR-0080 first-run / empty Library): _"No media yet. Add a Library to get started."_ +
  primary action.
- Post-scan / filtered-to-zero by the Profile's own filters: _"Nothing here right now."_ /
  _"No results."_ — and **`"No results."` is the mandated wording wherever distinguishing "none" from
  "filtered" would leak** (see Reveal language below).
- Never _"You have no access"_, _"Empty (restricted)"_, or anything that implies a hidden elsewhere.

**Error** — honest, non-alarming, recovery-oriented; says what failed at the user's altitude, not the
stack's:

- Allowed: _"Couldn't load this. Check your connection and try again."_ + Retry.
- Operator/local errors (Core Web UI, ADR-0080) may name an *authorized* local cause
  (_"Folder not found"_) but **never** leak secrets, tokens, or filesystem internals beyond the
  operator's authorization.
- Forbidden: stack traces, internal error codes as the headline, blame copy, or a retry that can't help.

**No-access** — has **no copy of its own**. It speaks exactly the genuine-Empty copy for that surface,
or nothing (absent).

### Reveal language (inherited from the accessibility-privacy contract)

Allowed unrevealed wording: _"Restricted Libraries are hidden for this Profile."_ /
_"Relocked. Restricted Libraries are hidden again."_ (only on the **explicit Reveal/Restrictions
surface**, an authenticated context) and the neutral _"No results."_ everywhere a count/empty would
otherwise leak. Forbidden anywhere: any title, count, path, source name, failed job, artwork, or
recovery action that proves restricted content exists. See
[accessibility-privacy.md](accessibility-privacy.md) §"Restrictions negative test matrix".

## 5. Empty-state-as-activation (ADR-0080)

Empty is **not** always inert. Where the Profile can act on the emptiness, Empty is a designed
activation surface and its `action` slot is **required and primary**:

- **First-run / fresh server** (ADR-0080): the empty Library is the first product moment — its Empty
  state drives setup ("Add a Library", "Run a scan"), not a dead end.
- **Post-scan / newly-added Library** before hydration completes: Loading, then either content or an
  Empty that points at the next setup step.
- **Empty Section inside a populated Layout**: per ADR-0014 this **collapses silently** (no
  placeholder hole) rather than rendering an Empty card — the Empty *family* applies at the
  Layout/library level, not per collapsed Section.

Activation actions are gated by **capability**, never by content visibility: the decision to show
"Add a Library" is "can this Profile add a Library?", computed without reference to whether any
Reveal-filtered content exists. An owner sees activation affordances; a managed Profile sees a calm
inert Empty — and that difference is a *capability* difference, identical whether or not restricted
content exists behind the scenes.

## 6. Accessibility (ties ADR-0100)

Each state instance satisfies the ADR-0100 floor (WCAG 2.2 AA realized through native AT):

- **Announced with its meaning.** On entering a state, AT announces the `headline`/`body` (e.g.
  "Loading", "No media yet"), so the state's meaning — not just its visuals — reaches a non-sighted
  user. Loading uses the platform busy/`aria-busy` (web) / accessibility-update idiom.
- **Action reachable + labelled.** Any `action` is in the focus order with a localized accessible
  label (ADR-0100 §2.1); a guaranteed initial focus exists (ADR-0014 / ADR-0100 §2.2). Error's Retry
  is focusable and operable by keyboard/remote/touch.
- **Illustration is decorative** to AT (§3); meaning never rides on the image or on color alone.
- **Reduced-motion + dynamic-type honored** — state copy reflows rather than clips at the largest
  supported text size; no looping motion.
- **Reveal is never spoken (ADR-0100 §2.6).** AT must never announce a Reveal-filtered item's
  existence or title. The No-access readout is byte-identical to genuine-Empty's readout — the
  indistinguishability rule applies to the **accessibility tree** exactly as to the visual render.

## 7. Localization (ties ADR-0034)

Every slot string resolves through the **ADR-0034 locale map**, never hardcoded:

- Keys live under a `state.*` namespace, e.g. `state.empty.library.headline`,
  `state.empty.library.action`, `state.error.load.body`, `state.error.load.action`. A11y labels are
  locale-map strings too (ADR-0100 §2.1), not separate hardcoded English.
- **English is the universal fallback**; regional variants are additional BCP-47 keys (Quebec French
  `fr-CA` is launch-blocking — see [memory: Quebec bilingual requirement]). A missing key falls back
  to English per ADR-0034, never to a blank state.
- **No-access borrows genuine-Empty's keys** — it never has a `state.noaccess.*` key, because a
  separate key (or a separate translation) would be an oracle. There is no localized "no permission"
  string for Reveal content.
- Copy must survive **text expansion / RTL** (ADR-0034 amendment): the layout reflows, the
  illustration does not crowd out an expanded headline, and `state.*` strings are bidi-isolated when
  interpolated.

## 8. The No-access ≡ Empty indistinguishability rule (load-bearing)

This is the rule the whole board exists to protect, restated as a hard invariant:

> A surface rendering **No-access** must be **byte-indistinguishable** from the same surface rendering
> a genuine **Empty** (or be **absent** entirely). This holds at the visual render, the copy, the
> illustration, the accessibility tree, item counts, timing, and any error/diagnostic output —
> at **browse, detail, search, and Source** surfaces alike (ADR-0004 completeness contract; ADR-0096
> Top Shelf privacy rule).

Consequences for implementers:

- The client must not *know* it is in No-access vs Empty: the server filters restricted content to
  absent **before** delivery (ADR-0004 §4 / ADR-0014 §1 filtering), so the client only ever sees an
  authorized result set that is empty. There is no client-side "restricted" branch to render.
- No timing, count, error, suggestion, telemetry, or AT difference may correlate with the existence
  of restricted content (the accessibility-privacy negative-test matrix enumerates the channels:
  navigation, search suggestions, source counts, "see all" counts, jobs/job errors, notifications,
  artwork fallbacks, etc.).
- **Operator/Admin access errors are a different family.** When an authenticated admin acts without an
  Admin Session (or lacks a capability in an authenticated context), that is **Error**, not
  No-access — it is a legitimate, non-oracle "you must elevate / you can't do this" in a context where
  the actor's identity is already established. No-access is strictly the Reveal-filtered *content*
  case, where betraying existence is the harm.

## 9. Conformance (verification implications)

Per ADR-0101 §"Verification implications". Per-surface conformance implementation is a **residual**
of PANEL-4 (separate from this spec), but the gates this board commits surfaces to are:

- **Four-family mapping.** A rendering-contract conformance test (extends ADR-0014's set) asserts each
  surface maps its non-content states onto exactly these four families with the required slots (§2).
- **No-access indistinguishability.** A Reveal test proves No-access is byte-indistinguishable from
  genuine-Empty/absent at browse, detail, search, and Source surfaces (§8; extends the
  ADR-0014/0004 conformance set and the accessibility-privacy negative-test matrix).
- **Loading → Error timeout.** Loading states have a defined timeout-to-Error path; resolve causes no
  layout jump (skeleton sized, §1).
- **Localized + announced.** State copy resolves through the locale map (§7) and is announced to AT
  with its meaning (§6, ADR-0100).

## Open items (inherited from ADR-0101)

- Canonical per-family illustration/iconography (design-system + PANEL-3 token governance) — this
  board specifies the §3 slot contract; the artwork itself is deferred.
- Whether Error offers a structured "report a problem" affordance given the no-telemetry posture
  (invariant #5) — likely a local log pointer, never a phone-home.
