import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef, useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import type { Locale } from "@/strings";
import { PlayerTransport } from "./PlayerTransport";
import type { PlayerState } from "./usePlayer";

const state: PlayerState = {
  phase: "playing",
  message: "",
  terminal: false,
  info: null,
  stream: { kind: "direct-play", url: "/api/media/token" },
  decision: "direct-play",
  markers: [],
  trickplay: null,
  trickplayBaseURL: "",
  hlsActive: false,
  tracksVersion: 0,
};

beforeEach(() => {
  HTMLElement.prototype.setPointerCapture = vi.fn();
  HTMLElement.prototype.releasePointerCapture = vi.fn();
  HTMLElement.prototype.hasPointerCapture = vi.fn(() => false);
});

afterEach(() => {
  vi.restoreAllMocks();
  Reflect.deleteProperty(document, "pictureInPictureEnabled");
  Reflect.deleteProperty(document, "pictureInPictureElement");
  Reflect.deleteProperty(document, "exitPictureInPicture");
  Reflect.deleteProperty(HTMLVideoElement.prototype, "requestPictureInPicture");
});

describe("PlayerTransport", () => {
  it("keeps the meta, seek, and single transport row hierarchy (CORE-1141)", () => {
    render(<Harness />);

    const title = screen.getByRole("heading", { name: "Long Weekend" });
    expect(screen.getByText("Direct play")).toHaveClass("bf-route-summary");
    const seek = screen.getByRole("slider", { name: "Playback position" }).closest(".bf-scrub-row");
    const row = screen.getByRole("button", { name: "Back 10s" }).closest(".bf-transport-row");
    const primary = screen
      .getByRole("button", { name: "Back 10s" })
      .closest<HTMLElement>(".bf-primary-controls");

    if (!seek || !row || !primary) throw new Error("player hierarchy groups are missing");
    expect(row).toContainElement(primary);
    expect(row).toContainElement(screen.getByRole("slider", { name: "Volume" }));
    expect(row).toContainElement(screen.getByRole("button", { name: "Settings" }));
    expect(title.compareDocumentPosition(seek) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(seek.compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });

  it("exposes a keyboard-operable fullscreen control", () => {
    Object.defineProperty(HTMLElement.prototype, "requestFullscreen", {
      configurable: true,
      value: vi.fn(),
    });
    Object.defineProperty(document, "exitFullscreen", {
      configurable: true,
      value: vi.fn(),
    });
    const requestFullscreen = vi
      .spyOn(HTMLElement.prototype, "requestFullscreen")
      .mockResolvedValue(undefined);
    const exitFullscreen = vi.spyOn(document, "exitFullscreen").mockResolvedValue(undefined);
    let fullscreenElement: Element | null = null;
    Object.defineProperty(document, "fullscreenElement", {
      configurable: true,
      get: () => fullscreenElement,
    });

    render(<Harness />);
    const stage = screen.getByTestId("stage");
    expect(screen.getByRole("slider", { name: "Playback position" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Fullscreen" }));
    expect(requestFullscreen).toHaveBeenCalled();

    fullscreenElement = stage;
    fireEvent(document, new Event("fullscreenchange"));
    fireEvent.click(screen.getByRole("button", { name: "Exit fullscreen" }));
    expect(exitFullscreen).toHaveBeenCalled();
  });

  it("shares the current playback offset", () => {
    const onShare = vi.fn();
    render(<Harness onShare={onShare} />);
    const video = screen.getByTestId("video") as HTMLVideoElement;
    Object.defineProperty(video, "currentTime", { configurable: true, value: 83.8 });

    fireEvent.timeUpdate(video);
    fireEvent.click(screen.getByRole("button", { name: "Share current time" }));

    expect(onShare).toHaveBeenCalledWith(83);
  });

  it("maps unmodified non-interactive keyboard shortcuts and clamps seek and volume", () => {
    Object.defineProperty(HTMLElement.prototype, "requestFullscreen", {
      configurable: true,
      value: vi.fn().mockResolvedValue(undefined),
    });
    Object.defineProperty(document, "fullscreenElement", {
      configurable: true,
      value: null,
    });
    const { play, pause, video } = renderHarnessVideo({
      currentTime: 5,
      duration: 100,
      volume: 0.98,
    });

    expect(dispatchKey(" ").defaultPrevented).toBe(true);
    expect(play).toHaveBeenCalledOnce();
    expect(dispatchKey("K").defaultPrevented).toBe(true);
    expect(pause).toHaveBeenCalledOnce();

    expect(dispatchKey("ArrowLeft").defaultPrevented).toBe(true);
    expect(video.currentTime).toBe(0);
    video.currentTime = 95;
    dispatchKey("ArrowRight");
    expect(video.currentTime).toBe(100);

    dispatchKey("ArrowUp");
    expect(video.volume).toBe(1);
    video.volume = 0.02;
    dispatchKey("ArrowDown");
    expect(video.volume).toBe(0);

    dispatchKey("f");
    expect(HTMLElement.prototype.requestFullscreen).toHaveBeenCalledOnce();
    video.volume = 0.65;
    dispatchKey("m");
    expect(video.muted).toBe(true);
    expect(video.volume).toBe(0.65);
    dispatchKey("m");
    expect(video.muted).toBe(false);
    expect(video.volume).toBe(0.65);
  });

  it("leaves modified, unknown, and interactive keyboard input native", () => {
    const { play, video } = renderHarnessVideo({ currentTime: 50, duration: 100 });
    const volume = screen.getByRole("slider", { name: "Volume" });

    const modified = dispatchKey("ArrowRight", { ctrlKey: true });
    expect(modified.defaultPrevented).toBe(false);
    expect(video.currentTime).toBe(50);

    const unknown = dispatchKey("Escape");
    expect(unknown.defaultPrevented).toBe(false);

    volume.focus();
    const interactive = dispatchKey(" ");
    expect(interactive.defaultPrevented).toBe(false);
    expect(play).not.toHaveBeenCalled();

    const editable = document.createElement("div");
    editable.setAttribute("contenteditable", "true");
    editable.tabIndex = 0;
    document.body.append(editable);
    editable.focus();
    const contenteditable = dispatchKey("k");
    expect(contenteditable.defaultPrevented).toBe(false);
    editable.remove();
  });

  it("removes shortcut and gesture listeners on unmount", () => {
    const { stage, unmount, video } = renderHarnessVideo({ currentTime: 50, duration: 100 });
    mockStageRect(stage);
    unmount();

    expect(dispatchKey("ArrowRight").defaultPrevented).toBe(false);
    fireTouch(stage, "pointerdown", { pointerId: 1, clientX: 75, clientY: 50 });
    fireTouch(stage, "pointerup", { pointerId: 1, clientX: 75, clientY: 50 });
    expect(video.currentTime).toBe(50);
  });

  it("mirrors live volume and speed, exposes their values, and restores muted volume", async () => {
    const { video } = renderHarnessVideo({ volume: 0.4, playbackRate: 1 });
    const user = userEvent.setup();
    const volume = screen.getByRole("slider", { name: "Volume" });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    const speed = screen.getByRole("combobox", { name: "Playback speed" });

    expect(volume).toHaveAttribute("aria-valuetext", "40% volume");
    fireEvent.change(volume, { target: { value: "0.8" } });
    expect(video.volume).toBe(0.8);

    video.volume = 0.35;
    fireEvent(video, new Event("volumechange"));
    expect(volume).toHaveValue("0.35");

    await user.click(speed);
    await user.click(await screen.findByRole("option", { name: "1.5×" }));
    expect(video.playbackRate).toBe(1.5);
    video.playbackRate = 0.75;
    fireEvent(video, new Event("ratechange"));
    expect(speed).toHaveTextContent("0.75×");

    fireEvent.click(screen.getByRole("button", { name: "Mute" }));
    expect(video.muted).toBe(true);
    expect(video.volume).toBe(0.35);
    fireEvent.click(screen.getByRole("button", { name: "Unmute" }));
    expect(video.muted).toBe(false);
    expect(video.volume).toBe(0.35);
  });

  it("renders an honest disabled Picture in Picture state when unavailable", () => {
    render(<Harness />);

    expect(screen.getByRole("button", { name: "Picture in Picture" })).toBeDisabled();
    expect(screen.getByText("Picture in Picture is unavailable in this browser.")).toBeVisible();
  });

  it("tracks Picture in Picture entry, exit, and browser leave events", async () => {
    let pictureInPictureElement: Element | null = null;
    Object.defineProperty(document, "pictureInPictureEnabled", {
      configurable: true,
      value: true,
    });
    Object.defineProperty(document, "pictureInPictureElement", {
      configurable: true,
      get: () => pictureInPictureElement,
    });
    Object.defineProperty(HTMLVideoElement.prototype, "requestPictureInPicture", {
      configurable: true,
      value: vi.fn(function (this: HTMLVideoElement) {
        pictureInPictureElement = this;
        this.dispatchEvent(new Event("enterpictureinpicture"));
        return Promise.resolve({});
      }),
    });
    Object.defineProperty(document, "exitPictureInPicture", {
      configurable: true,
      value: vi.fn(() => {
        pictureInPictureElement?.dispatchEvent(new Event("leavepictureinpicture"));
        pictureInPictureElement = null;
        return Promise.resolve();
      }),
    });
    const { video } = renderHarnessVideo();

    const enter = await screen.findByRole("button", { name: "Picture in Picture" });
    await waitFor(() => expect(enter).toBeEnabled());
    fireEvent.click(enter);
    await screen.findByRole("button", { name: "Exit Picture in Picture" });

    fireEvent.click(screen.getByRole("button", { name: "Exit Picture in Picture" }));
    await screen.findByRole("button", { name: "Picture in Picture" });
    expect(document.exitPictureInPicture).toHaveBeenCalledOnce();

    pictureInPictureElement = video;
    fireEvent(video, new Event("enterpictureinpicture"));
    expect(screen.getByRole("button", { name: "Exit Picture in Picture" })).toBeVisible();
    pictureInPictureElement = null;
    fireEvent(video, new Event("leavepictureinpicture"));
    expect(screen.getByRole("button", { name: "Picture in Picture" })).toBeVisible();
  });

  it("reports Picture in Picture rejection without claiming an active state", async () => {
    Object.defineProperty(document, "pictureInPictureEnabled", {
      configurable: true,
      value: true,
    });
    Object.defineProperty(document, "pictureInPictureElement", {
      configurable: true,
      value: null,
    });
    Object.defineProperty(document, "exitPictureInPicture", {
      configurable: true,
      value: vi.fn().mockResolvedValue(undefined),
    });
    Object.defineProperty(HTMLVideoElement.prototype, "requestPictureInPicture", {
      configurable: true,
      value: vi.fn().mockRejectedValue(new Error("denied")),
    });
    render(<Harness />);

    const button = screen.getByRole("button", { name: "Picture in Picture" });
    await waitFor(() => expect(button).toBeEnabled());
    fireEvent.click(button);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Picture in Picture could not be started.",
    );
    expect(button).toHaveAccessibleName("Picture in Picture");
  });

  it("reports Picture in Picture exit rejection without claiming it stopped", async () => {
    let pictureInPictureElement: Element | null = null;
    Object.defineProperty(document, "pictureInPictureEnabled", {
      configurable: true,
      value: true,
    });
    Object.defineProperty(document, "pictureInPictureElement", {
      configurable: true,
      get: () => pictureInPictureElement,
    });
    Object.defineProperty(HTMLVideoElement.prototype, "requestPictureInPicture", {
      configurable: true,
      value: vi.fn(function (this: HTMLVideoElement) {
        pictureInPictureElement = this;
        this.dispatchEvent(new Event("enterpictureinpicture"));
        return Promise.resolve({});
      }),
    });
    Object.defineProperty(document, "exitPictureInPicture", {
      configurable: true,
      value: vi.fn().mockRejectedValue(new Error("denied")),
    });
    render(<Harness />);

    const enter = screen.getByRole("button", { name: "Picture in Picture" });
    await waitFor(() => expect(enter).toBeEnabled());
    fireEvent.click(enter);

    const exit = await screen.findByRole("button", { name: "Exit Picture in Picture" });
    fireEvent.click(exit);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Picture in Picture could not be exited.",
    );
    expect(exit).toHaveAccessibleName("Exit Picture in Picture");
  });

  it("double-taps either stage half to seek with accumulation and excludes controls", () => {
    let now = 1_000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    const { stage, video } = renderHarnessVideo({ currentTime: 50, duration: 100 });
    mockStageRect(stage);

    tap(stage, 75, 1);
    now += 100;
    tap(stage, 75, 2);
    expect(video.currentTime).toBe(60);
    now += 100;
    tap(stage, 75, 3);
    expect(video.currentTime).toBe(70);
    expect(screen.getByText("Forward 20 seconds")).toBeVisible();

    now += 500;
    tap(stage, 25, 4);
    now += 100;
    tap(stage, 25, 5);
    expect(video.currentTime).toBe(60);

    const control = screen.getByRole("button", { name: "Play" });
    now += 500;
    tap(control, 75, 6);
    now += 100;
    tap(control, 75, 7);
    expect(video.currentTime).toBe(60);
    const scrubber = screen.getByRole("slider", { name: "Playback position" });
    now += 500;
    tap(scrubber, 75, 8);
    now += 100;
    tap(scrubber, 75, 9);
    expect(video.currentTime).toBe(60);
  });

  it("uses only threshold-crossing right-half vertical swipes and clamps volume", () => {
    const { stage, video } = renderHarnessVideo({ volume: 0.5 });
    mockStageRect(stage);

    fireTouch(stage, "pointerdown", { pointerId: 1, clientX: 75, clientY: 60 });
    fireTouch(stage, "pointermove", { pointerId: 1, clientX: 75, clientY: 40 });
    expect(video.volume).toBe(0.5);
    fireTouch(stage, "pointermove", { pointerId: 1, clientX: 75, clientY: -20 });
    expect(video.volume).toBe(1);
    fireTouch(stage, "pointerup", { pointerId: 1, clientX: 75, clientY: -20 });

    video.volume = 0.5;
    fireTouch(stage, "pointerdown", { pointerId: 2, clientX: 75, clientY: 40 });
    fireTouch(stage, "pointermove", { pointerId: 2, clientX: 75, clientY: 140 });
    expect(video.volume).toBe(0);

    video.volume = 0.5;
    fireTouch(stage, "pointerdown", { pointerId: 3, clientX: 25, clientY: 60 });
    fireTouch(stage, "pointermove", { pointerId: 3, clientX: 25, clientY: 0 });
    expect(video.volume).toBe(0.5);
  });

  it("gives every icon-only transport button a non-empty accessible name (CORE-0045)", () => {
    const { container } = render(<Harness onShare={vi.fn()} />);

    const icons = [...container.querySelectorAll<HTMLButtonElement>("button.is-icon")];
    // back, play/pause, forward, previous/next chapter, mute, share, settings, PiP, fullscreen
    expect(icons).toHaveLength(10);
    for (const button of icons) {
      expect(button.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
      expect(button.getAttribute("aria-label")?.trim()).toBeTruthy();
      expect(button.querySelector(".sr-only")?.textContent?.trim()).toBeTruthy();
      expect(screen.getByRole("button", { name: button.getAttribute("aria-label") ?? "" })).toBe(
        button,
      );
    }
  });

  const settingsLabel = {
    en: "Settings",
    fr: "Réglages",
    de: "Einstellungen",
    es: "Ajustes",
    pt: "Configurações",
  } as const;

  it.each([
    ["en", "Volume", "Playback speed", "Picture in Picture"],
    ["fr", "Volume", "Vitesse de lecture", "Image dans l’image"],
    ["de", "Lautstärke", "Wiedergabegeschwindigkeit", "Bild-in-Bild"],
    ["es", "Volumen", "Velocidad de reproducción", "Imagen en imagen"],
    ["pt", "Volume", "Velocidade de reprodução", "Imagem em imagem"],
  ] as const)("localizes accessible player controls for %s", async (locale, volume, speed, pip) => {
    render(<Harness locale={locale} />);

    expect(screen.getByRole("slider", { name: volume })).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: settingsLabel[locale] }));
    expect(screen.getByRole("combobox", { name: speed })).toBeVisible();
    expect(screen.getByRole("button", { name: pip })).toBeVisible();
  });
  it("previews a scrubber drag and seeks once on release (CORE-1360)", () => {
    const { video } = renderHarnessVideo({ currentTime: 10, duration: 100 });
    const seeks: number[] = [];
    let time = 10;
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      get: () => time,
      set: (value: number) => {
        time = value;
        seeks.push(value);
      },
    });
    const scrubber = screen.getByRole("slider", { name: "Playback position" });
    const control = scrubber.closest(".bf-slider-control") as HTMLElement;
    mockStageRect(control);

    fireMouse(control, "pointerdown", 20);
    fireMouse(document, "pointermove", 40);
    fireMouse(document, "pointermove", 60);
    expect(seeks).toEqual([]);
    expect(scrubber).toHaveAttribute("aria-valuetext", expect.stringContaining("1 minute"));
    expect(screen.getAllByText("1:00").length).toBeGreaterThan(0);
    // Playback moving on underneath does not pull the previewed thumb back.
    fireEvent.timeUpdate(video);
    expect(scrubber).toHaveAttribute("aria-valuetext", expect.stringContaining("1 minute"));

    fireMouse(document, "pointerup", 60);
    expect(seeks).toEqual([60]);
  });

  it("seeks on a touch tap of the scrubber (CORE-1360)", () => {
    const { video } = renderHarnessVideo({ currentTime: 10, duration: 100 });
    const seeks: number[] = [];
    let time = 10;
    Object.defineProperty(video, "currentTime", {
      configurable: true,
      get: () => time,
      set: (value: number) => {
        time = value;
        seeks.push(value);
      },
    });
    const scrubber = screen.getByRole("slider", { name: "Playback position" });
    const control = scrubber.closest(".bf-slider-control") as HTMLElement;
    mockStageRect(control);
    const touch = { identifier: 0, clientX: 20, clientY: 5 };

    // A finger fires touchstart and pointerdown at the same spot; the slider
    // skips its own commit for that pair, so the release must still seek.
    fireEvent.touchStart(control, { changedTouches: [touch], touches: [touch] });
    fireTouch(control, "pointerdown", { pointerId: 2, clientX: 20, clientY: 5 });
    fireTouch(control, "pointerup", { pointerId: 2, clientX: 20, clientY: 5 });
    fireEvent.touchEnd(control, { changedTouches: [touch], touches: [] });
    expect(seeks).toEqual([20]);
  });

  it("seeks straight away on scrubber keyboard steps (CORE-1360)", () => {
    const { video } = renderHarnessVideo({ currentTime: 50, duration: 100 });
    const scrubber = screen.getByRole("slider", { name: "Playback position" });

    fireEvent.keyDown(scrubber, { key: "ArrowRight" });
    expect(video.currentTime).toBe(55);
    fireEvent.keyDown(scrubber, { key: "End" });
    expect(video.currentTime).toBe(100);
  });

  it("routes keyboard seeks through the configured skip interval", () => {
    render(<Harness skipInterval={30} />);
    const video = screen.getByTestId("video") as HTMLVideoElement;
    Object.defineProperty(video, "duration", { configurable: true, value: 600 });
    video.currentTime = 100;

    expect(dispatchKey("ArrowRight").defaultPrevented).toBe(true);
    expect(video.currentTime).toBe(130);
    dispatchKey("ArrowLeft");
    expect(video.currentTime).toBe(100);
    expect(screen.getByRole("button", { name: "Forward 30s" })).toBeInTheDocument();
  });

  it("reports the menu closed when the transport unmounts", async () => {
    const onMenuOpenChange = vi.fn();
    const view = render(<Harness onMenuOpenChange={onMenuOpenChange} />);
    await userEvent.click(screen.getByRole("button", { name: "Settings" }));
    await waitFor(() => expect(onMenuOpenChange).toHaveBeenLastCalledWith(true));

    view.unmount();
    expect(onMenuOpenChange).toHaveBeenLastCalledWith(false);
  });
});

