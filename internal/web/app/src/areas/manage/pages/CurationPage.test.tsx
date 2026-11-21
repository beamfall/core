import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import {
  fetchEntity,
  fetchEntityRelations,
  fetchMedia,
  fetchMediaFiles,
  fetchMediaRelations,
  linkMediaRelation,
  searchResult,
  upsertEntityRelation,
} from "@/lib/graphql";
import type { SearchResultItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { CurationPage } from "./CurationPage";

// Radix Select drives PointerEvents that jsdom does not implement; stub the
// capture probes so the dropdown opens under the test renderer.
beforeEach(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false;
    Element.prototype.setPointerCapture = () => {};
    Element.prototype.releasePointerCapture = () => {};
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
});

vi.mock("@/lib/graphql", () => ({
  fetchMedia: vi.fn(),
  fetchMediaFiles: vi.fn(),
  fetchMediaRelations: vi.fn(),
  fetchEntity: vi.fn(),
  fetchEntityRelations: vi.fn(),
  upsertEntityRelation: vi.fn(),
  linkMediaRelation: vi.fn(),
  searchResult: vi.fn(),
}));

function hit(id: string, title: string, entityKind?: string): SearchResultItem {
  return { id, title, entityKind, kind: entityKind ? "entity" : "media" } as SearchResultItem;
}

beforeEach(() => {
  vi.mocked(fetchMedia)
    .mockReset()
    .mockResolvedValue({ id: "media-1", title: "The Film" } as Awaited<
      ReturnType<typeof fetchMedia>
    >);
  vi.mocked(fetchMediaFiles)
    .mockReset()
    .mockResolvedValue([
      {
        mediaID: "media-1",
        path: "/media/part-1.mkv",
        contentFingerprint: "fp1",
        versionLabel: "1080p",
        partOrdinal: 1,
      },
      {
        mediaID: "media-1",
        path: "/media/part-2.mkv",
        contentFingerprint: "fp2",
        versionLabel: "1080p",
        partOrdinal: 2,
      },
    ]);
  vi.mocked(fetchMediaRelations).mockReset().mockResolvedValue([]);
  vi.mocked(fetchEntity)
    .mockReset()
    .mockResolvedValue({
      id: "entity-1",
      kind: "container",
      title: "The Collection",
      fields: [{ name: "title", type: "string", value: "The Collection" }],
    });
  vi.mocked(fetchEntityRelations)
    .mockReset()
    .mockResolvedValue([
      {
        relation: "contains",
        targetKind: "media",
        media: { id: "media-a", title: "Part One", favorited: false },
        attributes: {},
        sortOrder: 0,
      },
      {
        relation: "contains",
        targetKind: "media",
        media: { id: "media-b", title: "Part Two", favorited: false },
        attributes: {},
        sortOrder: 1,
      },
    ]);
  vi.mocked(upsertEntityRelation).mockReset().mockResolvedValue({
    relation: "contains",
    sourceID: "entity-1",
    targetID: "media-a",
  });
  vi.mocked(linkMediaRelation).mockReset().mockResolvedValue({
    relation: "extra-of",
    sourceID: "media-x",
    targetID: "media-1",
  });
  vi.mocked(searchResult)
    .mockReset()
    .mockResolvedValue({
      mode: "keyword",
      items: [hit("media-1", "The Film"), hit("entity-1", "The Collection", "container")],
      restrictedResultsPresent: false,
    } as Awaited<ReturnType<typeof searchResult>>);
});

function row(
  id: string,
  title: string,
  sortOrder: number | null,
  relation = "contains",
): Awaited<ReturnType<typeof fetchEntityRelations>>[number] {
  return {
    relation,
    targetKind: "media",
    media: { id, title, favorited: false },
    attributes: {},
    sortOrder: sortOrder ?? undefined,
  };
}

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.search}</output>;
}

function renderPage(entry = "/") {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <I18nProvider>
        <CurationPage />
        <LocationProbe />
      </I18nProvider>
    </MemoryRouter>,
  );
}

async function search(user: ReturnType<typeof userEvent.setup>, label: string, term: string) {
  await user.type(screen.getByLabelText(label), term);
  await user.click(screen.getByRole("button", { name: "Search" }));
}

