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
  inDarkRoomWindow,
  nextDarkRoomBoundary,
  nextDelay,
  parseSlideshowSource,
  type Slide,
  type SlideShape,
  slideGroupSettled,
  slideGroups,
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
  useSession: () => ({
    manifest: { libraries: [{ id: "lib", title: "Pictures" }] },
    refreshSession: async () => {},
  }),
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

  it("pairs consecutive portraits on a landscape screen and leaves a lone portrait alone", () => {
    const slide = (id: string, clip = false): Slide => ({ item: media(id), clip });
    const shapes: Record<string, SlideShape> = {
      p1: "portrait",
      p2: "portrait",
      p3: "portrait",
      wide: "landscape",
      p4: "portrait",
      clip: "portrait",
      p5: "portrait",
    };
    const slides = ["p1", "p2", "p3", "wide", "p4", "clip", "p5", "unknown"].map((id) =>
      slide(id, id === "clip"),
    );
    const ids = (pair: boolean) =>
      slideGroups(slides, (s) => shapes[s.item.id], pair).map((group) =>
        group.slides.map((s) => s.item.id).join("+"),
      );
    expect(ids(true)).toEqual(["p1+p2", "p3", "wide", "p4", "clip", "p5", "unknown"]);
    expect(ids(false)).toEqual(["p1", "p2", "p3", "wide", "p4", "clip", "p5", "unknown"]);
    // The last portrait never wraps round to pair with the first.
    expect(ids(true).at(-1)).toBe("unknown");
    expect(slideGroups([slide("p1")], (s) => shapes[s.item.id], true)).toHaveLength(1);
  });

  it("waits for the portraits before a slide so Previous never splits a pair", () => {
    const slides = ["wide", "e", "f"].map((id): Slide => ({ item: media(id), clip: false }));
    const known: Record<string, SlideShape> = { wide: "landscape", f: "portrait" };
    const shapeOf = (s: Slide) => known[s.item.id];
    expect(slideGroupSettled(slides, 2, shapeOf, true)).toBe(false);
    known.e = "portrait";
    expect(slideGroups(slides, shapeOf, true).map((g) => g.start)).toEqual([0, 1]);
    expect(slideGroupSettled(slides, 1, shapeOf, true)).toBe(true);
  });

  it("finds the Dark Room window across midnight", () => {
    const at = (hours: number, minutes = 0) => new Date(2026, 0, 1, hours, minutes);
    expect(inDarkRoomWindow(at(23), "22:00", "07:00")).toBe(true);
    expect(inDarkRoomWindow(at(2, 30), "22:00", "07:00")).toBe(true);
    expect(inDarkRoomWindow(at(7), "22:00", "07:00")).toBe(false);
    expect(inDarkRoomWindow(at(12), "22:00", "07:00")).toBe(false);
    expect(inDarkRoomWindow(at(22), "22:00", "07:00")).toBe(true);
    expect(inDarkRoomWindow(at(13), "12:00", "14:00")).toBe(true);
    expect(inDarkRoomWindow(at(15), "12:00", "14:00")).toBe(false);
    expect(inDarkRoomWindow(at(12), "12:00", "12:00")).toBe(false);
    expect(inDarkRoomWindow(at(12), "noon", "14:00")).toBe(false);
  });

  it("ends a manual Dark Room choice at the schedule's next boundary", () => {
    const at = (day: number, hours: number) => new Date(2026, 0, day, hours);
    expect(nextDarkRoomBoundary(at(1, 12), "22:00", "07:00")).toEqual(at(1, 22));
    expect(nextDarkRoomBoundary(at(1, 23), "22:00", "07:00")).toEqual(at(2, 7));
    expect(nextDarkRoomBoundary(at(1, 7), "22:00", "07:00")).toEqual(at(1, 22));
    expect(nextDarkRoomBoundary(at(1, 12), "12:00", "12:00")).toBeNull();
  });

  it("hard-cuts until Oscillux ships a photo-slideshow mode", () => {
    expect(oscilluxSlideMotion("calm")).toBeNull();
    expect(slideshowMotionAvailable("calm")).toBe(false);
    expect(slideshowMotionAvailable("still")).toBe(true);
  });
});

/** jsdom never decodes images: this one "loads" portrait when its URL says "tall". */
class FakeImage {
  decoding = "";
  naturalWidth = 0;
  naturalHeight = 0;
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  set src(url: string) {
    const tall = url.includes("tall");
    this.naturalWidth = tall ? 900 : 1600;
    this.naturalHeight = tall ? 1350 : 1000;
    queueMicrotask(() => this.onload?.());
  }
}

describe("SlideshowPage", () => {
  const fetchSpy = vi.fn(async () => new Response("{}"));

  beforeEach(() => {
    vi.mocked(fetchSourceItems).mockResolvedValue(listed);
    vi.stubGlobal("fetch", fetchSpy);
    vi.stubGlobal("Image", FakeImage);
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

  it("shows two portraits as one slide and steps past the pair", async () => {
    vi.mocked(fetchSourceItems).mockResolvedValue([
      media("tall-a"),
      media("tall-b"),
      media("wide"),
    ]);
    renderAt("/slideshow?source=all-items");
    expect(await screen.findByRole("img", { name: "Title tall-a" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Title tall-b" })).toBeInTheDocument();
    fireEvent.keyDown(window, { key: "ArrowRight" });
    expect(await screen.findByRole("img", { name: "Title wide" })).toBeInTheDocument();
    expect(screen.getAllByRole("img")).toHaveLength(1);
    fireEvent.keyDown(window, { key: "ArrowLeft" });
    expect(await screen.findByRole("img", { name: "Title tall-b" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Title tall-a" })).toBeInTheDocument();
  });

  it("keeps a trailing pair together when Previous wraps to it", async () => {
    vi.mocked(fetchSourceItems).mockResolvedValue([
      media("wide"),
      media("tall-e"),
      media("tall-f"),
    ]);
    renderAt("/slideshow?source=all-items");
    await screen.findByRole("img", { name: "Title wide" });
    fireEvent.keyDown(window, { key: "ArrowLeft" });
    expect(await screen.findByRole("img", { name: "Title tall-e" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Title tall-f" })).toBeInTheDocument();
    fireEvent.keyDown(window, { key: "ArrowLeft" });
    expect(await screen.findByRole("img", { name: "Title wide" })).toBeInTheDocument();
  });

  it("dims on the Dark Room schedule", async () => {
    const clock = (date: Date) =>
      `${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
    const now = Date.now();
    localStorage.setItem(
      "bf.slideshow.prefs",
      JSON.stringify({
        darkRoom: true,
        darkRoomStart: clock(new Date(now - 3_600_000)),
        darkRoomEnd: clock(new Date(now + 3_600_000)),
      }),
    );
    renderAt("/slideshow?source=favorites");
    await screen.findByRole("img", { name: "Title one" });
    expect(screen.getByTestId("slideshow-dim")).toBeInTheDocument();
    fireEvent.keyDown(window, { key: "d" });
    expect(screen.queryByTestId("slideshow-dim")).not.toBeInTheDocument();
  });

  it("dims the stage for Dark Room on D", async () => {
    renderAt("/slideshow?source=favorites");
    await screen.findByRole("img", { name: "Title one" });
    expect(screen.queryByTestId("slideshow-dim")).not.toBeInTheDocument();
    fireEvent.keyDown(window, { key: "d" });
    expect(screen.getByTestId("slideshow-dim")).toBeInTheDocument();
  });
});
