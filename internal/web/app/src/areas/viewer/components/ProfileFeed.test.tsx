import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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

beforeEach(() => {
  // Reads past the mocked pages come back empty.
  vi.mocked(fetchSourceItems).mockResolvedValue([]);
});

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

  it("offers no more once a short page is followed by a run of empty ones", async () => {
    vi.mocked(fetchSourceItems).mockResolvedValueOnce(page(0, 4));
    renderFeed();

    expect(await screen.findByText("Item 3")).toBeInTheDocument();
    await waitFor(() => expect(fetchSourceItems).toHaveBeenCalledTimes(4));
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("reads past a page that muting shortened", async () => {
    // The server pages first and drops muted rows after, so a page of 99 can
    // still have a row after it.
    vi.mocked(fetchSourceItems)
      .mockResolvedValueOnce(page(0, PROFILE_FEED_LIMIT - 1))
      .mockResolvedValueOnce(page(PROFILE_FEED_LIMIT, 1));
    renderFeed();

    expect(await screen.findByText(`Item ${PROFILE_FEED_LIMIT}`)).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(PROFILE_FEED_LIMIT);
    await waitFor(() => expect(fetchSourceItems).toHaveBeenCalledTimes(5));
    expect(vi.mocked(fetchSourceItems).mock.calls.map(([query]) => query.offset)).toEqual(
      [0, 1, 2, 3, 4].map((n) => n * PROFILE_FEED_LIMIT),
    );
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("reads past a page that muting emptied", async () => {
    // A whole page of muted rows comes back empty while visible rows follow.
    vi.mocked(fetchSourceItems)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce(page(PROFILE_FEED_LIMIT, 2));
    renderFeed();

    expect(await screen.findByText(`Item ${PROFILE_FEED_LIMIT + 1}`)).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    expect(screen.queryByText("Nothing yet")).toBeNull();
    expect(vi.mocked(fetchSourceItems).mock.calls[1][0]).toMatchObject({
      offset: PROFILE_FEED_LIMIT,
    });
  });

  it("leaves Load more after a read that hit its bound on short pages", async () => {
    const reads = vi.mocked(fetchSourceItems);
    for (let n = 0; n < 10; n++) reads.mockResolvedValueOnce(page(n * PROFILE_FEED_LIMIT, 1));
    renderFeed();

    expect(await screen.findByText(`Item ${9 * PROFILE_FEED_LIMIT}`)).toBeInTheDocument();
    expect(fetchSourceItems).toHaveBeenCalledTimes(10);
    expect(screen.getByRole("button", { name: "Load more" })).toBeInTheDocument();
  });
});

describe("ProfileFeed filters (CORE-3333)", () => {
  it("sends the filters with every page so later pages stay narrowed", async () => {
    vi.mocked(fetchSourceItems)
      .mockResolvedValueOnce(page(0, PROFILE_FEED_LIMIT))
      .mockResolvedValueOnce(page(PROFILE_FEED_LIMIT, 1));
    render(
      <I18nProvider initialLocale="en">
        <ProfileFeed
          kind="recently-played"
          empty="Nothing yet"
          filters={{
            query: "coltrane",
            libraryID: "lib-music",
            playedAfter: "2026-11-01T00:00:00.000Z",
          }}
        />
      </I18nProvider>,
    );
    expect(await screen.findByText(`Item ${PROFILE_FEED_LIMIT - 1}`)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText(`Item ${PROFILE_FEED_LIMIT}`)).toBeInTheDocument();
    for (const [vars] of vi.mocked(fetchSourceItems).mock.calls) {
      expect(vars).toMatchObject({
        kind: "recently-played",
        query: "coltrane",
        libraryID: "lib-music",
        playedAfter: "2026-11-01T00:00:00.000Z",
      });
    }
  });

  it("ends a played feed on its first short page", async () => {
    vi.mocked(fetchSourceItems).mockResolvedValueOnce(page(0, 1));
    render(
      <I18nProvider initialLocale="en">
        <ProfileFeed
          kind="most-played"
          empty="Nothing yet"
          filters={{ query: "coltrane", libraryID: "", playedAfter: "" }}
        />
      </I18nProvider>,
    );
    expect(await screen.findByText("Item 0")).toBeInTheDocument();
    expect(fetchSourceItems).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
  });

  it("omits empty filters from the request", async () => {
    renderFeed();
    await waitFor(() => expect(fetchSourceItems).toHaveBeenCalled());
    const vars = vi.mocked(fetchSourceItems).mock.calls[0][0];
    expect(vars).not.toHaveProperty("query");
    expect(vars).not.toHaveProperty("playedAfter");
    expect(vars.libraryID).toBe("");
  });
});