describe("CurationPage", () => {
  it("says what the page is for before anything is open", () => {
    renderPage();
    expect(screen.getByText("Find an item or group")).toBeVisible();
    expect(screen.getByText("link extras and alternate editions to an item")).toBeVisible();
    expect(screen.getByLabelText("Search the catalog")).toBeVisible();
  });

  it("opens a search result and keeps it in the URL", async () => {
    const user = userEvent.setup();
    renderPage();
    await search(user, "Search the catalog", "film");

    await user.click(await screen.findByRole("button", { name: "Open: The Film" }));
    expect(screen.getByTestId("location")).toHaveTextContent("?open=media-1&as=media");
    expect(await screen.findByText("/media/part-1.mkv")).toBeVisible();
  });

  it("opens a group result as a group", async () => {
    const user = userEvent.setup();
    renderPage();
    await search(user, "Search the catalog", "collection");

    await user.click(await screen.findByRole("button", { name: "Open: The Collection" }));
    expect(screen.getByTestId("location")).toHaveTextContent("?open=entity-1&as=entity");
    expect(await screen.findByText("Part One")).toBeVisible();
  });

  it("shows an item's files with a link to edit its details", async () => {
    renderPage("/?open=media-1&as=media");

    expect(await screen.findByRole("heading", { name: "The Film" })).toBeVisible();
    expect(screen.getAllByText("1080p")).toHaveLength(2);
    expect(screen.getByText("/media/part-2.mkv")).toBeVisible();
    expect(screen.getByRole("link", { name: "Edit details" })).toHaveAttribute(
      "href",
      "/manage/items/media-1",
    );
    expect(screen.getByText("No extras are linked to this item.")).toBeVisible();
    expect(fetchMediaRelations).toHaveBeenCalledWith("media-1", "extra-of");
    expect(fetchMediaRelations).toHaveBeenCalledWith("media-1", "edition-of");
  });

  it("links a picked item as an extra of the open item", async () => {
    const user = userEvent.setup();
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [
        hit("media-1", "The Film"),
        hit("media-x", "Behind the Scenes"),
        hit("entity-1", "The Collection", "container"),
      ],
      restrictedResultsPresent: false,
    } as Awaited<ReturnType<typeof searchResult>>);
    renderPage("/?open=media-1&as=media");
    await screen.findByText("/media/part-1.mkv");

    await user.click(screen.getByRole("button", { name: "Add an extra" }));
    await search(user, "Add an extra", "behind");

    // The open item and groups cannot be linked as an extra.
    expect(
      await screen.findByRole("button", { name: "Link as extra: Behind the Scenes" }),
    ).toBeVisible();
    expect(screen.queryByRole("button", { name: "Link as extra: The Film" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Link as extra: The Collection" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Link as extra: Behind the Scenes" }));
    expect(linkMediaRelation).toHaveBeenCalledWith("media-x", "extra-of", "media-1");
  });

  it("reorders a group's items", async () => {
    const user = userEvent.setup();
    renderPage("/?open=entity-1&as=entity");

    expect(await screen.findByText("Part One")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Move down: Part One" }));
    await waitFor(() => expect(upsertEntityRelation).toHaveBeenCalledTimes(2));
    expect(upsertEntityRelation).toHaveBeenCalledWith({
      fromEntityID: "entity-1",
      relation: "contains",
      toEntityID: null,
      toMediaID: "media-b",
      sortOrder: 0,
    });
    expect(upsertEntityRelation).toHaveBeenCalledWith({
      fromEntityID: "entity-1",
      relation: "contains",
      toEntityID: null,
      toMediaID: "media-a",
      sortOrder: 1,
    });
    // The whole relation is read, in pages, before it is renumbered.
    expect(fetchEntityRelations).toHaveBeenCalledWith("entity-1", null, { limit: 200, offset: 0 });
  });

  it("numbers unranked items so a move takes effect", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchEntityRelations).mockResolvedValue([
      row("media-a", "Part One", null),
      row("media-b", "Part Two", null),
    ]);
    renderPage("/?open=entity-1&as=entity");

    await user.click(await screen.findByRole("button", { name: "Move down: Part One" }));
    await waitFor(() => expect(upsertEntityRelation).toHaveBeenCalledTimes(2));
    expect(upsertEntityRelation).toHaveBeenCalledWith(
      expect.objectContaining({ toMediaID: "media-b", sortOrder: 0 }),
    );
    expect(upsertEntityRelation).toHaveBeenCalledWith(
      expect.objectContaining({ toMediaID: "media-a", sortOrder: 1 }),
    );
  });

  it("moves items only within their own relation", async () => {
    vi.mocked(fetchEntityRelations).mockResolvedValue([
      row("media-a", "Part One", 0, "contains"),
      row("media-b", "Part Two", 0, "features"),
    ]);
    renderPage("/?open=entity-1&as=entity");

    expect(await screen.findByRole("button", { name: "Move down: Part One" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Move up: Part Two" })).toBeDisabled();
  });

  it("reads every page of a large group", async () => {
    const first = Array.from({ length: 200 }, (_, i) => row(`media-${i}`, `Item ${i}`, i));
    vi.mocked(fetchEntityRelations)
      .mockResolvedValueOnce(first)
      .mockResolvedValueOnce([row("media-last", "Last Item", 200)]);
    renderPage("/?open=entity-1&as=entity");

    expect(await screen.findByText("Last Item")).toBeVisible();
    expect(fetchEntityRelations).toHaveBeenCalledWith("entity-1", null, {
      limit: 200,
      offset: 200,
    });
  });

  it("reloads after a move fails partway", async () => {
    const user = userEvent.setup();
    vi.mocked(upsertEntityRelation)
      .mockResolvedValueOnce({ relation: "contains", sourceID: "entity-1", targetID: "media-b" })
      .mockRejectedValueOnce(new Error("boom"));
    renderPage("/?open=entity-1&as=entity");

    await user.click(await screen.findByRole("button", { name: "Move down: Part One" }));
    expect(await screen.findByText("That change was not saved. Try again.")).toBeVisible();
    expect(fetchEntityRelations).toHaveBeenCalledTimes(2);
  });

  it("disables the controls while a change is saving", async () => {
    const user = userEvent.setup();
    let finish: () => void = () => {};
    vi.mocked(upsertEntityRelation).mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = () => resolve({ relation: "contains", sourceID: "entity-1", targetID: "x" });
        }),
    );
    renderPage("/?open=entity-1&as=entity");

    await user.click(await screen.findByRole("button", { name: "Move down: Part One" }));
    expect(screen.getByRole("button", { name: "Move up: Part Two" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add an item" })).toBeDisabled();
    finish();
  });

  it("adds an item to the end of a group", async () => {
    const user = userEvent.setup();
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [hit("media-c", "Part Three")],
      restrictedResultsPresent: false,
    } as Awaited<ReturnType<typeof searchResult>>);
    renderPage("/?open=entity-1&as=entity");
    await screen.findByText("Part One");

    await user.click(screen.getByRole("button", { name: "Add an item" }));
    await search(user, "Add an item", "three");
    await user.click(await screen.findByRole("button", { name: "Add to group: Part Three" }));

    expect(upsertEntityRelation).toHaveBeenCalledWith({
      fromEntityID: "entity-1",
      relation: "contains",
      toEntityID: null,
      toMediaID: "media-c",
      sortOrder: 2,
    });
    // Already numbered 0 and 1, so nothing else is rewritten.
    expect(upsertEntityRelation).toHaveBeenCalledTimes(1);
  });

  it("does not rewrite an item that is already in the group", async () => {
    const user = userEvent.setup();
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [hit("media-b", "Part Two")],
      restrictedResultsPresent: false,
    } as Awaited<ReturnType<typeof searchResult>>);
    renderPage("/?open=entity-1&as=entity");
    await screen.findByText("Part One");

    await user.click(screen.getByRole("button", { name: "Add an item" }));
    await search(user, "Add an item", "two");
    await user.click(await screen.findByRole("button", { name: "Add to group: Part Two" }));

    expect(await screen.findByText("That is already in this group.")).toBeVisible();
    expect(upsertEntityRelation).not.toHaveBeenCalled();
  });

  it("adds a group found by search as a group", async () => {
    const user = userEvent.setup();
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      // A group row without its display kind is still a group.
      items: [{ id: "entity-2", title: "Sub Group", kind: "entity" } as SearchResultItem],
      restrictedResultsPresent: false,
    } as Awaited<ReturnType<typeof searchResult>>);
    renderPage("/?open=entity-1&as=entity");
    await screen.findByText("Part One");

    await user.click(screen.getByRole("button", { name: "Add an item" }));
    await search(user, "Add an item", "sub");
    await user.click(await screen.findByRole("button", { name: "Add to group: Sub Group" }));

    expect(upsertEntityRelation).toHaveBeenCalledWith(
      expect.objectContaining({ toEntityID: "entity-2", toMediaID: null, sortOrder: 2 }),
    );
  });

  it("numbers unranked items before adding one after them", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchEntityRelations).mockResolvedValue([
      row("media-a", "Part One", 0),
      row("media-b", "Part Two", null),
    ]);
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      items: [hit("media-c", "Part Three")],
      restrictedResultsPresent: false,
    } as Awaited<ReturnType<typeof searchResult>>);
    renderPage("/?open=entity-1&as=entity");
    await screen.findByText("Part One");

    await user.click(screen.getByRole("button", { name: "Add an item" }));
    await search(user, "Add an item", "three");
    await user.click(await screen.findByRole("button", { name: "Add to group: Part Three" }));

    await waitFor(() => expect(upsertEntityRelation).toHaveBeenCalledTimes(2));
    expect(vi.mocked(upsertEntityRelation).mock.calls[0][0]).toMatchObject({
      toMediaID: "media-b",
      sortOrder: 1,
    });
    expect(vi.mocked(upsertEntityRelation).mock.calls[1][0]).toMatchObject({
      toMediaID: "media-c",
      sortOrder: 2,
    });
  });

  it("loads more search results on request", async () => {
    const user = userEvent.setup();
    vi.mocked(searchResult)
      .mockResolvedValueOnce({
        mode: "keyword",
        hasMore: true,
        items: [hit("media-1", "The Film")],
        restrictedResultsPresent: false,
      } as Awaited<ReturnType<typeof searchResult>>)
      .mockResolvedValueOnce({
        mode: "keyword",
        hasMore: false,
        items: [hit("media-2", "The Sequel")],
        restrictedResultsPresent: false,
      } as Awaited<ReturnType<typeof searchResult>>);
    renderPage();
    await search(user, "Search the catalog", "the");
    await screen.findByRole("button", { name: "Open: The Film" });

    await user.click(screen.getByRole("button", { name: "More results" }));
    expect(await screen.findByRole("button", { name: "Open: The Sequel" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Open: The Film" })).toBeVisible();
    expect(vi.mocked(searchResult).mock.calls[1][3]).toEqual({ offset: 1 });
    expect(screen.queryByRole("button", { name: "More results" })).toBeNull();
  });

  it("says so when the open item cannot be loaded", async () => {
    vi.mocked(fetchMedia).mockRejectedValue(new Error("boom"));
    renderPage("/?open=media-1&as=media");
    expect(await screen.findByText("Could not load it. Try again.")).toBeVisible();
    expect(screen.getByRole("button", { name: "Choose another" })).toBeVisible();
  });

  it("says so when an item no longer exists", async () => {
    vi.mocked(fetchMediaFiles).mockRejectedValue(new ApiError("unknown media", 200));
    renderPage("/?open=media-1&as=media");
    expect(
      await screen.findByText("That item or group no longer exists, or is hidden from you."),
    ).toBeVisible();
  });

  it("says so when the server has no such item", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(null);
    renderPage("/?open=media-1&as=media");
    expect(
      await screen.findByText("That item or group no longer exists, or is hidden from you."),
    ).toBeVisible();
  });

  it("says so when a group is hidden from the caller", async () => {
    vi.mocked(fetchEntity).mockRejectedValue(new ApiError("unknown entity", 200));
    renderPage("/?open=entity-1&as=entity");
    expect(
      await screen.findByText("That item or group no longer exists, or is hidden from you."),
    ).toBeVisible();
  });

  it("says so when a group no longer exists", async () => {
    vi.mocked(fetchEntity).mockResolvedValue(null);
    renderPage("/?open=entity-1&as=entity");
    expect(
      await screen.findByText("That item or group no longer exists, or is hidden from you."),
    ).toBeVisible();
  });
});
