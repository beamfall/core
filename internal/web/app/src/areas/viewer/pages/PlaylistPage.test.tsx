import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchCollections } from "@/lib/graphql";
import { fetchMediaRows } from "@/lib/graphql/browse";
import type { CollectionInfo, GenericMediaInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { fetchEntryLocks } from "../lib/collections";
import type { PlayerNavState } from "../lib/watchQueue";
import { PlaylistPage } from "./PlaylistPage";

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    isManager: false,
    manifest: {
      libraries: [{ id: "lib-a", title: "Sounds", pluginID: null, previewClips: false }],
    },
  }),
  useViewerIdentity: () => "viewer-1",
}));
vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return { ...actual, fetchCollections: vi.fn() };
});
vi.mock("@/lib/graphql/browse", () => ({ fetchMediaRows: vi.fn() }));
vi.mock("../lib/collections", () => ({ fetchEntryLocks: vi.fn() }));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

const playlist: CollectionInfo = {
  id: "pl-1",
  libraryID: "lib-a",
  name: "Evening list",
  sortOrder: 0,
  memberCount: 3,
  members: [
    { mediaID: "m-1", sortOrder: 0 },
    { mediaID: "m-2", sortOrder: 1 },
    { mediaID: "m-3", sortOrder: 2 },
  ],
};

function row(id: string, title: string): GenericMediaInfo {
  return { id, title, durationSeconds: 60 } as GenericMediaInfo;
}

function WatchProbe() {
  const location = useLocation();
  const state = location.state as PlayerNavState | null;
  return (
    <p data-testid="watch">
      {location.pathname}|{(state?.queue ?? []).map((item) => item.id).join(",")}
    </p>
  );
}

function renderPage() {
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/playlists/pl-1"]}>
        <Routes>
          <Route path="/playlists/:playlistId" element={<PlaylistPage />} />
          <Route path="/watch/*" element={<WatchProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchCollections).mockResolvedValue([playlist] as never);
  vi.mocked(fetchMediaRows).mockResolvedValue({
    "m-1": row("m-1", "First tone"),
    "m-2": row("m-2", "Second tone"),
    "m-3": row("m-3", "Third tone"),
  } as never);
});

afterEach(() => {
  cleanup();
  vi.mocked(fetchCollections).mockReset();
  vi.mocked(fetchMediaRows).mockReset();
  vi.mocked(fetchEntryLocks).mockReset();
});

describe("PlaylistPage locked entries (CORE-1876)", () => {
  it("lists a locked entry as unplayable and leaves it out of the Play all queue", async () => {
    vi.mocked(fetchEntryLocks).mockResolvedValue(
      new Map([
        ["m-1", false],
        ["m-2", true],
        ["m-3", false],
      ]),
    );
    renderPage();

    const locked = await screen.findByRole("button", { name: "Play Second tone" });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Play First tone" })).toBeEnabled(),
    );
    expect(locked).toBeDisabled();
    expect(fetchEntryLocks).toHaveBeenCalledWith("pl-1", 3, expect.any(AbortSignal));

    await userEvent.click(screen.getByRole("button", { name: "Play all" }));
    expect(screen.getByTestId("watch")).toHaveTextContent("/watch/m-1|m-1,m-3");
  });

  it("fails closed on a member with no lock state (CORE-2843)", async () => {
    // The lock feed drops muted entries, so m-2 may or may not be locked.
    vi.mocked(fetchEntryLocks).mockResolvedValue(
      new Map([
        ["m-1", false],
        ["m-3", false],
      ]),
    );
    renderPage();

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Play First tone" })).toBeEnabled(),
    );
    expect(screen.getByRole("button", { name: "Play Second tone" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Play all" }));
    expect(screen.getByTestId("watch")).toHaveTextContent("/watch/m-1|m-1,m-3");
  });

  it("plays nothing when the lock read fails (CORE-2843)", async () => {
    vi.mocked(fetchEntryLocks).mockRejectedValue(new Error("offline"));
    renderPage();

    const first = await screen.findByRole("button", { name: "Play First tone" });
    await waitFor(() => expect(fetchEntryLocks).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(first).toBeDisabled();
    expect(screen.getByRole("button", { name: "Play Second tone" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Play Third tone" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Play all" })).toBeNull();
  });

  it("plays nothing until the lock state is known", async () => {
    vi.mocked(fetchEntryLocks).mockReturnValue(new Promise(() => {}));
    renderPage();

    expect(await screen.findByRole("button", { name: "Play First tone" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Play all" })).toBeNull();
  });
});
