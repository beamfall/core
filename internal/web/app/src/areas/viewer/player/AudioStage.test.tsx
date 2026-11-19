import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MediaCredit } from "@/lib/graphql/types";
import { AudioStage } from "./AudioStage";
import { resetTrackChangeMotionForTest, setTrackChangeMotion } from "./trackChangeMotion";

const credit = (title: string, archetype: string): MediaCredit => ({
  entityID: title,
  title,
  kind: "k",
  archetype,
  relation: "r",
});

// Most tests pin whole-line words so their text reads as one node; the
// per-letter styles have their own tests below.
beforeEach(() => {
  window.localStorage.clear();
  resetTrackChangeMotionForTest();
  setTrackChangeMotion({ words: "wipe", art: "zoom" });
});

describe("AudioStage (CORE-1143)", () => {
  it("shows the title, queue position and credits grouped by archetype", () => {
    render(
      <AudioStage
        title="Song"
        artworkURL="/art.jpg"
        queuePosition="2 of 9"
        credits={[credit("Ann", "person"), credit("Bo", "person"), credit("Album", "container")]}
      />,
    );
    expect(screen.getByText("Song")).toBeTruthy();
    expect(screen.getByText("2 of 9")).toBeTruthy();
    expect(screen.getByText("Ann, Bo")).toBeTruthy();
    expect(screen.getByText("Album")).toBeTruthy();
  });

  it("falls back quietly when the artwork fails to load", () => {
    const { container } = render(<AudioStage title="Song" artworkURL="/bad.jpg" credits={[]} />);
    const img = container.querySelector(".bf-audio-artwork img");
    expect(img).toBeTruthy();
    fireEvent.error(img!);
    expect(container.querySelector(".bf-audio-artwork img")).toBeNull();
    expect(container.querySelector(".bf-audio-artwork")).toBeTruthy();
  });

  it("reserves the visualizer slot and leaves it empty without a player (CORE-1309)", () => {
    const { container } = render(<AudioStage title="Song" artworkURL="" credits={[]} />);
    const slot = container.querySelector("[data-visualizer-slot]");
    expect(slot).toBeTruthy();
    expect(slot?.getAttribute("aria-hidden")).toBe("true");
    expect(slot?.childElementCount).toBe(0);
    expect(container.querySelector("canvas")).toBeNull();
  });
});

describe("AudioStage visualizer layout", () => {
  let style: HTMLStyleElement;

  beforeEach(() => {
    // jsdom cannot parse the full sheet. Use the shipped slot and canvas rules.
    const css = readFileSync(join(__dirname, "player-deferred.css"), "utf8");
    const rules = css.match(/^\.bf-audio-visualizer[^\n]*\{[^}]*\}/gm) ?? [];
    style = document.createElement("style");
    style.textContent = rules.join("\n");
    document.head.append(style);
  });

  afterEach(() => style.remove());

  function stage() {
    const { container } = render(<AudioStage title="Song" artworkURL="/art.jpg" credits={[]} />);
    const slot = container.querySelector<HTMLElement>("[data-visualizer-slot]")!;
    const canvas = document.createElement("canvas");
    canvas.className = "bf-audio-visualizer-canvas";
    return { container, slot, canvas };
  }

  it("anchors the canvas at the slot's top-left as the letterbox changes", () => {
    const { slot, canvas } = stage();
    slot.append(canvas);
    canvas.style.width = "1920px";
    for (const width of ["1920px", "1922px"]) {
      slot.style.width = width;
      const origin = getComputedStyle(canvas);
      expect(getComputedStyle(slot).position).toBe("absolute");
      expect(origin.position).toBe("absolute");
      expect(origin.top).toBe("0px");
      expect(origin.left).toBe("0px");
    }
  });

  // The sheet gives the slot no background, least of all a themed one: the visualizer paints it
  // the flash limiter's black behind its canvas (Visualizer.test.tsx, Codex round 25).
  it("leaves the artwork backdrop visible through the slot", () => {
    const { container, slot, canvas } = stage();
    const background = () => getComputedStyle(slot).backgroundColor;
    expect(container.querySelector(".bf-audio-backdrop img")?.getAttribute("src")).toBe("/art.jpg");
    expect(background()).toBe("rgba(0, 0, 0, 0)");
    slot.append(canvas);
    expect(background()).toBe("rgba(0, 0, 0, 0)");
  });
});

