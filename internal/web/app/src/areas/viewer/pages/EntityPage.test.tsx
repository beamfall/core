import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation, useParams } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchEntity, fetchMediaForEntity } from "@/lib/graphql";
import { fetchMediaRows, type MediaRow } from "@/lib/graphql/browse";
import type { EntityInfo, Layout, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache, readViewerCache, writeViewerCache } from "@/lib/viewerCache";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { sectionCacheKey } from "../components/HydratedSection";
import { entityLinkState } from "../lib/entityPreview";
import { EntityPage } from "./EntityPage";

const mocks = vi.hoisted(() => ({
  identity: null as string | null,
  layouts: [] as Layout[],
}));

vi.mock("@/lib/graphql", () => ({
  fetchEntity: vi.fn(),
  fetchMediaForEntity: vi.fn(),
}));
vi.mock("@/lib/graphql/browse", () => ({ fetchMediaRows: vi.fn() }));
vi.mock("@/lib/session", () => ({
  useSession: () => ({ manifest: { layouts: mocks.layouts } }),
  useViewerIdentity: () => mocks.identity,
}));
vi.mock("../player/preload", () => ({ preloadPlayer: vi.fn() }));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <main>{children}</main>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("../actions", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../actions")>()),
  ItemActions: () => null,
}));
vi.mock("../components/NotAvailable", () => ({ NotAvailable: () => <p>not available</p> }));
vi.mock("../components/MediaItemCard", () => ({
  MediaItemCard: ({ item }: { item: SourceItem }) => <li>{item.title}</li>,
}));
vi.mock("../components/LayoutView", () => ({
  LayoutView: ({ layout }: { layout: Layout }) => <p>detail layout {layout.id}</p>,
}));

const identity = JSON.stringify(["viewer", "p1", "adult", false]);
const revealedIdentity = JSON.stringify(["viewer", "p1", "adult", true]);
const entity: EntityInfo = {
  id: "e1",
  kind: "Series",
  title: "Server title",
  fields: [{ name: "Year", type: "string", value: "2001", label: { en: "Year" } }],
};
const related: SourceItem = {
  id: "m1",
  kind: "media",
  title: "Related item",
  artwork: { poster: "" },
};

function PlayerProbe() {
  const { mediaId } = useParams();
  const location = useLocation();
  return (
    <output aria-label="player">
      {mediaId} {JSON.stringify(location.state)}
    </output>
  );
}

