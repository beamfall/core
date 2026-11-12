import "@testing-library/jest-dom/vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation, useNavigate } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchItemState, setWatched } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { STRINGS } from "@/strings";
import { readEntityPreview } from "../lib/entityPreview";
import { fetchPreviewInfo } from "../lib/playback";
import { readPlayerNavState } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import { MediaItemCard, PREVIEW_DWELL_MS } from "./MediaItemCard";

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fetchItemState: vi.fn(),
  setWatched: vi.fn(),
}));

vi.mock("../lib/playback", () => ({
  fetchPreviewInfo: vi.fn(),
}));

vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => "viewer-1",
}));

vi.mock("../player/preload", () => ({
  preloadPlayer: vi.fn(),
}));

let playSpy: ReturnType<typeof vi.spyOn>;
let pauseSpy: ReturnType<typeof vi.spyOn>;
let loadSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  vi.useFakeTimers();
  installMatchMedia();
  vi.mocked(fetchPreviewInfo).mockReset();
  vi.mocked(fetchItemState).mockReset();
  vi.mocked(setWatched).mockReset();
  vi.mocked(fetchItemState).mockResolvedValue({
    id: "media-alpha",
    kind: "media",
    watched: false,
    watchlisted: false,
    rating: 0,
  });
  vi.mocked(preloadPlayer).mockReset();
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
    const card = screen.getByRole("button", { name: /^Alpha/ });

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
    const card = screen.getByRole("button", { name: /^Alpha/ });

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
    const card = screen.getByRole("button", { name: /^Alpha/ });

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
    const card = screen.getByRole("button", { name: /^Alpha/ });

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

    fireEvent.focus(screen.getByRole("button", { name: /^Alpha/ }));
    await advancePreviewTimers(PREVIEW_DWELL_MS);

    expect(previewVideo(container)).toBeNull();
    expect(fetchPreviewInfo).not.toHaveBeenCalled();
  });

  it("stops playback on card blur and browser focus loss", async () => {
    const { container } = renderCard();
    const card = screen.getByRole("button", { name: /^Alpha/ });

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
      <I18nProvider initialLocale="en">
        <MemoryRouter>
          <RouteChangeHarness />
        </MemoryRouter>
      </I18nProvider>,
    );
    const card = screen.getByRole("button", { name: /^Alpha/ });

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
    const card = screen.getByRole("button", { name: /^Alpha/ });

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

    fireEvent.pointerEnter(screen.getByRole("button", { name: /^Alpha/ }), {
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

    fireEvent.pointerEnter(screen.getByRole("button", { name: /^Alpha/ }), {
      pointerType: "touch",
    });
    await advancePreviewTimers(PREVIEW_DWELL_MS);

    expect(previewVideo(container)).toBeNull();
    expect(fetchPreviewInfo).not.toHaveBeenCalled();
    expect(playSpy).not.toHaveBeenCalled();
  });

  it("cleans up timers and media state on unmount", async () => {
    const pending = renderCard();

    fireEvent.pointerEnter(screen.getByRole("button", { name: /^Alpha/ }), {
      pointerType: "mouse",
    });
    pending.unmount();
    await advancePreviewTimers(PREVIEW_DWELL_MS);

    expect(playSpy).not.toHaveBeenCalled();

    const active = renderCard();
    fireEvent.pointerEnter(screen.getByRole("button", { name: /^Alpha/ }), {
      pointerType: "mouse",
    });
    await advancePreviewTimers(PREVIEW_DWELL_MS);
    expect(playSpy).toHaveBeenCalledTimes(1);

    active.unmount();

    expect(pauseSpy).toHaveBeenCalledTimes(1);
  });
});

describe("MediaItemCard entity navigation", () => {
  it("carries the identity-bound entity preview into the entity page", () => {
    render(
      <I18nProvider initialLocale="en">
        <MemoryRouter>
          <MediaItemCard item={baseItem({ id: "e1", kind: "series", title: "Show" })} />
          <LocationState />
        </MemoryRouter>
      </I18nProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: /^Show/ }));

    expect(screen.getByTestId("path")).toHaveTextContent("/entity/e1");
    const state = JSON.parse(screen.getByTestId("state").textContent || "null");
    expect(readEntityPreview(state, "viewer-1", "e1")).toEqual({ title: "Show", kind: "series" });
    expect(readEntityPreview(state, "other", "e1")).toBeNull();
  });
});

