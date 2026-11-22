import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation, useParams } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchEntity,
  fetchEntityStation,
  fetchItemState,
  fetchMedia,
  fetchMediaForEntity,
  fetchSourceItems,
} from "@/lib/graphql";
import { fetchMediaRows, type MediaRow } from "@/lib/graphql/browse";
import type { EntityInfo, Layout, Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache, readViewerCache, writeViewerCache } from "@/lib/viewerCache";
import type { Locale } from "@/strings";
import { notifyEngagementChanged } from "../actions/engagementSync";
import { sectionCacheKey } from "../components/HydratedSection";
import { entityLinkState } from "../lib/entityPreview";
import * as watchQueue from "../lib/watchQueue";
import { EntityPage } from "./EntityPage";

const mocks = vi.hoisted(() => ({
  identity: null as string | null,
  layouts: [] as Layout[],
  revealed: false,
}));

vi.mock("@/lib/graphql", () => ({
  fetchEntity: vi.fn(),
  fetchEntityStation: vi.fn(),
  fetchMutes: vi.fn(async () => []),
  setMute: vi.fn(async () => undefined),
  clearMute: vi.fn(async () => undefined),
  clearAllMutes: vi.fn(async () => 0),
  fetchItemState: vi.fn(),
  fetchMedia: vi.fn(),
  fetchMediaForEntity: vi.fn(),
  fetchSourceItems: vi.fn(),
}));
vi.mock("@/lib/graphql/browse", () => ({ fetchMediaRows: vi.fn() }));
vi.mock("@/lib/session", () => ({
  useSession: () => ({ manifest: { layouts: mocks.layouts }, revealed: mocks.revealed }),
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

const identity = JSON.stringify(["viewer", "p1", "adult", false]);
const revealedIdentity = JSON.stringify(["viewer", "p1", "adult", true]);
const entity: EntityInfo = {
  id: "e1",
  kind: "Series",
  title: "Server title",
  fields: [{ name: "Year", type: "string", value: "2001", label: { en: "Year" } }],
};
// The row menu renders after a click; under a loaded full gate it can take longer than the
// 1s Testing Library default to appear (CORE-2806), and past 5s too (CORE-2883).
const MENU_WAIT = { timeout: 10_000 };
// A test that waits on the menu needs a budget above that wait plus its render: at Vitest's
// 5s default, a slow menu timed out the whole test before the wait could finish (CORE-2883).
const MENU_TEST_TIMEOUT = 15_000;

const related: SourceItem = {
  id: "m1",
  kind: "media",
  title: "Related item",
  artwork: { poster: "" },
};
const recentSection: Section = {
  id: "recent",
  kind: "shelf",
  title: { en: "Recently added" },
  artworkSlot: "poster",
  source: { kind: "recent" },
};
const recentItem: SourceItem = { id: "recent-item", kind: "media", title: "Recent item" };

function PlayerProbe() {
  const mediaId = useParams()["*"];
  const location = useLocation();
  return (
    <output aria-label="player">
      {mediaId} {JSON.stringify(location.state)}
    </output>
  );
}

function renderEntity(state?: unknown, locale: Locale = "en") {
  return render(
    <I18nProvider initialLocale={locale}>
      <MemoryRouter initialEntries={[{ pathname: "/entity/e1", state }]}>
        <Routes>
          <Route path="/entity/:entityId" element={<EntityPage />} />
          <Route path="/watch/*" element={<PlayerProbe />} />
          <Route path="/media/:mediaId" element={<DetailProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

function DetailProbe() {
  const { mediaId } = useParams();
  const { state } = useLocation();
  return (
    <output aria-label="detail">
      {mediaId} {JSON.stringify(state)}
    </output>
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
  vi.mocked(fetchMedia).mockReset().mockResolvedValue(null);
  vi.mocked(fetchItemState).mockReset().mockResolvedValue({
    id: "m1",
    kind: "media",
    watched: false,
    watchlisted: false,
    rating: 0,
  });
  vi.mocked(fetchMediaRows).mockReset().mockResolvedValue({});
  vi.mocked(fetchSourceItems).mockReset().mockResolvedValue([]);
  vi.mocked(fetchMediaForEntity)
    .mockReset()
    .mockReturnValue(new Promise(() => {}));
});

afterEach(() => {
  cleanup();
  clearViewerCache();
  vi.restoreAllMocks();
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
    mocks.layouts = [
      { id: "series-detail", sections: [recentSection], role: "detail", entityKind: "Series" },
    ];
    vi.mocked(fetchEntity).mockReturnValue(new Promise(() => {}));
    vi.mocked(fetchMediaForEntity).mockResolvedValue([related]);
    vi.mocked(fetchSourceItems).mockResolvedValue([recentItem]);

    renderEntity(entityLinkState({ id: "e1", title: "Card title", kind: "Series" }, identity));

    expect(await screen.findByRole("list", { name: "Recently added" })).toBeInTheDocument();
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

  it("renders each related group under its display name after the Media list", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(withGroups);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([related]);

    renderEntity();

    const media = await screen.findByText("Related item");
    expect(screen.getByRole("heading", { name: "Boxes" })).toBeInTheDocument();
    // A group without a declared display name falls back to the kind.
    expect(screen.getByRole("heading", { name: "place" })).toBeInTheDocument();
    // Children first, then the related rows (CORE-2441).
    const box = screen.getByText("Linked box");
    expect(media.compareDocumentPosition(box) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
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

  it("renders related groups after a detail Layout", async () => {
    mocks.layouts = [
      { id: "rack-detail", sections: [recentSection], role: "detail", entityKind: "rack" },
    ];
    vi.mocked(fetchEntity).mockResolvedValue(withGroups);
    vi.mocked(fetchSourceItems).mockResolvedValue([recentItem]);

    renderEntity();

    const box = await screen.findByText("Linked box");
    const layout = await screen.findByRole("list", { name: "Recently added" });
    expect(layout.compareDocumentPosition(box) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
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
    expect(screen.getByText("On deck")).toBeInTheDocument();
    expect(screen.getByText("2 unwatched")).toBeInTheDocument();

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
    const { container: root } = renderEntity();
    await act(async () => {});
    const primary = root.querySelector(".bg-primary");
    expect(primary).toHaveAccessibleName("Play Third part");
    expect(primary).toHaveTextContent("Play");
  });

  // CORE-2826: the primary button names the child it plays by its plugin ordinal.
  it("names the next-up child's ordinal on the primary button", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(container);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      { ...children[0], subtitle: "S1 · E1 · First part" },
      { ...child("c3", "Third part"), subtitle: "S1 · E3 · Third part" },
    ]);
    const { container: root, unmount } = renderEntity();
    await act(async () => {});
    const primary = root.querySelector(".bg-primary");
    expect(primary).toHaveTextContent(/^Play S1 E3$/);
    expect(primary).toHaveAccessibleName("Play S1 E3, Third part");
    unmount();

    // Label in name holds where the verb trails: the spoken name still starts
    // with the visible label (CORE-2826).
    const { container: deRoot, unmount: unmountDe } = renderEntity(undefined, "de");
    await act(async () => {});
    const dePrimary = deRoot.querySelector(".bg-primary");
    expect(dePrimary).toHaveTextContent(/^S1 E3 abspielen$/);
    expect(dePrimary).toHaveAccessibleName("S1 E3 abspielen, Third part");
    unmountDe();

    vi.mocked(fetchMediaForEntity).mockResolvedValue([{ ...children[1], subtitle: "S1 · E2" }]);
    const resumed = renderEntity();
    await act(async () => {});
    const resume = resumed.container.querySelector(".bg-primary");
    expect(resume).toHaveTextContent(/^Resume S1 E2$/);
    expect(resume).toHaveAccessibleName("Resume S1 E2, Second part");
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

// CORE-2034 restores the shelf-card detail contract (CORE-1144) for both child layouts.
describe.each([false, true])("EntityPage child navigation (detail layout: %s)", (hasLayout) => {
  beforeEach(() => {
    vi.mocked(fetchEntity).mockResolvedValue({ ...entity, archetype: "container" });
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      { ...related, mediaClass: "video", artworkSlots: { thumb: "/art/child" } },
      { ...related, id: "m2", title: "Second child", mediaClass: "video" },
      { ...related, id: "locked", title: "Locked child", mediaClass: "video", locked: true },
    ]);
    mocks.layouts = hasLayout
      ? [
          {
            id: "children",
            role: "detail",
            entityKind: entity.kind,
            sections: [
              {
                id: "children",
                kind: "list",
                title: { en: "Children" },
                source: { kind: "entity-media" },
              },
            ],
          } as unknown as Layout,
        ]
      : [];
  });

  it.each(["title", "thumbnail"])("opens detail from the child %s", async (part) => {
    renderEntity();
    const link = await screen.findByRole("link", { name: /Related item/ });
    expect(link).toHaveAttribute("href", "/media/m1");
    fireEvent.click(
      part === "title" ? within(link).getByText("Related item") : link.querySelector("img")!,
    );
    expect(screen.getByRole("status", { name: "detail" })).toHaveTextContent("m1");
    expect(screen.queryByRole("status", { name: "player" })).toBeNull();
  });

  it(
    "opens detail from More",
    async () => {
      const queueWindow = vi.spyOn(watchQueue, "queueWindow");
      renderEntity();
      fireEvent.click(await screen.findByRole("button", { name: "More actions for Related item" }));
      const details = await screen.findByRole("menuitem", { name: "Details" }, MENU_WAIT);
      expect(queueWindow).not.toHaveBeenCalled();
      fireEvent.click(details);
      expect(screen.getByRole("status", { name: "detail" })).toHaveTextContent("m1");
      expect(queueWindow).toHaveBeenCalledExactlyOnceWith(expect.any(Array), "m1");
    },
    MENU_TEST_TIMEOUT,
  );

  it(
    "offers Mark played for an audio child alongside video children",
    async () => {
      vi.mocked(fetchMediaForEntity).mockResolvedValue([
        { ...related, mediaClass: "audio" },
        { ...related, id: "m2", title: "Video child", mediaClass: "video" },
      ]);
      renderEntity();
      fireEvent.click(await screen.findByRole("button", { name: "More actions for Related item" }));

      expect(
        await screen.findByRole("menuitem", { name: "Mark played" }, MENU_WAIT),
      ).toBeInTheDocument();
      expect(screen.queryByRole("menuitem", { name: "Mark watched" })).toBeNull();
    },
    MENU_TEST_TIMEOUT,
  );

  it.each(["button", "menu"])(
    "keeps direct Play through the %s",
    async (entry) => {
      const queueWindow = vi.spyOn(watchQueue, "queueWindow");
      renderEntity();
      const link = await screen.findByRole("link", { name: /Related item/ });
      const row = within(link.closest("li")!);
      expect(queueWindow).not.toHaveBeenCalled();
      if (entry === "menu") {
        fireEvent.click(row.getByRole("button", { name: "More actions for Related item" }));
        fireEvent.click(await screen.findByRole("menuitem", { name: "Play" }, MENU_WAIT));
      } else {
        fireEvent.click(row.getByRole("button", { name: "Play Related item" }));
      }
      const output = screen.getByRole("status", { name: "player" }).textContent!;
      expect(output.startsWith("m1 ")).toBe(true);
      const state = JSON.parse(output.slice(3));
      expect(state.queue.map((item: { id: string }) => item.id)).toEqual(["m1", "m2"]);
      expect(state.queueIdentity).toBe(identity);
      expect(queueWindow).toHaveBeenCalledExactlyOnceWith(expect.any(Array), "m1");
    },
    MENU_TEST_TIMEOUT,
  );

  it(
    "shows the localized action error on the child whose menu failed to load",
    async () => {
      // Every load of this child's state fails: a one-shot rejection can land on a load the
      // menu aborts and reruns, and the rerun then succeeds (CORE-2883).
      const loaded = vi.mocked(fetchItemState).getMockImplementation();
      vi.mocked(fetchItemState).mockImplementation((target, signal) =>
        "mediaID" in target && target.mediaID === "m1"
          ? Promise.reject(new Error("offline"))
          : loaded!(target, signal),
      );
      renderEntity(undefined, "fr");
      const link = await screen.findByRole("link", { name: /Related item/ });
      const row = within(link.closest("li")!);
      fireEvent.click(row.getByRole("button", { name: "Plus d’actions pour Related item" }));

      const message = "Échec de l'enregistrement. Réessayez.";
      expect(await row.findByText(message, {}, MENU_WAIT)).toHaveAttribute("role", "status");
      expect(screen.getAllByText(message)).toHaveLength(1);
    },
    MENU_TEST_TIMEOUT,
  );

  it("orders child controls as link, Play, More and excludes locked playback", async () => {
    const { container } = renderEntity();
    await screen.findByRole("link", { name: /Related item/ });
    const controls = Array.from(container.querySelectorAll("li a, li button"));
    expect(
      controls.map((control) => control.getAttribute("aria-label") ?? control.textContent),
    ).toEqual([
      expect.stringContaining("Related item"),
      "Play Related item",
      "More actions for Related item",
      expect.stringContaining("Second child"),
      "Play Second child",
      "More actions for Second child",
      expect.stringContaining("Locked child"),
    ]);
    expect(
      controls.every(
        (control) => !control.hasAttribute("tabindex") || control.getAttribute("tabindex") === "0",
      ),
    ).toBe(true);
    const user = userEvent.setup();
    (controls[0] as HTMLElement).focus();
    for (const control of controls.slice(1)) {
      await user.tab();
      expect(control).toHaveFocus();
    }
  });
});

it("windows a long child queue only when the child detail link is activated", async () => {
  const queueWindow = vi.spyOn(watchQueue, "queueWindow");
  const items = Array.from({ length: watchQueue.MAX_QUEUE + 101 }, (_, index) => ({
    id: `child-${index}`,
    kind: "media",
    title: `Child ${index}`,
    mediaClass: "video",
  }));
  const target = items.at(-2)!;
  vi.mocked(fetchEntity).mockResolvedValue({ ...entity, archetype: "container" });
  vi.mocked(fetchMediaForEntity).mockResolvedValue(items);
  renderEntity();
  const title = await screen.findByText(target.title);
  expect(queueWindow.mock.calls.length).toBe(0);

  fireEvent.click(title);

  expect(queueWindow).toHaveBeenCalledExactlyOnceWith(expect.any(Array), target.id);
  const output = screen.getByRole("status", { name: "detail" }).textContent!;
  expect(output.startsWith(`${target.id} `)).toBe(true);
  const state: watchQueue.PlayerNavState = JSON.parse(output.slice(target.id.length + 1));
  expect(state.queueIdentity).toBe(identity);
  expect(state.queue?.map((item) => item.id)).toEqual(
    items.slice(-watchQueue.MAX_QUEUE).map((item) => item.id),
  );
}, 10_000);

describe.each(["list", "grid"] as const)(
  "EntityPage child navigation in a mixed detail %s layout",
  (kind) => {
    beforeEach(() => {
      mocks.layouts = [
        {
          id: "mixed-detail",
          role: "detail",
          entityKind: entity.kind,
          sections: [
            {
              id: "children",
              kind,
              title: { en: "Children" },
              artworkSlot: "thumb",
              source: { kind: "entity-media" },
            },
            { ...recentSection, kind: "list" },
          ],
        },
      ];
      vi.mocked(fetchEntity).mockResolvedValue({ ...entity, archetype: "container" });
      const child = { ...related, mediaClass: "video", artworkSlots: { thumb: "/art/child" } };
      const lockedChild = { ...child, id: "locked", title: "Locked child", locked: true };
      vi.mocked(fetchMediaForEntity).mockResolvedValue([child, lockedChild]);
      vi.mocked(fetchSourceItems).mockImplementation(async ({ kind }) =>
        kind === "entity-media" ? [child, lockedChild] : [recentItem],
      );
    });

    it.each(["title", "thumbnail", "Details", "Play"])(
      "activates the child through its %s",
      async (entry) => {
        const queueWindow = vi.spyOn(watchQueue, "queueWindow");
        renderEntity();
        const children = await screen.findByRole("list", { name: "Children" });
        const link = within(children).getByRole("link", { name: /Related item/ });
        expect(await screen.findByRole("link", { name: /Recent item/ })).toHaveAttribute(
          "href",
          "/watch/recent-item",
        );
        expect(queueWindow).not.toHaveBeenCalledWith(expect.any(Array), "m1");

        if (entry === "title") {
          fireEvent.click(within(link).getByText("Related item"));
        } else if (entry === "thumbnail") {
          fireEvent.click(link.querySelector("img")!);
        } else {
          fireEvent.click(
            within(children).getByRole("button", { name: "More actions for Related item" }),
          );
          fireEvent.click(await screen.findByRole("menuitem", { name: entry }, MENU_WAIT));
        }

        const destination = entry === "Play" ? "player" : "detail";
        expect(screen.getByRole("status", { name: destination })).toHaveTextContent(/^m1\b/);
        expect(queueWindow.mock.calls.filter(([, id]) => id === "m1")).toHaveLength(1);
      },
      MENU_TEST_TIMEOUT,
    );

    it("tabs from detail through direct Play to More and plays from the focused button", async () => {
      const user = userEvent.setup();
      renderEntity();
      const children = within(await screen.findByRole("list", { name: "Children" }));
      const link = children.getByRole("link", { name: /Related item/ });
      const play = children.getByRole("button", { name: "Play Related item" });
      const more = children.getByRole("button", { name: "More actions for Related item" });
      const locked = children.getByRole("link", { name: /Locked child/ });

      link.focus();
      await user.tab();
      expect(play).toHaveFocus();
      await user.tab();
      expect(more).toHaveFocus();
      await user.tab();
      expect(locked).toHaveFocus();
      expect(within(locked.closest("li")!).queryByRole("button")).toBeNull();
      await user.tab({ shift: true });
      await user.tab({ shift: true });
      expect(play).toHaveFocus();
      await user.keyboard("{Enter}");

      expect(screen.getByRole("status", { name: "player" })).toHaveTextContent(/^m1\b/);
    });
  },
);

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

  // CORE-1879: verify the existing sequence/index pipeline at the album page boundary.
  it.each([
    ["en", "Disc"],
    ["fr", "Disque"],
    ["de", "CD"],
    ["es", "Disco"],
    ["pt", "Disco"],
  ] as const)(
    "groups a two-disc album in disc/track order with %s headings",
    async (locale, label) => {
      const shuffledTracks = [
        { id: "d2t2", kind: "media", title: "Second Disc Closer" },
        tracks[2],
        tracks[0],
        tracks[1],
      ];
      vi.mocked(fetchMediaForEntity).mockResolvedValue(shuffledTracks);
      vi.mocked(fetchMediaRows).mockResolvedValue({
        d2t2: row("d2t2", "Second Disc Closer", 2, 2),
        d1t2: row("d1t2", "First Disc Closer", 1, 2),
        d2t1: row("d2t1", "Second Disc Opener", 2, 1),
        d1t1: row("d1t1", "First Disc Opener", 1, 1),
      });
      renderEntity(undefined, locale);
      const first = await screen.findByRole("list", { name: `${label} 1` });
      const second = screen.getByRole("list", { name: `${label} 2` });
      expect(
        screen.getAllByRole("heading", { level: 3 }).map((heading) => heading.textContent),
      ).toEqual([`${label} 1`, `${label} 2`]);
      for (const [list, titles] of [
        [first, ["First Disc Opener", "First Disc Closer"]],
        [second, ["Second Disc Opener", "Second Disc Closer"]],
      ] as const) {
        const rows = within(list).getAllByRole("listitem");
        expect(rows).toHaveLength(2);
        rows.forEach((track, index) => {
          expect(within(track).getByText(titles[index])).toBeInTheDocument();
          expect(within(track).getByText(String(index + 1))).toHaveClass("bf-track-number");
        });
      }
      expect(fetchMediaRows).toHaveBeenCalledWith(
        shuffledTracks.map((track) => track.id),
        expect.any(AbortSignal),
        { positions: true },
      );
    },
  );

  it("uses the listed disc and track numbers when a track lookup is missing", async () => {
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      {
        id: "d2t2",
        kind: "media",
        title: "Second Disc Closer",
        mediaClass: "audio",
        sequence: 2,
        index: 2,
      },
      { ...tracks[2], mediaClass: "audio", sequence: 1, index: 2 },
      { ...tracks[0], mediaClass: "audio", sequence: 2, index: 1 },
      { ...tracks[1], mediaClass: "audio", sequence: 1, index: 1 },
    ]);
    vi.mocked(fetchMediaRows).mockResolvedValue({
      d1t2: row("d1t2", "First Disc Closer", 2, 3),
      d2t1: row("d2t1", "Second Disc Opener", 2, 1),
      d1t1: row("d1t1", "First Disc Opener", 1, 1),
    });
    renderEntity();

    const first = await screen.findByRole("list", { name: "Disc 1" });
    const second = screen.getByRole("list", { name: "Disc 2" });
    expect(
      screen.getAllByRole("heading", { level: 3 }).map((heading) => heading.textContent),
    ).toEqual(["Disc 1", "Disc 2"]);
    expect(
      within(first)
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(["1First Disc Opener", "2First Disc Closer"]);
    expect(
      within(second)
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(["1Second Disc Opener", "2Second Disc Closer"]);
  });

  it.each([1, 2])("keeps a single-disc album flat when its stored disc is %s", async (sequence) => {
    vi.mocked(fetchMediaForEntity).mockResolvedValue([tracks[2], tracks[1]]);
    vi.mocked(fetchMediaRows).mockResolvedValue({
      d1t2: row("d1t2", "First Disc Closer", sequence, 2),
      d1t1: row("d1t1", "First Disc Opener", sequence, 1),
    });
    renderEntity();
    const list = await screen.findByRole("list", { name: "Tracks" });
    expect(screen.queryByRole("heading", { level: 3 })).toBeNull();
    expect(
      within(list)
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(["1First Disc Opener", "2First Disc Closer"]);
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

  it.each(["title", "More"])(
    "opens the second disc track's detail through %s",
    async (entry) => {
      renderEntity();
      const disc = within(await screen.findByRole("list", { name: "Disc 2" }));

      if (entry === "title") {
        const link = disc.getByRole("link", { name: /Second Disc Opener/ });
        expect(link).toHaveAttribute("href", "/media/d2t1");
        fireEvent.click(within(link).getByText("Second Disc Opener"));
      } else {
        fireEvent.click(disc.getByRole("button", { name: "More actions for Second Disc Opener" }));
        fireEvent.click(await screen.findByRole("menuitem", { name: "Details" }, MENU_WAIT));
      }

      const output = screen.getByRole("status", { name: "detail" }).textContent!;
      expect(output.startsWith("d2t1 ")).toBe(true);
      const state: watchQueue.PlayerNavState = JSON.parse(output.slice("d2t1 ".length));
      expect(state.queueIdentity).toBe(identity);
      expect(state.queue?.map((item) => item.id)).toEqual(["d1t1", "d1t2", "d2t1"]);
      expect(screen.queryByRole("status", { name: "player" })).not.toBeInTheDocument();
    },
    MENU_TEST_TIMEOUT,
  );

  it("tabs from the track detail link to Play and More and plays with the cross-disc queue", async () => {
    const user = userEvent.setup();
    renderEntity();
    const disc = within(await screen.findByRole("list", { name: "Disc 2" }));
    disc.getByRole("link", { name: /Second Disc Opener/ }).focus();
    const play = disc.getByRole("button", { name: "Play Second Disc Opener" });

    await user.tab();
    expect(play).toHaveFocus();
    await user.tab();
    expect(disc.getByRole("button", { name: "More actions for Second Disc Opener" })).toHaveFocus();
    await user.tab({ shift: true });
    await user.keyboard("{Enter}");

    expect(playerState()).toEqual({ mediaId: "d2t1", queue: ["d1t1", "d1t2", "d2t1"] });
  });

  it("links a locked track to detail without exposing playback controls or queuing it", async () => {
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      { ...tracks[0], locked: true },
      ...tracks.slice(1),
    ]);
    renderEntity();
    const second = within(await screen.findByRole("list", { name: "Disc 2" }));
    expect(second.getByRole("link", { name: /Second Disc Opener/ })).toHaveAttribute(
      "href",
      "/media/d2t1",
    );
    expect(second.queryByRole("button")).not.toBeInTheDocument();

    const first = within(screen.getByRole("list", { name: "Disc 1" }));
    fireEvent.click(first.getByRole("button", { name: "Play First Disc Opener" }));
    expect(playerState()).toEqual({ mediaId: "d1t1", queue: ["d1t1", "d1t2"] });
  });

  it("offers no primary Play action until the order is known", async () => {
    vi.mocked(fetchMediaRows).mockReturnValue(new Promise(() => {}));
    const { container: root } = renderEntity();
    await act(async () => {});
    expect(root.querySelector(".bg-primary")).toBeNull();
    expect(fetchMediaForEntity).toHaveBeenCalledTimes(1);
  });
});

describe("EntityPage listening over a Media-only detail Layout (CORE-2362)", () => {
  const tracksLayout = {
    id: "record-detail",
    role: "detail",
    entityKind: "record",
    sections: [
      { id: "tracks", kind: "list", title: { en: "Rows" }, source: { kind: "entity-media" } },
    ],
  } as unknown as Layout;
  const record = {
    ...entity,
    kind: "record",
    archetype: "container",
    relatedEntities: [
      {
        kind: "maker",
        archetype: "person",
        kindDisplayName: { en: "Makers" },
        items: [{ id: "p1", kind: "entity", title: "Maker A" }],
      },
    ],
  } as EntityInfo;
  const track = (id: string, title: string, mediaClass = "audio"): SourceItem => ({
    id,
    kind: "media",
    title,
    mediaClass,
  });
  const row = (
    id: string,
    title: string,
    index: number,
    credits: MediaRow["credits"],
  ): MediaRow => ({
    id,
    title,
    favorited: false,
    mediaClass: "audio",
    durationSeconds: 125,
    credits,
    index,
  });
  const credit = (entityID: string, title: string) => ({
    entityID,
    title,
    kind: "maker",
    archetype: "person",
    relation: "credit",
  });
  const maker = credit("p1", "Maker A");
  const guest = credit("p2", "Guest B");

  beforeEach(() => {
    mocks.layouts = [tracksLayout];
    vi.mocked(fetchEntity).mockResolvedValue(record);
    // The listing carries each track's position, byline and duration, so the
    // rows come from it without a media(id) read (CORE-2922).
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      {
        ...track("t2", "Second Song"),
        index: 2,
        durationSeconds: 125,
        byline: ["Maker A", "Guest B"],
      },
      { ...track("t1", "First Song"), index: 1, durationSeconds: 125, byline: ["Maker A"] },
      { ...track("t3", "Third Song"), index: 3, durationSeconds: 125, byline: ["Maker A"] },
    ]);
    vi.mocked(fetchMediaRows).mockResolvedValue({
      t2: row("t2", "Second Song", 2, [maker, guest]),
      t1: row("t1", "First Song", 1, [maker]),
      t3: row("t3", "Third Song", 3, [maker]),
    });
  });

  it.each([false, true])(
    "uses listening wording for an audio container with poster %s",
    async (poster) => {
      vi.mocked(fetchEntity).mockResolvedValue({
        ...record,
        artworkSlots: poster ? { poster: "/a/album" } : {},
      });
      renderEntity();
      await act(async () => {});

      expect(screen.queryByText("3 unplayed")).toBeNull();
      expect(screen.getByText("Up next", { exact: true })).toBeInTheDocument();
      expect(screen.queryByText("3 unwatched")).toBeNull();
      expect(screen.queryByText("On deck")).toBeNull();
    },
  );

  it("lists the tracks in order with artist and duration instead of the Layout's rows", async () => {
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    expect(fetchSourceItems).not.toHaveBeenCalled();
    const list = screen.getByRole("list", { name: "Tracks" });
    const titles = within(list)
      .getAllByRole("button", { name: /^Play / })
      .map((button) => button.getAttribute("aria-label"));
    expect(titles).toEqual(["Play First Song", "Play Second Song", "Play Third Song"]);
    // A row names its people only when they go beyond the container's own.
    expect(within(list).getByText("Maker A, Guest B")).toBeInTheDocument();
    expect(within(list).queryAllByText("Maker A")).toHaveLength(0);
    expect(within(list).getAllByText("2:05")).toHaveLength(3);
  });

  it("names the container's people under the title as links instead of a card group", async () => {
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    const link = screen.getByRole("link", { name: "Maker A" });
    expect(link).toHaveAttribute("href", "/entity/p1");
    expect(screen.queryByRole("heading", { name: "Makers" })).toBeNull();
  });

  it("shuffles every track into the queue", async () => {
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    fireEvent.click(screen.getByRole("button", { name: "Shuffle" }));
    const player = screen.getByRole("status", { name: "player" }).textContent ?? "";
    const [mediaId] = player.split(" ", 1);
    const state = JSON.parse(player.slice(mediaId.length + 1));
    const queue = state.queue.map((item: { id: string }) => item.id);
    expect([...queue].sort()).toEqual(["t1", "t2", "t3"]);
    expect(queue[0]).toBe(mediaId);
    expect(state.queue.every((item: { mediaClass?: string }) => item.mediaClass === "audio")).toBe(
      true,
    );
  });

  it("plays a Station seeded by the Entity from Radio (CORE-2769)", async () => {
    vi.mocked(fetchEntityStation).mockResolvedValue([
      { mediaID: "s2", title: "Station Two" },
      { mediaID: "s1", title: "Station One" },
    ]);
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    fireEvent.click(
      screen.getByRole("button", { name: "Start a radio station from Server title" }),
    );
    await act(async () => {});
    expect(fetchEntityStation).toHaveBeenCalledWith("e1", expect.any(AbortSignal));
    const player = screen.getByRole("status", { name: "player" }).textContent ?? "";
    const [mediaId] = player.split(" ", 1);
    const state = JSON.parse(player.slice(mediaId.length + 1));
    expect(mediaId).toBe("s2");
    expect(state.queue).toEqual([
      { id: "s2", title: "Station Two", mediaClass: "audio" },
      { id: "s1", title: "Station One", mediaClass: "audio" },
    ]);
  });

  it("keeps the new Entity open when Radio responds for the previous one", async () => {
    const request = pending<Awaited<ReturnType<typeof fetchEntityStation>>>();
    vi.mocked(fetchEntityStation).mockReturnValueOnce(request.promise);
    vi.mocked(fetchEntity)
      .mockResolvedValueOnce({
        ...record,
        relatedEntities: [
          {
            kind: record.kind,
            archetype: "container",
            items: [{ id: "next", kind: "entity", title: "Next Release" }],
            sections: [{ value: "short", displayName: { en: "Short Players" }, count: 1 }],
          },
        ],
      } as EntityInfo)
      .mockResolvedValue({ ...record, id: "next", title: "Next Release" } as EntityInfo);
    renderEntity();
    await act(async () => {});

    const section = screen.getByRole("heading", { name: "Short Players 1" }).closest("section");
    expect(section).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Start a radio station from Server title" }),
    );
    const signal = vi.mocked(fetchEntityStation).mock.lastCall?.[1];
    fireEvent.click(within(section as HTMLElement).getByRole("link", { name: /^Next Release/ }));
    await act(async () => {});
    expect(signal?.aborted).toBe(true);

    await act(async () => request.resolve([{ mediaID: "late", title: "Late Track" }]));
    expect(screen.getByRole("heading", { name: "Next Release" })).toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "player" })).toBeNull();
  });

  it("gives ruled sections counts and keeps unruled related cards in order (CORE-2769)", async () => {
    vi.mocked(fetchEntity).mockResolvedValue({
      ...record,
      archetype: "person",
      relatedEntities: [
        {
          kind: "release",
          archetype: "container",
          kindPluralDisplayName: { en: "Releases" },
          items: [
            { id: "r1", kind: "entity", title: "Long One", year: 2010 },
            { id: "r2", kind: "entity", title: "Long Two", year: 1999 },
            { id: "r3", kind: "entity", title: "Short One", year: 2005 },
          ],
          sections: [
            { value: "long", displayName: { en: "Long Players" }, count: 2 },
            { value: "short", displayName: { en: "Short Players" }, count: 1 },
          ],
        },
        {
          kind: "release",
          archetype: "container",
          kindPluralDisplayName: { en: "Other Releases" },
          items: [
            { id: "r4", kind: "entity", title: "Z First", year: 1990 },
            { id: "r5", kind: "entity", title: "A Second", year: 2022 },
          ],
        },
      ],
    } as EntityInfo);
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    const long = screen.getByRole("heading", { name: "Long Players 2" });
    expect(within(long.closest("section") as HTMLElement).getAllByRole("listitem")).toHaveLength(2);
    expect(screen.getByRole("heading", { name: "Short Players 1" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /^Releases/ })).toBeNull();
    const unruled = screen.getByRole("heading", { name: "Other Releases" });
    const links = within(unruled.closest("section") as HTMLElement).getAllByRole("link");
    expect(links[0]).toHaveAccessibleName(/^Z First/);
    expect(links[1]).toHaveAccessibleName(/^A Second/);
  });

  it("lists a video container's own Media as detail rows over its Media-only Layout (CORE-2510)", async () => {
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      track("v1", "First Part", "video"),
      track("v2", "Second Part", "video"),
    ]);
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    // The rows replace the Layout's poster list, headed by its Section title.
    expect(fetchSourceItems).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "Rows" })).toBeInTheDocument();
    const rows = screen.getByRole("list", { name: "Rows" });
    expect(within(rows).getByRole("link", { name: /^First Part/ })).toHaveAttribute(
      "href",
      "/media/v1",
    );
    expect(within(rows).getByRole("button", { name: "Play Second Part" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Shuffle" })).toBeNull();
    // A person relation renders through the cast row (CORE-2441).
    expect(screen.getByRole("heading", { name: "People" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Maker A/ })).toHaveAttribute("href", "/entity/p1");
  });

  it("holds a Media-only Layout back until the Entity says whether its Media read as rows", () => {
    vi.mocked(fetchEntity).mockReturnValue(new Promise(() => {}));
    renderEntity(
      entityLinkState(
        { id: "e1", title: "Preview", kind: "record", mediaClass: "video" },
        identity,
      ),
    );
    // A container lists them as rows (CORE-2510), so nothing paints that the rows would replace.
    expect(fetchSourceItems).not.toHaveBeenCalled();
  });

  it("holds the Layout back while an audio container's Media are unknown", () => {
    vi.mocked(fetchEntity).mockReturnValue(new Promise(() => {}));
    renderEntity(
      entityLinkState(
        { id: "e1", title: "Preview", kind: "record", mediaClass: "audio" },
        identity,
      ),
    );
    expect(fetchSourceItems).not.toHaveBeenCalled();
  });
});

