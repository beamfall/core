import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GenericMediaInfo, Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache, readViewerCache, writeViewerCache } from "@/lib/viewerCache";
import { sectionCacheKey } from "../components/HydratedSection";
import {
  fetchMedia,
  fetchMediaDetailFields,
  mediaDetailCacheKey,
  mediaLinkState,
} from "../lib/mediaDetail";
import { MediaDetailPage } from "./MediaDetailPage";

const mocks = vi.hoisted(() => ({ identity: null as string | null }));

vi.mock("../lib/mediaDetail", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/mediaDetail")>()),
  fetchMedia: vi.fn(),
  fetchMediaDetailFields: vi.fn(),
}));
vi.mock("@/lib/session", () => ({ useViewerIdentity: () => mocks.identity }));
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
          <Route path="/watch/:mediaId" element={<Probe />} />
          <Route path="/media/:mediaId" element={<MediaDetailPage />} />
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
});

afterEach(() => {
  cleanup();
  clearViewerCache();
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
    expect(screen.getByText("rating")).toBeInTheDocument();
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

    const rail = screen.getByRole("list", { name: "More from Recent" });
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
});
