import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useNavigate } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { fetchPreviewInfo } from "../lib/playback";
import { MediaItemCard, PREVIEW_DWELL_MS } from "./MediaItemCard";

vi.mock("../lib/playback", () => ({
  fetchPreviewInfo: vi.fn(),
}));

let playSpy: ReturnType<typeof vi.spyOn>;
let pauseSpy: ReturnType<typeof vi.spyOn>;
let loadSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  vi.useFakeTimers();
  installMatchMedia();
  vi.mocked(fetchPreviewInfo).mockReset();
  vi.mocked(fetchPreviewInfo).mockResolvedValue({
    assets: { previewClip: "/api/media/assets/token/preview.mp4" },
  });
  playSpy = vi
    .spyOn(HTMLMediaElement.prototype, "play")
    .mockImplementation(() => Promise.resolve());
  pauseSpy = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  loadSpy = vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("MediaItemCard preview clip behavior", () => {
  it("starts preview playback only after the hover dwell delay", async () => {
    const { container } = renderCard();
    const card = screen.getByRole("button", { name: /Alpha/ });

    fireEvent.pointerEnter(card, { pointerType: "mouse" });
    await advancePreviewTimers(PREVIEW_DWELL_MS - 1);

    expect(previewVideo(container)).toBeNull();
    expect(fetchPreviewInfo).not.toHaveBeenCalled();
    expect(playSpy).not.toHaveBeenCalled();

    await advancePreviewTimers(1);

    expect(fetchPreviewInfo).toHaveBeenCalledWith("media-alpha");
    expect(previewVideo(container)).toHaveAttribute("src", "/api/media/assets/token/preview.mp4");
    const video = previewVideo(container);
    expect(video).toHaveProperty("muted", true);
    expect(video).toHaveProperty("loop", true);
    expect(playSpy).toHaveBeenCalledTimes(1);
  });

  it("cancels the preview when hover leaves before dwell completes", async () => {
    const { container } = renderCard();
    const card = screen.getByRole("button", { name: /Alpha/ });

    fireEvent.pointerEnter(card, { pointerType: "mouse" });
    await advancePreviewTimers(PREVIEW_DWELL_MS - 1);
    fireEvent.pointerLeave(card);
    await advancePreviewTimers(PREVIEW_DWELL_MS);

    expect(previewVideo(container)).toBeNull();
    expect(fetchPreviewInfo).not.toHaveBeenCalled();
    expect(playSpy).not.toHaveBeenCalled();
  });

  it("stops playback and removes the preview on pointer leave after playback starts", async () => {
    const { container } = renderCard();
    const card = screen.getByRole("button", { name: /Alpha/ });

    fireEvent.pointerEnter(card, { pointerType: "mouse" });
    await advancePreviewTimers(PREVIEW_DWELL_MS);
    expect(previewVideo(container)).not.toBeNull();

    fireEvent.pointerLeave(card);

    expect(previewVideo(container)).toBeNull();
    expect(pauseSpy).toHaveBeenCalled();
    expect(loadSpy).toHaveBeenCalled();
  });

  it("starts preview playback on keyboard focus after the dwell delay", async () => {
    const { container } = renderCard();
    const card = screen.getByRole("button", { name: /Alpha/ });

    fireEvent.focus(card);
    await advancePreviewTimers(PREVIEW_DWELL_MS - 1);

    expect(previewVideo(container)).toBeNull();
    expect(fetchPreviewInfo).not.toHaveBeenCalled();

    await advancePreviewTimers(1);

    expect(fetchPreviewInfo).toHaveBeenCalledWith("media-alpha");
    expect(previewVideo(container)).toHaveAttribute("src", "/api/media/assets/token/preview.mp4");
    expect(playSpy).toHaveBeenCalledTimes(1);

    fireEvent.blur(card);
    expect(previewVideo(container)).toBeNull();
  });

  it("does not start a keyboard-focus preview under reduced motion", async () => {
    installMatchMedia({ reducedMotion: true });
    const { container } = renderCard();

    fireEvent.focus(screen.getByRole("button", { name: /Alpha/ }));
    await advancePreviewTimers(PREVIEW_DWELL_MS);

    expect(previewVideo(container)).toBeNull();
    expect(fetchPreviewInfo).not.toHaveBeenCalled();
  });

  it("stops playback on card blur and browser focus loss", async () => {
    const { container } = renderCard();
    const card = screen.getByRole("button", { name: /Alpha/ });

    fireEvent.pointerEnter(card, { pointerType: "mouse" });
    await advancePreviewTimers(PREVIEW_DWELL_MS);
    expect(previewVideo(container)).not.toBeNull();

    fireEvent.blur(card);
    expect(previewVideo(container)).toBeNull();

    fireEvent.pointerEnter(card, { pointerType: "mouse" });
    await advancePreviewTimers(PREVIEW_DWELL_MS);
    expect(previewVideo(container)).not.toBeNull();

    fireEvent.blur(window);
    expect(previewVideo(container)).toBeNull();
  });

  it("stops playback on route changes", async () => {
    const { container } = render(
      <MemoryRouter>
        <RouteChangeHarness />
      </MemoryRouter>,
    );
    const card = screen.getByRole("button", { name: /Alpha/ });

    fireEvent.pointerEnter(card, { pointerType: "mouse" });
    await advancePreviewTimers(PREVIEW_DWELL_MS);
    expect(previewVideo(container)).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Change route" }));

    expect(previewVideo(container)).toBeNull();
    expect(pauseSpy).toHaveBeenCalled();
    expect(loadSpy).toHaveBeenCalled();
  });

  it("falls back to thumbnail artwork when the preview asset is missing", async () => {
    vi.mocked(fetchPreviewInfo).mockResolvedValue({ assets: {} });
    const { container } = renderCard({
      artwork: undefined,
      artworkSlots: { thumb: "/art/thumb-alpha.jpg" },
    });
    const card = screen.getByRole("button", { name: /Alpha/ });

    expect(container.querySelector("img")).toHaveAttribute(
      "src",
      "/art/thumb-alpha.jpg?bucket=w320",
    );

    fireEvent.pointerEnter(card, { pointerType: "mouse" });
    await advancePreviewTimers(PREVIEW_DWELL_MS);

    expect(fetchPreviewInfo).toHaveBeenCalledWith("media-alpha");
    expect(previewVideo(container)).toBeNull();
    expect(playSpy).not.toHaveBeenCalled();
  });

  it("keeps thumbnail-only behavior when reduced motion is preferred", async () => {
    installMatchMedia({ reducedMotion: true });
    const { container } = renderCard();

    fireEvent.pointerEnter(screen.getByRole("button", { name: /Alpha/ }), {
      pointerType: "mouse",
    });
    await advancePreviewTimers(PREVIEW_DWELL_MS);

    expect(container.querySelector("img")).toHaveAttribute(
      "src",
      "/art/poster-alpha.jpg?bucket=w320",
    );
    expect(previewVideo(container)).toBeNull();
    expect(fetchPreviewInfo).not.toHaveBeenCalled();
    expect(playSpy).not.toHaveBeenCalled();
  });

  it("keeps touch-only mobile behavior thumbnail-only", async () => {
    installMatchMedia({ finePointer: false });
    const { container } = renderCard();

    fireEvent.pointerEnter(screen.getByRole("button", { name: /Alpha/ }), {
      pointerType: "touch",
    });
    await advancePreviewTimers(PREVIEW_DWELL_MS);

    expect(previewVideo(container)).toBeNull();
    expect(fetchPreviewInfo).not.toHaveBeenCalled();
    expect(playSpy).not.toHaveBeenCalled();
  });

  it("cleans up timers and media state on unmount", async () => {
    const pending = renderCard();

    fireEvent.pointerEnter(screen.getByRole("button", { name: /Alpha/ }), {
      pointerType: "mouse",
    });
    pending.unmount();
    await advancePreviewTimers(PREVIEW_DWELL_MS);

    expect(playSpy).not.toHaveBeenCalled();

    const active = renderCard();
    fireEvent.pointerEnter(screen.getByRole("button", { name: /Alpha/ }), {
      pointerType: "mouse",
    });
    await advancePreviewTimers(PREVIEW_DWELL_MS);
    expect(playSpy).toHaveBeenCalledTimes(1);

    active.unmount();

    expect(pauseSpy).toHaveBeenCalledTimes(1);
  });
});

