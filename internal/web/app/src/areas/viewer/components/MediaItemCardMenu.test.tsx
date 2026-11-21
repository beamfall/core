import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  addCollectionMedia,
  clearMute,
  fetchCollections,
  fetchEntity,
  fetchItemState,
  fetchMedia,
  fetchMutes,
  fetchTagAssignments,
  setMute,
} from "@/lib/graphql";
import type { MediaCredit, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import type { Locale } from "@/strings";
import { MediaItemCard } from "./MediaItemCard";
import { MediaItemCardMenu } from "./MediaItemCardMenu";
import { TrackList } from "./TrackList";

const mocks = vi.hoisted(() => ({ profileID: undefined as string | undefined }));

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  addCollectionMedia: vi.fn(),
  fetchMutes: vi.fn(async () => []),
  setMute: vi.fn(async () => undefined),
  clearMute: vi.fn(async () => undefined),
  clearAllMutes: vi.fn(async () => 0),
  fetchCollections: vi.fn(),
  fetchEntity: vi.fn(),
  fetchItemState: vi.fn(),
  fetchMedia: vi.fn(),
  fetchTagAssignments: vi.fn(),
}));

vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => "viewer-1",
  useSession: () => ({
    manifest: { libraries: [{ id: "lib-1" }] },
    isManager: true,
    session: mocks.profileID ? { profileID: mocks.profileID } : null,
  }),
}));

beforeEach(() => {
  vi.mocked(fetchEntity).mockReset().mockResolvedValue(null);
  vi.mocked(fetchMedia).mockReset().mockResolvedValue(null);
  vi.mocked(fetchItemState).mockReset().mockResolvedValue({
    id: "t1",
    kind: "media",
    watched: false,
    watchlisted: false,
    rating: 0,
  });
  vi.mocked(fetchCollections)
    .mockReset()
    .mockResolvedValue([
      {
        id: "p1",
        libraryID: "lib-1",
        name: "Test List",
        sortOrder: 0,
        memberCount: 0,
        members: [],
      },
    ]);
  vi.mocked(fetchTagAssignments).mockReset().mockResolvedValue({
    targetType: "media",
    targetID: "t1",
    libraryID: "lib-1",
    tags: [],
  });
  vi.mocked(addCollectionMedia)
    .mockReset()
    .mockResolvedValue(undefined as never);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  mocks.profileID = undefined;
});

const artist: MediaCredit = {
  entityID: "artist/adele",
  title: "Adele",
  archetype: "person",
  kind: "performer",
  relation: "credit",
};
const album: MediaCredit = {
  entityID: "record-21",
  title: "21",
  archetype: "container",
  kind: "record",
  relation: "credit",
};

function CurrentPath() {
  return <output aria-label="Current path">{useLocation().pathname}</output>;
}

function renderMenu(
  isAudio: boolean,
  onDetails?: () => void,
  locale: Locale = "en",
  credits?: readonly MediaCredit[],
) {
  render(
    <I18nProvider initialLocale={locale}>
      <MemoryRouter>
        <MediaItemCardMenu
          title="Track One"
          primaryLabel="Play"
          isMedia
          isAudio={isAudio}
          credits={credits}
          href="https://example.test/watch/t1"
          target={{ mediaID: "t1" }}
          onPrimary={() => {}}
          onDetails={onDetails}
        />
        <CurrentPath />
      </MemoryRouter>
    </I18nProvider>,
  );
  expect(fetchMedia).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button"));
}

describe("Start radio from a card or row", () => {
  beforeEach(() => {
    mocks.profileID = "p1";
  });

  it.each([
    ["artist", "person"],
    ["album", "container"],
    ["track", undefined],
  ] as const)("starts the %s seed from the browsing menu", async (seedKind, archetype) => {
    const item: SourceItem = {
      id: "seed-1",
      kind: archetype ? "catalog-entity" : "media",
      title: "Radio seed",
      mediaClass: "audio",
    };
    vi.mocked(fetchEntity).mockResolvedValue({ ...item, archetype, fields: [] });
    const requests = vi.fn(async (_path: string, request: RequestInit) => {
      const operation = JSON.parse(String(request.body));
      if (operation.query.includes("stationGenerate(")) {
        expect(operation.variables).toEqual({ seedKind, seedID: item.id });
        return Response.json({
          data: { stationGenerate: { candidates: [{ mediaID: "radio-track" }] } },
        });
      }
      expect(operation.query).toContain("stationStart(");
      expect(operation.variables).toEqual({
        seedKind,
        seedID: item.id,
        expectedProfileID: "p1",
        confirmReplace: false,
      });
      return Response.json({
        data: { stationStart: [{ id: "radio-track", mediaClass: "audio", title: "Radio Track" }] },
      });
    });
    vi.stubGlobal("fetch", requests);
    render(
      <I18nProvider initialLocale="en">
        <MemoryRouter initialEntries={["/library"]}>
          {archetype ? (
            <MediaItemCard item={item} />
          ) : (
            <TrackList rows={[{ item, number: 1, credits: [] }]} queue={[]} label="Tracks" />
          )}
          <CurrentPath />
        </MemoryRouter>
      </I18nProvider>,
    );
    expect(requests).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "More actions for Radio seed" }));
    expect(screen.getByLabelText("Current path")).toHaveTextContent("/library");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Start radio" }));

    await waitFor(() =>
      expect(screen.getByLabelText("Current path")).toHaveTextContent("/watch/radio-track"),
    );
    expect(requests).toHaveBeenCalledTimes(2);
  });
});