describe("AudioStage track change (CORE-2218)", () => {
  afterEach(() => vi.useRealTimers());

  const art = (c: HTMLElement) =>
    [...c.querySelectorAll<HTMLImageElement>(".bf-audio-artwork img")].map((i) => [
      i.getAttribute("src"),
      i.dataset.state,
    ]);
  // The reveal waits two frames so the hidden start state is painted first.
  const load = (img: Element) => {
    fireEvent.load(img);
    act(() => vi.advanceTimersByTime(40));
  };

  it("keeps the old artwork under the new one until it loads, then drops it", () => {
    vi.useFakeTimers();
    const { container, rerender } = render(
      <AudioStage title="A" trackKey="a" artworkURL="/a.jpg" credits={[]} />,
    );
    expect(art(container)).toEqual([["/a.jpg", "pending"]]);
    load(container.querySelector(".bf-audio-artwork img")!);
    expect(art(container)).toEqual([["/a.jpg", "in"]]);

    rerender(<AudioStage title="B" trackKey="b" artworkURL="/b.jpg" credits={[]} />);
    expect(art(container)).toEqual([
      ["/a.jpg", "under"],
      ["/b.jpg", "pending"],
    ]);
    act(() => vi.advanceTimersByTime(5000));
    expect(art(container)).toHaveLength(2);

    load(container.querySelectorAll(".bf-audio-artwork img")[1]!);
    expect(art(container)).toEqual([
      ["/a.jpg", "out"],
      ["/b.jpg", "in"],
    ]);
    act(() => vi.advanceTimersByTime(700));
    expect(art(container)).toEqual([["/b.jpg", "in"]]);
  });

  it("changes to the placeholder the same way when the next item has none (CORE-2332)", () => {
    vi.useFakeTimers();
    const { container, rerender } = render(
      <AudioStage title="A" trackKey="a" artworkURL="/a.jpg" credits={[]} />,
    );
    load(container.querySelector(".bf-audio-artwork img")!);
    rerender(<AudioStage title="B" trackKey="b" artworkURL="" credits={[]} />);
    const placeholder = () =>
      container.querySelector<HTMLElement>(".bf-audio-artwork .bf-audio-art-placeholder");
    expect(art(container)).toEqual([["/a.jpg", "under"]]);
    expect(placeholder()?.dataset.state).toBe("pending");
    act(() => vi.advanceTimersByTime(40));
    expect(art(container)).toEqual([["/a.jpg", "out"]]);
    expect(placeholder()?.dataset.state).toBe("in");
    act(() => vi.advanceTimersByTime(700));
    expect(art(container)).toEqual([]);
    expect(placeholder()?.dataset.state).toBe("in");
    // The backdrop has no placeholder: it just lets the old picture go.
    expect(container.querySelector(".bf-audio-backdrop .bf-audio-art-placeholder")).toBeNull();
  });

  it("keeps the same picture in place when the next track shares it", () => {
    vi.useFakeTimers();
    const { container, rerender } = render(
      <AudioStage
        title="A"
        trackKey="a"
        artworkURL="/api/media/artwork/m-a/poster?v=h1"
        credits={[]}
      />,
    );
    load(container.querySelector(".bf-audio-artwork img")!);
    rerender(
      <AudioStage
        title="B"
        trackKey="b"
        artworkURL="/api/media/artwork/m-b/poster?v=h1"
        credits={[]}
      />,
    );
    expect(art(container)).toEqual([["/api/media/artwork/m-a/poster?v=h1", "in"]]);
    rerender(
      <AudioStage
        title="C"
        trackKey="c"
        artworkURL="/api/media/artwork/m-c/poster?v=h2"
        credits={[]}
      />,
    );
    expect(art(container)).toEqual([
      ["/api/media/artwork/m-a/poster?v=h1", "under"],
      ["/api/media/artwork/m-c/poster?v=h2", "pending"],
    ]);
  });

  it("lifts the old words away while the new ones come in", () => {
    vi.useFakeTimers();
    const { container, rerender } = render(
      <AudioStage title="Old song" trackKey="a" artworkURL="" credits={[]} />,
    );
    rerender(
      <AudioStage
        title="New song"
        trackKey="b"
        artworkURL=""
        credits={[credit("Ann", "person")]}
      />,
    );
    const layers = [...container.querySelectorAll<HTMLElement>(".bf-audio-meta-layer")];
    expect(
      layers.map((l) => [l.textContent, l.dataset.state, l.getAttribute("aria-hidden")]),
    ).toEqual([
      ["Old song", "out", "true"],
      ["New songAnn", "in", null],
    ]);
    act(() => vi.advanceTimersByTime(320));
    expect(container.querySelectorAll(".bf-audio-meta-layer")).toHaveLength(1);
    expect(screen.getByText("New song")).toBeTruthy();
  });

  it("updates the words in place when the same item's credits arrive", () => {
    const { container, rerender } = render(
      <AudioStage title="Song" trackKey="a" artworkURL="" credits={[]} />,
    );
    rerender(
      <AudioStage title="Song" trackKey="a" artworkURL="" credits={[credit("Ann", "person")]} />,
    );
    expect(container.querySelectorAll(".bf-audio-meta-layer")).toHaveLength(1);
    expect(screen.getByText("Ann")).toBeTruthy();
  });
});

