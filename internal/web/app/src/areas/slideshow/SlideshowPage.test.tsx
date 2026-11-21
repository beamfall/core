import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ItemTarget, SourceItem } from "@/lib/graphql/types";
import { fetchSourceItems } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import { fetchPreviewInfo } from "../viewer/lib/playback";
import {
  clampInterval,
  DEFAULT_INTERVAL_SECONDS,
  nextDelay,
  parseSlideshowSource,
  slidesFrom,
  slideshowPath,
} from "./lib/slideshow";
import { oscilluxSlideMotion, slideshowMotionAvailable } from "./lib/slideshowMotion";
import { SlideshowPage } from "./SlideshowPage";

vi.mock("@/lib/graphql/viewer", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql/viewer")>()),
  fetchSourceItems: vi.fn(),
}));
vi.mock("../viewer/lib/collections", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../viewer/lib/collections")>()),
  fetchCollectionItems: vi.fn(async () => []),
  fetchViewerCollections: vi.fn(async () => []),
}));
vi.mock("../viewer/lib/mediaDetail", () => ({ fetchMediaDetailFields: vi.fn(async () => []) }));
vi.mock("../viewer/lib/playback", () => ({ fetchPreviewInfo: vi.fn(async () => ({})) }));
vi.mock("../viewer/actions", () => ({
  useHiddenItems: () => ({
    mutes: [{ kind: "media", targetID: "hidden" }],
    failed: false,
    isHidden,
    unhideAll: async () => true,
  }),
}));
vi.mock("@/lib/session", () => ({
  useSession: () => ({ manifest: { libraries: [{ id: "lib", title: "Pictures" }] } }),
  useViewerIdentity: () => "profile-a",
}));

const isHidden = (target: ItemTarget) => "mediaID" in target && target.mediaID === "hidden";

function media(id: string, extra: Partial<SourceItem> = {}): SourceItem {
  return {
    id,
    kind: "media",
    title: `Title ${id}`,
    mediaClass: "image",
    artworkSlots: { poster: `/artwork/${id}.jpg` },
    ...extra,
  };
}

const listed: SourceItem[] = [
  media("one"),
  media("hidden"),
  media("locked", { locked: true }),
  media("clip", { mediaClass: "video", durationSeconds: 20 }),
  media("film", { mediaClass: "video", durationSeconds: 5400 }),
  media("two"),
  { ...media("album"), kind: "album" },
];

function renderAt(path: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/slideshow" element={<SlideshowPage />} />
          <Route path="/" element={<p>home</p>} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("slideshow helpers", () => {
  it("holds the interval to 5–60 seconds with a 12 second default", () => {
    expect(clampInterval(1)).toBe(5);
    expect(clampInterval(99)).toBe(60);
    expect(clampInterval("20")).toBe(20);
    expect(clampInterval("soon")).toBe(DEFAULT_INTERVAL_SECONDS);
    expect(DEFAULT_INTERVAL_SECONDS).toBe(12);
  });

  it("times a slide from its paint and keeps time shown before a pause", () => {
    expect(nextDelay(1000, 1000, 12_000)).toBe(12_000);
    expect(nextDelay(1000, 6000, 12_000)).toBe(7000);
    expect(nextDelay(1000, 1000, 12_000, 4000)).toBe(8000);
    expect(nextDelay(0, 99_000, 12_000)).toBe(0);
  });

  it("drops hidden, locked, long and non-media items, and clips without images", () => {
    expect(slidesFrom(listed, isHidden).map((slide) => slide.item.id)).toEqual([
      "one",
      "clip",
      "two",
    ]);
    expect(slidesFrom([media("clip", { mediaClass: "video" })], isHidden)).toEqual([]);
  });

  it("round-trips a Source through the address", () => {
    const source = { kind: "favorites", libraryID: "lib" } as const;
    expect(parseSlideshowSource(slideshowPath(source).split("?")[1])).toEqual(source);
    expect(parseSlideshowSource("source=nope")).toBeNull();
  });

  it("hard-cuts until Oscillux ships a photo-slideshow mode", () => {
    expect(oscilluxSlideMotion("calm")).toBeNull();
    expect(slideshowMotionAvailable("calm")).toBe(false);
    expect(slideshowMotionAvailable("still")).toBe(true);
  });
});

describe("SlideshowPage", () => {
  const fetchSpy = vi.fn(async () => new Response("{}"));

  beforeEach(() => {
    vi.mocked(fetchSourceItems).mockResolvedValue(listed);
    vi.stubGlobal("fetch", fetchSpy);
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("never shows a hidden item and opens no playback session", async () => {
    renderAt("/slideshow?source=all-items&library=lib");
    const first = await screen.findByRole("img", { name: "Title one" });
    expect(first).toHaveAttribute("src", expect.stringContaining("/artwork/one.jpg"));
    expect(vi.mocked(fetchSourceItems).mock.calls[0][0]).toMatchObject({
      kind: "all-items",
      libraryID: "lib",
    });

    const seen = new Set<string>(["Title one"]);
    for (let step = 0; step < 6; step += 1) {
      fireEvent.keyDown(window, { key: "ArrowRight" });
      const shown = await screen.findByRole("img");
      seen.add(shown.getAttribute("alt") ?? "");
    }
    expect([...seen].sort()).toEqual(["Title clip", "Title one", "Title two"]);
    expect(screen.queryByAltText("Title hidden")).not.toBeInTheDocument();
    expect(screen.queryByAltText("Title locked")).not.toBeInTheDocument();

    await waitFor(() => expect(fetchPreviewInfo).toHaveBeenCalledWith("clip"));
    const requested = fetchSpy.mock.calls.map((call) => String((call as unknown[])[0]));
    expect(requested.filter((url) => url.includes("/api/playback"))).toEqual([]);
  });

  it("opens the setup sheet without a Source and closes on Escape", async () => {
    renderAt("/slideshow");
    expect(await screen.findByRole("dialog", { name: "Slideshow settings" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Calm/ })).toBeDisabled();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(await screen.findByText("home")).toBeInTheDocument();
  });

  it("dims the stage for Dark Room on D", async () => {
    renderAt("/slideshow?source=favorites");
    await screen.findByRole("img", { name: "Title one" });
    expect(screen.queryByTestId("slideshow-dim")).not.toBeInTheDocument();
    fireEvent.keyDown(window, { key: "d" });
    expect(screen.getByTestId("slideshow-dim")).toBeInTheDocument();
  });
});
