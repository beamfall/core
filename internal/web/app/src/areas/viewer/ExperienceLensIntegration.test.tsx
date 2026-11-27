import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ExperienceLensProvider,
  resetLaunchLensSession,
  withExperienceModeParam,
} from "@/components/chrome/experienceLens";
import { LibrarySidebar } from "@/components/chrome/LibrarySidebar";
import { EMPTY_SIDEBAR_PREFS } from "@/components/chrome/sidebarPrefs";
import {
  fetchClientPrefs,
  fetchMetadataCredits,
  fetchMutes,
  fetchSemanticCapability,
  searchResult,
  setClientPref,
} from "@/lib/graphql";
import type {
  Layout,
  LibrarySummary,
  NavigationManifest,
  NavigationNode,
  ViewerPluginInfo,
} from "@/lib/graphql/types";
import { fetchHomeLibraries, fetchSourceItems, fetchViewerPlugins } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { RegisterProvider } from "@/lib/register";
import { clearViewerCache } from "@/lib/viewerCache";
import { HeaderSearch } from "./components/HeaderSearch";
import { LayoutView } from "./components/LayoutView";
import { ProfileSettingsPage } from "./pages/ProfileSettingsPage";
import { SearchPage } from "./pages/SearchPage";

const plugins: ViewerPluginInfo[] = [
  { id: "p-vid", displayName: { en: "Video" }, mediaClasses: ["video"] },
  {
    id: "p-stage",
    displayName: { en: "Stage" },
    mediaClasses: ["audio", "video"],
    presentation: { resumeMedia: false, selectPlays: true, defaultIcon: "mic" },
  },
  {
    id: "p-aud",
    displayName: { en: "Audio" },
    mediaClasses: ["audio"],
    capabilities: ["audio-station"],
  },
  {
    id: "p-img",
    displayName: { en: "Stills" },
    mediaClasses: ["image", "video"],
    presentation: { resumeMedia: false, defaultIcon: "camera" },
  },
];

const libraries: LibrarySummary[] = [
  { id: "lib-film", title: "Films", pluginID: "p-vid", previewClips: true },
  { id: "lib-karaoke", title: "Stage", pluginID: "p-stage", previewClips: false },
  { id: "lib-music", title: "Music", pluginID: "p-aud", previewClips: false },
  { id: "lib-photos", title: "Photos", pluginID: "p-img", previewClips: false },
];

const navigation: NavigationNode[] = [
  { id: "home", title: { en: "Home" }, view: "layout", layoutID: "layout-home" },
  ...libraries.map(
    (lib): NavigationNode => ({
      id: `library-${lib.id}`,
      libraryID: lib.id,
      title: { en: lib.title },
      view: "layout",
      layoutID: "layout-home",
    }),
  ),
];

const homeLayout: Layout = {
  id: "layout-home",
  sections: [
    {
      id: "continue",
      kind: "shelf",
      title: { en: "Continue Watching" },
      artworkSlot: "poster",
      source: { kind: "continue-watching" },
    },
    {
      id: "next-up",
      kind: "shelf",
      title: { en: "Up Next" },
      artworkSlot: "poster",
      source: { kind: "next-up", value: "video" },
    },
    {
      id: "continue-listening",
      kind: "shelf",
      title: { en: "Continue Listening" },
      artworkSlot: "poster",
      source: { kind: "next-up", value: "audio" },
    },
    {
      id: "recent",
      kind: "shelf",
      title: { en: "Recently Added" },
      artworkSlot: "poster",
      source: { kind: "recently-added" },
    },
  ],
};

const sessionState = {
  manifest: {
    profileID: "p1",
    revealed: false,
    generatedAt: "2026-11-26T00:00:00Z",
    libraries,
    navigation,
    layouts: [homeLayout],
    theme: { accentColor: "#9fd8e5" },
  } as NavigationManifest,
  session: {
    profileID: "p1",
    profile: "adult",
    deviceRole: "manager",
    revealed: false,
    revealPinSet: true,
    subtitleMode: "off",
    subtitleLanguages: [],
  },
  profiles: [{ id: "p1", name: "Alex", kind: "adult" as const }],
  isManager: true,
  refresh: vi.fn(),
  refreshProfiles: vi.fn().mockResolvedValue(undefined),
};

vi.mock("@/lib/session", () => ({
  useSession: () => sessionState,
  useViewerIdentity: () => "viewer-p1",
}));

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => (
    <div className="mock-shell">
      <LibrarySidebar
        id="sidebar"
        navigation={sessionState.manifest.navigation}
        libraries={sessionState.manifest.libraries}
        activeNodeId="home"
        collapsed={false}
        drawer={false}
        onNavigate={() => {}}
        prefs={EMPTY_SIDEBAR_PREFS}
      />
      <HeaderSearch />
      <main>{children}</main>
    </div>
  ),
}));