function renderCard(overrides: Partial<SourceItem> = {}) {
  return render(
    <MemoryRouter>
      <MediaItemCard item={baseItem(overrides)} />
    </MemoryRouter>,
  );
}

function RouteChangeHarness() {
  const navigate = useNavigate();
  return (
    <>
      <MediaItemCard item={baseItem()} />
      <button type="button" onClick={() => navigate("/next")}>
        Change route
      </button>
    </>
  );
}

function baseItem(overrides: Partial<SourceItem> = {}): SourceItem {
  return {
    id: "media-alpha",
    kind: "media",
    title: "Alpha",
    summary: "First item",
    artwork: { poster: "/art/poster-alpha.jpg" },
    ...overrides,
  };
}

function previewVideo(container: HTMLElement): HTMLVideoElement | null {
  return container.querySelector("video.bf-preview-clip");
}

async function advancePreviewTimers(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
  await act(async () => {
    await Promise.resolve();
  });
}

function installMatchMedia({
  finePointer = true,
  reducedMotion = false,
}: {
  finePointer?: boolean;
  reducedMotion?: boolean;
} = {}) {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    writable: true,
    value: vi.fn((query: string) => {
      const matches = query.includes("prefers-reduced-motion")
        ? reducedMotion
        : query.includes("hover: hover") || query.includes("pointer: fine")
          ? finePointer
          : false;
      return {
        matches,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      } as MediaQueryList;
    }),
  });
}
