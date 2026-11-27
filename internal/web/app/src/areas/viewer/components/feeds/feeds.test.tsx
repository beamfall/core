import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import {
  type FeedSubscription,
  followFeed,
  queuePlayNextMedia,
  refreshSubscription,
  type SubscriptionEntry,
  type SubscriptionHealth,
  searchFeeds,
  setSubscriptionDownloadPolicy,
  setWatched,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { nodePath } from "@/lib/nodePath";
import {
  asFeedURL,
  canDiscoverFeedURL,
  catalogueView,
  formatBytes,
  isForbidden,
  isRateLimited,
} from "../../lib/feeds";
import {
  insertQueueNext,
  type PlayQueueItem,
  resetPlayNextForTest,
  setActivePlayNextHandler,
} from "../../lib/watchQueue";
import { FeedCatalogue } from "./FeedCatalogue";
import { FeedFollowPanel } from "./FeedFollowPanel";
import { FeedHealth } from "./FeedHealth";
import { FeedList } from "./FeedList";

// CORE-2957: following a feed, the feed home's health and refresh, the
// Everything confirmation and the catalogue.

vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return {
    ...actual,
    searchFeeds: vi.fn(),
    followFeed: vi.fn(),
    queuePlayNextMedia: vi.fn(),
    refreshSubscription: vi.fn(),
    setSubscriptionDownloadPolicy: vi.fn(),
    setWatched: vi.fn(),
  };
});
vi.mock("@/lib/session", () => ({ useViewerIdentity: () => "viewer-1" }));

beforeEach(() => {
  vi.mocked(searchFeeds).mockReset();
  vi.mocked(followFeed).mockReset();
  vi.mocked(queuePlayNextMedia).mockReset();
  vi.mocked(refreshSubscription).mockReset();
  vi.mocked(setSubscriptionDownloadPolicy).mockReset();
  vi.mocked(setWatched).mockReset();
  resetPlayNextForTest();
});
afterEach(cleanup);

function subscription(overrides: Partial<FeedSubscription> = {}): FeedSubscription {
  return {
    id: "sub-1",
    libraryID: "lib-1",
    title: "Morning Notes",
    downloadPolicy: "latest",
    downloadLatestN: 3,
    healthStatus: "ok",
    entryCount: 120,
    downloadedCount: 3,
    catalogBytes: 41_000_000_000,
    ...overrides,
  };
}

function throttled(): ApiError {
  return new ApiError("subscription refresh throttled", 200, {
    errors: [{ message: "subscription refresh throttled", extensions: { code: "THROTTLED" } }],
  });
}

function wrap(ui: React.ReactNode, path = "/") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>{ui}</MemoryRouter>
    </I18nProvider>,
  );
}

describe("feed helpers", () => {
  it("tells a feed address from search words", () => {
    expect(asFeedURL("https://feeds.example.org/show.xml")).toBe(
      "https://feeds.example.org/show.xml",
    );
    expect(asFeedURL("  http://example.org/rss ")).toBe("http://example.org/rss");
    expect(asFeedURL("morning notes")).toBeNull();
    expect(asFeedURL("ftp://example.org/feed")).toBeNull();
  });

  it("recognises the refresh rate limit by its code", () => {
    expect(isRateLimited(throttled())).toBe(true);
    expect(isRateLimited(new ApiError("refresh unavailable", 200, { errors: [] }))).toBe(false);
    expect(isRateLimited(new Error("boom"))).toBe(false);
  });

  it("recognises role denials without treating compatibility refusals as role denials", () => {
    const roleDenial = new ApiError("forbidden", 200, {
      errors: [{ message: "forbidden", extensions: { code: "FORBIDDEN" } }],
    });
    const compatRefusal = new ApiError("POST /api/graphql failed", 403, {
      decision: "client-too-old",
      reasonCode: "unknown-build",
      messageClass: "compat-client-too-old",
      data: null,
      errors: [
        {
          message: "client update required",
          extensions: {
            code: "FORBIDDEN",
            decision: "client-too-old",
            reasonCode: "unknown-build",
            messageClass: "compat-client-too-old",
          },
        },
      ],
    });
    expect(isForbidden(roleDenial)).toBe(true);
    expect(isForbidden(compatRefusal)).toBe(false);
    expect(isForbidden(new Error("boom"))).toBe(false);
  });

  it("formats sizes in the display locale and hides unknown ones", () => {
    expect(formatBytes(41_000_000_000, "en")).toBe("41 GB");
    expect(formatBytes(0, "en")).toBe("");
  });
});