describe("EntityPage hero (CORE-2441)", () => {
  const show: EntityInfo = {
    id: "e1",
    kind: "set",
    archetype: "container",
    title: "Hero set",
    artworkSlots: { poster: "/a/poster", backdrop: "/a/backdrop" },
    fields: [
      { name: "year", type: "number", value: "2004", label: { en: "Year" } },
      { name: "summary", type: "text", value: "An overview.", label: { en: "Summary" } },
      { name: "certification", type: "text", value: "PG", label: { en: "Rating" } },
      { name: "network", type: "text", value: "Channel", label: { en: "Network" } },
    ],
    relatedEntities: [
      {
        kind: "part",
        kindDisplayName: { en: "Parts" },
        archetype: "container",
        items: [
          {
            id: "b1",
            kind: "entity",
            title: "Part one",
            engagement: { totalCount: 4, remainingCount: 1 },
          },
          {
            id: "b2",
            kind: "entity",
            title: "Part two",
            engagement: { totalCount: 6, remainingCount: 6 },
          },
        ],
      },
      {
        kind: "genre",
        archetype: "category",
        items: [{ id: "g1", kind: "entity", title: "Drama" }],
      },
      {
        kind: "cast",
        archetype: "person",
        items: [{ id: "p1", kind: "entity", title: "Person A" }],
      },
    ],
  };

  it("draws the backdrop, facts, overview and rows from data present", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(show);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([]);
    const { container } = renderEntity();
    await act(async () => {});

    expect(
      container.querySelector(".bf-detail-stage[data-backdrop] .bf-detail-backdrop img"),
    ).not.toBeNull();
    const facts = within(screen.getByRole("list", { name: "About this item" }))
      .getAllByRole("listitem")
      .map((li) => li.textContent);
    expect(facts).toEqual(["set", "2004", "PG", "Drama"]);
    // Related containers never feed the badge: their direction is unknown.
    expect(screen.queryByText(/unwatched/)).toBeNull();
    expect(screen.getByText("An overview.")).toBeInTheDocument();
    // The overview and year are not repeated in the field list; the rest is.
    expect(screen.queryByText("Summary")).toBeNull();
    expect(screen.getByText("Channel")).toBeInTheDocument();
    // Child containers carry their item counts on the card.
    expect(screen.getByText("Part one")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Parts" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "People" })).toBeInTheDocument();
  });

  it("degrades without a backdrop or overview", async () => {
    vi.mocked(fetchEntity).mockResolvedValue({
      ...show,
      artworkSlots: {},
      fields: [],
      relatedEntities: [],
    });
    vi.mocked(fetchMediaForEntity).mockResolvedValue([]);
    const { container } = renderEntity();
    await act(async () => {});

    expect(container.querySelector(".bf-detail-stage")).not.toHaveAttribute("data-backdrop");
    expect(container.querySelector(".bf-detail-backdrop")).toBeNull();
    expect(container.querySelector(".bf-detail-overview")).toBeNull();
    expect(screen.getByRole("heading", { level: 1, name: "Hero set" })).toBeInTheDocument();
  });

  it("heads a middle-level parent's own children with their count and names the next one", async () => {
    vi.mocked(fetchEntity).mockResolvedValue({ ...show, relatedEntities: [] });
    vi.mocked(fetchMediaForEntity).mockResolvedValue([
      { id: "m1", kind: "media", title: "One", engagement: { completed: true } },
      { id: "m2", kind: "media", title: "Two", subtitle: "Ordinal 2" },
    ]);
    renderEntity();
    await act(async () => {});

    expect(screen.getByRole("heading", { level: 2, name: "2 items" })).toBeInTheDocument();
    expect(screen.getByText("On deck")).toBeInTheDocument();
    // The On Deck line names the next child once: its ordinal, then its title (CORE-2510).
    expect(screen.getByText("Next up: Ordinal 2 · Two")).toBeInTheDocument();
    expect(screen.queryByText("Ordinal 2")).toBeNull();
    expect(screen.getByText("1 unwatched")).toBeInTheDocument();
  });

  // Codex round 2 repros: related groups carry both directions, so neither a
  // lone parent nor equal parent and child groups may produce a count.
  const container = (id: string, remaining: number, total: number) => ({
    id,
    kind: "entity",
    title: id,
    engagement: { remainingCount: remaining, totalCount: total },
  });

  it("shows no badge from a lone parent container, before or after its Media load", async () => {
    vi.mocked(fetchEntity).mockResolvedValue({
      ...show,
      relatedEntities: [{ kind: "whole", archetype: "container", items: [container("w", 9, 20)] }],
    });
    let resolveMedia: (items: SourceItem[]) => void = () => {};
    vi.mocked(fetchMediaForEntity).mockReturnValue(
      new Promise((resolve) => {
        resolveMedia = resolve;
      }),
    );
    renderEntity();
    await act(async () => {});
    expect(screen.queryByText(/unwatched/)).toBeNull();

    await act(async () => {
      resolveMedia([
        { id: "m1", kind: "media", title: "One", engagement: { completed: true } },
        { id: "m2", kind: "media", title: "Two" },
      ]);
    });
    expect(screen.getByText("1 unwatched")).toBeInTheDocument();
  });

  it("shows no badge when parent and child container groups have equal counts", async () => {
    vi.mocked(fetchEntity).mockResolvedValue({
      ...show,
      relatedEntities: [
        { kind: "whole", archetype: "container", items: [container("w", 3, 10)] },
        { kind: "part", archetype: "container", items: [container("b", 3, 10)] },
      ],
    });
    vi.mocked(fetchMediaForEntity).mockResolvedValue([]);
    renderEntity();
    await act(async () => {});
    expect(screen.queryByText(/unwatched/)).toBeNull();
  });
});

