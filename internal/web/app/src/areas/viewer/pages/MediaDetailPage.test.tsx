import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  calibrateWatchPartyClock,
  fetchEntity,
  fetchMediaForEntity,
  fetchPrePlayQueue,
  fetchRelatedMedia,
  fetchSimilarMedia,
  fetchSourceItems,
  fetchSubscriptionEntries,
} from "@/lib/graphql";
import type { GenericMediaInfo, Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache, readViewerCache, writeViewerCache } from "@/lib/viewerCache";
import { expectNoAxeViolations } from "@/test/axe";
import { sectionCacheKey } from "../components/HydratedSection";
import { mediaPath } from "../lib/mediaAddress";
import {
  fetchMedia,
  fetchMediaDetailFields,
  fetchMediaRelations,
  fetchMediaTracks,
  type MediaTrackVersion,
  mediaDetailCacheKey,
  mediaLinkState,
} from "../lib/mediaDetail";
import { readVersionChoice } from "../lib/versionChoice";
import { resetHomeNetworkProbeForTest } from "../lib/watchTogether";
import { usePlayerHost } from "../player/PlayerHost";
import { MediaDetailPage } from "./MediaDetailPage";
import { PlayerPage } from "./PlayerPage";

const mocks = vi.hoisted(() => ({
  identity: null as string | null,
  // The server's item state for the page's Media (CORE-2436); it outlives a render.
  itemState: { id: "m1", kind: "media", watched: false, watchlisted: false, rating: 0 },
  itemCalls: [] as Array<[string, unknown, unknown]>,
  session: null as { profileID: string; profile?: string } | null,
  isManager: false,
}));

vi.mock("../lib/mediaDetail", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/mediaDetail")>()),
  fetchMedia: vi.fn(),
  fetchMediaDetailFields: vi.fn(),
  fetchMediaRelations: vi.fn(),
  fetchMediaTracks: vi.fn(),
}));
vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fetchEntity: vi.fn(),
  fetchMediaForEntity: vi.fn(),
  fetchPrePlayQueue: vi.fn(),
  fetchRelatedMedia: vi.fn(),
  fetchSimilarMedia: vi.fn(),
  fetchSourceItems: vi.fn(),
  fetchSubscriptionEntries: vi.fn(),
  calibrateWatchPartyClock: vi.fn(),
  fetchItemState: vi.fn(async () => mocks.itemState),
  fetchPlaybackMarkers: vi.fn(async () => []),
  ...Object.fromEntries(
    (["setWatched", "setWatchlisted", "setRating"] as const).map((name) => [
      name,
      vi.fn(async (target: unknown, value: unknown) => {
        mocks.itemCalls.push([name, target, value]);
        const field = { setWatched: "watched", setWatchlisted: "watchlisted", setRating: "rating" }[
          name
        ];
        mocks.itemState = { ...mocks.itemState, [field]: value };
        return mocks.itemState;
      }),
    ]),
  ),
}));
vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => mocks.identity,
  useSession: () => ({
    manifest: null,
    revealed: false,
    session: mocks.session,
    isManager: mocks.isManager,
  }),
}));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <main>{children}</main>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("../player/PlayerHost", () => ({ usePlayerHost: vi.fn() }));
vi.mock("../player/preload", () => ({ preloadPlayer: vi.fn() }));

const identity = JSON.stringify(["viewer", "p1", "adult", false]);
const media: GenericMediaInfo = {
  id: "m1",
  title: "Server title",
  summary: "Server summary",
  year: 2001,
  favorited: false,
};
const card: SourceItem = {
  id: "m1",
  kind: "media",
  title: "Card title",
  artworkSlots: { poster: "/api/media/artwork/m1/poster?v=1" },
};
const shelf: Section = {
  id: "sec-recent",
  kind: "shelf",
  title: { en: "Recent" },
  artworkSlot: "poster",
  source: { kind: "library" } as Section["source"],
};

function Probe() {
  const location = useLocation();
  return (
    <>
      <p data-testid="path">{location.pathname}</p>
      <p data-testid="search">{location.search}</p>
      <p data-testid="state">{JSON.stringify(location.state)}</p>
    </>
  );
}

