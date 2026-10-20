import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Layout, Section, SourceItem } from "@/lib/graphql/types";
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

function renderLayout(layout: Layout) {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <LayoutView layout={layout} libraryID="lib" navigationID="home" />
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(fetchSectionItems).mockReset();
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

  it("keeps the connection-loss diagnostic alongside an all-failed empty Layout", async () => {
    vi.mocked(fetchSectionItems).mockRejectedValue(new Error("offline"));

    renderLayout({ id: "layout", sections: [section("one")] });

    expect(await screen.findByText(/connection lost/i)).toBeInTheDocument();
    expect(screen.getByText(/nothing to show in this library yet/i)).toBeInTheDocument();
  });

  it("focuses the first item of the first non-empty Section", async () => {
    vi.mocked(fetchSectionItems)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([item("first-visible"), item("second-visible")]);

    renderLayout({ id: "layout", sections: [section("empty"), section("visible")] });

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "first-visible" })).toHaveFocus(),
    );
  });

  it("restores a stable focus anchor after reload and falls back when it disappears", async () => {
    let nextItems = [item("first"), item("remembered")];
    vi.mocked(fetchSectionItems).mockImplementation(async () => nextItems);
    const view = renderLayout({ id: "layout", sections: [section("cards")] });

    const remembered = await screen.findByRole("button", { name: "remembered" });
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
    await waitFor(() => expect(screen.getByRole("button", { name: "remembered" })).toHaveFocus(), {
      timeout: 5000,
    });

    screen.getByRole("button", { name: "remembered" }).focus();
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
    await waitFor(() => expect(screen.getByRole("button", { name: "first" })).toHaveFocus(), {
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
});