// feature:container-entity-page (CORE-2510, CORE-2512, CORE-2522)
describe("EntityPage middle and top containers", () => {
  const ref = (id: string, title: string, extra: Partial<SourceItem> = {}): SourceItem => ({
    id,
    kind: "entity",
    title,
    ...extra,
  });
  const longOverview = "A season of football and second chances. ".repeat(8).trim();
  const part: EntityInfo = {
    id: "e1",
    kind: "part",
    archetype: "container",
    title: "Part 2",
    fields: [{ name: "overview", type: "text", value: longOverview, label: { en: "Overview" } }],
    relatedEntities: [
      {
        kind: "whole",
        kindDisplayName: { en: "Wholes" },
        archetype: "container",
        items: [ref("w1", "The Whole", { engagement: { totalCount: 20, remainingCount: 20 } })],
      },
    ],
  };
  const whole: EntityInfo = {
    id: "w1",
    kind: "whole",
    archetype: "container",
    title: "The Whole",
    fields: [],
    relatedEntities: [
      {
        kind: "part",
        kindDisplayName: { en: "Parts" },
        archetype: "container",
        items: [ref("p1", "Part 1"), ref("e1", "Part 2"), ref("p3", "Part 3")],
      },
    ],
  };
  const episodes: SourceItem[] = [
    { id: "m1", kind: "media", title: "One" },
    { id: "m2", kind: "media", title: "Two" },
  ];

  function serve(entities: EntityInfo[]) {
    vi.mocked(fetchEntity).mockImplementation((id: string) => {
      const found = entities.find((candidate) => candidate.id === id);
      return found ? Promise.resolve(found) : Promise.reject(new Error("unknown entity"));
    });
  }

  it("names its parent above the title and switches between its siblings", async () => {
    serve([part, whole]);
    vi.mocked(fetchMediaForEntity).mockResolvedValue(episodes);
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    const parent = screen.getByRole("link", { name: "The Whole" });
    expect(parent).toHaveAttribute("href", "/entity/w1");
    expect(parent.closest(".bf-entity-parent")).not.toBeNull();
    // The parent no longer appears as a one-card rail.
    expect(screen.queryByRole("heading", { name: "Wholes" })).toBeNull();

    const switcher = screen.getByRole("navigation", { name: "Parts" });
    const links = within(switcher).getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual(["Part 1", "Part 2", "Part 3"]);
    expect(links[0]).toHaveAttribute("href", "/entity/p1");
    expect(links[1]).toHaveAttribute("aria-current", "page");
    expect(links[0]).not.toHaveAttribute("aria-current");
    // A middle container is not offered similar ones.
    expect(screen.queryByRole("heading", { name: "Related" })).toBeNull();
  });

  it("links the parent and siblings by readable address, bare in a Revealed session", async () => {
    const parentID = `entity-${"a".repeat(32)}`;
    const siblingID = `entity-${"b".repeat(32)}`;
    const readablePart: EntityInfo = {
      ...part,
      relatedEntities: [
        {
          ...part.relatedEntities![0],
          items: [ref(parentID, "The Whole", { engagement: { totalCount: 20 } })],
        },
      ],
    };
    const readableWhole: EntityInfo = {
      ...whole,
      id: parentID,
      relatedEntities: [
        { ...whole.relatedEntities![0], items: [ref(siblingID, "Part 1"), ref("e1", "Part 2")] },
      ],
    };
    serve([readablePart, readableWhole]);
    vi.mocked(fetchMediaForEntity).mockResolvedValue(episodes);
    for (const revealed of [false, true]) {
      mocks.revealed = revealed;
      const { unmount } = renderEntity();
      await act(async () => {});
      await act(async () => {});
      expect(screen.getByRole("link", { name: "The Whole" })).toHaveAttribute(
        "href",
        revealed ? `/entity/${parentID}` : `/entity/the-whole-${"a".repeat(32)}`,
      );
      expect(screen.getByRole("link", { name: "Part 1" })).toHaveAttribute(
        "href",
        revealed ? `/entity/${siblingID}` : `/entity/the-whole-part-1-${"b".repeat(32)}`,
      );
      unmount();
    }
    mocks.revealed = false;
  });

  it("clamps a long overview field with a More toggle instead of listing it", async () => {
    serve([part, whole]);
    vi.mocked(fetchMediaForEntity).mockResolvedValue(episodes);
    renderEntity();
    await act(async () => {});

    expect(screen.queryByText("Overview")).toBeNull();
    expect(screen.getByText(longOverview)).toHaveAttribute("data-clamped");
    fireEvent.click(screen.getByRole("button", { name: "More" }));
    expect(screen.getByText(longOverview)).not.toHaveAttribute("data-clamped");
    expect(screen.getByRole("button", { name: "Less" })).toHaveAttribute("aria-expanded", "true");
  });

  it("degrades to no switcher or parent link when the parent cannot be read", async () => {
    serve([{ ...part, relatedEntities: [] }]);
    vi.mocked(fetchMediaForEntity).mockResolvedValue(episodes);
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    expect(screen.getByRole("heading", { level: 1, name: "Part 2" })).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Parts" })).toBeNull();
    expect(screen.queryByRole("link", { name: "The Whole" })).toBeNull();
  });

  const top: EntityInfo = {
    id: "e1",
    kind: "whole",
    archetype: "container",
    title: "The Whole",
    fields: [],
    relatedEntities: [
      { kind: "genre", archetype: "category", items: [ref("g1", "Comedy")] },
      { kind: "network", archetype: "category", items: [ref("n1", "Channel")] },
      { kind: "maker", archetype: "person", items: [ref("p1", "Maker")] },
    ],
  };
  const through = (id: string, ids: string[]): EntityInfo => ({
    id,
    kind: "genre",
    title: id,
    fields: [],
    relatedEntities: [
      { kind: "whole", items: ids.map((other) => ref(other, other.toUpperCase())) },
    ],
  });

  it("ends a top container with similar ones ranked by shared genres, networks and people", async () => {
    serve([
      top,
      through("g1", ["e1", "a", "b"]),
      through("n1", ["b", "c"]),
      through("p1", ["c", "b"]),
    ]);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([]);
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    const heading = screen.getByRole("heading", { name: "Related" });
    const rail = heading.closest("section") as HTMLElement;
    expect(
      within(rail)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual(["B", "C", "A"]);
  });

  it("hides the similar rail when nothing is similar or the reads fail", async () => {
    serve([top, through("g1", ["e1"])]);
    vi.mocked(fetchMediaForEntity).mockResolvedValue([]);
    renderEntity();
    await act(async () => {});
    await act(async () => {});

    expect(screen.getByRole("heading", { level: 1, name: "The Whole" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Related" })).toBeNull();
  });
});
