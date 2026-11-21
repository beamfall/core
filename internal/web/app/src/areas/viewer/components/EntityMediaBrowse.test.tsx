import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchSourceItems } from "@/lib/graphql";
import { fetchMediaRows } from "@/lib/graphql/browse";
import type { EntityInfo, GenericMediaInfo, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { readPlayerNavState } from "../lib/watchQueue";
import { EntityMediaBrowse } from "./EntityMediaBrowse";
import { useListingRows } from "./useListingRows";

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fetchSourceItems: vi.fn(),
}));

vi.mock("@/lib/graphql/browse", () => ({
  fetchMediaRows: vi.fn(),
}));

vi.mock("@/lib/session", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/session")>()),
  useViewerIdentity: () => "viewer-1",
}));

const tracks: SourceItem[] = [
  { id: "t1", kind: "media", title: "Track One" },
  { id: "t2", kind: "media", title: "Track Two" },
  { id: "t3", kind: "media", title: "Track Three" },
];

const credit = (entityID: string, title: string, archetype: string) => ({
  entityID,
  title,
  kind: archetype === "person" ? "performer" : "record",
  archetype,
  relation: "credit",
});

const rows: Record<string, GenericMediaInfo> = {
  t1: {
    id: "t1",
    title: "Track One",
    favorited: false,
    mediaClass: "audio",
    durationSeconds: 201,
    credits: [
      credit("person-a", "Test Artist", "person"),
      credit("album-1", "Test Album", "container"),
    ],
  },
  t2: {
    id: "t2",
    title: "Track Two",
    favorited: false,
    mediaClass: "audio",
    durationSeconds: 95,
    credits: [
      credit("person-a", "Test Artist", "person"),
      credit("person-b", "Guest Artist", "person"),
      credit("album-mix", "Test Mix", "container"),
    ],
  },
  t3: {
    id: "t3",
    title: "Track Three",
    favorited: false,
    mediaClass: "audio",
    credits: [
      credit("person-a", "Test Artist", "person"),
      credit("album-1", "Test Album", "container"),
    ],
  },
};

const album: EntityInfo = {
  id: "album-1",
  kind: "record",
  title: "Test Album",
  archetype: "container",
  fields: [],
  relatedEntities: [
    {
      kind: "performer",
      archetype: "person",
      items: [{ id: "person-a", kind: "entity", title: "Test Artist" }],
    },
  ],
} as unknown as EntityInfo;

const artist: EntityInfo = {
  id: "person-a",
  kind: "performer",
  title: "Test Artist",
  archetype: "person",
  fields: [],
  relatedEntities: [
    {
      kind: "record",
      archetype: "container",
      items: [{ id: "album-1", kind: "entity", title: "Test Album" }],
    },
  ],
} as unknown as EntityInfo;

beforeEach(() => {
  vi.mocked(fetchMediaRows).mockReset().mockResolvedValue(rows);
  vi.mocked(fetchSourceItems).mockReset().mockResolvedValue([]);
});

afterEach(cleanup);

function WatchProbe() {
  const location = useLocation();
  const nav = readPlayerNavState(location.state, "viewer-1");
  return (
    <output data-testid="watch">
      {location.pathname}|
      {(nav.queue ?? []).map((item) => `${item.id}:${item.mediaClass ?? ""}`).join(",")}
    </output>
  );
}

// The page reads the rows (useListingRows) and passes them in, as EntityPage does.
function Browse({ entity, items }: { entity: EntityInfo; items: SourceItem[] }) {
  const listing = useListingRows(entity, items);
  return <EntityMediaBrowse entity={entity} items={items} label="Children" listing={listing} />;
}

