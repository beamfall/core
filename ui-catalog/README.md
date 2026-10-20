# UI State Catalog — `web` surface (embedded Viewer/Manage UI)

This is the per-repo UI screenshot catalog for the **embedded AGPL web UI**
(`internal/web/app`, surface `web`, ADR-0007). It conforms to the
canonical contract in `beamfall-design-system/docs/ui-catalog/SPEC.md` (the
workspace-root spec; this `beamfall/ui-catalog/` is the `web` surface's emission).

Two consumers, one artifact:

1. **Visual regression** — `baselines/<id>.png` is the committed baseline; a
   re-capture diffs against it (pixelmatch) and writes `diffs/<id>.png` on
   mismatch.
2. **LLM-driven refinement** — `manifest.json` is the entry point. For each
   entry an LLM reads the `screenshot` + compact `hierarchy` dump (visible
   header/main HTML + hidden-element text summaries + accessibility snapshot)
   + the `intent` and critiques the render *against intent*.

## Layout

```
ui-catalog/
  manifest.json        # CatalogEntry[] — the LLM entry point
  images/<id>.png      # generated current render (gitignored)
  images/<id>.txt      # compact hierarchy dump for LLM review
  baselines/<id>.png   # committed baseline for regression diffing
  diffs/<id>.png       # (generated) baseline-vs-current diff, only on mismatch
  README.md            # this file
```

`<id>` is `web.<screen>.<state>.<viewport>` (globally unique, == image stem).

## How it renders (the deterministic seam)

The capture harness lives in [`../tools/ui-catalog/`](../tools/ui-catalog/)
(isolated Node package — Playwright + pixelmatch; it does **not** touch the
React app's Vitest/Playwright suites or `make gate`). It:

1. builds and boots `beamfall serve --seed=conformance --allow-conformance-seed`
   on an ephemeral 127.0.0.1 port (the deterministic corpus world: fixed
   read-clock at Unix 0, fixed Stream-Token key — see `internal/app/app.go` and
   `cmd/beamfall/serve_conformance_test.go`),
2. for each `captured` entry, opens a fresh headless-Chromium context, injects
   the entry's **seeded session cookies** (the conformance seed plants fixed
   sessions/devices — `internal/store/sqlite.go` `developmentSeed`; e.g.
   `session-default`/`device-tv`/`dev-tv-secret` for the locked adult viewer),
   navigates to `/web/`, and drives the real app to the target state,
3. screenshots to gitignored `images/<id>.png` and dumps compact hierarchy to
   committed `images/<id>.txt`,
4. (re)writes `manifest.json`, then diffs each capture against its baseline.

Determinism: animations/transitions/caret are frozen, `prefers-reduced-motion`
is forced, focus rings are suppressed (focus-as-state belongs to the tvOS /
AndroidTV surfaces, not web), the viewport is fixed, and runtime timestamps in
the Manage view (`nextDue`/`lastEnqueued`, computed against the real scheduler
clock) are normalized to `<deterministic-time>` so renders stay byte-stable.

## Run it

```sh
make ui-catalog          # capture + diff against baselines (fails on regression)
make ui-catalog-record   # re-bless baselines from current renders (review first!)
```

Or directly:

```sh
cd tools/ui-catalog
npm install && npx playwright install chromium   # one-time
npm run capture          # capture + diff
npm run record           # re-bless baselines
npm run diff             # diff existing images against baselines (no re-capture)
```

A baseline only ever changes through `record` — never silently. When a diff is
**intended** (a deliberate UI change), inspect `diffs/`, then re-record and
commit the new baselines alongside the code change.

## What is captured vs. listed

- **`captured`** — rendered against the conformance seed through a React-aware
  driver at `/web/`.
- **`listed`** — enumerated but not rendered because the React catalog driver or
  conformance fixture does not yet reach the state. Existing generated screenshots
  for these entries are ignored; the manifest `status` is authoritative.
- **`device-only`** — needs a real GPU/decoder: a live decoded video frame (the
  conformance corpus serves single-byte placeholder media, not decodable video;
  the transport chrome over the empty video element is captured separately).

## Leak-proof entries

Reveal-locked / managed / PIN / terminal entries carry `"leakProof": true` and
an `intent` asserting the ADR-0004/0007 contract: no restricted Library title,
artwork, summary, count, or placeholder may appear anywhere in the DOM for a
session that may not see it. The harness drives these from real seeded sessions
(scope is re-derived server-side), and the committed `images/<id>.txt` dumps keep
visible HTML plus hidden-element text summaries greppable for restricted strings
without forcing every LLM review to read full hidden DOM.

## CI wiring

`make ui-catalog` is **not** part of default `make gate`: Playwright plus a
Chromium download is heavy for the default suite. It is wired as a separate
nightly/manual GitHub Actions workflow in `.github/workflows/ui-catalog-nightly.yml`.
Re-recording baselines is always a deliberate, reviewed commit.

## Known finding surfaced by this catalog

Catalog follow-ups are tracked as roadmap/test-gate work, not as local
agent-memory bug files. The catalog records the real shipped render; when an
entry is only listed, the `note` names the missing React driver or fixture.