const { mockFetchClientPrefs, mockSetClientPref } = vi.hoisted(() => ({
  mockFetchClientPrefs: vi.fn(),
  mockSetClientPref: vi.fn(),
}));

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  searchResult: vi.fn(),
  fetchSemanticCapability: vi.fn(),
  fetchClientPrefs: mockFetchClientPrefs,
  fetchMetadataCredits: vi.fn(),
  fetchMutes: vi.fn(),
  setClientPref: mockSetClientPref,
}));

vi.mock("@/lib/graphql/viewer", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql/viewer")>()),
  fetchViewerPlugins: vi.fn(),
  fetchHomeLibraries: vi.fn(),
  fetchSourceItems: vi.fn(),
  fetchClientPrefs: mockFetchClientPrefs,
  setClientPref: mockSetClientPref,
}));

Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.setPointerCapture ??= () => {};
Element.prototype.releasePointerCapture ??= () => {};
Element.prototype.scrollIntoView ??= () => {};

function Where() {
  const loc = useLocation();
  return <span data-testid="where">{loc.pathname + loc.search}</span>;
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  clearViewerCache();
  resetLaunchLensSession("p1");
  vi.mocked(fetchViewerPlugins).mockReset().mockResolvedValue(plugins);
  vi.mocked(fetchClientPrefs).mockReset().mockResolvedValue([]);
  vi.mocked(fetchMetadataCredits).mockReset().mockResolvedValue([]);
  vi.mocked(fetchMutes).mockReset().mockResolvedValue([]);
  vi.mocked(setClientPref)
    .mockReset()
    .mockImplementation(async (key, value) => ({ key, value }));
  vi.mocked(fetchSemanticCapability).mockReset().mockResolvedValue([]);
  vi.mocked(fetchHomeLibraries)
    .mockReset()
    .mockResolvedValue(libraries.map((l) => ({ id: l.id, title: l.title, showOnHome: true })));
  vi.mocked(fetchSourceItems)
    .mockReset()
    .mockImplementation(async (vars) => {
      if (vars.libraryID === "lib-film") {
        return [
          {
            id: "m-film",
            kind: "media",
            title: "Blade Runner",
            libraryID: "lib-film",
            mediaClass: "video",
          },
        ];
      }
      if (vars.libraryID === "lib-karaoke") {
        return [
          {
            id: "m-karaoke",
            kind: "media",
            title: "Mr. Brightside",
            libraryID: "lib-karaoke",
            mediaClass: "audio",
          },
        ];
      }
      if (vars.libraryID === "lib-music") {
        return [
          {
            id: "m-music",
            kind: "media",
            title: "Kind of Blue",
            libraryID: "lib-music",
            mediaClass: "audio",
          },
        ];
      }
      if (vars.libraryID === "lib-photos") {
        return [
          {
            id: "m-photo",
            kind: "media",
            title: "Canyon",
            libraryID: "lib-photos",
            mediaClass: "image",
          },
        ];
      }
      if (vars.kind === "next-up" && vars.value === "audio") {
        return [
          {
            id: "next-aud",
            kind: "media",
            title: "Track Two",
            libraryID: "lib-music",
            mediaClass: "audio",
          },
        ];
      }
      if (vars.kind === "next-up" && vars.value === "video") {
        return [
          {
            id: "next-vid",
            kind: "media",
            title: "Pilot",
            libraryID: "lib-film",
            mediaClass: "video",
          },
        ];
      }
      return [];
    });
  vi.mocked(searchResult)
    .mockReset()
    .mockResolvedValue({
      mode: "keyword",
      items: [
        {
          id: "m1",
          kind: "media",
          title: "Blue Train",
          mode: "keyword",
          score: 1,
          libraryID: "lib-music",
          libraryTitle: "Music",
          mediaClass: "audio",
        },
        {
          id: "m2",
          kind: "media",
          title: "Blue Velvet",
          mode: "keyword",
          score: 1,
          libraryID: "lib-film",
          libraryTitle: "Films",
          mediaClass: "video",
        },
      ],
      restrictedResultsPresent: false,
    });
});

afterEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});

