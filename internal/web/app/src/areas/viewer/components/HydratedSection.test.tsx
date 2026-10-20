import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchSectionItems } from "../lib/sections";
import { HydratedSection, SECTION_LOAD_TIMEOUT_MS } from "./HydratedSection";

vi.mock("../lib/sections", () => ({
  fetchSectionItems: vi.fn(),
}));

const section: Section = {
  id: "sec-recent",
  kind: "shelf",
  title: { en: "Recent" },
  artworkSlot: "poster",
  source: { kind: "recent" } as Section["source"],
};

const item: SourceItem = {
  id: "media-1",
  kind: "media",
  title: "Visible item",
  artwork: { poster: "/poster.jpg" },
};

function renderSection(onConnectionLost = vi.fn()) {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <HydratedSection
          section={section}
          libraryID="lib1"
          entityID=""
          onConnectionLost={onConnectionLost}
        />
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.mocked(fetchSectionItems).mockReset();
});

describe("HydratedSection state-family conformance", () => {
  it("renders a fixed, inert skeleton while loading, then resolves without changing the slot shape", async () => {
    vi.mocked(fetchSectionItems).mockResolvedValue([item]);
    const { container } = renderSection();

    expect(container.innerHTML).toContain('aria-hidden="true"');
    expect(container.innerHTML).toContain("aspect-[2/3]");
    expect(container.querySelectorAll(".bf-shimmer")).toHaveLength(8);

    expect(await screen.findByRole("list", { name: "Recent" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Visible item/ })).toBeVisible();
  });

  it("times out loading into the error path instead of spinning forever", () => {
    vi.useFakeTimers();
    vi.mocked(fetchSectionItems).mockReturnValue(new Promise(() => {}));
    const onConnectionLost = vi.fn();
    const { container } = renderSection(onConnectionLost);

    act(() => {
      vi.advanceTimersByTime(SECTION_LOAD_TIMEOUT_MS);
    });

    expect(onConnectionLost).toHaveBeenCalledTimes(1);
    expect(container).toBeEmptyDOMElement();
  });

  it("collapses a no-access Source result exactly like a genuine empty Source result", async () => {
    vi.mocked(fetchSectionItems).mockResolvedValue([]);
    const empty = renderSection();
    await act(async () => {});
    const genuineEmpty = empty.container.innerHTML;
    cleanup();

    vi.mocked(fetchSectionItems).mockResolvedValue([]);
    const noAccess = renderSection();
    await act(async () => {});

    expect(noAccess.container.innerHTML).toBe(genuineEmpty);
    expect(noAccess.container.textContent).not.toMatch(
      /access|restricted|hidden|permission|count/i,
    );
  });
});
