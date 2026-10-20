import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchSectionItems } from "../lib/sections";
import type { SeeAllContext } from "../lib/seeall";
import { SeeAllPage } from "./SeeAllPage";

vi.mock("../lib/sections", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/sections")>();
  return { ...actual, fetchSectionItems: vi.fn() };
});
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

const section: Section = {
  id: "all",
  kind: "grid",
  title: { en: "All items" },
  artworkSlot: "poster",
  source: { kind: "recent" } as Section["source"],
};
const rows = Array.from<unknown, SourceItem>({ length: 120 }, (_, index) => ({
  id: `media-${index}`,
  kind: "media",
  title: `Item ${index}`,
  artwork: { poster: "" },
}));
const context: SeeAllContext = {
  section,
  libraryID: "lib",
  entityID: "",
  returnTo: "/",
};

afterEach(() => {
  cleanup();
  vi.mocked(fetchSectionItems).mockReset();
});

describe("See All rendering contract", () => {
  it("windows the accumulated result grid while retaining total set semantics", async () => {
    vi.mocked(fetchSectionItems)
      .mockResolvedValueOnce(rows.slice(0, 60))
      .mockResolvedValueOnce(rows.slice(60));
    const user = userEvent.setup();

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[{ pathname: "/seeall/source", state: context }]}>
          <Routes>
            <Route path="/seeall/:sourceId" element={<SeeAllPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );

    await screen.findByRole("button", { name: "Item 0" });
    await user.click(screen.getByRole("button", { name: /load more/i }));
    await waitFor(() => expect(fetchSectionItems).toHaveBeenCalledTimes(2));
    await waitFor(() => {
      const cards = document.querySelectorAll(".bf-card");
      expect(cards.length).toBeGreaterThan(0);
      expect(cards.length).toBeLessThan(120);
      expect(cards[0].closest("li")).toHaveAttribute("aria-setsize", "120");
      expect(document.querySelector(".bf-grid-spacer")).not.toBeNull();
    });
  });
});
