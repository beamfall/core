import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchEntity, fetchMediaForEntity } from "@/lib/graphql";
import type { EntityInfo, Layout, SourceItem } from "@/lib/graphql/types";
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
vi.mock("@/lib/session", () => ({
  useSession: () => ({ manifest: { layouts: mocks.layouts } }),
  useViewerIdentity: () => mocks.identity,
}));
vi.mock("@/lib/i18n", () => ({ useDisplayLocale: () => "en" }));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <main>{children}</main>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("../actions", () => ({ ItemActions: () => null }));
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
  fields: [{ name: "Year", type: "string", value: "2001" }],
};
const related: SourceItem = {
  id: "m1",
  kind: "media",
  title: "Related item",
  artwork: { poster: "" },
};

function renderEntity(state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[{ pathname: "/entity/e1", state }]}>
      <Routes>
        <Route path="/entity/:entityId" element={<EntityPage />} />
      </Routes>
    </MemoryRouter>,
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

  it("starts the detail Layout from the previewed kind without fetching the related list", () => {
    mocks.layouts = [{ id: "series-detail", sections: [], role: "detail", entityKind: "Series" }];
    vi.mocked(fetchEntity).mockReturnValue(new Promise(() => {}));

    renderEntity(entityLinkState({ id: "e1", title: "Card title", kind: "Series" }, identity));

    expect(screen.getByText("detail layout series-detail")).toBeInTheDocument();
    expect(fetchMediaForEntity).not.toHaveBeenCalled();
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

  it("renders related groups before a detail Layout", async () => {
    mocks.layouts = [{ id: "rack-detail", sections: [], role: "detail", entityKind: "rack" }];
    vi.mocked(fetchEntity).mockResolvedValue(withGroups);

    renderEntity();

    const box = await screen.findByText("Linked box");
    const layout = screen.getByText("detail layout rack-detail");
    expect(box.compareDocumentPosition(layout) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
