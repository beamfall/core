import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchSourceItems } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { PROFILE_FEED_LIMIT, ProfileFeed } from "./ProfileFeed";

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fetchSourceItems: vi.fn(),
}));
vi.mock("@/lib/session", () => ({ useViewerIdentity: () => "viewer-1" }));
vi.mock("../actions/engagementSync", () => ({ useEngagementEpoch: () => 0 }));
vi.mock("./MediaItemCard", () => ({
  MediaItemCard: ({ item }: { item: SourceItem }) => <li>{item.title}</li>,
}));

function page(start: number, count: number): SourceItem[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `m${start + i}`,
    kind: "media",
    title: `Item ${start + i}`,
  }));
}

function renderFeed() {
  render(
    <I18nProvider initialLocale="en">
      <ProfileFeed kind="favorites" empty="Nothing yet" />
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(fetchSourceItems).mockReset();
});

describe("ProfileFeed paging (CORE-1878)", () => {
  it("loads the next page of a feed longer than one page", async () => {
    vi.mocked(fetchSourceItems)
      .mockResolvedValueOnce(page(0, PROFILE_FEED_LIMIT))
      .mockResolvedValueOnce(page(PROFILE_FEED_LIMIT, 3))
      .mockResolvedValueOnce([]);
    renderFeed();

    expect(await screen.findByText(`Item ${PROFILE_FEED_LIMIT - 1}`)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));

    expect(await screen.findByText(`Item ${PROFILE_FEED_LIMIT + 2}`)).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(PROFILE_FEED_LIMIT + 3);
    expect(vi.mocked(fetchSourceItems).mock.calls[1][0]).toMatchObject({
      kind: "favorites",
      limit: PROFILE_FEED_LIMIT,
      offset: PROFILE_FEED_LIMIT,
    });
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("offers no more once a short page is followed by an empty one", async () => {
    vi.mocked(fetchSourceItems).mockResolvedValueOnce(page(0, 4)).mockResolvedValueOnce([]);
    renderFeed();

    expect(await screen.findByText("Item 3")).toBeInTheDocument();
    await waitFor(() => expect(fetchSourceItems).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("reads past a page that muting shortened", async () => {
    // The server pages first and drops muted rows after, so a page of 99 can
    // still have a row after it.
    vi.mocked(fetchSourceItems)
      .mockResolvedValueOnce(page(0, PROFILE_FEED_LIMIT - 1))
      .mockResolvedValueOnce(page(PROFILE_FEED_LIMIT, 1))
      .mockResolvedValueOnce([]);
    renderFeed();

    expect(await screen.findByText(`Item ${PROFILE_FEED_LIMIT}`)).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(PROFILE_FEED_LIMIT);
    expect(vi.mocked(fetchSourceItems).mock.calls.map(([query]) => query.offset)).toEqual([
      0,
      PROFILE_FEED_LIMIT,
      2 * PROFILE_FEED_LIMIT,
    ]);
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });
});