function renderEntity(state?: unknown) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[{ pathname: "/entity/e1", state }]}>
        <Routes>
          <Route path="/entity/:entityId" element={<EntityPage />} />
          <Route path="/watch/:mediaId" element={<PlayerProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

function pending<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

beforeEach(() => {
  mocks.identity = identity;
  mocks.layouts = [];
  vi.mocked(fetchEntity).mockReset();
  vi.mocked(fetchMediaRows).mockReset().mockResolvedValue({});
  vi.mocked(fetchMediaForEntity)
    .mockReset()
    .mockReturnValue(new Promise(() => {}));
});

afterEach(() => {
  cleanup();
  clearViewerCache();
});

describe("EntityPage instant paint", () => {
  it("paints the title and kind from identity-matched navigation state while the entity loads", async () => {
    const request = pending<EntityInfo | null>();
    vi.mocked(fetchEntity).mockReturnValue(request.promise);

    renderEntity(entityLinkState({ id: "e1", title: "Card title", kind: "Series" }, identity));

    expect(screen.getByRole("heading", { name: "Card title" })).toBeInTheDocument();
    expect(screen.getByText("Series")).toBeInTheDocument();
    expect(screen.queryByText("Year")).toBeNull();

    await act(async () => request.resolve(entity));
    expect(screen.getByRole("heading", { name: "Server title" })).toBeInTheDocument();
    expect(screen.getByText("2001")).toBeInTheDocument();
  });

  it("ignores navigation state recorded under another identity or for another entity", () => {
    vi.mocked(fetchEntity).mockReturnValue(new Promise(() => {}));

    renderEntity(entityLinkState({ id: "e1", title: "Revealed title" }, revealedIdentity));
    expect(screen.queryByText("Revealed title")).toBeNull();
    cleanup();

    renderEntity(entityLinkState({ id: "e2", title: "Other title" }, identity));
    expect(screen.queryByText("Other title")).toBeNull();
    expect(screen.queryByRole("heading")).toBeNull();
  });

  it("paints the title of an item already hydrated into a section for this identity", () => {
    vi.mocked(fetchEntity).mockReturnValue(new Promise(() => {}));
    const section = { id: "s", kind: "shelf", title: { en: "s" }, artworkSlot: "poster" };
    writeViewerCache(identity, sectionCacheKey(section as never, "lib", "", null), [
      { id: "e1", kind: "entity", title: "Shelf title", artwork: { poster: "" } },
    ]);

    renderEntity();
    expect(screen.getByRole("heading", { name: "Shelf title" })).toBeInTheDocument();
  });

  it("renders a cached entity at once and revalidates it in the background", async () => {
    writeViewerCache(identity, "entity:e1", entity);
    const request = pending<EntityInfo | null>();
    vi.mocked(fetchEntity).mockReturnValue(request.promise);

    renderEntity();
    expect(screen.getByRole("heading", { name: "Server title" })).toBeInTheDocument();
    expect(fetchEntity).toHaveBeenCalledTimes(1);

    const renamed = { ...entity, title: "Renamed" };
    await act(async () => request.resolve(renamed));
    expect(screen.getByRole("heading", { name: "Renamed" })).toBeInTheDocument();
    expect(readViewerCache(identity, "entity:e1")).toEqual(renamed);
  });

  it("drops the preview and the cached entity when the entity resolves as not available", async () => {
    writeViewerCache(identity, "entity:e1", entity);
    const request = pending<EntityInfo | null>();
    vi.mocked(fetchEntity).mockReturnValue(request.promise);

    renderEntity(entityLinkState({ id: "e1", title: "Card title" }, identity));
    await act(async () => request.resolve(null));

    expect(screen.getByText("not available")).toBeInTheDocument();
    expect(screen.queryByRole("heading")).toBeNull();
    expect(readViewerCache(identity, "entity:e1")).toBeUndefined();
  });

  it("starts the related-media request alongside the entity request when no detail Layout exists", async () => {
    const request = pending<EntityInfo | null>();
    vi.mocked(fetchEntity).mockReturnValue(request.promise);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([related]);

    renderEntity(entityLinkState({ id: "e1", title: "Card title" }, identity));

    expect(fetchEntity).toHaveBeenCalledTimes(1);
    expect(fetchMediaForEntity).toHaveBeenCalledTimes(1);
    expect(await screen.findByText("Related item")).toBeInTheDocument();
  });

  it("starts the detail Layout from the previewed kind and lists no Media beside it", async () => {
    mocks.layouts = [{ id: "series-detail", sections: [], role: "detail", entityKind: "Series" }];
    vi.mocked(fetchEntity).mockReturnValue(new Promise(() => {}));
    vi.mocked(fetchMediaForEntity).mockResolvedValue([related]);

    renderEntity(entityLinkState({ id: "e1", title: "Card title", kind: "Series" }, identity));
    await act(async () => {});

    expect(screen.getByText("detail layout series-detail")).toBeInTheDocument();
    // The Media are read for the primary action only; the Layout renders them.
    expect(screen.queryByText("Related item")).toBeNull();
  });

  it("aborts both requests when the page unmounts", () => {
    vi.mocked(fetchEntity).mockReturnValue(new Promise(() => {}));
    const view = renderEntity();
    const entitySignal = vi.mocked(fetchEntity).mock.calls[0][1];
    const mediaSignal = vi.mocked(fetchMediaForEntity).mock.calls[0][1];
    expect(entitySignal?.aborted).toBe(false);
    expect(mediaSignal?.aborted).toBe(false);

    view.unmount();

    expect(entitySignal?.aborted).toBe(true);
    expect(mediaSignal?.aborted).toBe(true);
  });

  it("reloads the related list when engagement changes", async () => {
    vi.mocked(fetchEntity).mockReturnValue(new Promise(() => {}));
    vi.mocked(fetchMediaForEntity).mockResolvedValue([related]);
    renderEntity(entityLinkState({ id: "e1", title: "Card title" }, identity));
    expect(await screen.findByText("Related item")).toBeInTheDocument();

    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      { ...related, title: "Related item, Finished" },
    ]);
    act(() => notifyEngagementChanged());

    expect(await screen.findByText("Related item, Finished")).toBeInTheDocument();
    expect(fetchMediaForEntity).toHaveBeenCalledTimes(2);
  });
});

