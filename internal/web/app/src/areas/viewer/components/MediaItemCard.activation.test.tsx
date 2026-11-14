import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { readMediaPreview } from "../lib/mediaDetail";
import { SectionView } from "./SectionView";

vi.mock("@/lib/session", () => ({ useViewerIdentity: () => "viewer-1" }));
vi.mock("../player/preload", () => ({ preloadPlayer: vi.fn() }));

// CORE-1144 activation model: a poster opens the item's detail page, a row in
// a list plays it at once, and a non-media card still opens its entity page.
const items: SourceItem[] = [
  { id: "m1", kind: "media", title: "Alpha" },
  { id: "e1", kind: "group", title: "Bundle" },
];

function section(kind: string): Section {
  return {
    id: `sec-${kind}`,
    kind,
    title: { en: kind },
    artworkSlot: "poster",
    source: { kind: "library" } as Section["source"],
  };
}

function Probe() {
  const location = useLocation();
  return (
    <>
      <p data-testid="path">{location.pathname}</p>
      <p data-testid="state">{JSON.stringify(location.state)}</p>
    </>
  );
}

function renderSection(kind: string) {
  render(
    <I18nProvider>
      <MemoryRouter>
        <Routes>
          <Route
            path="/"
            element={<SectionView section={section(kind)} items={items} navigationID="home" />}
          />
          <Route path="*" element={<Probe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(cleanup);

describe("card activation", () => {
  it.each(["shelf", "grid"])("opens the detail page from a %s poster", (kind) => {
    renderSection(kind);
    fireEvent.click(screen.getByRole("button", { name: "Alpha" }));

    expect(screen.getByTestId("path")).toHaveTextContent("/media/m1");
    const state = JSON.parse(screen.getByTestId("state").textContent || "null");
    expect(readMediaPreview(state, "viewer-1", "m1")).toEqual({
      item: items[0],
      sectionID: `sec-${kind}`,
    });
  });

  it("plays a list row at once", () => {
    renderSection("list");
    fireEvent.click(screen.getByRole("button", { name: "Alpha" }));

    expect(screen.getByTestId("path")).toHaveTextContent("/watch/m1");
    // The row hands its playable siblings to the player as the queue.
    expect(JSON.parse(screen.getByTestId("state").textContent || "null")).toMatchObject({
      title: "Alpha",
      queueIdentity: "viewer-1",
    });
  });

  it.each(["shelf", "list"])("still opens a non-media %s card's entity page", (kind) => {
    renderSection(kind);
    fireEvent.click(screen.getByRole("button", { name: "Bundle" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/entity/e1");
  });
});