describe("FeedFollowPanel", () => {
  it("searches, shows results and follows with the default Latest 3 policy", async () => {
    const user = userEvent.setup();
    vi.mocked(searchFeeds).mockResolvedValue([
      {
        title: "Morning Notes",
        feedURL: "https://feeds.example.org/morning.xml",
        artworkURL: "https://cdn.example.org/art.jpg",
        description: "By the Notes team",
      },
    ]);
    vi.mocked(followFeed).mockResolvedValue(subscription());
    const onFollowed = vi.fn();
    wrap(<FeedFollowPanel libraryID="lib-1" onFollowed={onFollowed} />);

    expect(screen.getByRole("radio", { name: "Latest 3" })).toBeChecked();
    await user.type(screen.getByRole("searchbox"), "morning");
    await user.click(screen.getByRole("button", { name: "Search" }));

    expect(searchFeeds).toHaveBeenCalledWith("morning");
    expect(await screen.findByText("Morning Notes")).toBeInTheDocument();
    expect(screen.getByText("By the Notes team")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Follow Morning Notes" }));

    expect(followFeed).toHaveBeenCalledWith("https://feeds.example.org/morning.xml", {
      libraryID: "lib-1",
      downloadPolicy: "latest",
      latestN: 3,
    });
    expect(onFollowed).toHaveBeenCalledWith(subscription());
    expect(await screen.findByRole("button", { name: "Following" })).toBeDisabled();
  });

  it("follows HTTP and credentialed feed addresses directly without calling discovery", async () => {
    const user = userEvent.setup();
    vi.mocked(followFeed).mockResolvedValue(subscription({ downloadPolicy: "stream-only" }));
    wrap(<FeedFollowPanel libraryID="lib-1" onFollowed={vi.fn()} />);

    await user.click(screen.getByRole("radio", { name: "Stream only" }));
    expect(screen.queryByRole("radio", { name: "Everything" })).not.toBeInTheDocument();
    await user.type(screen.getByRole("searchbox"), "http://example.org/feed.xml{Enter}");

    expect(searchFeeds).not.toHaveBeenCalled();
    await user.click(await screen.findByRole("button", { name: "Follow Feed address" }));
    expect(followFeed).toHaveBeenCalledWith("http://example.org/feed.xml", {
      libraryID: "lib-1",
      downloadPolicy: "stream-only",
      latestN: undefined,
    });

    const userinfoURL = new URL("https://example.org/private.xml");
    userinfoURL.username = "user";
    userinfoURL.password = "pass";
    await user.clear(screen.getByRole("searchbox"));
    await user.type(screen.getByRole("searchbox"), `${userinfoURL.toString()}{Enter}`);
    await user.clear(screen.getByRole("searchbox"));
    await user.type(
      screen.getByRole("searchbox"),
      "https://example.org/private.xml?private_token=abc{Enter}",
    );
    await user.clear(screen.getByRole("searchbox"));
    await user.type(
      screen.getByRole("searchbox"),
      "https://example.org/private.xml?feed_token=abc{Enter}",
    );
    await user.clear(screen.getByRole("searchbox"));
    await user.type(
      screen.getByRole("searchbox"),
      "https://example.org/private.xml?X-API-Key=abc{Enter}",
    );
    expect(searchFeeds).not.toHaveBeenCalled();
  });

  it("runs discovery for clean HTTPS addresses and keeps direct follow when none match or discovery fails", async () => {
    const user = userEvent.setup();
    vi.mocked(searchFeeds)
      .mockResolvedValueOnce([])
      .mockRejectedValueOnce(throttled())
      .mockRejectedValueOnce(new Error("discovery byte cap exceeded"))
      .mockResolvedValueOnce([
        {
          title: "Daily Apple",
          feedURL: "https://feeds.example.org/daily.xml",
        },
      ]);
    vi.mocked(followFeed).mockResolvedValue(subscription());
    wrap(<FeedFollowPanel libraryID="lib-1" onFollowed={vi.fn()} />);

    await user.type(screen.getByRole("searchbox"), "https://example.org/feed.xml{Enter}");
    expect(searchFeeds).toHaveBeenCalledWith("https://example.org/feed.xml");
    await user.click(await screen.findByRole("button", { name: "Follow Feed address" }));
    expect(followFeed).toHaveBeenCalledWith("https://example.org/feed.xml", {
      libraryID: "lib-1",
      downloadPolicy: "latest",
      latestN: 3,
    });

    // THROTTLED discovery on a valid HTTPS feed still falls back to direct follow.
    await user.clear(screen.getByRole("searchbox"));
    await user.type(screen.getByRole("searchbox"), "https://example.org/throttled.xml{Enter}");
    expect(await screen.findByRole("button", { name: "Follow Feed address" })).toBeInTheDocument();

    // Upstream/size cap failure during discovery also keeps direct follow available.
    await user.clear(screen.getByRole("searchbox"));
    await user.type(screen.getByRole("searchbox"), "https://example.org/huge.xml{Enter}");
    expect(await screen.findByRole("button", { name: "Follow Feed address" })).toBeInTheDocument();

    await user.clear(screen.getByRole("searchbox"));
    await user.type(
      screen.getByRole("searchbox"),
      "https://podcasts.apple.com/ca/podcast/daily/id1234{Enter}",
    );
    expect(await screen.findByText("Daily Apple")).toBeInTheDocument();
  });

  it("surfaces server-localized THROTTLED and INVALID errors from feed search", async () => {
    const user = userEvent.setup();
    vi.mocked(searchFeeds)
      .mockRejectedValueOnce(
        new ApiError("Limite de recherche atteinte.", 200, {
          errors: [{ message: "Limite de recherche atteinte.", extensions: { code: "THROTTLED" } }],
        }),
      )
      .mockRejectedValueOnce(
        new ApiError("Saisissez une URL de site web valide.", 200, {
          errors: [
            { message: "Saisissez une URL de site web valide.", extensions: { code: "INVALID" } },
          ],
        }),
      );
    wrap(<FeedFollowPanel libraryID="lib-1" onFollowed={vi.fn()} />);

    await user.type(screen.getByRole("searchbox"), "daily{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent("Limite de recherche atteinte.");

    await user.clear(screen.getByRole("searchbox"));
    await user.type(screen.getByRole("searchbox"), "https://example.org/bad{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Saisissez une URL de site web valide.",
    );
  });

  it("says so when a follow fails", async () => {
    const user = userEvent.setup();
    vi.mocked(searchFeeds).mockResolvedValue([]);
    vi.mocked(followFeed).mockRejectedValue(new ApiError("followFeed unavailable", 200));
    wrap(<FeedFollowPanel libraryID="lib-1" onFollowed={vi.fn()} />);
    await user.type(screen.getByRole("searchbox"), "https://example.org/feed.xml{Enter}");
    await user.click(await screen.findByRole("button", { name: "Follow Feed address" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not follow this feed");
  });

  it("names the cause when the server classified a refused follow", async () => {
    const user = userEvent.setup();
    vi.mocked(searchFeeds).mockResolvedValue([]);
    vi.mocked(followFeed).mockRejectedValue(new ApiError("feed could not be read: http-4xx", 200));
    wrap(<FeedFollowPanel libraryID="lib-1" onFollowed={vi.fn()} />);
    await user.type(screen.getByRole("searchbox"), "https://example.org/feed.xml{Enter}");
    await user.click(await screen.findByRole("button", { name: "Follow Feed address" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Could not follow this feed. Check the address and try again. The server refused the request.",
    );
  });
});

describe("FeedHealth", () => {
  const now = new Date("2026-11-21T12:00:00Z");
  function failing(errorClass: string, state = "failing"): SubscriptionHealth {
    return {
      healthStatus: state,
      lastErrorClass: errorClass,
      failingSince: "2026-11-20T08:00:00Z",
      nextPollAt: "2026-11-21T13:00:00Z",
    };
  }

  it.each([
    ["dns", "Server not found"],
    ["tls", "Secure connection failed"],
    ["http-4xx", "The server refused the request"],
    ["http-5xx", "The server had an error"],
    ["parse", "Not a valid feed"],
    ["ssrf-blocked", "Address not allowed"],
    ["auth", "The feed needs a sign-in"],
    ["gone", "Feed removed by publisher"],
    ["network", "Network problem"],
    ["something-new", "Unknown problem"],
  ])("names the %s failure in plain words", (errorClass, words) => {
    wrap(<FeedHealth health={failing(errorClass)} now={now} />);
    const status = screen.getByRole("status");
    expect(status).toHaveAttribute("data-health", "failing");
    expect(status).toHaveTextContent(`Not updating: ${words}`);
    expect(status).toHaveTextContent("Failing since");
    expect(status).toHaveTextContent("Next try in 1 hour");
  });

  it("turns amber after a week of failing", () => {
    wrap(<FeedHealth health={failing("http-5xx", "warning")} now={now} />);
    const status = screen.getByRole("status");
    expect(status).toHaveAttribute("data-health", "warning");
    expect(status).toHaveTextContent("Not updated for a week: The server had an error");
    expect(within(status).getByText(/Not updated for a week/).className).toContain(
      "--state-warning",
    );
  });

  it("keeps ok and pending quiet and gives retired its own state", () => {
    const { rerender } = wrap(
      <FeedHealth health={{ healthStatus: "ok" }} lastPolledAt="2026-11-21T11:00:00Z" now={now} />,
    );
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.getByText("Updated 1 hour ago")).toBeInTheDocument();
    rerender(
      <I18nProvider>
        <FeedHealth health={{ healthStatus: "ok" }} now={now} />
      </I18nProvider>,
    );
    expect(screen.getByText("Checking for the first time")).toBeInTheDocument();
    rerender(
      <I18nProvider>
        <FeedHealth health={{ healthStatus: "retired", lastErrorClass: "http-4xx" }} now={now} />
      </I18nProvider>,
    );
    expect(screen.getByText(/Feed retired by its publisher/)).toHaveAttribute(
      "data-health",
      "retired",
    );
  });
});

describe("FeedList", () => {
  it("shows counts, policy and health for each followed feed", () => {
    wrap(
      <FeedList
        libraryPath="/n/library-lib-1"
        subscriptions={[subscription(failing())]}
        canChangePolicy={false}
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByRole("link", { name: "Open Morning Notes" })).toHaveAttribute(
      "href",
      nodePath("library-lib-1", "/feeds/sub-1"),
    );
    expect(screen.getByText("120 entries · 3 downloaded")).toBeInTheDocument();
    expect(screen.getByText("Latest 3")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Not updating: Server not found");

    function failing(): SubscriptionHealth {
      return { healthStatus: "failing", lastErrorClass: "dns" };
    }
  });

  it("refreshes, and explains a refresh refused as too soon", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    vi.mocked(refreshSubscription)
      .mockResolvedValueOnce({
        id: "sub-1",
        title: "Morning Notes",
        healthStatus: "ok",
        nextPollAt: "2026-11-21T12:00:00Z",
      })
      .mockRejectedValueOnce(throttled());
    wrap(
      <FeedList
        libraryPath="/n/library-lib-1"
        subscriptions={[subscription()]}
        canChangePolicy={false}
        onChange={onChange}
      />,
    );
    const refresh = screen.getByRole("button", { name: "Refresh Morning Notes" });
    await user.click(refresh);
    expect(refreshSubscription).toHaveBeenCalledWith("sub-1");
    expect(onChange).toHaveBeenCalledWith(subscription({ nextPollAt: "2026-11-21T12:00:00Z" }));
    expect(await screen.findByText("Checking for new entries.")).toBeInTheDocument();

    await user.click(refresh);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Checked a moment ago. Try again in a few minutes.",
    );
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("asks before downloading everything, showing the catalogue size", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    vi.mocked(setSubscriptionDownloadPolicy).mockResolvedValue(
      subscription({ downloadPolicy: "everything" }),
    );
    wrap(
      <FeedList
        libraryPath="/n/library-lib-1"
        subscriptions={[subscription()]}
        canChangePolicy
        onChange={onChange}
      />,
    );
    const policy = screen.getByRole("combobox", { name: "Downloads for Morning Notes" });

    await user.selectOptions(policy, "everything");
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Download everything?");
    expect(dialog).toHaveTextContent("about 41 GB");
    expect(setSubscriptionDownloadPolicy).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(setSubscriptionDownloadPolicy).not.toHaveBeenCalled();

    await user.selectOptions(policy, "everything");
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Download everything",
      }),
    );
    expect(setSubscriptionDownloadPolicy).toHaveBeenCalledWith("sub-1", "everything", {
      latestN: undefined,
      confirmEverything: true,
    });
    expect(onChange).toHaveBeenCalledWith(subscription({ downloadPolicy: "everything" }));
  });

  it("changes other policies without asking", async () => {
    const user = userEvent.setup();
    vi.mocked(setSubscriptionDownloadPolicy).mockResolvedValue(
      subscription({ downloadPolicy: "new-only" }),
    );
    wrap(
      <FeedList
        libraryPath="/n/library-lib-1"
        subscriptions={[subscription()]}
        canChangePolicy
        onChange={vi.fn()}
      />,
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Downloads for Morning Notes" }),
      "new-only",
    );
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(setSubscriptionDownloadPolicy).toHaveBeenCalledWith("sub-1", "new-only", {
      latestN: undefined,
      confirmEverything: undefined,
    });
  });
});

describe("FeedCatalogue", () => {
  function entry(overrides: Partial<SubscriptionEntry>): SubscriptionEntry {
    return {
      mediaID: "m",
      title: "Entry",
      publishedAt: "2026-11-01T12:00:00Z",
      feedPosition: 0,
      enclosureLength: 52_000_000,
      downloadState: "none",
      availability: "not-downloaded",
      played: false,
      ...overrides,
    };
  }

  function Where() {
    const location = useLocation();
    return (
      <output data-testid="where">
        {location.pathname}|{JSON.stringify(location.state)}
      </output>
    );
  }

  it("lists entries in feed order with availability, and plays any of them", async () => {
    const user = userEvent.setup();
    const entries = [
      entry({ mediaID: "m-2", title: "Second", feedPosition: 1, downloadState: "queued" }),
      entry({
        mediaID: "m-1",
        title: "First",
        feedPosition: 0,
        downloadState: "downloaded",
        availability: "downloaded",
      }),
      entry({ mediaID: "m-3", title: "Third", feedPosition: 2 }),
    ];
    wrap(
      <Routes>
        <Route path="/" element={<FeedCatalogue entries={entries} />} />
        <Route path="*" element={<Where />} />
      </Routes>,
    );

    const rows = screen.getAllByRole("listitem");
    expect(rows.map((row) => row.getAttribute("data-entry"))).toEqual(["m-1", "m-2", "m-3"]);
    expect(within(rows[0]).getByText("Downloaded")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Downloading")).toBeInTheDocument();
    expect(within(rows[2]).getByText("Not downloaded")).toBeInTheDocument();
    expect(within(rows[0]).getByText("Nov 1, 2026 · 52 MB")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Play Third" }));
    const where = screen.getByTestId("where").textContent ?? "";
    expect(where.startsWith("/watch/m-3|")).toBe(true);
    const state = JSON.parse(where.slice(where.indexOf("|") + 1));
    expect(state.title).toBe("Third");
    expect(state.queueIdentity).toBe("viewer-1");
    expect(state.queue.map((item: { id: string }) => item.id)).toEqual(["m-1", "m-2", "m-3"]);
  });

  it("filters Unplayed, sorts oldest first and marks played (CORE-2959)", async () => {
    const user = userEvent.setup();
    vi.mocked(setWatched).mockResolvedValue({} as Awaited<ReturnType<typeof setWatched>>);
    const entries = [
      entry({
        mediaID: "m-3",
        title: "Third",
        feedPosition: 0,
        publishedAt: "2026-11-03T12:00:00Z",
      }),
      entry({
        mediaID: "m-2",
        title: "Second",
        feedPosition: 1,
        publishedAt: "2026-11-02T12:00:00Z",
        played: true,
      }),
      entry({
        mediaID: "m-1",
        title: "First",
        feedPosition: 2,
        publishedAt: "2026-11-01T12:00:00Z",
      }),
    ];
    wrap(<FeedCatalogue entries={entries} />);
    const order = () =>
      screen.getAllByRole("listitem").map((row) => row.getAttribute("data-entry"));

    expect(order()).toEqual(["m-3", "m-2", "m-1"]);
    expect(screen.getAllByRole("listitem")[1]).toHaveAttribute("data-played", "true");

    await user.selectOptions(screen.getByLabelText("Sort"), "oldest");
    expect(order()).toEqual(["m-1", "m-2", "m-3"]);

    await user.selectOptions(screen.getByLabelText("Show"), "unplayed");
    expect(order()).toEqual(["m-1", "m-3"]);

    await user.click(screen.getByRole("button", { name: "Mark First played" }));
    expect(setWatched).toHaveBeenCalledWith({ mediaID: "m-1" }, true);
    expect(order()).toEqual(["m-3"]);

    await user.selectOptions(screen.getByLabelText("Show"), "all");
    await user.click(screen.getByRole("button", { name: "Mark Second unplayed" }));
    expect(setWatched).toHaveBeenLastCalledWith({ mediaID: "m-2" }, false);
  });

  it("places undated entries after dated ones in both sorts (CORE-2959)", () => {
    const entries = [
      entry({ mediaID: "jan-1", feedPosition: 0, publishedAt: "2026-01-01T12:00:00Z" }),
      entry({ mediaID: "undated", feedPosition: 1, publishedAt: "" }),
      entry({ mediaID: "jan-3", feedPosition: 2, publishedAt: "2026-01-03T12:00:00Z" }),
    ];
    const ids = (sort: "newest" | "oldest") =>
      catalogueView(entries, "all", sort).map((item) => item.mediaID);
    expect(ids("newest")).toEqual(["jan-3", "jan-1", "undated"]);
    expect(ids("oldest")).toEqual(["undated", "jan-1", "jan-3"]);
  });

  it("links each row title to /media/:id with its show and availability (CORE-3249)", async () => {
    const user = userEvent.setup();
    const entries = [
      entry({
        mediaID: "m-1",
        title: "First Episode",
        feedPosition: 0,
        availability: "downloaded",
        downloadState: "downloaded",
      }),
    ];
    wrap(
      <Routes>
        <Route
          path="/"
          element={
            <FeedCatalogue
              entries={entries}
              subscriptionID="sub-1"
              showTitle="Morning Notes"
              showPath="/n/library-lib-1/feeds/sub-1"
            />
          }
        />
        <Route path="*" element={<Where />} />
      </Routes>,
    );

    const link = screen.getByRole("link", { name: "First Episode" });
    expect(link).toHaveAttribute("href", "/media/m-1");
    await user.click(link);
    const where = screen.getByTestId("where").textContent ?? "";
    expect(where.startsWith("/media/m-1|")).toBe(true);
    const state = JSON.parse(where.slice(where.indexOf("|") + 1));
    expect(state.feedEntryPreview.entry).toMatchObject({
      mediaID: "m-1",
      subscriptionID: "sub-1",
      showTitle: "Morning Notes",
      showPath: "/n/library-lib-1/feeds/sub-1",
      availability: "downloaded",
    });
  });

  it("inserts Play next after the currently playing item during a catalogue-started session (CORE-3250)", async () => {
    const user = userEvent.setup();
    const entries = [
      entry({
        mediaID: "m-1",
        title: "First",
        feedPosition: 0,
        publishedAt: "2026-11-03T12:00:00Z",
      }),
      entry({
        mediaID: "m-2",
        title: "Second",
        feedPosition: 1,
        publishedAt: "2026-11-02T12:00:00Z",
      }),
      entry({
        mediaID: "m-3",
        title: "Third",
        feedPosition: 2,
        publishedAt: "2026-11-01T12:00:00Z",
      }),
    ];
    let activeQueue: PlayQueueItem[] = [
      { id: "m-1", title: "First", mediaClass: "audio" },
      { id: "m-2", title: "Second", mediaClass: "audio" },
      { id: "m-3", title: "Third", mediaClass: "audio" },
    ];
    setActivePlayNextHandler((item) => {
      activeQueue = insertQueueNext(activeQueue, "m-1", "First", item);
      return true;
    });

    wrap(
      <Routes>
        <Route path="/" element={<FeedCatalogue entries={entries} />} />
        <Route path="*" element={<Where />} />
      </Routes>,
    );
    await user.click(screen.getByRole("button", { name: "Play Third next" }));

    expect(activeQueue.map((item) => item.id)).toEqual(["m-1", "m-3", "m-2"]);
    expect(screen.getByRole("status")).toHaveTextContent("Third plays next.");
    expect(queuePlayNextMedia).not.toHaveBeenCalled();
  });

  it("keeps staged Play next items across FeedCatalogue link renders until Play is clicked (CORE-3250)", async () => {
    const user = userEvent.setup();
    const entries = [
      entry({
        mediaID: "m-1",
        title: "First",
        feedPosition: 0,
        publishedAt: "2026-11-03T12:00:00Z",
      }),
      entry({
        mediaID: "m-2",
        title: "Second",
        feedPosition: 1,
        publishedAt: "2026-11-02T12:00:00Z",
      }),
      entry({
        mediaID: "m-3",
        title: "Third",
        feedPosition: 2,
        publishedAt: "2026-11-01T12:00:00Z",
      }),
    ];

    wrap(
      <Routes>
        <Route path="/" element={<FeedCatalogue entries={entries} />} />
        <Route path="*" element={<Where />} />
      </Routes>,
    );
    await user.click(screen.getByRole("button", { name: "Play Third next" }));
    expect(screen.getByRole("status")).toHaveTextContent("Third plays next.");

    await user.click(screen.getByRole("button", { name: "Play First" }));
    const where = screen.getByTestId("where").textContent ?? "";
    expect(where.startsWith("/watch/m-1|")).toBe(true);
    const state = JSON.parse(where.slice(where.indexOf("|") + 1));
    expect(state.queue.map((item: { id: string }) => item.id)).toEqual(["m-1", "m-3", "m-2"]);
  });
});

describe("opaque semicolon query discovery and direct follow (CORE-2970)", () => {
  it("allows benign semicolon queries in canDiscoverFeedURL while still blocking semicolon credentials", () => {
    expect(canDiscoverFeedURL("https://example.org/feed.xml?a=1;b=2")).toBe(true);
    expect(canDiscoverFeedURL("https://example.org/feed.xml?a=1;feed_token=secret")).toBe(false);
  });

  it("preserves direct Follow when searchFeeds returns INVALID for an opaque semicolon query", async () => {
    const user = userEvent.setup();
    vi.mocked(searchFeeds).mockRejectedValue(
      new ApiError("invalid URL query", 200, {
        errors: [{ message: "invalid URL query", extensions: { code: "INVALID" } }],
      }),
    );
    vi.mocked(followFeed).mockResolvedValue(subscription());

    wrap(<FeedFollowPanel libraryID="lib-1" onFollowed={vi.fn()} />);
    await user.type(screen.getByRole("searchbox"), "https://example.org/feed.xml?a=1;b=2");
    await user.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByText("https://example.org/feed.xml?a=1;b=2")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^Follow/i }));
    expect(followFeed).toHaveBeenCalledWith(
      "https://example.org/feed.xml?a=1;b=2",
      expect.objectContaining({ libraryID: "lib-1" }),
    );
  });
});
