import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  LibrarySummary,
  NavigationManifest,
  NavigationNode,
  ViewerPluginInfo,
} from "@/lib/graphql/types";
import {
  fetchClientPrefs,
  fetchSourceItems,
  fetchViewerPlugins,
  setClientPref,
} from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { expectNoAxeViolations } from "@/test/axe";
import {
  classifyLibraryModality,
  deriveExperienceModes,
  ExperienceLensProvider,
  itemMatchesLens,
  resetLaunchLensSession,
  sectionMatchesLens,
  useExperienceLens,
  withExperienceModeParam,
} from "./experienceLens";
import { LaunchLensOverlay } from "./LaunchLensOverlay";
import { ModeLensPill } from "./ModeLensPill";

const mockSessionState = {
  manifest: null as NavigationManifest | null,
  session: {
    profileID: "p1",
    profile: "adult",
    deviceRole: "viewer",
    revealed: false,
    revealPinSet: true,
    subtitleMode: "off",
    subtitleLanguages: [],
  },
};

vi.mock("@/lib/session", () => ({
  useSession: () => mockSessionState,
  useViewerIdentity: () => "viewer-p1",
}));

vi.mock("@/lib/graphql/viewer", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql/viewer")>()),
  fetchViewerPlugins: vi.fn(),
  fetchClientPrefs: vi.fn(),
  setClientPref: vi.fn(),
  fetchSourceItems: vi.fn(),
}));

function makePlugins(): ViewerPluginInfo[] {
  return [
    {
      id: "plug-video-custom",
      displayName: { en: "Cinema" },
      mediaClasses: ["video"],
    },
    {
      id: "plug-family-custom",
      displayName: { en: "Archive" },
      mediaClasses: ["video", "image"],
    },
    {
      id: "plug-sing-custom",
      displayName: { en: "Sing Along" },
      mediaClasses: ["audio", "video"],
      presentation: {
        resumeMedia: false,
        selectPlays: true,
        interactiveSidecars: ["cdg", "lrc", "txt"],
        defaultIcon: "mic",
      },
    },
    {
      id: "plug-tracks-custom",
      displayName: { en: "Musicbox" },
      mediaClasses: ["audio"],
      capabilities: ["audio-station"],
    },
    {
      id: "plug-casts-custom",
      displayName: { en: "Serials" },
      mediaClasses: ["audio"],
      presentation: { resumeMedia: true },
    },
    {
      id: "plug-photos-custom",
      displayName: { en: "Stills" },
      mediaClasses: ["image", "video"],
      presentation: { resumeMedia: false, defaultIcon: "camera" },
    },
  ];
}

function makeLibraries(withPhotos = true): LibrarySummary[] {
  const base: LibrarySummary[] = [
    { id: "lib-films", title: "Films", pluginID: "plug-video-custom", previewClips: true },
    {
      id: "lib-homevid",
      title: "Home Videos",
      pluginID: "plug-family-custom",
      previewClips: false,
    },
    { id: "lib-karaoke", title: "Stage Night", pluginID: "plug-sing-custom", previewClips: false },
    { id: "lib-music", title: "Records", pluginID: "plug-tracks-custom", previewClips: false },
    { id: "lib-podcasts", title: "Talks", pluginID: "plug-casts-custom", previewClips: false },
  ];
  if (withPhotos) {
    base.push({
      id: "lib-photos",
      title: "Snapshots",
      pluginID: "plug-photos-custom",
      previewClips: false,
    });
  }
  return base;
}

function makeManifest(libraries: LibrarySummary[]): NavigationManifest {
  return {
    profileID: "p1",
    revealed: false,
    generatedAt: "2026-11-26T00:00:00Z",
    libraries,
    navigation: [
      { id: "home", title: { en: "Home" }, view: "layout", layoutID: "layout-home" },
      ...libraries.map(
        (lib): NavigationNode => ({
          id: `library-${lib.id}`,
          title: { en: lib.title },
          view: "layout",
          layoutID: `layout-${lib.pluginID ?? "home"}`,
          libraryID: lib.id,
        }),
      ),
    ],
    layouts: [{ id: "layout-home", sections: [] }],
    theme: { accentColor: "#9fd8e5" },
  };
}

