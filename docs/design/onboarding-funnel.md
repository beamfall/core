# Beamfall onboarding-funnel coherence contract

Status: draft v0.1
Date: 2026-06-20

> A cross-surface contract sibling to ADR-0101 (state families): it homes the *seams* between
> per-station ADRs that today are nobody's job.
>
> **Mockup (design-gated, approved):**
> `../beamfall-design-system/mockups/web-app/onboarding-funnel.html` — the storyboard this spec is
> derived from. Change the mockup first for any visual change (CLAUDE.md design gate).

This document is binding. ADR-0080 (first-run setup), ADR-0081 (enrollment/pairing), ADR-0082
(profile switcher/settings), and ADR-0037 (Migration Wizard) each own their **station**; this
contract owns the **funnel** — the ordered path through them and the hand-offs between them. It
complements ADR-0101 (state families), ADR-0068 (unmatched queue), ADR-0083 (Viewer), and ADR-0061
(local management parity / update-awareness final step).

## 1. The problem this owns

Each station ADR completes "green" in isolation while the operator can be stranded *between*
stations — the literal "half-migrated bad first hour" (ADR-0037 §1 names it, but only *within*
migration). No ADR owns: whether setup hands off into migration or the operator rediscovers it
later; when the operator learns enrollment exists for their phone; whether the post-setup checklist
is the spine or a dead-end. The risk is **setup completes green while the operator never reaches
first value**.

## 2. The funnel spine (ordered flow)

```
Setup (0080) → Owner profile (0080) → [Seam A] Migrate (0037) → First scan (0080 §scan)
            → [Seam B] Post-setup checklist → First successful browse (0083)
```

- The funnel is a single product experience with one definition of done; stations are stages of it,
  not isolated wizards.
- **Definition of done — first value, not "setup green."** The funnel is complete when the operator
  reaches a **first successful browse with content**. "Setup wizard finished" is a *milestone*, not
  completion. Any state where setup is green but the library is unbrowsable is an **incomplete
  funnel**, not a finished one, and MUST surface the next action (scan / migrate) — never an inert
  green end-screen.

| Station | Owner ADR | Funnel responsibility added here |
|---|---|---|
| Unlock + admin + first library + owner profile + plugins | ADR-0080 | Setup is a stage, and its final step MUST present Seam A (not end the flow). |
| Migrate from another server | ADR-0037 | Reached via Seam A; lifecycle (discover/dry-run/abandon/resume/recover) per §3–§4. |
| First scan | ADR-0080 §scan | The funnel guarantees a scan is started or explicitly queued before "done"; scan-in-progress browse is Loading→activation (§6). |
| Post-setup checklist | this spec / ADR-0080 | Is the funnel **spine** (§5), not a dead-end list. |
| Enroll a device/browser | ADR-0081 | The funnel introduces it as a checklist step (operator learns pairing exists). |
| Create household/managed profiles | ADR-0082 | Introduced as a checklist step. |
| First browse | ADR-0083 | Empty-as-activation until content exists (§6). |

## 3. Seam A — the setup→Migration-Wizard handoff

This is the contract's core. It governs the boundary, not the importer mechanics (ADR-0037 owns
those).

### 3.1 Discovery — offered inline, never rediscovered
- Migration MUST be **offered inline as the final step of first-run setup** (after plugins activate,
  before/with first scan), framed around switching servers ("Bring your library with you").
- The offer lists what the *active plugins* declare (Plex/Emby/Jellyfin, OPML/Overcast/AntennaPod,
  Last.fm CSV, …) — Core never hardcodes a source name (ADR-0037 §2.1).
- The deep `/web/console/migrate` entry point still exists for later runs, but a first-run operator
  MUST NOT have to discover it to migrate. "Refugee never finds the wizard" is a defect of this seam.

### 3.2 Skip is first-class and stays findable
- The operator MAY skip migration at the offer. Skipping MUST NOT remove the path:
  - it pins a **persistent post-setup checklist item** ("Migrate from another server"), and
  - it makes the **owner empty-state CTA** offer migration alongside scan (§6).
- A skipped migration is "not yet done," never "declined and gone."

### 3.3 Trust communication
ADR-0037 §2.4 guarantees additive-only **at the data layer**; this seam owns the **copy that
communicates it** so a mid-evaluation refugee believes it. Before any write, the UI MUST state, in
product copy reviewed per ADR-0082/0034:
- Import is **additive-only** — it fills gaps and **never overwrites or downgrades** existing
  Beamfall progress.
- Import writes **watch-state, ratings, collections only** — never metadata, files, or settings
  (Beamfall re-scrapes its own).
- **No ongoing connection** to the source server is kept; the source server is **never modified**.
- Import is **re-runnable**: keep watching on the old server, re-import the delta on switch day.
- Every importer is **report-first**: a dry-run preview (source, categories, estimated records,
  match strategy, match rate, unmatched count, affected Profile/Library scope) is shown **before**
  the first write (ADR-0037 §2.2). Match reports are privacy-filtered (no source paths / restricted
  Library names) per ADR-0037 §2.3.

## 4. Migration lifecycle across the seam — abandonment, resume, partial recovery

- **Abandonment.** A long import is cancellable (ADR-0037 §2.4a). On cancel, tab close, browser
  reload, or server restart mid-import, the funnel MUST record a **`partial` migration state** with
  the import run id and match cursor (built on the ADR-0013 resumable-job substrate). Abandonment
  MUST NOT leave an ambiguous "did it work?" state.
