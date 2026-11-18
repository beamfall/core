import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { calibrateWatchPartyClock, fetchEntity, fetchMediaForEntity } from "@/lib/graphql";
import type { GenericMediaInfo, Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache, readViewerCache, writeViewerCache } from "@/lib/viewerCache";
import { expectNoAxeViolations } from "@/test/axe";
import { sectionCacheKey } from "../components/HydratedSection";
import {
  fetchMedia,
  fetchMediaDetailFields,
  fetchMediaRelations,
  mediaDetailCacheKey,
  mediaLinkState,
} from "../lib/mediaDetail";
import { resetHomeNetworkProbeForTest } from "../lib/watchTogether";
import { MediaDetailPage } from "./MediaDetailPage";

const mocks = vi.hoisted(() => ({
  identity: null as string | null,
  // The server's item state for the page's Media (CORE-2436); it outlives a render.
  itemState: { id: "m1", kind: "media", watched: false, watchlisted: false, rating: 0 },
  itemCalls: [] as Array<[string, unknown, unknown]>,
}));

vi.mock("../lib/mediaDetail", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/mediaDetail")>()),
  fetchMedia: vi.fn(),
  fetchMediaDetailFields: vi.fn(),
  fetchMediaRelations: vi.fn(),
}));
vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fetchEntity: vi.fn(),
  fetchMediaForEntity: vi.fn(),
  calibrateWatchPartyClock: vi.fn(),
  fetchItemState: vi.fn(async () => mocks.itemState),
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
  useSession: () => ({ manifest: null, revealed: false }),
}));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <main>{children}</main>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
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

function renderAt(entries: Array<string | { pathname: string; state?: unknown }>) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
        <Routes>
          <Route path="/" element={<Probe />} />
          <Route path="/n/:id" element={<Probe />} />
          <Route path="/watch/*" element={<Probe />} />
          <Route path="/entity/:id" element={<Probe />} />
          <Route path="/media/*" element={<MediaDetailPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
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
  vi.mocked(fetchMedia).mockReset();
  vi.mocked(fetchMediaDetailFields).mockReset().mockResolvedValue([]);
  vi.mocked(fetchMediaRelations).mockReset().mockResolvedValue([]);
  vi.mocked(fetchMediaForEntity).mockReset().mockResolvedValue([]);
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
});

