import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { GenericMediaInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache, readViewerCache, writeViewerCache } from "@/lib/viewerCache";
import { fetchMedia, mediaDetailCacheKey } from "../lib/mediaDetail";
import { CastCrewPage, castCrewPath } from "./CastCrewPage";

vi.mock("../lib/mediaDetail", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/mediaDetail")>()),
  fetchMedia: vi.fn(),
}));
const identity = JSON.stringify(["viewer", "p1", "adult", false]);
vi.mock("@/lib/session", () => ({ useViewerIdentity: () => identity }));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <main>{children}</main>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

const credit = (entityID: string, title: string, attributes: Record<string, string>) => ({
  entityID,
  title,
  kind: "person",
  archetype: "person",
  relation: "credited-in",
  attributes,
});

const media: GenericMediaInfo = {
  id: "m1",
  title: "A Beautiful Mind",
  favorited: false,
  credits: [
    credit("rc", "Russell Crowe", { character: "John Nash" }),
    credit("jc", "Jennifer Connelly", { character: "Alicia Nash" }),
    credit("rh", "Ron Howard", { job: "Director, Producer", department: "Directing, Production" }),
    credit("ag", "Akiva Goldsman", { job: "Screenplay", department: "Writing" }),
  ],
};

function MediaLocation() {
  return <p>{useLocation().pathname}</p>;
}

function renderAt(path: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/credits/*" element={<CastCrewPage />} />
          <Route path="/media/*" element={<MediaLocation />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  clearViewerCache();
  vi.clearAllMocks();
});

describe("CastCrewPage (CORE-2503)", () => {
  it("preserves the original direct credits address when Back precedes resolution", () => {
    const id = "media-sample1-a371bff46385f7ded";
    vi.mocked(fetchMedia).mockImplementationOnce(() => new Promise(() => {}));
    renderAt(castCrewPath(id));

    expect(fetchMedia).toHaveBeenCalledWith(id, expect.any(AbortSignal));
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByText(`/media/${id}`)).toBeVisible();
  });

  it("lists the cast and then each department in billing order", async () => {
    vi.mocked(fetchMedia).mockResolvedValue(media);
    renderAt(castCrewPath("m1"));

    expect(await screen.findByRole("heading", { level: 1, name: "Cast & Crew" })).toBeVisible();
    expect(screen.getByText("A Beautiful Mind")).toBeVisible();
    const names = (label: string) =>
      within(screen.getByRole("list", { name: label }))
        .getAllByRole("link")
        .map((link) => link.getAttribute("href"));
    expect(names("Cast")).toEqual(["/entity/rc", "/entity/jc"]);
    expect(names("Directing")).toEqual(["/entity/rh"]);
    expect(names("Writing")).toEqual(["/entity/ag"]);
    expect(names("Production")).toEqual(["/entity/rh"]);
  });

  it("shows not available when a cached item is gone, and evicts it", async () => {
    writeViewerCache(identity, mediaDetailCacheKey("m1"), { media, fields: [] });
    expect(readViewerCache(identity, mediaDetailCacheKey("m1"))).toBeDefined();
    vi.mocked(fetchMedia).mockResolvedValue(null);
    renderAt(castCrewPath("m1"));

    expect(await screen.findByRole("heading", { name: "Not available" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Cast & Crew" })).toBeNull();
    expect(readViewerCache(identity, mediaDetailCacheKey("m1"))).toBeUndefined();
  });
});