describe("AudioStage track-change styles (CORE-2331, CORE-2332)", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    Reflect.deleteProperty(HTMLImageElement.prototype, "decode");
  });

  const meta = (c: HTMLElement) => c.querySelector<HTMLElement>(".bf-audio-meta")!;
  const frame = (c: HTMLElement) => c.querySelector<HTMLElement>(".bf-audio-artwork")!;

  it("plays the chosen word and artwork styles", () => {
    setTrackChangeMotion({ words: "flap", art: "flip" });
    const { container } = render(<AudioStage title="A" trackKey="a" artworkURL="" credits={[]} />);
    expect(meta(container).dataset.words).toBe("flap");
    expect(frame(container).dataset.art).toBe("flip");
    act(() => setTrackChangeMotion({ words: "wipe", art: "slide" }));
    expect(meta(container).dataset.words).toBe("wipe");
    expect(frame(container).dataset.art).toBe("slide");
  });

  it("draws a rising title letter by letter and announces it once as words", () => {
    setTrackChangeMotion({ words: "rise" });
    const { container, rerender } = render(
      <AudioStage title="Old" trackKey="a" artworkURL="" credits={[]} />,
    );
    rerender(<AudioStage title="Big Sky" trackKey="b" artworkURL="" credits={[]} />);
    expect(meta(container).getAttribute("aria-live")).toBe("polite");
    const [leaving, incoming] = container.querySelectorAll<HTMLElement>(".bf-audio-meta-layer");
    // The leaving words are plain and hidden from assistive technology.
    expect(leaving.getAttribute("aria-hidden")).toBe("true");
    expect(leaving.querySelector(".bf-audio-char")).toBeNull();
    // The incoming title: a visually hidden copy is read, the letters are not.
    expect(incoming.querySelector(".sr-only")?.textContent).toBe("Big Sky");
    const title = incoming.querySelector<HTMLElement>(".bf-audio-title")!;
    expect(title.getAttribute("aria-hidden")).toBe("true");
    expect(title.textContent).toBe("Big Sky");
    const chars = [...title.querySelectorAll<HTMLElement>(".bf-audio-char")];
    expect(chars.map((c) => c.textContent).join("")).toBe("BigSky");
    expect(chars.map((c) => c.style.getPropertyValue("--bf-char"))).toEqual([
      "0",
      "1",
      "2",
      "3",
      "4",
      "5",
    ]);
    expect(title.querySelectorAll(".bf-audio-word")).toHaveLength(2);
  });

  it("orders the lines: title, who made it, what it belongs to, then the rest", () => {
    const { container } = render(
      <AudioStage
        title="Song"
        trackKey="a"
        artworkURL=""
        queuePosition="1 of 2"
        credits={[credit("Ann", "person"), credit("LP", "container")]}
      />,
    );
    const beats = [...container.querySelectorAll<HTMLElement>(".bf-audio-line")].map((l) => [
      l.textContent,
      l.style.getPropertyValue("--bf-line"),
    ]);
    expect(beats).toEqual([
      ["1 of 2", "3"],
      ["Song", "0"],
      ["Ann", "1"],
      ["LP", "2"],
    ]);
  });

  it("keeps a long title whole, and wipes never split letters", () => {
    setTrackChangeMotion({ words: "rise" });
    const long = "word ".repeat(20).trim();
    const { container, rerender } = render(
      <AudioStage title={long} trackKey="a" artworkURL="" credits={[]} />,
    );
    expect(container.querySelector(".bf-audio-char")).toBeNull();
    expect(container.querySelector(".bf-audio-meta .sr-only")).toBeNull();
    act(() => setTrackChangeMotion({ words: "wipe" }));
    rerender(<AudioStage title="Short" trackKey="b" artworkURL="" credits={[]} />);
    expect(container.querySelector(".bf-audio-char")).toBeNull();
  });

  it("only crossfades when the viewer prefers reduced motion", () => {
    vi.stubGlobal(
      "matchMedia",
      vi.fn((query: string) => ({
        matches: query.includes("prefers-reduced-motion"),
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    );
    setTrackChangeMotion({ words: "rise", art: "flip" });
    const { container } = render(
      <AudioStage title="Song" trackKey="a" artworkURL="/a.jpg" credits={[]} />,
    );
    expect(meta(container).dataset.words).toBe("fade");
    expect(frame(container).dataset.art).toBe("fade");
    expect(container.querySelector(".bf-audio-char")).toBeNull();
  });

  it("swaps the artwork only after the next picture has decoded", async () => {
    vi.useFakeTimers();
    let decoded: () => void = () => {};
    const decode = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          decoded = resolve;
        }),
    );
    Object.defineProperty(HTMLImageElement.prototype, "decode", {
      configurable: true,
      value: decode,
    });
    const { container } = render(
      <AudioStage title="A" trackKey="a" artworkURL="/a.jpg" credits={[]} />,
    );
    const img = container.querySelector<HTMLImageElement>(".bf-audio-artwork img")!;
    fireEvent.load(img);
    act(() => vi.advanceTimersByTime(40));
    expect(decode).toHaveBeenCalled();
    expect(img.dataset.state).toBe("pending");
    await act(async () => decoded());
    act(() => vi.advanceTimersByTime(40));
    expect(img.dataset.state).toBe("in");
  });

  it("cancels an unfinished change cleanly on rapid skips", () => {
    vi.useFakeTimers();
    const { container, rerender } = render(
      <AudioStage title="A" trackKey="a" artworkURL="/a.jpg" credits={[]} />,
    );
    fireEvent.load(container.querySelector(".bf-audio-artwork img")!);
    act(() => vi.advanceTimersByTime(40));
    for (const key of ["b", "c", "d", "e"]) {
      rerender(<AudioStage title={key} trackKey={key} artworkURL={`/${key}.jpg`} credits={[]} />);
      act(() => vi.advanceTimersByTime(60));
      const layers = [...container.querySelectorAll<HTMLElement>(".bf-audio-meta-layer")];
      expect(layers.filter((l) => l.dataset.state === "out").length).toBeLessThanOrEqual(1);
      expect(layers.at(-1)?.textContent).toBe(key);
      // At most the one picture on screen under the incoming one.
      expect(container.querySelectorAll(".bf-audio-artwork img").length).toBeLessThanOrEqual(2);
    }
    const last = [...container.querySelectorAll(".bf-audio-artwork img")].at(-1)!;
    expect(last.getAttribute("src")).toBe("/e.jpg");
    fireEvent.load(last);
    act(() => vi.advanceTimersByTime(1000));
    expect(container.querySelectorAll(".bf-audio-meta-layer")).toHaveLength(1);
    expect(
      [...container.querySelectorAll<HTMLImageElement>(".bf-audio-artwork img")].map((i) => [
        i.getAttribute("src"),
        i.dataset.state,
      ]),
    ).toEqual([["/e.jpg", "in"]]);
  });
});
