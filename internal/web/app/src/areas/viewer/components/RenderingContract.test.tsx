import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Layout, Section, SourceItem } from "@/lib/graphql/types";
import { fetchHomeLibraries } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { prepareRenderFocusRestore, resetRenderFocusForTests } from "../lib/renderFocus";
import { fetchSectionItems } from "../lib/sections";
import { LayoutView } from "./LayoutView";

vi.mock("../lib/sections", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/sections")>();
  return { ...actual, fetchSectionItems: vi.fn() };
});

vi.mock("@/lib/session", () => ({
  useSession: () => ({ manifest: null, isManager: false }),
  useViewerIdentity: () => null,
}));

vi.mock("@/lib/graphql/viewer", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql/viewer")>()),
  fetchHomeLibraries: vi.fn(),
}));

function section(id: string): Section {
  return {
    id,
    kind: "grid",
    title: { en: id },
    artworkSlot: "poster",
    source: { kind: "recent" } as Section["source"],
  };
}

function item(id: string): SourceItem {
  return { id, kind: "media", title: id, artwork: { poster: "" } };
}

function renderLayout(layout: Layout, home = false) {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <LayoutView layout={layout} libraryID={home ? "" : "lib"} home={home} navigationID="home" />
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(fetchSectionItems).mockReset();
  vi.mocked(fetchHomeLibraries).mockReset();
  resetRenderFocusForTests();
  vi.restoreAllMocks();
});

describe("React Viewer rendering contract", () => {
  it("shows the reveal-safe aggregate empty only after every Section hydrates empty", async () => {
    let resolveFirst!: (value: SourceItem[]) => void;
    let resolveSecond!: (value: SourceItem[]) => void;
    vi.mocked(fetchSectionItems)
      .mockReturnValueOnce(new Promise((resolve) => (resolveFirst = resolve)))
      .mockReturnValueOnce(new Promise((resolve) => (resolveSecond = resolve)));

    renderLayout({ id: "layout", sections: [section("one"), section("two")] });
    expect(screen.queryByText(/nothing to show/i)).toBeNull();

    await act(async () => resolveFirst([]));
    expect(screen.queryByText(/nothing to show/i)).toBeNull();

    await act(async () => resolveSecond([]));
    expect(screen.getByText(/nothing to show in this library yet/i)).toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/restricted|hidden|permission|count/i);
  });

  it("shows a retryable load failure, not the empty library, when every Section fails", async () => {
    vi.mocked(fetchSectionItems).mockRejectedValue(new Error("offline"));

    renderLayout({ id: "layout", sections: [section("one"), section("two")] });

    expect(await screen.findByText(/connection lost/i)).toBeInTheDocument();
    expect(await screen.findByText(/could not reach the server/i)).toBeInTheDocument();
    expect(screen.queryByText(/nothing to show in this library yet/i)).toBeNull();

    vi.mocked(fetchSectionItems).mockResolvedValue([item("recovered")]);
    await act(async () => screen.getByRole("button", { name: /try again/i }).click());

    expect(await screen.findAllByRole("link", { name: "recovered" })).toHaveLength(2);
    expect(screen.queryByText(/could not reach the server/i)).toBeNull();
    expect(screen.queryByText(/connection lost/i)).toBeNull();
  });

  it("focuses the first item of the first non-empty Section", async () => {
    vi.mocked(fetchSectionItems)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([item("first-visible"), item("second-visible")]);

    renderLayout({ id: "layout", sections: [section("empty"), section("visible")] });

    await waitFor(() => expect(screen.getByRole("link", { name: "first-visible" })).toHaveFocus());
  });

  it("restores a stable focus anchor after reload and falls back when it disappears", async () => {
    let nextItems = [item("first"), item("remembered")];
    vi.mocked(fetchSectionItems).mockImplementation(async () => nextItems);
    const view = renderLayout({ id: "layout", sections: [section("cards")] });

    const remembered = await screen.findByRole("link", { name: "remembered" });
    remembered.focus();
    prepareRenderFocusRestore();
    view.rerender(
      <I18nProvider>
        <MemoryRouter>
          <LayoutView
            layout={{ id: "layout", sections: [section("cards")] }}
            libraryID="lib"
            navigationID="home"
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    await waitFor(() => expect(screen.getByRole("link", { name: "remembered" })).toHaveFocus(), {
      timeout: 5000,
    });

    screen.getByRole("link", { name: "remembered" }).focus();
    prepareRenderFocusRestore();
    nextItems = [item("first")];
    view.rerender(
      <I18nProvider>
        <MemoryRouter>
          <LayoutView
            layout={{ id: "layout", sections: [section("cards")] }}
            libraryID="lib"
            navigationID="home"
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    await waitFor(() => expect(screen.getByRole("link", { name: "first" })).toHaveFocus(), {
      timeout: 5000,
    });
  });

  it("caps Sections and emits a reveal-neutral diagnostic", () => {
    vi.mocked(fetchSectionItems).mockReturnValue(new Promise(() => {}));
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const sections = Array.from({ length: 25 }, (_, index) => section(`private-${index}`));

    renderLayout({ id: "layout", sections });

    expect(warn).toHaveBeenCalledWith("Beamfall rendering diagnostic: SectionLimitExceeded");
    expect(warn.mock.calls.flat().join(" ")).not.toContain("private-");
    expect(fetchSectionItems).toHaveBeenCalledTimes(24);
  });

  it("drops unknown Section kinds and names each one once (CORE-0203)", () => {
    vi.mocked(fetchSectionItems).mockReturnValue(new Promise(() => {}));
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const sections: Section[] = [
      section("known"),
      { ...section("private-a"), kind: "carousel" },
      { ...section("private-b"), kind: "carousel" },
      { ...section("private-c"), kind: "timeline" },
    ];

    renderLayout({ id: "layout", sections });

    expect(fetchSectionItems).toHaveBeenCalledTimes(1);
    expect(warn.mock.calls).toEqual([
      ["Beamfall rendering diagnostic: UnknownSectionKind carousel"],
      ["Beamfall rendering diagnostic: UnknownSectionKind timeline"],
    ]);
    expect(warn.mock.calls.flat().join(" ")).not.toContain("private-");
  });

  it("uses the full Section budget for the manifest while Home feeds are pending", () => {
    vi.mocked(fetchHomeLibraries).mockReturnValue(new Promise(() => {}));
    vi.mocked(fetchSectionItems).mockReturnValue(new Promise(() => {}));
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const recent: Section = {
      ...section("recent"),
      kind: "shelf",
      source: { kind: "recently-added" },
    };
    const sections = [recent, ...Array.from({ length: 23 }, (_, i) => section(`private-${i}`))];

    const { container } = renderLayout({ id: "layout-home", sections }, true);

    expect(container.querySelectorAll(".bf-section")).toHaveLength(24);
    expect(container.querySelector(".bf-hero")).toBeNull();
    expect(warn).not.toHaveBeenCalled();
    expect(fetchSectionItems).toHaveBeenCalledWith(
      sections[23],
      expect.anything(),
      expect.anything(),
    );
  });
});
