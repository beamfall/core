import "@testing-library/jest-dom/vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Plugins } from "@/areas/console/sections/Plugins";
import { TrashPage } from "@/areas/manage/pages/TrashPage";
import { RevealControls } from "@/areas/viewer/components/RevealControls";
import { SectionView } from "@/areas/viewer/components/SectionView";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { STRINGS } from "@/strings";
import { expectNoAxeViolations, expectNoPositiveTabindex } from "@/test/axe";

// ADR-0100 — cross-surface accessibility contract, the Core Web UI per-renderer
// conformance check (docs/design/accessibility-contract.md). One representative
// surface per register (Viewer / Manage / Console) is rendered and asserted
// against the required-behavior set. The native renderers (apple/android/tvOS,
// separate repos) inherit the same contract by reference and gate it with their
// own platform tooling (XCUITest a11y audit / Espresso accessibility tests).
//
// Behaviors covered automatically here:
//   1. reachable + labelled actionable elements, labels via the ADR-0034 locale map
//   2. deterministic focus order (no positive tabindex) + guaranteed initial focus
//   3. text-scaling not disabled (viewport zoom — the jsdom-testable slice of reflow)
//   6. Reveal/privacy is never spoken (AT never announces a Reveal-filtered item)
// Behavior 4 (contrast) is verified at the design-token layer + a real-browser
// pass (see src/test/axe.ts); behavior 5 (captions/AD) is a player-surface ticket.

// A single combined module mock for the two data-backed surfaces (Console
// Plugins, Manage TrashPage). The Viewer SectionView needs no graphql.
vi.mock("@/lib/graphql", () => ({
  // Console: Plugins
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
  // Manage: TrashPage
  fetchTrashList: vi
    .fn()
    .mockResolvedValue([
      { id: "media-9c08", title: "Untitled clip 9c08", trashedAt: "2026-06-14T09:12:00Z" },
    ]),
  restoreMedia: vi.fn().mockResolvedValue([]),
  purgeTrash: vi.fn().mockResolvedValue({ affected: 0 }),
  fetchActivityLog: vi.fn().mockResolvedValue([]),
}));

// RevealControls reads the Reveal contract state from the session; relocked is
// the state under test (the PIN-entry dialog path).
vi.mock("@/lib/session", () => ({
  useSession: () => ({ revealed: false, reveal: vi.fn(), relock: vi.fn() }),
}));

afterEach(() => cleanup());

// A reveal-filtered shelf: the restricted item is ABSENT from `items` because
// the server omits it when not revealed (ADR-0004 "absent, not placeholder").
const RESTRICTED_TITLE = "Restricted Secret Title";
const shelf: Section = {
  id: "sec-continue",
  kind: "shelf",
  title: { en: "Continue Watching", fr: "Reprendre la lecture" },
  artworkSlot: "poster",
  source: { kind: "recent" } as Section["source"],
};
const visibleItems: SourceItem[] = [
  { id: "media-alpha", kind: "media", title: "Alpha", artwork: { poster: "/a.jpg" } },
  { id: "media-bravo", kind: "media", title: "Bravo", artwork: { poster: "/b.jpg" } },
];

function renderViewerShelf(locale: "en" | "fr" = "en") {
  return render(
    <I18nProvider initialLocale={locale} initialDisplayLocale={locale}>
      <MemoryRouter>
        <SectionView
          section={shelf}
          items={visibleItems}
          seeAll={{ href: "/seeall", state: null }}
        />
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("ADR-0100 web a11y conformance — Viewer (browse)", () => {
  it("renders no axe violations and labels every actionable element", async () => {
    const { container } = renderViewerShelf();
    // Section heading carries real h2 semantics; the card grid is a labelled list.
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("Continue Watching");
    // Each card is a reachable, labelled button (accessible name = its title).
    expect(screen.getByRole("button", { name: /Alpha/ })).toBeVisible();
    expect(screen.getByRole("button", { name: /Bravo/ })).toBeVisible();
    await expectNoAxeViolations(container);
    expectNoPositiveTabindex(container);
  });

  it("resolves labels through the ADR-0034 locale map, not hardcoded English", () => {
    renderViewerShelf("fr");
    // Heading uses the manifest display-string's fr entry…
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("Reprendre la lecture");
    // …and the keyed control label resolves through the fr catalog.
    expect(screen.getByRole("button", { name: STRINGS.fr.seeAll })).toBeVisible();
  });

  it("guarantees an initial focus when the Reveal dialog opens (behavior 2)", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <RevealControls />
      </I18nProvider>,
    );
    await user.click(screen.getByRole("button", { name: STRINGS.en.restrictions }));
    // The dialog moves focus to a defined target (the PIN field's autoFocus) —
    // never opening focus-less or trapping it on the page behind.
    const pin = await screen.findByLabelText(STRINGS.en.pin);
    await waitFor(() => expect(pin).toHaveFocus());
  });

  it("never speaks a Reveal-filtered item (ADR-0100 behavior 6)", () => {
    const { container } = renderViewerShelf();
    // The restricted title appears nowhere in the accessible tree…
    expect(screen.queryByText(RESTRICTED_TITLE)).toBeNull();
    expect(container.textContent).not.toContain(RESTRICTED_TITLE);
    // …and the list's accessible name is exactly the Section title — no item
    // count that could betray the existence of suppressed items.
    expect(screen.getByRole("list")).toHaveAccessibleName("Continue Watching");
  });
});

describe("ADR-0100 web a11y conformance — Console (Plugins)", () => {
  it("renders no axe violations with reachable, labelled controls", async () => {
    const { container } = render(
      <I18nProvider>
        <Plugins />
      </I18nProvider>,
    );
    await screen.findAllByText("Studio Metadata");
    // A config control with a programmatic label (reachable + labelled).
    expect(screen.getByLabelText("Fetch artwork")).toBeVisible();
    await expectNoAxeViolations(container);
    expectNoPositiveTabindex(container);
  });
});

describe("ADR-0100 web a11y conformance — Manage (Trash)", () => {
  it("renders no axe violations with reachable, labelled controls", async () => {
    const { container } = render(
      <I18nProvider>
        <TrashPage />
      </I18nProvider>,
    );
    await screen.findByText("Untitled clip 9c08");
    expect(screen.getByRole("button", { name: "Restore" })).toBeVisible();
    await expectNoAxeViolations(container);
    expectNoPositiveTabindex(container);
  });
});

describe("ADR-0100 web a11y conformance — text scaling (behavior 3)", () => {
  it("does not disable browser zoom in the viewport meta", () => {
    // Reflow proper needs a layout engine (verified in the real-browser pass);
    // the jsdom-testable slice is that the served document never pins or blocks
    // user scaling — WCAG 1.4.4 Resize Text.
    const html = readFileSync(resolve("index.html"), "utf8");
    const viewport = /<meta[^>]*name=["']viewport["'][^>]*>/i.exec(html)?.[0] ?? "";
    expect(viewport).not.toMatch(/user-scalable\s*=\s*no/i);
    expect(viewport).not.toMatch(/maximum-scale\s*=\s*1(\.0)?\b/i);
  });
});