describe("Experience Lens end-to-end viewer surfaces", () => {
  it("filters sidebar libraries and Home shelves immediately when switching Watch -> Listen", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/"]}>
          <ExperienceLensProvider initialPlugins={plugins}>
            <div className="flex">
              <LibrarySidebar
                id="sidebar"
                navigation={sessionState.manifest.navigation}
                libraries={sessionState.manifest.libraries}
                activeNodeId="home"
                collapsed={false}
                drawer={false}
                onNavigate={() => {}}
                prefs={EMPTY_SIDEBAR_PREFS}
              />
              <LayoutView layout={homeLayout} libraryID="" home navigationID="home" />
            </div>
            <Where />
          </ExperienceLensProvider>
        </MemoryRouter>
      </I18nProvider>,
    );

    // Wait for initial Home hydration across all modalities.
    expect(await screen.findByText("Blade Runner")).toBeInTheDocument();
    expect(await screen.findByText("Kind of Blue")).toBeInTheDocument();

    const sidebar = screen.getByRole("complementary");
    const lensGroup = within(sidebar).getByRole("group", { name: "Experience mode" });
    expect(within(lensGroup).getByRole("button", { name: "All" })).toBeInTheDocument();
    expect(within(lensGroup).getByRole("button", { name: "Watch" })).toBeInTheDocument();
    expect(within(lensGroup).getByRole("button", { name: "Listen" })).toBeInTheDocument();
    expect(within(lensGroup).getByRole("button", { name: "Photos" })).toBeInTheDocument();

    // Switch to Watch: Music and Photos drop from sidebar & Home, while Stage stays in Watch.
    await user.click(within(lensGroup).getByRole("button", { name: "Watch" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/?mode=watch");
    expect(within(sidebar).getByRole("link", { name: "Films" })).toHaveAttribute(
      "href",
      withExperienceModeParam("/n/library-lib-film", "watch"),
    );
    expect(within(sidebar).getByRole("link", { name: "Stage" })).toBeInTheDocument();
    expect(within(sidebar).queryByRole("link", { name: "Music" })).toBeNull();
    expect(within(sidebar).queryByRole("link", { name: "Photos" })).toBeNull();

    // Watch Home keeps Up Next (video) and hides Continue Listening (audio).
    expect(screen.queryByRole("heading", { name: "Continue Listening" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Up Next" })).toBeInTheDocument();
    expect(screen.queryByText("Kind of Blue")).toBeNull();

    // Switch to Listen: Music stays, Films/Stage/Photos drop, and search/Home update immediately.
    await user.click(within(lensGroup).getByRole("button", { name: "Listen" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/?mode=listen");
    expect(within(sidebar).getByRole("link", { name: "Music" })).toBeInTheDocument();
    expect(within(sidebar).queryByRole("link", { name: "Films" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "Up Next" })).toBeNull();
    expect(await screen.findByRole("heading", { name: "Continue Listening" })).toBeInTheDocument();
  });

  it("scopes SearchPage queries via libraryIDs and preserves ?mode= across typing and clearing", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/search?mode=listen"]}>
          <ExperienceLensProvider initialPlugins={plugins}>
            <Routes>
              <Route path="/search" element={<SearchPage />} />
            </Routes>
            <Where />
          </ExperienceLensProvider>
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(screen.getByText(/Searching Listen/)).toBeInTheDocument();
    const searchInput = within(screen.getByRole("main")).getByLabelText("Search");
    await user.type(searchInput, "blue");

    await waitFor(() =>
      expect(searchResult).toHaveBeenLastCalledWith(
        "blue",
        "keyword",
        expect.any(AbortSignal),
        undefined,
        ["lib-music"],
      ),
    );
    // Client-side filter hides video hit even when mock returned both rows.
    expect(await screen.findByText("Blue Train")).toBeInTheDocument();
    expect(screen.queryByText("Blue Velvet")).toBeNull();
    expect(screen.getByTestId("where")).toHaveTextContent("/search?mode=listen&q=blue");

    await user.clear(searchInput);
    expect(screen.getByTestId("where")).toHaveTextContent("/search?mode=listen");

    await user.type(searchInput, "blue");
    expect(await screen.findByText("Blue Train")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Search all libraries" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/search?q=blue");
    await waitFor(() =>
      expect(searchResult).toHaveBeenLastCalledWith("blue", "keyword", expect.any(AbortSignal)),
    );
  });

  it("saves Launch Experience policy and starting mode in ProfileSettingsPage", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <RegisterProvider>
          <MemoryRouter initialEntries={["/settings"]}>
            <ExperienceLensProvider initialPlugins={plugins}>
              <ProfileSettingsPage />
            </ExperienceLensProvider>
          </MemoryRouter>
        </RegisterProvider>
      </I18nProvider>,
    );

    const askRadio = await screen.findByRole("radio", { name: /Ask on open/i });
    await user.click(askRadio);
    expect(setClientPref).toHaveBeenCalledWith(
      "web.experience-launch",
      JSON.stringify({ policy: "ask", fixedMode: "all" }),
    );

    const fixedRadio = screen.getByRole("radio", { name: /Always start in a mode/i });
    await user.click(fixedRadio);
    expect(setClientPref).toHaveBeenLastCalledWith(
      "web.experience-launch",
      JSON.stringify({ policy: "fixed", fixedMode: "all" }),
    );

    // Choose fixed starting mode "Listen".
    await user.click(screen.getByLabelText("Starting mode"));
    await user.click(await screen.findByRole("option", { name: "Listen" }));
    expect(setClientPref).toHaveBeenLastCalledWith(
      "web.experience-launch",
      JSON.stringify({ policy: "fixed", fixedMode: "listen" }),
    );
  });
});
