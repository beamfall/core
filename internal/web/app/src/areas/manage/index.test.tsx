import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type ReactNode, useEffect } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createLibrary, fetchLibraries, fetchPlugins } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { ManageArea } from ".";

const sessionState = vi.hoisted(() => ({
  current: {
    isManager: true,
    loading: false,
    needsEnroll: false,
    session: { deviceRole: "manager", profile: "adult", profileID: "p1" },
  },
}));
const setRegister = vi.hoisted(() => vi.fn());

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("@/lib/register", () => ({ useRegister: () => ({ setRegister }) }));
vi.mock("@/lib/session", () => ({ useSession: () => sessionState.current }));
vi.mock("@/lib/graphql", () => ({
  createLibrary: vi.fn(),
  createManagedFolder: vi.fn(),
  deleteManagedPath: vi.fn(),
  fetchFolderTree: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchPlugins: vi.fn(),
  moveManagedPath: vi.fn(),
  renameManagedPath: vi.fn(),
  restoreManagedPath: vi.fn(),
  scanLibrary: vi.fn(),
  setLibraryPreviewClips: vi.fn(),
  setLibraryRoots: vi.fn(),
}));
vi.mock("./pages/ArtworkPage", () => ({ ArtworkPage: () => null }));
vi.mock("./pages/BackupExportPage", () => ({ BackupExportPage: () => null }));
vi.mock("./pages/BulkOpsPage", () => ({ BulkOpsPage: () => null }));
vi.mock("./pages/CollectionsPage", () => ({ CollectionsPage: () => null }));
vi.mock("./pages/ConvertPage", () => ({ ConvertPage: () => null }));
vi.mock("./pages/CurationPage", () => ({ CurationPage: () => null }));
vi.mock("./pages/EntityPage", () => ({ EntityPage: () => null }));
vi.mock("./pages/FieldLocksPage", () => ({ FieldLocksPage: () => null }));
vi.mock("./pages/MarkersPage", () => ({ MarkersPage: () => null }));
vi.mock("./pages/NavOverridePage", () => ({ NavOverridePage: () => null }));
vi.mock("./pages/OperationsPage", () => ({ OperationsPage: () => null }));
vi.mock("./pages/ProfilePage", () => ({ ProfilePage: () => null }));
vi.mock("./pages/RenamePage", () => ({ RenamePage: () => null }));
vi.mock("./pages/RestrictionsPage", () => ({ RestrictionsPage: () => null }));
vi.mock("./pages/SubscriptionsPage", () => ({ SubscriptionsPage: () => null }));
vi.mock("./pages/TagsPage", () => ({ TagsPage: () => null }));
vi.mock("./pages/TrashPage", () => ({ TrashPage: () => null }));
vi.mock("./pages/UnmatchedPage", () => ({ UnmatchedPage: () => null }));

const MANAGE_NAV = [
  ["Libraries", "/manage/libraries"],
  ["Jobs", "/manage/operations"],
  ["Convert", "/manage/convert"],
  ["Rename", "/manage/rename"],
  ["Trash policy", "/manage/trash"],
  ["Backups", "/manage/backups"],
  ["Subscriptions", "/manage/subscriptions"],
  ["Unmatched records", "/manage/unmatched"],
  ["Collections", "/manage/collections"],
  ["Bulk edit & file operations", "/manage/bulk"],
  ["Tags", "/manage/tags"],
  ["Curation", "/manage/curation"],
  ["Manage", "/manage/entities"],
  ["Artwork candidates", "/manage/artwork"],
  ["Markers", "/manage/markers"],
  ["Field locks", "/manage/field-locks"],
  ["Navigation", "/manage/navigation"],
  ["Restrictions", "/manage/restrictions"],
  ["Profiles", "/manage/profile"],
] as const;

function LocationProbe({ paths }: { paths: string[] }) {
  const { pathname } = useLocation();
  useEffect(() => {
    paths.push(pathname);
  }, [pathname, paths]);
  return null;
}

