import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { fetchSourceItems } from "@/lib/graphql/viewer";
import { I18nProvider } from "@/lib/i18n";
import type { SlideMotionDriver } from "./lib/slideshowMotion";
import { SlideshowPage } from "./SlideshowPage";

// The page's side of the motion seam (CORE-2981): a fake Calm driver whose
// whenPresented the test resolves by hand.
const drivers: (SlideMotionDriver & { advance: ReturnType<typeof vi.fn> })[] = [];
let present: () => void = () => {};

vi.mock("./lib/slideshowMotion", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./lib/slideshowMotion")>()),
  oscilluxSlideMotion: (motion: string) => {
    if (motion !== "calm") return null;
    const driver = {
      advance: vi.fn(),
      setRunning: vi.fn(),
      dispose: vi.fn(),
      whenPresented: vi.fn(
        () =>
          new Promise<void>((resolve) => {
            present = resolve;
          }),
      ),
    };
    drivers.push(driver);
    return driver;
  },
}));

vi.mock("@/lib/graphql/viewer", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql/viewer")>()),
  fetchSourceItems: vi.fn(),
}));
vi.mock("../viewer/lib/mediaDetail", () => ({ fetchMediaDetailFields: vi.fn(async () => []) }));
vi.mock("../viewer/lib/playback", () => ({ fetchPreviewInfo: vi.fn(async () => ({})) }));
vi.mock("../viewer/actions", () => ({
  useHiddenItems: () => ({
    mutes: [],
    failed: false,
    isHidden: () => false,
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

function media(id: string): SourceItem {
  return {
    id,
    kind: "media",
    title: `Title ${id}`,
    mediaClass: "image",
    artworkSlots: { poster: `/artwork/${id}.jpg` },
  };
}

/** jsdom never decodes images: every probe "loads" landscape. */
class FakeImage {
  decoding = "";
  naturalWidth = 1600;
  naturalHeight = 1000;
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  set src(_url: string) {
    queueMicrotask(() => this.onload?.());
  }
}

function renderAt(path: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/slideshow" element={<SlideshowPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

const flush = () => act(async () => {});

describe("SlideshowPage motion seam", () => {
  beforeEach(() => {
    drivers.length = 0;
    vi.stubGlobal("Image", FakeImage);
  });

  afterEach(() => {
    vi.useRealTimers();
    cleanup();
    vi.unstubAllGlobals();
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("starts a Calm slide's interval when the driver presents it, not at its DOM load", async () => {
    vi.mocked(fetchSourceItems).mockResolvedValue([media("one"), media("two")]);
    renderAt("/slideshow?source=favorites&library=lib");
    const first = await screen.findByRole("img", { name: "Title one" });
    vi.useFakeTimers();
    fireEvent.load(first);
    await flush();
    await flush();
    expect(drivers.at(-1)?.whenPresented).toHaveBeenCalled();
    // The canvas still holds black: a whole interval passes and the slide stays.
    await act(async () => vi.advanceTimersByTime(13_000));
    expect(screen.getByRole("img", { name: "Title one" })).toBeInTheDocument();
    present();
    await flush();
    await act(async () => vi.advanceTimersByTime(13_000));
    expect(screen.queryByRole("img", { name: "Title one" })).toBeNull();
  });

  it("hands the mounted slide to a Calm driver started after it (Still to Calm)", async () => {
    localStorage.setItem("bf.slideshow.prefs", JSON.stringify({ motion: "still" }));
    vi.mocked(fetchSourceItems).mockResolvedValue([media("one")]);
    renderAt("/slideshow?source=favorites&library=lib");
    const image = await screen.findByRole("img", { name: "Title one" });
    expect(drivers).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Slideshow settings" }));
    fireEvent.click(await screen.findByRole("radio", { name: /Calm/ }));
    expect(drivers).toHaveLength(1);
    const slide = image.closest(".bf-slideshow-slide");
    expect(drivers[0].advance).toHaveBeenCalledWith(null, slide);
  });
});
