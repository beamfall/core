import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { fetchHomeLibraries } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { fetchSectionItems } from "../lib/sections";
import type { SeeAllContext } from "../lib/seeall";
import { SeeAllPage } from "./SeeAllPage";

vi.mock("../lib/sections", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/sections")>();
  return { ...actual, fetchSectionItems: vi.fn() };
});
vi.mock("@/lib/graphql/viewer", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql/viewer")>()),
  fetchHomeLibraries: vi.fn(),
}));
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
const scopedHome = { ...context, home: true };

function renderSeeAll(state: SeeAllContext) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[{ pathname: "/seeall/source", state }]}>
        <Routes>
          <Route path="/seeall/:sourceId" element={<SeeAllPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(fetchHomeLibraries).mockReset();
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

    await screen.findByRole("link", { name: "Item 0" });
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

  it("reads the shown span again when engagement changes, from the first item", async () => {
    vi.mocked(fetchSectionItems)
      .mockResolvedValueOnce(rows.slice(0, 3))
      .mockResolvedValueOnce(rows.slice(1, 3));

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[{ pathname: "/seeall/source", state: context }]}>
          <Routes>
            <Route path="/seeall/:sourceId" element={<SeeAllPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );

    await screen.findByRole("link", { name: "Item 0" });
    act(() => notifyEngagementChanged());
    await waitFor(() => expect(screen.queryByRole("link", { name: "Item 0" })).toBeNull());
    expect(screen.getByRole("link", { name: "Item 1" })).toBeInTheDocument();
    expect(vi.mocked(fetchSectionItems).mock.calls.at(-1)?.[1]).toMatchObject({
      paging: { offset: 0 },
    });
  });

  it("reports a failed first load with a retry instead of an empty list", async () => {
    vi.mocked(fetchSectionItems)
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(rows.slice(0, 3));
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

    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load these items.");
    expect(screen.queryByRole("status")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    await screen.findByRole("link", { name: "Item 0" });
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps Libraries hidden from Home out of a See All opened from Home (CORE-2360)", async () => {
    vi.mocked(fetchSectionItems).mockResolvedValueOnce(rows.slice(0, 3));
    const fromHome: SeeAllContext = { ...context, libraryID: "", home: true };

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[{ pathname: "/seeall/source", state: fromHome }]}>
          <Routes>
            <Route path="/seeall/:sourceId" element={<SeeAllPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );

    await screen.findByRole("link", { name: "Item 0" });
    expect(vi.mocked(fetchSectionItems).mock.calls[0][1]).toMatchObject({
      libraryID: "",
      home: true,
    });
    expect(fetchHomeLibraries).not.toHaveBeenCalled();
  });

  it("pages and refreshes every loaded page of a Home library's scoped feed", async () => {
    vi.mocked(fetchHomeLibraries).mockResolvedValue([{ id: "lib", title: "Library" }]);
    vi.mocked(fetchSectionItems)
      .mockResolvedValueOnce(rows.slice(0, 60))
      .mockResolvedValueOnce(rows.slice(60, 61))
      .mockResolvedValueOnce(rows.slice(1, 61))
      .mockResolvedValueOnce(rows.slice(61, 62));
    const user = userEvent.setup();
    renderSeeAll(scopedHome);

    await screen.findByRole("link", { name: "Item 0" });
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load more" })).toBeNull());
    act(() => notifyEngagementChanged());
    await screen.findByRole("link", { name: "Item 1" });
    expect(screen.queryByRole("link", { name: "Item 0" })).toBeNull();
    expect(fetchHomeLibraries).toHaveBeenCalledTimes(4);
    expect(vi.mocked(fetchSectionItems).mock.calls.map(([, query]) => query)).toEqual(
      [0, 60, 0, 60].map((offset) => ({
        libraryID: "lib",
        entityID: "",
        home: true,
        paging: { limit: 60, offset },
      })),
    );
  });

  it.each([
    { name: "hidden", libraries: [{ id: "lib", title: "Library", showOnHome: false }] },
    { name: "missing", libraries: [{ id: "other", title: "Other library" }] },
    { name: "unreadable", libraries: null },
  ])("does not read a $name library from a Home See All route", async ({ libraries }) => {
    vi.mocked(fetchHomeLibraries).mockResolvedValue(libraries);
    renderSeeAll(scopedHome);

    await screen.findByRole("status");
    expect(fetchSectionItems).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("discards previously loaded rows if a library is hidden before the next page", async () => {
    vi.mocked(fetchHomeLibraries)
      .mockResolvedValueOnce([{ id: "lib", title: "Library", showOnHome: true }])
      .mockResolvedValueOnce([{ id: "lib", title: "Library", showOnHome: false }]);
    vi.mocked(fetchSectionItems).mockResolvedValueOnce(rows.slice(0, 60));
    const user = userEvent.setup();
    renderSeeAll(scopedHome);

    await screen.findByRole("link", { name: "Item 0" });
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await screen.findByRole("status");
    expect(screen.queryByRole("link", { name: "Item 0" })).toBeNull();
    expect(fetchSectionItems).toHaveBeenCalledTimes(1);
  });
});
