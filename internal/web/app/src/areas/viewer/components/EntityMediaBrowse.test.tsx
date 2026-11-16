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

vi.mock("@/lib/session", () => ({
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
    const buttons = within(list).getAllByRole("button", { name: /^Play / });
    expect(buttons.map((button) => button.textContent)).toEqual([
      "1Track One3:21",
      "2Track TwoTest Artist, Guest Artist1:35",
      "3Track Three",
    ]);
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
    await screen.findByRole("list", { name: "Top tracks" });
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
        .getAllByRole("button", { name: /^Play / })
        .map((button) => button.textContent),
    ).toEqual(["1Track Three", "2Track TwoTest Artist, Guest Artist1:35"]);
    const second = screen.getByRole("list", { name: "Disc 2" });
    expect(
      within(second)
        .getAllByRole("button", { name: /^Play / })
        .map((button) => button.textContent),
    ).toEqual(["1Track One3:21"]);
    expect(screen.queryByRole("list", { name: "Tracks" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Play Track One" }));
    expect(screen.getByTestId("watch")).toHaveTextContent("/watch/t1|t3:audio,t2:audio,t1:audio");
  });

  it("splits an audio person's page into top tracks and the containers it appears on", async () => {
    vi.mocked(fetchSourceItems).mockResolvedValue([
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
    expect(screen.getByRole("button", { name: /^Test Mix/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Test Album/ })).not.toBeInTheDocument();
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