function Harness({
  onShare,
  onMenuOpenChange,
  skipInterval,
  locale = "en",
}: {
  onShare?: (seconds: number) => void;
  onMenuOpenChange?: (open: boolean) => void;
  skipInterval?: number;
  locale?: Locale;
}) {
  const videoRef = createRef<HTMLVideoElement>();
  const hlsRef = createRef<null>();
  const stageRef = useRef<HTMLDivElement | null>(null);
  return (
    <I18nProvider initialLocale={locale}>
      <div ref={stageRef} data-testid="stage">
        <video data-testid="video" ref={videoRef} />
        <PlayerTransport
          videoRef={videoRef}
          hlsRef={hlsRef}
          state={state}
          stageRef={stageRef}
          mediaTitle="Long Weekend"
          onShare={onShare}
          onMenuOpenChange={onMenuOpenChange}
          skipInterval={skipInterval}
        />
      </div>
    </I18nProvider>
  );
}

function renderHarnessVideo({
  currentTime = 0,
  duration = 100,
  volume = 1,
  playbackRate = 1,
}: {
  currentTime?: number;
  duration?: number;
  volume?: number;
  playbackRate?: number;
} = {}) {
  const rendered = render(<Harness />);
  const video = screen.getByTestId("video") as HTMLVideoElement;
  const stage = screen.getByTestId("stage");
  let paused = true;
  Object.defineProperties(video, {
    currentTime: { configurable: true, writable: true, value: currentTime },
    duration: { configurable: true, writable: true, value: duration },
    volume: { configurable: true, writable: true, value: volume },
    muted: { configurable: true, writable: true, value: false },
    playbackRate: { configurable: true, writable: true, value: playbackRate },
    paused: { configurable: true, get: () => paused },
  });
  const play = vi.fn(() => {
    paused = false;
    fireEvent.play(video);
    return Promise.resolve();
  });
  const pause = vi.fn(() => {
    paused = true;
    fireEvent.pause(video);
  });
  video.play = play;
  video.pause = pause;
  fireEvent.loadedMetadata(video);
  return { ...rendered, pause, play, stage, video };
}

