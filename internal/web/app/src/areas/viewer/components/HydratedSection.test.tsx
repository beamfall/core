import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache, readViewerCache, writeViewerCache } from "@/lib/viewerCache";
import { fetchSectionItems } from "../lib/sections";
import { HydratedSection, SECTION_LOAD_TIMEOUT_MS, sectionCacheKey } from "./HydratedSection";

const identityMock = vi.hoisted(() => ({ identity: null as string | null }));

vi.mock("../lib/sections", () => ({
  fetchSectionItems: vi.fn(),
}));

// No identity by default: the state-family cases run uncached, as without a
// SessionProvider. The cache cases bind one explicitly.
vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => identityMock.identity,
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
  identityMock.identity = null;
  clearViewerCache();
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

describe("HydratedSection identity-scoped cache", () => {
  const identity = JSON.stringify(["viewer", "p1", "adult", false]);
  const key = sectionCacheKey(section, "lib1", "", undefined);
  const fresh: SourceItem = { ...item, id: "media-2", title: "Fresh item" };

  it("renders cached items without a skeleton, then revalidates and stores the result", async () => {
    identityMock.identity = identity;
    writeViewerCache(identity, key, [item]);
    let resolve!: (value: SourceItem[]) => void;
    vi.mocked(fetchSectionItems).mockReturnValue(new Promise((r) => (resolve = r)));

    const { container } = renderSection();

    expect(container.querySelectorAll(".bf-shimmer")).toHaveLength(0);
    expect(screen.getByRole("button", { name: /Visible item/ })).toBeVisible();
    expect(fetchSectionItems).toHaveBeenCalledTimes(1);

    await act(async () => resolve([fresh]));
    expect(screen.getByRole("button", { name: /Fresh item/ })).toBeVisible();
    expect(screen.queryByRole("button", { name: /Visible item/ })).toBeNull();
    expect(readViewerCache(identity, key)).toEqual([fresh]);
  });

  it("never renders items cached for a different identity", () => {
    writeViewerCache(identity, key, [item]);
    identityMock.identity = JSON.stringify(["viewer", "p1", "adult", true]);
    vi.mocked(fetchSectionItems).mockReturnValue(new Promise(() => {}));

    const { container } = renderSection();

    expect(container.querySelectorAll(".bf-shimmer")).toHaveLength(8);
    expect(screen.queryByRole("button", { name: /Visible item/ })).toBeNull();
  });

  it("aborts the in-flight fetch on unmount and neither renders nor caches its result", async () => {
    identityMock.identity = identity;
    let resolve!: (value: SourceItem[]) => void;
    vi.mocked(fetchSectionItems).mockReturnValue(new Promise((r) => (resolve = r)));

    const view = renderSection();
    const signal = vi.mocked(fetchSectionItems).mock.calls[0][2];
    expect(signal?.aborted).toBe(false);

    view.unmount();
    expect(signal?.aborted).toBe(true);
    await act(async () => resolve([fresh]));
    expect(readViewerCache(identity, key)).toBeUndefined();
  });

  it("keeps cached items and raises connection-lost when revalidation fails", async () => {
    identityMock.identity = identity;
    writeViewerCache(identity, key, [item]);
    vi.mocked(fetchSectionItems).mockRejectedValue(new Error("offline"));
    const onConnectionLost = vi.fn();

    renderSection(onConnectionLost);

    await waitFor(() => expect(onConnectionLost).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("button", { name: /Visible item/ })).toBeVisible();
    expect(readViewerCache(identity, key)).toEqual([item]);
  });

  it("drops the cache and the cached items when revalidation is rejected as unauthorized", async () => {
    identityMock.identity = identity;
    writeViewerCache(identity, key, [item]);
    writeViewerCache(identity, "entity:other", { id: "other" });
    vi.mocked(fetchSectionItems).mockRejectedValue(new ApiError("forbidden", 403));

    const { container } = renderSection();

    await waitFor(() => expect(container).toBeEmptyDOMElement());
    expect(readViewerCache(identity, key)).toBeUndefined();
    expect(readViewerCache(identity, "entity:other")).toBeUndefined();
  });
});
