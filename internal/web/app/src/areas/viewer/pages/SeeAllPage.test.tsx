import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { fetchHomeLibraries } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { fetchSectionItems } from "../lib/sections";
import type { HomeLibrarySeeAllState, SeeAllContext } from "../lib/seeall";
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
vi.mock("@/lib/session", () => ({
  useSession: () => ({ manifest }),
  useViewerIdentity: () => identity,
}));

const section: Section = {
  id: "all",
  kind: "grid",
  title: { en: "All items" },
  artworkSlot: "poster",
  source: { kind: "recent" } as Section["source"],
};
const recent: Section = {
  ...section,
  id: "recent",
  kind: "shelf",
  source: { kind: "recently-added" },
};
let manifest = {
  generatedAt: "t0",
  navigation: [{ id: "home", layoutID: "home", view: "layout" }],
  layouts: [{ id: "home", sections: [recent] }],
};
let identity = "revealed";
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
const scopedHome: HomeLibrarySeeAllState = { libraryID: "lib" };

function renderSeeAll(state: SeeAllContext | HomeLibrarySeeAllState) {
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
  identity = "revealed";
  manifest = { ...manifest, generatedAt: "t0" };
  vi.mocked(fetchHomeLibraries).mockReset();
  vi.mocked(fetchSectionItems).mockReset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
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

  it("keeps mixed artwork rows in view when scrolling beyond the first page", async () => {
    const columns = 4;
    const gap = 20;
    const viewport = 240;
    const rowHeights = [...Array(4).fill(240), ...Array(4).fill(145), ...Array(12).fill(80)];
    const offsets = [0];
    for (const height of rowHeights) offsets.push((offsets.at(-1) ?? 0) + height + gap);
    const art = rows.slice(0, rowHeights.length * columns).map((item, index) => ({
      ...item,
      artwork: {
        poster: `/assets/${index < 16 ? "portrait" : index < 32 ? "square" : "landscape"}.png`,
      },
    }));
    vi.stubGlobal("innerHeight", viewport);
    let scroll = 0;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
      this: HTMLElement,
    ) {
      if (this.classList.contains("bf-grid")) return { top: -scroll } as DOMRect;
      const index = Number(this.getAttribute("aria-posinset")) - 1;
      return { height: rowHeights[Math.floor(index / columns)] ?? 0 } as DOMRect;
    });
    vi.mocked(fetchSectionItems)
      .mockResolvedValueOnce(art.slice(0, 60))
      .mockResolvedValueOnce(art.slice(60));
    const user = userEvent.setup();
    renderSeeAll({ ...context, libraryID: "", home: true });
    await screen.findByRole("link", { name: "Item 0" });
    const grid = screen.getByRole("list");
    grid.style.gridTemplateColumns = "150px ".repeat(columns).trim();
    grid.style.rowGap = `${gap}px`;
    await user.click(screen.getByRole("button", { name: "Load more" }));

    // Scroll as a viewer would, measuring each row before it becomes a spacer.
    for (const row of [3, 6, 9, 12, 15, 17]) {
      scroll = offsets[row] + 5;
      fireEvent.scroll(window);
      const firstRendered = (row - 2) * columns;
      await waitFor(() => {
        const cards = screen.getAllByRole("listitem");
        expect(cards[0]).toHaveAttribute("aria-posinset", String(firstRendered + 1));
        expect(grid.firstElementChild).toHaveStyle({ height: `${offsets[row - 2] - gap}px` });
        let lastVisible = row;
        while (offsets[lastVisible + 1] < scroll + viewport) lastVisible += 1;
        expect(cards.at(-1)).toHaveAttribute("aria-setsize", String(art.length));
        expect(Number(cards.at(-1)?.getAttribute("aria-posinset"))).toBeGreaterThanOrEqual(
          (lastVisible + 1) * columns,
        );
      });
    }

    scroll = 0;
    fireEvent.scroll(window);
    await waitFor(() => {
      expect(screen.getAllByRole("listitem")[0]).toHaveAttribute("aria-posinset", "1");
      const renderedCount = screen.getAllByRole("listitem").length;
      expect(grid.lastElementChild).toHaveStyle({
        height: `${offsets[rowHeights.length] - offsets[renderedCount / columns] - gap}px`,
      });
    });
  }, 15_000);

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

  it.each([
    ["continue-watching", "Continue Watching"],
    ["watchlist", "Watchlist"],
  ])("opens the preserved %s source for an ordinary Home shelf", async (kind, title) => {
    const picked = { ...section, id: kind, title, source: { kind } };
    vi.mocked(fetchSectionItems).mockResolvedValueOnce(rows.slice(0, 1));
    renderSeeAll({ ...context, section: picked, home: true });

    await screen.findByRole("link", { name: "Item 0" });
    expect(screen.getByRole("heading", { name: title })).toBeVisible();
    expect(fetchSectionItems).toHaveBeenCalledWith(
      picked,
      {
        libraryID: "lib",
        entityID: "",
        home: true,
        paging: { limit: 60, offset: 0 },
      },
      undefined,
    );
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
    expect(screen.getByRole("heading", { name: "Library" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load more" })).toBeNull());
    act(() => notifyEngagementChanged());
    await screen.findByRole("link", { name: "Item 1" });
    expect(screen.queryByRole("link", { name: "Item 0" })).toBeNull();
    expect(fetchHomeLibraries).toHaveBeenCalledTimes(4);
    expect(vi.mocked(fetchSectionItems).mock.calls.every(([section]) => section === recent)).toBe(
      true,
    );
    expect(vi.mocked(fetchSectionItems).mock.calls.map(([, query]) => query)).toEqual(
      [0, 60, 0, 60].map((offset) => ({
        libraryID: "lib",
        entityID: "",
        home: true,
        paging: { limit: 60, offset },
      })),
    );
  }, 15_000);

  it("reads the first page again when the manifest refreshes", async () => {
    vi.mocked(fetchHomeLibraries).mockResolvedValue([{ id: "lib", title: "Library" }]);
    vi.mocked(fetchSectionItems)
      .mockResolvedValueOnce(rows.slice(0, 60))
      .mockResolvedValueOnce(rows.slice(60, 61))
      .mockResolvedValueOnce(rows.slice(1, 61));
    const user = userEvent.setup();
    const view = renderSeeAll(scopedHome);
    await screen.findByRole("link", { name: "Item 0" });
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(fetchSectionItems).toHaveBeenCalledTimes(2));

    manifest = { ...manifest, generatedAt: "t1" };
    view.rerender(
      <I18nProvider>
        <MemoryRouter initialEntries={[{ pathname: "/seeall/source", state: scopedHome }]}>
          <Routes>
            <Route path="/seeall/:sourceId" element={<SeeAllPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
    await screen.findByRole("link", { name: "Item 1" });
    expect(screen.queryByRole("link", { name: "Item 0" })).toBeNull();
    expect(vi.mocked(fetchSectionItems).mock.calls[2]?.[1]).toMatchObject({
      paging: { limit: 60, offset: 0 },
    });
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
    expect(screen.queryByRole("heading")).toBeNull();
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
    expect(screen.queryByRole("heading", { name: "Library" })).toBeNull();
    expect(fetchSectionItems).toHaveBeenCalledTimes(1);
  });

  it("discards a legacy history title when returning to See All after relock", async () => {
    vi.mocked(fetchHomeLibraries)
      .mockResolvedValueOnce([{ id: "lib", title: "Current library title" }])
      .mockResolvedValueOnce([]);
    vi.mocked(fetchSectionItems).mockResolvedValueOnce(rows.slice(0, 1));
    const user = userEvent.setup();
    const state = {
      ...context,
      home: true,
      section: { ...recent, id: "recent:lib", title: "Old library title" },
    };

    function Home() {
      const navigate = useNavigate();
      return <button onClick={() => navigate(-1)}>Browser back</button>;
    }

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[{ pathname: "/seeall/recent%3Alib", state }]}>
          <Routes>
            <Route path="/" element={<Home />} />
            <Route path="/seeall/:sourceId" element={<SeeAllPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(screen.queryByText("Old library title")).toBeNull();
    await screen.findByRole("heading", { name: "Current library title" });
    await screen.findByRole("link", { name: "Item 0" });
    await user.click(screen.getByRole("button", { name: "Back" }));

    identity = "relocked";
    await user.click(screen.getByRole("button", { name: "Browser back" }));
    await screen.findByRole("status");
    expect(screen.queryByRole("heading")).toBeNull();
    expect(screen.queryByRole("link", { name: "Item 0" })).toBeNull();
    expect(fetchHomeLibraries).toHaveBeenCalledTimes(2);
    expect(fetchSectionItems).toHaveBeenCalledTimes(1);
  });

  it("mounts only the on-screen rows of a long non-Home See All grid on a tall viewport", async () => {
    const many = Array.from<unknown, SourceItem>({ length: 200 }, (_, index) => ({
      id: `media-${index}`,
      kind: "media",
      title: `Item ${index}`,
      artwork: { poster: "" },
    }));
    vi.stubGlobal("innerHeight", 2500);
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
      this: HTMLElement,
    ) {
      if (this.classList.contains("bf-grid")) return { top: 0 } as DOMRect;
      return { height: 200 } as DOMRect;
    });
    vi.mocked(fetchSectionItems)
      .mockResolvedValueOnce(many.slice(0, 60))
      .mockResolvedValueOnce(many.slice(60, 120))
      .mockResolvedValueOnce(many.slice(120, 180))
      .mockResolvedValueOnce(many.slice(180, 200));
    const user = userEvent.setup();
    renderSeeAll(context);

    await screen.findByRole("link", { name: "Item 0" });
    const grid = screen.getByRole("list");
    grid.style.gridTemplateColumns = "110px ".repeat(12).trim();
    grid.style.rowGap = "20px";
    for (let page = 0; page < 3; page += 1) {
      await user.click(screen.getByRole("button", { name: "Load more" }));
    }
    await waitFor(() => expect(fetchSectionItems).toHaveBeenCalledTimes(4));
    fireEvent.resize(window);

    await waitFor(() => {
      const cards = document.querySelectorAll(".bf-card");
      expect(cards.length).toBe(144); // 12 on-screen rows, not all 200 cards
      expect(document.querySelector(".bf-grid-spacer")).not.toBeNull();
    });
  }, 15_000);
});