function manageRouter(path: string, paths: string[], children?: ReactNode) {
  return (
    <MemoryRouter initialEntries={[path]}>
      <I18nProvider>
        <LocationProbe paths={paths} />
        {children ?? (
          <Routes>
            <Route path="/manage/*" element={<ManageArea />} />
          </Routes>
        )}
      </I18nProvider>
    </MemoryRouter>
  );
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function manageNode() {
  return (
    <MemoryRouter initialEntries={["/libraries"]}>
      <I18nProvider>
        <ManageArea />
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("Manage identity boundary", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionState.current = {
      isManager: true,
      loading: false,
      needsEnroll: false,
      session: { deviceRole: "manager", profile: "adult", profileID: "p1" },
    };
  });

  it("clears rows/loading/error across transition and ignores a late rejection", async () => {
    const departedMutation = deferred<never>();
    const currentLoad = deferred<never>();
    vi.mocked(fetchLibraries)
      .mockResolvedValueOnce([
        {
          id: "departed-library",
          title: "Departed library",
          pluginID: null,
          roots: [],
          previewClips: false,
          restricted: false,
        },
      ])
      .mockReturnValueOnce(currentLoad.promise);
    vi.mocked(fetchPlugins).mockResolvedValue([]);
    vi.mocked(createLibrary).mockReturnValueOnce(departedMutation.promise);

    const user = userEvent.setup();
    const view = render(manageNode());
    expect(await screen.findByText("Departed library")).toBeVisible();

    await user.type(screen.getByLabelText("Title"), "Late create");
    await user.click(screen.getByRole("button", { name: "Create Library" }));

    sessionState.current = { ...sessionState.current, loading: true };
    view.rerender(manageNode());
    expect(screen.getByText("Loading...")).toBeVisible();
    expect(screen.queryByText("Departed library")).toBeNull();
    expect(screen.queryByText(/late create failed/i)).toBeNull();

    await act(async () => {
      departedMutation.reject(new Error("late create failed"));
      await departedMutation.promise.catch(() => {});
    });
    expect(screen.getByText("Loading...")).toBeVisible();
    expect(screen.queryByText(/late create failed/i)).toBeNull();

    sessionState.current = {
      ...sessionState.current,
      loading: false,
      session: { ...sessionState.current.session, profileID: "p2" },
    };
    view.rerender(manageNode());
    await waitFor(() => expect(fetchLibraries).toHaveBeenCalledTimes(2));
    expect(screen.queryByText("Departed library")).toBeNull();
    expect(screen.queryByText(/late create failed/i)).toBeNull();
    expect(document.querySelectorAll('tbody tr[aria-hidden="true"]')).toHaveLength(4);

    await act(async () => {
      currentLoad.reject(new Error("current load failed"));
      await currentLoad.promise.catch(() => {});
    });
    expect(await screen.findByText("current load failed")).toBeVisible();
    expect(document.querySelectorAll('tbody tr[aria-hidden="true"]')).toHaveLength(0);
    expect(screen.queryByText("Departed library")).toBeNull();
  });
});

describe("Manage navigation", () => {
  // Regression proof (2026-08-22): with the Libraries target restored to
  // `to="libraries"`, the focused test failed at line 218 with:
  // "expected ['/manage/libraries', …(1)] to have a length of 1 but got 2".
  beforeEach(() => {
    vi.clearAllMocks();
    sessionState.current = {
      isManager: true,
      loading: false,
      needsEnroll: false,
      session: { deviceRole: "manager", profile: "adult", profileID: "p1" },
    };
    vi.mocked(fetchLibraries).mockResolvedValue([]);
    vi.mocked(fetchPlugins).mockResolvedValue([]);
  });

  it.each(MANAGE_NAV)("lands on %s from every Manage route", async (label, expectedPath) => {
    const longestRouteLength = Math.max(...MANAGE_NAV.map(([, path]) => path.length));

    for (const [, startingPath] of MANAGE_NAV) {
      const paths: string[] = [];
      const user = userEvent.setup();
      const view = render(manageRouter(startingPath, paths));

      await user.click(screen.getByRole("link", { name: label }));
      await waitFor(() => expect(paths.at(-1)).toBe(expectedPath));

      expect(paths.filter((path) => path === expectedPath)).toHaveLength(1);
      expect(paths.every((path) => path.length <= longestRouteLength)).toBe(true);
      view.unmount();
    }
  });

  it("redirects an unknown Manage path to Libraries in one bounded hop", async () => {
    const paths: string[] = [];
    render(manageRouter("/manage/not-a-route", paths));

    await waitFor(() => expect(paths.at(-1)).toBe("/manage/libraries"));

    expect(paths).toEqual(["/manage/not-a-route", "/manage/libraries"]);
    expect(paths.at(-1)).not.toMatch(/\/(libraries)(?:\/\1)+$/);
    expect(Math.max(...paths.map((path) => path.length))).toBeLessThanOrEqual(
      "/manage/not-a-route".length,
    );
  });
});