describe("MediaItemCardMenu wording", () => {
  it.each([
    ["en", "Details"],
    ["fr", "Détails"],
    ["de", "Details"],
    ["es", "Detalles"],
    ["pt", "Detalhes"],
  ] as const)("localizes the detail action in %s", async (locale, label) => {
    const details = vi.fn();
    renderMenu(false, details, locale);
    fireEvent.click(await screen.findByRole("menuitem", { name: label }));
    expect(details).toHaveBeenCalledOnce();
  });

  it("uses listening wording for audio Media", async () => {
    renderMenu(true);
    expect(await screen.findByRole("menuitem", { name: "Mark played" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Mark watched" })).not.toBeInTheDocument();
  });

  it("keeps watching wording for other Media", async () => {
    renderMenu(false);
    expect(await screen.findByRole("menuitem", { name: "Mark watched" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Mark played" })).not.toBeInTheDocument();
  });

  it.each([
    ["Go to artist", "/entity/artist%2Fadele"],
    ["Go to album", "/entity/record-21"],
  ])("navigates with %s using credits already loaded by the listing", async (label, path) => {
    renderMenu(true, undefined, "en", [artist, album]);
    const link = await screen.findByRole("menuitem", { name: label });
    expect(link).toHaveAttribute("href", path);
    fireEvent.click(link);
    expect(screen.getByLabelText("Current path")).toHaveTextContent(path);
    expect(fetchMedia).not.toHaveBeenCalled();
  });

  it("loads credits when an audio card opens and omits an unlinked destination", async () => {
    vi.mocked(fetchMedia).mockResolvedValue({
      id: "t1",
      title: "Track One",
      favorited: false,
      mediaClass: "audio",
      credits: [artist],
    });
    renderMenu(true);
    expect(await screen.findByRole("menuitem", { name: "Go to artist" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Go to album" })).toBeNull();
    expect(fetchMedia).toHaveBeenCalledWith("t1", expect.any(AbortSignal));
  });

  it("keeps video menus free of audio destinations", async () => {
    renderMenu(false, undefined, "en", [artist, album]);
    await screen.findByRole("menuitem", { name: "Mark watched" });
    expect(screen.queryByRole("menuitem", { name: /^Go to/ })).toBeNull();
    expect(fetchMedia).not.toHaveBeenCalled();
  });

  it("adds the Media to a playlist from the menu", async () => {
    renderMenu(true);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Add to playlist" }));
    fireEvent.click(await screen.findByRole("button", { name: /Test List/ }));
    await vi.waitFor(() => expect(addCollectionMedia).toHaveBeenCalledWith("p1", ["t1"]));
    expect(await screen.findByRole("status")).toHaveTextContent("Test List");
  });
});

describe("MediaItemCardMenu hide (CORE-2836)", () => {
  beforeEach(() => {
    vi.mocked(setMute).mockClear();
    vi.mocked(clearMute).mockClear();
  });

  it("hides the item for this Profile through a media Mute", async () => {
    vi.mocked(fetchMutes).mockResolvedValue([]);
    renderMenu(false);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Hide" }));
    await vi.waitFor(() => expect(setMute).toHaveBeenCalledWith("media", "t1"));
  });

  it("offers Unhide for an item the Profile already hid", async () => {
    vi.mocked(fetchMutes).mockResolvedValue([
      { kind: "media", targetID: "t1", includeDescendants: false, mutedAt: "2026-11-20T00:00:00Z" },
    ]);
    renderMenu(false);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Unhide" }));
    await vi.waitFor(() => expect(clearMute).toHaveBeenCalledWith("media", "t1"));
  });
});
