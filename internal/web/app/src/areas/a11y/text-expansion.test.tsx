import "@testing-library/jest-dom/vitest";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

// ADR-0034 Decision 8 — text-expansion layout gate, the Core Web UI reference
// implementation (docs/design/text-expansion-gate.md). German and the Romance
// launch languages run 30–50% longer than English; this gate renders the shared
// surface set (the same Viewer/Manage/Console triple as the ADR-0100 a11y gate)
// against a *longest-plausible* pseudo-locale and fails if a localized string is
// dropped, blanked, or lossily truncated by component code.
//
// HONEST SCOPE (mirrors src/test/axe.ts color-contrast note): jsdom has NO
// layout/paint engine, so this file CANNOT measure pixel clipping or overlap —
// the actual "does it visually clip" check is the real-browser overflow arm
// (deferred; see the spec §Verification). What jsdom CAN prove, and what this
// file gates, is the two tractable slices of the same robustness property:
//   (1) Content survival — every expanded localized string renders IN FULL in
//       the accessible tree (no JS slice/maxLength, no blanking, no hardcoded
//       English that ignores the locale).
//   (2) Clip-surface ratchet — a static count of clip-prone Tailwind utilities
//       in the app source cannot rise without review (a new `truncate` /
//       `line-clamp` / `whitespace-nowrap` + `overflow-hidden` is a layout-clip
//       risk that the real-browser arm must verify truncates gracefully).
//
// The pseudo-locale is injected as `STRINGS.de` so the real `de` catalog (and
// en/fr/es/pt) is left intact for every other test. The mock uses an async
// factory (hoisted above imports) that pulls the real en table and expands it.
vi.mock("@/strings", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/strings")>();
  const { expandStrings, STRESS } = await import("@/test/pseudo");
  return {
    ...actual,
    STRINGS: { ...actual.STRINGS, de: expandStrings(actual.STRINGS.en, STRESS) },
  };
});

import { Plugins } from "@/areas/console/sections/Plugins";
import { TrashPage } from "@/areas/manage/pages/TrashPage";
import { SectionView } from "@/areas/viewer/components/SectionView";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { STRINGS } from "@/strings";
import { expandDisplayString, STRESS } from "@/test/pseudo";

// Same combined graphql mock as the a11y gate — the two data-backed surfaces
// (Console Plugins, Manage Trash) render from it; the Viewer SectionView needs
// no graphql. A config control with a `select` type exercises the Radix Select
// value slot (a known line-clamp-1 clip surface — see the spec ledger).
vi.mock("@/lib/graphql", () => ({
  fetchConsoleConfig: vi.fn().mockResolvedValue({
    backgroundWorkers: 2,
    updateCheckEnabled: false,
    updateCheckChannel: "stable",
    remoteEnrollmentEnabled: true,
    eventsRetentionDays: 180,
    plugins: [
      {
        id: "studio-meta",
        displayName: "Studio Metadata",
        active: true,
        controlPolicy: "managed",
        config: [
          {
            name: "fetchArtwork",
            type: "bool",
            label: "Fetch artwork",
            required: false,
            value: "true",
            configured: true,
          },
        ],
        permissions: [{ kind: "net.outbound", label: "Outbound network", highRisk: false }],
        status: [{ id: "endpoint", label: "Endpoint", state: "ok", detail: "Reachable." }],
        actions: [
          { id: "refresh", label: "Refresh catalog", risk: "info", requiredPermissions: [] },
        ],
        scrapers: [],
      },
    ],
  }),
  setPluginActive: vi.fn(),
  setPluginConfig: vi.fn(),
  setScraperCredential: vi.fn(),
  validateScraperCredential: vi.fn(),
  disableScraperCredential: vi.fn(),
  fetchTrashList: vi
    .fn()
    .mockResolvedValue([
      { id: "media-9c08", title: "Untitled clip 9c08", trashedAt: "2026-06-14T09:12:00Z" },
    ]),
  restoreMedia: vi.fn().mockResolvedValue([]),
  purgeTrash: vi.fn().mockResolvedValue({ affected: 0 }),
  fetchActivityLog: vi.fn().mockResolvedValue([]),
}));

vi.mock("@/lib/session", () => ({
  useSession: () => ({ revealed: false, reveal: vi.fn(), relock: vi.fn() }),
}));

afterEach(() => cleanup());

// The expanded pseudo-locale renders under `de`: I18nProvider resolves the keyed
// catalog from STRINGS.de (mocked → expanded), and displayString() resolves the
// manifest locale map from its `de` entry.
const LOCALE = "de" as const;

const shelf: Section = {
  id: "sec-continue",
  kind: "shelf",
  // Manifest display-string (ADR-0034 locale map) — expanded so the gate
  // exercises the display.ts locale-map path, not only the keyed catalog.
  title: expandDisplayString({
    en: "Continue Watching",
    fr: "Reprendre la lecture",
  }) as Section["title"],
  artworkSlot: "poster",
  source: { kind: "recent" } as Section["source"],
};
const items: SourceItem[] = [
  { id: "media-alpha", kind: "media", title: "Alpha", artwork: { poster: "/a.jpg" } },
];