- **Resume on return.** When a `partial` state exists, the operator MUST be shown, on next Console
  visit, a **resume affordance** in the checklist (and as an activation banner) reporting
  progress ("1,847 of 1,912 items imported") with three actions: **Resume**, **Re-run from start**,
  **Discard & finish later**. Never a silent dead-end.
- **Partial recovery is safe by construction.** Because re-run is additive-only and idempotent via
  provenance (ADR-0037 §2.4), Resume and Re-run can never corrupt or duplicate existing state; they
  only fill gaps. The unmatched remainder routes to the Manager's **Unmatched Queue** (ADR-0068) —
  nothing is lost, and a later re-run picks up matches as the scan fills metadata in. The funnel
  owns *communicating* this safety; it adds no new import semantics.
- **No new mutation path.** This seam introduces no data mutation beyond ADR-0037's; it is a
  state-and-communication contract over the existing job + importer machinery.

## 5. Seam B — the post-setup checklist IS the funnel spine

- The post-setup checklist MUST be the funnel spine, not a dead-end list. Each item **deep-links to
  its owning station** and shows completion state:
  - Finish migration → ADR-0037 (shows progress / unmatched count when `partial`).
  - Start your first scan → ADR-0080 §scan.
  - Add a device or browser → ADR-0081 (this is where the operator learns pairing exists).
  - Create profiles → ADR-0082.
- The checklist **persists** across reloads/restarts until the operator reaches first value or
  explicitly dismisses it; it survives a cancelled import (it carries the resume affordance of §4).
- The checklist MUST distinguish **done** (✓), **in-progress / resumable** (with the next action),
  and **not-started** items, so the operator always knows the single next step toward first value.

## 6. Empty-as-activation

ADR-0101 names the four state families and the load-bearing rule that **no-access ≡ empty/absent**.
This contract decides **which Empty instances are activation surfaces** vs which must stay inert.

- **Activation-empty (MUST guide forward):** the owner's pre-first-scan library, the post-setup
  home, and any empty **owner-controlled** library/section. These MUST render an Empty state with a
  headline + an action (Start first scan / Migrate) — the ADR-0101 Empty family's "action where one
  exists" slot. A fresh install's empty browse MUST NOT sit inert.
- **Privacy-empty / no-access (MUST stay inert + Reveal-safe):** any Reveal-filtered or managed-
  profile no-access surface. These MUST remain **byte-indistinguishable from genuine-empty** (no
  count, no "restricted," no activation CTA that betrays hidden content) — exactly ADR-0101 §2 and
  ADR-0004. The activation treatment is **never** applied to a no-access empty.
- **Scan-in-progress = Loading→activation.** While the first scan runs, browse MUST render the
  ADR-0101 **Loading** family showing scan progress + ETA (ties the OPS-4 first-scan estimate
  surface), not an inert blank, and resolve into content without a layout jump.
- The distinction is by **whose data and what authority**, never by content: activation applies only
  where the *operator owns and controls* the empty surface; the Reveal/no-access rule always wins
  where it applies.

## 7. Verification implications

- A funnel test proves first-run setup's final step presents the migration offer (Seam A §3.1) and
  that **skipping** pins both the checklist item and the owner empty-state CTA (§3.2).
- A trust-copy test asserts the additive-only / report-first / source-untouched statements appear
  before the first import write (§3.3) and that match reports are privacy-filtered.
- A lifecycle test proves cancel / reload / restart mid-import yields a `partial` state with a
  resume affordance offering Resume / Re-run / Discard, and that Resume and Re-run are additive-only
  and idempotent (no duplicate/overwrite) — reusing the ADR-0037 re-run test set.
- A spine test proves the post-setup checklist deep-links each station, persists across reload/
  restart, and reflects per-item state (§5).
- An empty-as-activation test proves owner pre-first-scan browse renders an Empty-with-action, while
  a Reveal-filtered surface stays byte-indistinguishable from genuine-empty (extends the ADR-0101 /
  ADR-0004 no-access conformance set), and scan-in-progress renders Loading-with-progress (§6).
- A completion test proves "setup green + unbrowsable library" surfaces the next action and is not
  reported as a finished funnel (§2).

## 8. Consequences

- The first hour gains a single owner; the setup→migration seam, the "did it work?" abandonment
  gap, and the inert fresh-install empty all get a decided treatment instead of falling between ADRs.
- No new data contract or mutation path: this is sequencing, hand-off, communication, and
  state-family selection over existing ADR-0080/0037/0081/0082/0101 machinery.
- Implementation is a follow-up (PANEL-6 residual): wiring the inline offer, the persistent
  checklist/resume state, and the empty-as-activation surfaces in `internal/web/` + the web client.
- Rejected: leaving migration discoverable only at `/web/console/migrate` (the "refugee never finds
  it" failure); treating skip as a permanent decline; treating all empties as inert (ADR-0101 calls
  this out); a continuous-sync resume model (ADR-0037 already rejected continuous sync).

## 9. Cross-references

- ADR-0080 — first-run setup & bootstrap (station + Seam A final step + first-scan).
- ADR-0037 — Migration Wizard (importer/dry-run/re-run mechanics this seam communicates).
- ADR-0081 — enrollment/pairing (checklist station; where the operator learns pairing exists).
- ADR-0082 — profile switcher/settings (checklist station; product-copy review).
- ADR-0101 — state families (Empty/Loading/Error/No-access; this spec selects which Empty activates).
- ADR-0068 — unmatched queue (partial-migration remainder destination).
- OPS-4 — first-scan estimate / deferrable-work surface (Loading→activation progress).
