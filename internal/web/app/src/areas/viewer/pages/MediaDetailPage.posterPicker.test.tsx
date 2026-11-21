import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchArtworkCandidates, pinArtworkCandidate } from "@/lib/graphql";
import type { ArtworkCandidate, GenericMediaInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache } from "@/lib/viewerCache";
import { expectNoAxeViolations } from "@/test/axe";
import { fetchMedia } from "../lib/mediaDetail";
import { MediaDetailPage } from "./MediaDetailPage";

// Quick poster picker on the detail poster (CORE-2933).

const mocks = vi.hoisted(() => ({
  session: null as { profileID: string; profile: string } | null,
  isManager: false,
}));

vi.mock("../lib/mediaDetail", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/mediaDetail")>()),
  fetchMedia: vi.fn(),
  fetchMediaDetailFields: vi.fn(async () => []),
  fetchMediaRelations: vi.fn(async () => []),
  fetchMediaTracks: vi.fn(async () => []),
}));
vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fetchEntity: vi.fn(async () => null),
  fetchMediaForEntity: vi.fn(async () => []),
  fetchPrePlayQueue: vi.fn(async () => []),
  fetchRelatedMedia: vi.fn(async () => []),
  fetchSourceItems: vi.fn(async () => []),
  calibrateWatchPartyClock: vi.fn(async () => {
    throw new Error("home network only");
  }),
  fetchItemState: vi.fn(async () => ({
    id: "m1",
    kind: "media",
    watched: false,
    watchlisted: false,
    rating: 0,
  })),
  fetchArtworkCandidates: vi.fn(),
  pinArtworkCandidate: vi.fn(),
}));
vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => JSON.stringify(["viewer", "p1", "adult", false]),
  useSession: () => ({
    manifest: null,
    revealed: false,
    session: mocks.session,
    isManager: mocks.isManager,
  }),
}));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <main>{children}</main>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("../player/PlayerHost", () => ({ usePlayerHost: vi.fn() }));
vi.mock("../player/preload", () => ({ preloadPlayer: vi.fn() }));

const media: GenericMediaInfo = {
  id: "m1",
  title: "Server title",
  year: 2001,
  favorited: false,
  artworkSlots: { poster: "/api/media/artwork/m1/poster?v=1" },
} as GenericMediaInfo;

const candidates: ArtworkCandidate[] = [
  {
    id: "c1",
    slot: "poster",
    applied: true,
    pluginID: "tmdb",
    scraperID: "posters",
    previewURL: "/api/media/artwork-candidate/c1",
  },
  {
    id: "c2",
    slot: "poster",
    applied: false,
    pluginID: "fanart",
    scraperID: "",
    previewURL: "https://elsewhere.example/c2.jpg",
  },
];

function renderPage() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/media/m1"]}>
        <Routes>
          <Route path="/media/*" element={<MediaDetailPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

async function editControl() {
  await screen.findByRole("heading", { name: "Server title" });
  return screen.queryByRole("button", { name: "Change poster for Server title" });
}

beforeEach(() => {
  mocks.session = { profileID: "p1", profile: "adult" };
  mocks.isManager = true;
  vi.mocked(fetchMedia).mockReset().mockResolvedValue(media);
  vi.mocked(fetchArtworkCandidates).mockReset().mockResolvedValue(candidates);
  vi.mocked(pinArtworkCandidate)
    .mockReset()
    .mockResolvedValue({ ...candidates[1], applied: true });
});

afterEach(() => {
  cleanup();
  clearViewerCache();
  window.localStorage.clear();
});

describe("MediaDetailPage poster picker (CORE-2933)", () => {
  it("is not offered to a non-manager device or a child Profile", async () => {
    mocks.isManager = false;
    renderPage();
    expect(await editControl()).toBeNull();
    cleanup();

    mocks.isManager = true;
    mocks.session = { profileID: "p2", profile: "child" };
    renderPage();
    expect(await editControl()).toBeNull();
  });

  it("lists the poster candidates, pins the chosen one and reloads the item", async () => {
    renderPage();
    const control = await editControl();
    expect(control).not.toBeNull();
    expect(fetchArtworkCandidates).not.toHaveBeenCalled();

    fireEvent.click(control as HTMLElement);
    const dialog = await screen.findByRole("dialog", { name: "Choose a poster" });
    expect(fetchArtworkCandidates).toHaveBeenCalledWith("m1", "poster");

    const applied = await within(dialog).findByRole("button", {
      name: "Use the poster from tmdb / posters",
    });
    expect(applied).toHaveAttribute("aria-current", "true");
    expect(within(applied).getByText("Applied")).toBeInTheDocument();
    // Only a relative preview URL becomes an image; an absolute one never loads.
    const other = within(dialog).getByRole("button", { name: "Use the poster from fanart" });
    expect(other.querySelector("img")).toBeNull();
    await expectNoAxeViolations(dialog);

    expect(fetchMedia).toHaveBeenCalledTimes(1);
    fireEvent.click(other);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(pinArtworkCandidate).toHaveBeenCalledWith("m1", "poster", "c2");
    await waitFor(() => expect(fetchMedia).toHaveBeenCalledTimes(2));
  });

  it("says when there are no candidates, and keeps the dialog open when a pin fails", async () => {
    vi.mocked(fetchArtworkCandidates).mockResolvedValueOnce([]);
    renderPage();
    const control = (await editControl()) as HTMLElement;
    control.focus();
    fireEvent.click(control);
    const empty = await screen.findByRole("dialog");
    expect(await within(empty).findByRole("status")).toHaveTextContent(/No candidates yet/);
    fireEvent.click(within(empty).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(control));

    vi.mocked(pinArtworkCandidate).mockRejectedValueOnce(new Error("nope"));
    fireEvent.click((await editControl()) as HTMLElement);
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(
      await within(dialog).findByRole("button", { name: "Use the poster from fanart" }),
    );
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Could not update artwork.");
    expect(fetchMedia).toHaveBeenCalledTimes(1);
  });

  it("announces when the candidates cannot be loaded", async () => {
    vi.mocked(fetchArtworkCandidates).mockRejectedValueOnce(new Error("down"));
    renderPage();
    fireEvent.click((await editControl()) as HTMLElement);
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Artwork candidates are unavailable right now.",
    );
  });
});