function LocationProbe() {
  const loc = useLocation();
  const lens = useExperienceLens();
  return (
    <div>
      <span data-testid="loc">{loc.pathname + loc.search}</span>
      <span data-testid="mode">{lens.mode}</span>
      <span data-testid="launch-open">{String(lens.launchOpen)}</span>
    </div>
  );
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  resetLaunchLensSession("p1");
  vi.mocked(fetchViewerPlugins).mockReset();
  vi.mocked(fetchClientPrefs).mockReset();
  vi.mocked(setClientPref).mockReset();
  vi.mocked(fetchSourceItems).mockReset();
  vi.mocked(fetchViewerPlugins).mockResolvedValue(makePlugins());
  vi.mocked(fetchClientPrefs).mockResolvedValue([]);
  vi.mocked(setClientPref).mockImplementation(async (key, value) => ({ key, value }));
  vi.mocked(fetchSourceItems).mockResolvedValue([]);
});

afterEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});

describe("experienceLens classification & mode derivation", () => {
  it("classifies arbitrary plugin IDs purely from declared mediaClasses and presentation", () => {
    const plugins = makePlugins();
    const byId = new Map(plugins.map((p) => [p.id, p]));

    expect(
      classifyLibraryModality(
        { id: "l1", pluginID: "plug-video-custom" },
        byId.get("plug-video-custom"),
      ).modality,
    ).toBe("watch");
    expect(
      classifyLibraryModality(
        { id: "l2", pluginID: "plug-family-custom" },
        byId.get("plug-family-custom"),
      ).modality,
    ).toBe("watch");
    // Stage video + audio with selectPlays + interactive sidecars belongs in Watch, not Listen.
    expect(
      classifyLibraryModality(
        { id: "l3", pluginID: "plug-sing-custom" },
        byId.get("plug-sing-custom"),
      ).modality,
    ).toBe("watch");
    expect(
      classifyLibraryModality(
        { id: "l4", pluginID: "plug-tracks-custom" },
        byId.get("plug-tracks-custom"),
      ).modality,
    ).toBe("listen");
    expect(
      classifyLibraryModality(
        { id: "l5", pluginID: "plug-casts-custom" },
        byId.get("plug-casts-custom"),
      ).modality,
    ).toBe("listen");
    // Image-primary with video clips stays in its own image modality.
    expect(
      classifyLibraryModality(
        { id: "l6", pluginID: "plug-photos-custom" },
        byId.get("plug-photos-custom"),
      ).modality,
    ).toBe("image");
  });

  it("auto-surfaces extra modalities only when matching libraries exist", () => {
    const plugins = makePlugins();
    const withoutPhotos = deriveExperienceModes(makeLibraries(false), plugins);
    expect(withoutPhotos.map((m) => m.id)).toEqual(["all", "watch", "listen"]);

    const withPhotos = deriveExperienceModes(makeLibraries(true), plugins);
    expect(withPhotos.map((m) => m.id)).toEqual(["all", "watch", "listen", "image"]);
    expect(withPhotos.find((m) => m.id === "watch")?.libraryIDs).toEqual([
      "lib-films",
      "lib-homevid",
      "lib-karaoke",
    ]);
    expect(withPhotos.find((m) => m.id === "listen")?.libraryIDs).toEqual([
      "lib-music",
      "lib-podcasts",
    ]);
    expect(withPhotos.find((m) => m.id === "image")?.libraryIDs).toEqual(["lib-photos"]);

    // Future third-party class surfaces automatically with plugin displayName.
    const modesWithBook = deriveExperienceModes(
      [
        ...makeLibraries(false),
        { id: "lib-comics", title: "Reads", pluginID: "plug-comics", previewClips: false },
      ],
      [...plugins, { id: "plug-comics", displayName: { en: "Comics" }, mediaClasses: ["comic"] }],
    );
    expect(modesWithBook.map((m) => m.id)).toEqual(["all", "watch", "listen", "comic"]);
    expect(modesWithBook.find((m) => m.id === "comic")?.fallbackLabel).toBe("Comics");
  });

  it("filters sections and items cleanly by active mode", () => {
    const modes = deriveExperienceModes(makeLibraries(true), makePlugins());
    const watch = modes.find((m) => m.id === "watch");
    const listen = modes.find((m) => m.id === "listen");

    expect(
      sectionMatchesLens(
        {
          id: "next-up",
          kind: "shelf",
          title: { en: "Up Next" },
          artworkSlot: "poster",
          source: { kind: "next-up", value: "video" },
        },
        listen,
      ),
    ).toBe(false);
    expect(
      sectionMatchesLens(
        {
          id: "continue-listening",
          kind: "shelf",
          title: { en: "Continue Listening" },
          artworkSlot: "poster",
          source: { kind: "next-up", value: "audio" },
        },
        watch,
      ),
    ).toBe(false);

    expect(itemMatchesLens({ libraryID: "lib-music", mediaClass: "audio" }, watch)).toBe(false);
    expect(itemMatchesLens({ libraryID: "lib-karaoke", mediaClass: "audio" }, watch)).toBe(true);
    expect(withExperienceModeParam("/n/lib-films?tab=1", "watch")).toBe(
      "/n/lib-films?tab=1&mode=watch",
    );
    expect(withExperienceModeParam("/n/lib-films", "all")).toBe("/n/lib-films");
  });
});