function dispatchKey(key: string, init: KeyboardEventInit = {}) {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  fireEvent(window, event);
  return event;
}

function mockStageRect(stage: HTMLElement) {
  vi.spyOn(stage, "getBoundingClientRect").mockReturnValue({
    bottom: 100,
    height: 100,
    left: 0,
    right: 100,
    top: 0,
    width: 100,
    x: 0,
    y: 0,
    toJSON: () => ({}),
  });
}

function fireTouch(
  target: Element,
  type: "pointerdown" | "pointermove" | "pointerup",
  init: { pointerId: number; clientX: number; clientY: number },
) {
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    clientX: init.clientX,
    clientY: init.clientY,
  });
  Object.defineProperties(event, {
    pointerId: { value: init.pointerId },
    pointerType: { value: "touch" },
  });
  fireEvent(target, event);
}

function tap(target: Element, clientX: number, pointerId: number) {
  fireTouch(target, "pointerdown", { pointerId, clientX, clientY: 50 });
  fireTouch(target, "pointerup", { pointerId, clientX, clientY: 50 });
}

function fireMouse(
  target: Element | Document,
  type: "pointerdown" | "pointermove" | "pointerup",
  clientX: number,
) {
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    buttons: type === "pointerup" ? 0 : 1,
    clientX,
    clientY: 5,
  });
  Object.defineProperties(event, {
    pointerId: { value: 1 },
    pointerType: { value: "mouse" },
  });
  fireEvent(target, event);
}
