import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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
      .mockResolvedValueOnce(page(PROFILE_FEED_LIMIT, 3));
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

  it("offers no more when the first page is short", async () => {
    vi.mocked(fetchSourceItems).mockResolvedValueOnce(page(0, 4));
    renderFeed();

    expect(await screen.findByText("Item 3")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
    expect(fetchSourceItems).toHaveBeenCalledTimes(1);
  });
});