describe("ModeLensPill, URL routing, and LaunchLensOverlay", () => {
  it("switches modes via ModeLensPill, updates URL ?mode=..., and persists last mode", async () => {
    const user = userEvent.setup();
    const plugins = makePlugins();
    mockSessionState.manifest = makeManifest(makeLibraries(true));

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/?mode=listen"]}>
          <ExperienceLensProvider initialPlugins={plugins}>
            <ModeLensPill variant="sidebar" />
            <LocationProbe />
          </ExperienceLensProvider>
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(screen.getByTestId("mode")).toHaveTextContent("listen");
    const group = screen.getByRole("group", { name: "Experience mode" });
    expect(within(group).getByRole("button", { name: "Listen" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    await user.click(within(group).getByRole("button", { name: "Watch" }));
    expect(screen.getByTestId("mode")).toHaveTextContent("watch");
    expect(screen.getByTestId("loc")).toHaveTextContent("/?mode=watch");
    expect(setClientPref).toHaveBeenCalledWith("web.experience-last-mode", "watch");

    await user.click(within(group).getByRole("button", { name: "All" }));
    expect(screen.getByTestId("mode")).toHaveTextContent("all");
    expect(screen.getByTestId("loc")).toHaveTextContent("/");
  });

  it("hides ModeLensPill when fewer than two modalities exist", () => {
    const plugins = makePlugins();
    mockSessionState.manifest = makeManifest([
      { id: "lib-films", title: "Films", pluginID: "plug-video-custom", previewClips: true },
    ]);

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/"]}>
          <ExperienceLensProvider initialPlugins={plugins}>
            <ModeLensPill variant="sidebar" />
          </ExperienceLensProvider>
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(screen.queryByRole("group", { name: "Experience mode" })).toBeNull();
  });

  it("navigates back to filtered Home when switching to a lens that excludes the open library", async () => {
    const user = userEvent.setup();
    const plugins = makePlugins();
    mockSessionState.manifest = makeManifest(makeLibraries(false));

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/n/records"]}>
          <ExperienceLensProvider initialPlugins={plugins}>
            <ModeLensPill variant="inline" />
            <LocationProbe />
          </ExperienceLensProvider>
        </MemoryRouter>
      </I18nProvider>,
    );

    // Note library node slug matching in findNodeBySlug uses title/id; match lib-music explicitly:
    mockSessionState.manifest.navigation[4].libraryID = "lib-music";
    await user.click(screen.getByRole("button", { name: "Watch" }));
    expect(screen.getByTestId("mode")).toHaveTextContent("watch");
  });

  it("honors Launch Experience 'fixed' policy unless overridden by explicit URL ?mode=all", async () => {
    const plugins = makePlugins();
    mockSessionState.manifest = makeManifest(makeLibraries(false));
    vi.mocked(fetchClientPrefs).mockResolvedValue([
      {
        key: "web.experience-launch",
        value: JSON.stringify({ policy: "fixed", fixedMode: "listen" }),
      },
    ]);

    const first = render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/"]}>
          <ExperienceLensProvider initialPlugins={plugins}>
            <LocationProbe />
          </ExperienceLensProvider>
        </MemoryRouter>
      </I18nProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("mode")).toHaveTextContent("listen"));
    first.unmount();

    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/?mode=all"]}>
          <ExperienceLensProvider initialPlugins={plugins}>
            <LocationProbe />
          </ExperienceLensProvider>
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(screen.getByTestId("mode")).toHaveTextContent("all");
  });

  it("opens sleek 1-tap LaunchLensOverlay on 'ask' with live Continue previews and passes axe", async () => {
    const user = userEvent.setup();
    const plugins = makePlugins();
    mockSessionState.manifest = makeManifest(makeLibraries(true));
    vi.mocked(fetchClientPrefs).mockResolvedValue([
      { key: "web.experience-launch", value: JSON.stringify({ policy: "ask", fixedMode: "all" }) },
    ]);
    vi.mocked(fetchSourceItems).mockImplementation(async (vars) => {
      if (vars.kind === "continue-watching" && vars.libraryIDs?.includes("lib-films")) {
        return [
          {
            id: "vid-1",
            kind: "media",
            title: "Solaris",
            libraryID: "lib-films",
            mediaClass: "video",
            engagement: { positionSeconds: 600, durationSeconds: 2400 },
          },
        ];
      }
      if (vars.kind === "next-up" && vars.value === "audio") {
        return [
          {
            id: "aud-1",
            kind: "media",
            title: "Blue in Green",
            libraryID: "lib-music",
            mediaClass: "audio",
          },
        ];
      }
      if (vars.kind === "recently-added" && vars.libraryIDs?.includes("lib-photos")) {
        return [
          {
            id: "img-1",
            kind: "media",
            title: "Alpine Sunrise",
            libraryID: "lib-photos",
            mediaClass: "image",
          },
        ];
      }
      return [];
    });

    const rendered = render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/"]}>
          <ExperienceLensProvider initialPlugins={plugins}>
            <LaunchLensOverlay />
            <LocationProbe />
          </ExperienceLensProvider>
        </MemoryRouter>
      </I18nProvider>,
    );

    const heading = await screen.findByRole("heading", { name: "Choose your focus" });
    expect(heading).toBeInTheDocument();
    expect(await screen.findByText("Solaris")).toBeInTheDocument();
    expect(await screen.findByText("Blue in Green")).toBeInTheDocument();
    expect(await screen.findByText("Alpine Sunrise")).toBeInTheDocument();
    expect(screen.getByText("Continue Watching")).toBeInTheDocument();
    expect(screen.getByText("Continue Listening")).toBeInTheDocument();

    await act(async () => {
      await expectNoAxeViolations(rendered.baseElement);
    });

    await user.click(screen.getByRole("button", { name: "Enter Listen" }));
    expect(screen.queryByRole("heading", { name: "Choose your focus" })).toBeNull();
    expect(screen.getByTestId("mode")).toHaveTextContent("listen");
    expect(screen.getByTestId("loc")).toHaveTextContent("/?mode=listen");
  });
});