function renderBrowse(entity: EntityInfo, items: SourceItem[]) {
  return render(
    <I18nProvider initialLocale="en">
      <MemoryRouter initialEntries={["/entity"]}>
        <Routes>
          <Route path="/entity" element={<Browse entity={entity} items={items} />} />
          <Route path="/watch/:id" element={<WatchProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("EntityMediaBrowse", () => {
  it("lists an audio container's tracks in server order with runtime and differing credits", async () => {
    renderBrowse(album, tracks);
    const list = await screen.findByRole("list", { name: "Tracks" });
    const links = within(list).getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual([
      "1Track One3:21",
      "2Track TwoTest Artist, Guest Artist1:35",
      "3Track Three",
    ]);
  });

  it("uses the fetched track credits for links in the row menu", async () => {
    renderBrowse(album, tracks);
    fireEvent.click(await screen.findByRole("button", { name: "More actions for Track One" }));
    expect(screen.getByRole("menuitem", { name: "Go to artist" })).toHaveAttribute(
      "href",
      "/entity/person-a",
    );
    expect(screen.getByRole("menuitem", { name: "Go to album" })).toHaveAttribute(
      "href",
      "/entity/album-1",
    );
  });

  it("plays from any track with the container's tracks as the queue", async () => {
    renderBrowse(album, tracks);
    fireEvent.click(await screen.findByRole("button", { name: "Play Track Two" }));
    expect(screen.getByTestId("watch")).toHaveTextContent("/watch/t2|t1:audio,t2:audio,t3:audio");
  });

  it("reads stored positions only for a container's track list", async () => {
    renderBrowse(album, tracks);
    await screen.findByRole("list", { name: "Tracks" });
    expect(vi.mocked(fetchMediaRows).mock.calls[0][2]).toEqual({ positions: true });
    cleanup();
    vi.mocked(fetchMediaRows).mockClear();
    renderBrowse(artist, tracks);
    await screen.findByRole("list", { name: "Tracks" });
    expect(vi.mocked(fetchMediaRows).mock.calls[0][2]).toEqual({ positions: false });
  });

  it("groups a container's tracks under a heading per stored sequence, playing on across groups", async () => {
    vi.mocked(fetchMediaRows).mockResolvedValue({
      t1: { ...rows.t1, sequence: 2, index: 1 },
      t2: { ...rows.t2, sequence: 1, index: 2 },
      t3: { ...rows.t3, sequence: 1, index: 1 },
    });
    renderBrowse(album, tracks);
    const first = await screen.findByRole("list", { name: "Disc 1" });
    expect(screen.getByRole("heading", { level: 3, name: "Disc 1" })).toBeInTheDocument();
    expect(
      within(first)
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(["1Track Three", "2Track TwoTest Artist, Guest Artist1:35"]);
    const second = screen.getByRole("list", { name: "Disc 2" });
    expect(within(second).getByRole("link").textContent).toBe("1Track One3:21");
    expect(screen.queryByRole("list", { name: "Tracks" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Play Track One" }));
    expect(screen.getByTestId("watch")).toHaveTextContent("/watch/t1|t3:audio,t2:audio,t1:audio");
  });

  it("splits an audio person's page into top tracks and the containers it appears on", async () => {
    vi.mocked(fetchSourceItems).mockResolvedValueOnce([
      { id: "t3", kind: "media", title: "Track Three" },
    ]);
    renderBrowse(artist, tracks);
    const top = await screen.findByRole("list", { name: "Top tracks" });
    await vi.waitFor(() =>
      expect(within(top).getAllByRole("button", { name: /^Play / })[0]).toHaveAccessibleName(
        "Play Track Three",
      ),
    );
    expect(screen.getByRole("heading", { name: "Appears on" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /^Test Mix/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /^Test Album/ })).not.toBeInTheDocument();
    // Every track already shows in the top list, so there is no second list.
    expect(screen.queryByRole("list", { name: "All tracks" })).not.toBeInTheDocument();
  });

  it("displays a person's top tracks in the same ranked order as the play queue", async () => {
    vi.mocked(fetchMediaRows).mockResolvedValue({
      t1: { ...rows.t1, sequence: 1, index: 1 },
      t2: { ...rows.t2, sequence: 1, index: 2 },
      t3: { ...rows.t3, sequence: 1, index: 8 },
    });
    vi.mocked(fetchSourceItems)
      .mockResolvedValueOnce([{ id: "other", kind: "media", title: "Another artist" }])
      .mockResolvedValueOnce([tracks[2], tracks[0]]);
    renderBrowse(artist, tracks);
    const top = within(await screen.findByRole("list", { name: "Top tracks" }));
    expect(fetchSourceItems).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ kind: "most-played", offset: 200 }),
      expect.any(AbortSignal),
    );
    await vi.waitFor(() =>
      expect(top.getAllByRole("link").map((link) => link.textContent)).toEqual([
        "1Track Three",
        "2Track One3:21",
        "3Track TwoTest Artist, Guest Artist1:35",
      ]),
    );

    const first = top.getAllByRole("button", { name: /^Play / })[0];
    expect(first).toHaveAccessibleName("Play Track Three");
    fireEvent.click(first);
    expect(screen.getByTestId("watch")).toHaveTextContent("/watch/t3|t3:audio,t1:audio,t2:audio");
  });

  it("stops after five most-played pages when a person's ranking has few matches", async () => {
    vi.mocked(fetchSourceItems).mockImplementation(async ({ offset = 0 }) => {
      if (offset === 0) return [tracks[2]];
      if (offset < 1000) return [{ id: "other", kind: "media", title: "Another artist" }];
      return [];
    });
    renderBrowse(artist, tracks);
    expect(await screen.findByRole("list", { name: "Top tracks" })).toBeInTheDocument();
    expect(
      vi.mocked(fetchSourceItems).mock.calls.map(([{ limit, offset }]) => [limit, offset]),
    ).toEqual([
      [200, 0],
      [200, 200],
      [200, 400],
      [200, 600],
      [200, 800],
    ]);
  });

  it("lists every track under the top tracks when the top list leaves some out (CORE-2362)", async () => {
    const many: SourceItem[] = Array.from({ length: 12 }, (_, index) => ({
      id: `m${index + 1}`,
      kind: "media",
      title: `Song ${index + 1}`,
    }));
    vi.mocked(fetchMediaRows).mockResolvedValue(
      Object.fromEntries(
        many.map((item, index) => [
          item.id,
          {
            id: item.id,
            title: item.title,
            favorited: false,
            mediaClass: "audio",
            sequence: 1,
            index: many.length - index,
            credits: [credit("person-a", "Test Artist", "person")],
          } as GenericMediaInfo,
        ]),
      ),
    );
    renderBrowse(artist, many);
    const all = await screen.findByRole("list", { name: "All tracks" });
    expect(
      within(all)
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(many.map((item, index) => `${index + 1}${item.title}`));
    const plays = within(all).getAllByRole("button", { name: /^Play / });
    expect(plays).toHaveLength(12);
    expect(plays[11]).toHaveAccessibleName("Play Song 12");
    const top = screen.getByRole("list", { name: "Tracks" });
    expect(
      within(top)
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(many.slice(0, 10).map((item, index) => `${index + 1}${item.title}`));
    expect(screen.queryByRole("heading", { name: "Top tracks" })).toBeNull();

    fireEvent.click(plays[11]);
    const watch = await screen.findByTestId("watch");
    expect(watch).toHaveTextContent("/watch/m12|");
    expect(watch).toHaveTextContent("m1:audio");
  });

  it("lists child rows when the listed Media is not audio", async () => {
    vi.mocked(fetchMediaRows).mockResolvedValue({
      ...rows,
      t2: { ...rows.t2, mediaClass: "video" } as GenericMediaInfo,
    });
    const { container } = renderBrowse(album, tracks);
    await vi.waitFor(() => expect(container.querySelector(".bf-children")).not.toBeNull());
    expect(screen.queryByRole("list", { name: "Tracks" })).not.toBeInTheDocument();
  });

  it("reads no rows for archetypes without a listening layout", () => {
    const { container } = renderBrowse({ ...album, archetype: undefined } as EntityInfo, tracks);
    expect(fetchMediaRows).not.toHaveBeenCalled();
    expect(container.querySelector(".bf-children")).not.toBeNull();
  });
});

describe("EntityMediaBrowse person filmography (CORE-2406)", () => {
  it("lists a person's video Media as cards, newest year first and undated last", () => {
    const actor: EntityInfo = {
      id: "person-v",
      kind: "person",
      title: "Lead",
      archetype: "person",
      fields: [],
    };
    renderBrowse(actor, [
      { id: "m-old", kind: "media", title: "Old Reel", year: 1999, mediaClass: "video" },
      { id: "m-undated", kind: "media", title: "Undated Reel", mediaClass: "video" },
      { id: "m-new", kind: "media", title: "New Reel", year: 2015, mediaClass: "video" },
    ]);
    const grid = screen.getByRole("list", { name: "Appears in" });
    const cards = within(grid).getAllByRole("listitem");
    expect(cards.map((card) => card.textContent)).toEqual([
      expect.stringContaining("New Reel"),
      expect.stringContaining("Old Reel"),
      expect.stringContaining("Undated Reel"),
    ]);
  });

  it("keeps each card's own title for items a container holds (CORE-2524)", () => {
    const actor: EntityInfo = {
      id: "person-v",
      kind: "person",
      title: "Lead",
      archetype: "person",
      fields: [],
    };
    renderBrowse(actor, [
      {
        id: "m-ep",
        kind: "media",
        title: "Pilot",
        year: 2020,
        mediaClass: "video",
        container: { id: "show-1", title: "Test Show" },
      },
    ]);
    const grid = screen.getByRole("list", { name: "Appears in" });
    expect(within(grid).getByRole("link", { name: /Pilot/ })).toBeInTheDocument();
    expect(within(grid).queryByText("Test Show")).toBeNull();
  });
});