describe("MediaItemCard play queue", () => {
  it("binds the queue it hands the player to the viewer identity (CORE-1143)", () => {
    const queue = [
      { id: "media-alpha", title: "Alpha" },
      { id: "media-beta", title: "Beta" },
    ];
    render(
      <I18nProvider initialLocale="en">
        <MemoryRouter>
          <MediaItemCard item={baseItem()} queue={queue} />
          <LocationState />
        </MemoryRouter>
      </I18nProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: /^Alpha/ }));

    expect(screen.getByTestId("path")).toHaveTextContent("/watch/media-alpha");
    const state = JSON.parse(screen.getByTestId("state").textContent || "null");
    expect(readPlayerNavState(state, "viewer-1").queue).toEqual(queue);
    expect(readPlayerNavState(state, "other").queue).toBeUndefined();
  });
});

describe("MediaItemCard player prefetch", () => {
  it("prefetches the player chunk on pointer enter, for any pointer type, without waiting for dwell", () => {
    renderCard();
    const card = screen.getByRole("button", { name: /^Alpha/ });

    fireEvent.pointerEnter(card, { pointerType: "touch" });

    expect(preloadPlayer).toHaveBeenCalledTimes(1);
  });

  it("prefetches the player chunk on keyboard focus", () => {
    renderCard();

    fireEvent.focus(screen.getByRole("button", { name: /^Alpha/ }));

    expect(preloadPlayer).toHaveBeenCalledTimes(1);
  });

  it("does not prefetch the player for a non-media card", () => {
    renderCard({ kind: "collection" });
    const card = screen.getByRole("button", { name: /^Alpha/ });

    fireEvent.pointerEnter(card, { pointerType: "mouse" });
    fireEvent.focus(card);

    expect(preloadPlayer).not.toHaveBeenCalled();
  });
});

function renderCard(overrides: Partial<SourceItem> = {}) {
  return render(
    <I18nProvider initialLocale="en">
      <MemoryRouter>
        <MediaItemCard item={baseItem(overrides)} />
      </MemoryRouter>
    </I18nProvider>,
  );
}

