import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createCollection, fetchCollections } from "@/lib/graphql";
import type { CollectionInfo, LibrarySummary } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchCollectionPosters } from "../lib/collections";
import { CollectionListPage } from "./CollectionListPage";

const session = vi.hoisted(() => ({
  isManager: true,
  libraries: [] as LibrarySummary[],
}));

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    isManager: session.isManager,
    manifest: { libraries: session.libraries },
  }),
  useViewerIdentity: () => "viewer-1",
}));
vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return { ...actual, fetchCollections: vi.fn(), createCollection: vi.fn() };
});
vi.mock("../lib/collections", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/collections")>();
  return { ...actual, fetchCollectionPosters: vi.fn() };
});
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

function library(id: string, title: string): LibrarySummary {
  return { id, title, pluginID: null, previewClips: false };
}

function collection(id: string, libraryID: string, name: string, count = 0): CollectionInfo {
  return { id, libraryID, name, sortOrder: 0, memberCount: count, members: [] };
}

function Probe() {
  return <p data-testid="path">{useLocation().pathname}</p>;
}

function renderPage() {
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/collections"]}>
        <Routes>
          <Route path="/collections" element={<CollectionListPage />} />
          <Route path="*" element={<Probe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchCollectionPosters).mockResolvedValue(new Map());
  session.isManager = true;
  session.libraries = [library("lib-a", "Videos"), library("lib-b", "Home clips")];
});

afterEach(() => {
  cleanup();
  vi.mocked(fetchCollections).mockReset();
  vi.mocked(createCollection).mockReset();
  vi.mocked(fetchCollectionPosters).mockReset();
});

describe("CollectionListPage", () => {
  it("lists every visible collection grouped by library, each linking to its page", async () => {
    vi.mocked(fetchCollections).mockImplementation(async (libraryID) =>
      libraryID === "lib-a" ? [collection("c1", "lib-a", "Weekend set", 3)] : [],
    );

    renderPage();

    const link = await screen.findByRole("link", { name: "Weekend set" });
    expect(link).toHaveAttribute("href", "/collections/lib-a/c1");
    const group = screen.getByRole("region", { name: "Videos" });
    expect(within(group).getByText("3 items")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Home clips" })).toBeNull();
    expect(fetchCollections).toHaveBeenCalledWith("lib-a");
    expect(fetchCollections).toHaveBeenCalledWith("lib-b");
  });

  it("draws each collection as a poster collage of its first members (CORE-2438)", async () => {
    vi.mocked(fetchCollections).mockImplementation(async (libraryID) =>
      libraryID === "lib-a"
        ? [
            collection("c1", "lib-a", "Weekend set", 5),
            collection("c2", "lib-a", "Empty set", 0),
            collection("c3", "lib-a", "No art", 2),
          ]
        : [],
    );
    vi.mocked(fetchCollectionPosters).mockResolvedValue(
      new Map([
        ["c1", ["/a/1", "/a/2", "/a/3", "/a/4"]],
        ["c3", []],
      ]),
    );

    renderPage();

    const link = await screen.findByRole("link", { name: "Weekend set" });
    await waitFor(() => expect(link.querySelectorAll("img")).toHaveLength(4));
    expect(link.querySelector("img")).toHaveAttribute("src", "/a/1?bucket=w320");
    expect(screen.getByText("5 items")).toBeInTheDocument();
    // Only collections with members are read, in one call.
    expect(fetchCollectionPosters).toHaveBeenCalledTimes(1);
    expect([...vi.mocked(fetchCollectionPosters).mock.calls[0][0]].sort()).toEqual(["c1", "c3"]);
    for (const name of ["Empty set", "No art"]) {
      const card = screen.getByRole("link", { name });
      expect(card.querySelector("img")).toBeNull();
      expect(card.querySelector("[data-glyph]")).not.toBeNull();
    }
  });

  it("creates a collection in the chosen library and opens it", async () => {
    vi.mocked(fetchCollections).mockResolvedValue([]);
    vi.mocked(createCollection).mockResolvedValue(collection("new-1", "lib-b", "Road trip"));
    const user = userEvent.setup();

    renderPage();

    await screen.findByText("No collections yet.");
    await user.type(screen.getByLabelText("Name"), "  Road trip ");
    await user.selectOptions(screen.getByLabelText("Library"), "lib-b");
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(createCollection).toHaveBeenCalledWith({ libraryID: "lib-b", name: "Road trip" }),
    );
    expect(await screen.findByTestId("path")).toHaveTextContent("/collections/lib-b/new-1");
  });

  it("shows no create form on a viewer device", async () => {
    session.isManager = false;
    vi.mocked(fetchCollections).mockResolvedValue([]);

    renderPage();

    await screen.findByText("No collections yet.");
    expect(screen.queryByRole("form", { name: "New collection" })).toBeNull();
  });

  it("reports a failed load and retries", async () => {
    vi.mocked(fetchCollections).mockRejectedValueOnce(new Error("offline")).mockResolvedValue([]);
    const user = userEvent.setup();

    renderPage();

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Could not load collections.");
    await user.click(within(alert).getByRole("button"));
    expect(await screen.findByText("No collections yet.")).toBeInTheDocument();
  });
});