describe("EntityPage related Entities", () => {
  const withGroups: EntityInfo = {
    ...entity,
    kind: "rack",
    relatedEntities: [
      {
        kind: "box",
        kindDisplayName: { en: "Boxes" },
        items: [{ id: "e2", kind: "entity", title: "Linked box", artwork: { poster: "" } }],
      },
      {
        kind: "place",
        items: [{ id: "e3", kind: "entity", title: "Linked place", artwork: { poster: "" } }],
      },
    ],
  };

  it("renders each related group under its display name before the Media list", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(withGroups);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([related]);

    renderEntity();

    const media = await screen.findByText("Related item");
    expect(screen.getByRole("heading", { name: "Boxes" })).toBeInTheDocument();
    // A group without a declared display name falls back to the kind.
    expect(screen.getByRole("heading", { name: "place" })).toBeInTheDocument();
    const box = screen.getByText("Linked box");
    expect(box.compareDocumentPosition(media) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("renders related groups when the Entity has no direct Media", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(withGroups);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([]);

    renderEntity();

    expect(await screen.findByText("Linked box")).toBeInTheDocument();
    expect(screen.getByText("Linked place")).toBeInTheDocument();
  });

  it("reloads related groups when engagement changes", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(withGroups);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([]);
    renderEntity();
    expect(await screen.findByText("Linked box")).toBeInTheDocument();

    const [boxGroup, placeGroup] = withGroups.relatedEntities ?? [];
    vi.mocked(fetchEntity).mockResolvedValue({
      ...withGroups,
      relatedEntities: [
        { ...boxGroup, items: [{ ...boxGroup.items[0], title: "Linked box, Finished" }] },
        placeGroup,
      ],
    });
    act(() => notifyEngagementChanged());

    expect(await screen.findByText("Linked box, Finished")).toBeInTheDocument();
    expect(fetchEntity).toHaveBeenCalledTimes(2);
  });

  it("renders related groups before a detail Layout", async () => {
    mocks.layouts = [{ id: "rack-detail", sections: [], role: "detail", entityKind: "rack" }];
    vi.mocked(fetchEntity).mockResolvedValue(withGroups);

    renderEntity();

    const box = await screen.findByText("Linked box");
    const layout = screen.getByText("detail layout rack-detail");
    expect(box.compareDocumentPosition(layout) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

// feature:container-entity-page (CORE-1786)
describe("EntityPage container lockup", () => {
  const child = (id: string, title: string, engagement?: SourceItem["engagement"]): SourceItem => ({
    id,
    kind: "media",
    title,
    artworkSlots: { thumb: `/art/${id}` },
    engagement,
  });
  const children = [
    child("c1", "First part", { durationSeconds: 2700, completed: true }),
    child("c2", "Second part", { positionSeconds: 600, durationSeconds: 3000 }),
    child("c3", "Third part", { durationSeconds: 5400 }),
  ];
  const container: EntityInfo = {
    id: "e1",
    kind: "set",
    kindDisplayName: { en: "Collection" },
    title: "Server title",
    fields: [
      { name: "studio", type: "text", value: "North", label: { en: "Studio" } },
      { name: "sort_index", type: "text", value: "7" },
    ],
  };

  it("shows a lockup with the plugin's kind label and viewer-facing fields only", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(container);
    vi.mocked(fetchMediaForEntity).mockResolvedValue(children);
    renderEntity();
    await act(async () => {});

    const facts = screen.getByRole("list", { name: "About this item" });
    expect(within(facts).getByText("Collection")).toBeInTheDocument();
    expect(screen.getByText("Studio")).toBeInTheDocument();
    expect(screen.queryByText("sort_index")).toBeNull();
    expect(screen.queryByText("7")).toBeNull();
  });

  it("offers one accent-filled action that resumes the next-up child with the container as queue", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(container);
    vi.mocked(fetchMediaForEntity).mockResolvedValue(children);
    const { container: root } = renderEntity();
    await act(async () => {});

    const filled = root.querySelectorAll(".bg-primary");
    expect(filled).toHaveLength(1);
    const resume = screen.getByRole("button", { name: "Resume Second part" });
    expect(filled[0]).toBe(resume);
    expect(resume).toHaveClass("h-11");
    expect(screen.getByText("Next up: Second part")).toBeInTheDocument();

    fireEvent.click(resume);
    const player = screen.getByRole("status", { name: "player" }).textContent ?? "";
    expect(player.startsWith("c2 ")).toBe(true);
    const state = JSON.parse(player.slice(3));
    expect(state.title).toBe("Second part");
    expect(state.queueIdentity).toBe(identity);
    expect(state.queue.map((item: { id: string }) => item.id)).toEqual(["c1", "c2", "c3"]);
  });

  it("plays the first unfinished child when none is in progress", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(container);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      children[0],
      child("c3", "Third part", { durationSeconds: 5400 }),
    ]);
    renderEntity();
    await act(async () => {});
    expect(screen.getByRole("button", { name: "Play Third part" })).toHaveTextContent("Play");
  });

  it("lists the children as numbered links in order with runtime, progress and watched state", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(container);
    vi.mocked(fetchMediaForEntity).mockResolvedValue(children);
    renderEntity();
    await act(async () => {});

    const list = screen.getByRole("list", { name: "In Server title" });
    expect(list.tagName).toBe("OL");
    const links = within(list).getAllByRole("link");
    // One link per child in document order, none with a positive tabindex, so
    // keyboard focus follows the visual order.
    expect(links.map((link) => link.getAttribute("href"))).toEqual([
      "/media/c1",
      "/media/c2",
      "/media/c3",
    ]);
    for (const link of links) expect(link.getAttribute("tabindex")).toBeNull();
    expect(links[0]).toHaveTextContent("1First part45 minWatched");
    expect(links[2]).toHaveTextContent("3Third part1 h 30 min");
    expect(within(links[1]).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "20");
    expect(links[0].querySelector("img")?.getAttribute("src")).toBe("/art/c1?bucket=w320");
  });

  it("groups children by kind in first-seen order", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(container);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      children[0],
      { ...children[1], kind: "extra" },
      children[2],
    ]);
    renderEntity();
    await act(async () => {});

    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings).toEqual(["media", "extra"]);
    expect(
      within(screen.getByRole("list", { name: "media" }))
        .getAllByRole("link")
        .map((link) => link.getAttribute("href")),
    ).toEqual(["/media/c1", "/media/c3"]);
  });

  it("plays on through the next-up child container when it has no Media of its own", async () => {
    mocks.layouts = [{ id: "set-detail", sections: [], role: "detail", entityKind: "set" }];
    const box = (id: string, remainingCount: number): SourceItem => ({
      id,
      kind: "entity",
      title: id,
      engagement: { totalCount: 3, remainingCount },
    });
    vi.mocked(fetchEntity).mockResolvedValue({
      ...container,
      relatedEntities: [
        { kind: "member", archetype: "person", items: [box("p1", 1)] },
        { kind: "part", archetype: "container", items: [box("b1", 0), box("b2", 2)] },
      ],
    });
    vi.mocked(fetchMediaForEntity).mockImplementation(async (id: string) =>
      id === "b2" ? children : [],
    );
    const { container: root } = renderEntity();
    await act(async () => {});
    await act(async () => {});

    expect(fetchMediaForEntity).toHaveBeenCalledWith("b2", expect.any(AbortSignal));
    expect(root.querySelectorAll(".bg-primary")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Resume Second part" }));
    const player = screen.getByRole("status", { name: "player" }).textContent ?? "";
    const state = JSON.parse(player.slice(3));
    expect(state.queue.map((item: { id: string }) => item.id)).toEqual(["c1", "c2", "c3"]);
  });

  it("has no primary action when nothing in the container can play", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(container);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([]);
    const { container: root } = renderEntity();
    await act(async () => {});
    expect(root.querySelectorAll(".bg-primary")).toHaveLength(0);
  });
});