function LocationState() {
  const location = useLocation();
  return (
    <>
      <span data-testid="path">{location.pathname}</span>
      <span data-testid="state">{JSON.stringify(location.state)}</span>
    </>
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

// CORE-1193: every card names itself to assistive technology. These fail if
// the card button loses its accessible name or stops announcing state.
describe("MediaItemCard accessible name", () => {
  it("names a card with no playback state by its title", () => {
    renderCard();
    expect(screen.getByRole("button", { name: "Alpha" })).toHaveClass("bf-card");
  });

  it("adds the time left to a partly played item and draws the progress bar", () => {
    const { container } = renderCard({
      engagement: { positionSeconds: 240, durationSeconds: 888 },
    });
    expect(screen.getByRole("button", { name: "Alpha, 11 minutes left" })).toBeInTheDocument();
    const bar = container.querySelector<HTMLElement>(".bf-card-progress > span");
    expect(bar?.style.width).toBe("27%");
  });

  it("announces a finished item and marks the artwork", () => {
    const { container } = renderCard({ engagement: { completed: true } });
    expect(screen.getByRole("button", { name: "Alpha, Finished" })).toBeInTheDocument();
    expect(container.querySelector(".bf-card-badge-finished")).not.toBeNull();
    expect(container.querySelector(".bf-card-progress")).toBeNull();
  });

  it("announces a container's remaining count and shows it on the artwork", () => {
    const { container } = renderCard({
      kind: "entity",
      engagement: { totalCount: 8, remainingCount: 3 },
    });
    expect(screen.getByRole("button", { name: "Alpha, 3 unplayed" })).toBeInTheDocument();
    expect(container.querySelector(".bf-card-badge")).toHaveTextContent("3");
  });

  it("never leaves a card button without an accessible name", () => {
    renderCard({ engagement: { completed: true } });
    for (const button of screen.getAllByRole("button")) {
      expect(button).toHaveAccessibleName();
    }
  });
});

describe("MediaItemCard actions", () => {
  it("offers Resume from the keyboard-operable More menu and plays the item", async () => {
    vi.useRealTimers();
    render(
      <I18nProvider initialLocale="en">
        <MemoryRouter initialEntries={["/"]}>
          <MediaItemCard
            item={baseItem({ engagement: { positionSeconds: 240, durationSeconds: 888 } })}
          />
          <LocationState />
        </MemoryRouter>
      </I18nProvider>,
    );
    const trigger = screen.getByRole("button", { name: "More actions for Alpha" });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "Enter" });
    fireEvent.click(trigger);

    const resume = await screen.findByRole("menuitem", { name: "Resume" });
    expect(screen.getByRole("menuitem", { name: "Copy link" })).toBeInTheDocument();
    fireEvent.click(resume);

    expect(screen.getByTestId("path")).toHaveTextContent("/watch/media-alpha");
  });

  it("marks the item watched from the More menu, loading its state only once opened", async () => {
    vi.useRealTimers();
    const state = {
      id: "media-alpha",
      kind: "media",
      watched: false,
      watchlisted: false,
      rating: 0,
    };
    vi.mocked(fetchItemState).mockResolvedValue(state);
    vi.mocked(setWatched).mockResolvedValue({ ...state, watched: true });
    renderCard();
    expect(fetchItemState).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "More actions for Alpha" }));
    const mark = await screen.findByRole("menuitem", { name: "Mark watched" });
    await waitFor(() => expect(mark).not.toHaveAttribute("aria-disabled", "true"));
    fireEvent.click(mark);

    await waitFor(() => expect(setWatched).toHaveBeenCalledWith({ mediaID: "media-alpha" }, true));
  });

  it("reports a failed mark-watched on the card after the menu closes", async () => {
    vi.useRealTimers();
    const state = {
      id: "media-alpha",
      kind: "media",
      watched: false,
      watchlisted: false,
      rating: 0,
    };
    vi.mocked(fetchItemState).mockResolvedValue(state);
    vi.mocked(setWatched).mockRejectedValue(new Error("offline"));
    renderCard();
    expect(screen.queryByRole("status")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "More actions for Alpha" }));
    const mark = await screen.findByRole("menuitem", { name: "Mark watched" });
    await waitFor(() => expect(mark).not.toHaveAttribute("aria-disabled", "true"));
    fireEvent.click(mark);

    const status = await screen.findByRole("status");
    expect(status).toHaveTextContent(STRINGS.en.itemActionFailed);
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    expect(status).toBeInTheDocument();
  });

  it("retries a failed state load when the More menu opens again", async () => {
    vi.useRealTimers();
    vi.mocked(fetchItemState).mockRejectedValueOnce(new Error("offline"));
    renderCard();
    const more = screen.getByRole("button", { name: "More actions for Alpha" });

    fireEvent.click(more);
    const mark = await screen.findByRole("menuitem", { name: "Mark watched" });
    await screen.findByRole("status");
    expect(mark).toHaveAttribute("aria-disabled", "true");
    fireEvent.keyDown(mark, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());

    fireEvent.click(more);
    const again = await screen.findByRole("menuitem", { name: "Mark watched" });
    await waitFor(() => expect(again).not.toHaveAttribute("aria-disabled", "true"));
    expect(fetchItemState).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("keeps the touch menu target at 44px, declared after the base row height", () => {
    const css = readFileSync(resolve(process.cwd(), "src/components/chrome/chrome.css"), "utf8");
    const base = css.indexOf(".bf-card-menu-item {\n  display: flex;");
    const touch = css.search(
      /@media \(hover: none\) \{\s*\.bf-card-menu-item \{\s*min-height: 44px;/,
    );
    expect(base).toBeGreaterThan(-1);
    expect(touch).toBeGreaterThan(base);
  });

  it("offers Open, not Play, for an item that is not playable itself", async () => {
    vi.useRealTimers();
    renderCard({ kind: "entity" });
    fireEvent.click(screen.getByRole("button", { name: "More actions for Alpha" }));
    expect(await screen.findByRole("menuitem", { name: "Open" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Play" })).toBeNull();
  });
});