describe("MediaDetailPage", () => {
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

  it("paints the opening card at once and plays the item", async () => {
    vi.mocked(fetchMedia).mockReturnValue(new Promise(() => {}));
    const { container } = renderAt([
      { pathname: "/media/m1", state: mediaLinkState(card, identity) },
    ]);

    expect(screen.getByRole("heading", { level: 1, name: "Card title" })).toBeInTheDocument();
    expect(container.querySelector(".bf-detail-poster img")?.getAttribute("src")).toContain(
      "/api/media/artwork/m1/poster",
    );

    fireEvent.click(screen.getByRole("button", { name: "Play Card title" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/watch/m1");
    expect(JSON.parse(screen.getByTestId("state").textContent || "null")).toEqual({
      title: "Card title",
    });
  });

  it("forwards the opening card's queue to the player, only for its own identity (CORE-1143)", () => {
    vi.mocked(fetchMedia).mockReturnValue(new Promise(() => {}));
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

    fireEvent.click(screen.getByRole("button", { name: "Play Card title" }));
    expect(JSON.parse(screen.getByTestId("state").textContent || "null")).toEqual({
      title: "Card title",
      queue,
      queueIdentity: identity,
    });
  });

  it("drops a queue listed for another identity", () => {
    vi.mocked(fetchMedia).mockReturnValue(new Promise(() => {}));
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

    fireEvent.click(screen.getByRole("button", { name: "Play Card title" }));
    expect(JSON.parse(screen.getByTestId("state").textContent || "null")).toEqual({
      title: "Card title",
    });
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
    expect(screen.getByRole("button", { name: "Resume Server title" })).toHaveTextContent("Resume");
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

  it("keeps a cached detail when revalidation fails", async () => {
    writeViewerCache(identity, mediaDetailCacheKey("m1"), { media, fields: [] });
    vi.mocked(fetchMedia).mockRejectedValue(new TypeError("Failed to fetch"));
    renderAt(["/media/m1"]);
    await act(async () => {});

    expect(screen.getByRole("heading", { level: 1, name: "Server title" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

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

  it("shows the rest of the cached section the card came from, which open their own pages", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    writeViewerCache(identity, sectionCacheKey(shelf, "lib", "", null), [
      card,
      { id: "m2", kind: "media", title: "Second" },
    ]);
    renderAt([{ pathname: "/media/m1", state: mediaLinkState(card, identity, shelf.id) }]);
    await act(async () => {});

    // Titled neutrally, not after the source row (CORE-2320).
    expect(screen.queryByText(/More from Recent/)).not.toBeInTheDocument();
    const rail = screen.getByRole("list", { name: "Keep browsing" });
    expect(rail).toHaveTextContent("Second");
    expect(rail).not.toHaveTextContent("Card title");

    vi.mocked(fetchMedia).mockReturnValue(new Promise(() => {}));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Second" }));
    });
    // The previous item's server detail never paints under the new id.
    expect(screen.getByRole("heading", { level: 1, name: "Second" })).toBeInTheDocument();
    expect(screen.queryByText("Server summary")).toBeNull();
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
  });

  it("names an audio item's people once, with no cast row even when they carry photos", async () => {
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
          attributes: { role: "Vocals" },
          artworkSlots: { poster: "/api/entity/artwork/p1/poster?v=1" },
        },
      ],
    });
    renderAt(["/media/m1"]);

    expect(await screen.findAllByRole("link", { name: "Ada" })).toHaveLength(1);
    expect(screen.queryByRole("heading", { level: 2, name: "People" })).toBeNull();
  });

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
      "Drama",
    ]);
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
    expect(JSON.parse(screen.getByTestId("state").textContent || "null")).toEqual({
      title: "Server title",
    });
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

    fireEvent.click(within(extras).getByRole("button", { name: /^Trailer/ }));
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
  it("ends with More with the first billed person and Related titles, without the current item (CORE-2444)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        {
          entityID: "g1",
          title: "Drama",
          kind: "genre",
          archetype: "category",
          relation: "tagged",
        },
        {
          entityID: "g2",
          title: "Heist",
          kind: "genre",
          archetype: "category",
          relation: "tagged",
        },
        {
          entityID: "p1",
          title: "Ada Lead",
          kind: "person",
          archetype: "person",
          relation: "credited-in",
        },
      ],
    });
    const item = (id: string, title: string): SourceItem => ({ id, kind: "media", title });
    vi.mocked(fetchMediaForEntity).mockImplementation(async (entityID) => {
      if (entityID === "p1") return [item("m1", "Server title"), item("a1", "Lead Again")];
      if (entityID === "g1")
        return [item("r1", "One Genre"), item("r2", "Both Genres"), item("m1", "Server title")];
      if (entityID === "g2") return [item("r2", "Both Genres")];
      return [];
    });
    renderAt(["/media/m1"]);

    const moreWith = await screen.findByRole("list", { name: "More with Ada Lead" });
    expect(within(moreWith).getAllByRole("listitem")).toHaveLength(1);
    expect(within(moreWith).getByRole("button", { name: /^Lead Again/ })).toBeInTheDocument();

    const related = screen.getByRole("list", { name: "Related" });
    const titles = within(related)
      .getAllByRole("listitem")
      .map((li) => li.textContent);
    expect(titles).toHaveLength(2);
    expect(titles[0]).toContain("Both Genres");
    expect(titles[1]).toContain("One Genre");

    fireEvent.click(within(related).getByRole("button", { name: /^Both Genres/ }));
    expect(screen.queryByTestId("path")).toBeNull();
    await screen.findByRole("heading", { name: "Both Genres" });
  });

  it("hides More with and Related when the lead has no other titles and there are no categories (CORE-2444)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        {
          entityID: "p1",
          title: "Ada Lead",
          kind: "person",
          archetype: "person",
          relation: "credited-in",
        },
        {
          entityID: "p2",
          title: "Second",
          kind: "person",
          archetype: "person",
          relation: "credited-in",
        },
      ],
    });
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      { id: "m1", kind: "media", title: "Server title" },
    ]);
    renderAt(["/media/m1"]);

    await screen.findByRole("heading", { name: "Server title" });
    await waitFor(() => expect(fetchMediaForEntity).toHaveBeenCalledWith("p1", expect.anything()));
    // Without a category nothing can be Related, so only the lead is read.
    expect(fetchMediaForEntity).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("heading", { name: /More with/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Related" })).not.toBeInTheDocument();
  });

  it("hides the closing rows when every read fails (CORE-2444)", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      ...media,
      credits: [
        {
          entityID: "g1",
          title: "Drama",
          kind: "genre",
          archetype: "category",
          relation: "tagged",
        },
        {
          entityID: "p1",
          title: "Ada Lead",
          kind: "person",
          archetype: "person",
          relation: "credited-in",
        },
      ],
    });
    vi.mocked(fetchMediaForEntity).mockRejectedValue(new TypeError("Failed to fetch"));
    renderAt(["/media/m1"]);

    await screen.findByRole("heading", { name: "Server title" });
    await waitFor(() => expect(fetchMediaForEntity).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("heading", { name: /More with/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Related" })).not.toBeInTheDocument();
  });
});
