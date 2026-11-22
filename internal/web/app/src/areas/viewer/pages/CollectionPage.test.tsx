import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  addCollectionMedia,
  removeCollectionMedia,
  reorderCollectionMedia,
  searchResult,
} from "@/lib/graphql";
import type { CollectionInfo, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchCollection, fetchCollectionItems } from "../lib/collections";
import { CollectionPage } from "./CollectionPage";

const session = vi.hoisted(() => ({ isManager: true }));

vi.mock("@/lib/session", () => ({
  useSession: () => ({ isManager: session.isManager }),
  useViewerIdentity: () => "viewer-1",
}));
vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return {
    ...actual,
    addCollectionMedia: vi.fn(),
    removeCollectionMedia: vi.fn(),
    reorderCollectionMedia: vi.fn(),
    searchResult: vi.fn(),
  };
});
vi.mock("../lib/collections", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/collections")>();
  return { ...actual, fetchCollection: vi.fn(), fetchCollectionItems: vi.fn() };
});
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("../player/preload", () => ({ preloadPlayer: vi.fn() }));

// Members in curated order; "m-hidden" is a member the session cannot see.
const COLLECTION: CollectionInfo = {
  id: "c1",
  libraryID: "lib-a",
  name: "Weekend set",
  sortOrder: 0,
  memberCount: 4,
  members: [
    { mediaID: "m1", sortOrder: 1, addedAt: "" },
    { mediaID: "m-hidden", sortOrder: 2, addedAt: "" },
    { mediaID: "m2", sortOrder: 3, addedAt: "" },
    { mediaID: "m3", sortOrder: 4, addedAt: "" },
  ],
};
const ITEMS: SourceItem[] = [
  { id: "m1", kind: "media", title: "Opening" },
  { id: "m2", kind: "media", title: "Middle" },
  { id: "m3", kind: "media", title: "Closing" },
];

function Probe() {
  const location = useLocation();
  return (
    <>
      <p data-testid="path">{location.pathname}</p>
      <p data-testid="state">{JSON.stringify(location.state)}</p>
    </>
  );
}

function renderPage() {
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/collections/lib-a/c1"]}>
        <Routes>
          <Route path="/collections/:libraryId/:collectionId" element={<CollectionPage />} />
          <Route path="*" element={<Probe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  session.isManager = true;
  vi.mocked(fetchCollection).mockResolvedValue(COLLECTION);
  vi.mocked(fetchCollectionItems).mockResolvedValue(ITEMS);
});

afterEach(() => {
  cleanup();
  vi.mocked(fetchCollection).mockReset();
  vi.mocked(fetchCollectionItems).mockReset();
  vi.mocked(addCollectionMedia).mockReset();
  vi.mocked(removeCollectionMedia).mockReset();
  vi.mocked(reorderCollectionMedia).mockReset();
  vi.mocked(searchResult).mockReset();
});

describe("CollectionPage", () => {
  it("shows the collection's entries in curated order", async () => {
    renderPage();

    expect(await screen.findByRole("heading", { level: 1, name: "Weekend set" })).toBeVisible();
    expect(screen.getByText("3 items")).toBeInTheDocument();
    const cards = document.querySelectorAll(".bf-card");
    expect([...cards].map((card) => card.textContent)).toEqual([
      expect.stringContaining("Opening"),
      expect.stringContaining("Middle"),
      expect.stringContaining("Closing"),
    ]);
    expect(fetchCollection).toHaveBeenCalledWith("lib-a", "c1");
    expect(fetchCollectionItems).toHaveBeenCalledWith("c1");
  });

  it("plays the whole collection in order as the player's queue", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Play all" }));

    expect(screen.getByTestId("path")).toHaveTextContent("/watch/m1");
    const state = JSON.parse(screen.getByTestId("state").textContent ?? "{}");
    expect(state.title).toBe("Opening");
    expect(state.queueIdentity).toBe("viewer-1");
    expect(state.queue.map((entry: { id: string }) => entry.id)).toEqual(["m1", "m2", "m3"]);
  });

  it("reorders with the full member order, keeping hidden members in place", async () => {
    const reordered: CollectionInfo = { ...COLLECTION };
    vi.mocked(reorderCollectionMedia).mockResolvedValue(reordered);
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Edit" }));
    await user.click(screen.getByRole("button", { name: "Move Opening down" }));

    await waitFor(() =>
      expect(reorderCollectionMedia).toHaveBeenCalledWith("c1", ["m2", "m-hidden", "m1", "m3"]),
    );
    await waitFor(() => expect(fetchCollectionItems).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("button", { name: "Move Opening up" })).toBeDisabled();
  });

  it("removes an entry and reports a failed save", async () => {
    vi.mocked(removeCollectionMedia).mockRejectedValueOnce(new Error("refused"));
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Edit" }));
    await user.click(screen.getByRole("button", { name: "Remove Middle" }));

    expect(removeCollectionMedia).toHaveBeenCalledWith("c1", ["m2"]);
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not save the change.");
  });

  it("adds an entry found in the collection's own library only", async () => {
    vi.mocked(searchResult).mockResolvedValue({
      mode: "keyword",
      restrictedResultsPresent: false,
      items: [
        { id: "m9", kind: "media", title: "Encore", mode: "keyword", score: 1, libraryID: "lib-a" },
        {
          id: "x1",
          kind: "media",
          title: "Elsewhere",
          mode: "keyword",
          score: 1,
          libraryID: "lib-b",
        },
        {
          id: "m1",
          kind: "media",
          title: "Opening",
          mode: "keyword",
          score: 1,
          libraryID: "lib-a",
        },
      ],
    });
    vi.mocked(addCollectionMedia).mockResolvedValue(COLLECTION);
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Edit" }));
    await user.type(screen.getByRole("searchbox", { name: "Search this library" }), "enc{Enter}");

    const add = await screen.findByRole("button", { name: "Add Encore" });
    expect(screen.queryByRole("button", { name: "Add Elsewhere" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Add Opening" })).toBeNull();
    await user.click(add);

    await waitFor(() => expect(addCollectionMedia).toHaveBeenCalledWith("c1", ["m9"]));
    expect(await screen.findByText("Added Encore.")).toBeInTheDocument();
  });

  it("keeps the screensaver away while a search term waits for its submit", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Edit" }));
    expect(screen.getByRole("searchbox", { name: "Search this library" })).toHaveAttribute(
      "data-unsaved-until-submit",
    );
  });

  it("offers no editing on a viewer device", async () => {
    session.isManager = false;
    renderPage();

    await screen.findByRole("heading", { level: 1, name: "Weekend set" });
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("says when the collection is not visible", async () => {
    vi.mocked(fetchCollection).mockResolvedValue(null);
    renderPage();

    expect(await screen.findByText("This collection is not available.")).toBeInTheDocument();
  });
});
