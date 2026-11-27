import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Layout, Section } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchSectionItems } from "../lib/sections";
import { LayoutView } from "./LayoutView";

const mockSession = vi.fn();
vi.mock("@/lib/session", () => ({
  useSession: () => mockSession(),
  useViewerIdentity: () => "v1",
}));
vi.mock("../lib/sections", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/sections")>();
  return { ...actual, fetchSectionItems: vi.fn() };
});

afterEach(() => {
  cleanup();
  mockSession.mockReset();
  vi.mocked(fetchSectionItems).mockReset();
});

function makeFilteredLayout(
  id: string,
  barID: string,
  shelfID: string,
  field: string,
  value: string,
): Layout {
  const filterBar: Section = {
    id: barID,
    kind: "filter-bar",
    title: { en: "Filters" },
    artworkSlot: "poster",
    source: { kind: "" } as Section["source"],
    filters: [{ field, value, label: { en: value } }],
  };
  const shelf: Section = {
    id: shelfID,
    kind: "shelf",
    title: { en: "Items" },
    artworkSlot: "poster",
    source: { kind: "recent" } as Section["source"],
  };
  return { id, sections: [filterBar, shelf] };
}

describe("LayoutView state isolation and empty-card stability", () => {
  it("clears filter selections when switching from layout A to layout B", async () => {
    vi.mocked(fetchSectionItems).mockResolvedValue([
      { id: "m1", kind: "media", title: "Alpha", artwork: { poster: "" } },
    ]);
    mockSession.mockReturnValue({ manifest: { libraries: [] }, isManager: false });
    const layoutA = makeFilteredLayout("layout-a", "bar-a", "shelf-a", "tag", "featured");
    const layoutB = makeFilteredLayout("layout-b", "bar-b", "shelf-b", "tag", "classic");
    const user = userEvent.setup();

    const view = render(
      <I18nProvider>
        <MemoryRouter>
          <LayoutView layout={layoutA} libraryID="lib-a" navigationID="node-a" />
        </MemoryRouter>
      </I18nProvider>,
    );

    const chipA = await screen.findByRole("button", { name: "featured" });
    await user.click(chipA);
    expect(chipA).toHaveAttribute("aria-pressed", "true");
    await waitFor(() =>
      expect(
        vi
          .mocked(fetchSectionItems)
          .mock.calls.some(
            ([s, opts]) => s.id === "shelf-a" && opts.extraFilters?.tag === "featured",
          ),
      ).toBe(true),
    );

    vi.mocked(fetchSectionItems).mockClear();
    view.rerender(
      <I18nProvider>
        <MemoryRouter>
          <LayoutView layout={layoutB} libraryID="lib-b" navigationID="node-b" />
        </MemoryRouter>
      </I18nProvider>,
    );

    const chipB = await screen.findByRole("button", { name: "classic" });
    expect(chipB).toHaveAttribute("aria-pressed", "false");
    await waitFor(() => expect(fetchSectionItems).toHaveBeenCalled());
    for (const [, opts] of vi.mocked(fetchSectionItems).mock.calls) {
      expect(opts.extraFilters).toBeNull();
    }
  });

  it("does not restore a selection made on node A after visiting B without selecting", async () => {
    vi.mocked(fetchSectionItems).mockResolvedValue([
      { id: "m1", kind: "media", title: "Alpha", artwork: { poster: "" } },
    ]);
    mockSession.mockReturnValue({ manifest: { libraries: [] }, isManager: false });
    const layoutA = makeFilteredLayout("layout-a", "bar-a", "shelf-a", "tag", "featured");
    const layoutB = makeFilteredLayout("layout-b", "bar-b", "shelf-b", "tag", "classic");
    const user = userEvent.setup();
    const tree = (layout: Layout, node: string) => (
      <I18nProvider>
        <MemoryRouter>
          <LayoutView layout={layout} libraryID={node} navigationID={node} />
        </MemoryRouter>
      </I18nProvider>
    );
    const view = render(tree(layoutA, "node-a"));
    const chip = await screen.findByRole("button", { name: "featured" });
    await user.click(chip);
    expect(chip).toHaveAttribute("aria-pressed", "true");
    view.rerender(tree(layoutB, "node-b"));
    await screen.findByRole("button", { name: "classic" });
    view.rerender(tree(layoutA, "node-a"));
    expect(await screen.findByRole("button", { name: "featured" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  it("keeps a selection when a refresh recreates the same layout", async () => {
    vi.mocked(fetchSectionItems).mockResolvedValue([
      { id: "m1", kind: "media", title: "Alpha", artwork: { poster: "" } },
    ]);
    mockSession.mockReturnValue({ manifest: { libraries: [] }, isManager: false });
    const user = userEvent.setup();
    const tree = (layout: Layout) => (
      <I18nProvider>
        <MemoryRouter>
          <LayoutView layout={layout} libraryID="n" navigationID="n" />
        </MemoryRouter>
      </I18nProvider>
    );
    const view = render(
      tree(makeFilteredLayout("layout-a", "bar-a", "shelf-a", "tag", "featured")),
    );
    await user.click(await screen.findByRole("button", { name: "featured" }));
    view.rerender(tree(makeFilteredLayout("layout-a", "bar-a", "shelf-a", "tag", "featured")));
    expect(await screen.findByRole("button", { name: "featured" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("keeps LibraryEmpty DOM node identity stable across re-renders", () => {
    for (const isManager of [false, true]) {
      mockSession.mockReturnValue({ manifest: { libraries: [] }, isManager });
      const emptyLayout: Layout = { id: "browse", sections: [] };
      const view = render(
        <I18nProvider>
          <MemoryRouter>
            <LayoutView layout={emptyLayout} libraryID="lib1" />
          </MemoryRouter>
        </I18nProvider>,
      );

      const firstNode = screen.getByRole("status");
      view.rerender(
        <I18nProvider>
          <MemoryRouter>
            <LayoutView layout={{ id: "browse", sections: [] }} libraryID="lib1" />
          </MemoryRouter>
        </I18nProvider>,
      );
      const secondNode = screen.getByRole("status");
      expect(secondNode).toBe(firstNode);
      view.unmount();
    }
  });

  it("does not steal focus from an external control when late hydration completes", async () => {
    let resolveItems!: (items: { id: string; kind: string; title: string }[]) => void;
    vi.mocked(fetchSectionItems).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveItems = resolve;
        }),
    );
    mockSession.mockReturnValue({ manifest: { libraries: [] }, isManager: false });
    const shelf: Section = {
      id: "recent",
      kind: "shelf",
      title: { en: "Recently Added" },
      artworkSlot: "poster",
      source: { kind: "recent" } as Section["source"],
    };
    render(
      <I18nProvider>
        <MemoryRouter>
          <input aria-label="Search" />
          <LayoutView
            layout={{ id: "browse", sections: [shelf] }}
            libraryID=""
            navigationID="home"
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    const search = screen.getByRole("textbox", { name: "Search" });
    search.focus();
    expect(search).toHaveFocus();

    resolveItems([{ id: "m1", kind: "media", title: "Sample Media" }]);
    expect(await screen.findByText("Sample Media")).toBeInTheDocument();
    expect(search).toHaveFocus();
  });

  it("still focuses the first card when focus sat on an external nav link before navigation", async () => {
    vi.mocked(fetchSectionItems).mockResolvedValue([
      { id: "m1", kind: "media", title: "Alpha", artwork: { poster: "" } },
    ]);
    mockSession.mockReturnValue({ manifest: { libraries: [] }, isManager: false });
    const shelf: Section = {
      id: "recent",
      kind: "shelf",
      title: { en: "Recently Added" },
      artworkSlot: "poster",
      source: { kind: "recent" } as Section["source"],
    };
    render(
      <I18nProvider>
        <MemoryRouter>
          <nav>
            <a href="/n/lib1">Library One</a>
          </nav>
        </MemoryRouter>
      </I18nProvider>,
    );
    const navLink = screen.getByRole("link", { name: "Library One" });
    navLink.focus();
    expect(navLink).toHaveFocus();

    render(
      <I18nProvider>
        <MemoryRouter>
          <LayoutView
            layout={{ id: "lib1", sections: [shelf] }}
            libraryID="lib1"
            navigationID="lib1"
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    const card = await screen.findByRole("link", { name: "Alpha" });
    await waitFor(() => expect(card).toHaveFocus());
  });

  it("recovers failed sections automatically when the server event stream reconnects (CORE-2161)", async () => {
    const { reportEventStreamState, resetServerConnectionForTest } = await import(
      "@/lib/serverConnection"
    );
    resetServerConnectionForTest();
    vi.mocked(fetchSectionItems).mockRejectedValue(new Error("server restarting"));
    mockSession.mockReturnValue({ manifest: { libraries: [] }, isManager: false });
    const shelf: Section = {
      id: "recent-recovery",
      kind: "shelf",
      title: { en: "Recently Added" },
      artworkSlot: "poster",
      source: { kind: "recent" } as Section["source"],
    };

    render(
      <I18nProvider>
        <MemoryRouter>
          <LayoutView layout={{ id: "lib-recovery", sections: [shelf] }} libraryID="lib-recovery" />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(await screen.findByRole("button", { name: "Try again" })).toBeVisible();

    reportEventStreamState("reconnecting");
    vi.mocked(fetchSectionItems).mockResolvedValue([
      { id: "m1", kind: "media", title: "Recovered Alpha", artwork: { poster: "" } },
    ]);
    reportEventStreamState("live");

    expect(await screen.findByRole("link", { name: "Recovered Alpha" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    resetServerConnectionForTest();
  });
});
