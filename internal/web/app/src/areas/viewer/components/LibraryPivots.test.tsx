import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LibrarySidebar } from "@/components/chrome/LibrarySidebar";
import type { NavigationNode } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { pivotOfPath, readLibraryPivot, rememberedLibraryPath } from "@/lib/libraryPivot";
import { LibraryPivots } from "./LibraryPivots";

// The pivots ask the manifest which plugin a Library runs (CORE-3362).
const session = vi.hoisted(() => ({
  manifest: { libraries: [] as { id: string; pluginID: string | null }[] },
}));
vi.mock("@/lib/session", () => ({ useSession: () => session }));

// CORE-2826: reopening a Library returns to the pivot last used there.

const NODE = {
  id: "library-library-films",
  title: "Films",
  view: "layout",
  layoutID: "l",
  libraryID: "library-films",
} satisfies NavigationNode;
const NAVIGATION: NavigationNode[] = [
  { id: "home", title: "Home", view: "layout", layoutID: "l" },
  NODE,
];

function Where() {
  const location = useLocation();
  return <output data-testid="where">{location.pathname}</output>;
}

function LibraryPage() {
  return (
    <>
      <LibraryPivots nodeID={NODE.id} libraryID={NODE.libraryID} />
      <Link to="/">Go home</Link>
    </>
  );
}

function renderAt(path: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Where />
        <Routes>
          <Route
            path="/"
            element={
              <LibrarySidebar
                id="side"
                navigation={NAVIGATION}
                libraries={[
                  { id: "library-films", title: "Films", pluginID: null, previewClips: false },
                ]}
                activeNodeId="home"
                collapsed={false}
                drawer={false}
                onNavigate={() => {}}
              />
            }
          />
          <Route path="/n/:nodeId/*" element={<LibraryPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => window.localStorage.clear());

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.localStorage.clear();
});

describe("remembered Library pivot", () => {
  it("reopens the Library on the pivot last used from the sidebar", async () => {
    const user = userEvent.setup();
    renderAt("/n/library-films");
    await user.click(screen.getByRole("link", { name: "Library" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/n/library-films/all");

    await user.click(screen.getByRole("link", { name: "Go home" }));
    const films = screen.getByRole("link", { name: "Films" });
    expect(films).toHaveAttribute("href", "/n/library-films/all");

    await user.click(films);
    expect(screen.getByTestId("where")).toHaveTextContent("/n/library-films/all");
    expect(screen.getByRole("link", { name: "Library" })).toHaveAttribute("aria-current", "page");
  });

  it("lets an explicit address win and remembers it in turn", () => {
    window.localStorage.setItem("beamfall.libraryPivot.library-library-films", "/all");
    renderAt("/n/library-films");
    expect(screen.getByRole("link", { name: "Recommended" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(readLibraryPivot(NODE.libraryID)).toBe("");
  });

  it("falls back to Recommended when storage is blocked or holds an unknown value", () => {
    window.localStorage.setItem("beamfall.libraryPivot.library-library-films", "/elsewhere");
    expect(rememberedLibraryPath(NODE.id, NODE.libraryID)).toBe("/n/library-films");

    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    renderAt("/");
    expect(screen.getByRole("link", { name: "Films" })).toHaveAttribute("href", "/n/library-films");
  });

  it("keeps the open Library's link on the pivot on screen", () => {
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/library-films/collections"]}>
          <LibrarySidebar
            id="side"
            navigation={NAVIGATION}
            libraries={[
              { id: "library-films", title: "Films", pluginID: null, previewClips: false },
            ]}
            activeNodeId={NODE.id}
            collapsed={false}
            drawer={false}
            onNavigate={() => {}}
            pathname="/n/library-films/collections"
          />
        </MemoryRouter>
      </I18nProvider>,
    );
    expect(screen.getByRole("link", { name: "Films" })).toHaveAttribute(
      "href",
      "/n/library-films/collections",
    );
  });

  it("reads the pivot from a Library address", () => {
    expect(pivotOfPath("/n/films")).toBe("");
    expect(pivotOfPath("/n/films/all")).toBe("/all");
    expect(pivotOfPath("/n/films/collections")).toBe("/collections");
    expect(pivotOfPath("/n/films/categories/")).toBe("/categories");
    expect(pivotOfPath("/n/films/unknown")).toBe("");
  });
});

describe("karaoke Library pivots", () => {
  afterEach(() => {
    session.manifest.libraries = [];
  });

  it("opens on the Songbook and keeps Collections and Categories", () => {
    session.manifest.libraries = [{ id: NODE.libraryID, pluginID: "karaoke" }];
    renderAt("/n/library-films");
    const links = screen.getByRole("navigation", { name: "Library views" }).querySelectorAll("a");
    expect([...links].map((link) => link.textContent)).toEqual([
      "Songbook",
      "Collections",
      "Categories",
    ]);
    expect(screen.getByRole("link", { name: "Songbook" })).toHaveAttribute("aria-current", "page");
  });

  it("keeps the generic pivots for any other Library", () => {
    session.manifest.libraries = [{ id: NODE.libraryID, pluginID: "films" }];
    renderAt("/n/library-films");
    expect(screen.getByRole("link", { name: "Recommended" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Songbook" })).toBeNull();
  });
});