describe("EntityPage audio container order", () => {
  // Server order puts the second disc's track first; the list and both play
  // entry points follow the stored disc and track positions instead.
  const record: EntityInfo = { ...entity, kind: "record", archetype: "container" } as EntityInfo;
  const tracks: SourceItem[] = [
    { id: "d2t1", kind: "media", title: "Second Disc Opener" },
    { id: "d1t1", kind: "media", title: "First Disc Opener" },
    { id: "d1t2", kind: "media", title: "First Disc Closer" },
  ];
  const row = (id: string, title: string, sequence: number, index: number): MediaRow => ({
    id,
    title,
    favorited: false,
    mediaClass: "audio",
    credits: [],
    sequence,
    index,
  });

  beforeEach(() => {
    vi.mocked(fetchEntity).mockResolvedValue(record);
    vi.mocked(fetchMediaForEntity).mockResolvedValue(tracks);
    vi.mocked(fetchMediaRows).mockResolvedValue({
      d2t1: row("d2t1", "Second Disc Opener", 2, 1),
      d1t1: row("d1t1", "First Disc Opener", 1, 1),
      d1t2: row("d1t2", "First Disc Closer", 1, 2),
    });
  });

  function playerState() {
    const player = screen.getByRole("status", { name: "player" }).textContent ?? "";
    const [mediaId] = player.split(" ", 1);
    const state = JSON.parse(player.slice(mediaId.length + 1));
    return { mediaId, queue: state.queue.map((item: { id: string }) => item.id) };
  }

  it("starts the primary Play action on the first listed track with the listed order queued", async () => {
    const { container: root } = renderEntity();
    await act(async () => {});
    await act(async () => {});

    expect(fetchMediaRows).toHaveBeenCalledWith(["d2t1", "d1t1", "d1t2"], expect.any(AbortSignal), {
      positions: true,
    });
    const primary = root.querySelector(".bg-primary") as HTMLElement;
    expect(primary).toHaveAccessibleName("Play First Disc Opener");
    fireEvent.click(primary);
    expect(playerState()).toEqual({ mediaId: "d1t1", queue: ["d1t1", "d1t2", "d2t1"] });
  });

  it("plays a row with the same listed order queued", async () => {
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    expect(screen.getByRole("list", { name: "Disc 1" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Play Second Disc Opener" }));
    expect(playerState()).toEqual({ mediaId: "d2t1", queue: ["d1t1", "d1t2", "d2t1"] });
  });

  it("offers no primary Play action until the order is known", async () => {
    vi.mocked(fetchMediaRows).mockReturnValue(new Promise(() => {}));
    const { container: root } = renderEntity();
    await act(async () => {});
    expect(root.querySelector(".bg-primary")).toBeNull();
    expect(fetchMediaForEntity).toHaveBeenCalledTimes(1);
  });
});