function renderAt(
  entries: Array<string | { pathname: string; state?: unknown }>,
  watchPage: ReactNode = <Probe />,
) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
        <Routes>
          <Route path="/" element={<Probe />} />
          <Route path="/n/:id" element={<Probe />} />
          <Route path="/watch/*" element={watchPage} />
          <Route path="/entity/:id" element={<Probe />} />
          <Route path="/media/*" element={<MediaDetailPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

// The player nav state the last Play or Start over sent, without its startID,
// which is checked to be present: each press is its own playback start.
function watchState() {
  const state = JSON.parse(screen.getByTestId("state").textContent || "null");
  expect(state.startID).toEqual(expect.any(String));
  expect(state.startID).not.toBe("");
  const { startID: _, ...rest } = state;
  return rest;
}

// The queue the last track-row Play sent; a row's Play carries no startID.
function rowQueueIDs(): string[] {
  const state = JSON.parse(screen.getByTestId("state").textContent || "null");
  return state.queue.map((item: { id: string }) => item.id);
}

function pending<T>() {
  let resolve!: (value: T) => void;
  let reject!: (err: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  mocks.identity = identity;
  mocks.isManager = false;
  vi.mocked(usePlayerHost).mockReset();
  vi.mocked(fetchMedia).mockReset();
  vi.mocked(fetchMediaDetailFields).mockReset().mockResolvedValue([]);
  vi.mocked(fetchMediaRelations).mockReset().mockResolvedValue([]);
  vi.mocked(fetchMediaTracks).mockReset().mockResolvedValue([]);
  vi.mocked(fetchMediaForEntity).mockReset().mockResolvedValue([]);
  vi.mocked(fetchPrePlayQueue).mockReset().mockResolvedValue([]);
  vi.mocked(fetchRelatedMedia).mockReset().mockResolvedValue([]);
  vi.mocked(fetchSimilarMedia).mockReset().mockResolvedValue([]);
  vi.mocked(fetchSourceItems).mockReset().mockResolvedValue([]);
  vi.mocked(fetchEntity).mockReset().mockResolvedValue(null);
  // Off the home network by default: Watch together is not offered (CORE-1764).
  resetHomeNetworkProbeForTest();
  vi.mocked(calibrateWatchPartyClock).mockReset().mockRejectedValue(new Error("home network only"));
});

afterEach(() => {
  cleanup();
  clearViewerCache();
  mocks.itemState = { id: "m1", kind: "media", watched: false, watchlisted: false, rating: 0 };
  mocks.itemCalls = [];
  mocks.session = null;
  window.localStorage.clear();
});

describe("MediaDetailPage", () => {
  it.each([
    [true, "adult", true],
    [false, "adult", false],
    [true, "child", false],
  ])("gates Assign and Edit metadata (%s, %s)", async (manager, profile, allowed) => {
    mocks.isManager = manager;
    mocks.session = { profileID: "p1", profile };
    vi.mocked(fetchMedia).mockResolvedValue(media);
    renderAt(["/media/m1"]);
    await screen.findByRole("heading", { name: media.title });

    const assign = screen.queryByRole("button", { name: "Assign…" });
    const edit = screen.queryByRole("link", { name: "Edit metadata" });
    if (allowed) {
      expect(assign).toBeEnabled();
      expect(edit).toHaveAttribute("href", "/manage/items/m1");
    } else {
      expect(assign).not.toBeInTheDocument();
      expect(edit).not.toBeInTheDocument();
    }
  });

  it("shows a busy skeleton, then the server header with labelled declared fields", async () => {
    const request = pending<GenericMediaInfo | null>();
    vi.mocked(fetchMedia).mockReturnValue(request.promise);
    vi.mocked(fetchMediaDetailFields).mockResolvedValue([
      { name: "studio", type: "text", value: "North", label: { en: "Studio" } },
      { name: "rating", type: "text", value: "PG" },
    ]);
    renderAt(["/media/m1"]);

    expect(screen.getByRole("status", { name: "Loading..." })).toHaveAttribute("aria-busy", "true");
    expect(screen.queryByRole("heading", { level: 1 })).toBeNull();

    await act(async () => request.resolve(media));

    expect(screen.getByRole("heading", { level: 1, name: "Server title" })).toBeInTheDocument();
    expect(screen.getByText("2001")).toBeInTheDocument();
    expect(screen.getByText("Server summary")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Details" })).toBeInTheDocument();
    expect(screen.getByText("Studio")).toBeInTheDocument();
    expect(screen.getByText("North")).toBeInTheDocument();
    // An undeclared field (no plugin label) is bookkeeping: Manage shows it, the Viewer does not.
    expect(screen.queryByText("rating")).toBeNull();
    expect(screen.queryByText("PG")).toBeNull();
    // A deep link knows no resume point: plain Play, no progress bar.
    expect(screen.getByRole("button", { name: "Play Server title" })).toHaveTextContent("Play");
    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(readViewerCache(identity, mediaDetailCacheKey("m1"))).toMatchObject({ media });
  });

  it.each([
    { id: "m1", fromSection: false },
    { id: "media-sample1-a371bff46385f7ded", fromSection: false },
    { id: "media-sample1-a371bff46385f7ded", fromSection: true },
  ])(
    "paints $id at once from the viewer preview and plays it (section=$fromSection)",
    async ({ id, fromSection }) => {
      const item = { ...card, id, artworkSlots: { poster: `/api/media/artwork/${id}/poster?v=1` } };
      if (fromSection) writeViewerCache(identity, sectionCacheKey(shelf, "lib", "", null), [item]);
      vi.mocked(fetchMedia).mockRejectedValue(new TypeError("Failed to fetch"));
      const path = `/media/${id}`;
      window.history.replaceState(null, "", path);
      try {
        const { container } = renderAt([
          { pathname: path, state: fromSection ? undefined : mediaLinkState(item, identity) },
        ]);

        expect(screen.getByRole("heading", { level: 1, name: "Card title" })).toBeInTheDocument();
        expect(container.querySelector(".bf-detail-poster img")?.getAttribute("src")).toContain(
          `/api/media/artwork/${id}/poster`,
        );
        await act(async () => {});
        expect(screen.getByRole("heading", { level: 1, name: "Card title" })).toBeInTheDocument();
        expect(window.location.pathname).toBe(path);
        expect(fetchMedia).toHaveBeenCalledWith(id, expect.any(AbortSignal));

        fireEvent.click(screen.getByRole("button", { name: "Play Card title" }));
        expect(screen.getByTestId("path")).toHaveTextContent(mediaPath("watch", id));
        // Unread credits: the item plays alone (ADR-0055 §2.4).
        expect(watchState()).toEqual({
          title: "Card title",
          queue: [{ id, title: "Card title" }],
          queueIdentity: identity,
        });
      } finally {
        window.history.replaceState(null, "", "/");
      }
    },
  );

  it.each([
    { action: "Play Server title", search: "" },
    { action: "Start Server title from the beginning", search: "?t=0" },
  ])(
    "opens the player through $action when the catalog is unavailable",
    async ({ action, search }) => {
      const id = "media-sample1-a371bff46385f7ded";
      const attach = vi.fn();
      vi.mocked(usePlayerHost).mockReturnValue({ attach, detach: vi.fn(), stop: vi.fn() });
      vi.mocked(fetchMedia).mockResolvedValue({ ...media, id });
      const item = { ...card, id, engagement: { positionSeconds: 60, durationSeconds: 600 } };
      renderAt(
        [
          {
            pathname: "/media/sample1.a371bff46385f7ded",
            state: search ? mediaLinkState(item, identity) : undefined,
          },
        ],
        <PlayerPage />,
      );
      const control = await screen.findByRole("button", { name: action });
      vi.mocked(fetchMedia).mockClear().mockRejectedValue(new TypeError("Failed to fetch"));

      fireEvent.click(control);

      await waitFor(() =>
        expect(attach).toHaveBeenCalledWith({
          mediaId: id,
          search,
          state: expect.objectContaining({ title: "Server title" }),
        }),
      );
      expect(fetchMedia).not.toHaveBeenCalled();
    },
  );

  it("forwards the opening card's queue to the player, only for its own identity (CORE-1143)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    const queue = [
      { id: "m1", title: "Card title" },
      { id: "m2", title: "Next" },
    ];
    renderAt([
      {
        pathname: "/media/m1",
        state: { ...mediaLinkState(card, identity), queue, queueIdentity: identity },
      },
    ]);

    fireEvent.click(await screen.findByRole("button", { name: "Play Server title" }));
    expect(watchState()).toEqual({
      title: "Server title",
      queue,
      queueIdentity: identity,
    });
  });

  it("drops a queue listed for another identity", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    renderAt([
      {
        pathname: "/media/m1",
        state: {
          ...mediaLinkState(card, identity),
          queue: [{ id: "m2", title: "Next" }],
          queueIdentity: "someone-else",
        },
      },
    ]);

    fireEvent.click(await screen.findByRole("button", { name: "Play Server title" }));
    expect(watchState()).toEqual({
      title: "Server title",
    });
  });

  // ADR-0055 §2.4: until the credits say whether the item is a sequenced
  // book, Play queues it alone, which also ends an earlier Play all queue.
  it("plays an item alone before its credits load", () => {
    vi.mocked(fetchMedia).mockReturnValue(new Promise(() => {}));
    renderAt([
      {
        pathname: "/media/m1",
        state: {
          ...mediaLinkState(card, identity),
          queue: [
            { id: "m1", title: "Card title" },
            { id: "m2", title: "Next" },
          ],
          queueIdentity: identity,
        },
      },
    ]);

    fireEvent.click(screen.getByRole("button", { name: "Play Card title" }));
    expect(watchState()).toEqual({
      title: "Card title",
      queue: [{ id: "m1", title: "Card title" }],
      queueIdentity: identity,
    });
  });

  // CORE-3147: an item whose plugin declares it plays alone (a standalone book,
  // or one in a series without a sequence) sends a one-item queue; another
  // loaded item forwards the queue its card carried.
  it.each([
    { playsAlone: true, queue: [{ id: "m1", title: "Server title" }] },
    {
      playsAlone: false,
      queue: [
        { id: "m1", title: "Card title" },
        { id: "m2", title: "Next" },
      ],
    },
  ])(
    "sends the queue for a loaded item (playsAlone=$playsAlone)",
    async ({ playsAlone, queue }) => {
      vi.mocked(fetchMedia).mockResolvedValue({ ...media, playsAlone });
      renderAt([
        {
          pathname: "/media/m1",
          state: {
            ...mediaLinkState(card, identity),
            queue: [
              { id: "m1", title: "Card title" },
              { id: "m2", title: "Next" },
            ],
            queueIdentity: identity,
          },
        },
      ]);
      await act(async () => {});
      fireEvent.click(screen.getByRole("button", { name: "Play Server title" }));
      expect(watchState()).toMatchObject({ queue, queueIdentity: identity });
    },
  );

  // CORE-3013: a book in a sequenced series offers the next one once played,
  // never queues it, and Mark played offers it at once.
  it("offers Next in series once the book is marked played, and plays the book alone", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      mediaClass: "audio",
      credits: [
        {
          entityID: "s1",
          title: "The Cycle",
          kind: "series",
          archetype: "container",
          relation: "contains-book",
          attributes: { sequence: "2" },
        },
      ],
    });
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      { id: "m1", kind: "media", title: "Server title" },
      { id: "m2", kind: "media", title: "Book Three" },
    ]);
    renderAt(["/media/m1"]);
    const played = await screen.findByRole("button", { name: "Mark played" });
    await act(async () => {});
    expect(screen.queryByRole("link", { name: /^Next in series/ })).toBeNull();

    fireEvent.click(played);
    expect(await screen.findByRole("link", { name: "Next in series: Book Three" })).toHaveAttribute(
      "href",
      "/media/m2",
    );
    fireEvent.click(screen.getByRole("button", { name: "Play Server title" }));
    expect(watchState().queue).toEqual([{ id: "m1", title: "Server title" }]);
  });

  it("ignores a card carried for another identity", () => {
    vi.mocked(fetchMedia).mockReturnValue(new Promise(() => {}));
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(card, "someone-else") }]);
    expect(screen.queryByText("Card title")).toBeNull();
    expect(screen.getByRole("status", { name: "Loading..." })).toBeInTheDocument();
  });

  it("offers Resume with progress when the card carried a position", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    const started = {
      ...card,
      engagement: { positionSeconds: 600, durationSeconds: 2400 },
    } as SourceItem;
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(started, identity) }]);
    await act(async () => {});

    const bar = screen.getByRole("progressbar");
    expect(bar).toHaveAttribute("aria-valuenow", "25");
    expect(bar).toHaveAttribute("aria-valuetext", "25% complete");
    const resume = screen.getByRole("button", { name: "Resume Server title" });
    expect(resume).toHaveTextContent("Resume");
    // The bar rides Resume's edge, beside the button, with no row of its own
    // and no visible percentage (CORE-2930).
    expect(bar.parentElement).toBe(resume.parentElement);
    expect(screen.queryByText("25% complete")).toBeNull();
  });

  it("reads back, title, facts and overview before the actions (CORE-2930)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    renderAt(["/media/m1"]);
    const title = await screen.findByRole("heading", { level: 1, name: "Server title" });
    const stage = title.closest("section") as HTMLElement;
    // Back sits inside the stage, ahead of the lockup, clear of the top bar.
    const back = within(stage).getByRole("button", { name: "Back" });
    expect(back.compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const overview = stage.querySelector(".bf-detail-overview") as HTMLElement;
    const play = within(stage).getByRole("button", { name: "Play Server title" });
    expect(overview.compareDocumentPosition(play) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("ends on the generic not-available surface for an unknown or hidden item", async () => {
    writeViewerCache(identity, mediaDetailCacheKey("m1"), { media, fields: [] });
    vi.mocked(fetchMedia).mockResolvedValue(null);
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(card, identity) }]);
    await act(async () => {});

    expect(screen.getByRole("heading", { level: 1, name: "Not available" })).toBeInTheDocument();
    expect(screen.queryByText("Server title")).toBeNull();
    expect(screen.queryByText("Card title")).toBeNull();
    expect(screen.queryByRole("button", { name: /Play/ })).toBeNull();
    expect(readViewerCache(identity, mediaDetailCacheKey("m1"))).toBeUndefined();
  });

  it("offers a retry when the request fails with nothing to show", async () => {
    vi.mocked(fetchMedia).mockRejectedValueOnce(new TypeError("Failed to fetch"));
    renderAt(["/media/m1"]);
    await act(async () => {});

    expect(screen.getByRole("alert")).toHaveTextContent("couldn’t be loaded");
    vi.mocked(fetchMedia).mockResolvedValue(media);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    });

    expect(screen.getByRole("heading", { level: 1, name: "Server title" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("reads the Versions again on Try again, and leaves an all-unknown audio track out of Details", async () => {
    vi.mocked(fetchMedia).mockRejectedValueOnce(new TypeError("Failed to fetch"));
    vi.mocked(fetchMediaTracks).mockRejectedValueOnce(new TypeError("Failed to fetch"));
    renderAt(["/media/m1"]);
    await act(async () => {});
    expect(fetchMediaTracks).toHaveBeenCalledTimes(1);

    vi.mocked(fetchMedia).mockResolvedValue(media);
    vi.mocked(fetchMediaTracks).mockResolvedValue([
      {
        id: "va",
        label: "",
        videoCodec: "h264",
        audioTracks: [{ streamIndex: 1, codec: "", language: "", channels: 0 }],
        captionTracks: [],
      },
    ]);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    });

    expect(fetchMediaTracks).toHaveBeenCalledTimes(2);
    const details = screen.getByRole("heading", { level: 2, name: "Details" }).closest("section");
    const rows = Array.from(
      (details as HTMLElement).querySelectorAll("dt"),
      (dt) => dt.textContent,
    );
    expect(rows).toContain("Video");
    expect(rows).not.toContain("Audio");
  });

  it.each(["m1", "media-sample1-a371bff46385f7ded"])(
    "keeps cached detail for %s when revalidation fails",
    async (id) => {
      writeViewerCache(identity, mediaDetailCacheKey(id), { media: { ...media, id }, fields: [] });
      vi.mocked(fetchMedia).mockRejectedValue(new TypeError("Failed to fetch"));
      const path = `/media/${id}`;
      window.history.replaceState(null, "", path);
      try {
        renderAt([path]);
        await act(async () => {});

        expect(screen.getByRole("heading", { level: 1, name: "Server title" })).toBeInTheDocument();
        expect(screen.queryByRole("alert")).toBeNull();
        expect(fetchMedia).toHaveBeenCalledWith(id, expect.any(AbortSignal));
        expect(fetchMedia).not.toHaveBeenCalledWith(
          "media-sha256-a371bff46385f7ded",
          expect.anything(),
        );
        expect(window.location.pathname).toBe(path);
      } finally {
        window.history.replaceState(null, "", "/");
      }
    },
  );

  it("goes back to the page it came from, or Home on a deep link", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    renderAt(["/n/library-a", "/media/m1"]);
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/n/library-a");

    cleanup();
    renderAt(["/media/m1"]);
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByTestId("path")).toHaveTextContent(/^\/$/);
  });

  it("shows no generic shelf of the row the card came from, only rows about the item (CORE-2507)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        { entityID: "p1", title: "Ada Lead", kind: "person", archetype: "person", relation: "by" },
      ],
    });
    vi.mocked(fetchMediaRelations).mockImplementation(async (_id, relation) =>
      relation === "extra-of" ? [{ id: "x1", kind: "media", title: "Trailer" }] : [],
    );
    vi.mocked(fetchSourceItems).mockResolvedValue([{ id: "a1", kind: "media", title: "Also Ada" }]);
    vi.mocked(fetchRelatedMedia).mockResolvedValue([{ id: "r1", kind: "media", title: "Alike" }]);
    // The viewer came from a cached Continue Watching row holding other items.
    writeViewerCache(identity, sectionCacheKey(shelf, "lib", "", null), [
      card,
      { id: "m2", kind: "media", title: "Second" },
    ]);
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(card, identity, shelf.id) }]);
    await screen.findByRole("heading", { level: 2, name: "Related" });

    expect(screen.queryByText("Keep browsing")).toBeNull();
    expect(screen.queryByText("Second")).toBeNull();
    expect(
      screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent),
    ).toEqual(["People", "Extras", "More with Ada Lead", "Related"]);
  });

  it("links category text to their Entity, two per kind with no +N (CORE-2504, CORE-2930)", async () => {
    const category = (entityID: string, title: string, kind: string) => ({
      entityID,
      title,
      kind,
      kindDisplayName: { en: kind === "g" ? "Genre" : "Maker" },
      archetype: "category",
      relation: "in",
    });
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        category("g1", "Drama", "g"),
        category("g2", "Biography", "g"),
        category("s1", "Universal Pictures", "s"),
        category("s2", "DreamWorks Pictures", "s"),
        category("s3", "Imagine Entertainment", "s"),
      ],
    });
    renderAt(["/media/m1"]);
    await screen.findByRole("heading", { level: 1, name: "Server title" });

    const facts = screen.getByRole("list", { name: "About this item" });
    expect(Array.from(facts.querySelectorAll("li"), (li) => li.textContent)).toEqual(["2001"]);
    const line = document.querySelector(".bf-detail-categories") as HTMLElement;
    expect(line).toHaveTextContent("Drama, Biography · Universal Pictures, DreamWorks Pictures");
    expect(line).not.toHaveTextContent("+");
    const links = within(line).getAllByRole("link");
    expect(links.map((link) => [link.textContent, link.getAttribute("href")])).toEqual([
      ["Drama", "/entity/g1"],
      ["Biography", "/entity/g2"],
      ["Universal Pictures", "/entity/s1"],
      ["DreamWorks Pictures", "/entity/s2"],
    ]);
    // Keyboard-reachable.
    links[2].focus();
    expect(links[2]).toHaveFocus();
    // Details lists every credit of each kind, the overflow included.
    const details = screen.getByRole("heading", { level: 2, name: "Details" }).closest("section");
    expect(
      within(details as HTMLElement).getByRole("link", { name: "Imagine Entertainment" }),
    ).toHaveAttribute("href", "/entity/s3");
    fireEvent.click(links[2]);
    expect(screen.getByTestId("path")).toHaveTextContent("/entity/s1");
  });

  it("lists credits by kind and job and the shown Version's file in Details (CORE-2508)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        {
          entityID: "g1",
          title: "Drama",
          kind: "g",
          kindDisplayName: { en: "Genre" },
          archetype: "category",
          relation: "in",
        },
        {
          entityID: "p1",
          title: "Ada Lead",
          kind: "person",
          archetype: "person",
          relation: "by",
          attributes: { character: "Hero" },
        },
        {
          entityID: "d1",
          title: "Dee Rector",
          kind: "person",
          archetype: "person",
          relation: "by",
          attributes: { job: "Director", department: "Directing" },
        },
        {
          entityID: "w1",
          title: "Wri Ter",
          kind: "person",
          archetype: "person",
          relation: "by",
          attributes: { job: "Screenplay" },
        },
      ],
    });
    vi.mocked(fetchMediaDetailFields).mockResolvedValue([
      { name: "country", type: "text", value: "United States", label: { en: "Country" } },
      { name: "empty", type: "text", value: " ", label: { en: "Empty" } },
    ]);
    vi.mocked(fetchMediaTracks).mockResolvedValue([
      {
        id: "va",
        label: "4K",
        videoCodec: "hevc",
        hdr: "HDR10",
        audioTracks: [
          { streamIndex: 1, codec: "aac", language: "eng", channels: 2 },
          { streamIndex: 2, codec: "eac3", language: "eng", channels: 6, default: true },
        ],
        captionTracks: [],
      },
    ]);
    renderAt(["/media/m1"]);
    await screen.findByText("HEVC · HDR10");

    const details = screen.getByRole("heading", { level: 2, name: "Details" }).closest("section");
    const rows = Array.from((details as HTMLElement).querySelectorAll("dt"), (dt) => [
      dt.textContent,
      dt.nextElementSibling?.textContent,
    ]);
    expect(rows).toEqual([
      ["Genre", "Drama"],
      ["Director", "Dee Rector"],
      ["Screenplay", "Wri Ter"],
      ["Country", "United States"],
      ["Version", "4K"],
      ["Video", "HEVC · HDR10"],
      ["Audio", "English · Dolby Digital Plus · 5.1"],
    ]);
    expect(
      within(details as HTMLElement).getByRole("link", { name: "Dee Rector" }),
    ).toHaveAttribute("href", "/entity/d1");
    // One listing feeds the pickers and Details.
    expect(fetchMediaTracks).toHaveBeenCalledTimes(1);
  });

  it("reserves the stage's space while the item loads (CORE-2509)", async () => {
    vi.mocked(fetchMedia).mockReturnValue(new Promise(() => {}));
    renderAt(["/media/m1"]);
    const skeleton = screen.getByRole("status", { name: "Loading..." });
    const skeletonStage = skeleton.closest(".bf-detail-stage");
    expect(skeletonStage).toHaveAttribute("data-backdrop", "");
    // Back already sits inside the stage, where the loaded page keeps it (CORE-2930).
    expect(skeletonStage?.querySelector(":scope > .bf-detail-back")).not.toBeNull();
    expect(skeleton.querySelector(".bf-detail-meta")).not.toBeNull();
    expect(skeleton.querySelector(".bf-detail-overview")).not.toBeNull();
    cleanup();

    // Painted from the opening card: the backdrop, facts and overview space stays reserved.
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(card, identity) }]);
    const stage = screen.getByRole("heading", { level: 1, name: "Card title" }).closest("section");
    expect(stage).toHaveAttribute("data-backdrop", "");
    expect(stage?.querySelector(".bf-detail-meta")).not.toBeNull();
    expect(stage?.querySelector(".bf-detail-overview")).not.toBeNull();
  });

  it("takes the runtime from the probed media duration over a short reported one (CORE-2314)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({ ...media, durationSeconds: 7260 });
    vi.mocked(fetchMediaDetailFields).mockResolvedValue([]);
    const timed = { ...card, engagement: { durationSeconds: 268 } } as SourceItem;
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(timed, identity) }]);
    await act(async () => {});

    const facts = screen.getByRole("list", { name: "About this item" });
    expect(facts).toHaveTextContent("2 h 1 min");
  });
  it("names an audio item's artists under its title, linked to their pages (CORE-2327)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      mediaClass: "audio",
      credits: [
        { entityID: "p1", title: "Ada", kind: "person", archetype: "person", relation: "by" },
        { entityID: "p2", title: "Bo", kind: "person", archetype: "person", relation: "by" },
      ],
    });
    renderAt(["/media/m1"]);

    const artist = await screen.findByRole("link", { name: "Ada" });
    expect(artist).toHaveAttribute("href", "/entity/p1");
    expect(screen.getByRole("link", { name: "Bo" })).toBeInTheDocument();
    // Names only: no cast row repeats them.
    expect(screen.queryByRole("heading", { level: 2, name: "People" })).toBeNull();
  });

  // Codex finding (CORE-2406 follow-up): the people-credits spec has no audio
  // exception, so an audio drama's cast with headshots and parts gets the row,
  // and the names under the title give way to it rather than repeat it.
  it.each([
    ["a headshot", { artworkSlots: { poster: "/api/entity/artwork/p1/poster?v=1" } }],
    ["a part", { attributes: { character: "The Narrator" } }],
  ])(
    "shows an audio item's cast row when a credit carries %s, naming each person once",
    async (_, extra) => {
      vi.mocked(fetchMedia).mockResolvedValue({
        ...media,
        mediaClass: "audio",
        credits: [
          {
            entityID: "p1",
            title: "Ada",
            kind: "person",
            archetype: "person",
            relation: "by",
            ...extra,
          },
          { entityID: "p2", title: "Bo", kind: "person", archetype: "person", relation: "by" },
        ],
      });
      const { container } = renderAt(["/media/m1"]);

      expect(await screen.findByRole("heading", { level: 2, name: "People" })).toBeInTheDocument();
      expect(container.querySelectorAll('a[href="/entity/p1"]')).toHaveLength(1);
      expect(container.querySelectorAll('a[href="/entity/p2"]')).toHaveLength(1);
      expect(container.querySelector(".bf-detail-people")).toBeNull();
    },
  );

  it("shows only viewer-facing fields and puts the rating in the facts row (CORE-1785)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        { entityID: "g1", title: "Drama", kind: "genre", archetype: "category", relation: "in" },
        { entityID: "p1", title: "A Person", kind: "person", archetype: "person", relation: "by" },
      ],
    });
    vi.mocked(fetchMediaDetailFields).mockResolvedValue([
      { name: "class", type: "text", value: "feature", label: { en: "Class" } },
      { name: "hydrated", type: "flag", value: "true", label: { en: "Grouped Locally" } },
      { name: "index", type: "number", value: "3" },
      { name: "sequence", type: "number", value: "7" },
      { name: "certification", type: "text", value: "PG-13", label: { en: "Certification" } },
      { name: "tagline", type: "text", value: "A line", label: { en: "Tagline" } },
    ]);
    const timed = { ...card, engagement: { durationSeconds: 5400 } } as SourceItem;
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(timed, identity) }]);
    await act(async () => {});

    const facts = screen.getByRole("list", { name: "About this item" });
    expect(Array.from(facts.querySelectorAll("li"), (li) => li.textContent)).toEqual([
      "2001",
      "1 h 30 min",
      "PG-13",
    ]);
    expect(document.querySelector(".bf-detail-categories")).toHaveTextContent(/^Drama$/);
    const details = screen.getByRole("heading", { level: 2, name: "Details" }).closest("section");
    expect(details).toHaveTextContent("Tagline");
    expect(details).toHaveTextContent("A line");
    for (const hidden of ["Class", "Grouped Locally", "index", "sequence", "Certification"]) {
      expect(details).not.toHaveTextContent(hidden);
    }
  });

  it("shows the display rating as a badge and the community score (CORE-2399)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    vi.mocked(fetchMediaDetailFields).mockResolvedValue([
      { name: "certification-display", type: "text", value: "TV-MA", label: { en: "Rating" } },
      { name: "community-rating", type: "number", value: "7.25", label: { en: "TMDB rating" } },
      {
        name: "community-rating-votes",
        type: "number",
        value: "1200",
        label: { en: "TMDB votes" },
      },
    ]);
    renderAt(["/media/m1"]);
    await act(async () => {});

    const facts = screen.getByRole("list", { name: "About this item" });
    const badge = within(facts).getByText("TV-MA");
    expect(badge).toHaveAttribute("data-variant", "rating");
    const score = facts.querySelector('[data-variant="score"]');
    expect(score?.querySelector('[aria-hidden="true"]')).toHaveTextContent("\u2605 7.3");
    expect(score?.querySelector(".sr-only")).toHaveTextContent(
      "TMDB rating 7.3 out of 10, 1,200 votes",
    );
    expect(screen.queryByRole("heading", { level: 2, name: "Details" })).toBeNull();
  });

  it("leaves the Details section out when no field is viewer-facing", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    vi.mocked(fetchMediaDetailFields).mockResolvedValue([
      { name: "index", type: "number", value: "3" },
      { name: "class", type: "text", value: "feature", label: { en: "Class" } },
    ]);
    renderAt(["/media/m1"]);
    await act(async () => {});

    expect(screen.getByRole("heading", { level: 1, name: "Server title" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { level: 2, name: "Details" })).toBeNull();
  });

  it("fills only Play with the accent, at the 44px size", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    const started = { ...card, engagement: { positionSeconds: 60, durationSeconds: 600 } };
    const { container } = renderAt([
      { pathname: "/media/m1", state: mediaLinkState(started as SourceItem, identity) },
    ]);
    await act(async () => {});

    const filled = container.querySelectorAll(".bg-primary");
    expect(filled).toHaveLength(1);
    expect(filled[0]).toBe(screen.getByRole("button", { name: "Resume Server title" }));
    expect(filled[0]).toHaveClass("h-11");
    expect(
      screen.getByRole("button", { name: "Start Server title from the beginning" }),
    ).toHaveClass("h-11");
  });

  it("offers Watch together as a ghost secondary on the home network only", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    renderAt(["/media/m1"]);
    await act(async () => {});
    expect(screen.queryByRole("button", { name: "Watch Server title together" })).toBeNull();
    cleanup();

    resetHomeNetworkProbeForTest();
    vi.mocked(calibrateWatchPartyClock).mockResolvedValue({ offsetMS: 0, roundTripMS: 2 });
    const { container } = renderAt(["/media/m1"]);
    await act(async () => {});
    const together = screen.getByRole("button", { name: "Watch Server title together" });
    expect(together).toHaveClass("h-11");
    expect(container.querySelectorAll(".bg-primary")).toHaveLength(1);
  });

  it("starts over from the beginning with ?t=0 only when a position is known", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    renderAt(["/media/m1"]);
    await act(async () => {});
    expect(screen.queryByRole("button", { name: /from the beginning/ })).toBeNull();

    cleanup();
    const started = { ...card, engagement: { positionSeconds: 60, durationSeconds: 600 } };
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(started as SourceItem, identity) }]);
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", { name: "Start Server title from the beginning" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/watch/m1");
    expect(screen.getByTestId("search")).toHaveTextContent("?t=0");
    expect(watchState()).toEqual({
      title: "Server title",
    });

    // Codex round 2, P2-1: pressing Start over again is another start, so a
    // player already docked on this Media starts afresh with the new picks.
    const first = JSON.parse(screen.getByTestId("state").textContent || "null").startID;
    cleanup();
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(started as SourceItem, identity) }]);
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", { name: "Start Server title from the beginning" }));
    expect(watchState()).toEqual({ title: "Server title" });
    expect(JSON.parse(screen.getByTestId("state").textContent || "null").startID).not.toBe(first);
  });

  it("links the container credit and the grouping above it, and offers Next", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        {
          entityID: "c-inner",
          title: "Part Two",
          kind: "inner",
          kindDisplayName: { en: "Part" },
          archetype: "container",
          relation: "in",
        },
      ],
    });
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      { id: "m0", kind: "media", title: "Before" },
      { id: "m1", kind: "media", title: "Server title" },
      { id: "m2", kind: "media", title: "After" },
    ]);
    vi.mocked(fetchEntity).mockResolvedValue({
      id: "c-inner",
      kind: "inner",
      title: "Part Two",
      fields: [],
      relatedEntities: [
        {
          kind: "outer",
          items: [
            { id: "c-outer", kind: "outer", title: "The Whole", engagement: { totalCount: 9 } },
          ],
        },
        {
          kind: "tag",
          items: [{ id: "t1", kind: "tag", title: "Small", engagement: { totalCount: 2 } }],
        },
      ],
    });
    renderAt(["/media/m1"]);
    await act(async () => {});

    const nav = screen.getByRole("navigation", { name: "Part of" });
    expect(Array.from(nav.querySelectorAll("a"), (a) => a.getAttribute("href"))).toEqual([
      "/entity/c-outer",
      "/entity/c-inner",
    ]);
    expect(fetchMediaForEntity).toHaveBeenCalledWith("c-inner", expect.anything());

    const next = screen.getByRole("link", { name: "Next: After" });
    expect(next).toHaveAttribute("href", "/media/m2");
    fireEvent.click(screen.getByRole("link", { name: "Part Two" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/entity/c-inner");
  });

  it("offers no Next on the last item and keeps the credit link when the list fails", async () => {
    const withCredit = {
      ...media,
      credits: [
        { entityID: "c1", title: "Group", kind: "inner", archetype: "container", relation: "in" },
      ],
    };
    vi.mocked(fetchMedia).mockResolvedValue(withCredit);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      { id: "m0", kind: "media", title: "Before" },
      { id: "m1", kind: "media", title: "Server title" },
    ]);
    renderAt(["/media/m1"]);
    await act(async () => {});
    expect(screen.queryByRole("link", { name: /^Next/ })).toBeNull();
    expect(screen.getByRole("link", { name: "Group" })).toHaveAttribute("href", "/entity/c1");

    cleanup();
    vi.mocked(fetchMediaForEntity).mockRejectedValue(new TypeError("Failed to fetch"));
    renderAt(["/media/m1"]);
    await act(async () => {});
    expect(screen.getByRole("link", { name: "Group" })).toHaveAttribute("href", "/entity/c1");
    expect(screen.queryByRole("link", { name: /^Next/ })).toBeNull();
  });

  it("clamps a long overview behind a More toggle", async () => {
    const long = "A sentence about the item. ".repeat(20).trim();
    vi.mocked(fetchMedia).mockResolvedValue({ ...media, summary: long });
    const { container } = renderAt(["/media/m1"]);
    await act(async () => {});

    const text = container.querySelector(".bf-detail-summary");
    expect(text).toHaveAttribute("data-clamped");
    const toggle = screen.getByRole("button", { name: "More" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(toggle);
    expect(text).not.toHaveAttribute("data-clamped");
    expect(screen.getByRole("button", { name: "Less" })).toHaveAttribute("aria-expanded", "true");
  });

  it("lays the backdrop behind the stage and fades it in only once loaded", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      artworkSlots: { backdrop: "/api/media/artwork/m1/backdrop?v=1" },
    });
    const { container } = renderAt(["/media/m1"]);
    await act(async () => {});

    const img = container.querySelector(".bf-detail-backdrop img");
    expect(img?.getAttribute("src")).toContain("backdrop?v=1&bucket=w1280");
    expect(img).not.toHaveAttribute("data-loaded");
    fireEvent.load(img as Element);
    expect(img).toHaveAttribute("data-loaded");
    expect(container.querySelector(".bf-detail-stage")).toHaveAttribute("data-backdrop");
  });

  it("marks the item watched, watchlists and rates it, and the state survives a reload (CORE-2436)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    renderAt(["/media/m1"]);
    const watched = await screen.findByRole("button", { name: "Mark watched" });
    await waitFor(() => expect(watched).toBeEnabled());

    fireEvent.click(watched);
    await screen.findByRole("button", { name: "Mark unwatched", pressed: true });
    fireEvent.click(screen.getByRole("button", { name: "Add to watchlist" }));
    await screen.findByRole("button", { name: "Remove from watchlist", pressed: true });
    fireEvent.click(screen.getByRole("button", { name: "Rate 4 out of 5" }));
    await screen.findByRole("button", { name: "Clear your rating" });
    expect(mocks.itemCalls).toEqual([
      ["setWatched", { mediaID: "m1" }, true],
      ["setWatchlisted", { mediaID: "m1" }, true],
      ["setRating", { mediaID: "m1" }, 8],
    ]);

    // A fresh page load reads the stored state back.
    cleanup();
    clearViewerCache();
    renderAt(["/media/m1"]);
    expect(
      await screen.findByRole("button", { name: "Mark unwatched", pressed: true }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove from watchlist" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    // The chosen fourth star reads "Clear your rating"; the three below it are filled too.
    expect(screen.getByRole("button", { name: "Clear your rating" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "Rate 3 out of 5" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    // Play stays the one accent-filled action.
    expect(document.querySelectorAll(".bg-primary")).toHaveLength(1);
  });

  it("offers Add to playlist and Copy link from the More menu (CORE-2436)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    renderAt(["/media/m1"]);
    const more = await screen.findByRole("button", { name: "More actions for Server title" });
    fireEvent.click(more);
    expect(await screen.findByRole("menuitem", { name: "Copy link" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: "Add to playlist" }));
    expect(
      await screen.findByRole("dialog", { name: "Add “Server title” to a playlist" }),
    ).toBeInTheDocument();
  });

  it("has no axe violations with the action row loaded (CORE-2436)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        {
          entityID: "p1",
          title: "Lead",
          kind: "person",
          archetype: "person",
          relation: "credited-in",
          attributes: { character: "Hero" },
        },
      ],
    });
    const { container } = renderAt(["/media/m1"]);
    await waitFor(() => expect(screen.getByRole("button", { name: "Mark watched" })).toBeEnabled());
    await expectNoAxeViolations(container);
  });
  it("shows an Extras row whose cards play their item, and no empty Editions row (CORE-2439)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    vi.mocked(fetchMediaRelations).mockImplementation(async (_id, relation) =>
      relation === "extra-of"
        ? [
            { id: "x1", kind: "media", title: "Behind the Lens", mediaClass: "video" },
            { id: "x2", kind: "media", title: "Trailer", mediaClass: "video" },
          ]
        : [],
    );
    renderAt(["/media/m1"]);

    const extras = await screen.findByRole("list", { name: "Extras" });
    expect(vi.mocked(fetchMediaRelations).mock.calls.map((call) => call.slice(0, 2))).toEqual([
      ["m1", "extra-of"],
      ["m1", "edition-of"],
    ]);
    expect(within(extras).getAllByRole("listitem")).toHaveLength(2);
    expect(screen.queryByRole("heading", { name: "Editions" })).not.toBeInTheDocument();

    fireEvent.click(within(extras).getByRole("link", { name: /^Trailer/ }));
    expect(screen.getByTestId("path")).toHaveTextContent("/watch/x2");
    expect(screen.getByTestId("state")).toHaveTextContent('"title":"Trailer"');
  });

  it("shows neither row for an item without extras or editions, nor when the read fails (CORE-2439)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    vi.mocked(fetchMediaRelations).mockImplementation(async (_id, relation) => {
      if (relation === "edition-of") throw new Error("offline");
      return [];
    });
    renderAt(["/media/m1"]);

    await screen.findByRole("heading", { name: "Server title" });
    await waitFor(() => expect(fetchMediaRelations).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("heading", { name: "Extras" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Editions" })).not.toBeInTheDocument();
  });
  const genre = (entityID: string, title: string) => ({
    entityID,
    title,
    kind: "genre",
    archetype: "category",
    relation: "tagged",
  });
  const person = (entityID: string, title: string, role?: string) => ({
    entityID,
    title,
    kind: "person",
    archetype: "person",
    relation: "credited-in",
    ...(role ? { attributes: { role } } : {}),
  });
  const item = (id: string, title: string): SourceItem => ({ id, kind: "media", title });

  it("ends with More with the first billed person and Related titles, without the current item (CORE-2444)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [genre("g1", "Drama"), genre("g2", "Heist"), person("p1", "Ada Lead")],
    });
    vi.mocked(fetchSourceItems).mockResolvedValue([
      item("m1", "Server title"),
      item("a1", "Lead Again"),
    ]);
    // The server ranks Related; the page shows its order.
    vi.mocked(fetchRelatedMedia).mockResolvedValue([
      item("r2", "Both Genres"),
      item("r1", "One Genre"),
    ]);
    renderAt(["/media/m1"]);

    const moreWith = await screen.findByRole("list", { name: "More with Ada Lead" });
    expect(within(moreWith).getAllByRole("listitem")).toHaveLength(1);
    expect(within(moreWith).getByRole("link", { name: /^Lead Again/ })).toBeInTheDocument();

    const related = screen.getByRole("list", { name: "Related" });
    const titles = within(related)
      .getAllByRole("listitem")
      .map((li) => li.textContent);
    expect(titles).toHaveLength(2);
    expect(titles[0]).toContain("Both Genres");
    expect(titles[1]).toContain("One Genre");

    fireEvent.click(within(related).getByRole("link", { name: /^Both Genres/ }));
    expect(screen.queryByTestId("path")).toBeNull();
    await screen.findByRole("heading", { name: "Both Genres" });
  });

  // Review findings P2-1 and P2-4: the rows read through the Mute-suppressed,
  // bounded server reads, never the direct-navigation mediaForEntity list.
  it("reads the closing rows through bounded, Mute-aware server reads (CORE-2444)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [genre("g1", "Drama"), person("p1", "Ada Lead")],
    });
    vi.mocked(fetchSourceItems).mockResolvedValue(
      Array.from({ length: 30 }, (_, i) => item(`a${i}`, `Lead ${i}`)),
    );
    renderAt(["/media/m1"]);

    const moreWith = await screen.findByRole("list", { name: "More with Ada Lead" });
    expect(within(moreWith).getAllByRole("listitem")).toHaveLength(20);
    expect(fetchSourceItems).toHaveBeenCalledTimes(1);
    expect(fetchSourceItems).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: "entity-media",
        entityID: "p1",
        excludeEditionGroupOf: "m1",
        limit: 20,
      }),
      expect.anything(),
    );
    expect(fetchRelatedMedia).toHaveBeenCalledTimes(1);
    expect(fetchRelatedMedia).toHaveBeenCalledWith("m1", 20, expect.anything());
    expect(fetchMediaForEntity).not.toHaveBeenCalled();
  });

  // Review finding P3-6: the lead is the first person credit in server
  // (billing) order, whatever its department.
  it("names More with after the first billed person in server order (CORE-2444)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [person("pb", "Bea Director", "Directing"), person("pa", "Al Actor", "Acting")],
    });
    vi.mocked(fetchSourceItems).mockResolvedValue([item("a1", "Other")]);
    renderAt(["/media/m1"]);

    await screen.findByRole("list", { name: "More with Bea Director" });
    expect(fetchSourceItems).toHaveBeenCalledWith(
      expect.objectContaining({ entityID: "pb" }),
      expect.anything(),
    );
  });

  it("names directors and writers linked to their pages and previews the cast (CORE-2503)", async () => {
    const credit = (entityID: string, title: string, attributes: Record<string, string>) => ({
      ...person(entityID, title),
      attributes,
    });
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        credit("rc", "Russell Crowe", { character: "John Nash" }),
        credit("rh", "Ron Howard", {
          job: "Director, Producer",
          department: "Directing, Production",
        }),
        credit("ag", "Akiva Goldsman", {
          job: "Screenplay, Producer",
          department: "Writing, Production",
        }),
      ],
    });
    vi.mocked(fetchSourceItems).mockResolvedValue([]);
    renderAt(["/media/m1"]);

    await screen.findAllByRole("link", { name: "Ron Howard" });
    // Details (CORE-2504) lists every credit too; these assertions read the crew line.
    const crew = document.querySelector<HTMLElement>(".bf-detail-crew");
    expect(crew).not.toBeNull();
    const director = within(crew as HTMLElement).getByRole("link", { name: "Ron Howard" });
    expect(director).toHaveAttribute("href", "/entity/rh");
    expect(director.closest("div")).toHaveTextContent("DirectorRon Howard");
    const writer = within(crew as HTMLElement).getByRole("link", { name: "Akiva Goldsman" });
    expect(writer).toHaveAttribute("href", "/entity/ag");
    expect(writer.closest("div")).toHaveTextContent("WriterAkiva Goldsman");
    const people = screen.getByRole("list", { name: "People" });
    expect(
      within(people)
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(["RRussell CroweJohn Nash"]);
    expect(screen.getByRole("link", { name: "See all" })).toHaveAttribute("href", "/credits/m1");
  });

  it("hides More with and Related when the reads return nothing else (CORE-2444)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [person("p1", "Ada Lead"), person("p2", "Second")],
    });
    vi.mocked(fetchSourceItems).mockResolvedValue([item("m1", "Server title")]);
    renderAt(["/media/m1"]);

    await screen.findByRole("heading", { name: "Server title" });
    await waitFor(() => expect(fetchRelatedMedia).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("heading", { name: /More with/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Related" })).not.toBeInTheDocument();
  });

  it("ends audio Media with a Sonically similar row in server order (CORE-3075)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({ ...media, mediaClass: "audio" });
    vi.mocked(fetchSimilarMedia).mockResolvedValue([
      item("m1", "Server title"),
      item("s2", "Nearest"),
      item("s1", "Further"),
    ]);
    renderAt(["/media/m1"]);

    const similar = await screen.findByRole("list", { name: "Sonically similar" });
    const titles = within(similar)
      .getAllByRole("listitem")
      .map((li) => li.textContent);
    expect(titles).toHaveLength(2);
    expect(titles[0]).toContain("Nearest");
    expect(titles[1]).toContain("Further");
    expect(fetchSimilarMedia).toHaveBeenCalledWith("m1", 20, expect.anything());
  });

  it("hides Sonically similar without vectors and never asks for non-audio Media (CORE-3075)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({ ...media, mediaClass: "audio" });
    renderAt(["/media/m1"]);
    await screen.findByRole("heading", { name: "Server title" });
    await waitFor(() => expect(fetchSimilarMedia).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("heading", { name: "Sonically similar" })).not.toBeInTheDocument();
    cleanup();
    clearViewerCache();

    vi.mocked(fetchSimilarMedia).mockClear();
    vi.mocked(fetchMedia).mockResolvedValue({ ...media, mediaClass: "video" });
    renderAt(["/media/m1"]);
    await screen.findByRole("heading", { name: "Server title" });
    await waitFor(() => expect(fetchRelatedMedia).toHaveBeenCalled());
    expect(fetchSimilarMedia).not.toHaveBeenCalled();
  });

  it("hides the closing rows when every read fails (CORE-2444)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [genre("g1", "Drama"), person("p1", "Ada Lead")],
    });
    vi.mocked(fetchSourceItems).mockRejectedValue(new TypeError("Failed to fetch"));
    vi.mocked(fetchRelatedMedia).mockRejectedValue(new TypeError("Failed to fetch"));
    renderAt(["/media/m1"]);

    await screen.findByRole("heading", { name: "Server title" });
    await waitFor(() => expect(fetchRelatedMedia).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("heading", { name: /More with/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Related" })).not.toBeInTheDocument();
  });

  describe("an ordered item (CORE-2469)", () => {
    const ordered: GenericMediaInfo = {
      ...media,
      title: "Server title",
      sequence: 2,
      index: 5,
      classLabel: { en: "Chapter" },
      headline: { en: "Arrival" },
      artworkSlots: {
        thumb: "/api/media/artwork/m1/thumb?v=1",
        poster: "/api/media/artwork/m1/poster?v=1",
      },
      credits: [
        {
          entityID: "c-inner",
          title: "Part Two",
          kind: "inner",
          kindDisplayName: { en: "Part" },
          archetype: "container",
          relation: "in",
        },
        person("p1", "Ada Lead"),
      ],
    };
    const inner = [item("m0", "Before"), item("m1", "Server title"), item("m2", "After")];
    // The grouping reports no count: the show's engagement totals are
    // outgoing-only, so the single related container is the grouping.
    const innerEntity = {
      id: "c-inner",
      kind: "inner",
      title: "Part Two",
      fields: [],
      relatedEntities: [
        {
          kind: "outer",
          archetype: "container",
          items: [{ id: "c-outer", kind: "outer", title: "The Whole" }],
        },
      ],
    };

    it("links the grouping, names its place and heads with its own headline over the still", async () => {
      vi.mocked(fetchMedia).mockResolvedValue(ordered);
      vi.mocked(fetchMediaForEntity).mockResolvedValue(inner);
      vi.mocked(fetchEntity).mockResolvedValue(innerEntity as never);
      const { container } = renderAt(["/media/m1"]);
      await act(async () => {});

      const nav = screen.getByRole("navigation", { name: "Part of" });
      expect(Array.from(nav.querySelectorAll("a"), (a) => a.textContent)).toEqual(["The Whole"]);
      const position = container.querySelector(".bf-detail-position");
      expect(position).toHaveTextContent("Part 2 · Chapter 5");
      expect(within(position as HTMLElement).getByRole("link", { name: "Part 2" })).toHaveAttribute(
        "href",
        "/entity/c-inner",
      );
      expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Arrival");
      expect(container.querySelector(".bf-detail-lockup")).toHaveAttribute("data-wide");
      expect(container.querySelector(".bf-detail-poster img")?.getAttribute("src")).toContain(
        "/thumb",
      );
    });

    it("heads with the title without a headline, and with the poster without a still", async () => {
      const { headline: _, artworkSlots: __, ...plain } = ordered;
      vi.mocked(fetchMedia).mockResolvedValue({
        ...plain,
        artworkSlots: { poster: "/api/media/artwork/m1/poster?v=1" },
      });
      const { container } = renderAt(["/media/m1"]);
      await act(async () => {});
      expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Server title");
      expect(container.querySelector(".bf-detail-lockup")).not.toHaveAttribute("data-wide");
      expect(container.querySelector(".bf-detail-poster img")?.getAttribute("src")).toContain(
        "/poster",
      );
    });

    it("shows the air date, resolution, HDR and audio in the facts row", async () => {
      vi.mocked(fetchMedia).mockResolvedValue({ ...ordered, videoWidth: 3840, videoHeight: 2160 });
      vi.mocked(fetchMediaDetailFields).mockResolvedValue([
        { name: "aired", type: "date", value: "2008-01-20", label: { en: "Aired" } },
      ]);
      vi.mocked(fetchMediaTracks).mockResolvedValue([
        {
          id: "va",
          label: "",
          videoCodec: "hevc",
          hdr: "hdr10",
          resolution: "4K",
          audioTracks: [
            { streamIndex: 1, codec: "aac", language: "eng", channels: 2 },
            { streamIndex: 2, codec: "eac3", language: "eng", channels: 6, default: true },
          ],
          captionTracks: [],
        },
      ]);
      renderAt(["/media/m1"]);
      await waitFor(() =>
        expect(screen.getByRole("list", { name: "About this item" }).textContent).toContain(
          "HDR10",
        ),
      );
      const facts = Array.from(
        screen.getByRole("list", { name: "About this item" }).querySelectorAll("li"),
        (li) => li.textContent,
      );
      expect(facts).toEqual(expect.arrayContaining(["4K", "HDR10", "Dolby Digital Plus 5.1"]));
      expect(facts[0]).toMatch(/2008/);
      expect(facts).not.toContain("2001");
      // Each format reads once on the page (CORE-2930).
      expect(screen.getAllByText("4K")).toHaveLength(1);
      expect(screen.getAllByText("HDR10")).toHaveLength(1);
    });

    it("takes the format badges from the Version on screen (CORE-2930)", async () => {
      vi.mocked(fetchMedia).mockResolvedValue(ordered);
      const uhd = {
        id: "va",
        label: "",
        hdr: "hdr10",
        resolution: "4K",
        audioTracks: [{ streamIndex: 1, codec: "aac", channels: 2 }],
        captionTracks: [],
      };
      vi.mocked(fetchMediaTracks).mockResolvedValue([
        uhd,
        { ...uhd, id: "vb", label: "HD", hdr: undefined, resolution: "1080p" },
      ]);
      renderAt(["/media/m1"]);
      fireEvent.change(await screen.findByRole("combobox", { name: "Version" }), {
        target: { value: "vb" },
      });
      const facts = Array.from(
        screen.getByRole("list", { name: "About this item" }).querySelectorAll("li"),
        (li) => li.textContent,
      );
      expect(facts).toContain("1080p");
      expect(facts).not.toContain("4K");
    });

    it("lists the rest of its container in a More in rail that the closing rows leave out", async () => {
      vi.mocked(fetchMedia).mockResolvedValue(ordered);
      vi.mocked(fetchMediaForEntity).mockResolvedValue(inner);
      vi.mocked(fetchEntity).mockResolvedValue(innerEntity as never);
      vi.mocked(fetchSourceItems).mockResolvedValue([
        item("m2", "After"),
        item("a1", "Lead Again"),
      ]);
      renderAt(["/media/m1"]);

      const moreIn = await screen.findByRole("list", { name: "More in Part Two" });
      expect(
        within(moreIn)
          .getAllByRole("listitem")
          .map((li) => li.textContent),
      ).toEqual([expect.stringContaining("Before"), expect.stringContaining("After")]);
      const moreWith = await screen.findByRole("list", { name: "More with Ada Lead" });
      expect(within(moreWith).getAllByRole("listitem")).toHaveLength(1);
      expect(within(moreWith).getByRole("link", { name: /^Lead Again/ })).toBeInTheDocument();
    });

    it("keeps the More in rail for a container of video items", async () => {
      vi.mocked(fetchMedia).mockResolvedValue(ordered);
      vi.mocked(fetchMediaForEntity).mockResolvedValue(
        inner.map((sibling) => ({ ...sibling, mediaClass: "video" })),
      );
      vi.mocked(fetchEntity).mockResolvedValue({ ...innerEntity, archetype: "container" } as never);
      renderAt(["/media/m1"]);

      expect(await screen.findByRole("list", { name: "More in Part Two" })).toBeInTheDocument();
      expect(document.querySelector(".bf-track-list")).toBeNull();
    });
  });

  describe("an item in a numbered list (CORE-3065)", () => {
    const audio = (id: string, title: string, sequence: number, index: number): SourceItem => ({
      id,
      kind: "media",
      title,
      mediaClass: "audio",
      byline: ["Ada Lead"],
      sequence,
      index,
      durationSeconds: 200 + index,
    });
    const listed: GenericMediaInfo = {
      ...media,
      mediaClass: "audio",
      credits: [
        { entityID: "c1", title: "The Set", kind: "set", archetype: "container", relation: "in" },
        person("p1", "Ada Lead"),
      ],
    };
    const siblings = [
      audio("m0", "Opening", 1, 1),
      audio("m1", "Server title", 1, 2),
      audio("m2", "Second Side", 2, 1),
    ];
    const setEntity = {
      id: "c1",
      kind: "set",
      title: "The Set",
      archetype: "container",
      fields: [],
      relatedEntities: [
        {
          kind: "artist",
          archetype: "person",
          items: [{ id: "p1", kind: "a", title: "Ada Lead" }],
        },
      ],
    };

    it("lists its container as the Entity page does, with its own row marked", async () => {
      vi.mocked(fetchMedia).mockResolvedValue(listed);
      vi.mocked(fetchMediaForEntity).mockResolvedValue(siblings);
      vi.mocked(fetchEntity).mockResolvedValue(setEntity as never);
      renderAt(["/media/m1"]);

      const first = await screen.findByRole("list", { name: "Disc 1" });
      const second = screen.getByRole("list", { name: "Disc 2" });
      expect(within(first).getAllByRole("listitem")).toHaveLength(2);
      expect(within(second).getAllByRole("listitem")).toHaveLength(1);
      const current = within(first).getByRole("link", { name: /Server title/ });
      expect(current).toHaveAttribute("aria-current", "page");
      expect(current.closest("li")).toHaveAttribute("data-current");
      expect(within(first).getByRole("link", { name: /Opening/ })).not.toHaveAttribute(
        "aria-current",
      );
      expect(screen.getByRole("heading", { level: 2, name: "The Set" })).toBeInTheDocument();
      expect(screen.queryByRole("list", { name: "More in The Set" })).toBeNull();

      fireEvent.click(within(first).getByRole("button", { name: "Play Opening" }));
      expect(rowQueueIDs()).toEqual(["m0", "m1", "m2"]);
    });

    // ADR-0055 §2.4: a row of a relation-sequenced series plays its book
    // alone, so finishing it never starts the next one.
    it("plays each row of a sequenced series alone", async () => {
      vi.mocked(fetchMedia).mockResolvedValue({
        ...listed,
        credits: [
          { ...listed.credits![0], attributes: { sequence: "2" } },
          person("p1", "Ada Lead"),
        ],
      });
      vi.mocked(fetchMediaForEntity).mockResolvedValue(siblings);
      vi.mocked(fetchEntity).mockResolvedValue(setEntity as never);
      renderAt(["/media/m1"]);

      const first = await screen.findByRole("list", { name: "Disc 1" });
      fireEvent.click(within(first).getByRole("button", { name: "Play Opening" }));
      expect(rowQueueIDs()).toEqual(["m0"]);
    });

    // CORE-3147: an unnumbered member of a series carries no sequence, but
    // its plugin declares it plays alone, so its rows never queue the next.
    it("plays each row alone when the item plays alone without a sequence", async () => {
      vi.mocked(fetchMedia).mockResolvedValue({ ...listed, playsAlone: true });
      vi.mocked(fetchMediaForEntity).mockResolvedValue(siblings);
      vi.mocked(fetchEntity).mockResolvedValue(setEntity as never);
      renderAt(["/media/m1"]);

      const first = await screen.findByRole("list", { name: "Disc 1" });
      fireEvent.click(within(first).getByRole("button", { name: "Play Opening" }));
      expect(rowQueueIDs()).toEqual(["m0"]);
    });
  });

  describe("track pickers before playback (CORE-2442)", () => {
    const main: MediaTrackVersion = {
      id: "va",
      label: "",
      videoCodec: "hevc",
      hdr: "HDR10",
      audioTracks: [
        { streamIndex: 1, codec: "eac3", language: "eng", channels: 6, default: true },
        { streamIndex: 2, codec: "aac", language: "eng", channels: 2, commentary: true },
      ],
      captionTracks: [
        { id: "muxed-main-3", format: "subrip", streamIndex: 3, language: "eng", forced: true },
        { id: "sidecar-main-0", format: "srt", language: "deu", sdh: true },
      ],
    };
    const small: MediaTrackVersion = {
      id: "vb",
      label: "720p",
      videoCodec: "h264",
      audioTracks: [{ streamIndex: 1, codec: "aac", language: "eng", channels: 2 }],
      captionTracks: [],
    };
    const navState = watchState;

    it("lists Versions and tracks without a session and carries picks into playback", async () => {
      mocks.session = { profileID: "p1" };
      vi.mocked(fetchMedia).mockResolvedValue(media);
      vi.mocked(fetchMediaTracks).mockResolvedValue([main, small]);
      const { container } = renderAt(["/media/m1"]);

      const version = await screen.findByRole("combobox", { name: "Version" });
      expect(fetchMediaTracks).toHaveBeenCalledWith("m1", expect.any(AbortSignal));
      expect(
        within(version)
          .getAllByRole("option")
          .map((o) => o.textContent),
      ).toEqual(["Auto", "Default · HEVC · HDR10", "720p · H264"]);
      // Nothing picked or remembered: the server chooses.
      expect(version).toHaveValue("");
      const audio = screen.getByRole("combobox", { name: "Audio" });
      expect(
        within(audio)
          .getAllByRole("option")
          .map((o) => o.textContent),
      ).toEqual([
        "Auto",
        "English · Dolby Digital Plus · 5.1",
        "English · AAC · Stereo · Commentary",
      ]);
      const subtitles = screen.getByRole("combobox", { name: "Subtitles" });
      expect(within(subtitles).getAllByRole("option")).toHaveLength(4);
      await expectNoAxeViolations(container);

      fireEvent.change(audio, { target: { value: "2" } });
      fireEvent.change(screen.getByRole("combobox", { name: "Subtitles" }), {
        target: { value: "sidecar-main-0" },
      });
      // A track pick names the Version on screen, so that Version rides along
      // with it and is remembered.
      expect(readVersionChoice("p1", "m1")).toBe("va");
      expect(screen.getByRole("combobox", { name: "Version" })).toHaveValue("va");

      fireEvent.click(screen.getByRole("button", { name: "Play Server title" }));
      expect(screen.getByTestId("path")).toHaveTextContent("/watch/m1");
      expect(navState()).toEqual({
        title: "Server title",
        tracks: { mediaID: "m1", versionID: "va", audioIndex: 2, captionID: "sidecar-main-0" },
      });
    });

    it("starts a new Version with no track picks and carries Off as null", async () => {
      mocks.session = { profileID: "p1" };
      vi.mocked(fetchMedia).mockResolvedValue(media);
      vi.mocked(fetchMediaTracks).mockResolvedValue([main, small]);
      renderAt(["/media/m1"]);

      fireEvent.change(await screen.findByRole("combobox", { name: "Audio" }), {
        target: { value: "2" },
      });
      fireEvent.change(screen.getByRole("combobox", { name: "Version" }), {
        target: { value: "vb" },
      });
      expect(readVersionChoice("p1", "m1")).toBe("vb");
      // One audio track and no captions: nothing left to pick for that Version.
      expect(screen.queryByRole("combobox", { name: "Audio" })).not.toBeInTheDocument();
      expect(screen.queryByRole("combobox", { name: "Subtitles" })).not.toBeInTheDocument();

      fireEvent.change(screen.getByRole("combobox", { name: "Version" }), {
        target: { value: "va" },
      });
      expect(screen.getByRole("combobox", { name: "Audio" })).toHaveValue("");
      fireEvent.change(screen.getByRole("combobox", { name: "Subtitles" }), {
        target: { value: "off" },
      });
      fireEvent.click(screen.getByRole("button", { name: "Play Server title" }));
      expect(navState()).toEqual({
        title: "Server title",
        tracks: { mediaID: "m1", versionID: "va", captionID: null },
      });
    });

    // Review finding P2-3 and P3-5: the Version rides the nav state with its
    // tracks, so blocked storage cannot split them, and Auto is a choice.
    it("carries the Version into playback even when storage is blocked, and offers Auto", async () => {
      mocks.session = { profileID: "p1" };
      const blocked = () => {
        throw new DOMException("blocked", "SecurityError");
      };
      const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(blocked);
      const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(blocked);
      try {
        vi.mocked(fetchMedia).mockResolvedValue(media);
        vi.mocked(fetchMediaTracks).mockResolvedValue([main, small]);
        const view = renderAt(["/media/m1"]);

        const version = await screen.findByRole("combobox", { name: "Version" });
        expect(version).toHaveValue("");
        // Picking the first Version from Auto is a real pick, not a no-op.
        fireEvent.change(version, { target: { value: "va" } });
        expect(screen.getByRole("combobox", { name: "Version" })).toHaveValue("va");
        fireEvent.click(screen.getByRole("button", { name: "Play Server title" }));
        expect(navState()).toEqual({
          title: "Server title",
          tracks: { mediaID: "m1", versionID: "va" },
        });

        view.unmount();

        // Back to Auto: "" tells the player to leave the Version to the server.
        renderAt(["/media/m1"]);
        fireEvent.change(await screen.findByRole("combobox", { name: "Version" }), {
          target: { value: "vb" },
        });
        fireEvent.change(screen.getByRole("combobox", { name: "Version" }), {
          target: { value: "" },
        });
        expect(screen.getByRole("combobox", { name: "Version" })).toHaveValue("");
        fireEvent.click(screen.getByRole("button", { name: "Play Server title" }));
        expect(navState()).toEqual({
          title: "Server title",
          tracks: { mediaID: "m1", versionID: "" },
        });
      } finally {
        getItem.mockRestore();
        setItem.mockRestore();
      }
    });

    it("shows no pickers without a choice to make, nor when the read fails", async () => {
      vi.mocked(fetchMedia).mockResolvedValue(media);
      vi.mocked(fetchMediaTracks).mockResolvedValue([small]);
      const view = renderAt(["/media/m1"]);
      await screen.findByRole("heading", { name: "Server title" });
      await waitFor(() => expect(fetchMediaTracks).toHaveBeenCalledTimes(1));
      expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
      view.unmount();

      vi.mocked(fetchMediaTracks).mockRejectedValue(new Error("offline"));
      renderAt(["/media/m1"]);
      await screen.findByRole("heading", { name: "Server title" });
      await waitFor(() => expect(fetchMediaTracks).toHaveBeenCalledTimes(2));
      expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Play Server title" }));
      expect(navState()).toEqual({ title: "Server title" });
    });

    it("links a feed entry detail page back to its show and shows availability (CORE-3249)", async () => {
      vi.mocked(fetchMedia).mockResolvedValue({
        ...media,
        id: "m1",
        title: "Episode 1",
        mediaClass: "audio",
        credits: [
          {
            entityID: "show-1",
            title: "Morning Notes",
            kind: "show",
            archetype: "container",
            relation: "member",
          },
        ],
      });
      vi.mocked(fetchMediaForEntity).mockResolvedValue([
        { id: "m1", kind: "media", title: "Episode 1", mediaClass: "audio" },
      ]);
      vi.mocked(fetchEntity).mockResolvedValue({
        id: "show-1",
        kind: "show",
        title: "Morning Notes",
        archetype: "container",
        audioForm: "feed",
        subscriptionID: "sub-1",
        fields: [],
      });
      vi.mocked(fetchSubscriptionEntries).mockResolvedValue([
        {
          mediaID: "m1",
          title: "Episode 1",
          feedPosition: 0,
          enclosureLength: 25_000_000,
          downloadState: "none",
          availability: "not-downloaded",
          played: false,
        },
      ]);

      renderAt(["/media/m1"]);
      expect(await screen.findByRole("link", { name: "Morning Notes" })).toHaveAttribute(
        "href",
        "/entity/show-1",
      );
      expect(await screen.findByText("Not downloaded")).toBeInTheDocument();
    });

    it("shows resume progress and Start over for a standalone audiobook opened by direct link (CORE-3148)", async () => {
      vi.mocked(fetchMedia).mockResolvedValue({
        ...media,
        id: "m1",
        title: "Solo Book",
        mediaClass: "audio",
        playsAlone: true,
        libraryID: "lib-books",
        credits: [],
        durationSeconds: 3600,
      });
      vi.mocked(fetchSourceItems).mockResolvedValue([
        {
          id: "m1",
          kind: "media",
          title: "Solo Book",
          mediaClass: "audio",
          engagement: { positionSeconds: 1800, durationSeconds: 3600, completed: false },
        },
      ]);

      renderAt(["/media/m1"]);

      await waitFor(() =>
        expect(fetchSourceItems).toHaveBeenCalledWith(
          expect.objectContaining({
            kind: "resume-progress",
            value: "in-progress",
            libraryID: "lib-books",
          }),
          expect.any(AbortSignal),
        ),
      );
      expect(await screen.findByRole("button", { name: "Resume Solo Book" })).toHaveTextContent(
        "Resume · 30 minutes left",
      );
      expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "50");
      expect(
        screen.getByRole("button", { name: "Start Solo Book from the beginning" }),
      ).toBeInTheDocument();

      fireEvent.click(screen.getByRole("button", { name: "Mark played" }));
      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Play Solo Book" })).toHaveTextContent("Play"),
      );
      expect(screen.queryByRole("progressbar")).toBeNull();
    });
  });
});
