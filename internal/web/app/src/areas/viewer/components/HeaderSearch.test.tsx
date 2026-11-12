import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { searchResult } from "@/lib/graphql";
import type { SearchResultItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { HeaderSearch } from "./HeaderSearch";

vi.mock("@/lib/graphql", () => ({ searchResult: vi.fn() }));
vi.mock("@/lib/session", () => ({ useViewerIdentity: () => "viewer" }));
vi.mock("../player/preload", () => ({ preloadPlayer: vi.fn() }));

const ITEMS: SearchResultItem[] = [
  {
    id: "m1",
    kind: "media",
    title: "Sintel",
    mode: "keyword",
    score: 2,
    year: 2010,
    mediaClass: "video",
    libraryTitle: "Open Films",
  },
  {
    id: "e1",
    kind: "entity",
    title: "Space",
    mode: "keyword",
    score: 1,
    entityKind: "category",
    entityKindDisplayName: { en: "Categories" },
    libraryTitle: "Open Films",
  },
];

function Where() {
  const location = useLocation();
  const queue = (location.state as { queue?: { id: string }[] } | null)?.queue;
  return (
    <span data-testid="where" data-queue={queue?.map((entry) => entry.id).join(",")}>
      {location.pathname + location.search}
    </span>
  );
}

function renderSearch() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/"]}>
        <HeaderSearch />
        <Where />
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(searchResult).mockReset();
  vi.mocked(searchResult).mockResolvedValue({
    mode: "keyword",
    items: ITEMS,
    restrictedResultsPresent: false,
  });
});

describe("HeaderSearch", () => {
  it("focuses the field on / and Ctrl-K, but not while typing elsewhere", () => {
    render(
      <I18nProvider>
        <MemoryRouter>
          <HeaderSearch />
          <input aria-label="Other" />
        </MemoryRouter>
      </I18nProvider>,
    );
    const field = screen.getByRole("combobox", { name: "Search" });
    const other = screen.getByLabelText("Other");
    other.focus();
    fireEvent.keyDown(other, { key: "/" });
    expect(other).toHaveFocus();
    fireEvent.keyDown(other, { key: "k", ctrlKey: true });
    expect(field).toHaveFocus();
    field.blur();
    fireEvent.keyDown(document.body, { key: "/" });
    expect(field).toHaveFocus();
  });

  it("shows grouped rows as the user types and announces the count", async () => {
    const user = userEvent.setup();
    renderSearch();
    await user.type(screen.getByRole("combobox", { name: "Search" }), "s");
    const grid = await screen.findByRole("grid", { name: "Search results" });
    expect(searchResult).toHaveBeenLastCalledWith("s", "keyword", expect.any(AbortSignal));
    const groups = within(grid).getAllByRole("group");
    expect(groups).toHaveLength(2);
    expect(groups[0]).toHaveTextContent("Video");
    expect(groups[0]).toHaveTextContent("Sintel2010 · Open Films · Video");
    expect(groups[1]).toHaveTextContent("Categories");
    expect(groups[1]).toHaveTextContent("SpaceOpen Films · Categories");
    expect(within(groups[0]).getAllByRole("gridcell")).toHaveLength(2);
    expect(within(groups[1]).getAllByRole("gridcell")).toHaveLength(1);
    expect(screen.getByRole("status")).toHaveTextContent("2 results for “s”");
  });

  it("opens the picked row on Enter and plays it from the Play control", async () => {
    const user = userEvent.setup();
    renderSearch();
    const field = screen.getByRole("combobox", { name: "Search" });
    await user.type(field, "s");
    await screen.findByRole("grid");
    await user.keyboard("{ArrowDown}{ArrowDown}");
    expect(field.getAttribute("aria-activedescendant")).toBe(
      screen.getByText("Space").closest("[role=gridcell]")?.id,
    );
    await user.keyboard("{Enter}");
    expect(screen.getByTestId("where")).toHaveTextContent("/entity/e1");

    await user.type(field, "s");
    await screen.findByRole("grid");
    await user.keyboard("{ArrowDown}{ArrowRight}");
    expect(field.getAttribute("aria-activedescendant")).toBe(
      screen.getByRole("gridcell", { name: "Play Sintel" }).id,
    );
    await user.keyboard("{Enter}");
    expect(screen.getByTestId("where")).toHaveTextContent("/watch/m1");
  });

  it("hands the listed playable rows to the player as its queue", async () => {
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [
        { id: "m1", kind: "media", title: "Sintel", mode: "keyword", score: 2 },
        { id: "m2", kind: "media", title: "Spring", mode: "keyword", score: 1 },
      ],
      restrictedResultsPresent: false,
    });
    const user = userEvent.setup();
    renderSearch();
    await user.type(screen.getByRole("combobox", { name: "Search" }), "s");
    await user.click(await screen.findByRole("gridcell", { name: "Play Spring" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/watch/m2");
    expect(screen.getByTestId("where")).toHaveAttribute("data-queue", "m1,m2");
  });

  it("does not pick a row that answers the previous query", async () => {
    const user = userEvent.setup();
    renderSearch();
    const field = screen.getByRole("combobox", { name: "Search" });
    await user.type(field, "s");
    await screen.findByRole("grid");
    vi.mocked(searchResult).mockReturnValue(new Promise(() => {}));
    await user.type(field, "x{ArrowDown}{Enter}");
    expect(screen.getByTestId("where")).toHaveTextContent("/search?q=sx");
  });

  it("opens the full search page on Enter when no row is picked", async () => {
    const user = userEvent.setup();
    renderSearch();
    await user.type(screen.getByRole("combobox", { name: "Search" }), "big buck{Enter}");
    expect(screen.getByTestId("where")).toHaveTextContent("/search?q=big%20buck");
  });

  it("names the query when nothing matches", async () => {
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [],
      restrictedResultsPresent: false,
    });
    const user = userEvent.setup();
    renderSearch();
    await user.type(screen.getByRole("combobox", { name: "Search" }), "zzz");
    expect(await screen.findByText("Check the spelling or try fewer words.")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("No results for “zzz”");
    expect(screen.queryByRole("grid")).toBeNull();
  });
});