function renderExpanded(ui: React.ReactElement) {
  return render(
    <I18nProvider initialLocale={LOCALE} initialDisplayLocale={LOCALE}>
      <MemoryRouter>{ui}</MemoryRouter>
    </I18nProvider>,
  );
}

describe("ADR-0034 text-expansion gate — pseudo-locale is actually applied", () => {
  it("expands the injected catalog beyond English length", () => {
    // Guards the gate itself: if the mock silently failed, every assertion
    // below would pass trivially against un-expanded English.
    expect(STRINGS.de.seeAll).not.toBe(STRINGS.en.seeAll);
    expect(STRINGS.de.seeAll.length).toBeGreaterThan(STRINGS.en.seeAll.length * (STRESS - 0.2));
  });
});

describe("ADR-0034 text-expansion gate — Viewer (browse)", () => {
  it("renders the expanded manifest title and keyed label in full", () => {
    renderExpanded(
      <SectionView section={shelf} items={items} seeAll={{ href: "/seeall", state: null }} />,
    );
    // Manifest locale-map path (display.ts) — full expanded title, not clipped.
    const expandedTitle = expandDisplayString("Continue Watching") as string;
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent(expandedTitle);
    // Keyed-catalog path (i18n.tsx) — the See-all control resolves the expanded de value in full.
    expect(screen.getByRole("button", { name: STRINGS.de.seeAll })).toBeVisible();
  });
});

describe("ADR-0034 text-expansion gate — Console (Plugins)", () => {
  it("renders expanded keyed labels in full (incl. Select-bearing surface)", async () => {
    const { container } = renderExpanded(<Plugins />);
    await screen.findAllByText("Studio Metadata");
    // The active-state chip resolves the expanded de value, present in full.
    expect(container.textContent).toContain(STRINGS.de.consoleConfigActive);
    expect(STRINGS.de.consoleConfigActive).toContain("⟧"); // not truncated to a prefix
  });
});

describe("ADR-0034 text-expansion gate — Manage (Trash)", () => {
  it("renders expanded keyed labels in full", async () => {
    const { container } = renderExpanded(<TrashPage />);
    await screen.findByText("Untitled clip 9c08");
    expect(screen.getByRole("button", { name: STRINGS.de.restore })).toBeVisible();
    expect(container.textContent).toContain(STRINGS.de.trashPolicyHeading);
  });
});

// ──────────────────────────────────────────────────────────────────────────
// Clip-surface ratchet. The app uses Tailwind utility classes (not global.css)
// for every truncation/clip; a CSS-file scan would be vacuously green. This
// counts clip-prone utilities across the app source and pins them to a recorded
// baseline. A NEW clip site (a `truncate` on a string slot in a future surface)
// raises the count and fails here — forcing a conscious check that it truncates
// *gracefully* (full value reachable by AT, never overlap) per the spec, and a
// baseline bump. This is a review-forcing ratchet, not a layout proof.
const CLIP_UTILITY =
  /(?:^|[\s"'`])(truncate|whitespace-nowrap|overflow-hidden|line-clamp-(?:\[|\d))(?![\w-])/;
// Baseline measured 2026-06-20 over internal/web/app/src (test/spec files
// excluded). Recompute after a reviewed change; do not silently raise.
// 2026-06-21 (WEB-6): +3 → 31 for the setup FolderPicker — 1 `overflow-hidden`
// on the picker container (no text slot) and 2 `truncate` on filesystem-derived
// slots (folder name + footer path). Both truncate gracefully: CSS ellipsis only,
// so the full value stays in the DOM text node and is reachable by AT.
const CLIP_BASELINE = 24;

function tsSources(dir: string, acc: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = resolve(dir, entry.name);
    if (entry.isDirectory()) {
      // `src/test/` is test scaffolding, not shipping UI — out of ratchet scope.
      if (entry.name === "test") continue;
      tsSources(full, acc);
    } else if (/\.tsx?$/.test(entry.name) && !/\.(test|spec)\.tsx?$/.test(entry.name)) {
      acc.push(full);
    }
  }
  return acc;
}

describe("ADR-0034 text-expansion gate — clip-surface ratchet", () => {
  it("does not add clip-prone Tailwind utilities beyond the reviewed baseline", () => {
    let count = 0;
    const sites: string[] = [];
    for (const file of tsSources(resolve("src"))) {
      const rel = file.slice(file.indexOf("src/"));
      for (const line of readFileSync(file, "utf8").split("\n")) {
        if (CLIP_UTILITY.test(line)) {
          count += 1;
          sites.push(rel);
        }
      }
    }
    expect(
      count,
      `clip-utility count ${count} != baseline ${CLIP_BASELINE}. New/removed clip sites:\n` +
        `${[...new Set(sites)].join("\n")}\n` +
        `If a site was added, confirm it truncates gracefully under expansion ` +
        `(full value reachable by AT, no overlap — docs/design/text-expansion-gate.md) and update CLIP_BASELINE.`,
    ).toBe(CLIP_BASELINE);
  });
});
