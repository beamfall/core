import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  addCollectionMedia,
  clearMute,
  fetchCollections,
  fetchItemState,
  fetchMedia,
  fetchMutes,
  fetchTagAssignments,
  setMute,
} from "@/lib/graphql";
import type { MediaCredit } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import type { Locale } from "@/strings";
import { MediaItemCardMenu } from "./MediaItemCardMenu";

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  addCollectionMedia: vi.fn(),
  fetchMutes: vi.fn(async () => []),
  setMute: vi.fn(async () => undefined),
  clearMute: vi.fn(async () => undefined),
  clearAllMutes: vi.fn(async () => 0),
  fetchCollections: vi.fn(),
  fetchItemState: vi.fn(),
  fetchMedia: vi.fn(),
  fetchTagAssignments: vi.fn(),
}));

vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => "viewer-1",
  useSession: () => ({ manifest: { libraries: [{ id: "lib-1" }] }, isManager: true }),
}));

beforeEach(() => {
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

afterEach(cleanup);

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
  onReveal?: () => void,
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
          onReveal={onReveal}
        />
        <CurrentPath />
      </MemoryRouter>
    </I18nProvider>,
  );
  expect(fetchMedia).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button"));
}

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

  it("offers Reveal details only for a guarded item (CORE-3057)", async () => {
    const reveal = vi.fn();
    renderMenu(false, undefined, "en", undefined, reveal);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Reveal details" }));
    expect(reveal).toHaveBeenCalledOnce();
    cleanup();
    renderMenu(false);
    await screen.findByRole("menuitem", { name: "Mark watched" });
    expect(screen.queryByRole("menuitem", { name: "Reveal details" })).toBeNull();
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
